package start

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"runtime"
	"testing"
	"time"

	"github.com/google/uuid"

	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/diff"
	randutil "k8s.io/apimachinery/pkg/util/rand"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/record"
	"k8s.io/klog/v2"

	configv1 "github.com/openshift/api/config/v1"
	clientset "github.com/openshift/client-go/config/clientset/versioned"

	"github.com/openshift/cluster-version-operator/lib/resourcemerge"
	"github.com/openshift/cluster-version-operator/pkg/cvo"
	"github.com/openshift/cluster-version-operator/pkg/payload"
)

func init() {
	klog.InitFlags(flag.CommandLine)
	_ = flag.CommandLine.Lookup("v").Value.Set("5")
	_ = flag.CommandLine.Lookup("alsologtostderr").Value.Set("true")
}

var (
	version_0_0_1 = map[string]interface{}{
		"release-manifests": map[string]interface{}{
			"release-metadata": `
			{
				"kind": "cincinnati-metadata-v0",
				"version": "0.0.1"
			}
			`,
			"image-references": `
			{
				"kind": "ImageStream",
				"apiVersion": "image.openshift.io/v1",
				"metadata": {
					"name": "0.0.1",
					"annotations": {
					  "include.release.openshift.io/self-managed-high-availability": "true"
					}
				}
			}
			`,
			// this manifest should not have ReleaseImage replaced because it is part of the user facing image
			"config2.json": `
			{
				"kind": "ConfigMap",
				"apiVersion": "v1",
				"metadata": {
					"name": "config2",
					"namespace": "$(NAMESPACE)",
					"annotations": {
					  "include.release.openshift.io/self-managed-high-availability": "true"
					}
				},
				"data": {
					"version": "0.0.1",
					"releaseImage": "{{.ReleaseImage}}"
				}
			}
			`,
		},
		"manifests": map[string]interface{}{
			// this manifest is part of the innate image and should have ReleaseImage replaced
			"config1.json": `
			{
				"kind": "ConfigMap",
				"apiVersion": "v1",
				"metadata": {
					"name": "config1",
					"namespace": "$(NAMESPACE)",
					"annotations": {
					  "include.release.openshift.io/self-managed-high-availability": "true"
					}
				},
				"data": {
					"version": "0.0.1",
					"releaseImage": "{{.ReleaseImage}}"
				}
			}
			`,
		},
	}
)

func TestIntegrationCVO_initializeAndUpgrade(t *testing.T) {
	ctx := context.Background()
	if os.Getenv("TEST_INTEGRATION") != "1" {
		t.Skipf("Integration tests are disabled unless TEST_INTEGRATION=1")
	}
	t.Parallel()

	// use the same client setup as the start command
	cb, err := newClientBuilder("")
	if err != nil {
		t.Fatal(err)
	}
	cfg := cb.RestConfig()
	kc := cb.KubeClientOrDie("integration-test")
	client := cb.ClientOrDie("integration-test")

	ns := fmt.Sprintf("e2e-cvo-%s", randutil.String(4))

	if _, err := kc.CoreV1().Namespaces().Create(
		ctx,
		&v1.Namespace{
			ObjectMeta: metav1.ObjectMeta{
				Name: ns,
			},
		},
		metav1.CreateOptions{},
	); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := kc.CoreV1().Namespaces().Delete(ctx, ns, metav1.DeleteOptions{}); err != nil {
			t.Logf("failed to delete namespace %s: %v", ns, err)
		}
	}()

	id, _ := uuid.NewRandom()

	if _, err := client.ConfigV1().ClusterVersions().Create(
		ctx,
		&configv1.ClusterVersion{
			ObjectMeta: metav1.ObjectMeta{
				Name: ns,
			},
			Spec: configv1.ClusterVersionSpec{
				Channel:   "fast",
				ClusterID: configv1.ClusterID(id.String()),
			},
		},
		metav1.CreateOptions{},
	); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := client.ConfigV1().ClusterVersions().Delete(ctx, ns, metav1.DeleteOptions{}); err != nil {
			t.Logf("failed to delete cluster version %s: %v", ns, err)
		}
	}()

	dir, err := os.MkdirTemp("", "cvo-test")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	if err := createContent(filepath.Join(dir, "0.0.1"), version_0_0_1, map[string]string{"NAMESPACE": ns}); err != nil {
		t.Fatal(err)
	}
	payloadImage1 := "arbitrary/release:image"
	retriever := &mapPayloadRetriever{map[string]string{
		payloadImage1: filepath.Join(dir, "0.0.1"),
	}}

	options := NewOptions()
	options.Namespace = ns
	options.Name = ns
	options.ListenAddr = ""
	options.NodeName = "test-node"
	options.ReleaseImage = payloadImage1
	options.PayloadOverride = filepath.Join(dir, "0.0.1")
	options.leaderElection = getLeaderElectionConfig(ctx, cfg)
	options.AlwaysEnableCapabilities = []string{string(configv1.ClusterVersionCapabilityIngress)}
	if err := options.ValidateAndComplete(); err != nil {
		t.Fatalf("incorrectly initialized options: %v", err)
	}

	clusterVersionConfigInformerFactory, configInformerFactory := options.prepareConfigInformerFactories(cb)
	featureset, gates, err := options.processInitialFeatureGate(context.Background(), configInformerFactory)
	if err != nil {
		t.Fatal(err)
	}
	controllers, err := options.NewControllerContext(cb, featureset, gates, clusterVersionConfigInformerFactory, configInformerFactory)
	if err != nil {
		t.Fatal(err)
	}

	worker := cvo.NewSyncWorker(retriever, cvo.NewResourceBuilder(cfg, cfg, nil, nil), 5*time.Second, wait.Backoff{Steps: 3}, "", "", record.NewFakeRecorder(100), payload.DefaultClusterProfile, stringsToCapabilities(options.AlwaysEnableCapabilities))
	controllers.CVO.SetSyncWorkerForTesting(worker)

	lock, err := createResourceLock(cb, options.Namespace, options.Name)
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go options.run(ctx, controllers, lock, cb.RestConfig(defaultQPS), cb.RestConfig(highQPS))

	t.Logf("wait until we observe the cluster version become available")
	lastCV, err := waitForUpdateAvailable(ctx, t, client, ns, false, "0.0.1")
	if err != nil {
		t.Logf("latest version:\n%s", printCV(lastCV))
		t.Fatalf("cluster version never became available: %v", err)
	}

	status := worker.Status()

	t.Logf("verify the available cluster version's status matches our expectations")
	t.Logf("Cluster version:\n%s", printCV(lastCV))
	verifyClusterVersionStatus(t, lastCV, configv1.Update{Image: payloadImage1, Version: "0.0.1"}, 1)
	verifyReleasePayload(ctx, t, kc, ns, "0.0.1", payloadImage1)

	t.Logf("wait for the next resync and verify that status didn't change")
	if err := wait.PollUntilContextTimeout(ctx, time.Second, 30*time.Second, false, func(_ context.Context) (bool, error) {
		updated := worker.Status()
		if updated.Completed >= status.Completed {
			return true, nil
		}
		return false, nil
	}); err != nil {
		t.Fatal(err)
	}
	time.Sleep(time.Second)
	cv, err := client.ConfigV1().ClusterVersions().Get(ctx, ns, metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(cv.Status, lastCV.Status) {
		t.Fatalf("unexpected: %s", diff.ObjectReflectDiff(lastCV.Status, cv.Status))
	}
	verifyReleasePayload(ctx, t, kc, ns, "0.0.1", payloadImage1)
}

func TestIntegrationCVO_gracefulStepDown(t *testing.T) {
	ctx := context.Background()
	if os.Getenv("TEST_INTEGRATION") != "1" {
		t.Skipf("Integration tests are disabled unless TEST_INTEGRATION=1")
	}
	t.Parallel()

	// use the same client setup as the start command
	cb, err := newClientBuilder("")
	if err != nil {
		t.Fatal(err)
	}
	cfg := cb.RestConfig()
	kc := cb.KubeClientOrDie("integration-test")
	client := cb.ClientOrDie("integration-test")

	ns := fmt.Sprintf("e2e-cvo-%s", randutil.String(6))

	if _, err := kc.CoreV1().Namespaces().Create(
		ctx,
		&v1.Namespace{
			ObjectMeta: metav1.ObjectMeta{
				Name: ns,
			},
		},
		metav1.CreateOptions{},
	); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := kc.CoreV1().Namespaces().Delete(ctx, ns, metav1.DeleteOptions{}); err != nil {
			t.Logf("failed to delete namespace %s: %v", ns, err)
		}
	}()

	id, _ := uuid.NewRandom()

	if _, err := client.ConfigV1().ClusterVersions().Create(
		ctx,
		&configv1.ClusterVersion{
			ObjectMeta: metav1.ObjectMeta{
				Name: ns,
			},
			Spec: configv1.ClusterVersionSpec{
				Channel:   "fast",
				ClusterID: configv1.ClusterID(id.String()),
			},
		},
		metav1.CreateOptions{},
	); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := client.ConfigV1().ClusterVersions().Delete(ctx, ns, metav1.DeleteOptions{}); err != nil {
			t.Logf("failed to delete cluster version %s: %v", ns, err)
		}
	}()

	dir, err := os.MkdirTemp("", "cvo-test")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	if err := createContent(filepath.Join(dir, "0.0.1"), version_0_0_1, map[string]string{"NAMESPACE": ns}); err != nil {
		t.Fatal(err)
	}
	payloadImage1 := "arbitrary/release:image"
	retriever := &mapPayloadRetriever{map[string]string{
		payloadImage1: filepath.Join(dir, "0.0.1"),
	}}

	options := NewOptions()
	options.Namespace = ns
	options.Name = ns
	options.ListenAddr = ""
	options.NodeName = "test-node"
	options.ReleaseImage = payloadImage1
	options.PayloadOverride = filepath.Join(dir, "0.0.1")
	options.leaderElection = getLeaderElectionConfig(ctx, cfg)
	options.AlwaysEnableCapabilities = []string{string(configv1.ClusterVersionCapabilityIngress)}
	if err := options.ValidateAndComplete(); err != nil {
		t.Fatalf("incorrectly initialized options: %v", err)
	}

	clusterVersionConfigInformerFactory, configInformerFactory := options.prepareConfigInformerFactories(cb)
	featureset, gates, err := options.processInitialFeatureGate(context.Background(), configInformerFactory)
	if err != nil {
		t.Fatal(err)
	}
	controllers, err := options.NewControllerContext(cb, featureset, gates, clusterVersionConfigInformerFactory, configInformerFactory)
	if err != nil {
		t.Fatal(err)
	}

	worker := cvo.NewSyncWorker(retriever, cvo.NewResourceBuilder(cfg, cfg, nil, nil), 5*time.Second, wait.Backoff{Steps: 3}, "", "", record.NewFakeRecorder(100), payload.DefaultClusterProfile, stringsToCapabilities(options.AlwaysEnableCapabilities))
	controllers.CVO.SetSyncWorkerForTesting(worker)

	lock, err := createResourceLock(cb, options.Namespace, options.Name)
	if err != nil {
		t.Fatal(err)
	}

	t.Logf("the controller should create a lock record on a config map")
	runContext, cancel := context.WithCancel(ctx)
	defer cancel()
	done := make(chan struct{})
	go func() {
		options.run(runContext, controllers, lock, cb.RestConfig(defaultQPS), cb.RestConfig(highQPS))
		close(done)
	}()

	// wait until the lock record exists
	err = wait.PollUntilContextTimeout(ctx, 200*time.Millisecond, 60*time.Second, true, func(localCtx context.Context) (bool, error) {
		_, _, err := lock.Get(localCtx)
		if err != nil {
			if errors.IsNotFound(err) {
				return false, nil
			}
			return false, err
		}
		return true, nil
	})
	if err != nil {
		t.Fatal(err)
	}

	t.Logf("verify the controller writes a leadership change event")
	events, err := kc.CoreV1().Events(ns).List(ctx, metav1.ListOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if !hasLeaderEvent(events.Items, ns) {
		t.Fatalf("no leader election events found in\n%#v", events.Items)
	}

	t.Logf("after the context is closed, the lock should be released quickly")
	cancel()
	startTime := time.Now()
	var endTime time.Time
	// the lock should be deleted immediately
	err = wait.PollUntilContextTimeout(ctx, 100*time.Millisecond, 10*time.Second, true, func(localCtx context.Context) (bool, error) {
		electionRecord, _, err := lock.Get(localCtx)
		if err != nil {
			if errors.IsNotFound(err) {
				return false, nil
			}
			return false, err
		}
		endTime = time.Now()
		return electionRecord.HolderIdentity == "", nil
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("lock released in %s", endTime.Sub(startTime))

	select {
	case <-time.After(time.Second):
		t.Fatalf("controller should exit more quickly")
	case <-done:
	}
}

func TestIntegrationCVO_cincinnatiRequest(t *testing.T) {
	ctx := context.Background()
	if os.Getenv("TEST_INTEGRATION") != "1" {
		t.Skipf("Integration tests are disabled unless TEST_INTEGRATION=1")
	}
	t.Parallel()

	requestQuery := make(chan string, 100)
	defer close(requestQuery)

	handler := func(w http.ResponseWriter, r *http.Request) {
		select {
		case requestQuery <- r.URL.RawQuery:
		default:
			t.Errorf("received too many requests at upstream URL")
		}
	}
	upstreamServer := httptest.NewServer(http.HandlerFunc(handler))
	defer upstreamServer.Close()

	// use the same client setup as the start command
	cb, err := newClientBuilder("")
	if err != nil {
		t.Fatal(err)
	}
	cfg := cb.RestConfig()
	kc := cb.KubeClientOrDie("integration-test")
	client := cb.ClientOrDie("integration-test")

	ns := fmt.Sprintf("e2e-cvo-%s", randutil.String(4))

	if _, err := kc.CoreV1().Namespaces().Create(
		ctx,
		&v1.Namespace{
			ObjectMeta: metav1.ObjectMeta{
				Name: ns,
			},
		},
		metav1.CreateOptions{},
	); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := kc.CoreV1().Namespaces().Delete(ctx, ns, metav1.DeleteOptions{}); err != nil {
			t.Logf("failed to delete namespace %s: %v", ns, err)
		}
	}()

	id, _ := uuid.NewRandom()

	if _, err := client.ConfigV1().ClusterVersions().Create(
		ctx,
		&configv1.ClusterVersion{
			ObjectMeta: metav1.ObjectMeta{
				Name: ns,
			},
			Spec: configv1.ClusterVersionSpec{
				Upstream:  configv1.URL(upstreamServer.URL),
				Channel:   "test-channel",
				ClusterID: configv1.ClusterID(id.String()),
			},
		},
		metav1.CreateOptions{},
	); err != nil {
		t.Fatal(err)
	}

	defer func() {
		if err := client.ConfigV1().ClusterVersions().Delete(ctx, ns, metav1.DeleteOptions{}); err != nil {
			t.Logf("failed to delete cluster version %s: %v", ns, err)
		}
	}()

	dir, err := os.MkdirTemp("", "cvo-test")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	if err := createContent(filepath.Join(dir, "0.0.1"), version_0_0_1, map[string]string{"NAMESPACE": ns}); err != nil {
		t.Fatal(err)
	}
	payloadImage1 := "arbitrary/release:image"
	retriever := &mapPayloadRetriever{map[string]string{
		payloadImage1: filepath.Join(dir, "0.0.1"),
	}}
	payloadDir := filepath.Join(dir, "payload")
	if err := os.Mkdir(payloadDir, 0777); err != nil {
		t.Fatal(err)
	}
	manifestsDir := filepath.Join(payloadDir, "manifests")
	if err := os.Mkdir(manifestsDir, 0777); err != nil {
		t.Fatal(err)
	}
	releaseManifestsDir := filepath.Join(payloadDir, "release-manifests")
	if err := os.Mkdir(releaseManifestsDir, 0777); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(releaseManifestsDir, "release-metadata"), []byte(`{
  "kind": "cincinnati-metadata-v0",
  "version": "0.0.1"
}`), 0777); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(releaseManifestsDir, "image-references"), []byte(`kind: ImageStream
apiVersion: image.openshift.io/v1
metadata:
  name: 0.0.1
`), 0777); err != nil {
		t.Fatal(err)
	}

	options := NewOptions()
	options.Namespace = ns
	options.Name = ns
	options.ListenAddr = ""
	options.NodeName = "test-node"
	options.ReleaseImage = payloadImage1
	options.PayloadOverride = payloadDir
	options.leaderElection = getLeaderElectionConfig(ctx, cfg)
	options.AlwaysEnableCapabilities = []string{string(configv1.ClusterVersionCapabilityIngress)}
	if err := options.ValidateAndComplete(); err != nil {
		t.Fatalf("incorrectly initialized options: %v", err)
	}

	clusterVersionConfigInformerFactory, configInformerFactory := options.prepareConfigInformerFactories(cb)
	featureset, gates, err := options.processInitialFeatureGate(context.Background(), configInformerFactory)
	if err != nil {
		t.Fatal(err)
	}
	controllers, err := options.NewControllerContext(cb, featureset, gates, clusterVersionConfigInformerFactory, configInformerFactory)
	if err != nil {
		t.Fatal(err)
	}

	worker := cvo.NewSyncWorker(retriever, cvo.NewResourceBuilder(cfg, cfg, nil, nil), 5*time.Second, wait.Backoff{Steps: 3}, "", "", record.NewFakeRecorder(100), payload.DefaultClusterProfile, stringsToCapabilities(options.AlwaysEnableCapabilities))
	controllers.CVO.SetSyncWorkerForTesting(worker)

	lock, err := createResourceLock(cb, options.Namespace, options.Name)
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go options.run(ctx, controllers, lock, cb.RestConfig(defaultQPS), cb.RestConfig(highQPS))

	t.Logf("wait until we observe the cluster version become available")
	lastCV, err := waitForUpdateAvailable(ctx, t, client, ns, false, "0.0.1")
	if err != nil {
		t.Logf("latest version:\n%s", printCV(lastCV))
		t.Fatalf("cluster version never became available: %v", err)
	}

	t.Logf("wait until we observe the request to the upstream url")
	actualQuery := ""
	select {
	case actualQuery = <-requestQuery:
	case <-time.After(10 * time.Second):
		t.Logf("latest version:\n%s", printCV(lastCV))
		t.Fatal("no request received at upstream URL")
	}
	expectedQuery := fmt.Sprintf("arch=%s&channel=test-channel&id=%s&version=0.0.1", runtime.GOARCH, id.String())
	expectedQueryValues, err := url.ParseQuery(expectedQuery)
	if err != nil {
		t.Fatalf("could not parse expected query: %v", err)
	}
	actualQueryValues, err := url.ParseQuery(actualQuery)
	if err != nil {
		t.Fatalf("could not parse acutal query: %v", err)
	}
	if e, a := expectedQueryValues, actualQueryValues; !reflect.DeepEqual(e, a) {
		t.Errorf("expected query to be %q, got: %q", e, a)
	}
}

// waitForUpdateAvailable checks invariants during an upgrade process. versions is a list of the expected versions that
// should be seen during update, with the last version being the one we wait to see.
func waitForUpdateAvailable(ctx context.Context, t *testing.T, client clientset.Interface, ns string, allowIncrementalFailure bool, versions ...string) (*configv1.ClusterVersion, error) {
	var lastCV *configv1.ClusterVersion
	return lastCV, wait.PollUntilContextTimeout(ctx, 1*time.Second, 1*time.Minute, true, func(localCtx context.Context) (bool, error) {
		cv, err := client.ConfigV1().ClusterVersions().Get(localCtx, ns, metav1.GetOptions{})
		if errors.IsNotFound(err) {
			return false, nil
		}
		if err != nil {
			return false, err
		}
		lastCV = cv

		if cv.Status.ObservedGeneration > cv.Generation {
			return false, fmt.Errorf("status generation should never be newer than metadata generation")
		}

		verifyClusterVersionHistory(t, cv)

		if !allowIncrementalFailure {
			if failing := resourcemerge.FindOperatorStatusCondition(cv.Status.Conditions, cvo.ClusterStatusFailing); failing != nil && failing.Status == configv1.ConditionTrue {
				return false, fmt.Errorf("operator listed as failing (%s): %s", failing.Reason, failing.Message)
			}
		}

		// just wait until the operator is available
		if len(versions) == 0 {
			available := resourcemerge.FindOperatorStatusCondition(cv.Status.Conditions, configv1.OperatorAvailable)
			return available != nil && available.Status == configv1.ConditionTrue, nil
		}

		if len(versions) == 1 {
			// we should not observe status.generation == metadata.generation without also observing a status history entry - if
			// a version is set, it must match our desired version (we can occasionally observe a "working towards" event where only
			// the image value is set, not the version)
			if cv.Status.ObservedGeneration == cv.Generation {
				if len(cv.Status.History) == 0 || (cv.Status.History[0].Version != "" && cv.Status.History[0].Version != versions[0]) {
					return false, fmt.Errorf("initializing operator should set history and generation at the same time")
				}
			}

			if available := resourcemerge.FindOperatorStatusCondition(cv.Status.Conditions, configv1.OperatorAvailable); available == nil || available.Status == configv1.ConditionFalse {
				if progressing := resourcemerge.FindOperatorStatusCondition(cv.Status.Conditions, configv1.OperatorProgressing); available != nil && (progressing == nil || progressing.Status != configv1.ConditionTrue) {
					return false, fmt.Errorf("initializing operator should have progressing if available is false: %#v", progressing)
				}
				return false, nil
			}
			if len(cv.Status.History) == 0 {
				return false, fmt.Errorf("initializing operator should have history after available goes true")
			}
			if cv.Status.History[0].Version != versions[len(versions)-1] {
				return false, fmt.Errorf("initializing operator should report the target version in history once available")
			}
			if cv.Status.History[0].State != configv1.CompletedUpdate {
				return false, fmt.Errorf("initializing operator should report history completed %#v", cv.Status.History[0])
			}
			if progressing := resourcemerge.FindOperatorStatusCondition(cv.Status.Conditions, configv1.OperatorProgressing); progressing == nil || progressing.Status == configv1.ConditionTrue {
				return false, fmt.Errorf("initializing operator should never be available and still progressing or lacking the condition: %#v", progressing)
			}
			return true, nil
		}

		// we should not observe status.generation == metadata.generation without also observing a status history entry
		if cv.Status.ObservedGeneration == cv.Generation {
			target := versions[len(versions)-1]
			hasVersion := cv.Status.Desired.Version != ""
			if hasVersion && cv.Status.Desired.Version != target {
				return false, fmt.Errorf("upgrading operator should always have desired version when spec version is set")
			}
			if len(cv.Status.History) == 0 || (hasVersion && cv.Status.History[0].Version != target) {
				return false, fmt.Errorf("upgrading operator should set history and generation at the same time")
			}
		}

		if available := resourcemerge.FindOperatorStatusCondition(cv.Status.Conditions, configv1.OperatorAvailable); available == nil || available.Status == configv1.ConditionFalse {
			return false, fmt.Errorf("upgrading operator should remain available: %#v", available)
		}
		if !stringInSlice(versions, cv.Status.Desired.Version) {
			return false, fmt.Errorf("upgrading operator status reported desired version %s which is not in the allowed set %v", cv.Status.Desired.Version, versions)
		}
		if len(cv.Status.History) == 0 {
			return false, fmt.Errorf("upgrading operator should have at least once history entry")
		}
		if !stringInSlice(versions, cv.Status.History[0].Version) {
			return false, fmt.Errorf("upgrading operator should have a valid history[0] version %s: %v", cv.Status.Desired.Version, versions)
		}

		if cv.Status.History[0].Version != versions[len(versions)-1] {
			return false, nil
		}

		if failing := resourcemerge.FindOperatorStatusCondition(cv.Status.Conditions, cvo.ClusterStatusFailing); failing != nil && failing.Status == configv1.ConditionTrue {
			return false, fmt.Errorf("operator listed as failing (%s): %s", failing.Reason, failing.Message)
		}

		progressing := resourcemerge.FindOperatorStatusCondition(cv.Status.Conditions, configv1.OperatorProgressing)
		if cv.Status.History[0].State != configv1.CompletedUpdate {
			if progressing == nil || progressing.Status != configv1.ConditionTrue {
				return false, fmt.Errorf("upgrading operator should have progressing true: %#v", progressing)
			}
			return false, nil
		}

		if progressing == nil || progressing.Status != configv1.ConditionFalse {
			return false, fmt.Errorf("upgraded operator should have progressing condition false: %#v", progressing)
		}
		return true, nil
	})
}

func stringInSlice(slice []string, s string) bool {
	for _, item := range slice {
		if s == item {
			return true
		}
	}
	return false
}

func verifyClusterVersionHistory(t *testing.T, cv *configv1.ClusterVersion) {
	t.Helper()
	var previous *configv1.UpdateHistory
	for i, history := range cv.Status.History {
		if history.StartedTime.IsZero() {
			t.Fatalf("Invalid history, entry %d had no start time: %#v", i, history)
		}
		if len(history.Image) == 0 && len(history.Version) == 0 {
			t.Fatalf("Invalid history, entry %d had no image or version: %#v", i, history)
		}
		if i == 0 {
			continue
		}
		previous = &cv.Status.History[i-1]
		if history.CompletionTime == nil || history.CompletionTime.IsZero() {
			t.Fatalf("Invalid history, entry %d had no completion time: %#v", i, history)
		}
		if history.Image == previous.Image && history.Version == previous.Version {
			t.Fatalf("Invalid history, entry %d and %d have identical updates, should be one entry: %s", i-1, i, diff.ObjectReflectDiff(previous, &history))
		}
	}
}

func verifyClusterVersionStatus(t *testing.T, cv *configv1.ClusterVersion, expectedUpdate configv1.Update, expectHistory int) {
	t.Helper()
	if cv.Status.ObservedGeneration != cv.Generation {
		t.Fatalf("unexpected: %d instead of %d", cv.Status.ObservedGeneration, cv.Generation)
	}
	if cv.Status.Desired.Image != expectedUpdate.Image || cv.Status.Desired.Version != expectedUpdate.Version {
		t.Fatalf("unexpected: %#v", cv.Status.Desired)
	}
	if len(cv.Status.History) != expectHistory {
		t.Fatalf("unexpected: %#v", cv.Status.History)
	}
	actual := cv.Status.History[0]
	if actual.StartedTime.Time.IsZero() || actual.CompletionTime == nil || actual.CompletionTime.Time.IsZero() || actual.CompletionTime.Time.Before(actual.StartedTime.Time) {
		t.Fatalf("unexpected: %s -> %s", actual.StartedTime, actual.CompletionTime)
	}
	expect := configv1.UpdateHistory{
		State:          configv1.CompletedUpdate,
		Version:        expectedUpdate.Version,
		Image:          expectedUpdate.Image,
		StartedTime:    actual.StartedTime,
		CompletionTime: actual.CompletionTime,
	}
	if !reflect.DeepEqual(expect, actual) {
		t.Fatalf("unexpected history: %s", diff.ObjectReflectDiff(expect, actual))
	}
	if len(cv.Status.VersionHash) == 0 {
		t.Fatalf("unexpected version hash: %#v", cv.Status.VersionHash)
	}
	if cv.Status.ObservedGeneration != cv.Generation {
		t.Fatalf("unexpected generation: %#v", cv.Status.ObservedGeneration)
	}
}

func verifyReleasePayload(ctx context.Context, t *testing.T, kc kubernetes.Interface, ns, version, image string) {
	t.Helper()
	verifyReleasePayloadConfigMap1(ctx, t, kc, ns, version, image)
	verifyReleasePayloadConfigMap2(ctx, t, kc, ns, version, image)
}

func verifyReleasePayloadConfigMap1(ctx context.Context, t *testing.T, kc kubernetes.Interface, ns, version, image string) {
	t.Helper()
	cm, err := kc.CoreV1().ConfigMaps(ns).Get(ctx, "config1", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("unable to find cm/config1 in ns %s: %v", ns, err)
	}
	if cm.Data["version"] != version || cm.Data["releaseImage"] != image {
		t.Fatalf("unexpected cm/config1 contents: %#v", cm.Data)
	}
}

func verifyReleasePayloadConfigMap2(ctx context.Context, t *testing.T, kc kubernetes.Interface, ns, version, image string) {
	t.Helper()
	cm, err := kc.CoreV1().ConfigMaps(ns).Get(ctx, "config2", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("unable to find cm/config2 in ns %s: %v", ns, err)
	}
	if cm.Data["version"] != version || cm.Data["releaseImage"] != "{{.ReleaseImage}}" {
		t.Fatalf("unexpected cm/config2 contents: %#v", cm.Data)
	}
}

func hasLeaderEvent(events []v1.Event, name string) bool {
	for _, event := range events {
		if event.Reason == "LeaderElection" && event.InvolvedObject.Name == name {
			return true
		}
	}
	return false
}

func printCV(cv *configv1.ClusterVersion) string {
	data, err := json.MarshalIndent(cv, "", "  ")
	if err != nil {
		return fmt.Sprintf("<error: %v>", err)
	}
	return string(data)
}

var reVariable = regexp.MustCompile(`\$\([a-zA-Z0-9_\-]+\)`)

func createContent(baseDir string, content map[string]interface{}, replacements ...map[string]string) error {
	if err := os.MkdirAll(baseDir, 0750); err != nil {
		if !os.IsExist(err) {
			return err
		}
	}
	for k, v := range content {
		switch t := v.(type) {
		case string:
			if len(replacements) > 0 {
				t = reVariable.ReplaceAllStringFunc(t, func(key string) string {
					key = key[2 : len(key)-1]
					for _, r := range replacements {
						v, ok := r[key]
						if !ok {
							continue
						}
						return v
					}
					return key
				})
			}
			if err := os.WriteFile(filepath.Join(baseDir, k), []byte(t), 0640); err != nil {
				return err
			}
		case map[string]interface{}:
			dir := filepath.Join(baseDir, k)
			if err := os.Mkdir(dir, 0750); err != nil {
				if !os.IsExist(err) {
					return err
				}
			}
			if err := createContent(dir, t, replacements...); err != nil {
				return err
			}
		}
	}
	return nil
}

type mapPayloadRetriever struct {
	Paths map[string]string
}

func (r *mapPayloadRetriever) RetrievePayload(ctx context.Context, update configv1.Update) (cvo.PayloadInfo, error) {
	path, ok := r.Paths[update.Image]
	if !ok {
		return cvo.PayloadInfo{}, fmt.Errorf("no image found for %q", update.Image)
	}
	return cvo.PayloadInfo{
		Directory: path,
	}, nil
}

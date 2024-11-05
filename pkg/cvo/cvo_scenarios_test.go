package cvo

import (
	"context"
	"fmt"
	"reflect"
	"runtime"
	"sort"
	"strconv"
	"testing"
	"time"

	"github.com/davecgh/go-spew/spew"
	"github.com/google/uuid"

	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	apiruntime "k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/wait"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	clientgotesting "k8s.io/client-go/testing"
	"k8s.io/client-go/tools/record"
	"k8s.io/client-go/util/workqueue"

	configv1 "github.com/openshift/api/config/v1"
	"github.com/openshift/client-go/config/clientset/versioned/fake"
	"github.com/openshift/library-go/pkg/manifest"

	"github.com/openshift/cluster-version-operator/pkg/featuregates"
	"github.com/openshift/cluster-version-operator/pkg/payload"
	"github.com/openshift/cluster-version-operator/pkg/payload/precondition"
)

var architecture string
var sortedCaps = configv1.ClusterVersionCapabilitySets[configv1.ClusterVersionCapabilitySetCurrent]
var sortedKnownCaps = configv1.KnownClusterVersionCapabilities

func init() {
	architecture = runtime.GOARCH

	sort.Slice(sortedCaps, func(i, j int) bool {
		return sortedCaps[i] < sortedCaps[j]
	})
	sort.Slice(sortedKnownCaps, func(i, j int) bool {
		return sortedKnownCaps[i] < sortedKnownCaps[j]
	})
}

func setupCVOTest(payloadDir string) (*Operator, map[string]apiruntime.Object, *fake.Clientset, *dynamicfake.FakeDynamicClient, func()) {
	client := &fake.Clientset{}
	client.AddReactor("*", "*", func(action clientgotesting.Action) (handled bool, ret apiruntime.Object, err error) {
		return false, nil, fmt.Errorf("unexpected client action: %#v", action)
	})
	cvs := make(map[string]apiruntime.Object)
	client.AddReactor("*", "clusterversions", func(action clientgotesting.Action) (handled bool, ret apiruntime.Object, err error) {
		switch a := action.(type) {
		case clientgotesting.GetActionImpl:
			obj, ok := cvs[a.GetName()]
			if !ok {
				return true, nil, errors.NewNotFound(schema.GroupResource{Resource: "clusterversions"}, a.GetName())
			}
			return true, obj.DeepCopyObject(), nil
		case clientgotesting.CreateActionImpl:
			obj := a.GetObject().DeepCopyObject().(*configv1.ClusterVersion)
			obj.Generation = 1
			cvs[obj.Name] = obj
			return true, obj, nil
		case clientgotesting.UpdateActionImpl:
			obj := a.GetObject().DeepCopyObject().(*configv1.ClusterVersion)
			existing := cvs[obj.Name].DeepCopyObject().(*configv1.ClusterVersion)
			rv, _ := strconv.Atoi(existing.ResourceVersion)
			nextRV := strconv.Itoa(rv + 1)
			if a.GetSubresource() == "status" {
				existing.Status = obj.Status
			} else {
				existing.Spec = obj.Spec
				existing.ObjectMeta = obj.ObjectMeta
				if existing.Generation > obj.Generation {
					existing.Generation = existing.Generation + 1
				} else {
					existing.Generation = obj.Generation + 1
				}
			}
			existing.ResourceVersion = nextRV
			cvs[existing.Name] = existing
			return true, existing, nil
		}
		return false, nil, fmt.Errorf("unexpected client action: %#v", action)
	})
	client.AddReactor("get", "featuregates", func(action clientgotesting.Action) (handled bool, ret apiruntime.Object, err error) {
		switch a := action.(type) {
		case clientgotesting.GetAction:
			return true, nil, errors.NewNotFound(schema.GroupResource{Resource: "clusterversions"}, a.GetName())
		}
		return false, nil, nil
	})

	_, err := client.ConfigV1().ClusterVersions().Create(context.Background(),
		&configv1.ClusterVersion{
			ObjectMeta: metav1.ObjectMeta{
				Name: "version",
			},
			Spec: configv1.ClusterVersionSpec{
				Channel: "fast",
			},
		},
		metav1.CreateOptions{})

	if err != nil {
		fmt.Printf("Cannot create cluster version object, err: %#v\n", err)
	}

	o := &Operator{
		namespace:           "test",
		name:                "version",
		queue:               workqueue.NewNamedRateLimitingQueue(workqueue.DefaultControllerRateLimiter(), "cvo-loop-test"),
		client:              client,
		enabledFeatureGates: featuregates.DefaultCvoGates("version"),
		cvLister:            &clientCVLister{client: client},
		exclude:             "exclude-test",
		eventRecorder:       record.NewFakeRecorder(100),
		clusterProfile:      payload.DefaultClusterProfile,
	}

	dynamicScheme := apiruntime.NewScheme()
	dynamicScheme.AddKnownTypeWithName(schema.GroupVersionKind{Group: "test.cvo.io", Version: "v1", Kind: "TestB"}, &unstructured.Unstructured{})
	dynamicClient := dynamicfake.NewSimpleDynamicClient(dynamicScheme)

	worker := NewSyncWorker(
		&fakeDirectoryRetriever{Info: PayloadInfo{Directory: payloadDir}},
		&testResourceBuilder{client: dynamicClient},
		time.Second/2,
		wait.Backoff{
			Steps: 1,
		},
		"exclude-test",
		"",
		record.NewFakeRecorder(100),
		o.clusterProfile,
		[]configv1.ClusterVersionCapability{configv1.ClusterVersionCapabilityIngress},
	)
	o.configSync = worker

	return o, cvs, client, dynamicClient, func() { o.queue.ShutDown() }
}

func TestCVO_StartupAndSync(t *testing.T) {
	o, cvs, client, _, shutdownFn := setupCVOTest("testdata/payloadtest")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	defer shutdownFn()
	worker := o.configSync.(*SyncWorker)
	go worker.Start(ctx, 1)

	// Step 1: Ensure the CVO reports a status error if it has nothing to sync
	//
	client.ClearActions()
	err := o.sync(ctx, o.queueKey())
	if err != nil {
		t.Fatal(err)
	}
	actions := client.Actions()
	if len(actions) != 2 {
		t.Fatalf("%s", spew.Sdump(actions))
	}
	// read from lister
	expectGet(t, actions[0], "clusterversions", "", "version")
	actual := cvs["version"].(*configv1.ClusterVersion)
	expectUpdateStatus(t, actions[1], "clusterversions", "", &configv1.ClusterVersion{
		ObjectMeta: metav1.ObjectMeta{
			Name:       "version",
			Generation: 1,
		},
		Spec: configv1.ClusterVersionSpec{
			ClusterID: actual.Spec.ClusterID,
			Channel:   "fast",
		},
		Status: configv1.ClusterVersionStatus{
			History: []configv1.UpdateHistory{
				// empty because the operator release image is not set, so we have no input
			},
			Conditions: []configv1.ClusterOperatorStatusCondition{
				{Type: ImplicitlyEnabledCapabilities, Status: "False", Reason: "AsExpected", Message: "Capabilities match configured spec"},
				{Type: configv1.OperatorAvailable, Status: configv1.ConditionFalse},
				// report back to the user that we don't have enough info to proceed
				{Type: ClusterStatusFailing, Status: configv1.ConditionTrue, Reason: "NoDesiredImage", Message: "No configured operator version, unable to update cluster"},
				{Type: configv1.OperatorProgressing, Status: configv1.ConditionTrue, Reason: "NoDesiredImage", Message: "Unable to apply <unknown>: an unknown error has occurred: NoDesiredImage"},
				{Type: configv1.RetrievedUpdates, Status: configv1.ConditionFalse},
			},
		},
	})
	verifyAllStatus(t, worker.StatusCh())

	// Step 3: Given an operator image, begin synchronizing
	//
	o.release.Image = "image/image:1"
	o.release.Version = "1.0.0-abc"
	desired := configv1.Release{Version: "1.0.0-abc", Image: "image/image:1"}
	//
	client.ClearActions()
	err = o.sync(ctx, o.queueKey())
	if err != nil {
		t.Fatal(err)
	}
	actions = client.Actions()
	if len(actions) != 2 {
		t.Fatalf("%s", spew.Sdump(actions))
	}
	expectGet(t, actions[0], "clusterversions", "", "version")
	actual = cvs["version"].(*configv1.ClusterVersion)
	expectUpdateStatus(t, actions[1], "clusterversions", "", &configv1.ClusterVersion{
		ObjectMeta: metav1.ObjectMeta{
			Name:            "version",
			Generation:      1,
			ResourceVersion: "1",
		},
		Spec: configv1.ClusterVersionSpec{
			ClusterID: actual.Spec.ClusterID,
			Channel:   "fast",
		},
		Status: configv1.ClusterVersionStatus{
			ObservedGeneration: 1,
			Desired:            desired,
			History: []configv1.UpdateHistory{
				{State: configv1.PartialUpdate, Image: "image/image:1", Version: "1.0.0-abc", StartedTime: defaultStartedTime},
			},
			Capabilities: configv1.ClusterVersionCapabilitiesStatus{
				EnabledCapabilities: sortedCaps,
				KnownCapabilities:   sortedKnownCaps,
			},
			Conditions: []configv1.ClusterOperatorStatusCondition{
				{Type: ImplicitlyEnabledCapabilities, Status: "False", Reason: "AsExpected", Message: "Capabilities match configured spec"},
				{Type: configv1.OperatorAvailable, Status: configv1.ConditionFalse},
				// cleared failing status and set progressing
				{Type: ClusterStatusFailing, Status: configv1.ConditionFalse},
				{Type: configv1.OperatorProgressing, Status: configv1.ConditionTrue, Message: "Working towards 1.0.0-abc"},
				{Type: configv1.RetrievedUpdates, Status: configv1.ConditionFalse},
				{Type: DesiredReleaseAccepted, Status: configv1.ConditionTrue, Reason: "PayloadLoaded",
					Message: "Payload loaded version=\"1.0.0-abc\" image=\"image/image:1\" architecture=\"" + architecture + "\""},
			},
		},
	})
	verifyAllStatus(t, worker.StatusCh(),
		SyncWorkerStatus{
			Generation: 1,
			Actual:     configv1.Release{Version: "1.0.0-abc", Image: "image/image:1"},
			loadPayloadStatus: LoadPayloadStatus{
				Step:               "RetrievePayload",
				Message:            "Retrieving and verifying payload version=\"1.0.0-abc\" image=\"image/image:1\"",
				LastTransitionTime: time.Unix(1, 0),
				Update:             configv1.Update{Version: "1.0.0-abc", Image: "image/image:1"},
			},
		},
		SyncWorkerStatus{
			Generation:   1,
			Actual:       configv1.Release{Version: "1.0.0-abc", Image: "image/image:1"},
			LastProgress: time.Unix(1, 0),
			loadPayloadStatus: LoadPayloadStatus{
				Step:               "PayloadLoaded",
				Message:            "Payload loaded version=\"1.0.0-abc\" image=\"image/image:1\" architecture=\"" + architecture + "\"",
				LastTransitionTime: time.Unix(2, 0),
				Update:             configv1.Update{Version: "1.0.0-abc", Image: "image/image:1"},
			},
		},
		SyncWorkerStatus{
			Generation:  1,
			Total:       3,
			Initial:     true,
			VersionHash: "DL-FFQ2Uem8=", Architecture: architecture,
			Actual: configv1.Release{
				Version:  "1.0.0-abc",
				Image:    "image/image:1",
				URL:      "https://example.com/v1.0.0-abc",
				Channels: []string{"channel-a", "channel-b", "channel-c"},
			},
			LastProgress: time.Unix(2, 0),
			loadPayloadStatus: LoadPayloadStatus{
				Step:               "PayloadLoaded",
				Message:            "Payload loaded version=\"1.0.0-abc\" image=\"image/image:1\" architecture=\"" + architecture + "\"",
				LastTransitionTime: time.Unix(3, 0),
				Update:             configv1.Update{Version: "1.0.0-abc", Image: "image/image:1"},
			},
			CapabilitiesStatus: CapabilityStatus{
				Status: configv1.ClusterVersionCapabilitiesStatus{
					EnabledCapabilities: sortedCaps,
					KnownCapabilities:   sortedKnownCaps,
				},
			},
		},
		SyncWorkerStatus{
			Generation:  1,
			Done:        1,
			Total:       3,
			Initial:     true,
			VersionHash: "DL-FFQ2Uem8=", Architecture: architecture,
			Actual: configv1.Release{
				Version:  "1.0.0-abc",
				Image:    "image/image:1",
				URL:      "https://example.com/v1.0.0-abc",
				Channels: []string{"channel-a", "channel-b", "channel-c"},
			},
			LastProgress: time.Unix(3, 0),
			loadPayloadStatus: LoadPayloadStatus{
				Step:               "PayloadLoaded",
				Message:            "Payload loaded version=\"1.0.0-abc\" image=\"image/image:1\" architecture=\"" + architecture + "\"",
				LastTransitionTime: time.Unix(4, 0),
				Update:             configv1.Update{Version: "1.0.0-abc", Image: "image/image:1"},
			},
			CapabilitiesStatus: CapabilityStatus{
				Status: configv1.ClusterVersionCapabilitiesStatus{
					KnownCapabilities:   sortedKnownCaps,
					EnabledCapabilities: sortedCaps,
				},
			},
		},
		SyncWorkerStatus{
			Generation:  1,
			Done:        2,
			Total:       3,
			Initial:     true,
			VersionHash: "DL-FFQ2Uem8=", Architecture: architecture,
			Actual: configv1.Release{
				Version:  "1.0.0-abc",
				Image:    "image/image:1",
				URL:      "https://example.com/v1.0.0-abc",
				Channels: []string{"channel-a", "channel-b", "channel-c"},
			},
			LastProgress: time.Unix(4, 0),
			loadPayloadStatus: LoadPayloadStatus{
				Step:               "PayloadLoaded",
				Message:            "Payload loaded version=\"1.0.0-abc\" image=\"image/image:1\" architecture=\"" + architecture + "\"",
				LastTransitionTime: time.Unix(5, 0),
				Update:             configv1.Update{Version: "1.0.0-abc", Image: "image/image:1"},
			},
			CapabilitiesStatus: CapabilityStatus{
				Status: configv1.ClusterVersionCapabilitiesStatus{
					EnabledCapabilities: sortedCaps,
					KnownCapabilities:   sortedKnownCaps,
				},
			},
		},
		SyncWorkerStatus{
			Generation:  1,
			Reconciling: true,
			Completed:   1,
			Done:        3,
			Total:       3,
			VersionHash: "DL-FFQ2Uem8=", Architecture: architecture,
			Actual: configv1.Release{
				Version:  "1.0.0-abc",
				Image:    "image/image:1",
				URL:      "https://example.com/v1.0.0-abc",
				Channels: []string{"channel-a", "channel-b", "channel-c"},
			},
			LastProgress: time.Unix(5, 0),
			loadPayloadStatus: LoadPayloadStatus{
				Step:               "PayloadLoaded",
				Message:            "Payload loaded version=\"1.0.0-abc\" image=\"image/image:1\" architecture=\"" + architecture + "\"",
				LastTransitionTime: time.Unix(6, 0),
				Update:             configv1.Update{Version: "1.0.0-abc", Image: "image/image:1"},
			},
			CapabilitiesStatus: CapabilityStatus{
				Status: configv1.ClusterVersionCapabilitiesStatus{
					EnabledCapabilities: sortedCaps,
					KnownCapabilities:   sortedKnownCaps,
				},
			},
		},
	)

	// Step 4: Now that sync is complete, verify status is updated to represent image contents
	//
	client.ClearActions()
	err = o.sync(ctx, o.queueKey())
	if err != nil {
		t.Fatal(err)
	}
	actions = client.Actions()
	if len(actions) != 2 {
		t.Fatalf("%s", spew.Sdump(actions))
	}
	expectGet(t, actions[0], "clusterversions", "", "version")
	// update the status to indicate we are synced, available, and report versions
	actual = cvs["version"].(*configv1.ClusterVersion)
	expectUpdateStatus(t, actions[1], "clusterversions", "", &configv1.ClusterVersion{
		ObjectMeta: metav1.ObjectMeta{
			Name:            "version",
			Generation:      1,
			ResourceVersion: "2",
		},
		Spec: configv1.ClusterVersionSpec{
			ClusterID: actual.Spec.ClusterID,
			Channel:   "fast",
		},
		Status: configv1.ClusterVersionStatus{
			ObservedGeneration: 1,
			// Prefers the image version over the operator's version (although in general they will remain in sync)
			Desired: configv1.Release{
				Version:  "1.0.0-abc",
				Image:    "image/image:1",
				URL:      "https://example.com/v1.0.0-abc",
				Channels: []string{"channel-a", "channel-b", "channel-c"},
			},
			VersionHash: "DL-FFQ2Uem8=",
			Capabilities: configv1.ClusterVersionCapabilitiesStatus{
				KnownCapabilities:   sortedKnownCaps,
				EnabledCapabilities: sortedCaps,
			},
			History: []configv1.UpdateHistory{
				{State: configv1.CompletedUpdate, Image: "image/image:1", Version: "1.0.0-abc", StartedTime: defaultStartedTime, CompletionTime: &defaultCompletionTime},
			},
			Conditions: []configv1.ClusterOperatorStatusCondition{
				{Type: ImplicitlyEnabledCapabilities, Status: "False", Reason: "AsExpected", Message: "Capabilities match configured spec"},
				{Type: configv1.OperatorAvailable, Status: configv1.ConditionTrue, Message: "Done applying 1.0.0-abc"},
				{Type: ClusterStatusFailing, Status: configv1.ConditionFalse},
				{Type: configv1.OperatorProgressing, Status: configv1.ConditionFalse, Message: "Cluster version is 1.0.0-abc"},
				{Type: configv1.RetrievedUpdates, Status: configv1.ConditionFalse},
				{Type: DesiredReleaseAccepted, Status: configv1.ConditionTrue, Reason: "PayloadLoaded",
					Message: "Payload loaded version=\"1.0.0-abc\" image=\"image/image:1\" architecture=\"" + architecture + "\""},
			},
		},
	})

	// Step 5: Wait for the SyncWorker to trigger a reconcile (500ms after the first)
	//
	verifyAllStatus(t, worker.StatusCh(),
		SyncWorkerStatus{
			Generation:  1,
			Reconciling: true,
			Total:       3,
			VersionHash: "DL-FFQ2Uem8=", Architecture: architecture,
			Actual: configv1.Release{
				Version:  "1.0.0-abc",
				Image:    "image/image:1",
				URL:      "https://example.com/v1.0.0-abc",
				Channels: []string{"channel-a", "channel-b", "channel-c"},
			},
			CapabilitiesStatus: CapabilityStatus{
				Status: configv1.ClusterVersionCapabilitiesStatus{
					EnabledCapabilities: sortedCaps,
					KnownCapabilities:   sortedKnownCaps,
				},
			},
			loadPayloadStatus: LoadPayloadStatus{
				Step:               "PayloadLoaded",
				Message:            "Payload loaded version=\"1.0.0-abc\" image=\"image/image:1\" architecture=\"" + architecture + "\"",
				LastTransitionTime: time.Unix(1, 0),
				Update:             configv1.Update{Version: "1.0.0-abc", Image: "image/image:1"},
			},
		},
		SyncWorkerStatus{
			Generation:  1,
			Reconciling: true,
			Done:        1,
			Total:       3,
			VersionHash: "DL-FFQ2Uem8=", Architecture: architecture,
			Actual: configv1.Release{
				Version:  "1.0.0-abc",
				Image:    "image/image:1",
				URL:      "https://example.com/v1.0.0-abc",
				Channels: []string{"channel-a", "channel-b", "channel-c"},
			},
			LastProgress: time.Unix(1, 0),
			CapabilitiesStatus: CapabilityStatus{
				Status: configv1.ClusterVersionCapabilitiesStatus{
					EnabledCapabilities: sortedCaps,
					KnownCapabilities:   sortedKnownCaps,
				},
			},
			loadPayloadStatus: LoadPayloadStatus{
				Step:               "PayloadLoaded",
				Message:            "Payload loaded version=\"1.0.0-abc\" image=\"image/image:1\" architecture=\"" + architecture + "\"",
				LastTransitionTime: time.Unix(2, 0),
				Update:             configv1.Update{Version: "1.0.0-abc", Image: "image/image:1"},
			},
		},
		SyncWorkerStatus{
			Generation:  1,
			Reconciling: true,
			Done:        2,
			Total:       3,
			VersionHash: "DL-FFQ2Uem8=", Architecture: architecture,
			Actual: configv1.Release{
				Version:  "1.0.0-abc",
				Image:    "image/image:1",
				URL:      "https://example.com/v1.0.0-abc",
				Channels: []string{"channel-a", "channel-b", "channel-c"},
			},
			LastProgress: time.Unix(2, 0),
			CapabilitiesStatus: CapabilityStatus{
				Status: configv1.ClusterVersionCapabilitiesStatus{
					EnabledCapabilities: sortedCaps,
					KnownCapabilities:   sortedKnownCaps,
				},
			},
			loadPayloadStatus: LoadPayloadStatus{
				Step:               "PayloadLoaded",
				Message:            "Payload loaded version=\"1.0.0-abc\" image=\"image/image:1\" architecture=\"" + architecture + "\"",
				LastTransitionTime: time.Unix(3, 0),
				Update:             configv1.Update{Version: "1.0.0-abc", Image: "image/image:1"},
			},
		},
		SyncWorkerStatus{
			Generation:  1,
			Reconciling: true,
			Completed:   2,
			Done:        3,
			Total:       3,
			VersionHash: "DL-FFQ2Uem8=", Architecture: architecture,
			Actual: configv1.Release{
				Version:  "1.0.0-abc",
				Image:    "image/image:1",
				URL:      "https://example.com/v1.0.0-abc",
				Channels: []string{"channel-a", "channel-b", "channel-c"},
			},
			LastProgress: time.Unix(3, 0),
			CapabilitiesStatus: CapabilityStatus{
				Status: configv1.ClusterVersionCapabilitiesStatus{
					EnabledCapabilities: sortedCaps,
					KnownCapabilities:   sortedKnownCaps,
				},
			},
			loadPayloadStatus: LoadPayloadStatus{
				Step:               "PayloadLoaded",
				Message:            "Payload loaded version=\"1.0.0-abc\" image=\"image/image:1\" architecture=\"" + architecture + "\"",
				LastTransitionTime: time.Unix(4, 0),
				Update:             configv1.Update{Version: "1.0.0-abc", Image: "image/image:1"},
			},
		},
	)

	// Step 6: After a reconciliation, there should be no status change because the state is the same
	//
	client.ClearActions()
	err = o.sync(ctx, o.queueKey())
	if err != nil {
		t.Fatal(err)
	}
	actions = client.Actions()
	if len(actions) != 1 {
		t.Fatalf("%s", spew.Sdump(actions))
	}
	expectGet(t, actions[0], "clusterversions", "", "version")
}

func TestCVO_StartupAndSyncUnverifiedPayload(t *testing.T) {
	o, cvs, client, _, shutdownFn := setupCVOTest("testdata/payloadtest")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	defer shutdownFn()

	// make the image report unverified
	payloadErr := &payload.UpdateError{
		Reason:  "ImageVerificationFailed",
		Message: "The update cannot be verified: some random error",
		Nested:  fmt.Errorf("some random error"),
	}
	if !isImageVerificationError(payloadErr) {
		t.Fatal("not the correct error type")
	}
	worker := o.configSync.(*SyncWorker)
	worker.retriever.(*fakeDirectoryRetriever).Info = PayloadInfo{
		Directory: "testdata/payloadtest",
		Local:     true,

		VerificationError: payloadErr,
	}

	go worker.Start(ctx, 1)

	// Step 1: Ensure the CVO reports a status error if it has nothing to sync
	//
	client.ClearActions()
	err := o.sync(ctx, o.queueKey())
	if err != nil {
		t.Fatal(err)
	}
	actions := client.Actions()
	if len(actions) != 2 {
		t.Fatalf("%s", spew.Sdump(actions))
	}
	expectGet(t, actions[0], "clusterversions", "", "version")
	actual := cvs["version"].(*configv1.ClusterVersion)
	expectUpdateStatus(t, actions[1], "clusterversions", "", &configv1.ClusterVersion{
		ObjectMeta: metav1.ObjectMeta{
			Name:       "version",
			Generation: 1,
		},
		Spec: configv1.ClusterVersionSpec{
			ClusterID: actual.Spec.ClusterID,
			Channel:   "fast",
		},
		Status: configv1.ClusterVersionStatus{
			History: []configv1.UpdateHistory{
				// empty because the operator release image is not set, so we have no input
			},
			Conditions: []configv1.ClusterOperatorStatusCondition{
				{Type: ImplicitlyEnabledCapabilities, Status: "False", Reason: "AsExpected", Message: "Capabilities match configured spec"},
				{Type: configv1.OperatorAvailable, Status: configv1.ConditionFalse},
				// report back to the user that we don't have enough info to proceed
				{Type: ClusterStatusFailing, Status: configv1.ConditionTrue, Reason: "NoDesiredImage", Message: "No configured operator version, unable to update cluster"},
				{Type: configv1.OperatorProgressing, Status: configv1.ConditionTrue, Reason: "NoDesiredImage", Message: "Unable to apply <unknown>: an unknown error has occurred: NoDesiredImage"},
				{Type: configv1.RetrievedUpdates, Status: configv1.ConditionFalse},
			},
		},
	})
	verifyAllStatus(t, worker.StatusCh())

	// Step 3: Given an operator image, begin synchronizing
	//
	o.release.Image = "image/image:1"
	o.release.Version = "1.0.0-abc"
	desired := configv1.Release{Version: "1.0.0-abc", Image: "image/image:1"}
	//
	client.ClearActions()
	err = o.sync(ctx, o.queueKey())
	if err != nil {
		t.Fatal(err)
	}
	actions = client.Actions()
	if len(actions) != 2 {
		t.Fatalf("%s", spew.Sdump(actions))
	}
	expectGet(t, actions[0], "clusterversions", "", "version")
	actual = cvs["version"].(*configv1.ClusterVersion)
	expectUpdateStatus(t, actions[1], "clusterversions", "", &configv1.ClusterVersion{
		ObjectMeta: metav1.ObjectMeta{
			Name:            "version",
			Generation:      1,
			ResourceVersion: "1",
		},
		Spec: configv1.ClusterVersionSpec{
			ClusterID: actual.Spec.ClusterID,
			Channel:   "fast",
		},
		Status: configv1.ClusterVersionStatus{
			Desired:            desired,
			ObservedGeneration: 1,
			History: []configv1.UpdateHistory{
				{State: configv1.PartialUpdate, Image: "image/image:1", Version: "1.0.0-abc", StartedTime: defaultStartedTime, AcceptedRisks: "The update cannot be verified: some random error"},
			},
			Capabilities: configv1.ClusterVersionCapabilitiesStatus{
				KnownCapabilities:   sortedKnownCaps,
				EnabledCapabilities: sortedCaps,
			},
			Conditions: []configv1.ClusterOperatorStatusCondition{
				{Type: ImplicitlyEnabledCapabilities, Status: "False", Reason: "AsExpected", Message: "Capabilities match configured spec"},
				{Type: configv1.OperatorAvailable, Status: configv1.ConditionFalse},
				// cleared failing status and set progressing
				{Type: ClusterStatusFailing, Status: configv1.ConditionFalse},
				{Type: configv1.OperatorProgressing, Status: configv1.ConditionTrue, Message: "Working towards 1.0.0-abc"},
				{Type: configv1.RetrievedUpdates, Status: configv1.ConditionFalse},
				{Type: DesiredReleaseAccepted, Status: configv1.ConditionTrue, Reason: "PayloadLoaded",
					Message: "Payload loaded version=\"1.0.0-abc\" image=\"image/image:1\" architecture=\"" + architecture + "\""},
			},
		},
	})
	verifyAllStatus(t, worker.StatusCh(),
		SyncWorkerStatus{
			Actual:     configv1.Release{Version: "1.0.0-abc", Image: "image/image:1"},
			Generation: 1,
			loadPayloadStatus: LoadPayloadStatus{
				Step:               "RetrievePayload",
				Message:            "Retrieving and verifying payload version=\"1.0.0-abc\" image=\"image/image:1\"",
				LastTransitionTime: time.Unix(1, 0),
				Update:             configv1.Update{Version: "1.0.0-abc", Image: "image/image:1"},
			},
		},
		SyncWorkerStatus{
			Actual: configv1.Release{
				Version: "1.0.0-abc",
				Image:   "image/image:1",
			},
			LastProgress: time.Unix(1, 0),
			Generation:   1,
			loadPayloadStatus: LoadPayloadStatus{
				Step:               "PayloadLoaded",
				Message:            "Payload loaded version=\"1.0.0-abc\" image=\"image/image:1\" architecture=\"" + architecture + "\"",
				AcceptedRisks:      "The update cannot be verified: some random error",
				LastTransitionTime: time.Unix(2, 0),
				Local:              true,
				Update:             configv1.Update{Version: "1.0.0-abc", Image: "image/image:1"},
			},
		},
		SyncWorkerStatus{
			Total:       3,
			Initial:     true,
			VersionHash: "DL-FFQ2Uem8=", Architecture: architecture,
			Actual: configv1.Release{
				Version:  "1.0.0-abc",
				Image:    "image/image:1",
				URL:      "https://example.com/v1.0.0-abc",
				Channels: []string{"channel-a", "channel-b", "channel-c"},
			},
			LastProgress: time.Unix(2, 0),
			Generation:   1,
			CapabilitiesStatus: CapabilityStatus{
				Status: configv1.ClusterVersionCapabilitiesStatus{
					EnabledCapabilities: sortedCaps,
					KnownCapabilities:   sortedKnownCaps,
				},
			},
			loadPayloadStatus: LoadPayloadStatus{
				Step:               "PayloadLoaded",
				Message:            "Payload loaded version=\"1.0.0-abc\" image=\"image/image:1\" architecture=\"" + architecture + "\"",
				AcceptedRisks:      "The update cannot be verified: some random error",
				LastTransitionTime: time.Unix(3, 0),
				Local:              true,
				Update:             configv1.Update{Version: "1.0.0-abc", Image: "image/image:1"},
			},
		},
		SyncWorkerStatus{
			Done:        1,
			Total:       3,
			Initial:     true,
			VersionHash: "DL-FFQ2Uem8=", Architecture: architecture,
			Actual: configv1.Release{
				Version:  "1.0.0-abc",
				Image:    "image/image:1",
				URL:      "https://example.com/v1.0.0-abc",
				Channels: []string{"channel-a", "channel-b", "channel-c"},
			},
			LastProgress: time.Unix(3, 0),
			Generation:   1,
			CapabilitiesStatus: CapabilityStatus{
				Status: configv1.ClusterVersionCapabilitiesStatus{
					EnabledCapabilities: sortedCaps,
					KnownCapabilities:   sortedKnownCaps,
				},
			},
			loadPayloadStatus: LoadPayloadStatus{
				Step:               "PayloadLoaded",
				Message:            "Payload loaded version=\"1.0.0-abc\" image=\"image/image:1\" architecture=\"" + architecture + "\"",
				AcceptedRisks:      "The update cannot be verified: some random error",
				LastTransitionTime: time.Unix(4, 0),
				Local:              true,
				Update:             configv1.Update{Version: "1.0.0-abc", Image: "image/image:1"},
			},
		},
		SyncWorkerStatus{
			Initial:     true,
			Done:        2,
			Total:       3,
			VersionHash: "DL-FFQ2Uem8=", Architecture: architecture,
			Actual: configv1.Release{
				Version:  "1.0.0-abc",
				Image:    "image/image:1",
				URL:      "https://example.com/v1.0.0-abc",
				Channels: []string{"channel-a", "channel-b", "channel-c"},
			},
			LastProgress: time.Unix(4, 0),
			Generation:   1,
			CapabilitiesStatus: CapabilityStatus{
				Status: configv1.ClusterVersionCapabilitiesStatus{
					EnabledCapabilities: sortedCaps,
					KnownCapabilities:   sortedKnownCaps,
				},
			},
			loadPayloadStatus: LoadPayloadStatus{
				Step:               "PayloadLoaded",
				Message:            "Payload loaded version=\"1.0.0-abc\" image=\"image/image:1\" architecture=\"" + architecture + "\"",
				AcceptedRisks:      "The update cannot be verified: some random error",
				LastTransitionTime: time.Unix(5, 0),
				Local:              true,
				Update:             configv1.Update{Version: "1.0.0-abc", Image: "image/image:1"},
			},
		},
	)

	// wait for status to reflect sync of new payload
	waitForStatusCompleted(t, worker)

	// Step 4: Now that sync is complete, verify status is updated to represent image contents
	//
	client.ClearActions()
	err = o.sync(ctx, o.queueKey())
	if err != nil {
		t.Fatal(err)
	}

	actions = client.Actions()

	if len(actions) != 2 {
		t.Fatalf("%s", spew.Sdump(actions))
	}
	expectGet(t, actions[0], "clusterversions", "", "version")
	// update the status to indicate we are synced, available, and report versions
	actual = cvs["version"].(*configv1.ClusterVersion)
	expectUpdateStatus(t, actions[1], "clusterversions", "", &configv1.ClusterVersion{
		ObjectMeta: metav1.ObjectMeta{
			Name:            "version",
			Generation:      1,
			ResourceVersion: "2",
		},
		Spec: configv1.ClusterVersionSpec{
			ClusterID: actual.Spec.ClusterID,
			Channel:   "fast",
		},
		Status: configv1.ClusterVersionStatus{
			ObservedGeneration: 1,
			// Prefers the image version over the operator's version (although in general they will remain in sync)
			Desired: configv1.Release{
				Version:  "1.0.0-abc",
				Image:    "image/image:1",
				URL:      "https://example.com/v1.0.0-abc",
				Channels: []string{"channel-a", "channel-b", "channel-c"},
			},
			VersionHash: "DL-FFQ2Uem8=",
			Capabilities: configv1.ClusterVersionCapabilitiesStatus{
				EnabledCapabilities: sortedCaps,
				KnownCapabilities:   sortedKnownCaps,
			},
			History: []configv1.UpdateHistory{
				{State: configv1.CompletedUpdate, Image: "image/image:1", Version: "1.0.0-abc", StartedTime: defaultStartedTime, CompletionTime: &defaultCompletionTime, AcceptedRisks: "The update cannot be verified: some random error"},
			},
			Conditions: []configv1.ClusterOperatorStatusCondition{
				{Type: ImplicitlyEnabledCapabilities, Status: "False", Reason: "AsExpected", Message: "Capabilities match configured spec"},
				{Type: configv1.OperatorAvailable, Status: configv1.ConditionTrue, Message: "Done applying 1.0.0-abc"},
				{Type: ClusterStatusFailing, Status: configv1.ConditionFalse},
				{Type: configv1.OperatorProgressing, Status: configv1.ConditionFalse, Message: "Cluster version is 1.0.0-abc"},
				{Type: configv1.RetrievedUpdates, Status: configv1.ConditionFalse},
				{Type: DesiredReleaseAccepted, Status: configv1.ConditionTrue, Reason: "PayloadLoaded",
					Message: "Payload loaded version=\"1.0.0-abc\" image=\"image/image:1\" architecture=\"" + architecture + "\""},
			},
		},
	})

	// Step 5: Wait for the SyncWorker to trigger a reconcile (500ms after the first)
	//
	verifyAllStatus(t, worker.StatusCh(),
		SyncWorkerStatus{
			Reconciling: true,
			Total:       3,
			VersionHash: "DL-FFQ2Uem8=", Architecture: architecture,
			Actual: configv1.Release{
				Version:  "1.0.0-abc",
				Image:    "image/image:1",
				URL:      "https://example.com/v1.0.0-abc",
				Channels: []string{"channel-a", "channel-b", "channel-c"},
			},
			Generation: 1,
			CapabilitiesStatus: CapabilityStatus{
				Status: configv1.ClusterVersionCapabilitiesStatus{
					EnabledCapabilities: sortedCaps,
					KnownCapabilities:   sortedKnownCaps,
				},
			},
			loadPayloadStatus: LoadPayloadStatus{
				Step:               "PayloadLoaded",
				Message:            "Payload loaded version=\"1.0.0-abc\" image=\"image/image:1\" architecture=\"" + architecture + "\"",
				AcceptedRisks:      "The update cannot be verified: some random error",
				LastTransitionTime: time.Unix(1, 0),
				Local:              true,
				Update:             configv1.Update{Version: "1.0.0-abc", Image: "image/image:1"},
			},
		},
		SyncWorkerStatus{
			Reconciling: true,
			Done:        1,
			Total:       3,
			VersionHash: "DL-FFQ2Uem8=", Architecture: architecture,
			Actual: configv1.Release{
				Version:  "1.0.0-abc",
				Image:    "image/image:1",
				URL:      "https://example.com/v1.0.0-abc",
				Channels: []string{"channel-a", "channel-b", "channel-c"},
			},
			Generation:   1,
			LastProgress: time.Unix(1, 0),
			CapabilitiesStatus: CapabilityStatus{
				Status: configv1.ClusterVersionCapabilitiesStatus{
					EnabledCapabilities: sortedCaps,
					KnownCapabilities:   sortedKnownCaps,
				},
			},
			loadPayloadStatus: LoadPayloadStatus{
				Step:               "PayloadLoaded",
				Message:            "Payload loaded version=\"1.0.0-abc\" image=\"image/image:1\" architecture=\"" + architecture + "\"",
				AcceptedRisks:      "The update cannot be verified: some random error",
				LastTransitionTime: time.Unix(2, 0),
				Local:              true,
				Update:             configv1.Update{Version: "1.0.0-abc", Image: "image/image:1"},
			},
		},
		SyncWorkerStatus{
			Reconciling: true,
			Done:        2,
			Total:       3,
			VersionHash: "DL-FFQ2Uem8=", Architecture: architecture,
			Actual: configv1.Release{
				Version:  "1.0.0-abc",
				Image:    "image/image:1",
				URL:      "https://example.com/v1.0.0-abc",
				Channels: []string{"channel-a", "channel-b", "channel-c"},
			},
			Generation:   1,
			LastProgress: time.Unix(2, 0),
			CapabilitiesStatus: CapabilityStatus{
				Status: configv1.ClusterVersionCapabilitiesStatus{
					EnabledCapabilities: sortedCaps,
					KnownCapabilities:   sortedKnownCaps,
				},
			},
			loadPayloadStatus: LoadPayloadStatus{
				Step:               "PayloadLoaded",
				Message:            "Payload loaded version=\"1.0.0-abc\" image=\"image/image:1\" architecture=\"" + architecture + "\"",
				AcceptedRisks:      "The update cannot be verified: some random error",
				LastTransitionTime: time.Unix(3, 0),
				Local:              true,
				Update:             configv1.Update{Version: "1.0.0-abc", Image: "image/image:1"},
			},
		},
		SyncWorkerStatus{
			Reconciling: true,
			Completed:   2,
			Done:        3,
			Total:       3,
			VersionHash: "DL-FFQ2Uem8=", Architecture: architecture,
			Actual: configv1.Release{
				Version:  "1.0.0-abc",
				Image:    "image/image:1",
				URL:      "https://example.com/v1.0.0-abc",
				Channels: []string{"channel-a", "channel-b", "channel-c"},
			},
			LastProgress: time.Unix(3, 0),
			Generation:   1,
			CapabilitiesStatus: CapabilityStatus{
				Status: configv1.ClusterVersionCapabilitiesStatus{
					EnabledCapabilities: sortedCaps,
					KnownCapabilities:   sortedKnownCaps,
				},
			},
			loadPayloadStatus: LoadPayloadStatus{
				Step:               "PayloadLoaded",
				Message:            "Payload loaded version=\"1.0.0-abc\" image=\"image/image:1\" architecture=\"" + architecture + "\"",
				AcceptedRisks:      "The update cannot be verified: some random error",
				LastTransitionTime: time.Unix(4, 0),
				Local:              true,
				Update:             configv1.Update{Version: "1.0.0-abc", Image: "image/image:1"},
			},
		},
	)

	// Step 6: After a reconciliation, there should be no status change because the state is the same
	//
	client.ClearActions()
	err = o.sync(ctx, o.queueKey())
	if err != nil {
		t.Fatal(err)
	}
	actions = client.Actions()
	if len(actions) != 1 {
		t.Fatalf("%s", spew.Sdump(actions))
	}
	expectGet(t, actions[0], "clusterversions", "", "version")
}

func TestCVO_StartupAndSyncPreconditionFailing(t *testing.T) {
	o, cvs, client, _, shutdownFn := setupCVOTest("testdata/payloadtest")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	defer shutdownFn()

	worker := o.configSync.(*SyncWorker)
	// Need the precondition check to fail permanently, so setting failure until 100 attempt to simulate that.
	worker.preconditions = []precondition.Precondition{&testPrecondition{SuccessAfter: 100}}
	worker.retriever.(*fakeDirectoryRetriever).Info = PayloadInfo{
		Directory: "testdata/payloadtest",
		Local:     true,
	}
	go worker.Start(ctx, 1)

	// Step 1: Ensure the CVO reports a status error if it has nothing to sync
	//
	client.ClearActions()
	err := o.sync(ctx, o.queueKey())
	if err != nil {
		t.Fatal(err)
	}
	actions := client.Actions()
	if len(actions) != 2 {
		t.Fatalf("%s", spew.Sdump(actions))
	}
	expectGet(t, actions[0], "clusterversions", "", "version")
	actual := cvs["version"].(*configv1.ClusterVersion)
	expectUpdateStatus(t, actions[1], "clusterversions", "", &configv1.ClusterVersion{
		ObjectMeta: metav1.ObjectMeta{
			Name:       "version",
			Generation: 1,
		},
		Spec: configv1.ClusterVersionSpec{
			ClusterID: actual.Spec.ClusterID,
			Channel:   "fast",
		},
		Status: configv1.ClusterVersionStatus{
			History: []configv1.UpdateHistory{
				// empty because the operator release image is not set, so we have no input
			},
			Conditions: []configv1.ClusterOperatorStatusCondition{
				{Type: ImplicitlyEnabledCapabilities, Status: "False", Reason: "AsExpected", Message: "Capabilities match configured spec"},
				{Type: configv1.OperatorAvailable, Status: configv1.ConditionFalse},
				// report back to the user that we don't have enough info to proceed
				{Type: ClusterStatusFailing, Status: configv1.ConditionTrue, Reason: "NoDesiredImage", Message: "No configured operator version, unable to update cluster"},
				{Type: configv1.OperatorProgressing, Status: configv1.ConditionTrue, Reason: "NoDesiredImage", Message: "Unable to apply <unknown>: an unknown error has occurred: NoDesiredImage"},
				{Type: configv1.RetrievedUpdates, Status: configv1.ConditionFalse},
			},
		},
	})
	verifyAllStatus(t, worker.StatusCh())

	// Step 3: Given an operator image, begin synchronizing
	//
	o.release.Image = "image/image:1"
	o.release.Version = "1.0.0-abc"
	desired := configv1.Release{Version: "1.0.0-abc", Image: "image/image:1"}
	//
	client.ClearActions()
	err = o.sync(ctx, o.queueKey())
	if err != nil {
		t.Fatal(err)
	}
	actions = client.Actions()
	if len(actions) != 2 {
		t.Fatalf("%s", spew.Sdump(actions))
	}
	expectGet(t, actions[0], "clusterversions", "", "version")
	actual = cvs["version"].(*configv1.ClusterVersion)
	expectUpdateStatus(t, actions[1], "clusterversions", "", &configv1.ClusterVersion{
		ObjectMeta: metav1.ObjectMeta{
			Name:            "version",
			Generation:      1,
			ResourceVersion: "1",
		},
		Spec: configv1.ClusterVersionSpec{
			ClusterID: actual.Spec.ClusterID,
			Channel:   "fast",
		},
		Status: configv1.ClusterVersionStatus{
			Desired:            desired,
			ObservedGeneration: 1,
			History: []configv1.UpdateHistory{
				{State: configv1.PartialUpdate, Image: "image/image:1", Version: "1.0.0-abc", StartedTime: defaultStartedTime},
			},
			Capabilities: configv1.ClusterVersionCapabilitiesStatus{
				EnabledCapabilities: sortedCaps,
				KnownCapabilities:   sortedKnownCaps,
			},
			Conditions: []configv1.ClusterOperatorStatusCondition{
				{Type: ImplicitlyEnabledCapabilities, Status: "False", Reason: "AsExpected", Message: "Capabilities match configured spec"},
				{Type: configv1.OperatorAvailable, Status: configv1.ConditionFalse},
				// cleared failing status and set progressing
				{Type: ClusterStatusFailing, Status: configv1.ConditionFalse},
				{Type: configv1.OperatorProgressing, Status: configv1.ConditionTrue, Message: "Working towards 1.0.0-abc"},
				{Type: configv1.RetrievedUpdates, Status: configv1.ConditionFalse},
				{Type: DesiredReleaseAccepted, Status: configv1.ConditionTrue, Reason: "PayloadLoaded",
					Message: "Payload loaded version=\"1.0.0-abc\" image=\"image/image:1\" architecture=\"" + architecture + "\""},
			},
		},
	})
	verifyAllStatus(t, worker.StatusCh(),
		SyncWorkerStatus{
			Actual:     configv1.Release{Version: "1.0.0-abc", Image: "image/image:1"},
			Generation: 1,
			loadPayloadStatus: LoadPayloadStatus{
				Step:               "RetrievePayload",
				Message:            "Retrieving and verifying payload version=\"1.0.0-abc\" image=\"image/image:1\"",
				LastTransitionTime: time.Unix(1, 0),
				Update:             configv1.Update{Version: "1.0.0-abc", Image: "image/image:1"},
			},
		},
		SyncWorkerStatus{
			Actual: configv1.Release{
				Version: "1.0.0-abc",
				Image:   "image/image:1",
			},
			Generation:   1,
			LastProgress: time.Unix(1, 0),
			loadPayloadStatus: LoadPayloadStatus{
				Step:               "PayloadLoaded",
				Message:            "Payload loaded version=\"1.0.0-abc\" image=\"image/image:1\" architecture=\"" + architecture + "\"",
				LastTransitionTime: time.Unix(2, 0),
				Local:              true,
				Update:             configv1.Update{Version: "1.0.0-abc", Image: "image/image:1"},
			},
		},
		SyncWorkerStatus{
			Total:       3,
			Initial:     true,
			VersionHash: "DL-FFQ2Uem8=", Architecture: architecture,
			Actual: configv1.Release{
				Version:  "1.0.0-abc",
				Image:    "image/image:1",
				URL:      "https://example.com/v1.0.0-abc",
				Channels: []string{"channel-a", "channel-b", "channel-c"},
			},
			LastProgress: time.Unix(2, 0),
			Generation:   1,
			loadPayloadStatus: LoadPayloadStatus{
				Step:               "PayloadLoaded",
				Message:            "Payload loaded version=\"1.0.0-abc\" image=\"image/image:1\" architecture=\"" + architecture + "\"",
				LastTransitionTime: time.Unix(3, 0),
				Local:              true,
				Update:             configv1.Update{Version: "1.0.0-abc", Image: "image/image:1"},
			},
			CapabilitiesStatus: CapabilityStatus{
				Status: configv1.ClusterVersionCapabilitiesStatus{
					EnabledCapabilities: sortedCaps,
					KnownCapabilities:   sortedKnownCaps,
				},
			},
		},
		SyncWorkerStatus{
			Done:        1,
			Total:       3,
			Initial:     true,
			VersionHash: "DL-FFQ2Uem8=", Architecture: architecture,
			Actual: configv1.Release{
				Version:  "1.0.0-abc",
				Image:    "image/image:1",
				URL:      "https://example.com/v1.0.0-abc",
				Channels: []string{"channel-a", "channel-b", "channel-c"},
			},
			LastProgress: time.Unix(3, 0),
			Generation:   1,
			loadPayloadStatus: LoadPayloadStatus{
				Step:               "PayloadLoaded",
				Message:            "Payload loaded version=\"1.0.0-abc\" image=\"image/image:1\" architecture=\"" + architecture + "\"",
				LastTransitionTime: time.Unix(4, 0),
				Local:              true,
				Update:             configv1.Update{Version: "1.0.0-abc", Image: "image/image:1"},
			},
			CapabilitiesStatus: CapabilityStatus{
				Status: configv1.ClusterVersionCapabilitiesStatus{
					EnabledCapabilities: sortedCaps,
					KnownCapabilities:   sortedKnownCaps,
				},
			},
		},
		SyncWorkerStatus{
			Done:        2,
			Total:       3,
			Initial:     true,
			VersionHash: "DL-FFQ2Uem8=", Architecture: architecture,
			Actual: configv1.Release{
				Version:  "1.0.0-abc",
				Image:    "image/image:1",
				URL:      "https://example.com/v1.0.0-abc",
				Channels: []string{"channel-a", "channel-b", "channel-c"},
			},
			LastProgress: time.Unix(4, 0),
			Generation:   1,
			CapabilitiesStatus: CapabilityStatus{
				Status: configv1.ClusterVersionCapabilitiesStatus{
					EnabledCapabilities: sortedCaps,
					KnownCapabilities:   sortedKnownCaps,
				},
			},
			loadPayloadStatus: LoadPayloadStatus{
				Step:               "PayloadLoaded",
				Message:            "Payload loaded version=\"1.0.0-abc\" image=\"image/image:1\" architecture=\"" + architecture + "\"",
				LastTransitionTime: time.Unix(5, 0),
				Local:              true,
				Update:             configv1.Update{Version: "1.0.0-abc", Image: "image/image:1"},
			},
		},
	)

	// wait for status to reflect sync of new payload
	waitForStatusCompleted(t, worker)

	// Step 4: Now that sync is complete, verify status is updated to represent image contents
	//
	client.ClearActions()
	err = o.sync(ctx, o.queueKey())
	if err != nil {
		t.Fatal(err)
	}

	actions = client.Actions()
	if len(actions) != 2 {
		t.Fatalf("%s", spew.Sdump(actions))
	}
	expectGet(t, actions[0], "clusterversions", "", "version")
	// update the status to indicate we are synced, available, and report versions
	actual = cvs["version"].(*configv1.ClusterVersion)
	expectUpdateStatus(t, actions[1], "clusterversions", "", &configv1.ClusterVersion{
		ObjectMeta: metav1.ObjectMeta{
			Name:            "version",
			Generation:      1,
			ResourceVersion: "2",
		},
		Spec: configv1.ClusterVersionSpec{
			ClusterID: actual.Spec.ClusterID,
			Channel:   "fast",
		},
		Status: configv1.ClusterVersionStatus{
			ObservedGeneration: 1,
			// Prefers the image version over the operator's version (although in general they will remain in sync)
			Desired: configv1.Release{
				Version:  "1.0.0-abc",
				Image:    "image/image:1",
				URL:      "https://example.com/v1.0.0-abc",
				Channels: []string{"channel-a", "channel-b", "channel-c"},
			},
			VersionHash: "DL-FFQ2Uem8=",
			Capabilities: configv1.ClusterVersionCapabilitiesStatus{
				EnabledCapabilities: sortedCaps,
				KnownCapabilities:   sortedKnownCaps,
			},
			History: []configv1.UpdateHistory{
				{State: configv1.CompletedUpdate, Image: "image/image:1", Version: "1.0.0-abc", StartedTime: defaultStartedTime, CompletionTime: &defaultCompletionTime},
			},
			Conditions: []configv1.ClusterOperatorStatusCondition{
				{Type: ImplicitlyEnabledCapabilities, Status: "False", Reason: "AsExpected", Message: "Capabilities match configured spec"},
				{Type: configv1.OperatorAvailable, Status: configv1.ConditionTrue, Message: "Done applying 1.0.0-abc"},
				{Type: ClusterStatusFailing, Status: configv1.ConditionFalse},
				{Type: configv1.OperatorProgressing, Status: configv1.ConditionFalse, Message: "Cluster version is 1.0.0-abc"},
				{Type: configv1.RetrievedUpdates, Status: configv1.ConditionFalse},
				{Type: DesiredReleaseAccepted, Status: configv1.ConditionTrue, Reason: "PayloadLoaded",
					Message: "Payload loaded version=\"1.0.0-abc\" image=\"image/image:1\" architecture=\"" + architecture + "\""},
			},
		},
	})

	// Step 5: Wait for the SyncWorker to trigger a reconcile (500ms after the first)
	//
	verifyAllStatus(t, worker.StatusCh(),
		SyncWorkerStatus{
			Reconciling: true,
			Total:       3,
			VersionHash: "DL-FFQ2Uem8=", Architecture: architecture,
			Actual: configv1.Release{
				Version:  "1.0.0-abc",
				Image:    "image/image:1",
				URL:      "https://example.com/v1.0.0-abc",
				Channels: []string{"channel-a", "channel-b", "channel-c"},
			},
			Generation: 1,
			CapabilitiesStatus: CapabilityStatus{
				Status: configv1.ClusterVersionCapabilitiesStatus{
					EnabledCapabilities: sortedCaps,
					KnownCapabilities:   sortedKnownCaps,
				},
			},
			loadPayloadStatus: LoadPayloadStatus{
				Step:               "PayloadLoaded",
				Message:            "Payload loaded version=\"1.0.0-abc\" image=\"image/image:1\" architecture=\"" + architecture + "\"",
				LastTransitionTime: time.Unix(1, 0),
				Local:              true,
				Update:             configv1.Update{Version: "1.0.0-abc", Image: "image/image:1"},
			},
		},
		SyncWorkerStatus{
			Reconciling: true,
			Done:        1,
			Total:       3,
			VersionHash: "DL-FFQ2Uem8=", Architecture: architecture,
			Actual: configv1.Release{
				Version:  "1.0.0-abc",
				Image:    "image/image:1",
				URL:      "https://example.com/v1.0.0-abc",
				Channels: []string{"channel-a", "channel-b", "channel-c"},
			},
			LastProgress: time.Unix(1, 0),
			Generation:   1,
			CapabilitiesStatus: CapabilityStatus{
				Status: configv1.ClusterVersionCapabilitiesStatus{
					EnabledCapabilities: sortedCaps,
					KnownCapabilities:   sortedKnownCaps,
				},
			},
			loadPayloadStatus: LoadPayloadStatus{
				Step:               "PayloadLoaded",
				Message:            "Payload loaded version=\"1.0.0-abc\" image=\"image/image:1\" architecture=\"" + architecture + "\"",
				LastTransitionTime: time.Unix(2, 0),
				Local:              true,
				Update:             configv1.Update{Version: "1.0.0-abc", Image: "image/image:1"},
			},
		},
		SyncWorkerStatus{
			Reconciling: true,
			Done:        2,
			Total:       3,
			VersionHash: "DL-FFQ2Uem8=", Architecture: architecture,
			Actual: configv1.Release{
				Version:  "1.0.0-abc",
				Image:    "image/image:1",
				URL:      "https://example.com/v1.0.0-abc",
				Channels: []string{"channel-a", "channel-b", "channel-c"},
			},
			LastProgress: time.Unix(2, 0),
			Generation:   1,
			CapabilitiesStatus: CapabilityStatus{
				Status: configv1.ClusterVersionCapabilitiesStatus{
					EnabledCapabilities: sortedCaps,
					KnownCapabilities:   sortedKnownCaps,
				},
			},
			loadPayloadStatus: LoadPayloadStatus{
				Step:               "PayloadLoaded",
				Message:            "Payload loaded version=\"1.0.0-abc\" image=\"image/image:1\" architecture=\"" + architecture + "\"",
				LastTransitionTime: time.Unix(3, 0),
				Local:              true,
				Update:             configv1.Update{Version: "1.0.0-abc", Image: "image/image:1"},
			},
		},
		SyncWorkerStatus{
			Reconciling: true,
			Completed:   2,
			Done:        3,
			Total:       3,
			VersionHash: "DL-FFQ2Uem8=", Architecture: architecture,
			Actual: configv1.Release{
				Version:  "1.0.0-abc",
				Image:    "image/image:1",
				URL:      "https://example.com/v1.0.0-abc",
				Channels: []string{"channel-a", "channel-b", "channel-c"},
			},
			LastProgress: time.Unix(3, 0),
			Generation:   1,
			CapabilitiesStatus: CapabilityStatus{
				Status: configv1.ClusterVersionCapabilitiesStatus{
					EnabledCapabilities: sortedCaps,
					KnownCapabilities:   sortedKnownCaps,
				},
			},
			loadPayloadStatus: LoadPayloadStatus{
				Step:               "PayloadLoaded",
				Message:            "Payload loaded version=\"1.0.0-abc\" image=\"image/image:1\" architecture=\"" + architecture + "\"",
				LastTransitionTime: time.Unix(4, 0),
				Local:              true,
				Update:             configv1.Update{Version: "1.0.0-abc", Image: "image/image:1"},
			},
		},
	)

	// Step 6: After a reconciliation, there should be no status change because the state is the same
	//
	client.ClearActions()
	err = o.sync(ctx, o.queueKey())
	if err != nil {
		t.Fatal(err)
	}
	actions = client.Actions()
	if len(actions) != 1 {
		t.Fatalf("%s", spew.Sdump(actions))
	}
	expectGet(t, actions[0], "clusterversions", "", "version")
}

func TestCVO_UpgradeUnverifiedPayload(t *testing.T) {
	o, cvs, client, _, shutdownFn := setupCVOTest("testdata/payloadtest-2")

	// Setup: a successful sync from a previous run, and the operator at the same image as before
	//
	o.release.Image = "image/image:0"
	o.release.Version = "1.0.0-abc"
	desired := configv1.Release{Version: "1.0.1-abc", Image: "image/image:1"}
	uid, _ := uuid.NewRandom()
	clusterUID := configv1.ClusterID(uid.String())
	cvs["version"] = &configv1.ClusterVersion{
		ObjectMeta: metav1.ObjectMeta{
			Name:            "version",
			ResourceVersion: "1",
			Generation:      1,
		},
		Spec: configv1.ClusterVersionSpec{
			ClusterID:     clusterUID,
			Channel:       "fast",
			DesiredUpdate: &configv1.Update{Version: desired.Version, Image: desired.Image},
		},
		Status: configv1.ClusterVersionStatus{
			// Prefers the image version over the operator's version (although in general they will remain in sync)
			Desired:     desired,
			VersionHash: "DL-FFQ2Uem8=",
			History: []configv1.UpdateHistory{
				{State: configv1.CompletedUpdate, Image: "image/image:0", Version: "1.0.0-abc", Verified: true, StartedTime: defaultStartedTime, CompletionTime: &defaultCompletionTime},
			},
		},
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	defer shutdownFn()

	// make the image report unverified
	payloadErr := &payload.UpdateError{
		Reason:  "ImageVerificationFailed",
		Message: "The update cannot be verified: some random error",
		Nested:  fmt.Errorf("some random error"),
	}
	if !isImageVerificationError(payloadErr) {
		t.Fatal("not the correct error type")
	}
	worker := o.configSync.(*SyncWorker)
	worker.initializedFunc = func() bool { return true }
	retriever := worker.retriever.(*fakeDirectoryRetriever)
	retriever.Set(PayloadInfo{}, payloadErr)

	go worker.Start(ctx, 1)

	// Step 1: The operator should report that it is blocked on unverified content
	//
	client.ClearActions()
	err := o.sync(ctx, o.queueKey())
	if err != nil {
		t.Fatal(err)
	}
	actions := client.Actions()
	verifyCVSingleUpdate(t, actions)

	verifyAllStatus(t, worker.StatusCh(),
		SyncWorkerStatus{
			Actual:     configv1.Release{Version: "1.0.1-abc", Image: "image/image:1"},
			Generation: 1,
			loadPayloadStatus: LoadPayloadStatus{
				Step:               "RetrievePayload",
				Message:            "Retrieving and verifying payload version=\"1.0.1-abc\" image=\"image/image:1\"",
				LastTransitionTime: time.Unix(1, 0),
				Update:             configv1.Update{Version: "1.0.1-abc", Image: "image/image:1"},
			},
		},
		SyncWorkerStatus{
			Actual:       configv1.Release{Version: "1.0.1-abc", Image: "image/image:1"},
			Generation:   1,
			LastProgress: time.Unix(1, 0),
			loadPayloadStatus: LoadPayloadStatus{
				Step:               "RetrievePayload",
				Message:            "Retrieving payload failed version=\"1.0.1-abc\" image=\"image/image:1\" failure=The update cannot be verified: some random error",
				LastTransitionTime: time.Unix(2, 0),
				Update:             configv1.Update{Version: "1.0.1-abc", Image: "image/image:1"},
				Failure:            payloadErr,
			},
		},
	)
	actions = client.Actions()
	if len(actions) != 2 {
		t.Fatalf("%s", spew.Sdump(actions))
	}
	expectGet(t, actions[0], "clusterversions", "", "version")
	actual := cvs["version"].(*configv1.ClusterVersion)
	expectUpdateStatus(t, actions[1], "clusterversions", "", &configv1.ClusterVersion{
		ObjectMeta: metav1.ObjectMeta{
			Name:            "version",
			ResourceVersion: "1",
			Generation:      1,
		},
		Spec: configv1.ClusterVersionSpec{
			ClusterID:     clusterUID,
			Channel:       "fast",
			DesiredUpdate: &configv1.Update{Version: desired.Version, Image: desired.Image},
		},
		Status: configv1.ClusterVersionStatus{
			// Prefers the image version over the operator's version (although in general they will remain in sync)
			ObservedGeneration: 1,
			Desired:            desired,
			VersionHash:        "DL-FFQ2Uem8=",
			History: []configv1.UpdateHistory{
				{State: configv1.PartialUpdate, Image: "image/image:1", Version: "1.0.1-abc", StartedTime: defaultStartedTime},
				{State: configv1.CompletedUpdate, Image: "image/image:0", Version: "1.0.0-abc", Verified: true, StartedTime: defaultStartedTime, CompletionTime: &defaultCompletionTime},
			},
			Conditions: []configv1.ClusterOperatorStatusCondition{
				{Type: ImplicitlyEnabledCapabilities, Status: "False", Reason: "AsExpected", Message: "Capabilities match configured spec"},
				{Type: DesiredReleaseAccepted, Status: configv1.ConditionFalse, Reason: "RetrievePayload",
					Message: "Retrieving payload failed version=\"1.0.1-abc\" image=\"image/image:1\" failure=The update cannot be verified: some random error"},
				{Type: "Available", Status: "False"},
				{Type: "Failing", Status: "False"},
				{Type: "Progressing", Status: "True", Message: "Working towards 1.0.1-abc"},
				{Type: configv1.RetrievedUpdates, Status: configv1.ConditionFalse},
			},
		},
	})

	// Step 2: Set allowUnverifiedImages to true, trigger a sync and the operator should apply the payload
	//
	// set an update
	copied := configv1.Update{
		Version: desired.Version,
		Image:   desired.Image,
		Force:   true,
	}
	actual.Spec.DesiredUpdate = &copied
	retriever.Set(PayloadInfo{Directory: "testdata/payloadtest-2", VerificationError: payloadErr}, nil)
	//
	// ensure the sync worker tells the sync loop about it
	err = o.sync(ctx, o.queueKey())
	if err != nil {
		t.Fatal(err)
	}

	// wait until we see the new payload show up
	count := 0
	for {
		var status SyncWorkerStatus
		select {
		case status = <-worker.StatusCh():
		case <-time.After(3 * time.Second):
			t.Fatalf("never saw expected retrieve payload event")
		}
		if reflect.DeepEqual(configv1.Release{Version: "1.0.1-abc", Image: "image/image:1"}, status.Actual) {
			break
		}
		t.Logf("Unexpected status waiting to see first retrieve: %#v", status)
		count++
		if count > 8 {
			t.Fatalf("saw too many sync events of the wrong form")
		}
	}
	// wait until the new payload is applied
	count = 0
	for {
		var status SyncWorkerStatus
		select {
		case status = <-worker.StatusCh():
		case <-time.After(3 * time.Second):
			t.Fatalf("never saw expected apply event")
		}
		if status.loadPayloadStatus.Step == "PayloadLoaded" {
			break
		}
		t.Log("Waiting to see step PayloadLoaded")
		count++
		if count > 8 {
			t.Fatalf("saw too many sync events of the wrong form")
		}
	}
	verifyAllStatus(t, worker.StatusCh(),
		SyncWorkerStatus{
			Total:       3,
			VersionHash: "DL-FFQ2Uem8=", Architecture: architecture,
			Actual: configv1.Release{
				Version: "1.0.1-abc",
				Image:   "image/image:1",
				URL:     "https://example.com/v1.0.1-abc",
			},
			Generation: 1,
			CapabilitiesStatus: CapabilityStatus{
				Status: configv1.ClusterVersionCapabilitiesStatus{
					EnabledCapabilities: sortedCaps,
					KnownCapabilities:   sortedKnownCaps,
				},
			},
			loadPayloadStatus: LoadPayloadStatus{
				Step:               "PayloadLoaded",
				Message:            "Payload loaded version=\"1.0.1-abc\" image=\"image/image:1\" architecture=\"" + architecture + "\"",
				AcceptedRisks:      "The update cannot be verified: some random error",
				LastTransitionTime: time.Unix(1, 0),
				Update:             configv1.Update{Version: "1.0.1-abc", Image: "image/image:1", Force: true},
			},
		},
	)

	// wait for status to reflect sync of new payload
	waitForStatusCompleted(t, worker)

	client.ClearActions()
	err = o.sync(ctx, o.queueKey())
	if err != nil {
		t.Fatal(err)
	}
	actions = client.Actions()
	if len(actions) != 2 {
		t.Fatalf("%s", spew.Sdump(actions))
	}
	expectGet(t, actions[0], "clusterversions", "", "version")
	expectUpdateStatus(t, actions[1], "clusterversions", "", &configv1.ClusterVersion{
		ObjectMeta: metav1.ObjectMeta{
			Name:            "version",
			ResourceVersion: "3",
			Generation:      1,
		},
		Spec: configv1.ClusterVersionSpec{
			ClusterID:     actual.Spec.ClusterID,
			Channel:       "fast",
			DesiredUpdate: &copied,
		},
		Status: configv1.ClusterVersionStatus{
			ObservedGeneration: 1,
			Desired: configv1.Release{
				Version: "1.0.1-abc",
				Image:   "image/image:1",
				URL:     "https://example.com/v1.0.1-abc",
			},
			VersionHash: "DL-FFQ2Uem8=",
			Capabilities: configv1.ClusterVersionCapabilitiesStatus{
				EnabledCapabilities: sortedCaps,
				KnownCapabilities:   sortedKnownCaps,
			},
			History: []configv1.UpdateHistory{
				{State: configv1.CompletedUpdate, Image: "image/image:1", Version: "1.0.1-abc", StartedTime: defaultStartedTime, CompletionTime: &defaultCompletionTime, AcceptedRisks: "The update cannot be verified: some random error"},
				{State: configv1.CompletedUpdate, Image: "image/image:0", Version: "1.0.0-abc", Verified: true, StartedTime: defaultStartedTime, CompletionTime: &defaultCompletionTime},
			},
			Conditions: []configv1.ClusterOperatorStatusCondition{
				{Type: ImplicitlyEnabledCapabilities, Status: "False", Reason: "AsExpected", Message: "Capabilities match configured spec"},
				{Type: DesiredReleaseAccepted, Status: configv1.ConditionTrue, Reason: "PayloadLoaded",
					Message: "Payload loaded version=\"1.0.1-abc\" image=\"image/image:1\" architecture=\"" + architecture + "\""},
				{Type: configv1.OperatorAvailable, Status: configv1.ConditionTrue, Message: "Done applying 1.0.1-abc"},
				{Type: ClusterStatusFailing, Status: configv1.ConditionFalse},
				{Type: configv1.OperatorProgressing, Status: configv1.ConditionFalse, Message: "Cluster version is 1.0.1-abc"},
				{Type: configv1.RetrievedUpdates, Status: configv1.ConditionFalse},
			},
		},
	})
}

func TestCVO_ResetPayloadLoadStatus(t *testing.T) {
	o, cvs, client, _, shutdownFn := setupCVOTest("testdata/payloadtest-2")

	// Setup: a successful sync from a previous run, and the operator at the same image as before
	//
	o.release.Image = "image/image:0"
	o.release.Version = "1.0.0-abc"
	desired := configv1.Release{Version: "1.0.1-abc", Image: "image/image:1"}
	uid, _ := uuid.NewRandom()
	clusterUID := configv1.ClusterID(uid.String())
	cvs["version"] = &configv1.ClusterVersion{
		ObjectMeta: metav1.ObjectMeta{
			Name:            "version",
			ResourceVersion: "1",
			Generation:      1,
		},
		Spec: configv1.ClusterVersionSpec{
			ClusterID:     clusterUID,
			Channel:       "fast",
			DesiredUpdate: &configv1.Update{Version: desired.Version, Image: desired.Image},
		},
		Status: configv1.ClusterVersionStatus{
			// Prefers the image version over the operator's version (although in general they will remain in sync)
			Desired:     desired,
			VersionHash: "DL-FFQ2Uem8=",
			History: []configv1.UpdateHistory{
				{State: configv1.CompletedUpdate, Image: "image/image:0", Version: "1.0.0-abc", Verified: true, StartedTime: defaultStartedTime, CompletionTime: &defaultCompletionTime},
			},
		},
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	defer shutdownFn()

	// make the image report unverified
	payloadErr := &payload.UpdateError{
		Reason:  "ImageVerificationFailed",
		Message: "The update cannot be verified: some random error",
		Nested:  fmt.Errorf("some random error"),
	}
	if !isImageVerificationError(payloadErr) {
		t.Fatal("not the correct error type")
	}
	worker := o.configSync.(*SyncWorker)
	worker.initializedFunc = func() bool { return true }
	// checked by SyncWorker.syncPayload
	worker.payload = &payload.Update{Release: o.release}

	retriever := worker.retriever.(*fakeDirectoryRetriever)
	retriever.Set(PayloadInfo{}, payloadErr)

	go worker.Start(ctx, 1)

	// Step 1: The operator should report that it is blocked on unverified content
	//
	client.ClearActions()
	err := o.sync(ctx, o.queueKey())
	if err != nil {
		t.Fatal(err)
	}
	actions := client.Actions()
	verifyCVSingleUpdate(t, actions)

	verifyAllStatus(t, worker.StatusCh(),
		SyncWorkerStatus{
			Actual:     configv1.Release{Version: "1.0.1-abc", Image: "image/image:1"},
			Generation: 1,
			loadPayloadStatus: LoadPayloadStatus{
				Step:               "RetrievePayload",
				Message:            "Retrieving and verifying payload version=\"1.0.1-abc\" image=\"image/image:1\"",
				LastTransitionTime: time.Unix(1, 0),
				Update:             configv1.Update{Version: "1.0.1-abc", Image: "image/image:1"},
			},
		},
		SyncWorkerStatus{
			Actual:       configv1.Release{Version: "1.0.1-abc", Image: "image/image:1"},
			Generation:   1,
			LastProgress: time.Unix(1, 0),
			loadPayloadStatus: LoadPayloadStatus{
				Step:               "RetrievePayload",
				Message:            "Retrieving payload failed version=\"1.0.1-abc\" image=\"image/image:1\" failure=The update cannot be verified: some random error",
				LastTransitionTime: time.Unix(2, 0),
				Update:             configv1.Update{Version: "1.0.1-abc", Image: "image/image:1"},
				Failure:            payloadErr,
			},
		},
	)
	actions = client.Actions()
	if len(actions) != 2 {
		t.Fatalf("%s", spew.Sdump(actions))
	}
	expectGet(t, actions[0], "clusterversions", "", "version")
	actual := cvs["version"].(*configv1.ClusterVersion)
	expectUpdateStatus(t, actions[1], "clusterversions", "", &configv1.ClusterVersion{
		ObjectMeta: metav1.ObjectMeta{
			Name:            "version",
			ResourceVersion: "1",
			Generation:      1,
		},
		Spec: configv1.ClusterVersionSpec{
			ClusterID:     clusterUID,
			Channel:       "fast",
			DesiredUpdate: &configv1.Update{Version: desired.Version, Image: desired.Image},
		},
		Status: configv1.ClusterVersionStatus{
			// Prefers the image version over the operator's version (although in general they will remain in sync)
			ObservedGeneration: 1,
			Desired:            desired,
			VersionHash:        "DL-FFQ2Uem8=",
			History: []configv1.UpdateHistory{
				{State: configv1.PartialUpdate, Image: "image/image:1", Version: "1.0.1-abc", StartedTime: defaultStartedTime},
				{State: configv1.CompletedUpdate, Image: "image/image:0", Version: "1.0.0-abc", Verified: true, StartedTime: defaultStartedTime, CompletionTime: &defaultCompletionTime},
			},
			Conditions: []configv1.ClusterOperatorStatusCondition{
				{Type: ImplicitlyEnabledCapabilities, Status: "False", Reason: "AsExpected", Message: "Capabilities match configured spec"},
				{Type: DesiredReleaseAccepted, Status: configv1.ConditionFalse, Reason: "RetrievePayload",
					Message: "Retrieving payload failed version=\"1.0.1-abc\" image=\"image/image:1\" failure=The update cannot be verified: some random error"},
				{Type: "Available", Status: "False"},
				{Type: "Failing", Status: "False"},
				{Type: "Progressing", Status: "True", Message: "Working towards 1.0.1-abc"},
				{Type: configv1.RetrievedUpdates, Status: configv1.ConditionFalse},
			},
		},
	})

	// Step 2: Set desired to original loaded image as if upgrade attempt was cleared. Payload status should be reset
	// to originally loaded image.
	//
	desired = configv1.Release{Version: "1.0.0-abc", Image: "image/image:0"}
	cvs["version"] = &configv1.ClusterVersion{
		ObjectMeta: metav1.ObjectMeta{
			Name:            "version",
			ResourceVersion: "2",
			Generation:      1,
		},
		Spec: configv1.ClusterVersionSpec{
			ClusterID:     clusterUID,
			Channel:       "fast",
			DesiredUpdate: &configv1.Update{Version: desired.Version, Image: desired.Image},
		},
		Status: configv1.ClusterVersionStatus{
			// Prefers the image version over the operator's version (although in general they will remain in sync)
			Desired:     desired,
			VersionHash: "DL-FFQ2Uem8=",
			History: []configv1.UpdateHistory{
				{State: configv1.CompletedUpdate, Image: "image/image:0", Version: "1.0.0-abc", Verified: true, StartedTime: defaultStartedTime, CompletionTime: &defaultCompletionTime},
			},
		},
	}
	copied := configv1.Update{
		Version: desired.Version,
		Image:   desired.Image,
	}
	actual.Spec.DesiredUpdate = &copied

	//
	// ensure the sync worker tells the sync loop about it
	err = o.sync(ctx, o.queueKey())
	if err != nil {
		t.Fatal(err)
	}

	// wait until payload status is reset
	count := 0
	for {
		var status SyncWorkerStatus
		select {
		case status = <-worker.StatusCh():
		case <-time.After(3 * time.Second):
			t.Fatalf("never saw expected apply event")
		}
		if status.loadPayloadStatus.Step == "PayloadLoaded" {
			break
		}
		t.Log("Waiting to see step PayloadLoaded")
		count++
		if count > 8 {
			t.Fatalf("saw too many sync events of the wrong form")
		}
	}
	verifyAllStatus(t, worker.StatusCh(),
		SyncWorkerStatus{
			Completed:   1,
			Reconciling: true,
			Actual: configv1.Release{
				Version: "1.0.0-abc",
				Image:   "image/image:0",
			},
			Generation:   1,
			LastProgress: time.Unix(1, 0),
			CapabilitiesStatus: CapabilityStatus{
				Status: configv1.ClusterVersionCapabilitiesStatus{
					EnabledCapabilities: sortedCaps,
					KnownCapabilities:   sortedKnownCaps,
				},
			},
			loadPayloadStatus: LoadPayloadStatus{
				Step:               "PayloadLoaded",
				Message:            "Payload loaded version=\"1.0.0-abc\" image=\"image/image:0\"",
				LastTransitionTime: time.Unix(1, 0),
				Update:             configv1.Update{Version: "1.0.0-abc", Image: "image/image:0"},
			},
		},
	)
	actions = client.Actions()
	if len(actions) != 4 {
		t.Fatalf("%s", spew.Sdump(actions))
	}
	expectGet(t, actions[0], "clusterversions", "", "version")
	expectUpdateStatus(t, actions[3], "clusterversions", "", &configv1.ClusterVersion{
		ObjectMeta: metav1.ObjectMeta{
			Name:            "version",
			ResourceVersion: "2",
			Generation:      1,
		},
		Spec: configv1.ClusterVersionSpec{
			ClusterID:     clusterUID,
			Channel:       "fast",
			DesiredUpdate: &configv1.Update{Version: desired.Version, Image: desired.Image},
		},
		Status: configv1.ClusterVersionStatus{
			// Prefers the image version over the operator's version (although in general they will remain in sync)
			ObservedGeneration: 1,
			Desired:            desired,
			VersionHash:        "DL-FFQ2Uem8=",
			Capabilities: configv1.ClusterVersionCapabilitiesStatus{
				EnabledCapabilities: sortedCaps,
				KnownCapabilities:   sortedKnownCaps,
			},
			History: []configv1.UpdateHistory{
				{State: configv1.CompletedUpdate, Image: "image/image:0", Version: "1.0.0-abc", Verified: true, StartedTime: defaultStartedTime, CompletionTime: &defaultCompletionTime},
			},
			Conditions: []configv1.ClusterOperatorStatusCondition{
				{Type: ImplicitlyEnabledCapabilities, Status: "False", Reason: "AsExpected", Message: "Capabilities match configured spec"},
				{Type: DesiredReleaseAccepted, Status: configv1.ConditionTrue, Reason: "PayloadLoaded",
					Message: "Payload loaded version=\"1.0.0-abc\" image=\"image/image:0\""},
				{Type: "Available", Status: "False"},
				{Type: "Failing", Status: "False"},
				{Type: "Progressing", Status: "False", Message: "Cluster version is 1.0.0-abc"},
				{Type: configv1.RetrievedUpdates, Status: configv1.ConditionFalse},
			},
		},
	})
}

func TestCVO_UpgradeFailedPayloadLoadWithCapsChanges(t *testing.T) {
	o, cvs, client, _, shutdownFn := setupCVOTest("testdata/payloadtest-3")

	// Setup: load and apply payload which sets "work" to non-nil
	//
	o.release.Image = "image/image:0"
	o.release.Version = "1.0.0-abc"
	desired := configv1.Release{Version: "1.0.1-abc", Image: "image/image:1"}
	uid, _ := uuid.NewRandom()
	clusterUID := configv1.ClusterID(uid.String())
	cvs["version"] = &configv1.ClusterVersion{
		ObjectMeta: metav1.ObjectMeta{
			Name:            "version",
			ResourceVersion: "1",
			Generation:      1,
		},
		Spec: configv1.ClusterVersionSpec{
			ClusterID:     clusterUID,
			Channel:       "fast",
			DesiredUpdate: &configv1.Update{Version: desired.Version, Image: desired.Image},
			Capabilities: &configv1.ClusterVersionCapabilitiesSpec{
				BaselineCapabilitySet:         configv1.ClusterVersionCapabilitySetNone,
				AdditionalEnabledCapabilities: []configv1.ClusterVersionCapability{configv1.ClusterVersionCapabilityBaremetal},
			},
		},
		Status: configv1.ClusterVersionStatus{
			// Prefers the image version over the operator's version (although in general they will remain in sync)
			Desired:     desired,
			VersionHash: "Y9500_0QNis=",
			History: []configv1.UpdateHistory{
				{State: configv1.CompletedUpdate, Image: "image/image:0", Version: "1.0.0-abc", Verified: true, StartedTime: defaultStartedTime, CompletionTime: &defaultCompletionTime},
			},
		},
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	defer shutdownFn()

	// make the image report unverified
	payloadErr := &payload.UpdateError{
		Reason:  "ImageVerificationFailed",
		Message: "The update cannot be verified: some random error",
		Nested:  fmt.Errorf("some random error"),
	}
	if !isImageVerificationError(payloadErr) {
		t.Fatal("not the correct error type")
	}
	worker := o.configSync.(*SyncWorker)
	worker.initializedFunc = func() bool { return true }
	retriever := worker.retriever.(*fakeDirectoryRetriever)
	retriever.Set(PayloadInfo{}, payloadErr)

	go worker.Start(ctx, 1)

	copied := configv1.Update{
		Version: desired.Version,
		Image:   desired.Image,
		Force:   true,
	}
	actual := cvs["version"].(*configv1.ClusterVersion)
	actual.Spec.DesiredUpdate = &copied
	retriever.Set(PayloadInfo{Directory: "testdata/payloadtest-3", VerificationError: payloadErr}, nil)
	//
	// ensure the sync worker tells the sync loop about it
	err := o.sync(ctx, o.queueKey())
	if err != nil {
		t.Fatal(err)
	}
	waitForStatus(t, 8, 3, worker.StatusCh(), waitForPayloadLoaded)
	clearAllStatusWithWait(t, "init", 3, worker.StatusCh())

	// Step 1: Attempt to load another payload which will fail. The operator should still save capability changes and reflect in status.
	//
	// set an update
	o.release.Image = "image/image:0"
	o.release.Version = "1.0.0-abc"
	desired = configv1.Release{Version: "1.0.0-abc", Image: "image/image:0"}
	copied = configv1.Update{
		Version: desired.Version,
		Image:   desired.Image,
	}
	actual.Spec.DesiredUpdate = &copied
	client.ClearActions()
	cvs["version"] = &configv1.ClusterVersion{
		ObjectMeta: metav1.ObjectMeta{
			Name:            "version",
			ResourceVersion: "3",
			Generation:      1,
		},
		Spec: configv1.ClusterVersionSpec{
			ClusterID:     clusterUID,
			Channel:       "fast",
			DesiredUpdate: &configv1.Update{Version: desired.Version, Image: desired.Image},
			Capabilities: &configv1.ClusterVersionCapabilitiesSpec{
				BaselineCapabilitySet:         configv1.ClusterVersionCapabilitySetNone,
				AdditionalEnabledCapabilities: []configv1.ClusterVersionCapability{configv1.ClusterVersionCapabilityBaremetal, configv1.ClusterVersionCapabilityMarketplace},
			},
		},
		Status: configv1.ClusterVersionStatus{
			// Prefers the image version over the operator's version (although in general they will remain in sync)
			Desired:     desired,
			VersionHash: "Y9500_0QNis=",
			History: []configv1.UpdateHistory{
				{State: configv1.CompletedUpdate, Image: "image/image:0", Version: "1.0.0-abc", Verified: true, StartedTime: defaultStartedTime, CompletionTime: &defaultCompletionTime},
			},
		},
	}
	err = o.sync(ctx, o.queueKey())
	if err != nil {
		t.Fatal(err)
	}
	// wait until the new payload load fails
	waitForStatus(t, 8, 3, worker.StatusCh(), waitForVerifyPayload)
	actions := client.Actions()

	// confirm capabilities are updated
	checkStatus(t, actions[1], "update", "clusterversions", "status", "", configv1.ClusterVersionCapabilitiesStatus{
		EnabledCapabilities: []configv1.ClusterVersionCapability{configv1.ClusterVersionCapabilityIngress, configv1.ClusterVersionCapabilityBaremetal, configv1.ClusterVersionCapabilityMarketplace},
		KnownCapabilities:   sortedKnownCaps,
	})
}

func TestCVO_InitImplicitlyEnabledCaps(t *testing.T) {
	o, cvs, client, _, shutdownFn := setupCVOTest("testdata/payloadtest-2")

	// Setup: a successful sync from a previous run, and the operator at the same image as before
	//
	o.release.Image = "image/image:0"
	o.release.Version = "1.0.0-abc"
	desired := configv1.Release{Version: "1.0.1-abc", Image: "image/image:1"}
	uid, _ := uuid.NewRandom()
	clusterUID := configv1.ClusterID(uid.String())
	cvs["version"] = &configv1.ClusterVersion{
		ObjectMeta: metav1.ObjectMeta{
			Name:            "version",
			ResourceVersion: "1",
			Generation:      1,
		},
		Spec: configv1.ClusterVersionSpec{
			ClusterID:     clusterUID,
			Channel:       "fast",
			DesiredUpdate: &configv1.Update{Version: desired.Version, Image: desired.Image},
			Capabilities: &configv1.ClusterVersionCapabilitiesSpec{
				BaselineCapabilitySet:         configv1.ClusterVersionCapabilitySetNone,
				AdditionalEnabledCapabilities: []configv1.ClusterVersionCapability{configv1.ClusterVersionCapabilityBaremetal},
			},
		},
		Status: configv1.ClusterVersionStatus{
			// Prefers the image version over the operator's version (although in general they will remain in sync)
			Desired:     desired,
			VersionHash: "DL-FFQ2Uem8=",
			History: []configv1.UpdateHistory{
				{State: configv1.CompletedUpdate, Image: "image/image:0", Version: "1.0.0-abc", Verified: true, StartedTime: defaultStartedTime, CompletionTime: &defaultCompletionTime},
			},
			// Emulates capabilities status set by a previous pod
			Capabilities: configv1.ClusterVersionCapabilitiesStatus{
				EnabledCapabilities: sortedCaps,
				KnownCapabilities:   []configv1.ClusterVersionCapability{configv1.ClusterVersionCapabilityBaremetal, configv1.ClusterVersionCapabilityMarketplace, configv1.ClusterVersionCapabilityOpenShiftSamples, configv1.ClusterVersionCapabilityOpenShiftSamples},
			},
		},
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	defer shutdownFn()
	worker := o.configSync.(*SyncWorker)
	worker.initializedFunc = func() bool { return true }

	go worker.Start(ctx, 1)

	// Step 1: The operator should report that it is blocked on unverified content
	//
	err := o.sync(ctx, o.queueKey())
	if err != nil {
		t.Fatal(err)
	}
	// wait until we see the new payload show up
	count := 0
	for {
		var status SyncWorkerStatus
		select {
		case status = <-worker.StatusCh():
		case <-time.After(3 * time.Second):
			t.Fatalf("never saw expected retrieve payload event")
		}
		if reflect.DeepEqual(configv1.Release{Version: "1.0.1-abc", Image: "image/image:1"}, status.Actual) {
			break
		}
		t.Logf("Unexpected status waiting to see first retrieve: %#v", status)
		count++
		if count > 8 {
			t.Fatalf("saw too many sync events of the wrong form")
		}
	}
	// wait until the new payload is applied
	count = 0
	for {
		var status SyncWorkerStatus
		select {
		case status = <-worker.StatusCh():
		case <-time.After(3 * time.Second):
			t.Fatalf("never saw expected apply event")
		}
		if status.loadPayloadStatus.Step == "PayloadLoaded" {
			break
		}
		t.Log("Waiting to see step PayloadLoaded")
		count++
		if count > 8 {
			t.Fatalf("saw too many sync events of the wrong form")
		}
	}

	verifyAllStatus(t, worker.StatusCh(),
		SyncWorkerStatus{
			Actual:      configv1.Release{Version: "1.0.1-abc", Image: "image/image:1", URL: "https://example.com/v1.0.1-abc"},
			Generation:  1,
			Total:       3,
			VersionHash: "DL-FFQ2Uem8=", Architecture: architecture,
			CapabilitiesStatus: CapabilityStatus{
				Status: configv1.ClusterVersionCapabilitiesStatus{
					EnabledCapabilities: sortedCaps,
					KnownCapabilities:   sortedKnownCaps,
				},
				ImplicitlyEnabledCaps: []configv1.ClusterVersionCapability{configv1.ClusterVersionCapabilityBuild, configv1.ClusterVersionCapabilityCSISnapshot, configv1.ClusterVersionCapabilityCloudControllerManager, configv1.ClusterVersionCapabilityCloudCredential, configv1.ClusterVersionCapabilityConsole, configv1.ClusterVersionCapabilityDeploymentConfig, configv1.ClusterVersionCapabilityImageRegistry, configv1.ClusterVersionCapabilityIngress, configv1.ClusterVersionCapabilityInsights, configv1.ClusterVersionCapabilityMachineAPI, configv1.ClusterVersionCapabilityNodeTuning, configv1.ClusterVersionCapabilityOperatorLifecycleManager, configv1.ClusterVersionCapabilityStorage, configv1.ClusterVersionCapabilityMarketplace, configv1.ClusterVersionCapabilityOpenShiftSamples},
			},
			loadPayloadStatus: LoadPayloadStatus{
				Step:               "PayloadLoaded",
				Message:            "Payload loaded version=\"1.0.1-abc\" image=\"image/image:1\" architecture=\"" + architecture + "\"",
				LastTransitionTime: time.Unix(1, 0),
				Update:             configv1.Update{Version: "1.0.1-abc", Image: "image/image:1"},
			},
		},
	)
	actions := client.Actions()
	if len(actions) != 3 {
		t.Fatalf("%s", spew.Sdump(actions))
	}
	expectGet(t, actions[1], "clusterversions", "", "version")
	expectFinalUpdateStatus(t, actions, "clusterversions", "", &configv1.ClusterVersion{
		ObjectMeta: metav1.ObjectMeta{
			Name:            "version",
			ResourceVersion: "1",
			Generation:      1,
		},
		Spec: configv1.ClusterVersionSpec{
			ClusterID:     clusterUID,
			Channel:       "fast",
			DesiredUpdate: &configv1.Update{Version: desired.Version, Image: desired.Image},
			Capabilities: &configv1.ClusterVersionCapabilitiesSpec{
				BaselineCapabilitySet:         configv1.ClusterVersionCapabilitySetNone,
				AdditionalEnabledCapabilities: []configv1.ClusterVersionCapability{configv1.ClusterVersionCapabilityBaremetal},
			},
		},
		Status: configv1.ClusterVersionStatus{
			// Prefers the image version over the operator's version (although in general they will remain in sync)
			ObservedGeneration: 1,
			Desired:            desired,
			VersionHash:        "DL-FFQ2Uem8=",
			History: []configv1.UpdateHistory{
				{State: configv1.PartialUpdate, Image: "image/image:1", Version: "1.0.1-abc", StartedTime: defaultStartedTime},
				{State: configv1.CompletedUpdate, Image: "image/image:0", Version: "1.0.0-abc", Verified: true, StartedTime: defaultStartedTime, CompletionTime: &defaultCompletionTime},
			},
			Capabilities: configv1.ClusterVersionCapabilitiesStatus{
				EnabledCapabilities: sortedCaps,
				KnownCapabilities:   sortedKnownCaps,
			},
			Conditions: []configv1.ClusterOperatorStatusCondition{
				{Type: ImplicitlyEnabledCapabilities, Status: configv1.ConditionTrue, Reason: "CapabilitiesImplicitlyEnabled", Message: "The following capabilities could not be disabled: Build, CSISnapshot, CloudControllerManager, CloudCredential, Console, DeploymentConfig, ImageRegistry, Ingress, Insights, MachineAPI, NodeTuning, OperatorLifecycleManager, Storage, marketplace, openshift-samples"},
				{Type: DesiredReleaseAccepted, Status: configv1.ConditionTrue, Reason: "PayloadLoaded",
					Message: "Payload loaded version=\"1.0.1-abc\" image=\"image/image:1\" architecture=\"" + architecture + "\""},
				{Type: "Available", Status: "False"},
				{Type: "Failing", Status: "False"},
				{Type: "Progressing", Status: "True", Message: "Working towards 1.0.1-abc"},
				{Type: configv1.RetrievedUpdates, Status: configv1.ConditionFalse},
			},
		},
	})
}

func TestCVO_UpgradeUnverifiedPayloadRetrieveOnce(t *testing.T) {
	o, cvs, client, _, shutdownFn := setupCVOTest("testdata/payloadtest-2")

	// Setup: a successful sync from a previous run, and the operator at the same image as before
	//
	o.release.Image = "image/image:0"
	o.release.Version = "1.0.0-abc"
	desired := configv1.Release{Version: "1.0.1-abc", Image: "image/image:1"}
	uid, _ := uuid.NewRandom()
	clusterUID := configv1.ClusterID(uid.String())
	cvs["version"] = &configv1.ClusterVersion{
		ObjectMeta: metav1.ObjectMeta{
			Name:            "version",
			ResourceVersion: "1",
			Generation:      1,
		},
		Spec: configv1.ClusterVersionSpec{
			ClusterID:     clusterUID,
			Channel:       "fast",
			DesiredUpdate: &configv1.Update{Version: desired.Version, Image: desired.Image},
		},
		Status: configv1.ClusterVersionStatus{
			// Prefers the image version over the operator's version (although in general they will remain in sync)
			Desired:     desired,
			VersionHash: "DL-FFQ2Uem8=",
			History: []configv1.UpdateHistory{
				{State: configv1.CompletedUpdate, Image: "image/image:0", Version: "1.0.0-abc", Verified: true, StartedTime: defaultStartedTime, CompletionTime: &defaultCompletionTime},
			},
			Conditions: []configv1.ClusterOperatorStatusCondition{
				{Type: ImplicitlyEnabledCapabilities, Status: "False", Reason: "AsExpected", Message: "Capabilities match configured spec"},
				{Type: configv1.OperatorAvailable, Status: configv1.ConditionTrue, Message: "Done applying 1.0.0-abc"},
				{Type: ClusterStatusFailing, Status: configv1.ConditionFalse},
				{Type: configv1.OperatorProgressing, Status: configv1.ConditionFalse, Message: "Cluster version is 1.0.0-abc"},
				{Type: configv1.RetrievedUpdates, Status: configv1.ConditionFalse},
			},
		},
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	defer shutdownFn()

	// make the image report unverified
	payloadErr := &payload.UpdateError{
		Reason:  "ImageVerificationFailed",
		Message: "The update cannot be verified: some random error",
		Nested:  fmt.Errorf("some random error"),
	}
	if !isImageVerificationError(payloadErr) {
		t.Fatal("not the correct error type")
	}
	worker := o.configSync.(*SyncWorker)
	worker.initializedFunc = func() bool { return true }
	retriever := worker.retriever.(*fakeDirectoryRetriever)
	retriever.Set(PayloadInfo{}, payloadErr)

	go worker.Start(ctx, 1)

	// Step 1: The operator should report that it is blocked on unverified content
	//
	client.ClearActions()
	err := o.sync(ctx, o.queueKey())
	if err != nil {
		t.Fatal(err)
	}
	actions := client.Actions()
	verifyCVSingleUpdate(t, actions)

	verifyAllStatus(t, worker.StatusCh(),
		SyncWorkerStatus{
			Actual:     configv1.Release{Version: "1.0.1-abc", Image: "image/image:1"},
			Generation: 1,
			loadPayloadStatus: LoadPayloadStatus{
				Step:               "RetrievePayload",
				Message:            "Retrieving and verifying payload version=\"1.0.1-abc\" image=\"image/image:1\"",
				LastTransitionTime: time.Unix(1, 0),
				Update:             configv1.Update{Version: "1.0.1-abc", Image: "image/image:1"},
			},
		},
		SyncWorkerStatus{
			Actual:       configv1.Release{Version: "1.0.1-abc", Image: "image/image:1"},
			Generation:   1,
			LastProgress: time.Unix(1, 0),
			loadPayloadStatus: LoadPayloadStatus{
				Step:               "RetrievePayload",
				Message:            "Retrieving payload failed version=\"1.0.1-abc\" image=\"image/image:1\" failure=The update cannot be verified: some random error",
				LastTransitionTime: time.Unix(2, 0),
				Update:             configv1.Update{Version: "1.0.1-abc", Image: "image/image:1"},
				Failure:            payloadErr,
			},
		},
	)
	actions = client.Actions()
	if len(actions) != 2 {
		t.Fatalf("%s", spew.Sdump(actions))
	}
	expectGet(t, actions[0], "clusterversions", "", "version")
	actual := cvs["version"].(*configv1.ClusterVersion)
	expectUpdateStatus(t, actions[1], "clusterversions", "", &configv1.ClusterVersion{
		ObjectMeta: metav1.ObjectMeta{
			Name:            "version",
			ResourceVersion: "1",
			Generation:      1,
		},
		Spec: configv1.ClusterVersionSpec{
			ClusterID:     clusterUID,
			Channel:       "fast",
			DesiredUpdate: &configv1.Update{Version: desired.Version, Image: desired.Image},
		},
		Status: configv1.ClusterVersionStatus{
			// Prefers the image version over the operator's version (although in general they will remain in sync)
			ObservedGeneration: 1,
			Desired:            desired,
			VersionHash:        "DL-FFQ2Uem8=",
			History: []configv1.UpdateHistory{
				{State: configv1.PartialUpdate, Image: "image/image:1", Version: "1.0.1-abc", StartedTime: defaultStartedTime},
				{State: configv1.CompletedUpdate, Image: "image/image:0", Version: "1.0.0-abc", Verified: true, StartedTime: defaultStartedTime, CompletionTime: &defaultCompletionTime},
			},
			Conditions: []configv1.ClusterOperatorStatusCondition{
				{Type: ImplicitlyEnabledCapabilities, Status: "False", Reason: "AsExpected", Message: "Capabilities match configured spec"},
				{Type: configv1.OperatorAvailable, Status: configv1.ConditionTrue, Message: "Done applying 1.0.0-abc"},
				// cleared failing status and set progressing
				{Type: ClusterStatusFailing, Status: configv1.ConditionFalse},
				{Type: configv1.OperatorProgressing, Status: configv1.ConditionTrue, Message: "Working towards 1.0.1-abc"},
				{Type: configv1.RetrievedUpdates, Status: configv1.ConditionFalse},
				{Type: DesiredReleaseAccepted, Status: configv1.ConditionFalse, Reason: "RetrievePayload",
					Message: "Retrieving payload failed version=\"1.0.1-abc\" image=\"image/image:1\" failure=The update cannot be verified: some random error"},
			},
		},
	})

	// Step 2: Set allowUnverifiedImages to true, trigger a sync and the operator should apply the payload
	//
	// set an update
	copied := configv1.Update{
		Version: desired.Version,
		Image:   desired.Image,
		Force:   true,
	}
	actual.Spec.DesiredUpdate = &copied
	retriever.Set(PayloadInfo{Directory: "testdata/payloadtest-2", VerificationError: payloadErr}, nil)
	//
	// ensure the sync worker tells the sync loop about it
	err = o.sync(ctx, o.queueKey())
	if err != nil {
		t.Fatal(err)
	}

	// wait until we see the new payload show up
	count := 0
	for {
		var status SyncWorkerStatus
		select {
		case status = <-worker.StatusCh():
		case <-time.After(3 * time.Second):
			t.Fatalf("never saw expected retrieve payload event")
		}
		if reflect.DeepEqual(configv1.Release{Version: "1.0.1-abc", Image: "image/image:1"}, status.Actual) {
			break
		}
		t.Logf("Unexpected status waiting to see first retrieve: %#v", status)
		count++
		if count > 8 {
			t.Fatalf("saw too many sync events of the wrong form")
		}
	}
	// wait until the new payload is applied
	count = 0
	for {
		var status SyncWorkerStatus
		select {
		case status = <-worker.StatusCh():
		case <-time.After(3 * time.Second):
			t.Fatalf("never saw expected apply event")
		}
		if status.loadPayloadStatus.Step == "PayloadLoaded" {
			break
		}
		t.Log("Waiting to see step PayloadLoaded")
		count++
		if count > 8 {
			t.Fatalf("saw too many sync events of the wrong form")
		}
	}
	verifyAllStatus(t, worker.StatusCh(),
		SyncWorkerStatus{
			Total:       3,
			VersionHash: "DL-FFQ2Uem8=", Architecture: architecture,
			Actual: configv1.Release{
				Version: "1.0.1-abc",
				Image:   "image/image:1",
				URL:     "https://example.com/v1.0.1-abc",
			},
			Generation: 1,
			CapabilitiesStatus: CapabilityStatus{
				Status: configv1.ClusterVersionCapabilitiesStatus{
					EnabledCapabilities: sortedCaps,
					KnownCapabilities:   sortedKnownCaps,
				},
			},
			loadPayloadStatus: LoadPayloadStatus{
				Step:               "PayloadLoaded",
				Message:            "Payload loaded version=\"1.0.1-abc\" image=\"image/image:1\" architecture=\"" + architecture + "\"",
				AcceptedRisks:      "The update cannot be verified: some random error",
				LastTransitionTime: time.Unix(1, 0),
				Update:             configv1.Update{Version: "1.0.1-abc", Image: "image/image:1", Force: true},
			},
		},
	)

	// wait for status to reflect sync of new payload
	waitForStatusCompleted(t, worker)

	client.ClearActions()
	err = o.sync(ctx, o.queueKey())
	if err != nil {
		t.Fatal(err)
	}
	actions = client.Actions()
	if len(actions) != 2 {
		t.Fatalf("%s", spew.Sdump(actions))
	}
	expectGet(t, actions[0], "clusterversions", "", "version")

	expectUpdateStatus(t, actions[1], "clusterversions", "", &configv1.ClusterVersion{
		ObjectMeta: metav1.ObjectMeta{
			Name:            "version",
			ResourceVersion: "3",
			Generation:      1,
		},
		Spec: configv1.ClusterVersionSpec{
			ClusterID:     actual.Spec.ClusterID,
			Channel:       "fast",
			DesiredUpdate: &copied,
		},
		Status: configv1.ClusterVersionStatus{
			ObservedGeneration: 1,
			Desired: configv1.Release{
				Version: "1.0.1-abc",
				Image:   "image/image:1",
				URL:     "https://example.com/v1.0.1-abc",
			},
			VersionHash: "DL-FFQ2Uem8=",
			Capabilities: configv1.ClusterVersionCapabilitiesStatus{
				EnabledCapabilities: sortedCaps,
				KnownCapabilities:   sortedKnownCaps,
			},
			History: []configv1.UpdateHistory{
				{State: configv1.CompletedUpdate, Image: "image/image:1", Version: "1.0.1-abc", StartedTime: defaultStartedTime, CompletionTime: &defaultCompletionTime, AcceptedRisks: "The update cannot be verified: some random error"},
				{State: configv1.CompletedUpdate, Image: "image/image:0", Version: "1.0.0-abc", Verified: true, StartedTime: defaultStartedTime, CompletionTime: &defaultCompletionTime},
			},
			Conditions: []configv1.ClusterOperatorStatusCondition{
				{Type: ImplicitlyEnabledCapabilities, Status: "False", Reason: "AsExpected", Message: "Capabilities match configured spec"},
				{Type: configv1.OperatorAvailable, Status: configv1.ConditionTrue, Message: "Done applying 1.0.1-abc"},
				{Type: ClusterStatusFailing, Status: configv1.ConditionFalse},
				{Type: configv1.OperatorProgressing, Status: configv1.ConditionFalse, Message: "Cluster version is 1.0.1-abc"},
				{Type: configv1.RetrievedUpdates, Status: configv1.ConditionFalse},
				{Type: DesiredReleaseAccepted, Status: configv1.ConditionTrue, Reason: "PayloadLoaded",
					Message: "Payload loaded version=\"1.0.1-abc\" image=\"image/image:1\" architecture=\"" + architecture + "\""},
			},
		},
	})

	// Step 5: Wait for the SyncWorker to trigger a reconcile (500ms after the first)
	//
	verifyFinalStatus(t, "Step 5", 10, true, worker.StatusCh(), SyncWorkerStatus{
		Reconciling:  true,
		Completed:    3,
		Done:         3,
		Total:        3,
		VersionHash:  "DL-FFQ2Uem8=",
		Architecture: architecture,
		Actual: configv1.Release{
			Version: "1.0.1-abc",
			Image:   "image/image:1",
			URL:     "https://example.com/v1.0.1-abc",
		},
		LastProgress: time.Unix(7, 0),
		Generation:   1,
		CapabilitiesStatus: CapabilityStatus{
			Status: configv1.ClusterVersionCapabilitiesStatus{
				EnabledCapabilities: sortedCaps,
				KnownCapabilities:   sortedKnownCaps,
			},
		},
		loadPayloadStatus: LoadPayloadStatus{
			Step:               "PayloadLoaded",
			Message:            "Payload loaded version=\"1.0.1-abc\" image=\"image/image:1\" architecture=\"" + architecture + "\"",
			AcceptedRisks:      "The update cannot be verified: some random error",
			LastTransitionTime: time.Unix(4, 0),
			Update:             configv1.Update{Version: "1.0.1-abc", Image: "image/image:1", Force: true},
		},
	},
		finalStatusIndicatorCompleted,
	)
}

func TestCVO_UpgradePreconditionFailing(t *testing.T) {
	o, cvs, client, _, shutdownFn := setupCVOTest("testdata/payloadtest-2")

	// Setup: a successful sync from a previous run, and the operator at the same image as before
	//
	o.release.Image = "image/image:0"
	o.release.Version = "1.0.0-abc"
	desired := configv1.Release{Version: "1.0.1-abc", Image: "image/image:1"}
	uid, _ := uuid.NewRandom()
	clusterUID := configv1.ClusterID(uid.String())
	cvs["version"] = &configv1.ClusterVersion{
		ObjectMeta: metav1.ObjectMeta{
			Name:            "version",
			ResourceVersion: "1",
			Generation:      1,
		},
		Spec: configv1.ClusterVersionSpec{
			ClusterID:     clusterUID,
			Channel:       "fast",
			DesiredUpdate: &configv1.Update{Version: desired.Version, Image: desired.Image},
		},
		Status: configv1.ClusterVersionStatus{
			// Prefers the image version over the operator's version (although in general they will remain in sync)
			Desired:     desired,
			VersionHash: "DL-FFQ2Uem8=",
			History: []configv1.UpdateHistory{
				{State: configv1.CompletedUpdate, Image: "image/image:0", Version: "1.0.0-abc", Verified: true, StartedTime: defaultStartedTime, CompletionTime: &defaultCompletionTime},
			},
			Conditions: []configv1.ClusterOperatorStatusCondition{
				{Type: ImplicitlyEnabledCapabilities, Status: "False", Reason: "AsExpected", Message: "Capabilities match configured spec"},
				{Type: configv1.OperatorAvailable, Status: configv1.ConditionTrue, Message: "Done applying 1.0.0-abc"},
				{Type: ClusterStatusFailing, Status: configv1.ConditionFalse},
				{Type: configv1.OperatorProgressing, Status: configv1.ConditionFalse, Message: "Cluster version is 1.0.0-abc"},
				{Type: configv1.RetrievedUpdates, Status: configv1.ConditionFalse},
			},
		},
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	defer shutdownFn()

	worker := o.configSync.(*SyncWorker)
	worker.initializedFunc = func() bool { return true }
	worker.preconditions = []precondition.Precondition{&testPrecondition{SuccessAfter: 3}}

	go worker.Start(ctx, 1)

	// Step 1: The operator should report that it is blocked on precondition checks failing
	//
	client.ClearActions()
	err := o.sync(ctx, o.queueKey())
	if err != nil {
		t.Fatal(err)
	}
	actions := client.Actions()
	verifyCVSingleUpdate(t, actions)

	verifyAllStatus(t, worker.StatusCh(),
		SyncWorkerStatus{
			Actual:     configv1.Release{Version: "1.0.1-abc", Image: "image/image:1"},
			Generation: 1,
			loadPayloadStatus: LoadPayloadStatus{
				Step:               "RetrievePayload",
				Message:            "Retrieving and verifying payload version=\"1.0.1-abc\" image=\"image/image:1\"",
				LastTransitionTime: time.Unix(1, 0),
				Update:             configv1.Update{Version: "1.0.1-abc", Image: "image/image:1"},
			},
		},
		SyncWorkerStatus{
			Actual:       configv1.Release{Version: "1.0.1-abc", Image: "image/image:1"},
			Generation:   1,
			LastProgress: time.Unix(1, 0),
			loadPayloadStatus: LoadPayloadStatus{
				Step:               "PreconditionChecks",
				Message:            "Preconditions failed for payload loaded version=\"1.0.1-abc\" image=\"image/image:1\": Precondition \"TestPrecondition SuccessAfter: 3\" failed because of \"CheckFailure\": failing, attempt: 1 will succeed after 3 attempt",
				LastTransitionTime: time.Unix(2, 0),
				Update:             configv1.Update{Version: "1.0.1-abc", Image: "image/image:1"},
				Failure:            &payload.UpdateError{Reason: "UpgradePreconditionCheckFailed", Message: "Precondition \"TestPrecondition SuccessAfter: 3\" failed because of \"CheckFailure\": failing, attempt: 1 will succeed after 3 attempt", Name: "PreconditionCheck"},
			},
		},
	)

	client.ClearActions()
	err = o.sync(ctx, o.queueKey())
	if err != nil {
		t.Fatal(err)
	}
	actions = client.Actions()
	if len(actions) != 2 {
		t.Fatalf("%s", spew.Sdump(actions))
	}
	expectGet(t, actions[0], "clusterversions", "", "version")
	actual := cvs["version"].(*configv1.ClusterVersion)

	// Step 2: Set allowUnverifiedImages to true, trigger a sync and the operator should apply the payload
	//
	// set an update
	copied := configv1.Update{
		Version: desired.Version,
		Image:   desired.Image,
		Force:   true,
	}
	actual.Spec.DesiredUpdate = &copied
	//
	// ensure the sync worker tells the sync loop about it
	err = o.sync(ctx, o.queueKey())
	if err != nil {
		t.Fatal(err)
	}

	// wait until we see the new payload show up
	count := 0
	for {
		var status SyncWorkerStatus
		select {
		case status = <-worker.StatusCh():
		case <-time.After(3 * time.Second):
			t.Fatalf("never saw expected retrieve payload event")
		}
		if reflect.DeepEqual(configv1.Release{Version: "1.0.1-abc", Image: "image/image:1"}, status.Actual) {
			break
		}
		t.Logf("Unexpected status waiting to see first retrieve: %#v", status)
		count++
		if count > 8 {
			t.Fatalf("saw too many sync events of the wrong form")
		}
	}
	// wait until the new payload is applied
	count = 0
	for {
		var status SyncWorkerStatus
		select {
		case status = <-worker.StatusCh():
		case <-time.After(3 * time.Second):
			t.Fatalf("never saw expected apply event")
		}
		if status.loadPayloadStatus.Step == "PayloadLoaded" {
			break
		}
		t.Log("Waiting to see step PayloadLoaded")
		count++
		if count > 8 {
			t.Fatalf("saw too many sync events of the wrong form")
		}
	}
	verifyAllStatus(t, worker.StatusCh(),
		SyncWorkerStatus{
			Total:       3,
			VersionHash: "DL-FFQ2Uem8=", Architecture: architecture,
			Actual: configv1.Release{
				Version: "1.0.1-abc",
				Image:   "image/image:1",
				URL:     "https://example.com/v1.0.1-abc",
			},
			Generation: 1,
			CapabilitiesStatus: CapabilityStatus{
				Status: configv1.ClusterVersionCapabilitiesStatus{
					EnabledCapabilities: sortedCaps,
					KnownCapabilities:   sortedKnownCaps,
				},
			},
			loadPayloadStatus: LoadPayloadStatus{
				Step:               "PayloadLoaded",
				Message:            "Payload loaded version=\"1.0.1-abc\" image=\"image/image:1\" architecture=\"" + architecture + "\"",
				LastTransitionTime: time.Unix(1, 0),
				Update:             configv1.Update{Version: "1.0.1-abc", Image: "image/image:1", Force: true},
			},
		},
		SyncWorkerStatus{
			Done:        1,
			Total:       3,
			VersionHash: "DL-FFQ2Uem8=", Architecture: architecture,
			Actual: configv1.Release{
				Version: "1.0.1-abc",
				Image:   "image/image:1",
				URL:     "https://example.com/v1.0.1-abc",
			},
			LastProgress: time.Unix(1, 0),
			Generation:   1,
			CapabilitiesStatus: CapabilityStatus{
				Status: configv1.ClusterVersionCapabilitiesStatus{
					EnabledCapabilities: sortedCaps,
					KnownCapabilities:   sortedKnownCaps,
				},
			},
			loadPayloadStatus: LoadPayloadStatus{
				Step:               "PayloadLoaded",
				Message:            "Payload loaded version=\"1.0.1-abc\" image=\"image/image:1\" architecture=\"" + architecture + "\"",
				LastTransitionTime: time.Unix(2, 0),
				Update:             configv1.Update{Version: "1.0.1-abc", Image: "image/image:1", Force: true},
			},
		},
		SyncWorkerStatus{
			Done:        2,
			Total:       3,
			VersionHash: "DL-FFQ2Uem8=", Architecture: architecture,
			Actual: configv1.Release{
				Version: "1.0.1-abc",
				Image:   "image/image:1",
				URL:     "https://example.com/v1.0.1-abc",
			},
			LastProgress: time.Unix(2, 0),
			Generation:   1,
			CapabilitiesStatus: CapabilityStatus{
				Status: configv1.ClusterVersionCapabilitiesStatus{
					EnabledCapabilities: sortedCaps,
					KnownCapabilities:   sortedKnownCaps,
				},
			},
			loadPayloadStatus: LoadPayloadStatus{
				Step:               "PayloadLoaded",
				Message:            "Payload loaded version=\"1.0.1-abc\" image=\"image/image:1\" architecture=\"" + architecture + "\"",
				LastTransitionTime: time.Unix(3, 0),
				Update:             configv1.Update{Version: "1.0.1-abc", Image: "image/image:1", Force: true},
			},
		},
	)

	// wait for status to reflect sync of new payload
	waitForStatusCompleted(t, worker)

	client.ClearActions()
	err = o.sync(ctx, o.queueKey())
	if err != nil {
		t.Fatal(err)
	}

	actions = client.Actions()
	if len(actions) != 2 {
		t.Fatalf("%s", spew.Sdump(actions))
	}
	expectGet(t, actions[0], "clusterversions", "", "version")
	expectUpdateStatus(t, actions[1], "clusterversions", "", &configv1.ClusterVersion{
		ObjectMeta: metav1.ObjectMeta{
			Name:            "version",
			ResourceVersion: "4",
			Generation:      1,
		},
		Spec: configv1.ClusterVersionSpec{
			ClusterID:     actual.Spec.ClusterID,
			Channel:       "fast",
			DesiredUpdate: &copied,
		},
		Status: configv1.ClusterVersionStatus{
			ObservedGeneration: 1,
			Desired: configv1.Release{
				Version: "1.0.1-abc",
				Image:   "image/image:1",
				URL:     "https://example.com/v1.0.1-abc",
			},
			VersionHash: "DL-FFQ2Uem8=",
			Capabilities: configv1.ClusterVersionCapabilitiesStatus{
				EnabledCapabilities: sortedCaps,
				KnownCapabilities:   sortedKnownCaps,
			},
			History: []configv1.UpdateHistory{
				{State: configv1.CompletedUpdate, Image: "image/image:1", Version: "1.0.1-abc", StartedTime: defaultStartedTime, CompletionTime: &defaultCompletionTime},
				{State: configv1.CompletedUpdate, Image: "image/image:0", Version: "1.0.0-abc", Verified: true, StartedTime: defaultStartedTime, CompletionTime: &defaultCompletionTime},
			},
			Conditions: []configv1.ClusterOperatorStatusCondition{
				{Type: ImplicitlyEnabledCapabilities, Status: "False", Reason: "AsExpected", Message: "Capabilities match configured spec"},
				{Type: configv1.OperatorAvailable, Status: configv1.ConditionTrue, Message: "Done applying 1.0.1-abc"},
				{Type: ClusterStatusFailing, Status: configv1.ConditionFalse},
				{Type: configv1.OperatorProgressing, Status: configv1.ConditionFalse, Message: "Cluster version is 1.0.1-abc"},
				{Type: configv1.RetrievedUpdates, Status: configv1.ConditionFalse},
				{Type: DesiredReleaseAccepted, Status: configv1.ConditionTrue, Reason: "PayloadLoaded",
					Message: "Payload loaded version=\"1.0.1-abc\" image=\"image/image:1\" architecture=\"" + architecture + "\""},
			},
		},
	})
}

func TestCVO_UpgradePreconditionFailingAcceptedRisks(t *testing.T) {
	o, cvs, client, _, shutdownFn := setupCVOTest("testdata/payloadtest-2")

	// Setup: a successful sync from a previous run, and the operator at the same image as before
	//
	o.release.Image = "image/image:0"
	o.release.Version = "1.0.0-abc"
	desired := configv1.Release{Version: "1.0.1-abc", Image: "image/image:1"}
	uid, _ := uuid.NewRandom()
	clusterUID := configv1.ClusterID(uid.String())
	cvs["version"] = &configv1.ClusterVersion{
		ObjectMeta: metav1.ObjectMeta{
			Name:            "version",
			ResourceVersion: "1",
			Generation:      1,
		},
		Spec: configv1.ClusterVersionSpec{
			ClusterID:     clusterUID,
			Channel:       "fast",
			DesiredUpdate: &configv1.Update{Version: desired.Version, Image: desired.Image},
		},
		Status: configv1.ClusterVersionStatus{
			// Prefers the image version over the operator's version (although in general they will remain in sync)
			Desired:     desired,
			VersionHash: "DL-FFQ2Uem8=",
			History: []configv1.UpdateHistory{
				{State: configv1.CompletedUpdate, Image: "image/image:0", Version: "1.0.0-abc", Verified: true, StartedTime: defaultStartedTime, CompletionTime: &defaultCompletionTime},
			},
			Conditions: []configv1.ClusterOperatorStatusCondition{
				{Type: ImplicitlyEnabledCapabilities, Status: "False", Reason: "AsExpected", Message: "Capabilities match configured spec"},
				{Type: configv1.OperatorAvailable, Status: configv1.ConditionTrue, Message: "Done applying 1.0.0-abc"},
				{Type: ClusterStatusFailing, Status: configv1.ConditionFalse},
				{Type: configv1.OperatorProgressing, Status: configv1.ConditionFalse, Message: "Cluster version is 1.0.0-abc"},
				{Type: configv1.RetrievedUpdates, Status: configv1.ConditionFalse},
			},
		},
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	defer shutdownFn()

	worker := o.configSync.(*SyncWorker)
	worker.initializedFunc = func() bool { return true }
	worker.preconditions = []precondition.Precondition{&testPreconditionAlwaysFail{PreConditionName: "PreCondition1"}, &testPreconditionAlwaysFail{PreConditionName: "PreCondition2"}}

	go worker.Start(ctx, 1)

	// Step 1: The operator should report that it is blocked on precondition checks failing
	//
	client.ClearActions()
	err := o.sync(ctx, o.queueKey())
	if err != nil {
		t.Fatal(err)
	}
	actions := client.Actions()
	verifyCVSingleUpdate(t, actions)

	verifyAllStatus(t, worker.StatusCh(),
		SyncWorkerStatus{
			Actual:     configv1.Release{Version: "1.0.1-abc", Image: "image/image:1"},
			Generation: 1,
			loadPayloadStatus: LoadPayloadStatus{
				Step:               "RetrievePayload",
				Message:            "Retrieving and verifying payload version=\"1.0.1-abc\" image=\"image/image:1\"",
				LastTransitionTime: time.Unix(1, 0),
				Update:             configv1.Update{Version: "1.0.1-abc", Image: "image/image:1"},
			},
		},
		SyncWorkerStatus{
			Actual:       configv1.Release{Version: "1.0.1-abc", Image: "image/image:1"},
			Generation:   1,
			LastProgress: time.Unix(1, 0),
			loadPayloadStatus: LoadPayloadStatus{
				Step:               "PreconditionChecks",
				Message:            "Preconditions failed for payload loaded version=\"1.0.1-abc\" image=\"image/image:1\": Multiple precondition checks failed:\n* Precondition \"PreCondition1\" failed because of \"CheckFailure\": PreCondition1 will always fail.\n* Precondition \"PreCondition2\" failed because of \"CheckFailure\": PreCondition2 will always fail.",
				LastTransitionTime: time.Unix(2, 0),
				Update:             configv1.Update{Version: "1.0.1-abc", Image: "image/image:1"},
				Failure:            &payload.UpdateError{Reason: "UpgradePreconditionCheckFailed", Message: "Multiple precondition checks failed:\n* Precondition \"PreCondition1\" failed because of \"CheckFailure\": PreCondition1 will always fail.\n* Precondition \"PreCondition2\" failed because of \"CheckFailure\": PreCondition2 will always fail.", Name: "PreconditionCheck"},
			},
		},
	)

	client.ClearActions()
	err = o.sync(ctx, o.queueKey())
	if err != nil {
		t.Fatal(err)
	}
	actual := cvs["version"].(*configv1.ClusterVersion)

	// Step 2: Force through precondition failures and ensure accepted risks are populated
	//
	// set an update
	copied := configv1.Update{
		Version: desired.Version,
		Image:   desired.Image,
		Force:   true,
	}
	actual.Spec.DesiredUpdate = &copied
	//
	// ensure the sync worker tells the sync loop about it
	err = o.sync(ctx, o.queueKey())
	if err != nil {
		t.Fatal(err)
	}

	waitForStatus(t, 8, 3, worker.StatusCh(), waitForPayloadLoaded)
	verifyFinalStatus(t, "Step 2", 10, true, worker.StatusCh(), SyncWorkerStatus{
		Total:       3,
		VersionHash: "DL-FFQ2Uem8=", Architecture: architecture,
		Actual: configv1.Release{
			Version: "1.0.1-abc",
			Image:   "image/image:1",
			URL:     "https://example.com/v1.0.1-abc",
		},
		LastProgress: time.Unix(2, 0),
		Generation:   1,
		CapabilitiesStatus: CapabilityStatus{
			Status: configv1.ClusterVersionCapabilitiesStatus{
				EnabledCapabilities: sortedCaps,
				KnownCapabilities:   sortedKnownCaps,
			},
		},
		loadPayloadStatus: LoadPayloadStatus{
			Step:               "PayloadLoaded",
			Message:            "Payload loaded version=\"1.0.1-abc\" image=\"image/image:1\" architecture=\"" + architecture + "\"",
			AcceptedRisks:      "Forced through blocking failures: Multiple precondition checks failed:\n* Precondition \"PreCondition1\" failed because of \"CheckFailure\": PreCondition1 will always fail.\n* Precondition \"PreCondition2\" failed because of \"CheckFailure\": PreCondition2 will always fail.",
			LastTransitionTime: time.Unix(3, 0),
			Update:             configv1.Update{Version: "1.0.1-abc", Image: "image/image:1", Force: true},
		},
	},
		acceptedRisksPopulated,
	)

	// wait for loaded payload to be sync'ed
	waitForStatus(t, 8, 3, worker.StatusCh(), waitForCompleted)
	actions = client.Actions()
	t.Logf("%#v", actions)

	// confirm history updated
	checkStatus(t, actions[2], "update", "clusterversions", "status", "", []configv1.UpdateHistory{
		{State: configv1.PartialUpdate, Image: "image/image:1", Version: "1.0.1-abc", StartedTime: defaultStartedTime, AcceptedRisks: "Forced through blocking failures: Multiple precondition checks failed:\n* Precondition \"PreCondition1\" failed because of \"CheckFailure\": PreCondition1 will always fail.\n* Precondition \"PreCondition2\" failed because of \"CheckFailure\": PreCondition2 will always fail."},
		{State: configv1.CompletedUpdate, Image: "image/image:0", Version: "1.0.0-abc", StartedTime: defaultStartedTime, CompletionTime: &defaultCompletionTime, Verified: true},
	})
}

func TestCVO_UpgradePayloadStillInitializing(t *testing.T) {
	o, cvs, client, _, shutdownFn := setupCVOTest("testdata/payloadtest")

	// Setup: an upgrade request from user to a new image and the operator at the same image as before
	//
	o.release.Image = "image/image:0"
	o.release.Version = "1.0.0-abc"
	desired := configv1.Release{Version: "1.0.1-abc", Image: "image/image:1"}
	uid, _ := uuid.NewRandom()
	clusterUID := configv1.ClusterID(uid.String())
	cvs["version"] = &configv1.ClusterVersion{
		ObjectMeta: metav1.ObjectMeta{
			Name:            "version",
			ResourceVersion: "1",
			Generation:      1,
		},
		Spec: configv1.ClusterVersionSpec{
			ClusterID:     clusterUID,
			Channel:       "fast",
			DesiredUpdate: &configv1.Update{Version: desired.Version, Image: desired.Image},
		},
		Status: configv1.ClusterVersionStatus{
			// Prefers the image version over the operator's version (although in general they will remain in sync)
			Desired:     desired,
			VersionHash: "DL-FFQ2Uem8=",
			History: []configv1.UpdateHistory{
				{State: configv1.CompletedUpdate, Image: "image/image:0", Version: "1.0.0-abc", Verified: true, StartedTime: defaultStartedTime, CompletionTime: &defaultCompletionTime},
			},
			Conditions: []configv1.ClusterOperatorStatusCondition{
				{Type: ImplicitlyEnabledCapabilities, Status: "False", Reason: "AsExpected", Message: "Capabilities match configured spec"},
				{Type: configv1.OperatorAvailable, Status: configv1.ConditionTrue, Message: "Done applying 1.0.0-abc"},
				{Type: ClusterStatusFailing, Status: configv1.ConditionFalse},
				{Type: configv1.OperatorProgressing, Status: configv1.ConditionFalse, Message: "Cluster version is 1.0.0-abc"},
				{Type: configv1.RetrievedUpdates, Status: configv1.ConditionFalse},
			},
		},
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	defer shutdownFn()

	worker := o.configSync.(*SyncWorker)
	retriever := worker.retriever.(*fakeDirectoryRetriever)
	retriever.Set(PayloadInfo{Directory: "testdata/payloadtest", Verified: true}, nil)

	go worker.Start(ctx, 1)

	// Step 1: Simulate a payload being retrieved while the sync worker is not initialized
	// and ensure the desired version from the operator is taken from the operator and a reconciliation is enqueued
	client.ClearActions()
	err := o.sync(ctx, o.queueKey())
	if err != nil {
		t.Fatal(err)
	}
	actions := client.Actions()
	if len(actions) != 2 {
		t.Fatalf("%s", spew.Sdump(actions))
	}
	expectGet(t, actions[0], "clusterversions", "", "version")
	expectUpdateStatus(t, actions[1], "clusterversions", "", &configv1.ClusterVersion{
		ObjectMeta: metav1.ObjectMeta{
			Name:            "version",
			ResourceVersion: "1",
			Generation:      1,
		},
		Spec: configv1.ClusterVersionSpec{
			ClusterID:     clusterUID,
			Channel:       "fast",
			DesiredUpdate: &configv1.Update{Version: desired.Version, Image: desired.Image},
		},
		Status: configv1.ClusterVersionStatus{
			ObservedGeneration: 1,
			// Prefers the operator's version
			Desired:     configv1.Release{Version: o.release.Version, Image: o.release.Image},
			VersionHash: "DL-FFQ2Uem8=",
			History: []configv1.UpdateHistory{
				{State: configv1.CompletedUpdate, Image: "image/image:0", Version: "1.0.0-abc", Verified: true, StartedTime: defaultStartedTime, CompletionTime: &defaultCompletionTime},
			},
			Capabilities: configv1.ClusterVersionCapabilitiesStatus{
				EnabledCapabilities: sortedCaps,
				KnownCapabilities:   sortedKnownCaps,
			},
			Conditions: []configv1.ClusterOperatorStatusCondition{
				{Type: ImplicitlyEnabledCapabilities, Status: "False", Reason: "AsExpected", Message: "Capabilities match configured spec"},
				{Type: configv1.OperatorAvailable, Status: configv1.ConditionTrue, Message: "Done applying 1.0.0-abc"},
				{Type: ClusterStatusFailing, Status: configv1.ConditionFalse},
				{Type: configv1.OperatorProgressing, Status: configv1.ConditionFalse, Message: "Cluster version is 1.0.0-abc"},
				{Type: configv1.RetrievedUpdates, Status: configv1.ConditionFalse},
				{Type: DesiredReleaseAccepted, Status: configv1.ConditionTrue, Reason: "PayloadLoaded",
					Message: `Payload loaded version="1.0.0-abc" image="image/image:0" architecture="` + architecture + `"`},
			},
		},
	})
	if l := o.queue.Len(); l != 1 {
		t.Errorf("expecting queue length is 1 but got %d", l)
	}

}

func TestCVO_UpgradeVerifiedPayload(t *testing.T) {
	o, cvs, client, _, shutdownFn := setupCVOTest("testdata/payloadtest-2")

	// Setup: a successful sync from a previous run, and the operator at the same image as before
	//
	o.release.Image = "image/image:0"
	o.release.Version = "1.0.0-abc"
	desired := configv1.Release{Version: "1.0.1-abc", Image: "image/image:1"}
	uid, _ := uuid.NewRandom()
	clusterUID := configv1.ClusterID(uid.String())
	cvs["version"] = &configv1.ClusterVersion{
		ObjectMeta: metav1.ObjectMeta{
			Name:            "version",
			ResourceVersion: "1",
			Generation:      1,
		},
		Spec: configv1.ClusterVersionSpec{
			ClusterID:     clusterUID,
			Channel:       "fast",
			DesiredUpdate: &configv1.Update{Version: desired.Version, Image: desired.Image},
		},
		Status: configv1.ClusterVersionStatus{
			// Prefers the image version over the operator's version (although in general they will remain in sync)
			Desired:     desired,
			VersionHash: "DL-FFQ2Uem8=",
			History: []configv1.UpdateHistory{
				{State: configv1.CompletedUpdate, Image: "image/image:0", Version: "1.0.0-abc", Verified: true, StartedTime: defaultStartedTime, CompletionTime: &defaultCompletionTime},
			},
			Conditions: []configv1.ClusterOperatorStatusCondition{
				{Type: ImplicitlyEnabledCapabilities, Status: "False", Reason: "AsExpected", Message: "Capabilities match configured spec"},
				{Type: configv1.OperatorAvailable, Status: configv1.ConditionTrue, Message: "Done applying 1.0.0-abc"},
				{Type: ClusterStatusFailing, Status: configv1.ConditionFalse},
				{Type: configv1.OperatorProgressing, Status: configv1.ConditionFalse, Message: "Cluster version is 1.0.0-abc"},
				{Type: configv1.RetrievedUpdates, Status: configv1.ConditionFalse},
			},
		},
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	defer shutdownFn()

	// make the image report unverified
	payloadErr := &payload.UpdateError{
		Reason:  "ImageVerificationFailed",
		Message: "The update cannot be verified: some random error",
		Nested:  fmt.Errorf("some random error"),
	}
	if !isImageVerificationError(payloadErr) {
		t.Fatal("not the correct error type")
	}
	worker := o.configSync.(*SyncWorker)
	worker.initializedFunc = func() bool { return true }
	retriever := worker.retriever.(*fakeDirectoryRetriever)
	retriever.Set(PayloadInfo{}, payloadErr)
	retriever.Set(PayloadInfo{Directory: "testdata/payloadtest-2", Verified: true}, nil)

	go worker.Start(ctx, 1)

	// Step 1: Simulate a verified payload being retrieved and ensure the operator sets verified
	//
	client.ClearActions()
	err := o.sync(ctx, o.queueKey())
	if err != nil {
		t.Fatal(err)
	}
	actions := client.Actions()
	if len(actions) != 2 {
		t.Fatalf("%s", spew.Sdump(actions))
	}
	expectGet(t, actions[0], "clusterversions", "", "version")
	expectUpdateStatus(t, actions[1], "clusterversions", "", &configv1.ClusterVersion{
		ObjectMeta: metav1.ObjectMeta{
			Name:            "version",
			ResourceVersion: "1",
			Generation:      1,
		},
		Spec: configv1.ClusterVersionSpec{
			ClusterID:     clusterUID,
			Channel:       "fast",
			DesiredUpdate: &configv1.Update{Version: desired.Version, Image: desired.Image},
		},
		Status: configv1.ClusterVersionStatus{
			ObservedGeneration: 1,
			// Prefers the image version over the operator's version (although in general they will remain in sync)
			Desired:     desired,
			VersionHash: "DL-FFQ2Uem8=",
			History: []configv1.UpdateHistory{
				{State: configv1.PartialUpdate, Image: "image/image:1", Version: "1.0.1-abc", StartedTime: defaultStartedTime},
				{State: configv1.CompletedUpdate, Image: "image/image:0", Version: "1.0.0-abc", Verified: true, StartedTime: defaultStartedTime, CompletionTime: &defaultCompletionTime},
			},
			Capabilities: configv1.ClusterVersionCapabilitiesStatus{
				EnabledCapabilities: sortedCaps,
				KnownCapabilities:   sortedKnownCaps,
			},
			Conditions: []configv1.ClusterOperatorStatusCondition{
				{Type: ImplicitlyEnabledCapabilities, Status: "False", Reason: "AsExpected", Message: "Capabilities match configured spec"},
				{Type: configv1.OperatorAvailable, Status: configv1.ConditionTrue, Message: "Done applying 1.0.0-abc"},
				// cleared failing status and set progressing
				{Type: ClusterStatusFailing, Status: configv1.ConditionFalse},
				{Type: configv1.OperatorProgressing, Status: configv1.ConditionTrue, Message: "Working towards 1.0.1-abc"},
				{Type: configv1.RetrievedUpdates, Status: configv1.ConditionFalse},
				{Type: DesiredReleaseAccepted, Status: configv1.ConditionTrue, Reason: "PayloadLoaded",
					Message: "Payload loaded version=\"1.0.1-abc\" image=\"image/image:1\" architecture=\"" + architecture + "\""},
			},
		},
	})
}

func TestCVO_RestartAndReconcile(t *testing.T) {
	o, cvs, client, _, shutdownFn := setupCVOTest("testdata/payloadtest")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	defer shutdownFn()
	worker := o.configSync.(*SyncWorker)

	// Setup: a successful sync from a previous run, and the operator at the same image as before
	//
	o.release.Image = "image/image:1"
	o.release.Version = "1.0.0-abc"
	o.release.URL = "https://example.com/v1.0.0-abc"
	o.release.Channels = []string{"channel-a", "channel-b", "channel-c"}
	uid, _ := uuid.NewRandom()
	clusterUID := configv1.ClusterID(uid.String())
	cvs["version"] = &configv1.ClusterVersion{
		ObjectMeta: metav1.ObjectMeta{
			Name:            "version",
			ResourceVersion: "1",
		},
		Spec: configv1.ClusterVersionSpec{
			ClusterID: clusterUID,
			Channel:   "fast",
		},
		Status: configv1.ClusterVersionStatus{
			// Prefers the image version over the operator's version (although in general they will remain in sync)
			Desired: configv1.Release{
				Version:  "1.0.0-abc",
				Image:    "image/image:1",
				URL:      "https://example.com/v1.0.0-abc",
				Channels: []string{"channel-a", "channel-b", "channel-c"},
			},
			VersionHash: "DL-FFQ2Uem8=",
			History: []configv1.UpdateHistory{
				// TODO: this is wrong, should be single partial entry
				{State: configv1.CompletedUpdate, Image: "image/image:1", Version: "1.0.0-abc", Verified: true, StartedTime: defaultStartedTime, CompletionTime: &defaultCompletionTime},
				{State: configv1.PartialUpdate, StartedTime: defaultStartedTime, CompletionTime: &defaultCompletionTime},
				{State: configv1.PartialUpdate, StartedTime: defaultStartedTime, CompletionTime: &defaultCompletionTime},
			},
			Conditions: []configv1.ClusterOperatorStatusCondition{
				{Type: ImplicitlyEnabledCapabilities, Status: "False", Reason: "AsExpected", Message: "Capabilities match configured spec"},
				{Type: configv1.OperatorAvailable, Status: configv1.ConditionTrue, Message: "Done applying 1.0.0-abc"},
				{Type: ClusterStatusFailing, Status: configv1.ConditionFalse},
				{Type: configv1.OperatorProgressing, Status: configv1.ConditionFalse, Message: "Cluster version is 1.0.0-abc"},
				{Type: configv1.RetrievedUpdates, Status: configv1.ConditionFalse},
			},
		},
	}

	// Step 1: The sync loop starts and triggers a sync, but does not update status
	//
	client.ClearActions()
	err := o.sync(ctx, o.queueKey())
	if err != nil {
		t.Fatal(err)
	}
	actions := client.Actions()
	if len(actions) != 2 {
		t.Fatalf("%s", spew.Sdump(actions))
	}
	expectGet(t, actions[0], "clusterversions", "", "version")

	// check the worker status is initially set to reconciling
	if status := worker.Status(); !status.Reconciling || status.Completed != 0 {
		t.Fatalf("The worker should be reconciling from the beginning: %#v", status)
	}
	if worker.work.State != payload.ReconcilingPayload {
		t.Fatalf("The worker should be reconciling: %v", worker.work)
	}

	// Step 2: Start the sync worker and verify the sequence of events, and then verify
	//         the status does not change
	//
	go worker.Start(ctx, 1)
	//
	verifyAllStatus(t, worker.StatusCh(),
		SyncWorkerStatus{
			Reconciling: true,
			Actual:      configv1.Release{Version: "1.0.0-abc", Image: "image/image:1"},
			loadPayloadStatus: LoadPayloadStatus{
				Step:               "RetrievePayload",
				Message:            "Retrieving and verifying payload version=\"1.0.0-abc\" image=\"image/image:1\"",
				LastTransitionTime: time.Unix(1, 0),
				Update:             configv1.Update{Version: "1.0.0-abc", Image: "image/image:1"},
			},
		},
		SyncWorkerStatus{
			Reconciling: true,
			Actual: configv1.Release{
				Version: "1.0.0-abc",
				Image:   "image/image:1",
			},
			LastProgress: time.Unix(1, 0),
			loadPayloadStatus: LoadPayloadStatus{
				Step:               "PayloadLoaded",
				Message:            "Payload loaded version=\"1.0.0-abc\" image=\"image/image:1\" architecture=\"" + architecture + "\"",
				LastTransitionTime: time.Unix(2, 0),
				Update:             configv1.Update{Version: "1.0.0-abc", Image: "image/image:1"},
			},
		},
		SyncWorkerStatus{
			Reconciling: true,
			Total:       3,
			VersionHash: "DL-FFQ2Uem8=", Architecture: architecture,
			Actual: configv1.Release{
				Version:  "1.0.0-abc",
				Image:    "image/image:1",
				URL:      "https://example.com/v1.0.0-abc",
				Channels: []string{"channel-a", "channel-b", "channel-c"},
			},
			LastProgress: time.Unix(2, 0),
			CapabilitiesStatus: CapabilityStatus{
				Status: configv1.ClusterVersionCapabilitiesStatus{
					EnabledCapabilities: sortedCaps,
					KnownCapabilities:   sortedKnownCaps,
				},
			},
			loadPayloadStatus: LoadPayloadStatus{
				Step:               "PayloadLoaded",
				Message:            "Payload loaded version=\"1.0.0-abc\" image=\"image/image:1\" architecture=\"" + architecture + "\"",
				LastTransitionTime: time.Unix(3, 0),
				Update:             configv1.Update{Version: "1.0.0-abc", Image: "image/image:1"},
			},
		},
		SyncWorkerStatus{
			Reconciling: true,
			Done:        1,
			Total:       3,
			VersionHash: "DL-FFQ2Uem8=", Architecture: architecture,
			Actual: configv1.Release{
				Version:  "1.0.0-abc",
				Image:    "image/image:1",
				URL:      "https://example.com/v1.0.0-abc",
				Channels: []string{"channel-a", "channel-b", "channel-c"},
			},
			LastProgress: time.Unix(3, 0),
			CapabilitiesStatus: CapabilityStatus{
				Status: configv1.ClusterVersionCapabilitiesStatus{
					EnabledCapabilities: sortedCaps,
					KnownCapabilities:   sortedKnownCaps,
				},
			},
			loadPayloadStatus: LoadPayloadStatus{
				Step:               "PayloadLoaded",
				Message:            "Payload loaded version=\"1.0.0-abc\" image=\"image/image:1\" architecture=\"" + architecture + "\"",
				LastTransitionTime: time.Unix(4, 0),
				Update:             configv1.Update{Version: "1.0.0-abc", Image: "image/image:1"},
			},
		},
		SyncWorkerStatus{
			Reconciling: true,
			Done:        2,
			Total:       3,
			VersionHash: "DL-FFQ2Uem8=", Architecture: architecture,
			Actual: configv1.Release{
				Version:  "1.0.0-abc",
				Image:    "image/image:1",
				URL:      "https://example.com/v1.0.0-abc",
				Channels: []string{"channel-a", "channel-b", "channel-c"},
			},
			LastProgress: time.Unix(4, 0),
			CapabilitiesStatus: CapabilityStatus{
				Status: configv1.ClusterVersionCapabilitiesStatus{
					EnabledCapabilities: sortedCaps,
					KnownCapabilities:   sortedKnownCaps,
				},
			},
			loadPayloadStatus: LoadPayloadStatus{
				Step:               "PayloadLoaded",
				Message:            "Payload loaded version=\"1.0.0-abc\" image=\"image/image:1\" architecture=\"" + architecture + "\"",
				LastTransitionTime: time.Unix(5, 0),
				Update:             configv1.Update{Version: "1.0.0-abc", Image: "image/image:1"},
			},
		},
	)
	client.ClearActions()
	err = o.sync(ctx, o.queueKey())
	if err != nil {
		t.Fatal(err)
	}
	actions = client.Actions()
	if len(actions) != 1 {
		t.Fatalf("%s", spew.Sdump(actions))
	}
	expectGet(t, actions[0], "clusterversions", "", "version")

	// Step 3: Wait until the next resync is triggered, and then verify that status does
	//         not change
	//
	verifyAllStatus(t, worker.StatusCh(),
		// note that the image is not retrieved a second time
		SyncWorkerStatus{
			Reconciling: true,
			Completed:   1,
			Done:        3,
			Total:       3,
			VersionHash: "DL-FFQ2Uem8=", Architecture: architecture,
			Actual: configv1.Release{
				Version:  "1.0.0-abc",
				Image:    "image/image:1",
				URL:      "https://example.com/v1.0.0-abc",
				Channels: []string{"channel-a", "channel-b", "channel-c"},
			},
			LastProgress: time.Unix(1, 0),
			CapabilitiesStatus: CapabilityStatus{
				Status: configv1.ClusterVersionCapabilitiesStatus{
					EnabledCapabilities: sortedCaps,
					KnownCapabilities:   sortedKnownCaps,
				},
			},
			loadPayloadStatus: LoadPayloadStatus{
				Step:               "PayloadLoaded",
				Message:            "Payload loaded version=\"1.0.0-abc\" image=\"image/image:1\" architecture=\"" + architecture + "\"",
				LastTransitionTime: time.Unix(1, 0),
				Update:             configv1.Update{Version: "1.0.0-abc", Image: "image/image:1"},
			},
		},
		SyncWorkerStatus{
			Reconciling: true,
			Total:       3,
			VersionHash: "DL-FFQ2Uem8=", Architecture: architecture,
			Actual: configv1.Release{
				Version:  "1.0.0-abc",
				Image:    "image/image:1",
				URL:      "https://example.com/v1.0.0-abc",
				Channels: []string{"channel-a", "channel-b", "channel-c"},
			},
			LastProgress: time.Unix(2, 0),
			CapabilitiesStatus: CapabilityStatus{
				Status: configv1.ClusterVersionCapabilitiesStatus{
					EnabledCapabilities: sortedCaps,
					KnownCapabilities:   sortedKnownCaps,
				},
			},
			loadPayloadStatus: LoadPayloadStatus{
				Step:               "PayloadLoaded",
				Message:            "Payload loaded version=\"1.0.0-abc\" image=\"image/image:1\" architecture=\"" + architecture + "\"",
				LastTransitionTime: time.Unix(2, 0),
				Update:             configv1.Update{Version: "1.0.0-abc", Image: "image/image:1"},
			},
		},
		SyncWorkerStatus{
			Reconciling: true,
			Done:        1,
			Total:       3,
			VersionHash: "DL-FFQ2Uem8=", Architecture: architecture,
			Actual: configv1.Release{
				Version:  "1.0.0-abc",
				Image:    "image/image:1",
				URL:      "https://example.com/v1.0.0-abc",
				Channels: []string{"channel-a", "channel-b", "channel-c"},
			},
			LastProgress: time.Unix(3, 0),
			CapabilitiesStatus: CapabilityStatus{
				Status: configv1.ClusterVersionCapabilitiesStatus{
					EnabledCapabilities: sortedCaps,
					KnownCapabilities:   sortedKnownCaps,
				},
			},
			loadPayloadStatus: LoadPayloadStatus{
				Step:               "PayloadLoaded",
				Message:            "Payload loaded version=\"1.0.0-abc\" image=\"image/image:1\" architecture=\"" + architecture + "\"",
				LastTransitionTime: time.Unix(3, 0),
				Update:             configv1.Update{Version: "1.0.0-abc", Image: "image/image:1"},
			},
		},
		SyncWorkerStatus{
			Reconciling: true,
			Done:        2,
			Total:       3,
			VersionHash: "DL-FFQ2Uem8=", Architecture: architecture,
			Actual: configv1.Release{
				Version:  "1.0.0-abc",
				Image:    "image/image:1",
				URL:      "https://example.com/v1.0.0-abc",
				Channels: []string{"channel-a", "channel-b", "channel-c"},
			},
			LastProgress: time.Unix(4, 0),
			CapabilitiesStatus: CapabilityStatus{
				Status: configv1.ClusterVersionCapabilitiesStatus{
					EnabledCapabilities: sortedCaps,
					KnownCapabilities:   sortedKnownCaps,
				},
			},
			loadPayloadStatus: LoadPayloadStatus{
				Step:               "PayloadLoaded",
				Message:            "Payload loaded version=\"1.0.0-abc\" image=\"image/image:1\" architecture=\"" + architecture + "\"",
				LastTransitionTime: time.Unix(4, 0),
				Update:             configv1.Update{Version: "1.0.0-abc", Image: "image/image:1"},
			},
		},
	)
	client.ClearActions()
	err = o.sync(ctx, o.queueKey())
	if err != nil {
		t.Fatal(err)
	}
	actions = client.Actions()
	if len(actions) != 1 {
		t.Fatalf("%s", spew.Sdump(actions))
	}
	expectGet(t, actions[0], "clusterversions", "", "version")
}

func TestCVO_ErrorDuringReconcile(t *testing.T) {
	o, cvs, client, _, shutdownFn := setupCVOTest("testdata/payloadtest")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	defer shutdownFn()
	worker := o.configSync.(*SyncWorker)
	b := newBlockingResourceBuilder()
	worker.builder = b

	// Setup: a successful sync from a previous run, and the operator at the same image as before
	//
	o.release.Image = "image/image:1"
	o.release.Version = "1.0.0-abc"
	o.release.URL = "https://example.com/v1.0.0-abc"
	o.release.Channels = []string{"channel-a", "channel-b", "channel-c"}
	uid, _ := uuid.NewRandom()
	clusterUID := configv1.ClusterID(uid.String())
	cvs["version"] = &configv1.ClusterVersion{
		ObjectMeta: metav1.ObjectMeta{
			Name:            "version",
			ResourceVersion: "1",
		},
		Spec: configv1.ClusterVersionSpec{
			ClusterID: clusterUID,
			Channel:   "fast",
		},
		Status: configv1.ClusterVersionStatus{
			// Prefers the image version over the operator's version (although in general they will remain in sync)
			Desired: configv1.Release{
				Version:  "1.0.0-abc",
				Image:    "image/image:1",
				URL:      "https://example.com/v1.0.0-abc",
				Channels: []string{"channel-a", "channel-b", "channel-c"},
			},
			VersionHash: "DL-FFQ2Uem8=",
			History: []configv1.UpdateHistory{
				{State: configv1.CompletedUpdate, Image: "image/image:1", Version: "1.0.0-abc", Verified: true, StartedTime: defaultStartedTime, CompletionTime: &defaultCompletionTime},
			},
			Conditions: []configv1.ClusterOperatorStatusCondition{
				{Type: ImplicitlyEnabledCapabilities, Status: "False", Reason: "AsExpected", Message: "Capabilities match configured spec"},
				{Type: configv1.OperatorAvailable, Status: configv1.ConditionTrue, Message: "Done applying 1.0.0-abc"},
				{Type: ClusterStatusFailing, Status: configv1.ConditionFalse},
				{Type: configv1.OperatorProgressing, Status: configv1.ConditionFalse, Message: "Cluster version is 1.0.0-abc"},
				{Type: configv1.RetrievedUpdates, Status: configv1.ConditionFalse},
			},
		},
	}

	// Step 1: The sync loop starts and triggers a sync, but does not update status
	//
	client.ClearActions()
	err := o.sync(ctx, o.queueKey())
	if err != nil {
		t.Fatal(err)
	}
	actions := client.Actions()
	if len(actions) != 2 {
		t.Fatalf("%s", spew.Sdump(actions))
	}
	expectGet(t, actions[0], "clusterversions", "", "version")

	// check the worker status is initially set to reconciling
	if status := worker.Status(); !status.Reconciling || status.Completed != 0 {
		t.Fatalf("The worker should be reconciling from the beginning: %#v", status)
	}
	if worker.work.State != payload.ReconcilingPayload {
		t.Fatalf("The worker should be reconciling: %v", worker.work)
	}
	//
	go worker.Start(ctx, 1)
	//
	verifyAllStatus(t, worker.StatusCh(),
		SyncWorkerStatus{
			Reconciling: true,
			Actual:      configv1.Release{Version: "1.0.0-abc", Image: "image/image:1"},
			loadPayloadStatus: LoadPayloadStatus{
				Step:               "RetrievePayload",
				Message:            "Retrieving and verifying payload version=\"1.0.0-abc\" image=\"image/image:1\"",
				LastTransitionTime: time.Unix(1, 0),
				Update:             configv1.Update{Version: "1.0.0-abc", Image: "image/image:1"},
			},
		},
		SyncWorkerStatus{
			Reconciling: true,
			Actual: configv1.Release{
				Version: "1.0.0-abc",
				Image:   "image/image:1",
			},
			LastProgress: time.Unix(1, 0),
			loadPayloadStatus: LoadPayloadStatus{
				Step:               "PayloadLoaded",
				Message:            "Payload loaded version=\"1.0.0-abc\" image=\"image/image:1\" architecture=\"" + architecture + "\"",
				LastTransitionTime: time.Unix(2, 0),
				Update:             configv1.Update{Version: "1.0.0-abc", Image: "image/image:1"},
			},
		},
	)
	// verify we haven't observed any other events
	verifyAllStatus(t, worker.StatusCh())

	// Step 2: Simulate a sync being triggered while we are partway through our first
	//         reconcile sync and verify status is not updated
	//
	client.ClearActions()
	err = o.sync(ctx, o.queueKey())
	if err != nil {
		t.Fatal(err)
	}

	actions = client.Actions()
	if len(actions) != 1 {
		t.Fatalf("%s", spew.Sdump(actions))
	}
	expectGet(t, actions[0], "clusterversions", "", "version")

	// Step 3: Unblock the first item from being applied
	//
	b.Send(nil)
	//
	// verify we observe the remaining changes in the first sync
	verifyAllStatus(t, worker.StatusCh(),
		SyncWorkerStatus{
			Reconciling: true,
			Total:       3,
			VersionHash: "DL-FFQ2Uem8=", Architecture: architecture,
			Actual: configv1.Release{
				Version:  "1.0.0-abc",
				Image:    "image/image:1",
				URL:      "https://example.com/v1.0.0-abc",
				Channels: []string{"channel-a", "channel-b", "channel-c"},
			},
			CapabilitiesStatus: CapabilityStatus{
				Status: configv1.ClusterVersionCapabilitiesStatus{
					EnabledCapabilities: sortedCaps,
					KnownCapabilities:   sortedKnownCaps,
				},
			},
			loadPayloadStatus: LoadPayloadStatus{
				Step:               "PayloadLoaded",
				Message:            "Payload loaded version=\"1.0.0-abc\" image=\"image/image:1\" architecture=\"" + architecture + "\"",
				LastTransitionTime: time.Unix(1, 0),
				Update:             configv1.Update{Version: "1.0.0-abc", Image: "image/image:1"},
			},
		},
	)
	clearAllStatus(t, worker.StatusCh())

	// Step 4: Unblock the first item from being applied
	//
	b.Send(nil)
	//
	// Verify we observe the remaining changes in the first sync. Since timing is
	// non-deterministic, use this instead of verifyAllStatus when don't know or
	// care how many are done.
	verifyAllStatusOptionalDone(t, "Step 4", true, worker.StatusCh(),
		SyncWorkerStatus{
			Reconciling: true,
			Done:        2,
			Total:       3,
			VersionHash: "DL-FFQ2Uem8=", Architecture: architecture,
			Actual: configv1.Release{
				Version:  "1.0.0-abc",
				Image:    "image/image:1",
				URL:      "https://example.com/v1.0.0-abc",
				Channels: []string{"channel-a", "channel-b", "channel-c"},
			},
			LastProgress: time.Unix(1, 0),
			CapabilitiesStatus: CapabilityStatus{
				Status: configv1.ClusterVersionCapabilitiesStatus{
					EnabledCapabilities: sortedCaps,
					KnownCapabilities:   sortedKnownCaps,
				},
			},
			loadPayloadStatus: LoadPayloadStatus{
				Step:               "PayloadLoaded",
				Message:            "Payload loaded version=\"1.0.0-abc\" image=\"image/image:1\" architecture=\"" + architecture + "\"",
				LastTransitionTime: time.Unix(1, 0),
				Update:             configv1.Update{Version: "1.0.0-abc", Image: "image/image:1"},
			},
		},
	)
	clearAllStatus(t, worker.StatusCh())

	// Step 5: Send an error, then verify it shows up in status
	//
	b.Send(fmt.Errorf("unable to proceed"))

	go func() {
		for len(b.ch) != 0 {
			time.Sleep(time.Millisecond)
		}
		cancel()
		for len(b.ch) == 0 || len(worker.StatusCh()) == 0 {
			time.Sleep(time.Millisecond)
		}
	}()

	//
	// verify we see the update after the context times out
	verifyAllStatus(t, worker.StatusCh(),
		SyncWorkerStatus{
			Reconciling: true,
			Done:        2,
			Total:       3,
			VersionHash: "DL-FFQ2Uem8=", Architecture: architecture,
			Failure: &payload.UpdateError{
				Nested:  fmt.Errorf("unable to proceed"),
				Reason:  "UpdatePayloadFailed",
				Message: "Could not update test \"file-yml\" (3 of 3)",
				Task:    &payload.Task{Index: 3, Total: 3, Manifest: &worker.payload.Manifests[2]},
			},
			Actual: configv1.Release{
				Version:  "1.0.0-abc",
				Image:    "image/image:1",
				URL:      "https://example.com/v1.0.0-abc",
				Channels: []string{"channel-a", "channel-b", "channel-c"},
			},
			LastProgress: time.Unix(1, 0),
			CapabilitiesStatus: CapabilityStatus{
				Status: configv1.ClusterVersionCapabilitiesStatus{
					EnabledCapabilities: sortedCaps,
					KnownCapabilities:   sortedKnownCaps,
				},
			},
			loadPayloadStatus: LoadPayloadStatus{
				Step:               "PayloadLoaded",
				Message:            "Payload loaded version=\"1.0.0-abc\" image=\"image/image:1\" architecture=\"" + architecture + "\"",
				LastTransitionTime: time.Unix(1, 0),
				Update:             configv1.Update{Version: "1.0.0-abc", Image: "image/image:1"},
			},
		},
	)
	client.ClearActions()
	err = o.sync(ctx, o.queueKey())
	if err != nil {
		t.Fatal(err)
	}
	actions = client.Actions()
	if len(actions) != 2 {
		t.Fatalf("%s", spew.Sdump(actions))
	}
	expectGet(t, actions[0], "clusterversions", "", "version")
	expectUpdateStatus(t, actions[1], "clusterversions", "", &configv1.ClusterVersion{
		ObjectMeta: metav1.ObjectMeta{
			Name:            "version",
			ResourceVersion: "2",
		},
		Spec: configv1.ClusterVersionSpec{
			ClusterID: clusterUID,
			Channel:   "fast",
		},
		Status: configv1.ClusterVersionStatus{
			// Prefers the image version over the operator's version (although in general they will remain in sync)
			Desired: configv1.Release{
				Version:  "1.0.0-abc",
				Image:    "image/image:1",
				URL:      "https://example.com/v1.0.0-abc",
				Channels: []string{"channel-a", "channel-b", "channel-c"},
			},
			VersionHash: "DL-FFQ2Uem8=",
			Capabilities: configv1.ClusterVersionCapabilitiesStatus{
				EnabledCapabilities: sortedCaps,
				KnownCapabilities:   sortedKnownCaps,
			},
			History: []configv1.UpdateHistory{
				{State: configv1.CompletedUpdate, Image: "image/image:1", Version: "1.0.0-abc", Verified: true, StartedTime: defaultStartedTime, CompletionTime: &defaultCompletionTime},
			},
			Conditions: []configv1.ClusterOperatorStatusCondition{
				{Type: ImplicitlyEnabledCapabilities, Status: "False", Reason: "AsExpected", Message: "Capabilities match configured spec"},
				{Type: configv1.OperatorAvailable, Status: configv1.ConditionTrue, Message: "Done applying 1.0.0-abc"},
				{Type: ClusterStatusFailing, Status: configv1.ConditionTrue, Reason: "UpdatePayloadFailed", Message: "Could not update test \"file-yml\" (3 of 3)"},
				{Type: configv1.OperatorProgressing, Status: configv1.ConditionFalse, Reason: "UpdatePayloadFailed", Message: "Error while reconciling 1.0.0-abc: the update could not be applied"},
				{Type: configv1.RetrievedUpdates, Status: configv1.ConditionFalse},
				{Type: DesiredReleaseAccepted, Status: configv1.ConditionTrue, Reason: "PayloadLoaded",
					Message: "Payload loaded version=\"1.0.0-abc\" image=\"image/image:1\" architecture=\"" + architecture + "\""},
			},
		},
	})
}

func TestCVO_ParallelError(t *testing.T) {
	o, cvs, client, _, shutdownFn := setupCVOTest("testdata/paralleltest")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	defer shutdownFn()
	worker := o.configSync.(*SyncWorker)
	b := &errorResourceBuilder{errors: map[string]error{
		"0000_10_a_file.yaml": &payload.UpdateError{
			UpdateEffect: payload.UpdateEffectNone,
			Message:      "Failed to reconcile 10-a-file resource",
		},
		"0000_20_a_file.yaml": nil,
		"0000_20_b_file.yaml": &payload.UpdateError{
			UpdateEffect: payload.UpdateEffectNone,
			Message:      "Failed to reconcile 20-b-file resource",
		},
	}}
	worker.builder = b

	// Setup: an initializing cluster version which will run in parallel
	//
	o.release.Image = "image/image:1"
	o.release.Version = "1.0.0-abc"
	desired := configv1.Release{Version: "1.0.0-abc", Image: "image/image:1"}
	uid, _ := uuid.NewRandom()
	clusterUID := configv1.ClusterID(uid.String())
	cvs["version"] = &configv1.ClusterVersion{
		ObjectMeta: metav1.ObjectMeta{
			Name:            "version",
			ResourceVersion: "1",
			Generation:      1,
		},
		Spec: configv1.ClusterVersionSpec{
			ClusterID: clusterUID,
			Channel:   "fast",
		},
		Status: configv1.ClusterVersionStatus{
			// Prefers the image version over the operator's version (although in general they will remain in sync)
			Desired:    desired,
			History:    []configv1.UpdateHistory{},
			Conditions: []configv1.ClusterOperatorStatusCondition{},
		},
	}

	// Step 1: Write initial status
	//
	client.ClearActions()
	err := o.sync(ctx, o.queueKey())
	if err != nil {
		t.Fatal(err)
	}
	actions := client.Actions()
	if len(actions) != 2 {
		t.Fatalf("%s", spew.Sdump(actions))
	}
	expectGet(t, actions[0], "clusterversions", "", "version")

	// check the worker status is initially set to reconciling
	if status := worker.Status(); status.Reconciling || status.Completed != 0 {
		t.Fatalf("The worker should be reconciling from the beginning: %#v", status)
	}
	if worker.work.State != payload.InitializingPayload {
		t.Fatalf("The worker should be reconciling: %v", worker.work)
	}

	// Step 2: Start the sync worker and wait for the payload to be loaded
	//
	cancellable, cancel := context.WithCancel(ctx)
	defer cancel()
	go worker.Start(cancellable, 1)

	// wait until the new payload is applied
	count := 0
	for {
		var status SyncWorkerStatus
		select {
		case status = <-worker.StatusCh():
		case <-time.After(3 * time.Second):
			t.Fatalf("never saw expected apply event")
		}
		if status.loadPayloadStatus.Step == "PayloadLoaded" {
			break
		}
		t.Log("Waiting to see step PayloadLoaded")
		count++
		if count > 8 {
			t.Fatalf("saw too many sync events of the wrong form")
		}
	}

	// Step 3: Cancel after we've accumulated 2/3 errors
	//
	// verify we observe the remaining changes in the first sync
	for status := range worker.StatusCh() {
		if status.Failure == nil {
			if status.Done == 0 || (status.Done == 1 && status.Total == 3) {
				if !reflect.DeepEqual(status, SyncWorkerStatus{
					Initial:      true,
					Done:         status.Done,
					Total:        3,
					VersionHash:  "Gyh2W6qcDO4=",
					Architecture: architecture,
					Actual:       configv1.Release{Version: "1.0.0-abc", Image: "image/image:1"},
					LastProgress: status.LastProgress,
					Generation:   1,
					CapabilitiesStatus: CapabilityStatus{
						Status: configv1.ClusterVersionCapabilitiesStatus{
							EnabledCapabilities: sortedCaps,
							KnownCapabilities:   sortedKnownCaps,
						},
					},
					loadPayloadStatus: LoadPayloadStatus{
						Step:               "PayloadLoaded",
						Message:            "Payload loaded version=\"1.0.0-abc\" image=\"image/image:1\" architecture=\"" + architecture + "\"",
						LastTransitionTime: status.loadPayloadStatus.LastTransitionTime,
						Update:             configv1.Update{Version: "1.0.0-abc", Image: "image/image:1"},
					},
				}) {
					t.Fatalf("unexpected status: %v", status)
				}
			}
			continue
		}
		err := status.Failure
		uErr, ok := err.(*payload.UpdateError)
		if !ok || uErr.Reason != "MultipleErrors" || uErr.Message != "Multiple errors are preventing progress:\n* Failed to reconcile 10-a-file resource\n* Failed to reconcile 20-b-file resource" {
			t.Fatalf("unexpected error: %v", err)
		}
		if status.LastProgress.IsZero() {
			t.Fatalf("unexpected last progress: %v", status.LastProgress)
		}
		if !reflect.DeepEqual(status, SyncWorkerStatus{
			Initial:      true,
			Failure:      err,
			Done:         1,
			Total:        3,
			VersionHash:  "Gyh2W6qcDO4=",
			Architecture: architecture,
			Actual:       configv1.Release{Version: "1.0.0-abc", Image: "image/image:1"},
			LastProgress: status.LastProgress,
			Generation:   1,
			CapabilitiesStatus: CapabilityStatus{
				Status: configv1.ClusterVersionCapabilitiesStatus{
					EnabledCapabilities: sortedCaps,
					KnownCapabilities:   sortedKnownCaps,
				},
			},
			loadPayloadStatus: LoadPayloadStatus{
				Step:               "PayloadLoaded",
				Message:            "Payload loaded version=\"1.0.0-abc\" image=\"image/image:1\" architecture=\"" + architecture + "\"",
				LastTransitionTime: status.loadPayloadStatus.LastTransitionTime,
				Update:             configv1.Update{Version: "1.0.0-abc", Image: "image/image:1"},
			},
		}) {
			t.Fatalf("unexpected final: %v", status)
		}
		break
	}
	cancel()
	verifyAllStatus(t, worker.StatusCh())

	client.ClearActions()
	err = o.sync(ctx, o.queueKey())
	if err != nil {
		t.Fatal(err)
	}
	actions = client.Actions()
	if len(actions) != 2 {
		t.Fatalf("%s", spew.Sdump(actions))
	}
	expectGet(t, actions[0], "clusterversions", "", "version")
	expectUpdateStatus(t, actions[1], "clusterversions", "", &configv1.ClusterVersion{
		ObjectMeta: metav1.ObjectMeta{
			Name:            "version",
			Generation:      1,
			ResourceVersion: "2",
		},
		Spec: configv1.ClusterVersionSpec{
			ClusterID: clusterUID,
			Channel:   "fast",
		},
		Status: configv1.ClusterVersionStatus{
			// Prefers the image version over the operator's version (although in general they will remain in sync)
			ObservedGeneration: 1,
			Desired:            configv1.Release{Version: "1.0.0-abc", Image: "image/image:1"},
			VersionHash:        "Gyh2W6qcDO4=",
			Capabilities: configv1.ClusterVersionCapabilitiesStatus{
				EnabledCapabilities: sortedCaps,
				KnownCapabilities:   sortedKnownCaps,
			},
			History: []configv1.UpdateHistory{
				{State: configv1.PartialUpdate, Image: "image/image:1", Version: "1.0.0-abc", StartedTime: defaultStartedTime},
			},
			Conditions: []configv1.ClusterOperatorStatusCondition{
				{Type: ImplicitlyEnabledCapabilities, Status: "False", Reason: "AsExpected", Message: "Capabilities match configured spec"},
				{Type: DesiredReleaseAccepted, Status: configv1.ConditionTrue, Reason: "PayloadLoaded",
					Message: "Payload loaded version=\"1.0.0-abc\" image=\"image/image:1\" architecture=\"" + architecture + "\""},
				{Type: configv1.OperatorAvailable, Status: configv1.ConditionFalse},
				{Type: ClusterStatusFailing, Status: configv1.ConditionTrue, Reason: "MultipleErrors", Message: "Multiple errors are preventing progress:\n* Failed to reconcile 10-a-file resource\n* Failed to reconcile 20-b-file resource"},
				{Type: configv1.OperatorProgressing, Status: configv1.ConditionTrue, Reason: "MultipleErrors", Message: "Unable to apply 1.0.0-abc: an unknown error has occurred: MultipleErrors"},
				{Type: configv1.RetrievedUpdates, Status: configv1.ConditionFalse},
			},
		},
	})
}

func TestCVO_VerifyInitializingPayloadState(t *testing.T) {
	ctx := context.Background()
	o, cvs, client, _, shutdownFn := setupCVOTest("testdata/payloadtest")
	stopCh := make(chan struct{})
	defer close(stopCh)
	defer shutdownFn()
	worker := o.configSync.(*SyncWorker)
	b := newBlockingResourceBuilder()
	worker.builder = b

	// Setup: a successful sync from a previous run, and the operator at the same image as before
	//
	o.release.Image = "image/image:1"
	o.release.Version = "1.0.0-abc"
	desired := configv1.Release{Version: "1.0.0-abc", Image: "image/image:1"}
	uid, _ := uuid.NewRandom()
	clusterUID := configv1.ClusterID(uid.String())
	cvs["version"] = &configv1.ClusterVersion{
		ObjectMeta: metav1.ObjectMeta{
			Name:            "version",
			ResourceVersion: "1",
		},
		Spec: configv1.ClusterVersionSpec{
			ClusterID: clusterUID,
			Channel:   "fast",
		},
		Status: configv1.ClusterVersionStatus{
			// Prefers the image version over the operator's version (although in general they will remain in sync)
			Desired:     desired,
			VersionHash: "DL-FFQ2Uem8=",
			History: []configv1.UpdateHistory{
				{State: configv1.PartialUpdate, Image: "image/image:1", Version: "1.0.0-abc", StartedTime: defaultStartedTime},
			},
			Conditions: []configv1.ClusterOperatorStatusCondition{
				{Type: ImplicitlyEnabledCapabilities, Status: "False", Reason: "AsExpected", Message: "Capabilities match configured spec"},
				{Type: configv1.OperatorAvailable, Status: configv1.ConditionTrue, Message: "Done applying 1.0.0-abc"},
				{Type: ClusterStatusFailing, Status: configv1.ConditionFalse},
				{Type: configv1.OperatorProgressing, Status: configv1.ConditionFalse, Message: "Cluster version is 1.0.0-abc"},
				{Type: configv1.RetrievedUpdates, Status: configv1.ConditionFalse},
			},
		},
	}

	// Step 1: The sync loop starts and triggers a sync, but does not update status
	//
	client.ClearActions()
	err := o.sync(ctx, o.queueKey())
	if err != nil {
		t.Fatal(err)
	}

	// check the worker status is initially set to reconciling
	if status := worker.Status(); status.Reconciling || status.Completed != 0 {
		t.Fatalf("The worker should be initializing from the beginning: %#v", status)
	}
	if worker.work.State != payload.InitializingPayload {
		t.Fatalf("The worker should be initializing: %v", worker.work)
	}
}

func TestCVO_VerifyUpdatingPayloadState(t *testing.T) {
	ctx := context.Background()
	o, cvs, client, _, shutdownFn := setupCVOTest("testdata/payloadtest")
	stopCh := make(chan struct{})
	defer close(stopCh)
	defer shutdownFn()
	worker := o.configSync.(*SyncWorker)
	b := newBlockingResourceBuilder()
	worker.builder = b

	// Setup: a successful sync from a previous run, and the operator at the same image as before
	//
	o.release.Image = "image/image:1"
	o.release.Version = "1.0.0-abc"
	desired := configv1.Release{Version: "1.0.0-abc", Image: "image/image:1"}
	uid, _ := uuid.NewRandom()
	clusterUID := configv1.ClusterID(uid.String())
	cvs["version"] = &configv1.ClusterVersion{
		ObjectMeta: metav1.ObjectMeta{
			Name:            "version",
			ResourceVersion: "1",
		},
		Spec: configv1.ClusterVersionSpec{
			ClusterID: clusterUID,
			Channel:   "fast",
		},
		Status: configv1.ClusterVersionStatus{
			// Prefers the image version over the operator's version (although in general they will remain in sync)
			Desired:     desired,
			VersionHash: "DL-FFQ2Uem8=",
			History: []configv1.UpdateHistory{
				{State: configv1.PartialUpdate, Image: "image/image:1", Version: "1.0.0-abc", StartedTime: defaultStartedTime},
				{State: configv1.CompletedUpdate, Image: "image/image:0", Version: "1.0.0-abc.0", StartedTime: defaultStartedTime, CompletionTime: &defaultCompletionTime},
			},
			Conditions: []configv1.ClusterOperatorStatusCondition{
				{Type: ImplicitlyEnabledCapabilities, Status: "False", Reason: "AsExpected", Message: "Capabilities match configured spec"},
				{Type: configv1.OperatorAvailable, Status: configv1.ConditionTrue, Message: "Done applying 1.0.0-abc"},
				{Type: ClusterStatusFailing, Status: configv1.ConditionFalse},
				{Type: configv1.OperatorProgressing, Status: configv1.ConditionFalse, Message: "Cluster version is 1.0.0-abc"},
				{Type: configv1.RetrievedUpdates, Status: configv1.ConditionFalse},
			},
		},
	}

	// Step 1: The sync loop starts and triggers a sync, but does not update status
	//
	client.ClearActions()
	err := o.sync(ctx, o.queueKey())
	if err != nil {
		t.Fatal(err)
	}

	// check the worker status is initially set to reconciling
	if status := worker.Status(); status.Reconciling || status.Completed != 0 {
		t.Fatalf("The worker should be updating from the beginning: %#v", status)
	}
	if worker.work.State != payload.UpdatingPayload {
		t.Fatalf("The worker should be updating: %v", worker.work)
	}
}

// verifyCVSingleUpdate ensures that the only object to be updated is a ClusterVersion type and it is updated only once
func verifyCVSingleUpdate(t *testing.T, actions []clientgotesting.Action) {
	var count int
	for _, a := range actions {
		if a.GetResource().Resource != "clusterversions" {
			t.Fatalf("found an action which accesses/updates resource other than clusterversion: %#v", a)
		}
		if a.GetVerb() != "get" {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("Expected only single update to clusterversion resource. Actual update count %d", count)
	}
}

func waitForPayloadLoaded(status SyncWorkerStatus) bool {
	return status.loadPayloadStatus.Step == "PayloadLoaded"
}

func waitForVerifyPayload(status SyncWorkerStatus) bool {
	return status.loadPayloadStatus.Step == "VerifyPayloadVersion"
}

func waitForCompleted(status SyncWorkerStatus) bool {
	return status.Completed == 1
}

func waitForStatus(t *testing.T, maxLoopCount int, timeOutSeconds time.Duration, ch <-chan SyncWorkerStatus, f func(s SyncWorkerStatus) bool) {
	count := 0
	for {
		var status SyncWorkerStatus
		select {
		case status = <-ch:
		case <-time.After(timeOutSeconds * time.Second):
			t.Fatalf("never saw expected apply event")
		}
		if f(status) {
			break
		}
		t.Log("Waiting for condition")
		count++
		if count > maxLoopCount {
			t.Fatalf("saw too many sync events of the wrong form")
		}
	}
}

func verifyAllStatus(t *testing.T, ch <-chan SyncWorkerStatus, items ...SyncWorkerStatus) {
	verifyAllStatusOptionalDone(t, "", false, ch, items...)
}

func clearAllStatusWithWait(t *testing.T, name string, timeOutSeconds time.Duration, ch <-chan SyncWorkerStatus) {
	testName := t.Name() + ":" + name
	t.Helper()
	t.Logf("%s: Clearing all status...", testName)
	select {
	case <-ch:
	case <-time.After(timeOutSeconds * time.Second):
		break
	}
}

// Since timing can be non-deterministic, use this instead of verifyAllStatus when
// don't know or care how many are done.
func verifyAllStatusOptionalDone(t *testing.T, stepName string, ignoreDone bool, ch <-chan SyncWorkerStatus, items ...SyncWorkerStatus) {

	testName := t.Name()
	if stepName != "" {
		testName = t.Name() + ":" + stepName
	}

	if len(items) == 0 {
		if len(ch) > 0 {
			t.Fatalf("%s: expected status to be empty, got %#v", testName, <-ch)
		}
		return
	}
	var lastTime time.Time
	count := int64(1)
	count2 := int64(1)
	for i, expect := range items {
		actual, ok := <-ch
		if !ok {
			t.Fatalf("%s: channel closed after reading only %d items", testName, i)
		}

		if nextTime := actual.LastProgress; !nextTime.Equal(lastTime) {
			actual.LastProgress = time.Unix(count, 0)
			count++
		} else if !lastTime.IsZero() {
			actual.LastProgress = time.Unix(count, 0)
		}

		lastTime = time.Unix(0, 0)
		if nextTime := actual.loadPayloadStatus.LastTransitionTime; !nextTime.Equal(lastTime) {
			actual.loadPayloadStatus.LastTransitionTime = time.Unix(count2, 0)
			count2++
		} else if !lastTime.IsZero() {
			actual.loadPayloadStatus.LastTransitionTime = time.Unix(count2, 0)
		}
		if ignoreDone {
			expect.Done = actual.Done
		}

		sort.Slice(actual.CapabilitiesStatus.ImplicitlyEnabledCaps, func(i, j int) bool {
			return actual.CapabilitiesStatus.ImplicitlyEnabledCaps[i] < actual.CapabilitiesStatus.ImplicitlyEnabledCaps[j]
		})

		if !reflect.DeepEqual(expect, actual) {
			t.Fatalf("%s: unexpected status item %d\nExpected: %#v\nActual: %#v", testName, i, expect, actual)
		}
	}
}

func finalStatusIndicatorCompleted(status SyncWorkerStatus) bool {
	return status.Completed == 3
}

func acceptedRisksPopulated(status SyncWorkerStatus) bool {
	return status.loadPayloadStatus.AcceptedRisks != ""
}

func verifyFinalStatus(t *testing.T, name string, maxLoopCount int, ignoreFields bool, ch <-chan SyncWorkerStatus,
	item SyncWorkerStatus, f func(SyncWorkerStatus) bool) {

	testName := t.Name() + ":" + name

	var status SyncWorkerStatus
	found := false
	for i := 0; i < maxLoopCount; i++ {
		select {
		case status = <-ch:
			if f(status) {
				if ignoreFields {
					status = setIgnoredFields(status, item)
				}
				if !reflect.DeepEqual(status, item) {
					t.Fatalf("%s: expected status not equal to actual:\n%#v\n%#v", testName, item, status)
				}
				found = true
			}
		case <-time.After(1 * time.Second):
		}
		if found {
			break
		}
	}
	if !found {
		t.Fatalf("%s: expected status was not found, expected:\n%#v", testName, item)
	}
}

func setIgnoredFields(status SyncWorkerStatus, setTo SyncWorkerStatus) SyncWorkerStatus {
	status.LastProgress = setTo.LastProgress
	status.loadPayloadStatus.LastTransitionTime = setTo.loadPayloadStatus.LastTransitionTime
	return status
}

// blockingResourceBuilder controls how quickly Apply() is executed and allows error
// injection.
type blockingResourceBuilder struct {
	ch chan error
}

func newBlockingResourceBuilder() *blockingResourceBuilder {
	return &blockingResourceBuilder{
		ch: make(chan error),
	}
}

func (b *blockingResourceBuilder) Send(err error) {
	b.ch <- err
}

func (b *blockingResourceBuilder) Apply(ctx context.Context, m *manifest.Manifest, state payload.State) error {
	return <-b.ch
}

type errorResourceBuilder struct {
	errors map[string]error
}

func (b *errorResourceBuilder) Apply(ctx context.Context, m *manifest.Manifest, state payload.State) error {
	if err, ok := b.errors[m.OriginalFilename]; ok {
		return err
	}
	return fmt.Errorf("unknown file %s", m.OriginalFilename)
}

// wait for status completed
func waitForStatusCompleted(t *testing.T, worker *SyncWorker) {
	count := 0
	for {
		var status SyncWorkerStatus
		select {
		case status = <-worker.StatusCh():
		case <-time.After(3 * time.Second):
			t.Fatalf("never saw status Completed > 0")
		}
		if status.Completed > 0 {
			break
		}
		t.Log("Waiting for Completed > 0")
		count++
		if count > 8 {
			t.Fatalf("saw too many sync events of the wrong form")
		}
	}
}

func clearAllStatus(t *testing.T, ch <-chan SyncWorkerStatus) {
	count := 0
	for {
		if len(ch) <= 0 {
			break
		}
		<-ch
		t.Log("Waiting for SyncWorkerStatus to clear")
		count++
		if count > 8 {
			t.Fatalf("Waited too long for SyncWorkerStatus to clear")
		}
	}
}

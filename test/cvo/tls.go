package cvo

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	g "github.com/onsi/ginkgo/v2"
	o "github.com/onsi/gomega"
	"github.com/openshift/cluster-version-operator/pkg/tls"

	corev1 "k8s.io/api/core/v1"
	kerrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"

	oteginkgo "github.com/openshift-eng/openshift-tests-extension/pkg/ginkgo"
	configv1 "github.com/openshift/api/config/v1"
	configv1client "github.com/openshift/client-go/config/clientset/versioned/typed/config/v1"
	routev1client "github.com/openshift/client-go/route/clientset/versioned"
	tlsprofile "github.com/openshift/controller-runtime-common/pkg/tls"
	"github.com/openshift/library-go/pkg/crypto"

	"github.com/openshift/cluster-version-operator/pkg/external"
	"github.com/openshift/cluster-version-operator/test/oc"
	ocapi "github.com/openshift/cluster-version-operator/test/oc/api"
	"github.com/openshift/cluster-version-operator/test/util"
)

var _ = g.Describe(`[Jira:"Cluster Version Operator"] cluster-version-operator`, func() {

	var (
		c            *rest.Config
		kubeClient   kubernetes.Interface
		configClient *configv1client.ConfigV1Client
		routeClient  *routev1client.Clientset
		ocClient     ocapi.OC
		err          error

		ctx         = context.Background()
		needRecover bool
		backup      configv1.APIServerSpec

		prometheusURL, bearerToken string
		waitStable                 bool
	)

	g.BeforeEach(func() {
		c, err = util.GetRestConfig()
		o.Expect(err).To(o.BeNil())

		o.Expect(util.SkipIfHypershift(ctx, c)).To(o.BeNil())
		o.Expect(util.SkipIfMicroshift(ctx, c)).To(o.BeNil())

		kubeClient, err = util.GetKubeClient(c)
		o.Expect(err).NotTo(o.HaveOccurred())

		configClient, err = configv1client.NewForConfig(c)
		o.Expect(err).To(o.BeNil())

		routeClient, err = routev1client.NewForConfig(c)
		o.Expect(err).To(o.BeNil())

		waitStable = strings.ToLower(os.Getenv("WAIT_STABLE")) == "true"

		timeout := 2 * time.Minute
		if waitStable {
			timeout = 61 * time.Minute
		}
		ocClient, err = oc.NewOC(ocapi.Options{Logger: logger, Timeout: timeout})
		o.Expect(err).NotTo(o.HaveOccurred())
		o.Expect(ocClient).NotTo(o.BeNil())

		if waitStable {
			// check if cluster is stable before testing
			_, err = ocClient.AdmWaitForStableCluster("1m0s", "5m0s")
			o.Expect(err).NotTo(o.HaveOccurred(), "The cluster isn't stable before testing")
		}

		prometheusURL, err = util.PrometheusRouteURL(ctx, routeClient)
		o.Expect(err).NotTo(o.HaveOccurred(), "Failed to get public url of prometheus")
		bearerToken, err = util.RequestPrometheusServiceAccountAPIToken(ctx, kubeClient)
		o.Expect(err).NotTo(o.HaveOccurred(), "Failed to request Prometheus service account API token")

		apiServer, err := configClient.APIServers().Get(ctx, tlsprofile.APIServerName, metav1.GetOptions{})
		o.Expect(err).NotTo(o.HaveOccurred())
		backup = *apiServer.Spec.DeepCopy()
		if backup.TLSAdherence == "" {
			backup.TLSAdherence = configv1.TLSAdherencePolicyLegacyAdheringComponentsOnly
		}
	})

	g.AfterEach(func() {
		if needRecover {
			apiServer, err := configClient.APIServers().Get(ctx, tlsprofile.APIServerName, metav1.GetOptions{})
			o.Expect(err).NotTo(o.HaveOccurred())
			apiServer.Spec = backup
			_, err = configClient.APIServers().Update(ctx, apiServer, metav1.UpdateOptions{})
			o.Expect(err).NotTo(o.HaveOccurred())

			if waitStable {
				// wait before handing the cluster over to other tests
				_, err = ocClient.AdmWaitForStableCluster("5m0s", "1h0m0s")
				o.Expect(err).NotTo(o.HaveOccurred())
			}
		}
	})

	// Automate the manual verification in https://github.com/openshift/cluster-version-operator/pull/1338#issuecomment-4593397211
	g.It("must get the APIServer when the TLS profile manager is created", oteginkgo.Informing(), func() {
		g.By("Checking if the APIServer exists on the cluster")
		_, err := configClient.APIServers().Get(ctx, tlsprofile.APIServerName, metav1.GetOptions{})
		if !kerrors.IsNotFound(err) {
			o.Expect(err).NotTo(o.HaveOccurred())
		} else {
			g.Skip("Skipping test: APIServer/cluster not found on the cluster")
		}

		g.By("Checking if CVO failed to load the APIServer when the TLS profile manager is created")
		podList, err := kubeClient.CoreV1().Pods(external.DefaultCVONamespace).List(ctx, metav1.ListOptions{
			LabelSelector: "k8s-app=cluster-version-operator",
		})
		o.Expect(err).NotTo(o.HaveOccurred())

		var podName string
		for _, pod := range podList.Items {
			podName = pod.Name
			break
		}
		o.Expect(podName).NotTo(o.BeEmpty(), "Failed to find the CVO pod")

		req := kubeClient.CoreV1().Pods(external.DefaultCVONamespace).GetLogs(podName, &corev1.PodLogOptions{
			Follow: false,
		})

		podStream, err := req.Stream(ctx)
		o.Expect(err).NotTo(o.HaveOccurred())
		defer func() {
			err := podStream.Close()
			o.Expect(err).NotTo(o.HaveOccurred())
		}()

		buf := new(strings.Builder)
		_, err = io.Copy(buf, podStream)
		o.Expect(err).NotTo(o.HaveOccurred())
		o.Expect(strings.Contains(buf.String(), tls.APIServerNotAvailableAtStartupLogKeyword)).To(o.BeFalse())
	})

	// Local as it updates APIServer/cluster on the cluster which is very destructive and impacts many monitor tests
	g.It("should update TLS profile", g.Label("Local"), g.Label("OTA-1996"), func() {

		controlPlaneTopology, err := util.GetControlPlaneTopology(ctx, configClient)
		o.Expect(err).NotTo(o.HaveOccurred())
		if controlPlaneTopology == configv1.ExternalTopologyMode {
			g.Skip("Skipping test: running on External cluster!")
		}

		g.By("Checking if the CVO target is up in Prometheus")

		promTargets := func() (*prometheusTargets, error) {
			contents, err := util.GetURLWithToken(util.MustJoinUrlPath(prometheusURL, "api/v1/targets"), bearerToken)
			if err != nil {
				return nil, err
			}
			targets := &prometheusTargets{}
			err = json.Unmarshal([]byte(contents), targets)
			if err != nil {
				return nil, err
			}
			// sanity check.
			if len(targets.Data.ActiveTargets) < 5 {
				return nil, fmt.Errorf("only got %d targets, something is wrong", len(targets.Data.ActiveTargets))
			}
			return targets, nil
		}

		targets, err := promTargets()
		o.Expect(err).NotTo(o.HaveOccurred())
		// ref. https://github.com/openshift/origin/blob/f4d1c208855b7216452041276a7f909c3cf477ce/test/extended/prometheus/prometheus.go#L722
		err = targets.Expect(labels{"job": "cluster-version-operator"}, "up", "^https://.*/metrics$")
		o.Expect(err).NotTo(o.HaveOccurred())

		g.By("Setting up modern TLS profile and strict TLS adherence")
		t := time.Now()
		apiServer, err := configClient.APIServers().Get(ctx, tlsprofile.APIServerName, metav1.GetOptions{})
		o.Expect(err).NotTo(o.HaveOccurred())
		apiServer.Spec.TLSAdherence = configv1.TLSAdherencePolicyStrictAllComponents
		apiServer.Spec.TLSSecurityProfile = &configv1.TLSSecurityProfile{
			Type:   configv1.TLSProfileModernType,
			Modern: &configv1.ModernTLSProfile{},
		}

		_, err = configClient.APIServers().Update(ctx, apiServer, metav1.UpdateOptions{})
		o.Expect(err).NotTo(o.HaveOccurred())
		needRecover = true

		g.By("Waiting for the cluster to stabilize")
		// It takes too long in CI to wait until the cluster is stable
		// co/authentication is about 5-8 mins
		// co/openshift-apiserver is about 50 - 60 mins
		if waitStable {
			_, err = ocClient.AdmWaitForStableCluster("5m0s", "1h0m0s")
			o.Expect(err).NotTo(o.HaveOccurred())
		} else {
			logger.Info("Did not waiting for the cluster to stabilize after updating API server", "waitStable", waitStable)
		}

		g.By("Checking if the CVO target is still up in Prometheus")
		count := 1
		if !waitStable {
			// checking 3 times in total; 30s once
			count = 3
		}
		for i := 0; i < count; i++ {
			if !waitStable {
				time.Sleep(30 * time.Second)
			}
			var errUp error
			errWait := wait.PollUntilContextTimeout(context.Background(), 10*time.Second, 2*time.Minute, true, func(context.Context) (bool, error) {
				targets, err = promTargets()
				o.Expect(err).NotTo(o.HaveOccurred())
				errUp = targets.Expect(labels{"job": "cluster-version-operator"}, "up", "^https://.*/metrics$")
				if errUp != nil {
					logger.Error(errUp, "The CVO target is not up in Prometheus, retrying...", "count", i)
				}
				return errUp == nil, nil
			})
			o.Expect(errWait).NotTo(o.HaveOccurred(), "The CVO target is not up in Prometheus with count=%d and errUp=%v", i, errUp)
			logger.Info("The CVO target is still up in Prometheus", "count", i, "at", time.Now().Format(time.RFC3339))
		}

		g.By("Checking if CVO updates TLS profile")
		podList, err := kubeClient.CoreV1().Pods(external.DefaultCVONamespace).List(ctx, metav1.ListOptions{
			LabelSelector: "k8s-app=cluster-version-operator",
		})
		o.Expect(err).NotTo(o.HaveOccurred())

		var podName string
		for _, pod := range podList.Items {
			podName = pod.Name
			break
		}
		o.Expect(podName).NotTo(o.BeEmpty(), "Failed to find the CVO pod")

		req := kubeClient.CoreV1().Pods(external.DefaultCVONamespace).GetLogs(podName, &corev1.PodLogOptions{
			Follow:     false,
			Timestamps: true,
		})

		podStream, err := req.Stream(ctx)
		o.Expect(err).NotTo(o.HaveOccurred())
		defer func() {
			err := podStream.Close()
			o.Expect(err).NotTo(o.HaveOccurred())
		}()

		buf := new(strings.Builder)
		_, err = io.Copy(buf, podStream)
		o.Expect(err).NotTo(o.HaveOccurred())

		scanner := bufio.NewScanner(strings.NewReader(buf.String()))
		var found bool
		for scanner.Scan() {
			line := scanner.Text()
			if strings.Contains(line, tls.SyncedCachedTLSProfileLogKeyword) {
				if timeInLog, logMessage, err := parseLogTimestamp(line); err == nil && timeInLog.After(t) {
					logger.Info("Found log", "logMessage", logMessage, "timestamp", timeInLog.Format(time.RFC3339))
					found = true
					break
				}

			}
		}
		o.Expect(found).To(o.BeTrue(), "Failed to find logs about updating TCP profile when ShouldHonorClusterTLSProfile=%t after %s",
			crypto.ShouldHonorClusterTLSProfile(apiServer.Spec.TLSAdherence), t.Format(time.RFC3339))
	})
})

func parseLogTimestamp(logLine string) (time.Time, string, error) {
	// 1. Split the line by the first space to separate the timestamp from the message
	parts := strings.SplitN(logLine, " ", 2)
	if len(parts) < 2 {
		return time.Time{}, "", fmt.Errorf("invalid log format, no space separator found")
	}

	timestampStr := parts[0]
	logMessage := parts[1]

	// 2. Parse the timestamp using the RFC3339Nano layout
	t, err := time.Parse(time.RFC3339Nano, timestampStr)
	if err != nil {
		// Fallback: Try standard RFC3339 if Nano fails for some reason
		t, err = time.Parse(time.RFC3339, timestampStr)
		if err != nil {
			return time.Time{}, "", fmt.Errorf("failed to parse timestamp '%s': %w", timestampStr, err)
		}
	}

	return t, logMessage, nil
}

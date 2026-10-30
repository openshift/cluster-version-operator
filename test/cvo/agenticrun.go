package cvo

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	g "github.com/onsi/ginkgo/v2"
	o "github.com/onsi/gomega"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/config"

	kerrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"

	oteginkgo "github.com/openshift-eng/openshift-tests-extension/pkg/ginkgo"
	configv1 "github.com/openshift/api/config/v1"
	configv1client "github.com/openshift/client-go/config/clientset/versioned/typed/config/v1"
	agenticrunv1alpha1 "github.com/openshift/lightspeed-agentic-operator/api/v1alpha1"

	"github.com/openshift/cluster-version-operator/pkg/agenticrun"
	"github.com/openshift/cluster-version-operator/pkg/external"
	"github.com/openshift/cluster-version-operator/test/util"
)

func init() {
	err := agenticrunv1alpha1.AddToScheme(scheme.Scheme)
	if err != nil {
		panic(err)
	}
}

var _ = g.Describe(`[Jira:"Cluster Version Operator"] cluster-version-operator`, func() {

	var (
		c            *rest.Config
		kubeClient   kubernetes.Interface
		configClient *configv1client.ConfigV1Client
		rtClient     ctrlruntimeclient.Client
		err          error

		ctx         = context.Background()
		needRecover bool
		backup      configv1.ClusterVersionSpec
	)

	g.BeforeEach(func() {
		c, err = util.GetRestConfig()
		o.Expect(err).To(o.BeNil())

		o.Expect(util.SkipIfHypershift(ctx, c)).To(o.BeNil())
		o.Expect(util.SkipIfMicroshift(ctx, c)).To(o.BeNil())

		kubeClient, err = util.GetKubeClient(c)
		o.Expect(err).To(o.BeNil())
		configClient, err = configv1client.NewForConfig(c)
		o.Expect(err).To(o.BeNil())

		rtClient, err = ctrlruntimeclient.New(config.GetConfigOrDie(), ctrlruntimeclient.Options{})
		o.Expect(err).To(o.BeNil())

		cv, err := configClient.ClusterVersions().Get(ctx, external.DefaultClusterVersionName, metav1.GetOptions{})
		o.Expect(err).NotTo(o.HaveOccurred())
		if du := cv.Spec.DesiredUpdate; du != nil {
			logger.WithValues("AcceptRisks", du.AcceptRisks).Info("Accept risks before testing")
			o.Expect(du.AcceptRisks).To(o.BeEmpty(), "found accept risks")
		}
		backup = *cv.Spec.DeepCopy()
	})

	g.AfterEach(func() {
		if needRecover {
			cv, err := configClient.ClusterVersions().Get(ctx, external.DefaultClusterVersionName, metav1.GetOptions{})
			o.Expect(err).NotTo(o.HaveOccurred())
			cv.Spec = backup
			_, err = configClient.ClusterVersions().Update(ctx, cv, metav1.UpdateOptions{})
			o.Expect(err).NotTo(o.HaveOccurred())
		}
	})

	g.It("should create agentic runs", g.Label("OTA-1966"), g.Label("Serial"), oteginkgo.Informing(), func() {
		o.Expect(util.SkipIfNetworkRestricted(ctx, c, util.FauxinnatiAPIURL)).To(o.BeNil())
		util.SkipIfNoAPI(ctx, c, "agenticruns.agentic.openshift.io")

		cv, err := configClient.ClusterVersions().Get(ctx, external.DefaultClusterVersionName, metav1.GetOptions{})
		o.Expect(err).NotTo(o.HaveOccurred())

		g.By("Using fauxinnati as the upstream and its simple channel")
		cv.Spec.Upstream = util.FauxinnatiAPIURL
		channel := "simple"
		cv.Spec.Channel = channel

		needRecover = true
		_, err = configClient.ClusterVersions().Update(ctx, cv, metav1.UpdateOptions{})
		o.Expect(err).NotTo(o.HaveOccurred())

		g.By("Checking if the namespace exists")
		_, err = kubeClient.CoreV1().Namespaces().Get(ctx, agenticrun.DefaultConfig().Namespace, metav1.GetOptions{})
		o.Expect(err).NotTo(o.HaveOccurred())

		g.By("Checking if the agentic runs are created")
		var agenticRuns agenticrunv1alpha1.AgenticRunList
		var created *agenticrunv1alpha1.AgenticRun
		o.Expect(wait.PollUntilContextTimeout(ctx, 30*time.Second, 5*time.Minute, true, func(ctx context.Context) (done bool, err error) {
			cv, err := configClient.ClusterVersions().Get(ctx, external.DefaultClusterVersionName, metav1.GetOptions{})
			if err != nil {
				return false, err
			}
			targets := map[string]bool{}
			for _, update := range cv.Status.AvailableUpdates {
				if slices.Contains(update.Channels, channel) {
					targets[update.Version] = true
				}
			}
			for _, update := range cv.Status.ConditionalUpdates {
				if slices.Contains(update.Release.Channels, channel) {
					targets[update.Release.Version] = true
				}
			}
			agenticRuns = agenticrunv1alpha1.AgenticRunList{}
			err = rtClient.List(ctx, &agenticRuns, ctrlruntimeclient.InNamespace(agenticrun.DefaultConfig().Namespace),
				ctrlruntimeclient.MatchingLabels(agenticrun.CVOAgenticRunLabels))
			o.Expect(err).NotTo(o.HaveOccurred())
			for _, ar := range agenticRuns.Items {
				// Fresh runs for an unchanged version pair are deliberately reused
				// across channel changes. They are valid candidates for re-analysis.
				if ar.DeletionTimestamp.IsZero() && ar.Labels["agentic.openshift.io/current-version"] == cv.Status.Desired.Version && targets[ar.Labels["agentic.openshift.io/target-version"]] {
					created = ar.DeepCopy()
					return true, nil
				}
			}
			return false, nil
		})).NotTo(o.HaveOccurred(), "expected agentic runs not found; agentic runs are:\n%s", getYaml(&agenticRuns))

		g.By("Deleting an assessment as the console Re-analyse action does")
		key := ctrlruntimeclient.ObjectKeyFromObject(created)
		deletedAt := time.Now()
		o.Expect(rtClient.Delete(ctx, created, ctrlruntimeclient.Preconditions{UID: &created.UID})).To(o.Succeed())

		g.By("Recreating the same update path promptly with a new run identity")
		// Shorter than the default minimum update-check interval (two minutes).
		o.Expect(wait.PollUntilContextTimeout(ctx, time.Second, 90*time.Second, true, func(ctx context.Context) (bool, error) {
			replacement := &agenticrunv1alpha1.AgenticRun{}
			if err := rtClient.Get(ctx, key, replacement); err != nil {
				if kerrors.IsNotFound(err) {
					return false, nil
				}
				return false, err
			}
			recreated := replacement.UID != created.UID && replacement.DeletionTimestamp.IsZero() &&
				replacement.Labels["agentic.openshift.io/target-version"] == created.Labels["agentic.openshift.io/target-version"] &&
				strings.Contains(replacement.Spec.Request, fmt.Sprintf("Channel: %s", channel)) &&
				strings.Contains(replacement.Spec.Request, "## Cluster Readiness Data")
			if recreated {
				g.GinkgoLogr.Info("AgenticRun recreated", "name", replacement.Name, "oldUID", created.UID, "newUID", replacement.UID, "elapsed", time.Since(deletedAt).String())
			}
			return recreated, nil
		})).To(o.Succeed(), "deleted assessment was not promptly recreated")
	})
})

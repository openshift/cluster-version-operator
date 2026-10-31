package cvo

import (
	"context"
	"io"
	"strings"
	"time"

	g "github.com/onsi/ginkgo/v2"
	o "github.com/onsi/gomega"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"

	configv1 "github.com/openshift/api/config/v1"
	configv1client "github.com/openshift/client-go/config/clientset/versioned/typed/config/v1"

	"github.com/openshift/cluster-version-operator/pkg/external"
	"github.com/openshift/cluster-version-operator/test/util"
)

const (
	fleetGraphChannel   = "aro-hcp-fleet"
	fleetCannotEvaluate = "FleetCannotEvaluate"
	fleetMatches        = "FleetMatches"
	fleetDoesNotMatch   = "FleetDoesNotMatch"
	fleetPruningLog     = `skipping unrecognized cluster condition type "Fleet"`
)

var _ = g.Describe(`[Jira:"Cluster Version Operator"] cluster-version-operator`, func() {
	var (
		ctx             = context.TODO()
		restConfig      *rest.Config
		kubeClient      kubernetes.Interface
		configClient    *configv1client.ConfigV1Client
		backup          configv1.ClusterVersionSpec
		updateServiceNS string
		needRecover     bool
	)

	g.BeforeEach(func() {
		var err error
		restConfig, err = util.GetRestConfig()
		o.Expect(err).NotTo(o.HaveOccurred())
		kubeClient, err = util.GetKubeClient(restConfig)
		o.Expect(err).NotTo(o.HaveOccurred())
		configClient, err = configv1client.NewForConfig(restConfig)
		o.Expect(err).NotTo(o.HaveOccurred())

		o.Expect(util.SkipIfHypershift(ctx, restConfig)).To(o.BeNil())
		o.Expect(util.SkipIfMicroshift(ctx, restConfig)).To(o.BeNil())
		// A TechPreviewNoUpgrade cluster is not Upgradeable, so CVO adds an
		// always-applying ClusterOperatorUpgradeable risk to every minor or major
		// update of the generated graph, which changes both the number of available
		// updates and the Recommended conditions the checks below expect.
		util.SkipIfTechPreviewNoUpgrade(ctx, restConfig)

		cv, err := configClient.ClusterVersions().Get(ctx, external.DefaultClusterVersionName, metav1.GetOptions{})
		o.Expect(err).NotTo(o.HaveOccurred())
		backup = *cv.Spec.DeepCopy()
		updateServiceNS = ""
		needRecover = false
	})

	g.AfterEach(func() {
		if needRecover {
			var updateErr error
			err := wait.PollUntilContextTimeout(ctx, 10*time.Second, 2*time.Minute, true, func(pollCtx context.Context) (bool, error) {
				cv, err := configClient.ClusterVersions().Get(pollCtx, external.DefaultClusterVersionName, metav1.GetOptions{})
				o.Expect(err).NotTo(o.HaveOccurred())
				cv.Spec = backup
				_, updateErr = configClient.ClusterVersions().Update(pollCtx, cv, metav1.UpdateOptions{})
				if errors.IsConflict(updateErr) {
					return false, nil
				}
				return updateErr == nil, updateErr
			})
			o.Expect(err).NotTo(o.HaveOccurred(), "failed to recover ClusterVersion with lastErr=%v", updateErr)
		}
		if updateServiceNS != "" {
			err := kubeClient.CoreV1().Namespaces().Delete(ctx, updateServiceNS, metav1.DeleteOptions{})
			if !errors.IsNotFound(err) {
				o.Expect(err).NotTo(o.HaveOccurred())
			}
		}
	})

	g.It("should ignore the unrecognized Fleet matching rules in a risk of a conditional update", g.Label("Serial"), func() {
		cv, err := configClient.ClusterVersions().Get(ctx, external.DefaultClusterVersionName, metav1.GetOptions{})
		o.Expect(err).NotTo(o.HaveOccurred())

		g.By("Creating an in-cluster update service with a graph containing risks with matching rules of the Fleet type")
		ns, err := kubeClient.CoreV1().Namespaces().Create(ctx, &corev1.Namespace{
			ObjectMeta: metav1.ObjectMeta{GenerateName: "e2e-cvo-update-service-"},
		}, metav1.CreateOptions{})
		o.Expect(err).NotTo(o.HaveOccurred())
		updateServiceNS = ns.Name

		graph, err := util.GenerateGraph(cv.Status.Desired, fleetGraphChannel)
		o.Expect(err).NotTo(o.HaveOccurred())
		upstream, err := util.RunUpdateService(ctx, kubeClient, updateServiceNS, string(graph))
		o.Expect(err).NotTo(o.HaveOccurred())

		g.By("Pointing ClusterVersion at the in-cluster update service")
		testStart := metav1.NewTime(time.Now())
		needRecover = true
		cv.Spec.Upstream = configv1.URL(upstream.String())
		cv.Spec.Channel = fleetGraphChannel
		_, err = configClient.ClusterVersions().Update(ctx, cv, metav1.UpdateOptions{})
		o.Expect(err).NotTo(o.HaveOccurred())

		g.By("Checking that the Fleet matching rules are pruned and the PromQL conditions are evaluated")
		o.Expect(wait.PollUntilContextTimeout(ctx, 30*time.Second, 5*time.Minute, true, func(ctx context.Context) (bool, error) {
			// Return the error instead of asserting here: a failed assertion inside
			// the closure aborts the spec with its own bare message and the
			// ClusterVersion below is never reported.
			latest, err := configClient.ClusterVersions().Get(ctx, external.DefaultClusterVersionName, metav1.GetOptions{})
			if err != nil {
				return false, err
			}
			cv = latest

			// one conditional update is recommended as NotExposedToRisks and thus joins AvailableUpdates
			if len(cv.Status.AvailableUpdates) != 2 || len(cv.Status.ConditionalUpdates) != 3 {
				return false, nil
			}
			expected := map[string]metav1.ConditionStatus{
				fleetCannotEvaluate: metav1.ConditionUnknown,
				fleetMatches:        metav1.ConditionFalse,
				fleetDoesNotMatch:   metav1.ConditionTrue,
			}
			for _, update := range cv.Status.ConditionalUpdates {
				if len(update.Risks) != 1 || len(update.Risks[0].MatchingRules) != 1 || update.Risks[0].MatchingRules[0].Type != "PromQL" {
					return false, nil
				}
				want, ok := expected[update.Risks[0].Name]
				if !ok {
					return false, nil
				}
				recommended := meta.FindStatusCondition(update.Conditions, external.ConditionalUpdateConditionTypeRecommended)
				if recommended == nil || recommended.Status != want {
					return false, nil
				}
				delete(expected, update.Risks[0].Name)
			}
			return len(expected) == 0, nil
		})).NotTo(o.HaveOccurred(), "expected one available update and three conditional updates with Recommended statuses Unknown (cannot evaluate), False (matches), and True (does not match) after Fleet rules were pruned; ClusterVersion:\n%s", getYaml(cv))

		g.By("Checking CVO logs for the pruned Fleet condition")
		pods, err := kubeClient.CoreV1().Pods(external.DefaultCVONamespace).List(ctx, metav1.ListOptions{
			LabelSelector: "k8s-app=cluster-version-operator",
		})
		o.Expect(err).NotTo(o.HaveOccurred())
		o.Expect(pods.Items).To(o.HaveLen(1), "expected one CVO pod")

		podStream, err := kubeClient.CoreV1().Pods(external.DefaultCVONamespace).GetLogs(pods.Items[0].Name, &corev1.PodLogOptions{
			SinceTime: &testStart,
		}).Stream(ctx)
		o.Expect(err).NotTo(o.HaveOccurred())
		defer func() {
			o.Expect(podStream.Close()).To(o.Succeed())
		}()
		logs, err := io.ReadAll(podStream)
		o.Expect(err).NotTo(o.HaveOccurred())
		o.Expect(strings.Contains(string(logs), fleetPruningLog)).To(o.BeTrue(), "CVO logs did not contain %q:\n%s", fleetPruningLog, logs)
	})
})

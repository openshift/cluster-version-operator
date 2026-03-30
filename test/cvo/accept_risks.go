package cvo

import (
	"context"
	"time"

	g "github.com/onsi/ginkgo/v2"
	o "github.com/onsi/gomega"
	monitoringv1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1"
	prometheusoperatorv1client "github.com/prometheus-operator/prometheus-operator/pkg/client/versioned/typed/monitoring/v1"

	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/rest"

	oteginkgo "github.com/openshift-eng/openshift-tests-extension/pkg/ginkgo"
	configv1 "github.com/openshift/api/config/v1"
	configv1client "github.com/openshift/client-go/config/clientset/versioned/typed/config/v1"

	"github.com/openshift/cluster-version-operator/pkg/external"
	"github.com/openshift/cluster-version-operator/test/util"
)

var _ = g.Describe(`[Jira:"Cluster Version Operator"] cluster-version-operator`, func() {

	var (
		c                *rest.Config
		configClient     *configv1client.ConfigV1Client
		monitoringClient *prometheusoperatorv1client.MonitoringV1Client
		err              error

		ctx         = context.TODO()
		needRecover bool
		backup      configv1.ClusterVersionSpec
	)

	g.BeforeEach(func() {
		c, err = util.GetRestConfig()
		o.Expect(err).To(o.BeNil())
		configClient, err = configv1client.NewForConfig(c)
		o.Expect(err).To(o.BeNil())
		monitoringClient, err = prometheusoperatorv1client.NewForConfig(c)
		o.Expect(err).To(o.BeNil())

		util.SkipIfNotTechPreviewNoUpgrade(ctx, c)
		o.Expect(util.SkipIfHypershift(ctx, c)).To(o.BeNil())
		o.Expect(util.SkipIfMicroshift(ctx, c)).To(o.BeNil())

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

	g.It("should work with risks from alerts", g.Label("OTA-1813"), g.Label("Serial"), oteginkgo.Informing(), func() {
		// This test case relies on a public service util.FauxinnatiAPIURL
		o.Expect(util.SkipIfNetworkRestricted(ctx, c, util.FauxinnatiAPIURL)).To(o.BeNil())

		cv, err := configClient.ClusterVersions().Get(ctx, external.DefaultClusterVersionName, metav1.GetOptions{})
		o.Expect(err).NotTo(o.HaveOccurred())

		g.By("Using fauxinnati as the upstream and its simple channel")
		cv.Spec.Upstream = util.FauxinnatiAPIURL
		cv.Spec.Channel = "simple"

		_, err = configClient.ClusterVersions().Update(ctx, cv, metav1.UpdateOptions{})
		o.Expect(err).NotTo(o.HaveOccurred())
		needRecover = true

		g.By("Create a critical alert for testing")
		prometheusRule := &monitoringv1.PrometheusRule{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "testing",
				Namespace: external.DefaultCVONamespace,
			},
			Spec: monitoringv1.PrometheusRuleSpec{
				Groups: []monitoringv1.RuleGroup{
					{
						Name: "test",
						Rules: []monitoringv1.Rule{
							{
								Alert:       "TestAlertFeatureE2ETestOTA1813",
								Annotations: map[string]string{"summary": "Test summary.", "description": "Test description."},
								Expr: intstr.IntOrString{
									Type:   intstr.String,
									StrVal: `up{job="cluster-version-operator"} == 1`,
								},
								Labels: map[string]string{"severity": "critical", "namespace": "openshift-cluster-version", "openShiftUpdatePrecheck": "true"},
							},
						},
					},
				},
			},
		}
		created, err := monitoringClient.PrometheusRules(external.DefaultCVONamespace).Create(ctx, prometheusRule, metav1.CreateOptions{})
		o.Expect(err).NotTo(o.HaveOccurred())
		defer func() {
			err := monitoringClient.PrometheusRules(external.DefaultCVONamespace).Delete(ctx, created.Name, metav1.DeleteOptions{})
			if !errors.IsNotFound(err) {
				o.Expect(err).To(o.BeNil())
			}
		}()

		g.By("Checking if the risk shows up in ClusterVersion's status")
		o.Expect(wait.PollUntilContextTimeout(ctx, 30*time.Second, 10*time.Minute, true, func(ctx context.Context) (done bool, err error) {
			cv, err := configClient.ClusterVersions().Get(ctx, external.DefaultClusterVersionName, metav1.GetOptions{})
			o.Expect(err).NotTo(o.HaveOccurred())
			for _, risk := range cv.Status.ConditionalUpdateRisks {
				if risk.Name == "TestAlertFeatureE2ETestOTA1813" {
					if c := meta.FindStatusCondition(risk.Conditions, external.ConditionalUpdateRiskConditionTypeApplies); c != nil {
						if c.Status == metav1.ConditionTrue && external.IsAlertConditionReason(c.Reason) {
							return true, nil
						}
					}
				}
			}
			return false, nil
		})).NotTo(o.HaveOccurred(), "no conditional update risk from alert found in ClusterVersion's status")

		g.By("Checking that no updates is recommended if alert is firing")
		o.Expect(wait.PollUntilContextTimeout(ctx, 30*time.Second, 5*time.Minute, true, func(ctx context.Context) (done bool, err error) {
			cv, err := configClient.ClusterVersions().Get(ctx, external.DefaultClusterVersionName, metav1.GetOptions{})
			o.Expect(err).NotTo(o.HaveOccurred())
			if len(cv.Status.AvailableUpdates) > 0 {
				return false, nil
			}
			for _, cu := range cv.Status.ConditionalUpdates {
				condition := meta.FindStatusCondition(cu.Conditions, external.ConditionalUpdateConditionTypeRecommended)
				if condition == nil || condition.Status == metav1.ConditionTrue || condition.Status == metav1.ConditionUnknown {
					return false, nil
				}
			}
			return true, nil
		})).NotTo(o.HaveOccurred(), "still recommending updates while alert is firing")
	})

	g.It("should work with accept risks", g.Label("Serial"), func() {
		// This test case relies on a public service util.FauxinnatiAPIURL
		o.Expect(util.SkipIfNetworkRestricted(ctx, c, util.FauxinnatiAPIURL)).To(o.BeNil())

		cv, err := configClient.ClusterVersions().Get(ctx, external.DefaultClusterVersionName, metav1.GetOptions{})
		o.Expect(err).NotTo(o.HaveOccurred())

		g.By("Using fauxinnati as the upstream and its risks-always channel")
		cv.Spec.Upstream = util.FauxinnatiAPIURL
		cv.Spec.Channel = "risks-always"

		_, err = configClient.ClusterVersions().Update(ctx, cv, metav1.UpdateOptions{})
		o.Expect(err).NotTo(o.HaveOccurred())
		needRecover = true

		g.By("Checking that conditional updates shows up in status")
		// waiting for the conditional updates to show up
		o.Expect(wait.PollUntilContextTimeout(ctx, 30*time.Second, 5*time.Minute, true, func(ctx context.Context) (done bool, err error) {
			cv, err = configClient.ClusterVersions().Get(ctx, external.DefaultClusterVersionName, metav1.GetOptions{})
			o.Expect(err).NotTo(o.HaveOccurred())
			if len(cv.Status.ConditionalUpdates) == 0 {
				return false, nil
			}
			return true, nil
		})).NotTo(o.HaveOccurred(), "no conditional updates found in status")

		g.By("Checking that no conditional updates are recommended")
		conditionalUpdatesLength := len(cv.Status.ConditionalUpdates)
		var acceptRisks []configv1.AcceptRisk
		var releases []configv1.Release
		names := sets.New[string]()
		for _, cu := range cv.Status.ConditionalUpdates {
			releases = append(releases, cu.Release)
			o.Expect(cu.RiskNames).NotTo(o.BeEmpty())
			for _, name := range cu.RiskNames {
				if names.Has(name) {
					continue
				}
				names.Insert(name)
				acceptRisks = append(acceptRisks, configv1.AcceptRisk{Name: name})
			}
			o.Expect(cu.Risks).NotTo(o.BeEmpty())
			recommendedCondition := meta.FindStatusCondition(cu.Conditions, external.ConditionalUpdateConditionTypeRecommended)
			o.Expect(recommendedCondition).NotTo(o.BeNil())
			o.Expect(recommendedCondition.Status).To(o.Equal(metav1.ConditionFalse))
		}

		g.By("Accepting all risks")
		o.Expect(acceptRisks).NotTo(o.BeEmpty())
		if cv.Spec.DesiredUpdate == nil {
			cv.Spec.DesiredUpdate = &configv1.Update{}
		}
		cv.Spec.DesiredUpdate.AcceptRisks = acceptRisks

		_, err = configClient.ClusterVersions().Update(ctx, cv, metav1.UpdateOptions{})
		o.Expect(err).NotTo(o.HaveOccurred())

		g.By("Checking that all conditional updates are recommended")
		var releasesNow []configv1.Release
		// waiting for the conditional updates to be refreshed
		o.Expect(wait.PollUntilContextTimeout(ctx, 30*time.Second, 5*time.Minute, true, func(ctx context.Context) (done bool, err error) {
			cv, err = configClient.ClusterVersions().Get(ctx, external.DefaultClusterVersionName, metav1.GetOptions{})
			o.Expect(err).NotTo(o.HaveOccurred())
			o.Expect(cv.Status.ConditionalUpdates).NotTo(o.BeEmpty())
			releasesNow = nil
			for _, cu := range cv.Status.ConditionalUpdates {
				releasesNow = append(releasesNow, cu.Release)
				recommendedCondition := meta.FindStatusCondition(cu.Conditions, external.ConditionalUpdateConditionTypeRecommended)
				o.Expect(recommendedCondition).NotTo(o.BeNil())
				if recommendedCondition.Status != metav1.ConditionTrue {
					return false, nil
				}
			}
			return true, nil
		})).NotTo(o.HaveOccurred(), "no conditional updates are recommended in status after accepting risks")

		o.Expect(cv.Spec.DesiredUpdate.AcceptRisks).To(o.Equal(acceptRisks))
		o.Expect(cv.Status.ConditionalUpdates).To(o.HaveLen(conditionalUpdatesLength))
		o.Expect(releasesNow).To(o.Equal(releases))
	})

	g.It("should install prometheus rules correctly", func() {
		_, err = monitoringClient.PrometheusRules(external.DefaultCVONamespace).Get(ctx, "cluster-version-operator-accept-risks", metav1.GetOptions{})
		if err != nil {
			o.Expect(err).NotTo(o.HaveOccurred())
		}
	})
})

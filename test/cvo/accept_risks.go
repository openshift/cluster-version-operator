package cvo

import (
	"context"
	"time"

	g "github.com/onsi/ginkgo/v2"
	o "github.com/onsi/gomega"

	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/rest"

	configv1 "github.com/openshift/api/config/v1"
	configv1client "github.com/openshift/client-go/config/clientset/versioned/typed/config/v1"

	"github.com/openshift/cluster-version-operator/pkg/external"
	"github.com/openshift/cluster-version-operator/test/util"
)

var _ = g.Describe(`[Jira:"Cluster Version Operator"] cluster-version-operator`, func() {

	var (
		c            *rest.Config
		configClient *configv1client.ConfigV1Client
		err          error

		ctx         = context.TODO()
		needRecover bool
		backup      configv1.ClusterVersionSpec
	)

	g.BeforeEach(func() {
		c, err = util.GetRestConfig()
		o.Expect(err).To(o.BeNil())
		configClient, err = configv1client.NewForConfig(c)
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

	g.It("should work with accept risks", g.Label("Serial"), func() {
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
			cv.Spec.DesiredUpdate = &configv1.Update{
				Image:        cv.Status.Desired.Image,
				Version:      cv.Status.Desired.Version,
				Architecture: cv.Status.Desired.Architecture,
			}
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
})

package cvo

import (
	"context"
	"time"

	g "github.com/onsi/ginkgo/v2"
	o "github.com/onsi/gomega"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/config"

	apiextensionsclientset "k8s.io/apiextensions-apiserver/pkg/client/clientset/clientset"
	kerrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"

	oteginkgo "github.com/openshift-eng/openshift-tests-extension/pkg/ginkgo"
	configv1 "github.com/openshift/api/config/v1"
	configv1client "github.com/openshift/client-go/config/clientset/versioned/typed/config/v1"

	"github.com/openshift/cluster-version-operator/pkg/external"
	proposalv1alpha1 "github.com/openshift/cluster-version-operator/pkg/proposal/api/v1alpha1"
	"github.com/openshift/cluster-version-operator/test/util"
)

func init() {
	err := proposalv1alpha1.AddToScheme(scheme.Scheme)
	if err != nil {
		panic(err)
	}
}

var _ = g.Describe(`[Jira:"Cluster Version Operator"] cluster-version-operator`, func() {

	var (
		c                   *rest.Config
		configClient        *configv1client.ConfigV1Client
		apiExtensionsClient apiextensionsclientset.Interface
		rtClient            ctrlruntimeclient.Client
		err                 error

		ctx         = context.Background()
		needRecover bool
		backup      configv1.ClusterVersionSpec
	)

	g.BeforeEach(func() {
		c, err = util.GetRestConfig()
		o.Expect(err).To(o.BeNil())

		o.Expect(util.SkipIfHypershift(ctx, c)).To(o.BeNil())
		o.Expect(util.SkipIfMicroshift(ctx, c)).To(o.BeNil())

		configClient, err = configv1client.NewForConfig(c)
		o.Expect(err).To(o.BeNil())

		apiExtensionsClient, err = apiextensionsclientset.NewForConfig(c)
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

	g.It("should install light speed CRDs correctly", func() {
		for _, name := range []string{"proposals.agentic.openshift.io", "agents.agentic.openshift.io", "workflows.agentic.openshift.io"} {
			_, err := apiExtensionsClient.ApiextensionsV1().CustomResourceDefinitions().Get(ctx, name, metav1.GetOptions{})
			if util.IsTechPreviewNoUpgrade(ctx, c) {
				o.Expect(err).To(o.BeNil())
			} else {
				o.Expect(kerrors.IsNotFound(err)).To(o.BeTrue())
			}
		}
	})

	g.It("should create proposals", g.Label("Serial"), oteginkgo.Informing(), func() {
		o.Expect(util.SkipIfNetworkRestricted(ctx, c, util.FauxinnatiAPIURL)).To(o.BeNil())
		util.SkipIfNotTechPreviewNoUpgrade(ctx, c)

		cv, err := configClient.ClusterVersions().Get(ctx, external.DefaultClusterVersionName, metav1.GetOptions{})
		o.Expect(err).NotTo(o.HaveOccurred())

		g.By("Using fauxinnati as the upstream and its simple channel")
		cv.Spec.Upstream = util.FauxinnatiAPIURL
		cv.Spec.Channel = "simple"

		_, err = configClient.ClusterVersions().Update(ctx, cv, metav1.UpdateOptions{})
		o.Expect(err).NotTo(o.HaveOccurred())
		needRecover = true

		g.By("Checking if the proposal are created")
		o.Expect(wait.PollUntilContextTimeout(ctx, 30*time.Second, 5*time.Minute, true, func(ctx context.Context) (done bool, err error) {
			proposals := proposalv1alpha1.ProposalList{}
			err = rtClient.List(ctx, &proposals, ctrlruntimeclient.InNamespace(external.DefaultCVONamespace))
			o.Expect(err).NotTo(o.HaveOccurred())
			if len(proposals.Items) == 0 {
				return false, nil
			}
			return true, nil
		})).NotTo(o.HaveOccurred(), "no proposals found")
	})
})

package cvo

import (
	"context"

	g "github.com/onsi/ginkgo/v2"
	o "github.com/onsi/gomega"

	apiextensionsclientset "k8s.io/apiextensions-apiserver/pkg/client/clientset/clientset"
	kerrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/rest"

	"github.com/openshift/cluster-version-operator/test/util"
)

var _ = g.Describe(`[Jira:"Cluster Version Operator"] cluster-version-operator`, func() {

	var (
		c   *rest.Config
		err error

		ctx                 = context.Background()
		apiExtensionsClient apiextensionsclientset.Interface
	)

	g.BeforeEach(func() {
		c, err = util.GetRestConfig()
		o.Expect(err).To(o.BeNil())

		o.Expect(util.SkipIfHypershift(ctx, c)).To(o.BeNil())
		o.Expect(util.SkipIfMicroshift(ctx, c)).To(o.BeNil())

		apiExtensionsClient, err = apiextensionsclientset.NewForConfig(c)
		o.Expect(err).To(o.BeNil())

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
})

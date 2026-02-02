package cvo

import (
	"context"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"

	g "github.com/onsi/ginkgo/v2"
	o "github.com/onsi/gomega"

	"github.com/openshift/cluster-version-operator/test/oc"
	ocapi "github.com/openshift/cluster-version-operator/test/oc/api"
	"github.com/openshift/cluster-version-operator/test/util"
)

var logger = g.GinkgoLogr.WithName("cluster-version-operator-tests")

const cvoNamespace = "openshift-cluster-version"

var _ = g.Describe(`[Jira:"Cluster Version Operator"] cluster-version-operator-tests`, func() {
	g.It("should support passing tests", func() {
		o.Expect(true).To(o.BeTrue())
	})

	g.It("can use oc to get the version information", func() {
		ocClient, err := oc.NewOC(ocapi.Options{Logger: logger})
		o.Expect(err).NotTo(o.HaveOccurred())
		o.Expect(ocClient).NotTo(o.BeNil())

		output, err := ocClient.Version(ocapi.VersionOptions{Client: true})
		o.Expect(err).NotTo(o.HaveOccurred())
		o.Expect(output).To(o.ContainSubstring("Client Version:"))
	})
})

// CVO tests which need access the live cluster will be placed here
var _ = g.Describe(`[Jira:"Cluster Version Operator"] cluster-version-operator`, func() {
	var (
		restCfg    *rest.Config
		kubeClient kubernetes.Interface
	)

	g.BeforeEach(func() {
		var err error
		// Respects KUBECONFIG env var
		restCfg, err = util.GetRestConfig()
		o.Expect(err).NotTo(o.HaveOccurred(), "Failed to load Kubernetes configuration. Please ensure KUBECONFIG environment variable is set.")

		kubeClient, err = util.GetKubeClient(restCfg)
		o.Expect(err).NotTo(o.HaveOccurred(), "Failed to create Kubernetes client")
	})

	// Migrated from case NonHyperShiftHOST-Author:jiajliu-Low-46922-check runlevel and scc in cvo ns
	// Refer to https://github.com/openshift/openshift-tests-private/blob/40374cf20946ff03c88712839a5626af2c88ab31/test/extended/ota/cvo/cvo.go#L1081
	g.It("should have correct runlevel and scc", func() {
		ctx := context.Background()
		err := util.SkipIfHypershift(ctx, restCfg)
		o.Expect(err).NotTo(o.HaveOccurred(), "Failed to determine if cluster is HyperShift")
		err = util.SkipIfMicroshift(ctx, restCfg)
		o.Expect(err).NotTo(o.HaveOccurred(), "Failed to determine if cluster is MicroShift")

		g.By("Checking that the 'openshift.io/run-level' label exists on the namespace and has the empty value")
		ns, err := kubeClient.CoreV1().Namespaces().Get(ctx, cvoNamespace, metav1.GetOptions{})
		o.Expect(err).NotTo(o.HaveOccurred(), "Failed to get namespace %s", cvoNamespace)
		runLevel, exists := ns.Labels["openshift.io/run-level"]
		o.Expect(exists).To(o.BeTrue(), "The 'openshift.io/run-level' label on namespace %s does not exist", cvoNamespace)
		o.Expect(runLevel).To(o.BeEmpty(), "Expected the 'openshift.io/run-level' label value on namespace %s has the empty value, but got %s", cvoNamespace, runLevel)

		g.By("Checking that the annotation 'openshift.io/scc annotation' on the CVO pod has the value hostaccess")
		podList, err := kubeClient.CoreV1().Pods(cvoNamespace).List(ctx, metav1.ListOptions{
			LabelSelector: "k8s-app=cluster-version-operator",
			FieldSelector: "status.phase=Running",
		})
		o.Expect(err).NotTo(o.HaveOccurred(), "Failed to list running CVO pods")
		o.Expect(podList.Items).To(o.HaveLen(1), "Expected exactly one running CVO pod, but found: %d", len(podList.Items))

		cvoPod := podList.Items[0]
		sccAnnotation := cvoPod.Annotations["openshift.io/scc"]
		o.Expect(sccAnnotation).To(o.Equal("hostaccess"), "Expected the annotation 'openshift.io/scc annotation' on pod %s to have the value 'hostaccess', but got %s", cvoPod.Name, sccAnnotation)
	})
})

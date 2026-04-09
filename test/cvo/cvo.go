// Package cvo contains end-to-end tests for the Cluster Version Operator.
// The accept_risks test validates the feature about accept risks for conditional updates.
package cvo

import (
	"context"

	g "github.com/onsi/ginkgo/v2"
	o "github.com/onsi/gomega"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"

	"github.com/openshift/cluster-version-operator/pkg/external"
	"github.com/openshift/cluster-version-operator/test/oc"
	ocapi "github.com/openshift/cluster-version-operator/test/oc/api"
	"github.com/openshift/cluster-version-operator/test/util"
)

var logger = g.GinkgoLogr.WithName("cluster-version-operator-tests")

var _ = g.Describe(`[Jira:"Cluster Version Operator"] cluster-version-operator-tests`, func() {
	g.It("should support passing tests", func() {
		o.Expect(true).To(o.BeTrue())
	})

	g.It("should support passing serial tests [Serial]", func() {
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

	g.It("should have a network policy", g.Label("OCPBUGS-77762"), func() {
		ctx := context.Background()
		err := util.SkipIfHypershift(ctx, restCfg)
		o.Expect(err).NotTo(o.HaveOccurred(), "Failed to determine if cluster is HyperShift")
		err = util.SkipIfMicroshift(ctx, restCfg)
		o.Expect(err).NotTo(o.HaveOccurred(), "Failed to determine if cluster is MicroShift")

		g.By("Checking if the default network policy exists on the namespace")
		if _, err := kubeClient.NetworkingV1().NetworkPolicies(external.DefaultCVONamespace).
			Get(ctx, "default-deny", metav1.GetOptions{}); err != nil {
			o.Expect(err).NotTo(o.HaveOccurred(), "Failed to get the default network policy")
		}
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
		ns, err := kubeClient.CoreV1().Namespaces().Get(ctx, external.DefaultCVONamespace, metav1.GetOptions{})
		o.Expect(err).NotTo(o.HaveOccurred(), "Failed to get namespace %s", external.DefaultCVONamespace)
		runLevel, exists := ns.Labels["openshift.io/run-level"]
		o.Expect(exists).To(o.BeTrue(), "The 'openshift.io/run-level' label on namespace %s does not exist", external.DefaultCVONamespace)
		o.Expect(runLevel).To(o.BeEmpty(), "Expected the 'openshift.io/run-level' label value on namespace %s has the empty value, but got %s", external.DefaultCVONamespace, runLevel)

		g.By("Checking that the annotation 'openshift.io/scc annotation' on the CVO pod has the value hostaccess")
		podList, err := kubeClient.CoreV1().Pods(external.DefaultCVONamespace).List(ctx, metav1.ListOptions{
			LabelSelector: "k8s-app=cluster-version-operator",
			FieldSelector: "status.phase=Running",
		})
		o.Expect(err).NotTo(o.HaveOccurred(), "Failed to list running CVO pods")
		o.Expect(podList.Items).To(o.HaveLen(1), "Expected exactly one running CVO pod, but found: %d", len(podList.Items))

		cvoPod := podList.Items[0]
		sccAnnotation := cvoPod.Annotations["openshift.io/scc"]
		o.Expect(sccAnnotation).To(o.Equal("hostaccess"), "Expected the annotation 'openshift.io/scc annotation' on pod %s to have the value 'hostaccess', but got %s", cvoPod.Name, sccAnnotation)
	})

	/*// Migrated from case NonHyperShiftHOST-Author:dis-High-OCP-33876-Clusterversion status has metadata stored
	g.It("Clusterversion status has metadata stored", func() {
		ctx := context.Background()
		err := util.SkipIfHypershift(ctx, restCfg)
		o.Expect(err).NotTo(o.HaveOccurred(), "Failed to determine if cluster is HyperShift")
		err = util.SkipIfMicroshift(ctx, restCfg)
		o.Expect(err).NotTo(o.HaveOccurred(), "Failed to determine if cluster is MicroShift")

		g.By("Checking that the Clusterversion status has metadata stored")
		clusterversion, err := kubeClient.ConfigV1().ClusterVersions().Get(ctx, "version", metav1.GetOptions{})
		o.Expect(err).NotTo(o.HaveOccurred(), "Failed to get Clusterversion")
	})*/

	// Migrated from case NonHyperShiftHOST-Author:dis-Medium-OCP-54887-The default channel should be corret
	g.It("The default channel should be correct", func() {
		ctx := context.Background()
		err := util.SkipIfHypershift(ctx, restCfg)
		o.Expect(err).NotTo(o.HaveOccurred(), "Failed to determine if cluster is HyperShift")
		err = util.SkipIfMicroshift(ctx, restCfg)
		o.Expect(err).NotTo(o.HaveOccurred(), "Failed to determine if cluster is MicroShift")

		g.By("Checking that the default channel matches the cluster version")
		configClient, err := util.GetConfigClient(restCfg)
		o.Expect(err).NotTo(o.HaveOccurred(), "Failed to create config client")
		clusterversion, err := configClient.ConfigV1().ClusterVersions().Get(ctx, "version", metav1.GetOptions{})
		o.Expect(err).NotTo(o.HaveOccurred(), "Failed to get Clusterversion")

		g.By("Extracting version details from ClusterVersion status")
		o.Expect(clusterversion.Status.Desired.Version).NotTo(o.BeEmpty(), "ClusterVersion desired version should not be empty")
		version := clusterversion.Status.Desired.Version
		logger.Info("Cluster version detected", "version", version)

		g.By("Verifying the cluster is using a signed build")
		o.Expect(clusterversion.Status.History).NotTo(o.BeEmpty(), "ClusterVersion history should not be empty")
		currentUpdate := clusterversion.Status.History[0]
		o.Expect(currentUpdate.Verified).To(o.BeTrue(), "Expected the cluster build to be signed and verified, but Verified is false. Version: %s, Image: %s", currentUpdate.Version, currentUpdate.Image)
		logger.Info("Cluster build is verified", "version", currentUpdate.Version, "image", currentUpdate.Image, "verified", currentUpdate.Verified)

		g.By("Validating default channel format matches stable-<major>.<minor>")
		channel := clusterversion.Spec.Channel
		o.Expect(channel).To(o.MatchRegexp(`^stable-\d+\.\d+$`), "Expected channel to match pattern 'stable-<major>.<minor>', but got %s", channel)

		g.By("Verifying channel matches the cluster's major.minor version")
		expectedChannelPrefix := util.GetExpectedChannelPrefix(version)
		o.Expect(channel).To(o.Equal(expectedChannelPrefix), "Expected channel to be %s based on version %s, but got %s", expectedChannelPrefix, version, channel)
		logger.Info("Deafault channel for version is correct", "version:", version, "channel:", channel)
	})
})

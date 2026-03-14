// Package cvo contains end-to-end tests for the Cluster Version Operator.
// The accept_risks test validates the feature about accept risks for conditional updates.
package cvo

import (
	"context"
	"encoding/json"
	"time"

	g "github.com/onsi/ginkgo/v2"
	o "github.com/onsi/gomega"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"

	"github.com/openshift/cluster-version-operator/lib/resourcemerge"
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

	// Migrated from case Author:jiajliu-Medium-53906-The architecture info in clusterversion's status should be correct
	// Refer to https://github.com/jiajliu/openshift-tests-private/blob/1ac5f94ee596419194ff7b0070732cb7930fe39e/test/extended/ota/cvo/cvo.go#L1775
	g.It("should have correct architecture info in clusterversion status", func() {
		const heterogeneousArchKeyword = "multi"

		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()

		err := util.SkipIfMicroshift(ctx, restCfg)
		o.Expect(err).NotTo(o.HaveOccurred(), "Failed to determine if cluster is MicroShift")

		g.By("Getting release architecture from release info")
		ocClient, err := oc.NewOC(ocapi.Options{
			Logger:  logger,
			Timeout: 2 * time.Minute,
		})
		o.Expect(err).NotTo(o.HaveOccurred(), "Failed to create oc client")
		releaseInfoJSON, err := ocClient.AdmReleaseInfo(ocapi.ReleaseInfoOptions{})
		o.Expect(err).NotTo(o.HaveOccurred(), "Failed to get release info")

		// Extract architecture from release info: metadata.metadata."release.openshift.io/architecture"
		// Valid payloads:
		// - Stable single-arch: metadata.metadata exists but without "release.openshift.io/architecture"
		// - Stable multi-arch: metadata.metadata."release.openshift.io/architecture" = "multi"
		// - Nightly single-arch: metadata.metadata not exists
		// - Nightly multi-arch: metadata.metadata."release.openshift.io/architecture" = "multi"
		var releaseInfo struct {
			Metadata *struct {
				Metadata map[string]string `json:"metadata"`
			} `json:"metadata"`
		}
		o.Expect(json.Unmarshal([]byte(releaseInfoJSON), &releaseInfo)).To(o.Succeed(), "Failed to unmarshal release info JSON")
		if releaseInfo.Metadata == nil {
			g.Skip("Release info missing top-level 'metadata' field, cannot determine payload architecture type")
		}

		releaseArch := releaseInfo.Metadata.Metadata["release.openshift.io/architecture"]
		logger.Info("Release architecture from release info", "architecture", releaseArch)
		if releaseArch != "" && releaseArch != heterogeneousArchKeyword {
			g.Skip("Unknown architecture value in release info: " + releaseArch)
		}

		g.By("Check the arch info in ClusterVersion status is expected")
		configClient, err := util.GetConfigClient(restCfg)
		o.Expect(err).NotTo(o.HaveOccurred(), "Failed to create config client")
		cv, err := configClient.ConfigV1().ClusterVersions().Get(ctx, external.DefaultClusterVersionName, metav1.GetOptions{})
		o.Expect(err).NotTo(o.HaveOccurred(), "Failed to get ClusterVersion")
		releaseAcceptedCond := resourcemerge.FindOperatorStatusCondition(cv.Status.Conditions, "ReleaseAccepted")
		o.Expect(releaseAcceptedCond).NotTo(o.BeNil(), "ReleaseAccepted condition not found in ClusterVersion status")
		cvArchInfo := releaseAcceptedCond.Message
		logger.Info("ClusterVersion ReleaseAccepted message", "message", cvArchInfo)

		if releaseArch == "" {
			// For non-heterogeneous payload, the architecture info in ClusterVersion status should be consistent with node architectures
			g.By("Verifying all nodes have the same architecture")
			nodes, err := kubeClient.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
			o.Expect(err).NotTo(o.HaveOccurred(), "Failed to list nodes")
			o.Expect(nodes.Items).NotTo(o.BeEmpty(), "No nodes found in cluster")

			nodeArchs := sets.New[string]()
			for _, node := range nodes.Items {
				nodeArchs.Insert(node.Status.NodeInfo.Architecture)
			}
			logger.Info("Node architectures found", "architectures", sets.List(nodeArchs))
			o.Expect(nodeArchs).To(o.HaveLen(1), "Expected all nodes to have the same architecture in non-heterogeneous cluster, but found: %v", sets.List(nodeArchs))

			expectedArch := sets.List(nodeArchs)[0]
			g.By("Verifying ClusterVersion status architecture matches node architecture")
			o.Expect(cvArchInfo).To(o.ContainSubstring(expectedArch), "ClusterVersion ReleaseAccepted message should contain node architecture %q, but got: %s", expectedArch, cvArchInfo)
		} else {
			// For heterogeneous payload, the architecture info in ClusterVersion status should be Multi
			g.By("Verifying ClusterVersion status architecture includes architecture=\"Multi\"")
			expectedArchMsg := `architecture="Multi"`
			o.Expect(cvArchInfo).To(o.ContainSubstring(expectedArchMsg), "ClusterVersion ReleaseAccepted message should contain %q for heterogeneous payload, but got: %s", expectedArchMsg, cvArchInfo)
		}
	})
})

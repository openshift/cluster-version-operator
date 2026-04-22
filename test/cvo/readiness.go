package cvo

import (
	"context"
	"encoding/json"

	g "github.com/onsi/ginkgo/v2"
	o "github.com/onsi/gomega"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"

	configv1client "github.com/openshift/client-go/config/clientset/versioned/typed/config/v1"

	"github.com/openshift/cluster-version-operator/pkg/readiness"
	"github.com/openshift/cluster-version-operator/test/util"
)

var _ = g.Describe(`[Jira:"Cluster Version Operator"] cluster-version-operator readiness checks`, func() {
	var (
		dynamicClient  dynamic.Interface
		kubeClient     kubernetes.Interface
		configClient   *configv1client.ConfigV1Client
		ctx            = context.TODO()
		currentVersion string
		targetVersion  string
	)

	g.BeforeEach(func() {
		restCfg, err := util.GetRestConfig()
		o.Expect(err).NotTo(o.HaveOccurred())

		dynamicClient, err = dynamic.NewForConfig(restCfg)
		o.Expect(err).NotTo(o.HaveOccurred())

		kubeClient, err = kubernetes.NewForConfig(restCfg)
		o.Expect(err).NotTo(o.HaveOccurred())

		configClient, err = configv1client.NewForConfig(restCfg)
		o.Expect(err).NotTo(o.HaveOccurred())

		// Read actual versions from the cluster
		cv, err := configClient.ClusterVersions().Get(ctx, "version", metav1.GetOptions{})
		o.Expect(err).NotTo(o.HaveOccurred())
		currentVersion = cv.Status.Desired.Version
		o.Expect(currentVersion).NotTo(o.BeEmpty(), "cluster must have a current version")

		// Pick the first available update as target, or use current if none
		targetVersion = currentVersion
		if len(cv.Status.AvailableUpdates) > 0 {
			targetVersion = cv.Status.AvailableUpdates[0].Version
		}
	})

	g.It("should run all checks without errors", func() {
		output := readiness.RunAll(ctx, dynamicClient, currentVersion, targetVersion)

		o.Expect(output.Meta.TotalChecks).To(o.Equal(9))
		o.Expect(output.Meta.ChecksErrored).To(o.Equal(0),
			"no check should error on a healthy cluster")
	})

	g.It("should produce valid JSON that round-trips", func() {
		output := readiness.RunAll(ctx, dynamicClient, currentVersion, targetVersion)

		data, err := json.Marshal(output)
		o.Expect(err).NotTo(o.HaveOccurred())

		var parsed map[string]interface{}
		o.Expect(json.Unmarshal(data, &parsed)).To(o.Succeed())
		o.Expect(parsed).To(o.HaveKey("checks"))
		o.Expect(parsed).To(o.HaveKey("meta"))
	})

	g.It("should report node count matching the actual cluster", func() {
		// Ground truth: list nodes via typed client
		nodeList, err := kubeClient.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
		o.Expect(err).NotTo(o.HaveOccurred())

		expectedTotal := len(nodeList.Items)
		expectedReady := 0
		for _, node := range nodeList.Items {
			for _, cond := range node.Status.Conditions {
				if cond.Type == "Ready" && cond.Status == "True" {
					expectedReady++
				}
			}
		}

		// Our check
		output := readiness.RunAll(ctx, dynamicClient, currentVersion, targetVersion)
		result := output.Checks["node_capacity"]
		o.Expect(result.Status).To(o.Equal("ok"))
		o.Expect(result.Data["total_nodes"]).To(o.Equal(expectedTotal),
			"node count should match actual nodes in cluster")
		o.Expect(result.Data["ready_nodes"]).To(o.Equal(expectedReady),
			"ready node count should match actual ready nodes")
	})

	g.It("should report operator count matching actual ClusterOperators", func() {
		// Ground truth: list ClusterOperators via typed client
		coList, err := configClient.ClusterOperators().List(ctx, metav1.ListOptions{})
		o.Expect(err).NotTo(o.HaveOccurred())

		expectedTotal := len(coList.Items)
		expectedDegraded := 0
		expectedNotUpgradeable := 0
		for _, co := range coList.Items {
			for _, cond := range co.Status.Conditions {
				if cond.Type == "Degraded" && cond.Status == "True" {
					expectedDegraded++
				}
				if cond.Type == "Upgradeable" && cond.Status == "False" {
					expectedNotUpgradeable++
				}
			}
		}

		// Our check
		output := readiness.RunAll(ctx, dynamicClient, currentVersion, targetVersion)
		result := output.Checks["operator_health"]
		o.Expect(result.Status).To(o.Equal("ok"))

		summary := result.Data["summary"].(map[string]any)
		o.Expect(summary["total_operators"]).To(o.Equal(expectedTotal),
			"operator count should match actual ClusterOperators")
		o.Expect(summary["degraded_count"]).To(o.Equal(expectedDegraded),
			"degraded count should match actual degraded operators")
		o.Expect(summary["not_upgradeable_count"]).To(o.Equal(expectedNotUpgradeable),
			"not-upgradeable count should match actual operators")
	})

	g.It("should report etcd member count matching actual etcd pods", func() {
		// Ground truth: list etcd pods via typed client
		podList, err := kubeClient.CoreV1().Pods("openshift-etcd").List(ctx, metav1.ListOptions{
			LabelSelector: "app=etcd",
		})
		o.Expect(err).NotTo(o.HaveOccurred())

		expectedTotal := len(podList.Items)
		expectedHealthy := 0
		for _, pod := range podList.Items {
			if pod.Status.Phase == "Running" {
				expectedHealthy++
			}
		}

		// Our check
		output := readiness.RunAll(ctx, dynamicClient, currentVersion, targetVersion)
		result := output.Checks["etcd_health"]
		o.Expect(result.Status).To(o.Equal("ok"))
		o.Expect(result.Data["total_members"]).To(o.Equal(expectedTotal),
			"etcd member count should match actual etcd pods")
		o.Expect(result.Data["healthy_members"]).To(o.Equal(expectedHealthy),
			"healthy member count should match actual running etcd pods")
	})

	g.It("should report network type matching actual Network config", func() {
		// Ground truth: get Network config via typed client
		network, err := configClient.Networks().Get(ctx, "cluster", metav1.GetOptions{})
		o.Expect(err).NotTo(o.HaveOccurred())

		// Our check
		output := readiness.RunAll(ctx, dynamicClient, currentVersion, targetVersion)
		result := output.Checks["network"]
		o.Expect(result.Status).To(o.Equal("ok"))
		o.Expect(result.Data["network_type"]).To(o.Equal(network.Status.NetworkType),
			"network type should match actual Network config")
	})

	g.It("should report PDB count matching actual PodDisruptionBudgets", func() {
		// Ground truth: list PDBs across all namespaces
		pdbList, err := kubeClient.PolicyV1().PodDisruptionBudgets("").List(ctx, metav1.ListOptions{})
		o.Expect(err).NotTo(o.HaveOccurred())

		expectedTotal := len(pdbList.Items)
		expectedBlocking := 0
		for _, pdb := range pdbList.Items {
			if pdb.Status.DisruptionsAllowed == 0 && pdb.Status.CurrentHealthy > 0 {
				expectedBlocking++
			}
		}

		// Our check
		output := readiness.RunAll(ctx, dynamicClient, currentVersion, targetVersion)
		result := output.Checks["pdb_drain"]
		o.Expect(result.Status).To(o.Equal("ok"))
		o.Expect(result.Data["total_pdbs"]).To(o.Equal(expectedTotal),
			"PDB count should match actual PDBs in cluster")

		blockingPDBs := result.Data["blocking_pdbs"].([]map[string]any)
		o.Expect(len(blockingPDBs)).To(o.Equal(expectedBlocking),
			"blocking PDB count should match actual blocking PDBs")
	})

	g.It("should report cluster conditions matching ClusterVersion status", func() {
		// Ground truth: get ClusterVersion via typed client
		cv, err := configClient.ClusterVersions().Get(ctx, "version", metav1.GetOptions{})
		o.Expect(err).NotTo(o.HaveOccurred())

		// Our check
		output := readiness.RunAll(ctx, dynamicClient, currentVersion, targetVersion)
		result := output.Checks["cluster_conditions"]
		o.Expect(result.Status).To(o.Equal("ok"))
		o.Expect(result.Data["channel"]).To(o.Equal(cv.Spec.Channel),
			"channel should match ClusterVersion spec")
		o.Expect(result.Data["cluster_id"]).To(o.Equal(string(cv.Spec.ClusterID)),
			"cluster ID should match ClusterVersion spec")
	})

	g.It("should complete all checks within 60 seconds", func() {
		output := readiness.RunAll(ctx, dynamicClient, currentVersion, targetVersion)

		o.Expect(output.Meta.ElapsedSeconds).To(o.BeNumerically("<", 60))
		for name, result := range output.Checks {
			o.Expect(result.Elapsed).To(o.BeNumerically("<", 60),
				"check %s exceeded timeout", name)
		}
	})
})

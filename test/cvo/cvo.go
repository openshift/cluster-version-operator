// Package cvo contains end-to-end tests for the Cluster Version Operator.
// The accept_risks test validates the feature about accept risks for conditional updates.
package cvo

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/blang/semver/v4"
	g "github.com/onsi/ginkgo/v2"
	o "github.com/onsi/gomega"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"

	ote "github.com/openshift-eng/openshift-tests-extension/pkg/ginkgo"
	configv1 "github.com/openshift/api/config/v1"
	clientconfigv1 "github.com/openshift/client-go/config/clientset/versioned"

	"github.com/openshift/cluster-version-operator/pkg/external"
	"github.com/openshift/cluster-version-operator/pkg/payload/precondition/clusterversion"
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

	g.It("cvo drops invalid conditional edges", ote.Informing(), g.Label("46422", "Low", "ConnectedOnly", "Serial"), func() {
		ctx := context.Background()
		err := util.SkipIfHypershift(ctx, restCfg)
		o.Expect(err).NotTo(o.HaveOccurred(), "Failed to determine if cluster is HyperShift")
		err = util.SkipIfMicroshift(ctx, restCfg)
		o.Expect(err).NotTo(o.HaveOccurred(), "Failed to determine if cluster is MicroShift")
		err = util.SkipIfNetworkRestricted(ctx, restCfg, util.FauxinnatiAPIURL)
		o.Expect(err).NotTo(o.HaveOccurred(), "Failed to check network connectivity")

		ns, err := kubeClient.CoreV1().Namespaces().Create(ctx, &corev1.Namespace{
			ObjectMeta: metav1.ObjectMeta{
				GenerateName: "test-ns-46422-",
			},
		}, metav1.CreateOptions{})
		o.Expect(err).NotTo(o.HaveOccurred())
		defer func() {
			_ = kubeClient.CoreV1().Namespaces().Delete(ctx, ns.Name, metav1.DeleteOptions{})
		}()

		clientconfigv1, err := clientconfigv1.NewForConfig(restCfg)
		o.Expect(err).NotTo(o.HaveOccurred())
		cv, err := clientconfigv1.ConfigV1().ClusterVersions().Get(ctx, "version", metav1.GetOptions{})
		o.Expect(err).NotTo(o.HaveOccurred())
		o.Expect(cv).NotTo(o.BeNil())

		oldUpstream := cv.Spec.Upstream
		oldChannel := cv.Spec.Channel
		defer func() {
			_, err := util.PatchUpstream(ctx, clientconfigv1, string(oldUpstream), oldChannel)
			o.Expect(err).NotTo(o.HaveOccurred())
		}()

		versionString, image := clusterversion.GetCurrentVersionAndImage(cv.Status.History)
		o.Expect(versionString).NotTo(o.BeEmpty())
		o.Expect(image).NotTo(o.BeEmpty())

		ver, err := semver.Make(versionString)
		o.Expect(err).NotTo(o.HaveOccurred())
		channel := fmt.Sprintf("candidate-%d.%d", ver.Major, ver.Minor)

		nodes := []util.Node{
			{Version: versionString, Payload: image, Channel: channel},
		}
		edges := []util.Edge{}
		conditionalEdges := []util.ConditionalEdge{
			{
				Edge: util.StringEdge{
					From: versionString,
					To:   "",
				},
				Risks: []util.Risk{
					{
						Url:     "https://bugzilla.redhat.com/show_bug.cgi?id=123456",
						Name:    "Bug 123456",
						Message: "Empty target node",
						Rule:    map[string]interface{}{"type": "Always"},
					},
				},
			},
		}
		g.By("create upstream graph template with invalid conditional edges")
		buf, err := util.CreateGraphTemplate(nodes, edges, conditionalEdges)
		o.Expect(err).NotTo(o.HaveOccurred())

		label1 := map[string]string{"app": "test-update-service1"}
		g.By("run update service with the graph template")
		deployment1, cm1, err := util.RunUpdateService(ctx, kubeClient, ns.Name, buf.String(), label1)
		o.Expect(err).NotTo(o.HaveOccurred())
		logger.Info(fmt.Sprintf("deployment1: %s, %s, %d", deployment1.Spec.Template.Spec.Containers[0].Image, deployment1.Spec.Template.Spec.Containers[0].Ports[0].Name, deployment1.Spec.Template.Spec.Containers[0].Ports[0].ContainerPort))
		defer func() {
			if deployment1 != nil {
				_ = util.DeleteDeployment(ctx, kubeClient, ns.Name, deployment1.Name)
			}
			if cm1 != nil {
				_ = util.DeleteConfigMap(ctx, kubeClient, ns.Name, cm1.Name)
			}
		}()

		service1, url1, err := util.CreateService(ctx, kubeClient, ns.Name, deployment1, 46422)
		o.Expect(err).NotTo(o.HaveOccurred())
		logger.Info("Update Service URL: " + url1.String())
		logger.Info(fmt.Sprintf("Service: %s, %d, %v", service1.Spec.Ports[0].Name, &service1.Spec.Ports[0].Port, service1.Spec.Ports[0].TargetPort))
		defer func() { _ = util.CleanupService(ctx, kubeClient, ns.Name, service1.Name) }()

		policyName := service1.Name + "-policy"
		_, err = util.CreateNetworkPolicy(ctx, kubeClient, ns.Name, policyName, label1)
		o.Expect(err).NotTo(o.HaveOccurred())
		pollErr := wait.PollUntilContextTimeout(ctx, 5*time.Second, 60*time.Second, true, func(ctx context.Context) (bool, error) {
			if res, getErr := util.NetworkRestricted(ctx, restCfg, url1.String()); getErr != nil {
				return false, getErr
			} else if res {
				return false, fmt.Errorf("expected network to be available, but it is not")
			}
			return true, nil
		})
		o.Expect(pollErr).NotTo(o.HaveOccurred(), fmt.Sprintf("Failed to verify network connectivity to the update service at %s: %v", url1.String(), pollErr))
		defer func() {
			_ = util.DeleteNetworkPolicy(ctx, kubeClient, ns.Name, policyName)
		}()

		g.By("patch upstream with null target node conditional edge")
		o.Expect(err).NotTo(o.HaveOccurred())
		_, err = util.PatchUpstream(ctx, clientconfigv1, url1.String(), channel)
		o.Expect(err).NotTo(o.HaveOccurred())
		pollErr = wait.PollUntilContextTimeout(ctx, 5*time.Second, 10*time.Minute, true, func(ctx context.Context) (bool, error) {
			cv, err := clientconfigv1.ConfigV1().ClusterVersions().Get(ctx, "version", metav1.GetOptions{})
			if err != nil {
				return false, err
			}
			for _, condition := range cv.Status.Conditions {
				if condition.Type == configv1.ClusterStatusConditionType("RetrievedUpdates") {
					if condition.Status != configv1.ConditionFalse {
						return false, nil
					}
					if !strings.Contains(condition.Message, "no node for conditional update") {
						return false, nil
					}
					return true, nil
				}
			}
			return false, nil
		})
		o.Expect(pollErr).NotTo(o.HaveOccurred(), fmt.Sprintf("Failed to verify the cluster version condition for the null target node conditional edge: %v", pollErr))
		_, err = util.PatchUpstream(ctx, clientconfigv1, string(oldUpstream), oldChannel)
		o.Expect(err).NotTo(o.HaveOccurred())
		defer func() {
			_, err = util.PatchUpstream(ctx, clientconfigv1, string(oldUpstream), oldChannel)
			o.Expect(err).NotTo(o.HaveOccurred())
		}()
		err = util.CleanupService(ctx, kubeClient, ns.Name, service1.Name)
		o.Expect(err).NotTo(o.HaveOccurred())
		err = util.DeleteDeployment(ctx, kubeClient, ns.Name, deployment1.Name)
		o.Expect(err).NotTo(o.HaveOccurred())
		err = util.DeleteConfigMap(ctx, kubeClient, ns.Name, cm1.Name)
		o.Expect(err).NotTo(o.HaveOccurred())

		g.By("prepare new graph file")
		targetVersion := fmt.Sprintf("%d.%d.999", ver.Major, ver.Minor)
		nodes = append(nodes, util.Node{
			Version: targetVersion,
			Payload: "quay.io/openshift-release-dev/ocp-release@sha256:fc88d0bf145c81d9c9e5b8a1cfcaa7cbbacfa698f0c3a252fbbdbfbb1b2",
			Channel: channel,
		})
		edges = []util.Edge{}
		conditionalEdges = []util.ConditionalEdge{
			{
				Edge: util.StringEdge{
					From: versionString,
					To:   targetVersion,
				},
				Risks: []util.Risk{
					{
						Url:     "// example.com",
						Name:    "InvalidURL",
						Message: "Invalid URL.",
						Rule:    map[string]interface{}{"type": "PromQL", "promql": map[string]interface{}{"promql": "cluster_installer"}},
					},
					{
						Url:     "https://bug.example.com/b",
						Name:    "TypeNull",
						Message: "MatchingRules type is null.",
						Rule:    "{\"type\": \"\"}",
					},
					{
						Url:     "https://bug.example.com/c",
						Name:    "InvalidMatchingRulesType",
						Message: "MatchingRules type is invalid, support Always and PromQL.",
						Rule:    map[string]interface{}{"type": "nonexist", "promql": map[string]interface{}{"promql": "group(cluster_version_available_updates{channel=\"buggy\"})\nor\n0 * group(cluster_version_available_updates{channel!=\"buggy\"})"}},
					},
					{
						Url:     "https://bug.example.com/d",
						Name:    "InvalidPromQLQueryReturnValue",
						Message: "PromQL query return value is not supported, support 0 and 1.",
						Rule:    map[string]interface{}{"type": "PromQL", "promql": map[string]interface{}{"promql": "max(cluster_version)"}},
					},
					{
						Url:     "https://bug.example.com/d",
						Name:    "InvalidPromQLQuery",
						Message: "Invalid PromQL Query.",
						Rule:    map[string]interface{}{"type": "PromQL", "promql": map[string]interface{}{"promql": "cluster_infrastructure_provider{type=~\"VSphere|None\"}"}},
					},
				},
			},
		}
		g.By("create upstream graph template with multi risks conditional edges")
		buf, err = util.CreateGraphTemplate(nodes, edges, conditionalEdges)
		o.Expect(err).NotTo(o.HaveOccurred())

		g.By("run update service with the multi risks graph template")
		label2 := map[string]string{"app": "test-update-service2"}
		deployment2, cm2, err := util.RunUpdateService(ctx, kubeClient, ns.Name, buf.String(), label2)
		o.Expect(err).NotTo(o.HaveOccurred())
		defer func() {
			if deployment2 != nil {
				_ = util.DeleteDeployment(ctx, kubeClient, ns.Name, deployment2.Name)
			}
			if cm2 != nil {
				_ = util.DeleteConfigMap(ctx, kubeClient, ns.Name, cm2.Name)
			}
		}()

		service2, url2, err := util.CreateService(ctx, kubeClient, ns.Name, deployment2, 46422)
		o.Expect(err).NotTo(o.HaveOccurred())
		logger.Info("Update Service URL: " + url2.String())
		logger.Info(fmt.Sprintf("Service: %s, %d, %v", service2.Spec.Ports[0].Name, &service2.Spec.Ports[0].Port, service2.Spec.Ports[0].TargetPort))
		pollErr = wait.PollUntilContextTimeout(ctx, 5*time.Second, 60*time.Second, true, func(ctx context.Context) (bool, error) {
			if res, getErr := util.NetworkRestricted(ctx, restCfg, url2.String()); getErr != nil {
				return false, getErr
			} else if res {
				return false, fmt.Errorf("expected network to be available, but it is not")
			}
			return true, nil
		})
		o.Expect(pollErr).NotTo(o.HaveOccurred(), fmt.Sprintf("Failed to verify network connectivity to the update service at %s: %v", url2.String(), pollErr))
		defer func() { _ = util.CleanupService(ctx, kubeClient, ns.Name, service2.Name) }()

		g.By("patch upstream with multi risks conditional edge")
		_, err = util.PatchUpstream(ctx, clientconfigv1, url2.String(), channel)
		o.Expect(err).NotTo(o.HaveOccurred())
		pollErr = wait.PollUntilContextTimeout(ctx, 5*time.Second, 10*time.Minute, true, func(ctx context.Context) (bool, error) {
			cv, err := clientconfigv1.ConfigV1().ClusterVersions().Get(ctx, "version", metav1.GetOptions{})
			if err != nil {
				return false, err
			}
			if cv.Status.AvailableUpdates != nil {
				return false, nil
			}
			for _, condition := range cv.Status.Conditions {
				if condition.Type == configv1.ClusterStatusConditionType("RetrievedUpdates") {
					if condition.Status != configv1.ConditionTrue {
						return false, nil
					}
					return true, nil
				}
			}
			return true, nil
		})
		o.Expect(pollErr).NotTo(o.HaveOccurred(), fmt.Sprintf("Failed to verify the cluster version condition for the multi risks target node conditional edge: %v", pollErr))
	})
})

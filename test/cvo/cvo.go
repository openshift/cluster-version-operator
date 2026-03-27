// Package cvo contains end-to-end tests for the Cluster Version Operator.
// The accept_risks test validates the feature about accept risks for conditional updates.
package cvo

import (
	"context"
	"fmt"
	"strings"

	g "github.com/onsi/ginkgo/v2"
	o "github.com/onsi/gomega"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"

	"github.com/openshift/cluster-version-operator/pkg/cvo/external/dynamicclient"
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

	g.It(`should not install resources annotated with release.openshift.io/delete=true`, g.Label("Conformance", "High", "42543"), func() {
		ctx := context.Background()
		err := util.SkipIfHypershift(ctx, restCfg)
		o.Expect(err).NotTo(o.HaveOccurred(), "Failed to determine if cluster is HyperShift")
		err = util.SkipIfMicroshift(ctx, restCfg)
		o.Expect(err).NotTo(o.HaveOccurred(), "Failed to determine if cluster is MicroShift")

		pods, err := util.GetPodsByNamespace(ctx, kubeClient, external.DefaultCVONamespace, map[string]string{"k8s-app": "cluster-version-operator"})
		o.Expect(err).NotTo(o.HaveOccurred())
		o.Expect(pods).NotTo(o.BeEmpty(), "Expected at least one CVO pod")

		annotation := "release.openshift.io/delete"
		manifestDir := "/release-manifests/"
		command := []string{"find", manifestDir, "-iname", "*.yaml", "-exec", "grep", "-l", annotation, "{}", ";"}
		files, err := util.ListFilesInPodContainer(ctx, restCfg, command, external.DefaultCVONamespace, pods[0].Name, "cluster-version-operator")
		o.Expect(err).NotTo(o.HaveOccurred())
		o.Expect(files).ToNot(o.BeEmpty(), "Expected to find files in manifests directory of CVO pod, but found none")

		for _, f := range files {
			fileContent, err := util.GetFileContentInPodContainer(ctx, restCfg, external.DefaultCVONamespace, pods[0].Name, "cluster-version-operator", f)
			o.Expect(err).NotTo(o.HaveOccurred(), fmt.Sprintf("Failed to get content of file %s in CVO pod", f))
			o.Expect(fileContent).ToNot(o.BeEmpty(), fmt.Sprintf("Expected to get content of file %s in CVO pod, but got empty content", f))

			if !strings.Contains(fileContent, annotation) {
				continue
			}

			manifest, err := util.ParseManifest(fileContent)
			o.Expect(err).NotTo(o.HaveOccurred(), fmt.Sprintf("Failed to parse manifest content of file %s in CVO pod", f))

			ann := manifest.Obj.GetAnnotations()
			if ann[annotation] != "true" {
				continue
			}
			logger.Info("Checking file: ", f, ", GVK:", manifest.GVK.String(), ", Name: ", manifest.Obj.GetName(), ", Namespace: ", manifest.Obj.GetNamespace())
			client, err := dynamicclient.New(restCfg, manifest.GVK, manifest.Obj.GetNamespace())
			o.Expect(err).NotTo(o.HaveOccurred())
			_, err = client.Get(ctx, manifest.Obj.GetName(), metav1.GetOptions{})
			o.Expect(apierrors.IsNotFound(err)).To(o.BeTrue(),
				fmt.Sprintf("The deleted manifest should not be installed, but actually installed: manifest: %s %s in namespace %s from file %q, error: %v",
					manifest.GVK, manifest.Obj.GetName(), manifest.Obj.GetNamespace(), f, err))
		}
	})
})

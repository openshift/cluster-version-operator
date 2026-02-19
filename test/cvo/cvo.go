package cvo

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"

	g "github.com/onsi/ginkgo/v2"
	o "github.com/onsi/gomega"
	"github.com/openshift/cluster-version-operator/test/oc"
	ocapi "github.com/openshift/cluster-version-operator/test/oc/api"
	"github.com/openshift/cluster-version-operator/test/util"
	"github.com/openshift/library-go/pkg/manifest"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/util/sets"

	"github.com/openshift/cluster-version-operator/pkg/cvo/external/dynamicclient"
)

var logger = g.GinkgoLogr.WithName("cluster-version-operator-tests")

const cvoNamespace = "openshift-cluster-version"

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

	g.It(`should not install resources annotated with release.openshift.io/delete=true`, g.Label("Conformance", "High", "42543"), func() {
		ctx := context.Background()
		err := util.SkipIfHypershift(ctx, restCfg)
		o.Expect(err).NotTo(o.HaveOccurred(), "Failed to determine if cluster is HyperShift")
		err = util.SkipIfMicroshift(ctx, restCfg)
		o.Expect(err).NotTo(o.HaveOccurred(), "Failed to determine if cluster is MicroShift")

		// Initialize the ocapi.OC instance
		g.By("Setting up oc")
		ocClient, err := oc.NewOC(ocapi.Options{Logger: logger, Timeout: 90 * time.Second})
		o.Expect(err).NotTo(o.HaveOccurred())

		g.By("Extracting manifests in the release")
		annotation := "release.openshift.io/delete"
		tempDir, err := os.MkdirTemp("", "OTA-42543-manifest-")
		o.Expect(err).NotTo(o.HaveOccurred(), "create temp manifest dir failed")
		manifestDir := ocapi.ReleaseExtractOptions{To: tempDir}
		logger.Info(fmt.Sprintf("Extract manifests to: %s", manifestDir.To))
		defer func() { _ = os.RemoveAll(manifestDir.To) }()
		err = ocClient.AdmReleaseExtract(manifestDir)
		o.Expect(err).NotTo(o.HaveOccurred(), "extracting manifests failed")

		files, err := os.ReadDir(manifestDir.To)
		o.Expect(err).NotTo(o.HaveOccurred())
		g.By(fmt.Sprintf("Checking if getting manifests with %s on the cluster led to not-found error", annotation))

		ignore := sets.New[string]("release-metadata")

		for _, manifestFile := range files {
			if manifestFile.IsDir() || ignore.Has(manifestFile.Name()) {
				continue
			}
			filePath := filepath.Join(manifestDir.To, manifestFile.Name())
			o.Expect(err).NotTo(o.HaveOccurred(), "failed to read manifest file")
			manifests, err := manifest.ManifestsFromFiles([]string{filePath})
			o.Expect(err).NotTo(o.HaveOccurred(), fmt.Sprintf("failed to parse manifest file: %s", filePath))

			for _, ms := range manifests {
				ann := ms.Obj.GetAnnotations()
				if ann[annotation] != "true" {
					continue
				}
				client, err := dynamicclient.New(restCfg, ms.GVK, ms.Obj.GetNamespace())
				o.Expect(err).NotTo(o.HaveOccurred())
				_, err = client.Get(ctx, ms.Obj.GetName(), metav1.GetOptions{})
				o.Expect(apierrors.IsNotFound(err)).To(o.BeTrue(),
					fmt.Sprintf("The deleted manifest should not be installed, but actually installed: manifest: %s %s in namespace %s from file %q, error: %v",
						ms.GVK, ms.Obj.GetName(), ms.Obj.GetNamespace(), ms.OriginalFilename, err))
			}
		}
	})
})

package util

import (
	"context"
	"time"

	g "github.com/onsi/ginkgo/v2"
	o "github.com/onsi/gomega"

	configv1 "github.com/openshift/api/config/v1"
	clientconfigv1 "github.com/openshift/client-go/config/clientset/versioned"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

// IsHypershift checks if running on a HyperShift hosted cluster
// Refer to https://github.com/openshift/origin/blob/31704414237b8bd5c66ad247c105c94abc9470b1/test/extended/util/framework.go#L2301
func IsHypershift(ctx context.Context, restConfig *rest.Config) (bool, error) {
	configClient, err := GetConfigClient(restConfig)
	if err != nil {
		return false, err
	}
	infrastructure, err := configClient.ConfigV1().Infrastructures().Get(ctx, "cluster", metav1.GetOptions{})
	if err != nil {
		// If the Infrastructure resource doesn't exist (e.g., in MicroShift),it's not a HyperShift
		if apierrors.IsNotFound(err) {
			return false, nil
		}
		return false, err
	}
	return infrastructure.Status.ControlPlaneTopology == configv1.ExternalTopologyMode, nil
}

// SkipIfHypershift skips the test if running on a HyperShift hosted cluster
func SkipIfHypershift(ctx context.Context, restConfig *rest.Config) error {
	isHypershift, err := IsHypershift(ctx, restConfig)
	if err != nil {
		return err
	}
	if isHypershift {
		g.Skip("Skipping test: running on HyperShift hosted cluster!")
	}
	return nil
}

// IsMicroshift checks if running on a MicroShift cluster
// Refer to https://github.com/openshift/origin/blob/31704414237b8bd5c66ad247c105c94abc9470b1/test/extended/util/framework.go#L2312
func IsMicroshift(ctx context.Context, restConfig *rest.Config) (bool, error) {
	kubeClient, err := GetKubeClient(restConfig)
	if err != nil {
		return false, err
	}
	var cm *corev1.ConfigMap
	duration := 5 * time.Minute
	if err := wait.PollUntilContextTimeout(ctx, 10*time.Second, duration, true, func(ctx context.Context) (bool, error) {
		// MicroShift cluster contains "microshift-version" configmap in "kube-public" namespace
		var err error
		cm, err = kubeClient.CoreV1().ConfigMaps("kube-public").Get(ctx, "microshift-version", metav1.GetOptions{})
		if err == nil {
			return true, nil
		}
		if apierrors.IsNotFound(err) {
			cm = nil
			return true, nil
		}
		g.GinkgoLogr.Info("error accessing microshift-version configmap", "error", err)
		return false, nil
	}); err != nil {
		g.GinkgoLogr.Info("failed to find microshift-version configmap while polling", "duration", duration, "error", err)
		return false, err
	}
	if cm == nil {
		g.GinkgoLogr.Info("microshift-version configmap not found")
		return false, nil
	}
	g.GinkgoLogr.Info("MicroShift cluster detected", "version", cm.Data["version"])
	return true, nil
}

// SkipIfMicroshift skips the test if running on a MicroShift cluster
func SkipIfMicroshift(ctx context.Context, restConfig *rest.Config) error {
	isMicroshift, err := IsMicroshift(ctx, restConfig)
	if err != nil {
		return err
	}
	if isMicroshift {
		g.Skip("Skipping test: running on MicroShift cluster!")
	}
	return nil
}

// GetRestConfig loads the Kubernetes REST configuration from KUBECONFIG environment variable.
func GetRestConfig() (*rest.Config, error) {
	return clientcmd.NewNonInteractiveDeferredLoadingClientConfig(clientcmd.NewDefaultClientConfigLoadingRules(), &clientcmd.ConfigOverrides{}).ClientConfig()
}

// GetKubeClient creates a Kubernetes client from the given REST config.
func GetKubeClient(restConfig *rest.Config) (kubernetes.Interface, error) {
	return kubernetes.NewForConfig(restConfig)
}

// GetConfigClient creates an OpenShift config client from the given REST config.
func GetConfigClient(restConfig *rest.Config) (clientconfigv1.Interface, error) {
	return clientconfigv1.NewForConfig(restConfig)
}

// IsTechPreviewNoUpgrade checks if a cluster is a TechPreviewNoUpgrade cluster
func IsTechPreviewNoUpgrade(ctx context.Context, restConfig *rest.Config) bool {
	configClient, err := GetConfigClient(restConfig)
	o.Expect(err).NotTo(o.HaveOccurred())
	featureGate, err := configClient.ConfigV1().FeatureGates().Get(ctx, "cluster", metav1.GetOptions{})
	if err != nil {
		if apierrors.IsNotFound(err) {
			return false
		}
		o.Expect(err).NotTo(o.HaveOccurred(), "could not retrieve feature-gate: %v", err)
	}
	return featureGate.Spec.FeatureSet == configv1.TechPreviewNoUpgrade
}

// SkipIfNotTechPreviewNoUpgrade skips the test if a cluster is not a TechPreviewNoUpgrade cluster
func SkipIfNotTechPreviewNoUpgrade(ctx context.Context, restConfig *rest.Config) {
	if !IsTechPreviewNoUpgrade(ctx, restConfig) {
		g.Skip("This test is skipped because the Tech Preview NoUpgrade is not enabled")
	}
}

const (
	// fauxinnati mocks Cincinnati Update Graph Server for OpenShift
	FauxinnatiAPIURL = "https://fauxinnati-fauxinnati.apps.ota-stage.q2z4.p1.openshiftapps.com/api/upgrades_info/graph"
)

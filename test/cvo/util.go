package cvo

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	configv1 "github.com/openshift/api/config/v1"
	clientconfigv1 "github.com/openshift/client-go/config/clientset/versioned"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
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
		Skip("Skipping test: running on HyperShift hosted cluster!")
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

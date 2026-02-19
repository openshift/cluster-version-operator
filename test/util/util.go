package util

import (
	"context"
	"fmt"
	"time"

	imagev1 "github.com/openshift/api/image/v1"
	securityv1 "github.com/openshift/api/security/v1"
	"github.com/openshift/cluster-version-operator/lib/resourceread"
	"github.com/openshift/library-go/pkg/manifest"
	operatorsv1 "github.com/operator-framework/api/pkg/operators/v1"
	monitoringv1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1"
	admissionregistrationv1 "k8s.io/api/admissionregistration/v1"
	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"

	g "github.com/onsi/ginkgo/v2"
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

// GetManifestExpectNotFoundError get manifest to check if it is installed
func GetManifestExpectNotFoundError(ms manifest.Manifest) error {
	obj, err := resourceread.Read(ms.Raw)
	if err != nil {
		return err
	}
	switch typedObject := obj.(type) {
	case *imagev1.ImageStream:
		client, err := getImageClient()
		if err != nil {
			return err
		}
		_, err = client.ImageStreams(typedObject.Namespace).Get(context.TODO(), typedObject.Name, metav1.GetOptions{})
		return err
	case *securityv1.SecurityContextConstraints:
		client, err := getSecurityClient()
		if err != nil {
			return err
		}
		_, err = client.SecurityContextConstraints().Get(context.TODO(), typedObject.Name, metav1.GetOptions{})
		return err
	case *operatorsv1.OperatorGroup:
		client, err := getOperatorClient()
		if err != nil {
			return err
		}
		_, err = client.OperatorGroups(typedObject.Namespace).Get(context.TODO(), typedObject.Name, metav1.GetOptions{})
		return err
	case *monitoringv1.PrometheusRule:
		client, err := getMonitoringClient()
		if err != nil {
			return err
		}
		_, err = client.PrometheusRules(typedObject.Namespace).Get(context.TODO(), typedObject.Name, metav1.GetOptions{})
		return err
	case *monitoringv1.ServiceMonitor:
		client, err := getMonitoringClient()
		if err != nil {
			return err
		}
		_, err = client.ServiceMonitors(typedObject.Namespace).Get(context.TODO(), typedObject.Name, metav1.GetOptions{})
		return err
	case *admissionregistrationv1.ValidatingWebhookConfiguration:
		client, err := getAdmissionRegistrationClient()
		if err != nil {
			return err
		}
		_, err = client.ValidatingWebhookConfigurations().Get(context.TODO(), typedObject.Name, metav1.GetOptions{})
		return err
	case *appsv1.DaemonSet:
		client, err := getAppsClient()
		if err != nil {
			return err
		}
		_, err = client.DaemonSets(typedObject.Namespace).Get(context.TODO(), typedObject.Name, metav1.GetOptions{})
		return err
	case *appsv1.Deployment:
		client, err := getAppsClient()
		if err != nil {
			return err
		}
		_, err = client.Deployments(typedObject.Namespace).Get(context.TODO(), typedObject.Name, metav1.GetOptions{})
		return err
	case *batchv1.CronJob:
		client, err := getBatchClient()
		if err != nil {
			return err
		}
		_, err = client.CronJobs(typedObject.Namespace).Get(context.TODO(), typedObject.Name, metav1.GetOptions{})
		return err
	case *batchv1.Job:
		client, err := getBatchClient()
		if err != nil {
			return err
		}
		_, err = client.Jobs(typedObject.Namespace).Get(context.TODO(), typedObject.Name, metav1.GetOptions{})
		return err
	case *corev1.ConfigMap:
		client, err := getCoreV1Client()
		if err != nil {
			return err
		}
		_, err = client.ConfigMaps(typedObject.Namespace).Get(context.TODO(), typedObject.Name, metav1.GetOptions{})
		return err
	case *corev1.Namespace:
		client, err := getCoreV1Client()
		if err != nil {
			return err
		}
		_, err = client.Namespaces().Get(context.TODO(), typedObject.Name, metav1.GetOptions{})
		return err
	case *corev1.Secret:
		client, err := getCoreV1Client()
		if err != nil {
			return err
		}
		_, err = client.Secrets(typedObject.Namespace).Get(context.TODO(), typedObject.Name, metav1.GetOptions{})
		return err
	case *corev1.Service:
		client, err := getCoreV1Client()
		if err != nil {
			return err
		}
		_, err = client.Services(typedObject.Namespace).Get(context.TODO(), typedObject.Name, metav1.GetOptions{})
		return err
	case *corev1.ServiceAccount:
		client, err := getCoreV1Client()
		if err != nil {
			return err
		}
		_, err = client.ServiceAccounts(typedObject.Namespace).Get(context.TODO(), typedObject.Name, metav1.GetOptions{})
		return err
	case *rbacv1.ClusterRole:
		client, err := getRbacV1Client()
		if err != nil {
			return err
		}
		_, err = client.ClusterRoles().Get(context.TODO(), typedObject.Name, metav1.GetOptions{})
		return err
	case *rbacv1.ClusterRoleBinding:
		client, err := getRbacV1Client()
		if err != nil {
			return err
		}
		_, err = client.ClusterRoleBindings().Get(context.TODO(), typedObject.Name, metav1.GetOptions{})
		return err
	case *rbacv1.Role:
		client, err := getRbacV1Client()
		if err != nil {
			return err
		}
		_, err = client.Roles(typedObject.Namespace).Get(context.TODO(), typedObject.Name, metav1.GetOptions{})
		return err
	case *rbacv1.RoleBinding:
		client, err := getRbacV1Client()
		if err != nil {
			return err
		}
		_, err = client.RoleBindings(typedObject.Namespace).Get(context.TODO(), typedObject.Name, metav1.GetOptions{})
		return err
	case *apiextensionsv1.CustomResourceDefinition:
		client, err := getApiextV1Client()
		if err != nil {
			return err
		}
		_, err = client.CustomResourceDefinitions().Get(context.TODO(), typedObject.Name, metav1.GetOptions{})
		return err
	default:
		return fmt.Errorf("unrecognized manifest type: %T", obj)
	}
}

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

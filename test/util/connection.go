package util

import (
	"fmt"

	imageclientv1 "github.com/openshift/client-go/image/clientset/versioned/typed/image/v1"
	securityclientv1 "github.com/openshift/client-go/security/clientset/versioned/typed/security/v1"
	operatorsclientv1 "github.com/operator-framework/operator-lifecycle-manager/pkg/api/client/clientset/versioned/typed/operators/v1"
	monitoringclientv1 "github.com/prometheus-operator/prometheus-operator/pkg/client/versioned/typed/monitoring/v1"
	apiextclientv1 "k8s.io/apiextensions-apiserver/pkg/client/clientset/clientset/typed/apiextensions/v1"
	admissionregistrationclientv1 "k8s.io/client-go/kubernetes/typed/admissionregistration/v1"
	appsclientv1 "k8s.io/client-go/kubernetes/typed/apps/v1"
	batchclientv1 "k8s.io/client-go/kubernetes/typed/batch/v1"
	coreclientv1 "k8s.io/client-go/kubernetes/typed/core/v1"
	rbacclientv1 "k8s.io/client-go/kubernetes/typed/rbac/v1"
)

func getAdmissionRegistrationClient() (*admissionregistrationclientv1.AdmissionregistrationV1Client, error) {
	config, err := GetRestConfig()
	if err != nil {
		return nil, fmt.Errorf("unable to load build config: %w", err)
	}
	return admissionregistrationclientv1.NewForConfigOrDie(config), nil
}

func getApiextV1Client() (*apiextclientv1.ApiextensionsV1Client, error) {
	config, err := GetRestConfig()
	if err != nil {
		return nil, fmt.Errorf("unable to load build config: %w", err)
	}
	return apiextclientv1.NewForConfigOrDie(config), nil
}

func getAppsClient() (*appsclientv1.AppsV1Client, error) {
	config, err := GetRestConfig()
	if err != nil {
		return nil, fmt.Errorf("unable to load build config: %w", err)
	}
	return appsclientv1.NewForConfigOrDie(config), nil
}

func getBatchClient() (*batchclientv1.BatchV1Client, error) {
	config, err := GetRestConfig()
	if err != nil {
		return nil, fmt.Errorf("unable to load build config: %w", err)
	}
	return batchclientv1.NewForConfigOrDie(config), nil
}

func getCoreV1Client() (*coreclientv1.CoreV1Client, error) {
	config, err := GetRestConfig()
	if err != nil {
		return nil, fmt.Errorf("unable to load build config: %w", err)
	}
	return coreclientv1.NewForConfigOrDie(config), nil
}

func getImageClient() (*imageclientv1.ImageV1Client, error) {
	config, err := GetRestConfig()
	if err != nil {
		return nil, fmt.Errorf("unable to load build config: %w", err)
	}
	return imageclientv1.NewForConfigOrDie(config), nil
}

func getMonitoringClient() (*monitoringclientv1.MonitoringV1Client, error) {
	config, err := GetRestConfig()
	if err != nil {
		return nil, fmt.Errorf("unable to load build config: %w", err)
	}
	return monitoringclientv1.NewForConfigOrDie(config), nil
}

func getOperatorClient() (*operatorsclientv1.OperatorsV1Client, error) {
	config, err := GetRestConfig()
	if err != nil {
		return nil, fmt.Errorf("unable to load build config: %w", err)
	}
	return operatorsclientv1.NewForConfigOrDie(config), nil
}

func getRbacV1Client() (*rbacclientv1.RbacV1Client, error) {
	config, err := GetRestConfig()
	if err != nil {
		return nil, fmt.Errorf("unable to load build config: %w", err)
	}
	return rbacclientv1.NewForConfigOrDie(config), nil
}

func getSecurityClient() (*securityclientv1.SecurityV1Client, error) {
	config, err := GetRestConfig()
	if err != nil {
		return nil, fmt.Errorf("unable to load build config: %w", err)
	}
	return securityclientv1.NewForConfigOrDie(config), nil
}

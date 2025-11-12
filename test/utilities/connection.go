package utilities

import (
	"errors"
	"fmt"
	"os"

	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"

	configclientv1 "github.com/openshift/client-go/config/clientset/versioned/typed/config/v1"
)

// getKubeConfig get KUBECONFIG file from environment variable
func getKubeConfig() (*rest.Config, error) {
	configPath, present := os.LookupEnv("KUBECONFIG")
	if !present {
		return nil, errors.New("the environment variable KUBECONFIG must be set")
	}
	config, err := clientcmd.BuildConfigFromFlags("", configPath)
	return config, err
}

// getKubeClient creates a kubernetes.Clientset instance.
func getKubeClient() (*kubernetes.Clientset, error) {
	config, err := getKubeConfig()
	if err != nil {
		return nil, fmt.Errorf("unable to load build config: %w", err)
	}
	// Create the Clientset
	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		return nil, fmt.Errorf("unable to create a Kubernetes clientset: %w", err)
	}

	return clientset, nil
}

// getV1Client creates a configclientv1.ConfigV1Client instance.
func getV1Client() (*configclientv1.ConfigV1Client, error) {
	config, err := getKubeConfig()
	if err != nil {
		return nil, fmt.Errorf("unable to load build config: %w", err)
	}
	// Create the Clientset
	clientset, err := configclientv1.NewForConfig(config)
	if err != nil {
		return nil, fmt.Errorf("unable to create a configclientv1 clientset: %w", err)
	}

	return clientset, nil
}

// MustGetKubeClient creates a kubernetes.Clientset instance, or panics on failures.
func MustGetKubeClient() *kubernetes.Clientset {
	clientset, err := getKubeClient()
	if err != nil {
		panic("unable to create a Kubernetes clientset: " + err.Error())
	}
	return clientset
}

// MustGetV1Client creates a configclientv1.ConfigV1Client instance, or panics on failures.
func MustGetV1Client() *configclientv1.ConfigV1Client {
	clientset, err := getV1Client()
	if err != nil {
		panic("unable to create a configclientv1 clientset: " + err.Error())
	}
	return clientset
}

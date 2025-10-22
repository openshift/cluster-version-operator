package utilities

import (
	"errors"
	"fmt"
	"os"

	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
)

// getKubeClient creates a Kubernetes clientset.
func getKubeClient() (*kubernetes.Clientset, error) {
	configPath, present := os.LookupEnv("KUBECONFIG")
	if !present {
		return nil, errors.New("the environment variable KUBECONFIG must be set")
	}
	config, err := clientcmd.BuildConfigFromFlags("", configPath)
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

// MustGetKubeClient creates a Kubernetes clientset, or panics on failures.
func MustGetKubeClient() *kubernetes.Clientset {
	clientset, err := getKubeClient()
	if err != nil {
		panic("unable to create a Kubernetes clientset: " + err.Error())
	}
	return clientset
}

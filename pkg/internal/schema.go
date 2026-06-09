package internal

import (
	"fmt"
	"sync"

	"k8s.io/client-go/kubernetes/scheme"

	proposalv1alpha1 "github.com/openshift/lightspeed-agentic-operator/api/v1alpha1"
)

var (
	addSchemesOnce sync.Once
	addSchemesErr  error
)

// AddSchemes registers the proposalv1alpha1 scheme with the global Kubernetes scheme.
// This function is safe to call concurrently and will only execute once.
func AddSchemes() error {
	addSchemesOnce.Do(func() {
		if err := proposalv1alpha1.AddToScheme(scheme.Scheme); err != nil {
			addSchemesErr = fmt.Errorf("failed to add proposalv1alpha1 to scheme: %w", err)
		}
	})
	return addSchemesErr
}

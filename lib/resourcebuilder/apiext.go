package resourcebuilder

import (
	"context"
	"fmt"
	"time"

	apiextv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/klog/v2"
)

const (
	crdEstablishedPollInterval = 100 * time.Millisecond
	crdEstablishedPollTimeout  = 5 * time.Second
)

// checkCRDEstablished checks whether a CRD has Established=True in its status
// conditions. Returns nil if established, or an error describing the current state.
func checkCRDEstablished(crd *apiextv1.CustomResourceDefinition) error {
	for _, condition := range crd.Status.Conditions {
		if condition.Type == apiextv1.Established {
			if condition.Status == apiextv1.ConditionTrue {
				return nil
			}
			return fmt.Errorf("CustomResourceDefinition %s Established=%s, %s: %s", crd.Name, condition.Status, condition.Reason, condition.Message)
		}
	}

	return fmt.Errorf("CustomResourceDefinition %s does not declare an Established status condition: %v", crd.Name, crd.Status.Conditions)
}

func (b *builder) checkCustomResourceDefinitionHealth(ctx context.Context, crd *apiextv1.CustomResourceDefinition) error {
	if b.mode == InitializingMode {
		return nil
	}

	if err := checkCRDEstablished(crd); err == nil {
		return nil
	}

	// When status conditions are empty, the CRD was likely just created and the
	// API server has not yet populated conditions. This is a known TOCTOU race:
	// CVO creates the CRD and immediately checks Established, but the API server
	// needs time to validate the schema and set conditions. Poll briefly to allow
	// the API server to complete CRD registration before returning an error.
	if len(crd.Status.Conditions) == 0 {
		klog.V(4).Infof("CRD %s has empty status conditions, polling for Established", crd.Name)
		pollErr := wait.PollUntilContextTimeout(ctx, crdEstablishedPollInterval, crdEstablishedPollTimeout, false, func(ctx context.Context) (bool, error) {
			updated, err := b.apiextensionsClientv1.CustomResourceDefinitions().Get(ctx, crd.Name, metav1.GetOptions{})
			if err != nil {
				return false, err
			}
			crd = updated
			return checkCRDEstablished(crd) == nil, nil
		})
		if pollErr == nil {
			return nil
		}
		klog.V(2).Infof("CRD %s did not reach Established=True after polling for %s: %v", crd.Name, crdEstablishedPollTimeout, pollErr)
	}

	return checkCRDEstablished(crd)
}

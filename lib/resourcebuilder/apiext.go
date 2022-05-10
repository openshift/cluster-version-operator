package resourcebuilder

import (
	"context"
	"fmt"

	apiextv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
)

func (b *builder) checkCustomResourceDefinitionHealth(ctx context.Context, crd *apiextv1.CustomResourceDefinition) error {
	if b.mode == InitializingMode {
		return nil
	}

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

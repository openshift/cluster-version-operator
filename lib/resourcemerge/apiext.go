package resourcemerge

import (
	"strings"

	apiextv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"

	"k8s.io/apimachinery/pkg/api/equality"
)

// EnsureCustomResourceDefinitionV1 ensures that the existing matches the required.
// modified is set to true when existing had to be updated with required.
func EnsureCustomResourceDefinitionV1(modified *bool, existing *apiextv1.CustomResourceDefinition, required apiextv1.CustomResourceDefinition) {
	EnsureObjectMeta(modified, &existing.ObjectMeta, required.ObjectMeta)

	// apply defaults to blue print
	if len(required.Spec.Names.Singular) == 0 {
		required.Spec.Names.Singular = strings.ToLower(required.Spec.Names.Kind)
	}
	if len(required.Spec.Names.ListKind) == 0 {
		required.Spec.Names.ListKind = required.Spec.Names.Kind + "List"
	}

	// we stomp everything
	if !equality.Semantic.DeepEqual(existing.Spec, required.Spec) {
		*modified = true
		existing.Spec = required.Spec
	}
}

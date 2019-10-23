package resourcemerge

import (
	"strings"

	apiextv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	apiextv1beta1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1beta1"
	"k8s.io/utils/pointer"

	"k8s.io/apimachinery/pkg/api/equality"
)

// EnsureCustomResourceDefinitionV1beta1 ensures that the existing matches the required.
// modified is set to true when existing had to be updated with required.
func EnsureCustomResourceDefinitionV1beta1(modified *bool, existing *apiextv1beta1.CustomResourceDefinition, required apiextv1beta1.CustomResourceDefinition) {
	EnsureObjectMeta(modified, &existing.ObjectMeta, required.ObjectMeta)

	// apply defaults to blue print
	if len(required.Spec.Versions) > 0 && len(required.Spec.Version) == 0 {
		required.Spec.Version = required.Spec.Versions[0].Name
	}
	if len(required.Spec.Versions) == 0 && len(required.Spec.Version) > 0 {
		required.Spec.Versions = []apiextv1beta1.CustomResourceDefinitionVersion{
			{
				Name:    required.Spec.Version,
				Served:  true,
				Storage: true,
			},
		}
	}
	if required.Spec.PreserveUnknownFields == nil {
		required.Spec.PreserveUnknownFields = pointer.BoolPtr(true)
	}
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

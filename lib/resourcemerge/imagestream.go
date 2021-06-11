package resourcemerge

import (
	imagev1 "github.com/openshift/api/image/v1"
	"k8s.io/apimachinery/pkg/api/equality"
)

func EnsureImagestreamv1(modified *bool, existing *imagev1.ImageStream, required imagev1.ImageStream) {
	EnsureObjectMeta(modified, &existing.ObjectMeta, required.ObjectMeta)
	if !equality.Semantic.DeepEqual(existing.Spec.LookupPolicy, required.Spec.LookupPolicy) {
		*modified = true
		existing.Spec.LookupPolicy = required.Spec.LookupPolicy
	}
	for _, required := range required.Spec.Tags {
		var existingCurr *imagev1.TagReference
		for j, curr := range existing.Spec.Tags {
			if curr.Name == required.Name {
				existingCurr = &existing.Spec.Tags[j]
				break
			}
		}
		if existingCurr == nil {
			*modified = true
			existing.Spec.Tags = append(existing.Spec.Tags, imagev1.TagReference{})
			existingCurr = &existing.Spec.Tags[len(existing.Spec.Tags)-1]
		}
		ensureTagReferencev1(modified, existingCurr, required)
	}
}

func ensureTagReferencev1(modified *bool, existing *imagev1.TagReference, required imagev1.TagReference) {
	if !equality.Semantic.DeepEqual(existing.ImportPolicy, required.ImportPolicy) {
		*modified = true
		existing.ImportPolicy = required.ImportPolicy
	}
	if !equality.Semantic.DeepEqual(existing.From, required.From) {
		*modified = true
		existing.From = required.From
	}
}

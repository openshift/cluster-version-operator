package resourcemerge

import (
	monv1alpha1 "github.com/openshift/api/monitoring/v1alpha1"
	//"k8s.io/apimachinery/pkg/api/equality"
)

func EnsurePrometheusRulev1alpha1(modified *bool, existing *monv1alpha1.AlertingRule, required monv1alpha1.AlertingRule) {
	/*
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
				required.DeepCopyInto(&existing.Spec.Tags[len(existing.Spec.Tags)-1])
			} else {
				ensureTagReferencev1(modified, existingCurr, required)
			}
		}
	*/
}

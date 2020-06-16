package resourcemerge

import (
	appsv1 "k8s.io/api/apps/v1"
	"k8s.io/apimachinery/pkg/api/equality"
)

// EnsureDeployment ensures that the existing matches the required.
// modified is set to true when existing had to be upgraded with required.
func EnsureDeployment(modified *bool, existing *appsv1.Deployment, required appsv1.Deployment) {
	EnsureObjectMeta(modified, &existing.ObjectMeta, required.ObjectMeta)

	if required.Spec.Replicas != nil && *required.Spec.Replicas != *existing.Spec.Replicas {
		*modified = true
		existing.Spec.Replicas = required.Spec.Replicas
	}

	if existing.Spec.Selector == nil && required.Spec.Selector != nil {
		*modified = true
		existing.Spec.Selector = required.Spec.Selector
	}
	if !equality.Semantic.DeepEqual(existing.Spec.Selector, required.Spec.Selector) {
		*modified = true
		existing.Spec.Selector = required.Spec.Selector
	}

	ensurePodTemplateSpec(modified, &existing.Spec.Template, required.Spec.Template)
}

// EnsureDaemonSet ensures that the existing matches the required.
// modified is set to true when existing had to be upgraded with required.
func EnsureDaemonSet(modified *bool, existing *appsv1.DaemonSet, required appsv1.DaemonSet) {
	EnsureObjectMeta(modified, &existing.ObjectMeta, required.ObjectMeta)

	if existing.Spec.Selector == nil {
		*modified = true
		existing.Spec.Selector = required.Spec.Selector
	}
	if !equality.Semantic.DeepEqual(existing.Spec.Selector, required.Spec.Selector) {
		*modified = true
		existing.Spec.Selector = required.Spec.Selector
	}

	ensurePodTemplateSpec(modified, &existing.Spec.Template, required.Spec.Template)
}

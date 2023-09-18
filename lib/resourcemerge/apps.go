package resourcemerge

import (
	appsv1 "k8s.io/api/apps/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/utils/pointer"
)

// EnsureDeployment ensures that the existing matches the required.
// modified is set to true when existing had to be updated with required.
func EnsureDeployment(modified *bool, existing *appsv1.Deployment, required appsv1.Deployment) {
	EnsureObjectMeta(modified, &existing.ObjectMeta, required.ObjectMeta)

	ensureReplicasDefault(&required)
	if existing.Spec.Replicas == nil || *required.Spec.Replicas != *existing.Spec.Replicas {
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
	if existing.Spec.Paused != required.Spec.Paused {
		*modified = true
		existing.Spec.Paused = required.Spec.Paused
	}
	if existing.Spec.MinReadySeconds != required.Spec.MinReadySeconds {
		*modified = true
		existing.Spec.MinReadySeconds = required.Spec.MinReadySeconds
	}
	if required.Spec.RevisionHistoryLimit != nil && !equality.Semantic.DeepEqual(existing.Spec.RevisionHistoryLimit, required.Spec.RevisionHistoryLimit) {
		*modified = true
		existing.Spec.RevisionHistoryLimit = required.Spec.RevisionHistoryLimit
	}
	if required.Spec.ProgressDeadlineSeconds != nil && !equality.Semantic.DeepEqual(existing.Spec.ProgressDeadlineSeconds, required.Spec.ProgressDeadlineSeconds) {
		*modified = true
		existing.Spec.ProgressDeadlineSeconds = required.Spec.ProgressDeadlineSeconds
	}

	ensureStrategyDefault(&required)
	if !equality.Semantic.DeepEqual(existing.Spec.Strategy, required.Spec.Strategy) {
		*modified = true
		existing.Spec.Strategy = required.Spec.Strategy
	}

	ensurePodTemplateSpec(modified, &existing.Spec.Template, required.Spec.Template)
}

func ensureReplicasDefault(required *appsv1.Deployment) {
	if required.Spec.Replicas == nil {
		required.Spec.Replicas = pointer.Int32(1)
	}
}

func ensureStrategyDefault(required *appsv1.Deployment) {
	if len(required.Spec.Strategy.Type) == 0 || required.Spec.Strategy.Type == appsv1.RollingUpdateDeploymentStrategyType {
		required.Spec.Strategy.Type = appsv1.RollingUpdateDeploymentStrategyType
		if required.Spec.Strategy.RollingUpdate == nil {
			required.Spec.Strategy.RollingUpdate = &appsv1.RollingUpdateDeployment{}
		}
		if required.Spec.Strategy.RollingUpdate.MaxUnavailable == nil {
			twentyFivePercent := intstr.FromString("25%")
			required.Spec.Strategy.RollingUpdate.MaxUnavailable = &twentyFivePercent
		}
		if required.Spec.Strategy.RollingUpdate.MaxSurge == nil {
			twentyFivePercent := intstr.FromString("25%")
			required.Spec.Strategy.RollingUpdate.MaxSurge = &twentyFivePercent
		}
	}
}

// EnsureDaemonSet ensures that the existing matches the required.
// modified is set to true when existing had to be updated with required.
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

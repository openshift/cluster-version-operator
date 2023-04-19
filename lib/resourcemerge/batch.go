package resourcemerge

import (
	batchv1 "k8s.io/api/batch/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	"k8s.io/utils/pointer"
)

// EnsureJob ensures that the existing matches the required.
// modified is set to true when existing had to be updated with required.
func EnsureJob(modified *bool, existing *batchv1.Job, required batchv1.Job) {
	EnsureObjectMeta(modified, &existing.ObjectMeta, required.ObjectMeta)
	ensureJobSpec(modified, &existing.Spec, required.Spec)
}

func ensureJobSpec(modified *bool, existing *batchv1.JobSpec, required batchv1.JobSpec) {
	if required.Selector != nil {
		panic("spec.selector is not supported in Job manifests")
	}
	ensureJobSpecDefault(&required)
	setInt32Ptr(modified, &existing.Parallelism, required.Parallelism)
	setInt32Ptr(modified, &existing.Completions, required.Completions)
	setInt64Ptr(modified, &existing.ActiveDeadlineSeconds, required.ActiveDeadlineSeconds)
	setInt32Ptr(modified, &existing.BackoffLimit, required.BackoffLimit)
	setBoolPtr(modified, &existing.ManualSelector, required.ManualSelector)
	setInt32Ptr(modified, &existing.TTLSecondsAfterFinished, required.TTLSecondsAfterFinished)
	setBoolPtr(modified, &existing.Suspend, required.Suspend)
	ensurePodTemplateSpec(modified, &existing.Template, required.Template)
	if !equality.Semantic.DeepEqual(existing.CompletionMode, required.CompletionMode) {
		*modified = true
		existing.CompletionMode = required.CompletionMode
	}
	if !equality.Semantic.DeepEqual(existing.PodFailurePolicy, required.PodFailurePolicy) {
		*modified = true
		existing.PodFailurePolicy = required.PodFailurePolicy
	}
}

func ensureJobSpecDefault(required *batchv1.JobSpec) {
	if required.BackoffLimit == nil {
		required.BackoffLimit = pointer.Int32(6)
	}
	if required.CompletionMode == nil {
		completionMode := batchv1.NonIndexedCompletion
		required.CompletionMode = &completionMode
	}
	if required.Suspend == nil {
		required.Suspend = pointer.Bool(false)
	}
}

package resourcemerge

import (
	batchv1 "k8s.io/api/batch/v1"
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
	setInt32Ptr(modified, &existing.Parallelism, required.Parallelism)
	setInt32Ptr(modified, &existing.Completions, required.Completions)
	setInt64Ptr(modified, &existing.ActiveDeadlineSeconds, required.ActiveDeadlineSeconds)
	setInt32Ptr(modified, &existing.BackoffLimit, required.BackoffLimit)
	setBoolPtr(modified, &existing.ManualSelector, required.ManualSelector)
	ensurePodTemplateSpec(modified, &existing.Template, required.Template)
}

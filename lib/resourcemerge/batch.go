package resourcemerge

import (
	batchv1 "k8s.io/api/batch/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	"k8s.io/utils/ptr"
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
		required.BackoffLimit = ptr.To(int32(6))
	}
	if required.CompletionMode == nil {
		completionMode := batchv1.NonIndexedCompletion
		required.CompletionMode = &completionMode
	}
	if required.Suspend == nil {
		required.Suspend = ptr.To(false)
	}
}

// EnsureCronJob ensures that the existing matches the required.
// modified is set to true when existing had to be updated with required.
func EnsureCronJob(modified *bool, existing *batchv1.CronJob, required batchv1.CronJob) {
	EnsureObjectMeta(modified, &existing.ObjectMeta, required.ObjectMeta)
	ensureCronJobSpec(modified, &existing.Spec, required.Spec)
}

func ensureCronJobSpec(modified *bool, existing *batchv1.CronJobSpec, required batchv1.CronJobSpec) {
	ensureCronJobSpecDefault(&required)
	setInt64Ptr(modified, &existing.StartingDeadlineSeconds, required.StartingDeadlineSeconds)
	setBoolPtr(modified, &existing.Suspend, required.Suspend)
	setInt32Ptr(modified, &existing.SuccessfulJobsHistoryLimit, required.SuccessfulJobsHistoryLimit)
	setInt32Ptr(modified, &existing.FailedJobsHistoryLimit, required.FailedJobsHistoryLimit)
	if existing.Schedule != required.Schedule {
		*modified = true
		existing.Schedule = required.Schedule
	}
	if !equality.Semantic.DeepEqual(existing.TimeZone, required.TimeZone) {
		*modified = true
		existing.TimeZone = required.TimeZone
	}
	if existing.ConcurrencyPolicy != required.ConcurrencyPolicy {
		*modified = true
		existing.ConcurrencyPolicy = required.ConcurrencyPolicy
	}
	ensureJobTemplateSpec(modified, &existing.JobTemplate, required.JobTemplate)
}

func ensureCronJobSpecDefault(required *batchv1.CronJobSpec) {
	if required.ConcurrencyPolicy == "" {
		required.ConcurrencyPolicy = batchv1.AllowConcurrent
	}
	if required.FailedJobsHistoryLimit == nil {
		required.FailedJobsHistoryLimit = ptr.To(int32(1))
	}
	if required.SuccessfulJobsHistoryLimit == nil {
		required.SuccessfulJobsHistoryLimit = ptr.To(int32(3))
	}
	if required.Suspend == nil {
		required.Suspend = ptr.To(false)
	}
}

func ensureJobTemplateSpec(modified *bool, existing *batchv1.JobTemplateSpec, required batchv1.JobTemplateSpec) {
	EnsureObjectMeta(modified, &existing.ObjectMeta, required.ObjectMeta)
	ensureJobSpec(modified, &existing.Spec, required.Spec)
}

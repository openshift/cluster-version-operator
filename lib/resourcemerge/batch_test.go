package resourcemerge

import (
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	batchv1 "k8s.io/api/batch/v1"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/pointer"
)

func TestEnsureJob_JobStatus(t *testing.T) {
	tests := []struct {
		name     string
		existing batchv1.JobStatus
		required batchv1.JobStatus
	}{
		{
			name: "cvo should ignore same status field",
			existing: batchv1.JobStatus{
				StartTime: &metav1.Time{Time: time.Unix(0, 0)},
				Active:    3,
				Succeeded: 4,
				Failed:    2,
				Ready:     pointer.Int32(1),
			},
			required: batchv1.JobStatus{
				StartTime: &metav1.Time{Time: time.Unix(0, 0)},
				Active:    3,
				Succeeded: 4,
				Failed:    2,
				Ready:     pointer.Int32(1),
			},
		},
		{
			name: "cvo should ignore different status field",
			existing: batchv1.JobStatus{
				StartTime: &metav1.Time{Time: time.Unix(0, 0)},
				Active:    3,
				Succeeded: 4,
				Failed:    2,
				Ready:     pointer.Int32(1),
			},
			required: batchv1.JobStatus{},
		},
	}

	var expected batchv1.Job
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			existing := batchv1.Job{Status: test.existing}
			required := batchv1.Job{Status: test.required}
			defaultJob(&existing, existing)
			existing.DeepCopyInto(&expected)
			modified := pointer.Bool(false)
			EnsureJob(modified, &existing, required)
			if *modified != false {
				t.Errorf("mismatch modified got: %v want: %v", *modified, false)
			}
			if !equality.Semantic.DeepEqual(existing, expected) {
				t.Errorf("unexpected: %s", cmp.Diff(expected, existing))
			}
		})
	}
}

func TestEnsureJob_JobSpec(t *testing.T) {
	NonIndexedCompletion := batchv1.NonIndexedCompletion
	IndexedCompletion := batchv1.IndexedCompletion
	tests := []struct {
		name     string
		existing batchv1.JobSpec
		required batchv1.JobSpec

		expectedModified bool
	}{
		{
			name: "same parallelism",
			existing: batchv1.JobSpec{
				Parallelism: pointer.Int32(10),
			},
			required: batchv1.JobSpec{
				Parallelism: pointer.Int32(10),
			},
			expectedModified: false,
		},
		{
			name: "different parallelism",
			existing: batchv1.JobSpec{
				Parallelism: pointer.Int32(10),
			},
			required: batchv1.JobSpec{
				Parallelism: pointer.Int32(5),
			},
			expectedModified: true,
		},
		{
			name: "same completions",
			existing: batchv1.JobSpec{
				Completions: pointer.Int32(10),
			},
			required: batchv1.JobSpec{
				Completions: pointer.Int32(10),
			},
			expectedModified: false,
		},
		{
			name: "different completions",
			existing: batchv1.JobSpec{
				Completions: pointer.Int32(10),
			},
			required: batchv1.JobSpec{
				Completions: pointer.Int32(5),
			},
			expectedModified: true,
		},
		{
			name: "same active deadline seconds",
			existing: batchv1.JobSpec{
				ActiveDeadlineSeconds: pointer.Int64(10),
			},
			required: batchv1.JobSpec{
				ActiveDeadlineSeconds: pointer.Int64(10),
			},
			expectedModified: false,
		},
		{
			name: "different active deadline seconds",
			existing: batchv1.JobSpec{
				ActiveDeadlineSeconds: pointer.Int64(10),
			},
			required: batchv1.JobSpec{
				ActiveDeadlineSeconds: pointer.Int64(5),
			},
			expectedModified: true,
		},
		{
			name: "same pod failure policy",
			existing: batchv1.JobSpec{
				PodFailurePolicy: &batchv1.PodFailurePolicy{
					Rules: []batchv1.PodFailurePolicyRule{
						{
							Action: batchv1.PodFailurePolicyActionIgnore,
							OnPodConditions: []batchv1.PodFailurePolicyOnPodConditionsPattern{
								{
									Type:   v1.PodReady,
									Status: metav1.StatusSuccess,
								},
							},
						},
					},
				},
			},
			required: batchv1.JobSpec{
				PodFailurePolicy: &batchv1.PodFailurePolicy{
					Rules: []batchv1.PodFailurePolicyRule{
						{
							Action: batchv1.PodFailurePolicyActionIgnore,
							OnPodConditions: []batchv1.PodFailurePolicyOnPodConditionsPattern{
								{
									Type:   v1.PodReady,
									Status: metav1.StatusSuccess,
								},
							},
						},
					},
				},
			},
			expectedModified: false,
		},
		{
			name: "different pod failure policy",
			existing: batchv1.JobSpec{
				PodFailurePolicy: &batchv1.PodFailurePolicy{
					Rules: []batchv1.PodFailurePolicyRule{
						{
							Action: batchv1.PodFailurePolicyActionIgnore,
							OnPodConditions: []batchv1.PodFailurePolicyOnPodConditionsPattern{
								{
									Type:   v1.PodReady,
									Status: metav1.StatusSuccess,
								},
							},
						},
					},
				},
			},
			required:         batchv1.JobSpec{},
			expectedModified: true,
		},
		{
			name: "same backofflimit count",
			existing: batchv1.JobSpec{
				BackoffLimit: pointer.Int32(2),
			},
			required: batchv1.JobSpec{
				BackoffLimit: pointer.Int32(2),
			},
			expectedModified: false,
		},
		{
			name: "different backofflimit count",
			existing: batchv1.JobSpec{
				BackoffLimit: pointer.Int32(2),
			},
			required: batchv1.JobSpec{
				BackoffLimit: pointer.Int32(3),
			},
			expectedModified: true,
		},
		{
			name: "implicit backofflimit count",
			existing: batchv1.JobSpec{
				BackoffLimit: pointer.Int32(6),
			},
			required:         batchv1.JobSpec{},
			expectedModified: false,
		},
		{
			name: "same manual selector",
			existing: batchv1.JobSpec{
				ManualSelector: pointer.Bool(true),
			},
			required: batchv1.JobSpec{
				ManualSelector: pointer.Bool(true),
			},
			expectedModified: false,
		},
		{
			name: "different manual selector",
			existing: batchv1.JobSpec{
				ManualSelector: pointer.Bool(true),
			},
			required: batchv1.JobSpec{
				ManualSelector: pointer.Bool(false),
			},
			expectedModified: true,
		},
		{
			name: "same template",
			existing: batchv1.JobSpec{
				Template: v1.PodTemplateSpec{
					Spec: v1.PodSpec{
						Containers: []v1.Container{
							{
								Name:  "app",
								Image: "app:latest",
							},
						},
					},
				},
			},
			required: batchv1.JobSpec{
				Template: v1.PodTemplateSpec{
					Spec: v1.PodSpec{
						Containers: []v1.Container{
							{
								Name:  "app",
								Image: "app:latest",
							},
						},
					},
				},
			},
			expectedModified: false,
		},
		{
			name: "different template",
			existing: batchv1.JobSpec{
				Template: v1.PodTemplateSpec{
					Spec: v1.PodSpec{
						Containers: []v1.Container{
							{
								Name:  "app",
								Image: "app:latest",
							},
						},
					},
				},
			},
			required:         batchv1.JobSpec{},
			expectedModified: true,
		},
		{
			name: "same TTL seconds after finished",
			existing: batchv1.JobSpec{
				TTLSecondsAfterFinished: pointer.Int32(10),
			},
			required: batchv1.JobSpec{
				TTLSecondsAfterFinished: pointer.Int32(10),
			},
			expectedModified: false,
		},
		{
			name: "different TTL seconds after finished",
			existing: batchv1.JobSpec{
				TTLSecondsAfterFinished: pointer.Int32(10),
			},
			required: batchv1.JobSpec{
				TTLSecondsAfterFinished: pointer.Int32(5),
			},
			expectedModified: true,
		},
		{
			name: "same completion mode",
			existing: batchv1.JobSpec{
				CompletionMode: &IndexedCompletion,
			},
			required: batchv1.JobSpec{
				CompletionMode: &IndexedCompletion,
			},
			expectedModified: false,
		},
		{
			name: "different completion mode",
			existing: batchv1.JobSpec{
				CompletionMode: &IndexedCompletion,
			},
			required: batchv1.JobSpec{
				CompletionMode: &NonIndexedCompletion,
			},
			expectedModified: true,
		},
		{
			name: "implicit completion mode",
			existing: batchv1.JobSpec{
				CompletionMode: &NonIndexedCompletion,
			},
			required:         batchv1.JobSpec{},
			expectedModified: false,
		},
		{
			name: "same suspend",
			existing: batchv1.JobSpec{
				Suspend: pointer.Bool(true),
			},
			required: batchv1.JobSpec{
				Suspend: pointer.Bool(true),
			},
			expectedModified: false,
		},
		{
			name: "different suspend",
			existing: batchv1.JobSpec{
				Suspend: pointer.Bool(true),
			},
			required: batchv1.JobSpec{
				Suspend: pointer.Bool(false),
			},
			expectedModified: true,
		},
		{
			name: "implicit suspend",
			existing: batchv1.JobSpec{
				Suspend: pointer.Bool(false),
			},
			required:         batchv1.JobSpec{},
			expectedModified: false,
		},
	}
	for _, test := range tests {
		var expected batchv1.Job
		t.Run(test.name, func(t *testing.T) {
			existing := batchv1.Job{Spec: test.existing}
			required := batchv1.Job{Spec: test.required}
			if test.expectedModified == false {
				existing.DeepCopyInto(&expected)
			} else {
				required.DeepCopyInto(&expected)
			}
			defaultJob(&existing, existing)
			defaultJob(&expected, expected)
			modified := pointer.Bool(false)
			EnsureJob(modified, &existing, required)
			if *modified != test.expectedModified {
				t.Errorf("mismatch modified got: %v want: %v", *modified, test.expectedModified)
			}
			if !equality.Semantic.DeepEqual(existing, expected) {
				t.Errorf("unexpected: %s", cmp.Diff(existing, expected))
			}
		})
	}
}

func TestEnsureJob_JobSpec_Selector(t *testing.T) {
	labelSelector := metav1.LabelSelector{}
	tests := []struct {
		name     string
		existing batchv1.JobSpec
		required batchv1.JobSpec

		expectedPanic bool
	}{
		{
			name: "required-Selector not nil",
			existing: batchv1.JobSpec{
				Selector:       &labelSelector,
				ManualSelector: pointer.Bool(false),
			},
			required: batchv1.JobSpec{
				Selector:       &labelSelector,
				ManualSelector: pointer.Bool(true),
			},
			expectedPanic: true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			defer func() {
				switch r := recover(); r {
				case nil:
					if test.expectedPanic {
						t.Errorf(test.name + " should have panicked!")
					}
				default:
					if !test.expectedPanic {
						panic(r)
					}
				}
			}()
			existing := batchv1.Job{Spec: test.existing}
			required := batchv1.Job{Spec: test.required}
			defaultJob(&existing, existing)
			modified := pointer.Bool(false)
			EnsureJob(modified, &existing, required)
		})
	}
}

// Ensures the structure contains any defaults not explicitly set by the test
func defaultJob(in *batchv1.Job, from batchv1.Job) {
	modified := pointer.Bool(false)
	EnsureJob(modified, in, from)
}

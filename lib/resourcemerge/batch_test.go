package resourcemerge

import (
	"testing"

	batchv1 "k8s.io/api/batch/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/pointer"
)

func TestEnsureJob(t *testing.T) {
	labelSelector := metav1.LabelSelector{}
	tests := []struct {
		name     string
		existing batchv1.Job
		required batchv1.Job

		expectedModified bool
		expectedPanic    bool
		expected         batchv1.Job
	}{
		{
			name: "different backofflimit count",
			existing: batchv1.Job{
				Spec: batchv1.JobSpec{
					BackoffLimit: pointer.Int32(2)}},
			required: batchv1.Job{
				Spec: batchv1.JobSpec{
					BackoffLimit: pointer.Int32(3)}},

			expectedModified: true,
			expected: batchv1.Job{
				Spec: batchv1.JobSpec{
					BackoffLimit: pointer.Int32(3)}},
		},
		{
			name: "same backofflimit count",
			existing: batchv1.Job{
				Spec: batchv1.JobSpec{
					BackoffLimit: pointer.Int32(2)}},
			required: batchv1.Job{
				Spec: batchv1.JobSpec{
					BackoffLimit: pointer.Int32(2)}},

			expectedModified: false,
			expected: batchv1.Job{
				Spec: batchv1.JobSpec{
					BackoffLimit: pointer.Int32(2)}},
		},
		{
			name: "required-Selector not nil",
			existing: batchv1.Job{
				Spec: batchv1.JobSpec{
					Selector:       &labelSelector,
					ManualSelector: pointer.Bool(false)}},
			required: batchv1.Job{
				Spec: batchv1.JobSpec{
					Selector:       &labelSelector,
					ManualSelector: pointer.Bool(true)}},

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
			defaultJob(&test.existing, test.existing)
			defaultJob(&test.expected, test.expected)
			modified := pointer.Bool(false)
			EnsureJob(modified, &test.existing, test.required)
			if *modified != test.expectedModified {
				t.Errorf("mismatch modified got: %v want: %v", *modified, test.expectedModified)
			}

			if !equality.Semantic.DeepEqual(test.existing, test.expected) {
				t.Errorf("mismatch Job got: %v want: %v", test.existing, test.expected)
			}
		})
	}
}

// Ensures the structure contains any defaults not explicitly set by the test
func defaultJob(in *batchv1.Job, from batchv1.Job) {
	modified := pointer.Bool(false)
	EnsureJob(modified, in, from)
}

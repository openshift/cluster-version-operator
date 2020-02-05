package resourcemerge

import (
	"testing"

	batchv1 "k8s.io/api/batch/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	pointer "k8s.io/utils/pointer"
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
					BackoffLimit: pointer.Int32Ptr(2)}},
			required: batchv1.Job{
				Spec: batchv1.JobSpec{
					BackoffLimit: pointer.Int32Ptr(3)}},

			expectedModified: true,
			expected: batchv1.Job{
				Spec: batchv1.JobSpec{
					BackoffLimit: pointer.Int32Ptr(3)}},
		},
		{
			name: "same backofflimit count",
			existing: batchv1.Job{
				Spec: batchv1.JobSpec{
					BackoffLimit: pointer.Int32Ptr(2)}},
			required: batchv1.Job{
				Spec: batchv1.JobSpec{
					BackoffLimit: pointer.Int32Ptr(2)}},

			expectedModified: false,
			expected: batchv1.Job{
				Spec: batchv1.JobSpec{
					BackoffLimit: pointer.Int32Ptr(2)}},
		},
		{
			name: "required-Selector not nil",
			existing: batchv1.Job{
				Spec: batchv1.JobSpec{
					Selector:       &labelSelector,
					ManualSelector: pointer.BoolPtr(false)}},
			required: batchv1.Job{
				Spec: batchv1.JobSpec{
					Selector:       &labelSelector,
					ManualSelector: pointer.BoolPtr(true)}},

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
			modified := pointer.BoolPtr(false)
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

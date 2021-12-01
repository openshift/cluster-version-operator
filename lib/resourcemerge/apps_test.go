package resourcemerge

import (
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/pointer"
)

func TestEnsureDeployment(t *testing.T) {
	labelSelector := metav1.LabelSelector{}
	tests := []struct {
		name     string
		existing appsv1.Deployment
		required appsv1.Deployment

		expectedModified bool
		expected         appsv1.Deployment
	}{
		{
			name: "different replica count",
			existing: appsv1.Deployment{
				Spec: appsv1.DeploymentSpec{
					Replicas: pointer.Int32(2)}},
			required: appsv1.Deployment{
				Spec: appsv1.DeploymentSpec{
					Replicas: pointer.Int32(3)}},

			expectedModified: true,
			expected: appsv1.Deployment{
				Spec: appsv1.DeploymentSpec{
					Replicas: pointer.Int32(3)}},
		},
		{
			name: "same replica count",
			existing: appsv1.Deployment{
				Spec: appsv1.DeploymentSpec{
					Replicas: pointer.Int32(2)}},
			required: appsv1.Deployment{
				Spec: appsv1.DeploymentSpec{
					Replicas: pointer.Int32(2)}},

			expectedModified: false,
			expected: appsv1.Deployment{
				Spec: appsv1.DeploymentSpec{
					Replicas: pointer.Int32(2)}},
		},
		{
			name: "implicit replica count",
			existing: appsv1.Deployment{
				Spec: appsv1.DeploymentSpec{
					Replicas: pointer.Int32(2)}},
			expectedModified: true,
			expected: appsv1.Deployment{
				Spec: appsv1.DeploymentSpec{
					Replicas: pointer.Int32(1)}},
		},
		{
			name:     "existing-selector-nil-required-selector-non-nil",
			existing: appsv1.Deployment{},
			required: appsv1.Deployment{
				Spec: appsv1.DeploymentSpec{
					Selector: &labelSelector}},

			expectedModified: true,
			expected: appsv1.Deployment{
				Spec: appsv1.DeploymentSpec{
					Selector: &labelSelector}},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			defaultDeployment(&test.existing, test.existing)
			defaultDeployment(&test.expected, test.expected)
			modified := pointer.BoolPtr(false)
			EnsureDeployment(modified, &test.existing, test.required)
			if *modified != test.expectedModified {
				t.Errorf("mismatch modified got: %v want: %v", *modified, test.expectedModified)
			}

			if !equality.Semantic.DeepEqual(test.existing, test.expected) {
				t.Errorf("mismatch Deployment got: %v want: %v", test.existing, test.expected)
			}
		})
	}
}

// Ensures the structure contains any defaults not explicitly set by the test
func defaultDeployment(in *appsv1.Deployment, from appsv1.Deployment) {
	modified := pointer.BoolPtr(false)
	EnsureDeployment(modified, in, from)
}

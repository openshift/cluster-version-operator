package resourcemerge

import (
	"testing"

	operatorsv1 "github.com/operator-framework/api/pkg/operators/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
)

func TestEnsureOperatorGroup(t *testing.T) {
	labelSelector := metav1.LabelSelector{}
	tests := []struct {
		name     string
		existing operatorsv1.OperatorGroup
		required operatorsv1.OperatorGroup

		expectedModified bool
		expected         operatorsv1.OperatorGroup
	}{
		{
			name:     "existing-selector-nil-required-selector-non-nil",
			existing: operatorsv1.OperatorGroup{},
			required: operatorsv1.OperatorGroup{
				Spec: operatorsv1.OperatorGroupSpec{
					Selector: &labelSelector}},

			expectedModified: true,
			expected: operatorsv1.OperatorGroup{
				Spec: operatorsv1.OperatorGroupSpec{
					Selector: &labelSelector}},
		},
		{
			name: "same target namespaces",
			existing: operatorsv1.OperatorGroup{
				Spec: operatorsv1.OperatorGroupSpec{
					TargetNamespaces: []string{"foo", "bar"},
				},
			},
			required: operatorsv1.OperatorGroup{
				Spec: operatorsv1.OperatorGroupSpec{
					TargetNamespaces: []string{"foo", "bar"},
				},
			},

			expectedModified: false,
			expected: operatorsv1.OperatorGroup{
				Spec: operatorsv1.OperatorGroupSpec{
					TargetNamespaces: []string{"foo", "bar"},
				},
			},
		},
		{
			name: "different target namespaces",
			existing: operatorsv1.OperatorGroup{
				Spec: operatorsv1.OperatorGroupSpec{
					TargetNamespaces: []string{},
				},
			},
			required: operatorsv1.OperatorGroup{
				Spec: operatorsv1.OperatorGroupSpec{
					TargetNamespaces: []string{"foo", "bar"},
				},
			},

			expectedModified: true,
			expected: operatorsv1.OperatorGroup{
				Spec: operatorsv1.OperatorGroupSpec{
					TargetNamespaces: []string{"foo", "bar"},
				},
			},
		},
		{
			name: "same service account names",
			existing: operatorsv1.OperatorGroup{
				Spec: operatorsv1.OperatorGroupSpec{
					ServiceAccountName: "foo",
				},
			},
			required: operatorsv1.OperatorGroup{
				Spec: operatorsv1.OperatorGroupSpec{
					ServiceAccountName: "foo",
				},
			},

			expectedModified: false,
			expected: operatorsv1.OperatorGroup{
				Spec: operatorsv1.OperatorGroupSpec{
					ServiceAccountName: "foo",
				},
			},
		},
		{
			name: "different service account names",
			existing: operatorsv1.OperatorGroup{
				Spec: operatorsv1.OperatorGroupSpec{
					ServiceAccountName: "foo",
				},
			},
			required: operatorsv1.OperatorGroup{
				Spec: operatorsv1.OperatorGroupSpec{
					ServiceAccountName: "bar",
				},
			},

			expectedModified: true,
			expected: operatorsv1.OperatorGroup{
				Spec: operatorsv1.OperatorGroupSpec{
					ServiceAccountName: "bar",
				},
			},
		},
		{
			name: "same static provided apis",
			existing: operatorsv1.OperatorGroup{
				Spec: operatorsv1.OperatorGroupSpec{
					StaticProvidedAPIs: true,
				},
			},
			required: operatorsv1.OperatorGroup{
				Spec: operatorsv1.OperatorGroupSpec{
					StaticProvidedAPIs: true,
				},
			},

			expectedModified: false,
			expected: operatorsv1.OperatorGroup{
				Spec: operatorsv1.OperatorGroupSpec{
					StaticProvidedAPIs: true,
				},
			},
		},
		{
			name: "different static provided apis",
			existing: operatorsv1.OperatorGroup{
				Spec: operatorsv1.OperatorGroupSpec{
					StaticProvidedAPIs: true,
				},
			},
			required: operatorsv1.OperatorGroup{
				Spec: operatorsv1.OperatorGroupSpec{
					StaticProvidedAPIs: false,
				},
			},

			expectedModified: true,
			expected: operatorsv1.OperatorGroup{
				Spec: operatorsv1.OperatorGroupSpec{
					StaticProvidedAPIs: false,
				},
			},
		},
		{
			name: "implicit upgrade strategy",
			existing: operatorsv1.OperatorGroup{
				Spec: operatorsv1.OperatorGroupSpec{
					UpgradeStrategy: operatorsv1.UpgradeStrategyUnsafeFailForward,
				},
			},
			required: operatorsv1.OperatorGroup{
				Spec: operatorsv1.OperatorGroupSpec{},
			},

			expectedModified: true,
			expected: operatorsv1.OperatorGroup{
				Spec: operatorsv1.OperatorGroupSpec{
					UpgradeStrategy: operatorsv1.UpgradeStrategyDefault,
				},
			},
		},
		{
			name: "same upgrade strategy",
			existing: operatorsv1.OperatorGroup{
				Spec: operatorsv1.OperatorGroupSpec{
					UpgradeStrategy: operatorsv1.UpgradeStrategyUnsafeFailForward,
				},
			},
			required: operatorsv1.OperatorGroup{
				Spec: operatorsv1.OperatorGroupSpec{
					UpgradeStrategy: operatorsv1.UpgradeStrategyUnsafeFailForward,
				},
			},

			expectedModified: false,
			expected: operatorsv1.OperatorGroup{
				Spec: operatorsv1.OperatorGroupSpec{
					UpgradeStrategy: operatorsv1.UpgradeStrategyUnsafeFailForward,
				},
			},
		},
		{
			name: "different upgrade strategy",
			existing: operatorsv1.OperatorGroup{
				Spec: operatorsv1.OperatorGroupSpec{
					UpgradeStrategy: operatorsv1.UpgradeStrategyUnsafeFailForward,
				},
			},
			required: operatorsv1.OperatorGroup{
				Spec: operatorsv1.OperatorGroupSpec{
					UpgradeStrategy: operatorsv1.UpgradeStrategyDefault,
				},
			},

			expectedModified: true,
			expected: operatorsv1.OperatorGroup{
				Spec: operatorsv1.OperatorGroupSpec{
					UpgradeStrategy: operatorsv1.UpgradeStrategyDefault,
				},
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			defaultOperatorGroup(&test.existing, test.existing)
			defaultOperatorGroup(&test.expected, test.expected)
			modified := ptr.To(false)
			EnsureOperatorGroup(modified, &test.existing, test.required)
			if *modified != test.expectedModified {
				t.Errorf("mismatch modified got: %v want: %v", *modified, test.expectedModified)
			}

			if !equality.Semantic.DeepEqual(test.existing, test.expected) {
				t.Errorf("mismatch OperatorGroup got: %v want: %v", test.existing, test.expected)
			}
		})
	}
}

// Ensures the structure contains any defaults not explicitly set by the test
func defaultOperatorGroup(in *operatorsv1.OperatorGroup, from operatorsv1.OperatorGroup) {
	modified := ptr.To(false)
	EnsureOperatorGroup(modified, in, from)
}

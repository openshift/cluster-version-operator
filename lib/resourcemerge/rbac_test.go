package resourcemerge

import (
	"testing"

	"github.com/google/go-cmp/cmp"

	rbacv1 "k8s.io/api/rbac/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	"k8s.io/utils/ptr"
)

func TestEnsureClusterRole2Bindingsv1(t *testing.T) {
	tests := []struct {
		name     string
		existing rbacv1.ClusterRoleBinding
		input    rbacv1.ClusterRoleBinding

		expectedModified bool
		expected         rbacv1.ClusterRoleBinding
	}{
		{
			name: "add subject",
			existing: rbacv1.ClusterRoleBinding{
				Subjects: []rbacv1.Subject{},
				RoleRef: rbacv1.RoleRef{
					APIGroup: "rbac.authorization.k8s.io",
					Kind:     "ClusterRole",
					Name:     "role-baz",
				},
			},
			input: rbacv1.ClusterRoleBinding{
				Subjects: []rbacv1.Subject{
					{
						Name:      "foo",
						Namespace: "bar",
					},
				},
				RoleRef: rbacv1.RoleRef{
					APIGroup: "rbac.authorization.k8s.io",
					Kind:     "ClusterRole",
					Name:     "role-baz",
				},
			},
			expectedModified: true,
			expected: rbacv1.ClusterRoleBinding{
				Subjects: []rbacv1.Subject{
					{
						Name:      "foo",
						Namespace: "bar",
					},
				},
				RoleRef: rbacv1.RoleRef{
					APIGroup: "rbac.authorization.k8s.io",
					Kind:     "ClusterRole",
					Name:     "role-baz",
				},
			},
		},
		{
			name: "remove subject",
			existing: rbacv1.ClusterRoleBinding{
				Subjects: []rbacv1.Subject{
					{
						Name:      "foo",
						Namespace: "bar",
					},
				},
				RoleRef: rbacv1.RoleRef{
					APIGroup: "rbac.authorization.k8s.io",
					Kind:     "ClusterRole",
					Name:     "role-baz",
				},
			},
			input: rbacv1.ClusterRoleBinding{
				RoleRef: rbacv1.RoleRef{
					APIGroup: "rbac.authorization.k8s.io",
					Kind:     "ClusterRole",
					Name:     "role-baz",
				},
			},
			expectedModified: true,
			expected: rbacv1.ClusterRoleBinding{
				RoleRef: rbacv1.RoleRef{
					APIGroup: "rbac.authorization.k8s.io",
					Kind:     "ClusterRole",
					Name:     "role-baz",
				},
			},
		},
		{
			name: "replace subject",
			existing: rbacv1.ClusterRoleBinding{
				Subjects: []rbacv1.Subject{
					{
						Name:      "foo",
						Namespace: "bar",
					},
				},
				RoleRef: rbacv1.RoleRef{
					APIGroup: "rbac.authorization.k8s.io",
					Kind:     "ClusterRole",
					Name:     "role-baz",
				},
			},
			input: rbacv1.ClusterRoleBinding{
				Subjects: []rbacv1.Subject{
					{
						Name:      "cake",
						Namespace: "pie",
					},
				},
				RoleRef: rbacv1.RoleRef{
					APIGroup: "rbac.authorization.k8s.io",
					Kind:     "ClusterRole",
					Name:     "role-baz",
				},
			},
			expectedModified: true,
			expected: rbacv1.ClusterRoleBinding{
				Subjects: []rbacv1.Subject{
					{
						Name:      "cake",
						Namespace: "pie",
					},
				},
				RoleRef: rbacv1.RoleRef{
					APIGroup: "rbac.authorization.k8s.io",
					Kind:     "ClusterRole",
					Name:     "role-baz",
				},
			},
		},
		{
			name: "same subject",
			existing: rbacv1.ClusterRoleBinding{
				Subjects: []rbacv1.Subject{
					{
						Name:      "foo",
						Namespace: "bar",
					},
				},
				RoleRef: rbacv1.RoleRef{
					APIGroup: "rbac.authorization.k8s.io",
					Kind:     "ClusterRole",
					Name:     "role-baz",
				},
			},
			input: rbacv1.ClusterRoleBinding{
				Subjects: []rbacv1.Subject{
					{
						Name:      "foo",
						Namespace: "bar",
					},
				},
				RoleRef: rbacv1.RoleRef{
					APIGroup: "rbac.authorization.k8s.io",
					Kind:     "ClusterRole",
					Name:     "role-baz",
				},
			},
			expectedModified: false,
			expected: rbacv1.ClusterRoleBinding{
				Subjects: []rbacv1.Subject{
					{
						Name:      "foo",
						Namespace: "bar",
					},
				},
				RoleRef: rbacv1.RoleRef{
					APIGroup: "rbac.authorization.k8s.io",
					Kind:     "ClusterRole",
					Name:     "role-baz",
				},
			},
		},
		{
			name:     "add roleref",
			existing: rbacv1.ClusterRoleBinding{},
			input: rbacv1.ClusterRoleBinding{
				RoleRef: rbacv1.RoleRef{
					APIGroup: "rbac.authorization.k8s.io",
					Kind:     "ClusterRole",
					Name:     "role-baz",
				},
			},
			expectedModified: true,
			expected: rbacv1.ClusterRoleBinding{
				RoleRef: rbacv1.RoleRef{
					APIGroup: "rbac.authorization.k8s.io",
					Kind:     "ClusterRole",
					Name:     "role-baz",
				},
			},
		},
		{
			name:     "add roleref (empty apigroup)",
			existing: rbacv1.ClusterRoleBinding{},
			input: rbacv1.ClusterRoleBinding{
				RoleRef: rbacv1.RoleRef{
					Kind: "ClusterRole",
					Name: "role-baz",
				},
			},
			expectedModified: true,
			expected: rbacv1.ClusterRoleBinding{
				RoleRef: rbacv1.RoleRef{
					APIGroup: "rbac.authorization.k8s.io",
					Kind:     "ClusterRole",
					Name:     "role-baz",
				},
			},
		},
		{
			name: "replace roleref",
			existing: rbacv1.ClusterRoleBinding{
				RoleRef: rbacv1.RoleRef{
					APIGroup: "rbac.authorization.k8s.io",
					Kind:     "ClusterRole",
					Name:     "role-baz",
				},
			},
			input: rbacv1.ClusterRoleBinding{
				RoleRef: rbacv1.RoleRef{
					APIGroup: "rbac.authorization.k8s.io",
					Kind:     "ClusterRole",
					Name:     "role-cake",
				},
			},
			expectedModified: true,
			expected: rbacv1.ClusterRoleBinding{
				RoleRef: rbacv1.RoleRef{
					APIGroup: "rbac.authorization.k8s.io",
					Kind:     "ClusterRole",
					Name:     "role-cake",
				},
			},
		},
		{
			name: "same roleref",
			existing: rbacv1.ClusterRoleBinding{
				RoleRef: rbacv1.RoleRef{
					APIGroup: "rbac.authorization.k8s.io",
					Kind:     "ClusterRole",
					Name:     "role-baz",
				},
			},
			input: rbacv1.ClusterRoleBinding{
				RoleRef: rbacv1.RoleRef{
					APIGroup: "rbac.authorization.k8s.io",
					Kind:     "ClusterRole",
					Name:     "role-baz",
				},
			},
			expectedModified: false,
			expected: rbacv1.ClusterRoleBinding{
				RoleRef: rbacv1.RoleRef{
					APIGroup: "rbac.authorization.k8s.io",
					Kind:     "ClusterRole",
					Name:     "role-baz",
				},
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			modified := ptr.To(false)
			EnsureClusterRoleBinding(modified, &test.existing, test.input)
			if *modified != test.expectedModified {
				t.Errorf("mismatch modified got: %v want: %v", *modified, test.expectedModified)
			}

			if !equality.Semantic.DeepEqual(test.existing, test.expected) {
				t.Errorf("unexpected: %s", cmp.Diff(test.expected, test.existing))
			}
		})
	}
}

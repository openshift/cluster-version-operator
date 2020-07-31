package resourcemerge

import (
	rbacv1beta1 "k8s.io/api/rbac/v1beta1"
	"k8s.io/apimachinery/pkg/api/equality"
)

// EnsureClusterRoleBindingv1beta1 ensures that the existing matches the required.
// modified is set to true when existing had to be updated with required.
func EnsureClusterRoleBindingv1beta1(modified *bool, existing *rbacv1beta1.ClusterRoleBinding, required rbacv1beta1.ClusterRoleBinding) {
	EnsureObjectMeta(modified, &existing.ObjectMeta, required.ObjectMeta)
	if !equality.Semantic.DeepEqual(existing.Subjects, required.Subjects) {
		*modified = true
		existing.Subjects = required.Subjects
	}
	if !equality.Semantic.DeepEqual(existing.RoleRef, required.RoleRef) {
		*modified = true
		existing.RoleRef = required.RoleRef
	}
}

// EnsureClusterRolev1beta1 ensures that the existing matches the required.
// modified is set to true when existing had to be updated with required.
func EnsureClusterRolev1beta1(modified *bool, existing *rbacv1beta1.ClusterRole, required rbacv1beta1.ClusterRole) {
	EnsureObjectMeta(modified, &existing.ObjectMeta, required.ObjectMeta)
	if !equality.Semantic.DeepEqual(existing.Rules, required.Rules) {
		*modified = true
		existing.Rules = required.Rules
	}
}

// EnsureRoleBindingv1beta1 ensures that the existing matches the required.
// modified is set to true when existing had to be updated with required.
func EnsureRoleBindingv1beta1(modified *bool, existing *rbacv1beta1.RoleBinding, required rbacv1beta1.RoleBinding) {
	EnsureObjectMeta(modified, &existing.ObjectMeta, required.ObjectMeta)
	if !equality.Semantic.DeepEqual(existing.Subjects, required.Subjects) {
		*modified = true
		existing.Subjects = required.Subjects
	}
	if !equality.Semantic.DeepEqual(existing.RoleRef, required.RoleRef) {
		*modified = true
		existing.RoleRef = required.RoleRef
	}
}

// EnsureRolev1beta1 ensures that the existing matches the required.
// modified is set to true when existing had to be updated with required.
func EnsureRolev1beta1(modified *bool, existing *rbacv1beta1.Role, required rbacv1beta1.Role) {
	EnsureObjectMeta(modified, &existing.ObjectMeta, required.ObjectMeta)
	if !equality.Semantic.DeepEqual(existing.Rules, required.Rules) {
		*modified = true
		existing.Rules = required.Rules
	}
}

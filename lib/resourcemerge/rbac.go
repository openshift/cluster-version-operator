package resourcemerge

import (
	rbacv1 "k8s.io/api/rbac/v1"
	"k8s.io/apimachinery/pkg/api/equality"
)

// EnsureClusterRoleBinding ensures that the existing matches the required.
// Returns true when existing had to be updated with required.
func EnsureClusterRoleBinding(existing *rbacv1.ClusterRoleBinding, required rbacv1.ClusterRoleBinding) bool {
	var modified bool
	EnsureObjectMeta(&modified, &existing.ObjectMeta, required.ObjectMeta)
	ensureRoleRefDefaultsv1(&required.RoleRef)
	if !equality.Semantic.DeepEqual(existing.Subjects, required.Subjects) {
		modified = true
		existing.Subjects = required.Subjects
	}
	if !equality.Semantic.DeepEqual(existing.RoleRef, required.RoleRef) {
		modified = true
		existing.RoleRef = required.RoleRef
	}

	return modified
}

// EnsureClusterRole ensures that the existing matches the required.
// Returns true when existing had to be updated with required.
func EnsureClusterRole(existing *rbacv1.ClusterRole, required rbacv1.ClusterRole) bool {
	var modified bool
	EnsureObjectMeta(&modified, &existing.ObjectMeta, required.ObjectMeta)
	if !equality.Semantic.DeepEqual(existing.AggregationRule, required.AggregationRule) {
		modified = true
		existing.AggregationRule = required.AggregationRule
	}
	if required.AggregationRule != nil {
		// The control plane overwrites any values that are manually specified in the rules field of an aggregate ClusterRole.
		// Skip reconciling on Rules field
		return modified
	}
	if !equality.Semantic.DeepEqual(existing.Rules, required.Rules) {
		modified = true
		existing.Rules = required.Rules
	}
	return modified
}

// EnsureRoleBinding ensures that the existing matches the required.
// Returns true when existing had to be updated with required.
func EnsureRoleBinding(existing *rbacv1.RoleBinding, required rbacv1.RoleBinding) bool {
	var modified bool
	EnsureObjectMeta(&modified, &existing.ObjectMeta, required.ObjectMeta)
	ensureRoleRefDefaultsv1(&required.RoleRef)
	if !equality.Semantic.DeepEqual(existing.Subjects, required.Subjects) {
		modified = true
		existing.Subjects = required.Subjects
	}
	if !equality.Semantic.DeepEqual(existing.RoleRef, required.RoleRef) {
		modified = true
		existing.RoleRef = required.RoleRef
	}

	return modified
}

func ensureRoleRefDefaultsv1(roleRef *rbacv1.RoleRef) {
	if roleRef.APIGroup == "" {
		roleRef.APIGroup = rbacv1.GroupName
	}
}

// EnsureRole ensures that the existing matches the required.
// Returns true when existing had to be updated with required.
func EnsureRole(existing *rbacv1.Role, required rbacv1.Role) bool {
	var modified bool
	EnsureObjectMeta(&modified, &existing.ObjectMeta, required.ObjectMeta)
	if !equality.Semantic.DeepEqual(existing.Rules, required.Rules) {
		modified = true
		existing.Rules = required.Rules
	}

	return modified
}

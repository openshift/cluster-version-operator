package resourceapply

import (
	"context"

	"github.com/openshift/cluster-version-operator/lib"
	"github.com/openshift/cluster-version-operator/lib/resourcemerge"
	rbacv1beta1 "k8s.io/api/rbac/v1beta1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	rbacclientv1beta1 "k8s.io/client-go/kubernetes/typed/rbac/v1beta1"
	"k8s.io/utils/pointer"
)

// ApplyClusterRoleBindingv1beta1 applies the required clusterrolebinding to the cluster.
func ApplyClusterRoleBindingv1beta1(ctx context.Context, client rbacclientv1beta1.ClusterRoleBindingsGetter, required *rbacv1beta1.ClusterRoleBinding) (*rbacv1beta1.ClusterRoleBinding, bool, error) {
	existing, err := client.ClusterRoleBindings().Get(ctx, required.Name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		actual, err := client.ClusterRoleBindings().Create(ctx, required, lib.Metav1CreateOptions())
		return actual, true, err
	}
	if err != nil {
		return nil, false, err
	}
	// if we only create this resource, we have no need to continue further
	if IsCreateOnly(required) {
		return nil, false, nil
	}

	modified := pointer.BoolPtr(false)
	resourcemerge.EnsureClusterRoleBindingv1beta1(modified, existing, *required)
	if !*modified {
		return existing, false, nil
	}

	actual, err := client.ClusterRoleBindings().Update(ctx, existing, lib.Metav1UpdateOptions())
	return actual, true, err
}

// ApplyClusterRolev1beta1 applies the required clusterrole to the cluster.
func ApplyClusterRolev1beta1(ctx context.Context, client rbacclientv1beta1.ClusterRolesGetter, required *rbacv1beta1.ClusterRole) (*rbacv1beta1.ClusterRole, bool, error) {
	existing, err := client.ClusterRoles().Get(ctx, required.Name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		actual, err := client.ClusterRoles().Create(ctx, required, lib.Metav1CreateOptions())
		return actual, true, err
	}
	if err != nil {
		return nil, false, err
	}
	// if we only create this resource, we have no need to continue further
	if IsCreateOnly(required) {
		return nil, false, nil
	}

	modified := pointer.BoolPtr(false)
	resourcemerge.EnsureClusterRolev1beta1(modified, existing, *required)
	if !*modified {
		return existing, false, nil
	}

	actual, err := client.ClusterRoles().Update(ctx, existing, lib.Metav1UpdateOptions())
	return actual, true, err
}

// ApplyRoleBindingv1beta1 applies the required clusterrolebinding to the cluster.
func ApplyRoleBindingv1beta1(ctx context.Context, client rbacclientv1beta1.RoleBindingsGetter, required *rbacv1beta1.RoleBinding) (*rbacv1beta1.RoleBinding, bool, error) {
	existing, err := client.RoleBindings(required.Namespace).Get(ctx, required.Name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		actual, err := client.RoleBindings(required.Namespace).Create(ctx, required, lib.Metav1CreateOptions())
		return actual, true, err
	}
	if err != nil {
		return nil, false, err
	}
	// if we only create this resource, we have no need to continue further
	if IsCreateOnly(required) {
		return nil, false, nil
	}

	modified := pointer.BoolPtr(false)
	resourcemerge.EnsureRoleBindingv1beta1(modified, existing, *required)
	if !*modified {
		return existing, false, nil
	}

	actual, err := client.RoleBindings(required.Namespace).Update(ctx, existing, lib.Metav1UpdateOptions())
	return actual, true, err
}

// ApplyRolev1beta1 applies the required clusterrole to the cluster.
func ApplyRolev1beta1(ctx context.Context, client rbacclientv1beta1.RolesGetter, required *rbacv1beta1.Role) (*rbacv1beta1.Role, bool, error) {
	existing, err := client.Roles(required.Namespace).Get(ctx, required.Name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		actual, err := client.Roles(required.Namespace).Create(ctx, required, lib.Metav1CreateOptions())
		return actual, true, err
	}
	if err != nil {
		return nil, false, err
	}
	// if we only create this resource, we have no need to continue further
	if IsCreateOnly(required) {
		return nil, false, nil
	}

	modified := pointer.BoolPtr(false)
	resourcemerge.EnsureRolev1beta1(modified, existing, *required)
	if !*modified {
		return existing, false, nil
	}

	actual, err := client.Roles(required.Namespace).Update(ctx, existing, lib.Metav1UpdateOptions())
	return actual, true, err
}

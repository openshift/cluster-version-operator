package resourceapply

import (
	"context"

	"github.com/google/go-cmp/cmp"
	"github.com/openshift/cluster-version-operator/lib/resourcemerge"
	rbacv1 "k8s.io/api/rbac/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	rbacclientv1 "k8s.io/client-go/kubernetes/typed/rbac/v1"
	"k8s.io/klog/v2"
	"k8s.io/utils/pointer"
)

// ApplyClusterRoleBindingv1 applies the required clusterrolebinding to the cluster.
func ApplyClusterRoleBindingv1(ctx context.Context, client rbacclientv1.ClusterRoleBindingsGetter, required *rbacv1.ClusterRoleBinding, reconciling bool) (*rbacv1.ClusterRoleBinding, bool, error) {
	existing, err := client.ClusterRoleBindings().Get(ctx, required.Name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		klog.V(2).Infof("ClusterRoleBinding %s not found, creating", required.Name)
		actual, err := client.ClusterRoleBindings().Create(ctx, required, metav1.CreateOptions{})
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
	resourcemerge.EnsureClusterRoleBinding(modified, existing, *required)
	if !*modified {
		return existing, false, nil
	}
	if reconciling {
		klog.V(4).Infof("Updating ClusterRoleBinding %s due to diff: %v", required.Name, cmp.Diff(existing, required))
	}

	actual, err := client.ClusterRoleBindings().Update(ctx, existing, metav1.UpdateOptions{})
	return actual, true, err
}

// ApplyClusterRolev1 applies the required clusterrole to the cluster.
func ApplyClusterRolev1(ctx context.Context, client rbacclientv1.ClusterRolesGetter, required *rbacv1.ClusterRole, reconciling bool) (*rbacv1.ClusterRole, bool, error) {
	existing, err := client.ClusterRoles().Get(ctx, required.Name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		klog.V(2).Infof("ClusterRole %s not found, creating", required.Name)
		actual, err := client.ClusterRoles().Create(ctx, required, metav1.CreateOptions{})
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
	resourcemerge.EnsureClusterRole(modified, existing, *required)
	if !*modified {
		return existing, false, nil
	}
	if reconciling {
		klog.V(4).Infof("Updating ClusterRole %s due to diff: %v", required.Name, cmp.Diff(existing, required))
	}

	actual, err := client.ClusterRoles().Update(ctx, existing, metav1.UpdateOptions{})
	return actual, true, err
}

// ApplyRoleBindingv1 applies the required clusterrolebinding to the cluster.
func ApplyRoleBindingv1(ctx context.Context, client rbacclientv1.RoleBindingsGetter, required *rbacv1.RoleBinding, reconciling bool) (*rbacv1.RoleBinding, bool, error) {
	existing, err := client.RoleBindings(required.Namespace).Get(ctx, required.Name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		klog.V(2).Infof("RoleBinding %s/%s not found, creating", required.Namespace, required.Name)
		actual, err := client.RoleBindings(required.Namespace).Create(ctx, required, metav1.CreateOptions{})
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
	resourcemerge.EnsureRoleBinding(modified, existing, *required)
	if !*modified {
		return existing, false, nil
	}
	if reconciling {
		klog.V(4).Infof("Updating RoleBinding %s/%s due to diff: %v", required.Namespace, required.Name, cmp.Diff(existing, required))
	}

	actual, err := client.RoleBindings(required.Namespace).Update(ctx, existing, metav1.UpdateOptions{})
	return actual, true, err
}

// ApplyRolev1 applies the required clusterrole to the cluster.
func ApplyRolev1(ctx context.Context, client rbacclientv1.RolesGetter, required *rbacv1.Role, reconciling bool) (*rbacv1.Role, bool, error) {
	existing, err := client.Roles(required.Namespace).Get(ctx, required.Name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		klog.V(2).Infof("Role %s/%s not found, creating", required.Namespace, required.Name)
		actual, err := client.Roles(required.Namespace).Create(ctx, required, metav1.CreateOptions{})
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
	resourcemerge.EnsureRole(modified, existing, *required)
	if !*modified {
		return existing, false, nil
	}
	if reconciling {
		klog.V(4).Infof("Updating Role %s/%s due to diff: %v", required.Namespace, required.Name, cmp.Diff(existing, required))
	}

	actual, err := client.Roles(required.Namespace).Update(ctx, existing, metav1.UpdateOptions{})
	return actual, true, err
}

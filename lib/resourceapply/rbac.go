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

	var original rbacv1.ClusterRoleBinding
	existing.DeepCopyInto(&original)

	modified := resourcemerge.EnsureClusterRoleBinding(existing, *required)
	if !modified {
		return existing, false, nil
	}

	if reconciling {
		if diff := cmp.Diff(&original, existing); diff != "" {
			klog.V(2).Infof("Updating ClusterRoleBinding %s due to diff: %v", required.Name, diff)
		} else {
			klog.V(2).Infof("Updating ClusterRoleBinding %s with empty diff: possible hotloop after wrong comparison", required.Name)
		}
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

	var original rbacv1.ClusterRole
	existing.DeepCopyInto(&original)

	modified := resourcemerge.EnsureClusterRole(existing, *required)
	if !modified {
		return existing, false, nil
	}

	if reconciling {
		if diff := cmp.Diff(&original, existing); diff != "" {
			klog.V(2).Infof("Updating ClusterRole %s due to diff: %v", required.Name, diff)
		} else {
			klog.V(2).Infof("Updating ClusterRole %s with empty diff: possible hotloop after wrong comparison", required.Name)
		}
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

	var original rbacv1.RoleBinding
	existing.DeepCopyInto(&original)

	modified := resourcemerge.EnsureRoleBinding(existing, *required)
	if !modified {
		return existing, false, nil
	}

	if reconciling {
		if diff := cmp.Diff(&original, existing); diff != "" {
			klog.V(2).Infof("Updating RoleBinding %s due to diff: %v", required.Name, diff)
		} else {
			klog.V(2).Infof("Updating RoleBinding %s with empty diff: possible hotloop after wrong comparison", required.Name)
		}
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

	var original rbacv1.Role
	original.DeepCopyInto(&original)

	modified := resourcemerge.EnsureRole(existing, *required)
	if !modified {
		return existing, false, nil
	}

	if reconciling {
		if diff := cmp.Diff(&original, existing); diff != "" {
			klog.V(2).Infof("Updating Role %s due to diff: %v", required.Name, diff)
		} else {
			klog.V(2).Infof("Updating Role %s with empty diff: possible hotloop after wrong comparison", required.Name)
		}
	}

	actual, err := client.Roles(required.Namespace).Update(ctx, existing, metav1.UpdateOptions{})
	return actual, true, err
}

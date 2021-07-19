package resourcedelete

import (
	"context"
	"fmt"

	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	rbacclientv1 "k8s.io/client-go/kubernetes/typed/rbac/v1"
)

// DeleteClusterRoleBindingv1 checks the given resource for a valid delete annotation. If found
// it checks the status of a previousily issued delete request. If delete has not been
// requested and in UpdatingMode it will issue a delete request.
func DeleteClusterRoleBindingv1(ctx context.Context, client rbacclientv1.ClusterRoleBindingsGetter, required *rbacv1.ClusterRoleBinding,
	updateMode bool) (bool, error) {

	if delAnnoFound, err := ValidDeleteAnnotation(required.Annotations); !delAnnoFound || err != nil {
		return delAnnoFound, err
	}
	existing, err := client.ClusterRoleBindings().Get(ctx, required.Name, metav1.GetOptions{})
	resource := Resource{
		Kind:      "clusterrolebinding",
		Namespace: "",
		Name:      required.Name,
	}
	if deleteRequested, err := GetDeleteProgress(resource, err); err == nil {
		// Only request deletion when in update mode.
		if !deleteRequested && updateMode {
			if err := client.ClusterRoleBindings().Delete(ctx, required.Name, metav1.DeleteOptions{}); err != nil {
				return true, fmt.Errorf("Delete request for %s failed, err=%v", resource, err)
			}
			SetDeleteRequested(existing, resource)
		}
	} else {
		return true, fmt.Errorf("Error running delete for %s, err=%v", resource, err)
	}
	return true, nil
}

// DeleteClusterRolev1 checks the given resource for a valid delete annotation. If found
// it checks the status of a previousily issued delete request. If delete has not been
// requested and in UpdatingMode it will issue a delete request.
func DeleteClusterRolev1(ctx context.Context, client rbacclientv1.ClusterRolesGetter, required *rbacv1.ClusterRole,
	updateMode bool) (bool, error) {

	if delAnnoFound, err := ValidDeleteAnnotation(required.Annotations); !delAnnoFound || err != nil {
		return delAnnoFound, err
	}
	existing, err := client.ClusterRoles().Get(ctx, required.Name, metav1.GetOptions{})
	resource := Resource{
		Kind:      "clusterrole",
		Namespace: "",
		Name:      required.Name,
	}
	if deleteRequested, err := GetDeleteProgress(resource, err); err == nil {
		// Only request deletion when in update mode.
		if !deleteRequested && updateMode {
			if err := client.ClusterRoles().Delete(ctx, required.Name, metav1.DeleteOptions{}); err != nil {
				return true, fmt.Errorf("Delete request for %s failed, err=%v", resource, err)
			}
			SetDeleteRequested(existing, resource)
		}
	} else {
		return true, fmt.Errorf("Error running delete for %s, err=%v", resource, err)
	}
	return true, nil
}

// DeleteRoleBindingv1 checks the given resource for a valid delete annotation. If found
// it checks the status of a previousily issued delete request. If delete has not been
// requested and in UpdatingMode it will issue a delete request.
func DeleteRoleBindingv1(ctx context.Context, client rbacclientv1.RoleBindingsGetter, required *rbacv1.RoleBinding,
	updateMode bool) (bool, error) {

	if delAnnoFound, err := ValidDeleteAnnotation(required.Annotations); !delAnnoFound || err != nil {
		return delAnnoFound, err
	}
	existing, err := client.RoleBindings(required.Namespace).Get(ctx, required.Name, metav1.GetOptions{})
	resource := Resource{
		Kind:      "rolebinding",
		Namespace: required.Namespace,
		Name:      required.Name,
	}
	if deleteRequested, err := GetDeleteProgress(resource, err); err == nil {
		// Only request deletion when in update mode.
		if !deleteRequested && updateMode {
			if err := client.RoleBindings(required.Namespace).Delete(ctx, required.Name, metav1.DeleteOptions{}); err != nil {
				return true, fmt.Errorf("Delete request for %s failed, err=%v", resource, err)
			}
			SetDeleteRequested(existing, resource)
		}
	} else {
		return true, fmt.Errorf("Error running delete for %s, err=%v", resource, err)
	}
	return true, nil
}

// DeleteRolev1 checks the given resource for a valid delete annotation. If found
// it checks the status of a previousily issued delete request. If delete has not been
// requested and in UpdatingMode it will issue a delete request.
func DeleteRolev1(ctx context.Context, client rbacclientv1.RolesGetter, required *rbacv1.Role,
	updateMode bool) (bool, error) {

	if delAnnoFound, err := ValidDeleteAnnotation(required.Annotations); !delAnnoFound || err != nil {
		return delAnnoFound, err
	}
	existing, err := client.Roles(required.Namespace).Get(ctx, required.Name, metav1.GetOptions{})
	resource := Resource{
		Kind:      "role",
		Namespace: required.Namespace,
		Name:      required.Name,
	}
	if deleteRequested, err := GetDeleteProgress(resource, err); err == nil {
		// Only request deletion when in update mode.
		if !deleteRequested && updateMode {
			if err := client.Roles(required.Namespace).Delete(ctx, required.Name, metav1.DeleteOptions{}); err != nil {
				return true, fmt.Errorf("Delete request for %s failed, err=%v", resource, err)
			}
			SetDeleteRequested(existing, resource)
		}
	} else {
		return true, fmt.Errorf("Error running delete for %s, err=%v", resource, err)
	}
	return true, nil
}

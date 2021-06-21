package resourcedelete

import (
	"context"
	"fmt"

	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	rbacclientv1 "k8s.io/client-go/kubernetes/typed/rbac/v1"
)

// DeleteClusterRoleBindingv1 checks the given resource for a valid delete annotation. If found
// and in UpdatingMode it will issue a delete request or provide status of a previousily issued delete request.
// If not in UpdatingMode it simply returns an indication that the delete annotation was found. An error is
// returned if an invalid annotation is found or an error occurs during delete processing.
func DeleteClusterRoleBindingv1(ctx context.Context, client rbacclientv1.ClusterRoleBindingsGetter, required *rbacv1.ClusterRoleBinding,
	updateMode bool) (bool, error) {

	if delAnnoFound, err := ValidDeleteAnnotation(required.Annotations); !delAnnoFound || err != nil {
		return delAnnoFound, err
	} else if !updateMode {
		return true, nil
	}
	resource := Resource{
		Kind:      "clusterrolebinding",
		Namespace: "",
		Name:      required.Name,
	}
	existing, err := client.ClusterRoleBindings().Get(ctx, required.Name, metav1.GetOptions{})
	if deleteRequested, err := GetDeleteProgress(resource, err); err == nil {
		if !deleteRequested {
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
// and in UpdatingMode it will issue a delete request or provide status of a previousily issued delete request.
// If not in UpdatingMode it simply returns an indication that the delete annotation was found. An error is
// returned if an invalid annotation is found or an error occurs during delete processing.
func DeleteClusterRolev1(ctx context.Context, client rbacclientv1.ClusterRolesGetter, required *rbacv1.ClusterRole,
	updateMode bool) (bool, error) {

	if delAnnoFound, err := ValidDeleteAnnotation(required.Annotations); !delAnnoFound || err != nil {
		return delAnnoFound, err
	} else if !updateMode {
		return true, nil
	}
	resource := Resource{
		Kind:      "clusterrole",
		Namespace: "",
		Name:      required.Name,
	}
	existing, err := client.ClusterRoles().Get(ctx, required.Name, metav1.GetOptions{})
	if deleteRequested, err := GetDeleteProgress(resource, err); err == nil {
		if !deleteRequested {
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
// and in UpdatingMode it will issue a delete request or provide status of a previousily issued delete request.
// If not in UpdatingMode it simply returns an indication that the delete annotation was found. An error is
// returned if an invalid annotation is found or an error occurs during delete processing.
func DeleteRoleBindingv1(ctx context.Context, client rbacclientv1.RoleBindingsGetter, required *rbacv1.RoleBinding,
	updateMode bool) (bool, error) {

	if delAnnoFound, err := ValidDeleteAnnotation(required.Annotations); !delAnnoFound || err != nil {
		return delAnnoFound, err
	} else if !updateMode {
		return true, nil
	}
	resource := Resource{
		Kind:      "rolebinding",
		Namespace: required.Namespace,
		Name:      required.Name,
	}
	existing, err := client.RoleBindings(required.Namespace).Get(ctx, required.Name, metav1.GetOptions{})
	if deleteRequested, err := GetDeleteProgress(resource, err); err == nil {
		if !deleteRequested {
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
// and in UpdatingMode it will issue a delete request or provide status of a previousily issued delete request.
// If not in UpdatingMode it simply returns an indication that the delete annotation was found. An error is
// returned if an invalid annotation is found or an error occurs during delete processing.
func DeleteRolev1(ctx context.Context, client rbacclientv1.RolesGetter, required *rbacv1.Role,
	updateMode bool) (bool, error) {

	if delAnnoFound, err := ValidDeleteAnnotation(required.Annotations); !delAnnoFound || err != nil {
		return delAnnoFound, err
	} else if !updateMode {
		return true, nil
	}
	resource := Resource{
		Kind:      "role",
		Namespace: required.Namespace,
		Name:      required.Name,
	}
	existing, err := client.Roles(required.Namespace).Get(ctx, required.Name, metav1.GetOptions{})
	if deleteRequested, err := GetDeleteProgress(resource, err); err == nil {
		if !deleteRequested {
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

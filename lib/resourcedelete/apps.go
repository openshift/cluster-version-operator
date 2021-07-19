package resourcedelete

import (
	"context"
	"fmt"

	appsv1 "k8s.io/api/apps/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	appsclientv1 "k8s.io/client-go/kubernetes/typed/apps/v1"
)

// DeleteDeploymentv1 checks the given resource for a valid delete annotation. If found
// it checks the status of a previousily issued delete request. If delete has not been
// requested and in UpdatingMode it will issue a delete request.
func DeleteDeploymentv1(ctx context.Context, client appsclientv1.DeploymentsGetter, required *appsv1.Deployment,
	updateMode bool) (bool, error) {

	if delAnnoFound, err := ValidDeleteAnnotation(required.Annotations); !delAnnoFound || err != nil {
		return delAnnoFound, err
	}
	existing, err := client.Deployments(required.Namespace).Get(ctx, required.Name, metav1.GetOptions{})
	resource := Resource{
		Kind:      "deployment",
		Namespace: required.Namespace,
		Name:      required.Name,
	}
	if deleteRequested, err := GetDeleteProgress(resource, err); err == nil {
		// Only request deletion when in update mode.
		if !deleteRequested && updateMode {
			if err := client.Deployments(required.Namespace).Delete(ctx, required.Name, metav1.DeleteOptions{}); err != nil {
				return true, fmt.Errorf("Delete request for %s failed, err=%v", resource, err)
			}
			SetDeleteRequested(existing, resource)
		}
	} else {
		return true, fmt.Errorf("Error running delete for %s, err=%v", resource, err)
	}
	return true, nil
}

// DeleteDaemonSetv1 checks the given resource for a valid delete annotation. If found
// it checks the status of a previousily issued delete request. If delete has not been
// requested and in UpdatingMode it will issue a delete request.
func DeleteDaemonSetv1(ctx context.Context, client appsclientv1.DaemonSetsGetter, required *appsv1.DaemonSet,
	updateMode bool) (bool, error) {

	if delAnnoFound, err := ValidDeleteAnnotation(required.Annotations); !delAnnoFound || err != nil {
		return delAnnoFound, err
	}
	existing, err := client.DaemonSets(required.Namespace).Get(ctx, required.Name, metav1.GetOptions{})
	resource := Resource{
		Kind:      "daemonset",
		Namespace: required.Namespace,
		Name:      required.Name,
	}
	if deleteRequested, err := GetDeleteProgress(resource, err); err == nil {
		// Only request deletion when in update mode.
		if !deleteRequested && updateMode {
			if err := client.DaemonSets(required.Namespace).Delete(ctx, required.Name, metav1.DeleteOptions{}); err != nil {
				return true, fmt.Errorf("Delete request for %s failed, err=%v", resource, err)
			}
			SetDeleteRequested(existing, resource)
		}
	} else {
		return true, fmt.Errorf("Error running delete for %s, err=%v", resource, err)
	}
	return true, nil
}

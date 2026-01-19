package resourcedelete

import (
	"context"
	"fmt"

	operatorsv1 "github.com/operator-framework/api/pkg/operators/v1"
	operatorsclientv1 "github.com/operator-framework/operator-lifecycle-manager/pkg/api/client/clientset/versioned/typed/operators/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// DeleteOperatorGroupv1 checks the given resource for a valid delete annotation. If found
// it checks the status of a previously issued delete request. If delete has not been
// requested and in UpdatingMode it will issue a delete request.
func DeleteOperatorGroupv1(ctx context.Context, client operatorsclientv1.OperatorGroupsGetter, required *operatorsv1.OperatorGroup,
	updateMode bool) (bool, error) {

	if delAnnoFound, err := ValidDeleteAnnotation(required.Annotations); !delAnnoFound || err != nil {
		return delAnnoFound, err
	}
	existing, err := client.OperatorGroups(required.Namespace).Get(ctx, required.Name, metav1.GetOptions{})
	resource := Resource{
		Kind:      "operatorgroup",
		Namespace: required.Namespace,
		Name:      required.Name,
	}
	if deleteRequested, err := GetDeleteProgress(resource, err); err == nil {
		// Only request deletion when in update mode.
		if !deleteRequested && updateMode {
			if err := client.OperatorGroups(required.Namespace).Delete(ctx, required.Name, metav1.DeleteOptions{}); err != nil {
				return true, fmt.Errorf("delete request for %s failed, err=%v", resource, err)
			}
			SetDeleteRequested(existing, resource)
		}
	} else {
		return true, fmt.Errorf("error running delete for %s, err=%v", resource, err)
	}
	return true, nil
}

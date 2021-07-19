package resourcedelete

import (
	"context"
	"fmt"

	batchv1 "k8s.io/api/batch/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	batchclientv1 "k8s.io/client-go/kubernetes/typed/batch/v1"
)

// DeleteJobv1 checks the given resource for a valid delete annotation. If found
// it checks the status of a previousily issued delete request. If delete has not been
// requested and in UpdatingMode it will issue a delete request.
func DeleteJobv1(ctx context.Context, client batchclientv1.JobsGetter, required *batchv1.Job,
	updateMode bool) (bool, error) {

	if delAnnoFound, err := ValidDeleteAnnotation(required.Annotations); !delAnnoFound || err != nil {
		return delAnnoFound, err
	}
	existing, err := client.Jobs(required.Namespace).Get(ctx, required.Name, metav1.GetOptions{})
	resource := Resource{
		Kind:      "job",
		Namespace: required.Namespace,
		Name:      required.Name,
	}
	if deleteRequested, err := GetDeleteProgress(resource, err); err == nil {
		// Only request deletion when in update mode.
		if !deleteRequested && updateMode {
			if err := client.Jobs(required.Namespace).Delete(ctx, required.Name, metav1.DeleteOptions{}); err != nil {
				return true, fmt.Errorf("Delete request for %s failed, err=%v", resource, err)
			}
			SetDeleteRequested(existing, resource)
		}
	} else {
		return true, fmt.Errorf("Error running delete for %s, err=%v", resource, err)
	}
	return true, nil
}

package resourcedelete

import (
	"context"
	"fmt"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	apiregv1 "k8s.io/kube-aggregator/pkg/apis/apiregistration/v1"
	apiregclientv1 "k8s.io/kube-aggregator/pkg/client/clientset_generated/clientset/typed/apiregistration/v1"
)

// DeleteAPIServicev1 checks the given resource for a valid delete annotation. If found
// and in UpdatingMode it will issue a delete request or provide status of a previousily issued delete request.
// If not in UpdatingMode it simply returns an indication that the delete annotation was found. An error is
// returned if an invalid annotation is found or an error occurs during delete processing.
func DeleteAPIServicev1(ctx context.Context, client apiregclientv1.APIServicesGetter, required *apiregv1.APIService,
	updateMode bool) (bool, error) {

	if delAnnoFound, err := ValidDeleteAnnotation(required.Annotations); !delAnnoFound || err != nil {
		return delAnnoFound, err
	} else if !updateMode {
		return true, nil
	}
	resource := Resource{
		Kind:      "apiservice",
		Namespace: "",
		Name:      required.Name,
	}
	existing, err := client.APIServices().Get(ctx, required.Name, metav1.GetOptions{})
	if deleteRequested, err := GetDeleteProgress(resource, err); err == nil {
		if !deleteRequested {
			if err := client.APIServices().Delete(ctx, required.Name, metav1.DeleteOptions{}); err != nil {
				return true, fmt.Errorf("Delete request for %s failed, err=%v", resource, err)
			}
			SetDeleteRequested(existing, resource)
		}
	} else {
		return true, fmt.Errorf("Error running delete for %s, err=%v", resource, err)
	}
	return true, nil
}

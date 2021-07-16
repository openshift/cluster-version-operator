package resourcedelete

import (
	"context"
	"fmt"

	imagev1 "github.com/openshift/api/image/v1"
	imageclientv1 "github.com/openshift/client-go/image/clientset/versioned/typed/image/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// DeleteImageStreamv1 checks the given resource for a valid delete annotation. If found
// and in UpdatingMode it will issue a delete request or provide status of a previousily issued delete request.
// If not in UpdatingMode it simply returns an indication that the delete annotation was found. An error is
// returned if an invalid annotation is found or an error occurs during delete processing.
func DeleteImageStreamv1(ctx context.Context, client imageclientv1.ImageStreamsGetter, required *imagev1.ImageStream,
	updateMode bool) (bool, error) {

	if delAnnoFound, err := ValidDeleteAnnotation(required.Annotations); !delAnnoFound || err != nil {
		return delAnnoFound, err
	}
	existing, err := client.ImageStreams(required.Namespace).Get(ctx, required.Name, metav1.GetOptions{})
	resource := Resource{
		Kind:      "imagestream",
		Namespace: required.Namespace,
		Name:      required.Name,
	}
	if deleteRequested, err := GetDeleteProgress(resource, err); err == nil {
		// Only request deletion when in update mode.
		if !deleteRequested && updateMode {
			if err := client.ImageStreams(required.Namespace).Delete(ctx, required.Name, metav1.DeleteOptions{}); err != nil {
				return true, fmt.Errorf("Delete request for %s failed, err=%v", resource, err)
			}
			SetDeleteRequested(existing, resource)
		}
	} else {
		return true, fmt.Errorf("Error running delete for %s, err=%v", resource, err)
	}
	return true, nil
}

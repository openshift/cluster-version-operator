package resourcedelete

import (
	"context"
	"fmt"

	apiextv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	apiextclientv1 "k8s.io/apiextensions-apiserver/pkg/client/clientset/clientset/typed/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// DeleteCustomResourceDefinitionv1 checks the given resource for a valid delete annotation. If found
// and in UpdatingMode it will issue a delete request or provide status of a previousily issued delete request.
// If not in UpdatingMode it simply returns an indication that the delete annotation was found. An error is
// returned if an invalid annotation is found or an error occurs during delete processing.
func DeleteCustomResourceDefinitionv1(ctx context.Context, client apiextclientv1.CustomResourceDefinitionsGetter,
	required *apiextv1.CustomResourceDefinition, updateMode bool) (bool, error) {

	resource := Resource{
		Kind:      "customresourcedefinition",
		Namespace: "",
		Name:      required.Name,
	}

	if delAnnoFound, err := ValidDeleteAnnotation(required.Annotations); !delAnnoFound || err != nil {
		return delAnnoFound, err
	} else if !updateMode && !DeleteInProgress(resource) {
		return true, nil
	}
	existing, err := client.CustomResourceDefinitions().Get(ctx, required.Name, metav1.GetOptions{})
	if deleteRequested, err := GetDeleteProgress(resource, err); err == nil {
		// Only request deletion when in update mode.
		if !deleteRequested && updateMode {
			if err := client.CustomResourceDefinitions().Delete(ctx, required.Name, metav1.DeleteOptions{}); err != nil {
				return true, fmt.Errorf("Delete request for %s failed, err=%v", resource, err)
			}
			SetDeleteRequested(existing, resource)
		}
	} else {
		return true, fmt.Errorf("Error running delete for %s, err=%v", resource, err)
	}
	return true, nil
}

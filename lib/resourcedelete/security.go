package resourcedelete

import (
	"context"
	"fmt"

	securityv1 "github.com/openshift/api/security/v1"
	securityclientv1 "github.com/openshift/client-go/security/clientset/versioned/typed/security/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// DeleteSecurityContextConstraintsv1 checks the given resource for a valid delete annotation. If found
// and in UpdatingMode it will issue a delete request or provide status of a previousily issued delete request.
// If not in UpdatingMode it simply returns an indication that the delete annotation was found. An error is
// returned if an invalid annotation is found or an error occurs during delete processing.
func DeleteSecurityContextConstraintsv1(ctx context.Context, client securityclientv1.SecurityContextConstraintsGetter,
	required *securityv1.SecurityContextConstraints, updateMode bool) (bool, error) {

	if delAnnoFound, err := ValidDeleteAnnotation(required.Annotations); !delAnnoFound || err != nil {
		return delAnnoFound, err
	} else if !updateMode {
		return true, nil
	}
	resource := Resource{
		Kind:      "securitycontextconstraints",
		Namespace: "",
		Name:      required.Name,
	}
	existing, err := client.SecurityContextConstraints().Get(ctx, required.Name, metav1.GetOptions{})
	if deleteRequested, err := GetDeleteProgress(resource, err); err == nil {
		if !deleteRequested {
			if err := client.SecurityContextConstraints().Delete(ctx, required.Name, metav1.DeleteOptions{}); err != nil {
				return true, fmt.Errorf("Delete request for %s failed, err=%v", resource, err)
			}
			SetDeleteRequested(existing, resource)
		}
	} else {
		return true, fmt.Errorf("Error running delete for %s, err=%v", resource, err)
	}
	return true, nil
}

package resourcedelete

import (
	"context"
	"fmt"

	securityv1 "github.com/openshift/api/security/v1"
	securityclientv1 "github.com/openshift/client-go/security/clientset/versioned/typed/security/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// DeleteSecurityContextConstraintsv1 checks the given resource for a valid delete annotation. If found
// it checks the status of a previously issued delete request. If delete has not been
// requested and in UpdatingMode it will issue a delete request.
func DeleteSecurityContextConstraintsv1(ctx context.Context, client securityclientv1.SecurityContextConstraintsGetter,
	required *securityv1.SecurityContextConstraints, updateMode bool) (bool, error) {

	if delAnnoFound, err := ValidDeleteAnnotation(required.Annotations); !delAnnoFound || err != nil {
		return delAnnoFound, err
	}
	existing, err := client.SecurityContextConstraints().Get(ctx, required.Name, metav1.GetOptions{})
	resource := Resource{
		Kind:      "securitycontextconstraints",
		Namespace: "",
		Name:      required.Name,
	}
	if deleteRequested, err := GetDeleteProgress(resource, err); err == nil {
		// Only request deletion when in update mode.
		if !deleteRequested && updateMode {
			if err := client.SecurityContextConstraints().Delete(ctx, required.Name, metav1.DeleteOptions{}); err != nil {
				return true, fmt.Errorf("delete request for %s failed, err=%v", resource, err)
			}
			SetDeleteRequested(existing, resource)
		}
	} else {
		return true, fmt.Errorf("error running delete for %s, err=%v", resource, err)
	}
	return true, nil
}

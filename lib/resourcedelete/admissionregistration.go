package resourcedelete

import (
	"context"
	"fmt"

	admissionregv1 "k8s.io/api/admissionregistration/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	admissionregclientv1 "k8s.io/client-go/kubernetes/typed/admissionregistration/v1"
)

// DeleteValidatingWebhookConfigurationv1 checks the given resource for a valid delete
// annotation. If found it checks the status of a previously issued delete request.
// If delete has not been requested and in UpdatingMode it will issue a delete request.
func DeleteValidatingWebhookConfigurationv1(ctx context.Context,
	client admissionregclientv1.ValidatingWebhookConfigurationsGetter,
	required *admissionregv1.ValidatingWebhookConfiguration,
	updateMode bool) (bool, error) {

	if delAnnoFound, err := ValidDeleteAnnotation(required.Annotations); !delAnnoFound || err != nil {
		return delAnnoFound, err
	}
	existing, err := client.ValidatingWebhookConfigurations().Get(ctx, required.Name, metav1.GetOptions{})
	resource := Resource{
		Kind:      "validatingwebhookconfiguration",
		Namespace: required.Namespace,
		Name:      required.Name,
	}
	if deleteRequested, err := GetDeleteProgress(resource, err); err == nil {
		// Only request deletion when in update mode.
		if !deleteRequested && updateMode {
			if err := client.ValidatingWebhookConfigurations().Delete(ctx, required.Name, metav1.DeleteOptions{}); err != nil {
				return true, fmt.Errorf("delete request for %s failed, err=%v", resource, err)
			}
			SetDeleteRequested(existing, resource)
		}
	} else {
		return true, fmt.Errorf("error running delete for %s, err=%v", resource, err)
	}
	return true, nil
}

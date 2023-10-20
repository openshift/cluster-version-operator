package resourceapply

import (
	"context"

	"github.com/google/go-cmp/cmp"
	"github.com/openshift/cluster-version-operator/lib/resourcemerge"
	admissionregv1 "k8s.io/api/admissionregistration/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	admissionregclientv1 "k8s.io/client-go/kubernetes/typed/admissionregistration/v1"
	"k8s.io/klog/v2"
	"k8s.io/utils/ptr"
)

func ApplyValidatingWebhookConfigurationv1(ctx context.Context, client admissionregclientv1.ValidatingWebhookConfigurationsGetter, required *admissionregv1.ValidatingWebhookConfiguration, reconciling bool) (*admissionregv1.ValidatingWebhookConfiguration, bool, error) {
	existing, err := client.ValidatingWebhookConfigurations().Get(ctx, required.Name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		klog.V(2).Infof("ValidatingWebhookConfiguration %s/%s not found, creating", required.Namespace, required.Name)
		actual, err := client.ValidatingWebhookConfigurations().Create(ctx, required, metav1.CreateOptions{})
		return actual, true, err
	}
	if err != nil {
		return nil, false, err
	}
	// if we only create this resource, we have no need to continue further
	if IsCreateOnly(required) {
		return nil, false, nil
	}

	var original admissionregv1.ValidatingWebhookConfiguration
	existing.DeepCopyInto(&original)
	modified := ptr.To(false)
	resourcemerge.EnsureValidatingWebhookConfiguration(modified, existing, *required)
	if !*modified {
		return existing, false, nil
	}
	if reconciling {
		if diff := cmp.Diff(&original, existing); diff != "" {
			klog.V(2).Infof("Updating ValidatingWebhookConfiguration %s/%s due to diff: %v", required.Namespace, required.Name, diff)
		} else {
			klog.V(2).Infof("Updating ValidatingWebhookConfiguration %s/%s with empty diff: possible hotloop after wrong comparison", required.Namespace, required.Name)
		}
	}

	actual, err := client.ValidatingWebhookConfigurations().Update(ctx, existing, metav1.UpdateOptions{})
	return actual, true, err
}

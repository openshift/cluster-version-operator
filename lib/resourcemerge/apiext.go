package resourcemerge

import (
	"strings"

	apiextv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"

	"k8s.io/apimachinery/pkg/api/equality"
)

// EnsureCustomResourceDefinitionV1 ensures that the existing matches the required.
// modified is set to true when existing had to be updated with required.
func EnsureCustomResourceDefinitionV1(modified *bool, existing *apiextv1.CustomResourceDefinition, required apiextv1.CustomResourceDefinition) {
	EnsureObjectMeta(modified, &existing.ObjectMeta, required.ObjectMeta)
	ensureCustomResourceDefinitionV1Defaults(&required)
	ensureCustomResourceDefinitionV1CaBundle(&required, *existing)
	if !equality.Semantic.DeepEqual(existing.Spec, required.Spec) {
		*modified = true
		existing.Spec = required.Spec
	}
}

func ensureCustomResourceDefinitionV1Defaults(required *apiextv1.CustomResourceDefinition) {
	if len(required.Spec.Names.Singular) == 0 {
		required.Spec.Names.Singular = strings.ToLower(required.Spec.Names.Kind)
	}
	if len(required.Spec.Names.ListKind) == 0 {
		required.Spec.Names.ListKind = required.Spec.Names.Kind + "List"
	}
}

// ensureCustomResourceDefinitionV1CaBundle ensures that the field
// spec.Conversion.Webhook.ClientConfig.CABundle of a CRD is not managed by the CVO when
// the service-ca controller is responsible for the field.
func ensureCustomResourceDefinitionV1CaBundle(required *apiextv1.CustomResourceDefinition, existing apiextv1.CustomResourceDefinition) {
	if val, ok := existing.Annotations[injectCABundleAnnotation]; !ok || val != "true" {
		return
	}
	req := required.Spec.Conversion
	if req == nil ||
		req.Webhook == nil ||
		req.Webhook.ClientConfig == nil {
		return
	}
	if req.Strategy != apiextv1.WebhookConverter {
		// The service CA bundle is only injected by the service-ca controller into
		// the CRD if the CRD is configured to use a webhook for conversion
		return
	}
	exc := existing.Spec.Conversion
	if exc != nil &&
		exc.Webhook != nil &&
		exc.Webhook.ClientConfig != nil {
		req.Webhook.ClientConfig.CABundle = exc.Webhook.ClientConfig.CABundle
	}
}

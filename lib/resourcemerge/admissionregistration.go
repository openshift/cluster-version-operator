package resourcemerge

import (
	admissionregv1 "k8s.io/api/admissionregistration/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/pointer"
)

// EnsureValidatingWebhookConfiguration ensures that the existing matches the required.
// modified is set to true when existing had to be updated with required.
func EnsureValidatingWebhookConfiguration(modified *bool, existing *admissionregv1.ValidatingWebhookConfiguration, required admissionregv1.ValidatingWebhookConfiguration) {
	EnsureObjectMeta(modified, &existing.ObjectMeta, required.ObjectMeta)
	ensureValidatingWebhookConfigurationDefaults(&required)
	if !equality.Semantic.DeepEqual(existing.Webhooks, required.Webhooks) {
		*modified = true
		existing.Webhooks = required.Webhooks
	}
}

func ensureValidatingWebhookConfigurationDefaults(required *admissionregv1.ValidatingWebhookConfiguration) {
	for i := range required.Webhooks {
		ensureValidatingWebhookDefaults(&required.Webhooks[i])
	}
}

func ensureValidatingWebhookDefaults(required *admissionregv1.ValidatingWebhook) {
	if required.FailurePolicy == nil {
		policy := admissionregv1.Fail
		required.FailurePolicy = &policy
	}
	if required.MatchPolicy == nil {
		policy := admissionregv1.Equivalent
		required.MatchPolicy = &policy
	}
	if required.NamespaceSelector == nil {
		required.NamespaceSelector = &metav1.LabelSelector{}
	}
	if required.ObjectSelector == nil {
		required.ObjectSelector = &metav1.LabelSelector{}
	}
	if required.TimeoutSeconds == nil {
		required.TimeoutSeconds = pointer.Int32(10)
	}
	for i := range required.Rules {
		ensureRuleWithOperationsDefaults(&required.Rules[i])
	}
}

func ensureRuleWithOperationsDefaults(required *admissionregv1.RuleWithOperations) {
	if required.Scope == nil {
		scope := admissionregv1.AllScopes
		required.Scope = &scope
	}
}


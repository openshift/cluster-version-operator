package resourcemerge

import (
	operatorsv1 "github.com/operator-framework/api/pkg/operators/v1"
	"k8s.io/apimachinery/pkg/api/equality"
)

func EnsureOperatorGroup(modified *bool, existing *operatorsv1.OperatorGroup, required operatorsv1.OperatorGroup) {
	EnsureObjectMeta(modified, &existing.ObjectMeta, required.ObjectMeta)

	if existing.Spec.Selector == nil && required.Spec.Selector != nil {
		*modified = true
		existing.Spec.Selector = required.Spec.Selector
	}
	if !equality.Semantic.DeepEqual(existing.Spec.Selector, required.Spec.Selector) {
		*modified = true
		existing.Spec.Selector = required.Spec.Selector
	}

	setStringSlice(modified, &existing.Spec.TargetNamespaces, required.Spec.TargetNamespaces)
	setStringIfSet(modified, &existing.Spec.ServiceAccountName, required.Spec.ServiceAccountName)
	setBool(modified, &existing.Spec.StaticProvidedAPIs, required.Spec.StaticProvidedAPIs)

	ensureUpgradeStrategyDefault(&required)
	if !equality.Semantic.DeepEqual(existing.Spec.UpgradeStrategy, required.Spec.UpgradeStrategy) {
		*modified = true
		existing.Spec.UpgradeStrategy = required.Spec.UpgradeStrategy
	}
}

func ensureUpgradeStrategyDefault(required *operatorsv1.OperatorGroup) {
	if required.Spec.UpgradeStrategy == "" {
		required.Spec.UpgradeStrategy = operatorsv1.UpgradeStrategyDefault
	}
}

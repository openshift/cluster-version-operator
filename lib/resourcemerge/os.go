package resourcemerge

import (
	"time"

	"k8s.io/apimachinery/pkg/api/equality"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	osv1 "github.com/openshift/cluster-version-operator/pkg/apis/operatorstatus.openshift.io/v1"
)

func EnsureClusterOperatorStatus(modified *bool, existing *osv1.ClusterOperator, required osv1.ClusterOperator) {
	EnsureObjectMeta(modified, &existing.ObjectMeta, required.ObjectMeta)
	ensureClusterOperatorStatus(modified, &existing.Status, required.Status)
}

func ensureClusterOperatorStatus(modified *bool, existing *osv1.ClusterOperatorStatus, required osv1.ClusterOperatorStatus) {
	if !equality.Semantic.DeepEqual(existing.Conditions, required.Conditions) {
		*modified = true
		existing.Conditions = required.Conditions
	}
	if existing.Version != required.Version {
		*modified = true
		existing.Version = required.Version
	}
	if !equality.Semantic.DeepEqual(existing.Extension.Raw, required.Extension.Raw) {
		*modified = true
		existing.Extension.Raw = required.Extension.Raw
	}
	if !equality.Semantic.DeepEqual(existing.Extension.Object, required.Extension.Object) {
		*modified = true
		existing.Extension.Object = required.Extension.Object
	}
}

func SetOperatorStatusCondition(conditions *[]osv1.ClusterOperatorStatusCondition, newCondition osv1.ClusterOperatorStatusCondition) {
	if conditions == nil {
		conditions = &[]osv1.ClusterOperatorStatusCondition{}
	}
	existingCondition := FindOperatorStatusCondition(*conditions, newCondition.Type)
	if existingCondition == nil {
		newCondition.LastTransitionTime = metav1.NewTime(time.Now())
		*conditions = append(*conditions, newCondition)
		return
	}

	if existingCondition.Status != newCondition.Status {
		existingCondition.Status = newCondition.Status
		existingCondition.LastTransitionTime = newCondition.LastTransitionTime
	}

	existingCondition.Reason = newCondition.Reason
	existingCondition.Message = newCondition.Message
}

func RemoveOperatorStatusCondition(conditions *[]osv1.ClusterOperatorStatusCondition, conditionType osv1.ClusterStatusConditionType) {
	if conditions == nil {
		conditions = &[]osv1.ClusterOperatorStatusCondition{}
	}
	newConditions := []osv1.ClusterOperatorStatusCondition{}
	for _, condition := range *conditions {
		if condition.Type != conditionType {
			newConditions = append(newConditions, condition)
		}
	}

	*conditions = newConditions
}

func FindOperatorStatusCondition(conditions []osv1.ClusterOperatorStatusCondition, conditionType osv1.ClusterStatusConditionType) *osv1.ClusterOperatorStatusCondition {
	for i := range conditions {
		if conditions[i].Type == conditionType {
			return &conditions[i]
		}
	}

	return nil
}

func IsOperatorStatusConditionTrue(conditions []osv1.ClusterOperatorStatusCondition, conditionType osv1.ClusterStatusConditionType) bool {
	return IsOperatorStatusConditionPresentAndEqual(conditions, conditionType, osv1.ConditionTrue)
}

func IsOperatorStatusConditionFalse(conditions []osv1.ClusterOperatorStatusCondition, conditionType osv1.ClusterStatusConditionType) bool {
	return IsOperatorStatusConditionPresentAndEqual(conditions, conditionType, osv1.ConditionFalse)
}

func IsOperatorStatusConditionPresentAndEqual(conditions []osv1.ClusterOperatorStatusCondition, conditionType osv1.ClusterStatusConditionType, status osv1.ConditionStatus) bool {
	for _, condition := range conditions {
		if condition.Type == conditionType {
			return condition.Status == status
		}
	}
	return false
}

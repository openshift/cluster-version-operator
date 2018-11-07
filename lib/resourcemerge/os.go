package resourcemerge

import (
	"time"

	"k8s.io/apimachinery/pkg/api/equality"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	cvv1 "github.com/openshift/cluster-version-operator/pkg/apis/config.openshift.io/v1"
)

func EnsureClusterOperatorStatus(modified *bool, existing *cvv1.ClusterOperator, required cvv1.ClusterOperator) {
	EnsureObjectMeta(modified, &existing.ObjectMeta, required.ObjectMeta)
	ensureClusterOperatorStatus(modified, &existing.Status, required.Status)
}

func ensureClusterOperatorStatus(modified *bool, existing *cvv1.ClusterOperatorStatus, required cvv1.ClusterOperatorStatus) {
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

func SetOperatorStatusCondition(conditions *[]cvv1.ClusterOperatorStatusCondition, newCondition cvv1.ClusterOperatorStatusCondition) {
	if conditions == nil {
		conditions = &[]cvv1.ClusterOperatorStatusCondition{}
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

func RemoveOperatorStatusCondition(conditions *[]cvv1.ClusterOperatorStatusCondition, conditionType cvv1.ClusterStatusConditionType) {
	if conditions == nil {
		conditions = &[]cvv1.ClusterOperatorStatusCondition{}
	}
	newConditions := []cvv1.ClusterOperatorStatusCondition{}
	for _, condition := range *conditions {
		if condition.Type != conditionType {
			newConditions = append(newConditions, condition)
		}
	}

	*conditions = newConditions
}

func FindOperatorStatusCondition(conditions []cvv1.ClusterOperatorStatusCondition, conditionType cvv1.ClusterStatusConditionType) *cvv1.ClusterOperatorStatusCondition {
	for i := range conditions {
		if conditions[i].Type == conditionType {
			return &conditions[i]
		}
	}

	return nil
}

func IsOperatorStatusConditionTrue(conditions []cvv1.ClusterOperatorStatusCondition, conditionType cvv1.ClusterStatusConditionType) bool {
	return IsOperatorStatusConditionPresentAndEqual(conditions, conditionType, cvv1.ConditionTrue)
}

func IsOperatorStatusConditionFalse(conditions []cvv1.ClusterOperatorStatusCondition, conditionType cvv1.ClusterStatusConditionType) bool {
	return IsOperatorStatusConditionPresentAndEqual(conditions, conditionType, cvv1.ConditionFalse)
}

func IsOperatorStatusConditionPresentAndEqual(conditions []cvv1.ClusterOperatorStatusCondition, conditionType cvv1.ClusterStatusConditionType, status cvv1.ConditionStatus) bool {
	for _, condition := range conditions {
		if condition.Type == conditionType {
			return condition.Status == status
		}
	}
	return false
}

func IsOperatorStatusConditionNotIn(conditions []cvv1.ClusterOperatorStatusCondition, conditionType cvv1.ClusterStatusConditionType, status ...cvv1.ConditionStatus) bool {
	for _, condition := range conditions {
		if condition.Type == conditionType {
			for _, s := range status {
				if s == condition.Status {
					return false
				}
			}
			return true
		}
	}
	return true
}

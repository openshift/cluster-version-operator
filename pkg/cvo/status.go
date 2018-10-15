package cvo

import (
	"fmt"

	"github.com/google/uuid"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"

	"github.com/openshift/cluster-version-operator/lib/resourceapply"
	"github.com/openshift/cluster-version-operator/lib/resourcemerge"
	cvv1 "github.com/openshift/cluster-version-operator/pkg/apis/clusterversion.openshift.io/v1"
	osv1 "github.com/openshift/cluster-version-operator/pkg/apis/operatorstatus.openshift.io/v1"
	"github.com/openshift/cluster-version-operator/pkg/cincinnati"
	"github.com/openshift/cluster-version-operator/pkg/version"
)

func (optr *Operator) syncProgressingStatus(config *cvv1.CVOConfig) error {
	var cvoUpdates []cvv1.Update
	if updates, err := checkForUpdate(*config); err == nil {
		for _, update := range updates {
			cvoUpdates = append(cvoUpdates, cvv1.Update{
				Version: update.Version.String(),
				Payload: update.Payload,
			})
		}
	}

	status := &osv1.ClusterOperator{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: optr.namespace,
			Name:      optr.name,
		},
		Status: osv1.ClusterOperatorStatus{
			Version: version.Raw,
			Extension: runtime.RawExtension{
				Raw: nil,
				Object: &cvv1.CVOStatus{
					AvailableUpdates: cvoUpdates,
				},
			},
		},
	}
	resourcemerge.SetOperatorStatusCondition(&status.Status.Conditions, osv1.ClusterOperatorStatusCondition{Type: osv1.OperatorAvailable, Status: osv1.ConditionFalse})
	resourcemerge.SetOperatorStatusCondition(&status.Status.Conditions, osv1.ClusterOperatorStatusCondition{
		Type: osv1.OperatorProgressing, Status: osv1.ConditionTrue,
		Message: fmt.Sprintf("Working towards %s", config),
	})
	resourcemerge.SetOperatorStatusCondition(&status.Status.Conditions, osv1.ClusterOperatorStatusCondition{Type: osv1.OperatorFailing, Status: osv1.ConditionFalse})

	_, _, err := resourceapply.ApplyOperatorStatusFromCache(optr.clusterOperatorLister, optr.client.OperatorstatusV1(), status)
	return err
}

func (optr *Operator) syncAvailableStatus(config *cvv1.CVOConfig) error {
	var cvoUpdates []cvv1.Update
	if updates, err := checkForUpdate(*config); err == nil {
		for _, update := range updates {
			cvoUpdates = append(cvoUpdates, cvv1.Update{
				Version: update.Version.String(),
				Payload: update.Payload,
			})
		}
	}

	status := &osv1.ClusterOperator{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: optr.namespace,
			Name:      optr.name,
		},
		Status: osv1.ClusterOperatorStatus{
			Version: version.Raw,
			Extension: runtime.RawExtension{
				Raw: nil,
				Object: &cvv1.CVOStatus{
					AvailableUpdates: cvoUpdates,
				},
			},
		},
	}
	resourcemerge.SetOperatorStatusCondition(&status.Status.Conditions, osv1.ClusterOperatorStatusCondition{
		Type: osv1.OperatorAvailable, Status: osv1.ConditionTrue,
		Message: fmt.Sprintf("Done applying %s", config),
	})
	resourcemerge.SetOperatorStatusCondition(&status.Status.Conditions, osv1.ClusterOperatorStatusCondition{Type: osv1.OperatorProgressing, Status: osv1.ConditionFalse})
	resourcemerge.SetOperatorStatusCondition(&status.Status.Conditions, osv1.ClusterOperatorStatusCondition{Type: osv1.OperatorFailing, Status: osv1.ConditionFalse})

	_, _, err := resourceapply.ApplyOperatorStatusFromCache(optr.clusterOperatorLister, optr.client.OperatorstatusV1(), status)
	return err
}

// syncDegradedStatus updates the OperatorStatus to Degraded.
// if ierr is nil, return nil
// if ierr is not nil, update OperatorStatus as Degraded and return ierr
func (optr *Operator) syncDegradedStatus(ierr error) error {
	if ierr == nil {
		return nil
	}

	status := &osv1.ClusterOperator{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: optr.namespace,
			Name:      optr.name,
		},
		Status: osv1.ClusterOperatorStatus{
			Version:   version.Raw,
			Extension: runtime.RawExtension{},
		},
	}
	resourcemerge.SetOperatorStatusCondition(&status.Status.Conditions, osv1.ClusterOperatorStatusCondition{Type: osv1.OperatorAvailable, Status: osv1.ConditionFalse})
	resourcemerge.SetOperatorStatusCondition(&status.Status.Conditions, osv1.ClusterOperatorStatusCondition{Type: osv1.OperatorProgressing, Status: osv1.ConditionFalse})
	resourcemerge.SetOperatorStatusCondition(&status.Status.Conditions, osv1.ClusterOperatorStatusCondition{
		Type: osv1.OperatorFailing, Status: osv1.ConditionTrue,
		Message: fmt.Sprintf("error syncing: %v", ierr),
	})

	_, _, err := resourceapply.ApplyOperatorStatusFromCache(optr.clusterOperatorLister, optr.client.OperatorstatusV1(), status)
	if err != nil {
		return err
	}
	return ierr
}

func checkForUpdate(config cvv1.CVOConfig) ([]cincinnati.Update, error) {
	uuid, err := uuid.Parse(string(config.ClusterID))
	if err != nil {
		return nil, err
	}
	return cincinnati.NewClient(uuid).GetUpdates(string(config.Upstream), config.Channel, version.Version)
}

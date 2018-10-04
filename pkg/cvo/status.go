package cvo

import (
	"fmt"

	"github.com/google/uuid"

	"github.com/openshift/cluster-version-operator/lib/resourceapply"
	cvv1 "github.com/openshift/cluster-version-operator/pkg/apis/clusterversion.openshift.io/v1"
	osv1 "github.com/openshift/cluster-version-operator/pkg/apis/operatorstatus.openshift.io/v1"
	"github.com/openshift/cluster-version-operator/pkg/cincinnati"
	"github.com/openshift/cluster-version-operator/pkg/version"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

func (optr *Operator) syncStatus(config *cvv1.CVOConfig, cond osv1.OperatorStatusCondition) error {
	if cond.Type == osv1.OperatorStatusConditionTypeDegraded {
		return fmt.Errorf("invalid cond %s", cond.Type)
	}

	var cvoUpdates []cvv1.Update
	if updates, err := checkForUpdate(*config); err == nil {
		for _, update := range updates {
			cvoUpdates = append(cvoUpdates, cvv1.Update{
				Version: update.Version.String(),
				Payload: update.Payload,
			})
		}
	}

	status := &osv1.OperatorStatus{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: optr.namespace,
			Name:      optr.name,
		},
		Condition:  cond,
		Version:    version.Raw,
		LastUpdate: metav1.Now(),
		Extension: runtime.RawExtension{
			Raw: nil,
			Object: &cvv1.CVOStatus{
				AvailableUpdates: cvoUpdates,
			},
		},
	}
	_, _, err := resourceapply.ApplyOperatorStatusFromCache(optr.operatorStatusLister, optr.client.OperatorstatusV1(), status)
	return err
}

// syncDegradedStatus updates the OperatorStatus to Degraded.
// if ierr is nil, return nil
// if ierr is not nil, update OperatorStatus as Degraded and return ierr
func (optr *Operator) syncDegradedStatus(ierr error) error {
	if ierr == nil {
		return nil
	}
	cond := osv1.OperatorStatusCondition{
		Type:    osv1.OperatorStatusConditionTypeDegraded,
		Message: fmt.Sprintf("error syncing: %v", ierr),
	}

	status := &osv1.OperatorStatus{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: optr.namespace,
			Name:      optr.name,
		},
		Condition:  cond,
		Version:    version.Raw,
		LastUpdate: metav1.Now(),
		Extension:  runtime.RawExtension{},
	}
	_, _, err := resourceapply.ApplyOperatorStatusFromCache(optr.operatorStatusLister, optr.client.OperatorstatusV1(), status)
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

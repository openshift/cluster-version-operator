package cvo

import (
	"fmt"

	"github.com/openshift/cluster-version-operator/lib/resourceapply"
	"github.com/openshift/cluster-version-operator/pkg/apis/clusterversion.openshift.io/v1"
	"github.com/openshift/cluster-version-operator/pkg/cincinnati"
	"github.com/openshift/cluster-version-operator/pkg/version"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

func (optr *Operator) syncStatus(config *v1.CVOConfig, cond v1.OperatorStatusCondition) error {
	if cond.Type == v1.OperatorStatusConditionTypeDegraded {
		return fmt.Errorf("invalid cond %s", cond.Type)
	}

	updates, err := checkForUpdate(*config)
	if err != nil {
		return err
	}
	var cvoUpdates []v1.Update
	for _, update := range updates {
		cvoUpdates = append(cvoUpdates, v1.Update{
			Version: update.Version.String(),
			Payload: update.Payload,
		})
	}

	status := &v1.OperatorStatus{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: optr.namespace,
			Name:      optr.name,
		},
		Condition:  cond,
		Version:    version.Raw,
		LastUpdate: metav1.Now(),
		Extension: runtime.RawExtension{
			Raw: nil,
			Object: &v1.CVOStatus{
				AvailableUpdates: cvoUpdates,
			},
		},
	}
	_, _, err = resourceapply.ApplyOperatorStatusFromCache(optr.operatorStatusLister, optr.client.ClusterversionV1(), status)
	return err
}

// syncDegradedStatus updates the OperatorStatus to Degraded.
// if ierr is nil, return nil
// if ierr is not nil, update OperatorStatus as Degraded and return ierr
func (optr *Operator) syncDegradedStatus(ierr error) error {
	if ierr == nil {
		return nil
	}
	cond := v1.OperatorStatusCondition{
		Type:    v1.OperatorStatusConditionTypeDegraded,
		Message: fmt.Sprintf("error syncing: %v", ierr),
	}

	status := &v1.OperatorStatus{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: optr.namespace,
			Name:      optr.name,
		},
		Condition:  cond,
		Version:    version.Raw,
		LastUpdate: metav1.Now(),
		Extension:  runtime.RawExtension{},
	}
	_, _, err := resourceapply.ApplyOperatorStatusFromCache(optr.operatorStatusLister, optr.client.ClusterversionV1(), status)
	if err != nil {
		return err
	}
	return ierr
}

func checkForUpdate(config v1.CVOConfig) ([]cincinnati.Update, error) {
	return cincinnati.NewClient(config.ClusterID).GetUpdates(string(config.Upstream), config.Channel, version.Version)
}

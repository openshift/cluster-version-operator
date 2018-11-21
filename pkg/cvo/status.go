package cvo

import (
	"fmt"

	"k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/openshift/cluster-version-operator/lib/resourcemerge"
	cvv1 "github.com/openshift/cluster-version-operator/pkg/apis/config.openshift.io/v1"
	osv1 "github.com/openshift/cluster-version-operator/pkg/apis/operatorstatus.openshift.io/v1"
	cvclientv1 "github.com/openshift/cluster-version-operator/pkg/generated/clientset/versioned/typed/config.openshift.io/v1"
)

func (optr *Operator) syncAvailableUpdatesStatus(original *cvv1.ClusterVersion) (bool, error) {
	config := optr.getAvailableUpdates().NeedsUpdate(original)
	if config == nil {
		return false, nil
	}

	config.Status.Current = optr.currentVersion()

	// only report change if we actually updated the server
	updated, err := applyClusterVersionStatus(optr.client.ConfigV1(), config, original)
	return updated != nil && updated.ResourceVersion != original.ResourceVersion, err
}

func (optr *Operator) syncProgressingStatus(config *cvv1.ClusterVersion) error {
	original := config.DeepCopy()

	config.Status.Generation = config.Generation
	config.Status.Current = optr.currentVersion()

	now := metav1.Now()

	// clear the available condition
	resourcemerge.SetOperatorStatusCondition(&config.Status.Conditions, osv1.ClusterOperatorStatusCondition{Type: osv1.OperatorAvailable, Status: osv1.ConditionFalse, LastTransitionTime: now})

	// preserve the most recent failing condition
	if resourcemerge.IsOperatorStatusConditionNotIn(config.Status.Conditions, osv1.OperatorFailing, osv1.ConditionTrue) {
		resourcemerge.SetOperatorStatusCondition(&config.Status.Conditions, osv1.ClusterOperatorStatusCondition{Type: osv1.OperatorFailing, Status: osv1.ConditionFalse, LastTransitionTime: now})
	}

	// set progressing with an accurate summary message
	if c := resourcemerge.FindOperatorStatusCondition(config.Status.Conditions, osv1.OperatorFailing); c != nil && c.Status == osv1.ConditionTrue {
		reason := c.Reason
		msg := summaryForReason(reason)
		resourcemerge.SetOperatorStatusCondition(&config.Status.Conditions, osv1.ClusterOperatorStatusCondition{
			Type:               osv1.OperatorProgressing,
			Status:             osv1.ConditionTrue,
			Reason:             reason,
			Message:            fmt.Sprintf("Unable to apply %s: %s", optr.desiredVersionString(config), msg),
			LastTransitionTime: now,
		})
	} else {
		resourcemerge.SetOperatorStatusCondition(&config.Status.Conditions, osv1.ClusterOperatorStatusCondition{
			Type:               osv1.OperatorProgressing,
			Status:             osv1.ConditionTrue,
			Message:            fmt.Sprintf("Working towards %s", optr.desiredVersionString(config)),
			LastTransitionTime: now,
		})

	}

	updated, err := applyClusterVersionStatus(optr.client.ConfigV1(), config, original)
	optr.rememberLastUpdate(updated)
	return err
}

func (optr *Operator) syncAvailableStatus(config *cvv1.ClusterVersion, current cvv1.Update, versionHash string) error {
	original := config.DeepCopy()

	config.Status.Current = current
	config.Status.VersionHash = versionHash
	config.Status.Generation = config.Generation

	now := metav1.Now()
	version := optr.currentVersionString(config)

	// set the available condition
	resourcemerge.SetOperatorStatusCondition(&config.Status.Conditions, osv1.ClusterOperatorStatusCondition{
		Type:    osv1.OperatorAvailable,
		Status:  osv1.ConditionTrue,
		Message: fmt.Sprintf("Done applying %s", version),

		LastTransitionTime: now,
	})

	// clear the failure condition
	resourcemerge.SetOperatorStatusCondition(&config.Status.Conditions, osv1.ClusterOperatorStatusCondition{Type: osv1.OperatorFailing, Status: osv1.ConditionFalse, LastTransitionTime: now})

	// clear the progressing condition
	resourcemerge.SetOperatorStatusCondition(&config.Status.Conditions, osv1.ClusterOperatorStatusCondition{
		Type:    osv1.OperatorProgressing,
		Status:  osv1.ConditionFalse,
		Message: fmt.Sprintf("Cluster version is %s", version),

		LastTransitionTime: now,
	})

	updated, err := applyClusterVersionStatus(optr.client.ConfigV1(), config, original)
	optr.rememberLastUpdate(updated)
	return err
}

func (optr *Operator) syncPayloadFailingStatus(original *cvv1.ClusterVersion, err error) error {
	config := original.DeepCopy()

	config.Status.Generation = config.Generation
	config.Status.Current = optr.currentVersion()

	now := metav1.Now()
	var reason string
	msg := "an error occurred"
	if uErr, ok := err.(*updateError); ok {
		reason = uErr.Reason
		msg = summaryForReason(reason)
	}

	// leave the available condition alone

	// set the failing condition
	resourcemerge.SetOperatorStatusCondition(&config.Status.Conditions, osv1.ClusterOperatorStatusCondition{
		Type:               osv1.OperatorFailing,
		Status:             osv1.ConditionTrue,
		Reason:             reason,
		Message:            err.Error(),
		LastTransitionTime: now,
	})

	// update the progressing condition message to indicate there is an error
	if resourcemerge.IsOperatorStatusConditionTrue(config.Status.Conditions, osv1.OperatorProgressing) {
		resourcemerge.SetOperatorStatusCondition(&config.Status.Conditions, osv1.ClusterOperatorStatusCondition{
			Type:               osv1.OperatorProgressing,
			Status:             osv1.ConditionTrue,
			Reason:             reason,
			Message:            fmt.Sprintf("Unable to apply %s: %s", optr.desiredVersionString(config), msg),
			LastTransitionTime: now,
		})
	} else {
		resourcemerge.SetOperatorStatusCondition(&config.Status.Conditions, osv1.ClusterOperatorStatusCondition{
			Type:               osv1.OperatorProgressing,
			Status:             osv1.ConditionFalse,
			Reason:             reason,
			Message:            fmt.Sprintf("Error while reconciling %s: %s", optr.desiredVersionString(config), msg),
			LastTransitionTime: now,
		})
	}

	updated, err := applyClusterVersionStatus(optr.client.ConfigV1(), config, original)
	optr.rememberLastUpdate(updated)
	return err
}

func (optr *Operator) syncUpdateFailingStatus(original *cvv1.ClusterVersion, err error) error {
	config := original.DeepCopy()

	config.Status.Generation = config.Generation
	config.Status.Current = optr.currentVersion()

	now := metav1.Now()
	var reason string
	msg := "an error occurred"
	if uErr, ok := err.(*updateError); ok {
		reason = uErr.Reason
		msg = summaryForReason(reason)
	}

	// clear the available condition
	resourcemerge.SetOperatorStatusCondition(&config.Status.Conditions, osv1.ClusterOperatorStatusCondition{Type: osv1.OperatorAvailable, Status: osv1.ConditionFalse, LastTransitionTime: now})

	// set the failing condition
	resourcemerge.SetOperatorStatusCondition(&config.Status.Conditions, osv1.ClusterOperatorStatusCondition{
		Type:               osv1.OperatorFailing,
		Status:             osv1.ConditionTrue,
		Reason:             reason,
		Message:            err.Error(),
		LastTransitionTime: now,
	})

	// update the progressing condition message to indicate there is an error
	if resourcemerge.IsOperatorStatusConditionTrue(config.Status.Conditions, osv1.OperatorProgressing) {
		resourcemerge.SetOperatorStatusCondition(&config.Status.Conditions, osv1.ClusterOperatorStatusCondition{
			Type:               osv1.OperatorProgressing,
			Status:             osv1.ConditionTrue,
			Reason:             reason,
			Message:            fmt.Sprintf("Unable to apply %s: %s", optr.desiredVersionString(config), msg),
			LastTransitionTime: now,
		})
	} else {
		resourcemerge.SetOperatorStatusCondition(&config.Status.Conditions, osv1.ClusterOperatorStatusCondition{
			Type:               osv1.OperatorProgressing,
			Status:             osv1.ConditionFalse,
			Reason:             reason,
			Message:            fmt.Sprintf("Error while reconciling %s: %s", optr.desiredVersionString(config), msg),
			LastTransitionTime: now,
		})
	}

	updated, err := applyClusterVersionStatus(optr.client.ConfigV1(), config, original)
	optr.rememberLastUpdate(updated)
	return err
}

// syncDegradedStatus handles generic errors in the cluster version. It tries to preserve
// all status fields that it can by using the provided config or loading the latest version
// from the cache (instead of clearing the status).
// if ierr is nil, return nil
// if ierr is not nil, update OperatorStatus as Failing and return ierr
func (optr *Operator) syncFailingStatus(config *cvv1.ClusterVersion, ierr error) error {
	if ierr == nil {
		return nil
	}

	// try to reuse the most recent status if available
	if config == nil {
		config, _ = optr.cvLister.Get(optr.name)
	}
	if config == nil {
		config = &cvv1.ClusterVersion{
			ObjectMeta: metav1.ObjectMeta{
				Name: optr.name,
			},
		}
	}

	original := config.DeepCopy()

	config.Status.Current = optr.currentVersion()

	now := metav1.Now()
	msg := fmt.Sprintf("Error ensuring the cluster version is up to date: %v", ierr)

	// clear the available condition
	resourcemerge.SetOperatorStatusCondition(&config.Status.Conditions, osv1.ClusterOperatorStatusCondition{Type: osv1.OperatorAvailable, Status: osv1.ConditionFalse, LastTransitionTime: now})

	// reset the failing message
	resourcemerge.SetOperatorStatusCondition(&config.Status.Conditions, osv1.ClusterOperatorStatusCondition{
		Type:               osv1.OperatorFailing,
		Status:             osv1.ConditionTrue,
		Message:            ierr.Error(),
		LastTransitionTime: now,
	})

	// preserve the status of the existing progressing condition
	progressingStatus := osv1.ConditionFalse
	if resourcemerge.IsOperatorStatusConditionTrue(config.Status.Conditions, osv1.OperatorProgressing) {
		progressingStatus = osv1.ConditionTrue
	}
	resourcemerge.SetOperatorStatusCondition(&config.Status.Conditions, osv1.ClusterOperatorStatusCondition{
		Type:               osv1.OperatorProgressing,
		Status:             progressingStatus,
		Message:            msg,
		LastTransitionTime: now,
	})

	updated, err := applyClusterVersionStatus(optr.client.ConfigV1(), config, original)
	optr.rememberLastUpdate(updated)
	if err != nil {
		return err
	}
	return ierr
}

// applyClusterVersionStatus attempts to overwrite the status subresource of required. If
// original is provided it is compared to required and no update will be made if the
// object does not change. The method will retry a conflict by retrieving the latest live
// version and updating the metadata of required. required is modified if the object on
// the server is newer.
func applyClusterVersionStatus(client cvclientv1.ClusterVersionsGetter, required, original *cvv1.ClusterVersion) (*cvv1.ClusterVersion, error) {
	if original != nil && equality.Semantic.DeepEqual(&original.Status, &required.Status) {
		return required, nil
	}
	actual, err := client.ClusterVersions().UpdateStatus(required)
	if apierrors.IsConflict(err) {
		existing, cErr := client.ClusterVersions().Get(required.Name, metav1.GetOptions{})
		if err != nil {
			return nil, cErr
		}
		if existing.UID != required.UID {
			return nil, fmt.Errorf("cluster version was deleted and recreated, cannot update status")
		}
		if equality.Semantic.DeepEqual(&existing.Status, &required.Status) {
			return existing, nil
		}
		required.ObjectMeta = existing.ObjectMeta
		actual, err = client.ClusterVersions().UpdateStatus(required)
	}
	if err != nil {
		return nil, err
	}
	required.ObjectMeta = actual.ObjectMeta
	return actual, nil
}

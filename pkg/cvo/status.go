package cvo

import (
	"fmt"
	"time"

	"github.com/blang/semver"

	"github.com/golang/glog"
	"github.com/google/uuid"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/openshift/cluster-version-operator/lib/resourceapply"
	"github.com/openshift/cluster-version-operator/lib/resourcemerge"
	cvv1 "github.com/openshift/cluster-version-operator/pkg/apis/config.openshift.io/v1"
	osv1 "github.com/openshift/cluster-version-operator/pkg/apis/operatorstatus.openshift.io/v1"
	"github.com/openshift/cluster-version-operator/pkg/cincinnati"
)

// syncAvailableUpdates attempts to retrieve the latest updates and update the status of the ClusterVersion
// object. It will set the RetrievedUpdates condition. Updates are only checked if it has been more than
// the minimumUpdateCheckInterval since the last check.
func (optr *Operator) syncAvailableUpdates(config *cvv1.ClusterVersion) error {
	// updates are only checked at most once per minimumUpdateCheckInterval or if the generation changes
	if config.Generation == config.Status.Generation && optr.hasRecentlyRetrievedUpdates() {
		glog.V(4).Infof("Available updates were recently retrieved, will try later.")
		return nil
	}

	config = config.DeepCopy()

	// default the upstream server to the configured value
	if config.Spec.Upstream == nil && len(optr.defaultUpstreamServer) > 0 {
		u := cvv1.URL(optr.defaultUpstreamServer)
		config.Spec.Upstream = &u
	}

	// we will create a new condition with a new transition time.
	resourcemerge.RemoveOperatorStatusCondition(&config.Status.Conditions, cvv1.RetrievedUpdates)
	if err := calculateAvailableUpdatesStatus(config, optr.releaseVersion); err != nil {
		return err
	}

	optr.setLastRetrieveAt(time.Now())

	_, _, err := resourceapply.ApplyClusterVersionStatusFromCache(optr.cvoConfigLister, optr.client.ConfigV1(), config)
	return err
}

// hasRecentlyRetrievedUpdates returns true if the most recent update retrieval attempt
// happened within the minimum update check interval.
func (optr *Operator) hasRecentlyRetrievedUpdates() bool {
	if optr.minimumUpdateCheckInterval == 0 {
		return false
	}
	optr.lastAtLock.Lock()
	defer optr.lastAtLock.Unlock()
	return optr.lastRetrieveAt.After(time.Now().Add(-optr.minimumUpdateCheckInterval))
}

// setLastSyncAt sets the time the operator was last synced at.
func (optr *Operator) setLastRetrieveAt(t time.Time) {
	optr.lastAtLock.Lock()
	defer optr.lastAtLock.Unlock()
	optr.lastRetrieveAt = t
}

func calculateAvailableUpdatesStatus(config *cvv1.ClusterVersion, version string) error {
	var cvoUpdates []cvv1.Update
	if config.Spec.Upstream == nil {
		resourcemerge.SetOperatorStatusCondition(&config.Status.Conditions, osv1.ClusterOperatorStatusCondition{
			Type: cvv1.RetrievedUpdates, Status: osv1.ConditionFalse, Reason: "NoUpstream",
			Message: "No upstream server has been set to retrieve updates.",
		})
		return nil
	}

	if len(version) == 0 {
		resourcemerge.SetOperatorStatusCondition(&config.Status.Conditions, osv1.ClusterOperatorStatusCondition{
			Type: cvv1.RetrievedUpdates, Status: osv1.ConditionFalse, Reason: "NoCurrentVersion",
			Message: "The cluster version does not have a semantic version assigned and cannot calculate valid upgrades.",
		})
		return nil
	}

	currentVersion, err := semver.Parse(version)
	if err != nil {
		glog.V(2).Infof("Unable to parse current semantic version %q: %v", version, err)
		resourcemerge.SetOperatorStatusCondition(&config.Status.Conditions, osv1.ClusterOperatorStatusCondition{
			Type: cvv1.RetrievedUpdates, Status: osv1.ConditionFalse, Reason: "InvalidCurrentVersion",
			Message: "The current cluster version is not a valid semantic version and cannot be used to calculate upgrades.",
		})
		return nil
	}

	updates, err := checkForUpdate(*config, currentVersion)
	if err != nil {
		glog.V(2).Infof("Upstream server %s could not return available updates: %v", *config.Spec.Upstream, err)
		resourcemerge.SetOperatorStatusCondition(&config.Status.Conditions, osv1.ClusterOperatorStatusCondition{
			Type: cvv1.RetrievedUpdates, Status: osv1.ConditionFalse, Reason: "RemoteFailed",
			Message: fmt.Sprintf("Unable to retrieve available updates: %v", err),
		})
		return nil
	}

	for _, update := range updates {
		cvoUpdates = append(cvoUpdates, cvv1.Update{
			Version: update.Version.String(),
			Payload: update.Payload,
		})
	}
	config.Status.AvailableUpdates = cvoUpdates
	resourcemerge.SetOperatorStatusCondition(&config.Status.Conditions, osv1.ClusterOperatorStatusCondition{
		Type:   cvv1.RetrievedUpdates,
		Status: osv1.ConditionTrue,

		LastTransitionTime: metav1.Now(),
	})
	return nil
}

func (optr *Operator) syncProgressingStatus(config *cvv1.ClusterVersion) error {
	now := metav1.Now()
	resourcemerge.SetOperatorStatusCondition(&config.Status.Conditions, osv1.ClusterOperatorStatusCondition{Type: osv1.OperatorAvailable, Status: osv1.ConditionFalse, LastTransitionTime: now})
	resourcemerge.SetOperatorStatusCondition(&config.Status.Conditions, osv1.ClusterOperatorStatusCondition{
		Type:    osv1.OperatorProgressing,
		Status:  osv1.ConditionTrue,
		Message: fmt.Sprintf("Working towards %s", optr.desiredVersionString(config)),

		LastTransitionTime: now,
	})
	resourcemerge.SetOperatorStatusCondition(&config.Status.Conditions, osv1.ClusterOperatorStatusCondition{Type: osv1.OperatorFailing, Status: osv1.ConditionFalse, LastTransitionTime: now})

	config.Status.Generation = config.Generation
	config.Status.Current = optr.currentVersion()

	_, _, err := resourceapply.ApplyClusterVersionStatusFromCache(optr.cvoConfigLister, optr.client.ConfigV1(), config)
	return err
}

func (optr *Operator) syncAvailableStatus(config *cvv1.ClusterVersion) error {
	now := metav1.Now()
	version := optr.currentVersionString(config)
	resourcemerge.SetOperatorStatusCondition(&config.Status.Conditions, osv1.ClusterOperatorStatusCondition{
		Type:    osv1.OperatorAvailable,
		Status:  osv1.ConditionTrue,
		Message: fmt.Sprintf("Done applying %s", version),

		LastTransitionTime: now,
	})
	resourcemerge.SetOperatorStatusCondition(&config.Status.Conditions, osv1.ClusterOperatorStatusCondition{
		Type:    osv1.OperatorProgressing,
		Status:  osv1.ConditionFalse,
		Message: fmt.Sprintf("Cluster version is %s", version),

		LastTransitionTime: now,
	})
	resourcemerge.SetOperatorStatusCondition(&config.Status.Conditions, osv1.ClusterOperatorStatusCondition{Type: osv1.OperatorFailing, Status: osv1.ConditionFalse, LastTransitionTime: now})

	config.Status.Generation = config.Generation

	_, _, err := resourceapply.ApplyClusterVersionStatusFromCache(optr.cvoConfigLister, optr.client.ConfigV1(), config)
	return err
}

// syncDegradedStatus updates the ClusterVersion status to Degraded.
// if ierr is nil, return nil
// if ierr is not nil, update OperatorStatus as Degraded and return ierr
func (optr *Operator) syncDegradedStatus(config *cvv1.ClusterVersion, ierr error) error {
	if ierr == nil {
		return nil
	}

	clusterVersion := &cvv1.ClusterVersion{
		ObjectMeta: metav1.ObjectMeta{
			Name: optr.name,
		},
	}
	// try to reuse the most recent status if available
	if config == nil {
		config, _ = optr.cvoConfigLister.Get(optr.name)
	}
	if config != nil {
		clusterVersion.Status = config.Status
	}

	now := metav1.Now()
	resourcemerge.SetOperatorStatusCondition(&clusterVersion.Status.Conditions, osv1.ClusterOperatorStatusCondition{Type: osv1.OperatorAvailable, Status: osv1.ConditionFalse, LastTransitionTime: now})
	resourcemerge.SetOperatorStatusCondition(&clusterVersion.Status.Conditions, osv1.ClusterOperatorStatusCondition{
		Type:    osv1.OperatorProgressing,
		Status:  osv1.ConditionFalse,
		Message: fmt.Sprintf("Failure to apply the cluster version, check operators"),

		LastTransitionTime: now,
	})
	resourcemerge.SetOperatorStatusCondition(&clusterVersion.Status.Conditions, osv1.ClusterOperatorStatusCondition{
		Type:    osv1.OperatorFailing,
		Status:  osv1.ConditionTrue,
		Message: fmt.Sprintf("error syncing: %v", ierr),

		LastTransitionTime: now,
	})

	clusterVersion.Status.Current = optr.currentVersion()

	_, _, err := resourceapply.ApplyClusterVersionStatusFromCache(optr.cvoConfigLister, optr.client.ConfigV1(), clusterVersion)
	if err != nil {
		return err
	}
	return ierr
}

func checkForUpdate(config cvv1.ClusterVersion, currentVersion semver.Version) ([]cincinnati.Update, error) {
	uuid, err := uuid.Parse(string(config.Spec.ClusterID))
	if err != nil {
		return nil, err
	}
	if config.Spec.Upstream == nil {
		return nil, fmt.Errorf("no upstream URL set for cluster version")
	}
	return cincinnati.NewClient(uuid).GetUpdates(string(*config.Spec.Upstream), config.Spec.Channel, currentVersion)
}

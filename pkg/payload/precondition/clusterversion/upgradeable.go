package clusterversion

import (
	"context"
	"fmt"
	"time"

	"github.com/blang/semver/v4"
	configv1 "github.com/openshift/api/config/v1"
	configv1listers "github.com/openshift/client-go/config/listers/config/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/klog/v2"

	"github.com/openshift/cluster-version-operator/lib/resourcemerge"
	"github.com/openshift/cluster-version-operator/pkg/payload/precondition"
)

// Upgradeable checks if clusterversion is upgradeable currently.
type Upgradeable struct {
	key    string
	lister configv1listers.ClusterVersionLister
}

// NewUpgradeable returns a new Upgradeable precondition check.
func NewUpgradeable(lister configv1listers.ClusterVersionLister) *Upgradeable {
	return &Upgradeable{
		key:    "version",
		lister: lister,
	}
}

// ClusterVersionOverridesCondition returns an UpgradeableClusterVersionOverrides condition when overrides are set, and nil when no overrides are set.
func ClusterVersionOverridesCondition(cv *configv1.ClusterVersion) *configv1.ClusterOperatorStatusCondition {
	for _, override := range cv.Spec.Overrides {
		if override.Unmanaged {
			condition := configv1.ClusterOperatorStatusCondition{
				Type:    configv1.ClusterStatusConditionType("UpgradeableClusterVersionOverrides"),
				Status:  configv1.ConditionFalse,
				Reason:  "ClusterVersionOverridesSet",
				Message: "Disabling ownership via cluster version overrides prevents upgrades. Please remove overrides before continuing.",
			}
			return &condition
		}
	}
	return nil
}

// Run runs the Upgradeable precondition.
// If the feature gate `key` is not found, or the api for clusterversion doesn't exist, this check is inert and always returns nil error.
// Otherwise, if Upgradeable condition is set to false in the object, it returns an PreconditionError when possible.
func (pf *Upgradeable) Run(ctx context.Context, releaseContext precondition.ReleaseContext) error {
	cv, err := pf.lister.Get(pf.key)
	if apierrors.IsNotFound(err) || meta.IsNoMatchError(err) {
		return nil
	}
	if err != nil {
		return &precondition.Error{
			Nested:  err,
			Reason:  "UnknownError",
			Message: err.Error(),
			Name:    pf.Name(),
		}
	}

	// if we are upgradeable==true we can always upgrade
	up := resourcemerge.FindOperatorStatusCondition(cv.Status.Conditions, configv1.OperatorUpgradeable)
	if up == nil {
		klog.V(2).Infof("Precondition %s passed: no Upgradeable condition on ClusterVersion.", pf.Name())
		return nil
	}
	if up.Status != configv1.ConditionFalse {
		klog.V(2).Infof("Precondition %s passed: Upgradeable %s since %s: %s: %s", pf.Name(), up.Status, up.LastTransitionTime.Format(time.RFC3339), up.Reason, up.Message)
		return nil
	}

	currentVersion, err := semver.Parse(cv.Status.Desired.Version)
	if err != nil {
		return &precondition.Error{
			Nested:             err,
			Reason:             "InvalidCurrentVersion",
			Message:            err.Error(),
			Name:               pf.Name(),
			NonBlockingWarning: true, // do not block on issues that require an update to fix
		}
	}

	targetVersion, err := semver.Parse(releaseContext.DesiredVersion)
	if err != nil {
		return &precondition.Error{
			Nested:  err,
			Reason:  "InvalidDesiredVersion",
			Message: err.Error(),
			Name:    pf.Name(),
		}
	}

	klog.V(4).Infof("The current version is %s parsed from %s and the target version is %s parsed from %s", currentVersion.String(), cv.Status.Desired.Version, targetVersion.String(), releaseContext.DesiredVersion)
	if targetVersion.LTE(currentVersion) || (targetVersion.Major == currentVersion.Major && targetVersion.Minor == currentVersion.Minor) {
		// When Upgradeable==False, a patch level update with the same minor level is allowed unless overrides are set
		// This Upgradeable precondition is only concerned about moving forward, i.e., do not care about downgrade which is taken care of by the Rollback precondition
		if condition := ClusterVersionOverridesCondition(cv); condition != nil {
			klog.V(2).Infof("Retarget from %s to %s is blocked by %s: %s", currentVersion.String(), targetVersion.String(), condition.Reason, condition.Message)
			return &precondition.Error{
				Reason:  condition.Reason,
				Message: condition.Message,
				Name:    pf.Name(),
			}
		} else {
			if completedVersion := minorUpdateIsInProgress(cv.Status, currentVersion); completedVersion != "" &&
				targetVersion.Major == currentVersion.Major &&
				targetVersion.Minor == currentVersion.Minor {
				// This is to generate an accepted risk for the accepting case 4.y.z -> 4.y+1.z' -> 4.y+1.z''
				return &precondition.Error{
					Reason:             "MinorVersionClusterUpgradeInProgress",
					Message:            fmt.Sprintf("Retarget to %s while a minor level upgrade from %s to %s is in progress", targetVersion, completedVersion, targetVersion),
					Name:               pf.Name(),
					NonBlockingWarning: true,
				}
			}
			klog.V(2).Infof("Precondition %q passed on update to %s", pf.Name(), targetVersion.String())
			return nil
		}
	}

	return &precondition.Error{
		Nested:  err,
		Reason:  up.Reason,
		Message: up.Message,
		Name:    pf.Name(),
	}
}

// minorUpdateIsInProgress returns the version that was installed completed if a minor level upgrade is in progress
// and the empty string otherwise
func minorUpdateIsInProgress(status configv1.ClusterVersionStatus, currentVersion semver.Version) string {
	completedVersionStr := GetCurrentVersion(status.History)
	if completedVersionStr == "" {
		return ""
	}
	v, err := semver.Parse(completedVersionStr)
	if err != nil {
		return ""
	}
	if cond := resourcemerge.FindOperatorStatusCondition(status.Conditions, configv1.OperatorProgressing); cond != nil &&
		cond.Status == configv1.ConditionTrue &&
		v.Major == currentVersion.Major &&
		v.Minor < currentVersion.Minor {
		return completedVersionStr
	}
	return ""
}

// Name returns Name for the precondition.
func (pf *Upgradeable) Name() string { return "ClusterVersionUpgradeable" }

// GetCurrentVersion determines and returns the cluster's current version by iterating through the
// provided update history until it finds the first version with update State of Completed. If a
// Completed version is not found the version of the oldest history entry, which is the originally
// installed version, is returned. If history is empty the empty string is returned.
func GetCurrentVersion(history []configv1.UpdateHistory) string {
	for _, h := range history {
		if h.State == configv1.CompletedUpdate {
			klog.V(2).Infof("Cluster current version=%s", h.Version)
			return h.Version
		}
	}
	// Empty history should only occur if method is called early in startup before history is populated.
	if len(history) != 0 {
		return history[len(history)-1].Version
	}
	return ""
}

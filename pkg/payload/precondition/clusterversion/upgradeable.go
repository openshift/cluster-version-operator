package clusterversion

import (
	"context"
	"strconv"
	"strings"

	configv1 "github.com/openshift/api/config/v1"
	configv1listers "github.com/openshift/client-go/config/listers/config/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/klog/v2"

	"github.com/openshift/cluster-version-operator/lib/resourcemerge"
	precondition "github.com/openshift/cluster-version-operator/pkg/payload/precondition"
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
		klog.V(4).Infof("Precondition %s passed: no Upgradeable condition on ClusterVersion.", pf.Name())
		return nil
	}
	if up.Status != configv1.ConditionFalse {
		klog.V(4).Infof("Precondition %s passed: Upgradeable %s since %v: %s: %s", pf.Name(), up.Status, up.LastTransitionTime, up.Reason, up.Message)
		return nil
	}

	// we can always allow the upgrade if there isn't a version already installed
	if len(cv.Status.History) == 0 {
		klog.V(4).Infof("Precondition %s passed: no release history.", pf.Name())
		return nil
	}

	currentVersion := GetCurrentVersion(cv.Status.History)
	currentMinor := GetEffectiveMinor(currentVersion)
	desiredMinor := GetEffectiveMinor(releaseContext.DesiredVersion)
	klog.V(5).Infof("currentMinor %s releaseContext.DesiredVersion %s desiredMinor %s", currentMinor, releaseContext.DesiredVersion, desiredMinor)

	// if there is no difference in the minor version (4.y.z where 4.y is the same for current and desired), then we can still upgrade
	// if no cluster overrides have been set
	if currentMinor == desiredMinor {
		klog.V(4).Infof("Precondition %q passed: minor from the current %s matches minor from the target %s (both %s).", pf.Name(), currentVersion, releaseContext.DesiredVersion, currentMinor)
		if condition := ClusterVersionOverridesCondition(cv); condition != nil {
			klog.V(4).Infof("Update from %s to %s blocked by %s: %s", currentVersion, releaseContext.DesiredVersion, condition.Reason, condition.Message)

			return &precondition.Error{
				Reason:  condition.Reason,
				Message: condition.Message,
				Name:    pf.Name(),
			}
		} else {
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

// Name returns Name for the precondition.
func (pf *Upgradeable) Name() string { return "ClusterVersionUpgradeable" }

// GetCurrentVersion determines and returns the cluster's current version by iterating through the
// provided update history until it finds the first version with update State of Completed. If a
// Completed version is not found the version of the oldest history entry, which is the originally
// installed version, is returned. If history is empty the empty string is returned.
func GetCurrentVersion(history []configv1.UpdateHistory) string {
	for _, h := range history {
		if h.State == configv1.CompletedUpdate {
			klog.V(5).Infof("Cluster current version=%s", h.Version)
			return h.Version
		}
	}
	// Empty history should only occur if method is called early in startup before history is populated.
	if len(history) != 0 {
		return history[len(history)-1].Version
	}
	return ""
}

// GetEffectiveMinor attempts to do a simple parse of the version provided.  If it does not parse, the value is considered
// empty string, which works for the comparison done here for equivalence.
func GetEffectiveMinor(version string) string {
	splits := strings.Split(version, ".")
	if len(splits) < 2 {
		return ""
	}
	return splits[1]
}

// minorVersionUpgrade returns true if the the desired update minor version number is greater
// than the current version minor version number. Errors resulting from either version
// number being unset or NaN are ignored simply resulting in false returned.
func minorVersionUpgrade(currentMinor string, desiredMinor string) bool {
	if currentMinorNum, err := strconv.Atoi(currentMinor); err == nil {
		if desiredMinorNum, err := strconv.Atoi(desiredMinor); err == nil {
			if desiredMinorNum > currentMinorNum {
				return true
			}
		}
	}
	return false
}

package clusterversion

import (
	"context"
	"strings"

	configv1 "github.com/openshift/api/config/v1"
	configv1listers "github.com/openshift/client-go/config/listers/config/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"

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
	if up == nil || up.Status != configv1.ConditionFalse {
		return nil
	}

	// we can always allow the upgrade if there isn't a version already installed
	if len(cv.Status.History) == 0 {
		return nil
	}

	currentMinor := getEffectiveMinor(cv.Status.History[0].Version)
	desiredMinor := getEffectiveMinor(releaseContext.DesiredVersion)

	// if there is no difference in the minor version (4.y.z where 4.y is the same for current and desired), then we can still upgrade
	if currentMinor == desiredMinor {
		return nil
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

// getEffectiveMinor attempts to do a simple parse of the version provided.  If it does not parse, the value is considered
// empty string, which works for the comparison done here for equivalence.
func getEffectiveMinor(version string) string {
	splits := strings.Split(version, ".")
	if len(splits) < 2 {
		return ""
	}
	return splits[1]
}

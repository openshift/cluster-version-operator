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

// UpgradeableMinor checks if clusterversion is upgradeable currently.
type UpgradeableMinor struct {
	key    string
	lister configv1listers.ClusterVersionLister
}

// NewUpgradeableMinor returns a new UpgradeableMinor precondition check.
func NewUpgradeableMinor(lister configv1listers.ClusterVersionLister) *UpgradeableMinor {
	return &UpgradeableMinor{
		key:    "version",
		lister: lister,
	}
}

// Run runs the UpgradeableMinor precondition.
// If the feature gate `key` is not found, or the api for clusterversion doesn't exist, this check is inert and always returns nil error.
// Otherwise, if UpgradeableMinor condition is set to false in the object, it returns an PreconditionError when possible.
func (pf *UpgradeableMinor) Run(ctx context.Context, desiredVersion string) error {
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

	if len(cv.Status.History) == 0 {
		return nil
	}

	up := resourcemerge.FindOperatorStatusCondition(cv.Status.Conditions, "UpgradeableMinor")
	if up == nil || up.Status != configv1.ConditionFalse {
		return nil
	}

	// TODO properly find version
	currentMinor := strings.Split(cv.Status.History[0].Version, ".")[1]
	desiredMinor := strings.Split(desiredVersion, ".")[1]

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
func (pf *UpgradeableMinor) Name() string { return "ClusterVersionUpgradeableMinor" }

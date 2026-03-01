package clusterversion

import (
	"context"
	"fmt"

	"github.com/blang/semver/v4"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"

	configv1listers "github.com/openshift/client-go/config/listers/config/v1"

	"github.com/openshift/cluster-version-operator/pkg/payload/precondition"
)

// GiantHop blocks giant hops from the version that is currently being reconciled.
type GiantHop struct {
	lister configv1listers.ClusterVersionLister
}

// NewGiantHop returns a new GiantHop precondition check.
func NewGiantHop(lister configv1listers.ClusterVersionLister) *GiantHop {
	return &GiantHop{
		lister: lister,
	}
}

// Name returns the name of the precondition.
func (p *GiantHop) Name() string { return "ClusterVersionGiantHop" }

// Run runs the GiantHop precondition, blocking giant hops from the
// version that is currently being reconciled.  It returns a
// PreconditionError when possible.
func (p *GiantHop) Run(ctx context.Context, releaseContext precondition.ReleaseContext) error {
	cv, err := p.lister.Get("version")
	if apierrors.IsNotFound(err) || meta.IsNoMatchError(err) {
		return nil
	}
	if err != nil {
		return &precondition.Error{
			Nested:  err,
			Reason:  "UnknownError",
			Message: err.Error(),
			Name:    p.Name(),
		}
	}

	currentVersion, err := semver.Parse(cv.Status.Desired.Version)
	if err != nil {
		return &precondition.Error{
			Nested:             err,
			Reason:             "InvalidCurrentVersion",
			Message:            err.Error(),
			Name:               p.Name(),
			NonBlockingWarning: true, // do not block on issues that require an update to fix
		}
	}

	targetVersion, err := semver.Parse(releaseContext.DesiredVersion)
	if err != nil {
		return &precondition.Error{
			Nested:  err,
			Reason:  "InvalidDesiredVersion",
			Message: err.Error(),
			Name:    p.Name(),
		}
	}

	if targetVersion.Major > currentVersion.Major {
		if targetVersion.Major == 5 && currentVersion.Major == 4 { // TODO: temporary access from v4 to 5.0, to be reverted once we are in v5 development
			if targetVersion.Minor == 0 {
				return nil
			}
			return &precondition.Error{
				Reason:  "MajorVersionUpdate",
				Message: fmt.Sprintf("%s has a larger major version than the current target %s (%d > %d), and only updates within the current major version or to 5.0 are supported.", targetVersion, currentVersion, targetVersion.Major, currentVersion.Major),
				Name:    p.Name(),
			}
		}
		return &precondition.Error{
			Reason:  "MajorVersionUpdate",
			Message: fmt.Sprintf("%s has a larger major version than the current target %s (%d > %d), and only updates within the current major version are supported.", targetVersion, currentVersion, targetVersion.Major, currentVersion.Major),
			Name:    p.Name(),
		}
	}

	if targetVersion.Minor > currentVersion.Minor+1 {
		return &precondition.Error{
			Reason:  "MultipleMinorVersionsUpdate",
			Message: fmt.Sprintf("%s is more than one minor version beyond the current target %s (%d.%d > %d.(%d+1)), and only updates within the current minor version or to the next minor version are supported.", targetVersion, currentVersion, targetVersion.Major, targetVersion.Minor, currentVersion.Major, currentVersion.Minor),
			Name:    p.Name(),
		}
	}

	return nil
}

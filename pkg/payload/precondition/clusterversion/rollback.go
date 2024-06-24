package clusterversion

import (
	"context"
	"fmt"

	"github.com/blang/semver/v4"
	configv1 "github.com/openshift/api/config/v1"

	precondition "github.com/openshift/cluster-version-operator/pkg/payload/precondition"
)

// currentRelease is a callback for returning the version that is currently being reconciled.
type currentRelease func() configv1.Release

// Rollback blocks rollbacks from the version that is currently being reconciled.
type Rollback struct {
	currentRelease
}

// NewRollback returns a new Rollback precondition check.
func NewRollback(fn currentRelease) *Rollback {
	return &Rollback{
		currentRelease: fn,
	}
}

// Name returns Name for the precondition.
func (pf *Rollback) Name() string { return "ClusterVersionRollback" }

// Run runs the Rollback precondition, blocking rollbacks from the
// version that is currently being reconciled.  It returns a
// PreconditionError when possible.
func (p *Rollback) Run(ctx context.Context, releaseContext precondition.ReleaseContext) error {
	currentRelease := p.currentRelease()
	currentVersion, err := semver.Parse(currentRelease.Version)
	if err != nil {
		return &precondition.Error{
			Nested:  err,
			Reason:  "InvalidCurrentVersion",
			Message: err.Error(),
			Name:    p.Name(),
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

	if targetVersion.LT(currentVersion) {
		return &precondition.Error{
			Reason:  "LowDesiredVersion",
			Message: fmt.Sprintf("%s is less than the current target %s, but rollbacks and downgrades are not recommended", targetVersion, currentVersion),
			Name:    p.Name(),
		}
	}

	return nil
}

package clusterversion

import (
	"context"
	"fmt"

	configv1 "github.com/openshift/api/config/v1"

	precondition "github.com/openshift/cluster-version-operator/pkg/payload/precondition"
)

// currentRelease is a callback for returning the version that is currently being reconciled.
type currentRelease func() configv1.Release

// MetadataUpdate blocks metadataUpdates from the version that is currently being reconciled.
type MetadataUpdate struct {
	currentRelease
}

// NewMetadataUpdate returns a new MetadataUpdate precondition check.
func NewMetadataUpdate(fn currentRelease) *MetadataUpdate {
	return &MetadataUpdate{
		currentRelease: fn,
	}
}

// Name returns Name for the precondition.
func (pf *MetadataUpdate) Name() string { return "ClusterVersionMetadataUpdate" }

// Run runs the MetadataUpdate precondition, blocking metadataUpdates from the
// version that is currently being reconciled.  It returns a
// PreconditionError when possible.
func (p *MetadataUpdate) Run(ctx context.Context, releaseContext precondition.ReleaseContext) error {
	currentRelease := p.currentRelease()
	if releaseContext.DesiredVersion == currentRelease {
		return nil
	}

	for _, previous in := range releaseContext.Previous {
		if currentRelease == previous {
			return nil
		}
	}

	return &precondition.Error{
		Reason:  "InvalidDesiredVersion",
		Message: fmt.Sprintf("The current version %q is neither the desired version %q nor a version declared in the desired version's previous-release update metadata: %v", currentVersion, releaseContext.DesiredVersion, releaseContext.Previous)
		Name:    p.Name(),
	}
}

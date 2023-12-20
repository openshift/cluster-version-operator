package clusterversion

import (
	"context"
	"fmt"

	"github.com/blang/semver/v4"
	configv1listers "github.com/openshift/client-go/config/listers/config/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/klog/v2"

	precondition "github.com/openshift/cluster-version-operator/pkg/payload/precondition"
)

// Rollback blocks rollbacks from the version that is currently being reconciled.
type Rollback struct {
	key    string
	lister configv1listers.ClusterVersionLister
}

// NewRollback returns a new Rollback precondition check.
func NewRollback(lister configv1listers.ClusterVersionLister) *Rollback {
	return &Rollback{
		key:    "version",
		lister: lister,
	}
}

// Name returns Name for the precondition.
func (p *Rollback) Name() string { return "ClusterVersionRollback" }

// Run runs the Rollback precondition, blocking rollbacks from the
// version that is currently being reconciled.  It returns a
// PreconditionError when possible.
func (p *Rollback) Run(ctx context.Context, releaseContext precondition.ReleaseContext) error {
	cv, err := p.lister.Get(p.key)
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

	if targetVersion.LT(currentVersion) {
		var previousVersion *semver.Version
		var previousImage string
		for _, entry := range cv.Status.History {
			if entry.Version != currentVersion.String() || entry.Image != cv.Status.Desired.Image {
				version, err := semver.Parse(entry.Version)
				if err != nil {
					klog.Errorf("Precondition %q previous version %q invalid SemVer: %v", p.Name(), entry.Version, err)
				} else {
					previousVersion = &version
					previousImage = entry.Image
				}
				break
			}
		}

		if previousVersion != nil {
			if !targetVersion.EQ(*previousVersion) {
				return &precondition.Error{
					Reason:  "LowDesiredVersion",
					Message: fmt.Sprintf("%s is less than the current target %s, and the only supported rollback is to the cluster's previous version %s (%s)", targetVersion, currentVersion, *previousVersion, previousImage),
					Name:    p.Name(),
				}
			}
			if previousVersion.Major == currentVersion.Major && previousVersion.Minor == currentVersion.Minor {
				klog.V(2).Infof("Precondition %q allows rollbacks from %s to the previous version %s within a z-stream", p.Name(), currentVersion, targetVersion)
				return nil
			}
			return &precondition.Error{
				Reason:  "LowDesiredVersion",
				Message: fmt.Sprintf("%s is less than the current target %s and matches the cluster's previous version, but rollbacks that change major or minor versions are not recommended", targetVersion, currentVersion),
				Name:    p.Name(),
			}
		}
		return &precondition.Error{
			Reason:  "LowDesiredVersion",
			Message: fmt.Sprintf("%s is less than the current target %s, and the cluster has no previous Semantic Version", targetVersion, currentVersion),
			Name:    p.Name(),
		}
	}

	return nil
}

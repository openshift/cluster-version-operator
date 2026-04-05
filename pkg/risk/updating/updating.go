// Package update declares a risk of updating to a later major or
// minor version when an update is currently in progress.
package updating

import (
	"context"
	"fmt"

	"github.com/blang/semver/v4"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/client-go/tools/cache"
	"k8s.io/klog/v2"

	configv1 "github.com/openshift/api/config/v1"
	configinformersv1 "github.com/openshift/client-go/config/informers/externalversions/config/v1"
	configlistersv1 "github.com/openshift/client-go/config/listers/config/v1"

	"github.com/openshift/cluster-version-operator/lib/resourcemerge"
	"github.com/openshift/cluster-version-operator/pkg/risk"
	"github.com/openshift/cluster-version-operator/pkg/risk/upgradeable"
)

type updating struct {
	name        string
	cvName      string
	cvLister    configlistersv1.ClusterVersionLister
	wasUpdating *bool
}

// New returns a new updating-risk source, tracking whether an update is in progress.
func New(name string, cvName string, cvInformer configinformersv1.ClusterVersionInformer, changeCallback func()) risk.Source {
	cvLister := cvInformer.Lister()
	source := &updating{name: name, cvName: cvName, cvLister: cvLister}
	cvInformer.Informer().AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc:    func(_ interface{}) { source.eventHandler(changeCallback) },
		UpdateFunc: func(_, _ interface{}) { source.eventHandler(changeCallback) },
	})
	return source
}

// Name returns the source's name.
func (u *updating) Name() string {
	return u.name
}

// Risks returns the current set of risks the source is aware of.
func (u *updating) Risks(_ context.Context, versions []string) ([]configv1.ConditionalUpdateRisk, map[string][]string, error) {
	cv, updating, err := u.isUpdating()
	if meta.IsNoMatchError(err) || apierrors.IsNotFound(err) {
		return nil, nil, nil
	} else if err != nil {
		return nil, nil, err
	}

	u.wasUpdating = &updating
	if !updating {
		return nil, nil, nil
	}

	version, err := semver.Parse(cv.Status.Desired.Version)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to parse current target %q to SemVer: %w", cv.Status.Desired.Version, err)
	}

	risk := configv1.ConditionalUpdateRisk{
		Name:          u.name,
		Message:       fmt.Sprintf("An update is already in progress and the details are in the %s condition.", configv1.OperatorProgressing),
		URL:           fmt.Sprintf("https://docs.redhat.com/en/documentation/openshift_container_platform/%d.%d/html/updating_clusters/understanding-openshift-updates-1#understanding_clusteroperator_conditiontypes_understanding-openshift-updates", version.Major, version.Minor),
		MatchingRules: []configv1.ClusterCondition{{Type: "Always"}},
	}

	majorAndMinorUpdates := upgradeable.MajorAndMinorUpdates(version, versions)

	var versionMap map[string][]string
	if len(majorAndMinorUpdates) > 0 {
		versionMap = map[string][]string{risk.Name: majorAndMinorUpdates}
	}

	return []configv1.ConditionalUpdateRisk{risk}, versionMap, nil
}

func (u *updating) isUpdating() (*configv1.ClusterVersion, bool, error) {
	cv, err := u.cvLister.Get(u.cvName)
	if err != nil {
		return cv, false, err
	}

	updating := resourcemerge.IsOperatorStatusConditionTrue(cv.Status.Conditions, configv1.OperatorProgressing)
	return cv, updating, nil
}

func (u *updating) eventHandler(changeCallback func()) {
	if u.wasUpdating == nil {
		if changeCallback != nil {
			changeCallback()
		}
		return
	}
	_, updating, err := u.isUpdating()
	if err != nil {
		return
	}
	if updating != *u.wasUpdating {
		klog.V(2).Infof("ClusterVersion updating changed from %t to %t, triggering override callback", *u.wasUpdating, updating)
		if changeCallback != nil {
			changeCallback()
		}
	}
}

// Package overrides declares a risk of updating to the next major
// or minor release when ClusterVersion overrides are in place.
package overrides

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

	"github.com/openshift/cluster-version-operator/pkg/risk"
	"github.com/openshift/cluster-version-operator/pkg/risk/upgradeable"
)

type overrides struct {
	name         string
	cvName       string
	cvLister     configlistersv1.ClusterVersionLister
	sawOverrides *bool
}

// New returns a new update-risk source, tracking the use of ClusterVersion overrides.
func New(name string, cvName string, cvInformer configinformersv1.ClusterVersionInformer, changeCallback func()) risk.Source {
	cvLister := cvInformer.Lister()
	source := &overrides{name: name, cvName: cvName, cvLister: cvLister}
	cvInformer.Informer().AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc:    func(_ interface{}) { source.eventHandler(changeCallback) },
		UpdateFunc: func(_, _ interface{}) { source.eventHandler(changeCallback) },
	})
	return source
}

// Name returns the source's name.
func (o *overrides) Name() string {
	return o.name
}

// Risks returns the current set of risks the source is aware of.
func (o *overrides) Risks(_ context.Context, versions []string) ([]configv1.ConditionalUpdateRisk, map[string][]string, error) {
	cv, overrides, err := o.hasOverrides()
	if meta.IsNoMatchError(err) || apierrors.IsNotFound(err) {
		return nil, nil, nil
	}

	o.sawOverrides = &overrides
	if !overrides {
		return nil, nil, nil
	}

	version, err := semver.Parse(cv.Status.Desired.Version)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to parse current target %q to SemVer: %w", cv.Status.Desired.Version, err)
	}

	risk := configv1.ConditionalUpdateRisk{
		Name:          o.name,
		Message:       "Disabling ownership via cluster version overrides prevents updates between minor or major versions. Please remove overrides before requesting a minor or major version update.",
		URL:           fmt.Sprintf("https://docs.redhat.com/en/documentation/openshift_container_platform/%d.%d/html/config_apis/clusterversion-config-openshift-io-v1#spec-8", version.Major, version.Minor),
		MatchingRules: []configv1.ClusterCondition{{Type: "Always"}},
	}

	majorAndMinorUpdates := upgradeable.MajorAndMinorUpdates(version, versions)

	var versionMap map[string][]string
	if len(majorAndMinorUpdates) > 0 {
		versionMap = map[string][]string{risk.Name: majorAndMinorUpdates}
	}

	return []configv1.ConditionalUpdateRisk{risk}, versionMap, nil
}

func (o *overrides) hasOverrides() (*configv1.ClusterVersion, bool, error) {
	cv, err := o.cvLister.Get(o.cvName)
	if err != nil {
		return cv, false, err
	}

	overrides := false
	for _, o := range cv.Spec.Overrides {
		if o.Unmanaged {
			overrides = true
		}
	}
	return cv, overrides, nil
}

func (o *overrides) eventHandler(changeCallback func()) {
	if o.sawOverrides == nil {
		if changeCallback != nil {
			changeCallback()
		}
		return
	}
	_, overrides, err := o.hasOverrides()
	if err != nil {
		return
	}
	if overrides != *o.sawOverrides {
		klog.V(2).Infof("ClusterVersion overrides changed from %t to %t, triggering override callback", *o.sawOverrides, overrides)
		if changeCallback != nil {
			changeCallback()
		}
	}
}

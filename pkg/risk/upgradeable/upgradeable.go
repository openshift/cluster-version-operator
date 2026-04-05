// Package upgradeable implements an update risk source based on ClusterOperator
// Upgradeable conditions.
package upgradeable

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/blang/semver/v4"
	"github.com/google/go-cmp/cmp"

	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/client-go/tools/cache"
	"k8s.io/klog/v2"

	configv1 "github.com/openshift/api/config/v1"
	configinformersv1 "github.com/openshift/client-go/config/informers/externalversions/config/v1"
	configlistersv1 "github.com/openshift/client-go/config/listers/config/v1"

	"github.com/openshift/cluster-version-operator/lib/resourcemerge"
	"github.com/openshift/cluster-version-operator/pkg/risk"
)

type upgradeable struct {
	name           string
	currentVersion func() configv1.Release
	coLister       configlistersv1.ClusterOperatorLister
	lastSeen       []configv1.ConditionalUpdateRisk
}

// New returns a new update-risk source, tracking ClusterOperator Upgradeable conditions.
func New(name string, currentVersion func() configv1.Release, coInformer configinformersv1.ClusterOperatorInformer, changeCallback func()) risk.Source {
	coLister := coInformer.Lister()
	source := &upgradeable{name: name, currentVersion: currentVersion, coLister: coLister}
	coInformer.Informer().AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc:    func(_ interface{}) { source.eventHandler(changeCallback) },
		UpdateFunc: func(_, _ interface{}) { source.eventHandler(changeCallback) },
		DeleteFunc: func(_ interface{}) { source.eventHandler(changeCallback) },
	})
	return source
}

// Name returns the source's name.
func (u *upgradeable) Name() string {
	return u.name
}

// Risks returns the current set of risks the source is aware of.
func (u *upgradeable) Risks(_ context.Context, versions []string) ([]configv1.ConditionalUpdateRisk, map[string][]string, error) {
	risks, version, err := u.risks()
	if meta.IsNoMatchError(err) {
		u.lastSeen = nil
		return nil, nil, nil
	}

	if len(risks) == 0 {
		u.lastSeen = nil
		return nil, nil, err
	}

	u.lastSeen = risks
	majorAndMinorUpdates := MajorAndMinorUpdates(version, versions)
	if len(majorAndMinorUpdates) == 0 {
		return risks, nil, err
	}

	versionMap := make(map[string][]string, len(risks))
	for _, risk := range risks {
		versionMap[risk.Name] = majorAndMinorUpdates
	}

	return risks, versionMap, err
}

func (u *upgradeable) risks() ([]configv1.ConditionalUpdateRisk, semver.Version, error) {
	currentRelease := u.currentVersion()
	version, err := semver.Parse(currentRelease.Version)
	if err != nil {
		return []configv1.ConditionalUpdateRisk{{
			Name:          fmt.Sprintf("%sUnknown", u.name),
			Message:       fmt.Sprintf("Failed to parse cluster version to check Upgradeable conditions: %v", err),
			URL:           "https://docs.redhat.com/en/documentation/openshift_container_platform/",
			MatchingRules: []configv1.ClusterCondition{{Type: "Always"}},
		}}, version, err
	}

	ops, err := u.coLister.List(labels.Everything())
	if meta.IsNoMatchError(err) {
		return nil, version, nil
	}
	if err != nil {
		return []configv1.ConditionalUpdateRisk{{
			Name:          fmt.Sprintf("%sUnknown", u.name),
			Message:       fmt.Sprintf("Failed to retrieve ClusterOperators to check Upgradeable conditions: %v", err),
			URL:           fmt.Sprintf("https://docs.redhat.com/en/documentation/openshift_container_platform/%d.%d/html/updating_clusters/understanding-openshift-updates-1#understanding_clusteroperator_conditiontypes_understanding-openshift-updates", version.Major, version.Minor),
			MatchingRules: []configv1.ClusterCondition{{Type: "Always"}},
		}}, version, err
	}

	var risks []configv1.ConditionalUpdateRisk
	for _, op := range ops {
		if up := resourcemerge.FindOperatorStatusCondition(op.Status.Conditions, configv1.OperatorUpgradeable); up != nil && up.Status == configv1.ConditionFalse {
			risks = append(risks, configv1.ConditionalUpdateRisk{
				Name:          fmt.Sprintf("%s-%s", u.name, op.Name),
				Message:       up.Message,
				URL:           fmt.Sprintf("https://docs.redhat.com/en/documentation/openshift_container_platform/%d.%d/html/updating_clusters/understanding-openshift-updates-1#understanding_clusteroperator_conditiontypes_understanding-openshift-updates", version.Major, version.Minor),
				MatchingRules: []configv1.ClusterCondition{{Type: "Always"}},
			})
		}
	}

	slices.SortFunc(risks, func(a, b configv1.ConditionalUpdateRisk) int {
		return strings.Compare(a.Name, b.Name)
	})

	return risks, version, nil
}

func (u *upgradeable) eventHandler(changeCallback func()) {
	risks, _, err := u.risks()
	if err != nil {
		if changeCallback != nil {
			changeCallback()
		}
		return
	}
	if diff := cmp.Diff(u.lastSeen, risks); diff != "" {
		klog.V(2).Infof("ClusterOperator Upgradeable changed (-old +new):\n%s", diff)
		if changeCallback != nil {
			changeCallback()
		}
	}
}

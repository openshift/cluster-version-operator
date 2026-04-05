// Package deletion implements an update risk source based on
// in-progress resource deletion.
package deletion

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/blang/semver/v4"

	configv1 "github.com/openshift/api/config/v1"

	"github.com/openshift/cluster-version-operator/lib/resourcedelete"
	"github.com/openshift/cluster-version-operator/pkg/risk"
	"github.com/openshift/cluster-version-operator/pkg/risk/upgradeable"
)

type deletion struct {
	name           string
	currentVersion func() configv1.Release
}

// New returns a new update-risk source, tracking in-progress resource deletion.
func New(name string, currentVersion func() configv1.Release) risk.Source {
	return &deletion{name: name, currentVersion: currentVersion}
}

// Name returns the source's name.
func (d *deletion) Name() string {
	return d.name
}

// Risks returns the current set of risks the source is aware of.
func (d *deletion) Risks(_ context.Context, versions []string) ([]configv1.ConditionalUpdateRisk, map[string][]string, error) {
	currentRelease := d.currentVersion()
	version, err := semver.Parse(currentRelease.Version)
	if err != nil {
		return []configv1.ConditionalUpdateRisk{{
			Name:          fmt.Sprintf("%sUnknown", d.name),
			Message:       fmt.Sprintf("Failed to parse cluster version to check resource deletion: %v", err),
			URL:           "https://docs.redhat.com/en/documentation/openshift_container_platform/",
			MatchingRules: []configv1.ClusterCondition{{Type: "Always"}},
		}}, nil, err
	}

	var risks []configv1.ConditionalUpdateRisk
	if deletes := resourcedelete.DeletesInProgress(); len(deletes) > 0 {
		sort.Strings(deletes)
		resources := strings.Join(deletes, ",")
		risks = append(risks, configv1.ConditionalUpdateRisk{
			Name:          d.name,
			Message:       fmt.Sprintf("Cluster minor or major version upgrades are not allowed while resource deletions are in progress; resources=%s", resources),
			URL:           fmt.Sprintf("https://docs.redhat.com/en/documentation/openshift_container_platform/%d.%d/html/updating_clusters/understanding-openshift-updates-1#update-manifest-application_how-updates-work", version.Major, version.Minor),
			MatchingRules: []configv1.ClusterCondition{{Type: "Always"}},
		})
	}

	if len(risks) == 0 {
		return nil, nil, nil
	}

	majorAndMinorUpdates := upgradeable.MajorAndMinorUpdates(version, versions)
	if len(majorAndMinorUpdates) == 0 {
		return risks, nil, err
	}

	versionMap := make(map[string][]string, len(risks))
	for _, risk := range risks {
		versionMap[risk.Name] = majorAndMinorUpdates
	}

	return risks, versionMap, err

}

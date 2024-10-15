package clusterversion

import (
	"context"
	"fmt"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	configv1 "github.com/openshift/api/config/v1"
	configv1listers "github.com/openshift/client-go/config/listers/config/v1"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/tools/cache"

	"github.com/openshift/cluster-version-operator/pkg/payload/precondition"
)

func TestUpgradeableRun(t *testing.T) {
	ctx := context.Background()
	ptr := func(status configv1.ConditionStatus) *configv1.ConditionStatus {
		return &status
	}

	tests := []struct {
		name             string
		upgradeable      *configv1.ConditionStatus
		completedVersion string
		currVersion      string
		desiredVersion   string
		expected         *precondition.Error
	}{
		{
			name:           "first",
			desiredVersion: "4.2",
		},
		{
			name:           "move-y, no condition",
			currVersion:    "4.1",
			desiredVersion: "4.2",
		},
		{
			name:           "move-y, with true condition",
			upgradeable:    ptr(configv1.ConditionTrue),
			currVersion:    "4.1",
			desiredVersion: "4.2",
		},
		{
			name:           "move-y, with unknown condition",
			upgradeable:    ptr(configv1.ConditionUnknown),
			currVersion:    "4.1",
			desiredVersion: "4.2",
		},
		{
			name:           "invalid current version",
			upgradeable:    ptr(configv1.ConditionFalse),
			currVersion:    "4.1",
			desiredVersion: "4.2",
			expected:       &precondition.Error{Name: "ClusterVersionUpgradeable", Message: "No Major.Minor.Patch elements found", Reason: "InvalidCurrentVersion", NonBlockingWarning: true},
		},
		{
			name:           "invalid target version",
			upgradeable:    ptr(configv1.ConditionFalse),
			currVersion:    "4.1.1",
			desiredVersion: "4.2",
			expected:       &precondition.Error{Name: "ClusterVersionUpgradeable", Message: "No Major.Minor.Patch elements found", Reason: "InvalidDesiredVersion"},
		},
		{
			name:           "move-y, with false condition",
			upgradeable:    ptr(configv1.ConditionFalse),
			currVersion:    "4.1.1",
			desiredVersion: "4.2.1",
			expected:       &precondition.Error{Name: "ClusterVersionUpgradeable", Message: "set to False", Reason: "bla"},
		},
		{
			name:           "move-z, with false condition",
			upgradeable:    ptr(configv1.ConditionFalse),
			currVersion:    "4.1.3",
			desiredVersion: "4.1.4",
		},
		{
			name: "empty target",
		},
		{
			name:           "downgrade z",
			currVersion:    "4.16.3",
			desiredVersion: "4.16.1",
		},
		{
			name:           "downgrade z with Upgradeable=False",
			upgradeable:    ptr(configv1.ConditionFalse),
			currVersion:    "4.16.3",
			desiredVersion: "4.16.1",
		},
		{
			name:           "downgrade y",
			currVersion:    "4.16.1",
			desiredVersion: "4.15.3",
		},
		{
			name:           "downgrade y with Upgradeable=False",
			upgradeable:    ptr(configv1.ConditionFalse),
			currVersion:    "4.16.1",
			desiredVersion: "4.15.3",
		},
		{
			name:           "move to y+2 directly",
			currVersion:    "4.16.1",
			desiredVersion: "4.18.3",
		},
		{
			name:           "move to y+2 directly with Upgradeable=False",
			upgradeable:    ptr(configv1.ConditionFalse),
			currVersion:    "4.16.1",
			desiredVersion: "4.18.3",
			expected:       &precondition.Error{Name: "ClusterVersionUpgradeable", Message: "set to False", Reason: "bla"},
		},
		{
			name:           "cluster-bot build to release candidate",
			currVersion:    "4.17.0-0.test-2024-10-07-173400-ci-ln-pwn9ngk-latest",
			desiredVersion: "4.17.0-rc.6",
		},
		{
			name:           "cluster-bot build to release candidate with Upgradeable=False",
			upgradeable:    ptr(configv1.ConditionFalse),
			currVersion:    "4.17.0-0.test-2024-10-07-173400-ci-ln-pwn9ngk-latest",
			desiredVersion: "4.17.0-rc.6",
		},
		{
			name:             "move-y-then-z, with false condition",
			upgradeable:      ptr(configv1.ConditionFalse),
			completedVersion: "4.1.1",
			currVersion:      "4.2.1",
			desiredVersion:   "4.2.3",
			expected: &precondition.Error{
				NonBlockingWarning: true,
				Name:               "ClusterVersionUpgradeable",
				Message:            "Retarget to 4.2.3 while a minor level upgrade from 4.1.1 to 4.2.3 is in progress",
				Reason:             "MinorVersionClusterUpgradeInProgress",
			},
		},
		{
			name:             "move-z-then-z, with false condition",
			upgradeable:      ptr(configv1.ConditionFalse),
			completedVersion: "4.2.1",
			currVersion:      "4.2.2",
			desiredVersion:   "4.2.3",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			clusterVersion := &configv1.ClusterVersion{
				ObjectMeta: metav1.ObjectMeta{Name: "version"},
				Spec:       configv1.ClusterVersionSpec{},
				Status: configv1.ClusterVersionStatus{
					Desired: configv1.Release{Version: tc.currVersion},
					History: []configv1.UpdateHistory{{State: configv1.CompletedUpdate, Version: tc.completedVersion}},
				},
			}
			if tc.completedVersion != "" && tc.completedVersion != tc.currVersion {
				clusterVersion.Status.Conditions = append(clusterVersion.Status.Conditions,
					configv1.ClusterOperatorStatusCondition{Type: configv1.OperatorProgressing, Status: configv1.ConditionTrue})
			}
			if tc.upgradeable != nil {
				clusterVersion.Status.Conditions = append(clusterVersion.Status.Conditions, configv1.ClusterOperatorStatusCondition{
					Type:    configv1.OperatorUpgradeable,
					Status:  *tc.upgradeable,
					Reason:  "bla",
					Message: fmt.Sprintf("set to %v", *tc.upgradeable),
				})
			}
			cvLister := fakeClusterVersionLister(t, clusterVersion)
			instance := NewUpgradeable(cvLister)

			err := instance.Run(ctx, precondition.ReleaseContext{
				DesiredVersion: tc.desiredVersion,
			})

			if (tc.expected == nil && err != nil) || (tc.expected != nil && err == nil) {
				t.Errorf("%s: expected %v but got %v", tc.name, tc.expected, err)
			} else if tc.expected != nil && err != nil {
				if pError, ok := err.(*precondition.Error); !ok {
					t.Errorf("Failed to convert to err: %v", err)
				} else if diff := cmp.Diff(tc.expected, pError, cmpopts.IgnoreFields(precondition.Error{}, "Nested")); diff != "" {
					t.Errorf("%s differs from expected:\n%s", tc.name, diff)
				}
			}
		})
	}
}

func fakeClusterVersionLister(t *testing.T, clusterVersion *configv1.ClusterVersion) configv1listers.ClusterVersionLister {
	indexer := cache.NewIndexer(cache.MetaNamespaceKeyFunc, cache.Indexers{})
	if clusterVersion == nil {
		return configv1listers.NewClusterVersionLister(indexer)
	}

	err := indexer.Add(clusterVersion)
	if err != nil {
		t.Fatal(err)
	}
	return configv1listers.NewClusterVersionLister(indexer)
}

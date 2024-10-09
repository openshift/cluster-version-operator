package clusterversion

import (
	"context"
	"fmt"
	"testing"

	"github.com/google/go-cmp/cmp"
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
		name               string
		upgradeable        *configv1.ConditionStatus
		currVersion        string
		desiredVersion     string
		NonBlockingWarning bool
		expectedReason     string
		expected           string
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
			name:               "invalid current version",
			upgradeable:        ptr(configv1.ConditionFalse),
			currVersion:        "4.1",
			desiredVersion:     "4.2",
			NonBlockingWarning: true,
			expectedReason:     "InvalidCurrentVersion",
			expected:           "No Major.Minor.Patch elements found",
		},
		{
			name:           "invalid target version",
			upgradeable:    ptr(configv1.ConditionFalse),
			currVersion:    "4.1.1",
			desiredVersion: "4.2",
			expectedReason: "InvalidDesiredVersion",
			expected:       "No Major.Minor.Patch elements found",
		},
		{
			name:           "move-y, with false condition",
			upgradeable:    ptr(configv1.ConditionFalse),
			currVersion:    "4.1.1",
			desiredVersion: "4.2.1",
			expected:       "set to False",
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
			upgradeable:    ptr(configv1.ConditionFalse),
			currVersion:    "4.16.3",
			desiredVersion: "4.16.1",
		},
		{
			name:           "downgrade y",
			upgradeable:    ptr(configv1.ConditionFalse),
			currVersion:    "4.16.1",
			desiredVersion: "4.15.3",
			expected:       "set to False",
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
			expected:       "set to False",
		},
		{
			name:           "cluster-bot build to release candidate",
			upgradeable:    ptr(configv1.ConditionFalse),
			currVersion:    "4.17.0-0.test-2024-10-07-173400-ci-ln-pwn9ngk-latest",
			desiredVersion: "4.17.0-rc.6",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			clusterVersion := &configv1.ClusterVersion{
				ObjectMeta: metav1.ObjectMeta{Name: "version"},
				Spec:       configv1.ClusterVersionSpec{},
				Status: configv1.ClusterVersionStatus{
					Desired: configv1.Release{Version: tc.currVersion},
				},
			}
			if tc.upgradeable != nil {
				clusterVersion.Status.Conditions = append(clusterVersion.Status.Conditions, configv1.ClusterOperatorStatusCondition{
					Type:    configv1.OperatorUpgradeable,
					Status:  *tc.upgradeable,
					Message: fmt.Sprintf("set to %v", *tc.upgradeable),
				})
			}
			cvLister := fakeClusterVersionLister(t, clusterVersion)
			instance := NewUpgradeable(cvLister)

			err := instance.Run(ctx, precondition.ReleaseContext{
				DesiredVersion: tc.desiredVersion,
			})
			pError, ok := err.(*precondition.Error)
			if ok {
				if diff := cmp.Diff(tc.expectedReason, pError.Reason); diff != "" {
					t.Errorf("%s differs from expected:\n%s", tc.name, diff)
				}
			}
			if tc.NonBlockingWarning {
				if !ok {
					t.Errorf("Failed to convert to err: %v", err)
				} else if !pError.NonBlockingWarning {
					t.Error("NonBlockingWarning should be true")
				}
			} else if ok && pError.NonBlockingWarning {
				t.Error("NonBlockingWarning should be false")
			}
			switch {
			case err != nil && len(tc.expected) == 0:
				t.Error(err)
			case err != nil && err.Error() == tc.expected:
			case err != nil && err.Error() != tc.expected:
				t.Error(err)
			case err == nil && len(tc.expected) == 0:
			case err == nil && len(tc.expected) != 0:
				t.Error(err)
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

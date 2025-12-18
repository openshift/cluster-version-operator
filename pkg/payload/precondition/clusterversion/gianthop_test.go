package clusterversion

import (
	"context"
	"testing"

	configv1 "github.com/openshift/api/config/v1"
	"github.com/openshift/cluster-version-operator/pkg/payload/precondition"
)

func TestGiantHopRun(t *testing.T) {
	ctx := context.Background()

	tests := []struct {
		name           string
		clusterVersion configv1.ClusterVersion
		expected       string
	}{
		{
			name: "update",
			clusterVersion: configv1.ClusterVersion{
				Spec: configv1.ClusterVersionSpec{
					DesiredUpdate: &configv1.Update{
						Version: "1.0.1",
					},
				},
				Status: configv1.ClusterVersionStatus{
					Desired: configv1.Release{
						Version: "1.0.0",
					},
				},
			},
			expected: "",
		},
		{
			name: "no change",
			clusterVersion: configv1.ClusterVersion{
				Spec: configv1.ClusterVersionSpec{
					DesiredUpdate: &configv1.Update{
						Version: "1.0.0",
					},
				},
				Status: configv1.ClusterVersionStatus{
					Desired: configv1.Release{
						Version: "1.0.0",
					},
				},
			},
			expected: "",
		},
		{
			name: "major version",
			clusterVersion: configv1.ClusterVersion{
				Spec: configv1.ClusterVersionSpec{
					DesiredUpdate: &configv1.Update{
						Version: "2.0.0",
					},
				},
				Status: configv1.ClusterVersionStatus{
					Desired: configv1.Release{
						Version: "1.0.0",
					},
				},
			},
			expected: "2.0.0 has a larger major version than the current target 1.0.0 (2 > 1), and only updates within the current major version are supported.",
		},
		{
			name: "major version is 5.0",
			clusterVersion: configv1.ClusterVersion{
				Spec: configv1.ClusterVersionSpec{
					DesiredUpdate: &configv1.Update{
						Version: "5.0.0",
					},
				},
				Status: configv1.ClusterVersionStatus{
					Desired: configv1.Release{
						Version: "4.0.0",
					},
				},
			},
			expected: "",
		},
		{
			name: "major version is 5.1",
			clusterVersion: configv1.ClusterVersion{
				Spec: configv1.ClusterVersionSpec{
					DesiredUpdate: &configv1.Update{
						Version: "5.1.0",
					},
				},
				Status: configv1.ClusterVersionStatus{
					Desired: configv1.Release{
						Version: "4.0.0",
					},
				},
			},
			expected: "5.1.0 has a larger major version than the current target 4.0.0 (5 > 4), and only updates within the current major version or to 5.0 are supported.",
		},
		{
			name: "two minor versions",
			clusterVersion: configv1.ClusterVersion{
				Spec: configv1.ClusterVersionSpec{
					DesiredUpdate: &configv1.Update{
						Version: "1.2.0",
					},
				},
				Status: configv1.ClusterVersionStatus{
					Desired: configv1.Release{
						Version: "1.0.0",
					},
				},
			},
			expected: "1.2.0 is more than one minor version beyond the current target 1.0.0 (1.2 > 1.(0+1)), and only updates within the current minor version or to the next minor version are supported.",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			tc.clusterVersion.ObjectMeta.Name = "version"
			cvLister := fakeClusterVersionLister(t, &tc.clusterVersion)
			instance := NewGiantHop(cvLister)

			err := instance.Run(ctx, precondition.ReleaseContext{
				DesiredVersion: tc.clusterVersion.Spec.DesiredUpdate.Version,
			})
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

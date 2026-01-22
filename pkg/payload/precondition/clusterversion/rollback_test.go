package clusterversion

import (
	"context"
	"testing"

	configv1 "github.com/openshift/api/config/v1"
	"github.com/openshift/cluster-version-operator/pkg/payload/precondition"
)

func TestRollbackRun(t *testing.T) {
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
			name: "rollback no history",
			clusterVersion: configv1.ClusterVersion{
				Spec: configv1.ClusterVersionSpec{
					DesiredUpdate: &configv1.Update{
						Version: "1.0.0",
					},
				},
				Status: configv1.ClusterVersionStatus{
					Desired: configv1.Release{
						Version: "1.0.1",
					},
				},
			},
			expected: "1.0.0 is less than the current target 1.0.1, and the cluster has no previous Semantic Version",
		},
		{
			name: "rollback to previous in the same minor version",
			clusterVersion: configv1.ClusterVersion{
				Spec: configv1.ClusterVersionSpec{
					DesiredUpdate: &configv1.Update{
						Version: "1.0.0",
					},
				},
				Status: configv1.ClusterVersionStatus{
					Desired: configv1.Release{
						Version: "1.0.1",
					},
					History: []configv1.UpdateHistory{
						{
							State:   configv1.CompletedUpdate,
							Version: "1.0.1",
						},
						{
							State:   configv1.CompletedUpdate,
							Version: "1.0.0",
						},
					},
				},
			},
			expected: "",
		},
		{
			name: "rollback to previous completed with intermediate partial",
			clusterVersion: configv1.ClusterVersion{
				Spec: configv1.ClusterVersionSpec{
					DesiredUpdate: &configv1.Update{
						Version: "1.0.0",
					},
				},
				Status: configv1.ClusterVersionStatus{
					Desired: configv1.Release{
						Version: "1.0.2",
						Image:   "example.com/image:1.0.2",
					},
					History: []configv1.UpdateHistory{
						{
							State:   configv1.PartialUpdate,
							Version: "1.0.2",
							Image:   "example.com/image:1.0.2",
						},
						{
							State:   configv1.PartialUpdate,
							Version: "1.0.1",
							Image:   "example.com/image:1.0.1",
						},
						{
							State:   configv1.CompletedUpdate,
							Version: "1.0.0",
							Image:   "example.com/image:1.0.0",
						},
					},
				},
			},
			expected: "1.0.0 is less than the current target 1.0.2, and the only supported rollback is to the cluster's previous version 1.0.1 (example.com/image:1.0.1)",
		},
		{
			name: "rollback to previous completed with intermediate image change",
			clusterVersion: configv1.ClusterVersion{
				Spec: configv1.ClusterVersionSpec{
					DesiredUpdate: &configv1.Update{
						Version: "1.0.0",
					},
				},
				Status: configv1.ClusterVersionStatus{
					Desired: configv1.Release{
						Version: "1.0.1",
						Image:   "example.com/image:multi-arch",
					},
					History: []configv1.UpdateHistory{
						{
							State:   configv1.CompletedUpdate,
							Version: "1.0.1",
							Image:   "example.com/image:multi-arch",
						},
						{
							State:   configv1.CompletedUpdate,
							Version: "1.0.1",
							Image:   "example.com/image:single-arch",
						},
						{
							State:   configv1.CompletedUpdate,
							Version: "1.0.0",
						},
					},
				},
			},
			expected: "1.0.0 is less than the current target 1.0.1, and the only supported rollback is to the cluster's previous version 1.0.1 (example.com/image:single-arch)",
		},
		{
			name: "rollback to previous in an earlier minor release",
			clusterVersion: configv1.ClusterVersion{
				Spec: configv1.ClusterVersionSpec{
					DesiredUpdate: &configv1.Update{
						Version: "1.0.0",
					},
				},
				Status: configv1.ClusterVersionStatus{
					Desired: configv1.Release{
						Version: "2.0.0",
					},
					History: []configv1.UpdateHistory{
						{
							State:   configv1.CompletedUpdate,
							Version: "2.0.0",
						},
						{
							State:   configv1.CompletedUpdate,
							Version: "1.0.0",
						},
					},
				},
			},
			expected: "1.0.0 is less than the current target 2.0.0 and matches the cluster's previous version, but rollbacks that change major or minor versions are not recommended",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			tc.clusterVersion.Name = "version"
			cvLister := fakeClusterVersionLister(t, &tc.clusterVersion)
			instance := NewRollback(cvLister)

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

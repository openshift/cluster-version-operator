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
		currVersion    string
		desiredVersion string
		expected       string
	}{
		{
			name:           "update",
			currVersion:    "1.0.0",
			desiredVersion: "1.0.1",
			expected:       "",
		},
		{
			name:           "no change",
			currVersion:    "1.0.0",
			desiredVersion: "1.0.0",
			expected:       "",
		},
		{
			name:           "rollback",
			currVersion:    "1.0.1",
			desiredVersion: "1.0.0",
			expected:       "1.0.0 is less than the current target 1.0.1, but rollbacks and downgrades are not recommended",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			instance := NewRollback(func() configv1.Release {
				return configv1.Release{
					Version: tc.currVersion,
				}
			})

			err := instance.Run(ctx, precondition.ReleaseContext{
				DesiredVersion: tc.desiredVersion,
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

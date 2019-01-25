package cvo

import (
	"testing"

	configv1 "github.com/openshift/api/config/v1"
)

func Test_mergeEqualVersions(t *testing.T) {
	type args struct {
	}
	tests := []struct {
		name    string
		current *configv1.UpdateHistory
		desired configv1.Update
		want    bool
	}{
		{
			current: &configv1.UpdateHistory{Image: "test:1", Version: "0.0.1"},
			desired: configv1.Update{Image: "test:1", Version: "0.0.1"},
			want:    true,
		},
		{
			current: &configv1.UpdateHistory{Image: "test:1", Version: ""},
			desired: configv1.Update{Image: "test:1", Version: "0.0.1"},
			want:    true,
		},
		{
			current: &configv1.UpdateHistory{Image: "test:1", Version: "0.0.1"},
			desired: configv1.Update{Image: "test:1", Version: ""},
			want:    false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := mergeEqualVersions(tt.current, tt.desired); got != tt.want {
				t.Errorf("%v != %v", tt.want, got)
			}
		})
	}
}

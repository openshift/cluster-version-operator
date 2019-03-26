package cvo

import (
	"reflect"
	"testing"

	configv1 "github.com/openshift/api/config/v1"
	"k8s.io/apimachinery/pkg/util/diff"
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
		{
			current: &configv1.UpdateHistory{Image: "test:1", Version: "0.0.1"},
			desired: configv1.Update{Image: "", Version: "0.0.1"},
			want:    false,
		},
		{
			current: &configv1.UpdateHistory{Image: "test:1", Version: "0.0.1"},
			desired: configv1.Update{Image: "test:2", Version: "0.0.1"},
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

func Test_pruneStatusHistory(t *testing.T) {
	obj := &configv1.ClusterVersion{
		Status: configv1.ClusterVersionStatus{
			History: []configv1.UpdateHistory{
				{State: configv1.PartialUpdate, Version: "0.0.10"},
				{State: configv1.PartialUpdate, Version: "0.0.9"},
				{State: configv1.PartialUpdate, Version: "0.0.8"},
				{State: configv1.CompletedUpdate, Version: "0.0.7"},
				{State: configv1.PartialUpdate, Version: "0.0.6"},
			},
		},
	}
	tests := []struct {
		name       string
		config     *configv1.ClusterVersion
		maxHistory int
		want       []configv1.UpdateHistory
	}{
		{
			config:     obj.DeepCopy(),
			maxHistory: 2,
			want: []configv1.UpdateHistory{
				{State: configv1.PartialUpdate, Version: "0.0.10"},
				{State: configv1.CompletedUpdate, Version: "0.0.7"},
			},
		},
		{
			config:     obj.DeepCopy(),
			maxHistory: 3,
			want: []configv1.UpdateHistory{
				{State: configv1.PartialUpdate, Version: "0.0.10"},
				{State: configv1.PartialUpdate, Version: "0.0.9"},
				{State: configv1.CompletedUpdate, Version: "0.0.7"},
			},
		},
		{
			config:     obj.DeepCopy(),
			maxHistory: 4,
			want: []configv1.UpdateHistory{
				{State: configv1.PartialUpdate, Version: "0.0.10"},
				{State: configv1.PartialUpdate, Version: "0.0.9"},
				{State: configv1.PartialUpdate, Version: "0.0.8"},
				{State: configv1.CompletedUpdate, Version: "0.0.7"},
			},
		},
		{
			config:     obj.DeepCopy(),
			maxHistory: 5,
			want: []configv1.UpdateHistory{
				{State: configv1.PartialUpdate, Version: "0.0.10"},
				{State: configv1.PartialUpdate, Version: "0.0.9"},
				{State: configv1.PartialUpdate, Version: "0.0.8"},
				{State: configv1.CompletedUpdate, Version: "0.0.7"},
				{State: configv1.PartialUpdate, Version: "0.0.6"},
			},
		},
		{
			config:     obj.DeepCopy(),
			maxHistory: 6,
			want: []configv1.UpdateHistory{
				{State: configv1.PartialUpdate, Version: "0.0.10"},
				{State: configv1.PartialUpdate, Version: "0.0.9"},
				{State: configv1.PartialUpdate, Version: "0.0.8"},
				{State: configv1.CompletedUpdate, Version: "0.0.7"},
				{State: configv1.PartialUpdate, Version: "0.0.6"},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			config := tt.config.DeepCopy()
			pruneStatusHistory(config, tt.maxHistory)
			if !reflect.DeepEqual(tt.want, config.Status.History) {
				t.Fatalf("%s", diff.ObjectReflectDiff(tt.want, config.Status.History))
			}
		})
	}
}

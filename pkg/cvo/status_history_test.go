package cvo

import (
	"reflect"
	"testing"

	"k8s.io/apimachinery/pkg/util/diff"
	"k8s.io/klog/v2"

	configv1 "github.com/openshift/api/config/v1"
)

func Test_prune(t *testing.T) {
	tests := []struct {
		name       string
		history    []configv1.UpdateHistory
		maxHistory int
		want       []configv1.UpdateHistory
	}{
		{
			name: "partial update within a minor transition",
			history: []configv1.UpdateHistory{
				{State: configv1.CompletedUpdate, Version: "4.6.3"},
				{State: configv1.CompletedUpdate, Version: "4.5.3"},
				{State: configv1.CompletedUpdate, Version: "4.4.3"},
				{State: configv1.CompletedUpdate, Version: "4.3.3"},
				{State: configv1.CompletedUpdate, Version: "4.3.2"},
				{State: configv1.CompletedUpdate, Version: "4.3.1"},
				{State: configv1.CompletedUpdate, Version: "4.2.1"},
				{State: configv1.PartialUpdate, Version: "4.1.4"},
				{State: configv1.PartialUpdate, Version: "4.1.3"},
				{State: configv1.PartialUpdate, Version: "4.1.2"},
				{State: configv1.CompletedUpdate, Version: "4.1.1"},
			},
			maxHistory: 10,
			want: []configv1.UpdateHistory{
				{State: configv1.CompletedUpdate, Version: "4.6.3"},
				{State: configv1.CompletedUpdate, Version: "4.5.3"},
				{State: configv1.CompletedUpdate, Version: "4.4.3"},
				{State: configv1.CompletedUpdate, Version: "4.3.3"},
				{State: configv1.CompletedUpdate, Version: "4.3.2"},
				{State: configv1.CompletedUpdate, Version: "4.3.1"},
				{State: configv1.CompletedUpdate, Version: "4.2.1"},
				{State: configv1.PartialUpdate, Version: "4.1.4"},
				{State: configv1.PartialUpdate, Version: "4.1.3"},
				{State: configv1.CompletedUpdate, Version: "4.1.1"},
			},
		},
		{
			name: "partial update within a z-stream transition",
			history: []configv1.UpdateHistory{
				{State: configv1.CompletedUpdate, Version: "4.5.3"},
				{State: configv1.CompletedUpdate, Version: "4.4.3"},
				{State: configv1.CompletedUpdate, Version: "4.4.3"},
				{State: configv1.CompletedUpdate, Version: "4.4.2"},
				{State: configv1.CompletedUpdate, Version: "4.1.10"},
				{State: configv1.CompletedUpdate, Version: "4.1.9"},
				{State: configv1.CompletedUpdate, Version: "4.1.4"},
				{State: configv1.PartialUpdate, Version: "4.1.3"},
				{State: configv1.CompletedUpdate, Version: "4.1.2"},
				{State: configv1.PartialUpdate, Version: "4.0.1"},
				{State: configv1.CompletedUpdate, Version: "4.0.1"},
			},
			maxHistory: 10,
			want: []configv1.UpdateHistory{
				{State: configv1.CompletedUpdate, Version: "4.5.3"},
				{State: configv1.CompletedUpdate, Version: "4.4.3"},
				{State: configv1.CompletedUpdate, Version: "4.4.3"},
				{State: configv1.CompletedUpdate, Version: "4.4.2"},
				{State: configv1.CompletedUpdate, Version: "4.1.10"},
				{State: configv1.CompletedUpdate, Version: "4.1.9"},
				{State: configv1.CompletedUpdate, Version: "4.1.4"},
				{State: configv1.CompletedUpdate, Version: "4.1.2"},
				{State: configv1.PartialUpdate, Version: "4.0.1"},
				{State: configv1.CompletedUpdate, Version: "4.0.1"},
			},
		},
		{
			name: "prune oldest not in mostImportantWeight set",
			history: []configv1.UpdateHistory{
				{State: configv1.CompletedUpdate, Version: "4.11.0"},
				{State: configv1.CompletedUpdate, Version: "4.10.0"},
				{State: configv1.CompletedUpdate, Version: "4.9.0"},
				{State: configv1.CompletedUpdate, Version: "4.8.0"},
				{State: configv1.CompletedUpdate, Version: "4.7.0"},
				{State: configv1.CompletedUpdate, Version: "4.6.0"},
				{State: configv1.CompletedUpdate, Version: "4.5.0"},
				{State: configv1.CompletedUpdate, Version: "4.4.0"},
				{State: configv1.CompletedUpdate, Version: "4.3.0"},
				{State: configv1.CompletedUpdate, Version: "4.2.0"},
				{State: configv1.CompletedUpdate, Version: "4.1.0"},
			},
			maxHistory: 10,
			want: []configv1.UpdateHistory{
				{State: configv1.CompletedUpdate, Version: "4.11.0"},
				{State: configv1.CompletedUpdate, Version: "4.10.0"},
				{State: configv1.CompletedUpdate, Version: "4.9.0"},
				{State: configv1.CompletedUpdate, Version: "4.8.0"},
				{State: configv1.CompletedUpdate, Version: "4.7.0"},
				{State: configv1.CompletedUpdate, Version: "4.6.0"},
				{State: configv1.CompletedUpdate, Version: "4.5.0"},
				{State: configv1.CompletedUpdate, Version: "4.4.0"},
				{State: configv1.CompletedUpdate, Version: "4.3.0"},
				{State: configv1.CompletedUpdate, Version: "4.1.0"},
			},
		},
		{
			name: "prune only partial not in mostImportantWeight set",
			history: []configv1.UpdateHistory{
				{State: configv1.CompletedUpdate, Version: "4.11.0"},
				{State: configv1.PartialUpdate, Version: "4.10.0"},
				{State: configv1.CompletedUpdate, Version: "4.9.0"},
				{State: configv1.CompletedUpdate, Version: "4.8.0"},
				{State: configv1.CompletedUpdate, Version: "4.7.0"},
				{State: configv1.PartialUpdate, Version: "4.6.0"},
				{State: configv1.CompletedUpdate, Version: "4.5.0"},
				{State: configv1.CompletedUpdate, Version: "4.4.0"},
				{State: configv1.CompletedUpdate, Version: "4.3.0"},
				{State: configv1.CompletedUpdate, Version: "4.2.0"},
				{State: configv1.CompletedUpdate, Version: "4.1.0"},
			},
			maxHistory: 10,
			want: []configv1.UpdateHistory{
				{State: configv1.CompletedUpdate, Version: "4.11.0"},
				{State: configv1.PartialUpdate, Version: "4.10.0"},
				{State: configv1.CompletedUpdate, Version: "4.9.0"},
				{State: configv1.CompletedUpdate, Version: "4.8.0"},
				{State: configv1.CompletedUpdate, Version: "4.7.0"},
				{State: configv1.CompletedUpdate, Version: "4.5.0"},
				{State: configv1.CompletedUpdate, Version: "4.4.0"},
				{State: configv1.CompletedUpdate, Version: "4.3.0"},
				{State: configv1.CompletedUpdate, Version: "4.2.0"},
				{State: configv1.CompletedUpdate, Version: "4.1.0"},
			},
		},
		{
			name: "prune the z-stream partial over the minor partial",
			history: []configv1.UpdateHistory{
				{State: configv1.CompletedUpdate, Version: "4.11.0"},
				{State: configv1.CompletedUpdate, Version: "4.10.0"},
				{State: configv1.CompletedUpdate, Version: "4.9.0"},
				{State: configv1.CompletedUpdate, Version: "4.8.0"},
				{State: configv1.CompletedUpdate, Version: "4.6.1"},
				{State: configv1.PartialUpdate, Version: "4.6.1"},
				{State: configv1.CompletedUpdate, Version: "4.6.0"},
				{State: configv1.CompletedUpdate, Version: "4.4.0"},
				{State: configv1.PartialUpdate, Version: "4.3.0"},
				{State: configv1.CompletedUpdate, Version: "4.2.0"},
				{State: configv1.CompletedUpdate, Version: "4.1.0"},
			},
			maxHistory: 10,
			want: []configv1.UpdateHistory{
				{State: configv1.CompletedUpdate, Version: "4.11.0"},
				{State: configv1.CompletedUpdate, Version: "4.10.0"},
				{State: configv1.CompletedUpdate, Version: "4.9.0"},
				{State: configv1.CompletedUpdate, Version: "4.8.0"},
				{State: configv1.CompletedUpdate, Version: "4.6.1"},
				{State: configv1.CompletedUpdate, Version: "4.6.0"},
				{State: configv1.CompletedUpdate, Version: "4.4.0"},
				{State: configv1.PartialUpdate, Version: "4.3.0"},
				{State: configv1.CompletedUpdate, Version: "4.2.0"},
				{State: configv1.CompletedUpdate, Version: "4.1.0"},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := prune(tt.history, tt.maxHistory)
			if klog.V(2).Enabled() {
				dumpHistory(t, h)
			}
			if !reflect.DeepEqual(tt.want, h) {
				t.Fatalf("%s", diff.ObjectReflectDiff(tt.want, h))
			}
		})
	}
}

func dumpHistory(t *testing.T, h []configv1.UpdateHistory) {
	for i, entry := range h {
		t.Logf("%d: %s\t%s", i, entry.State, entry.Version)
	}
}

func Test_isTheInitialEntry(t *testing.T) {
	tests := []struct {
		name       string
		history    []configv1.UpdateHistory
		entryIdx   int
		maxHistory int
		want       bool
	}{
		{
			name: "is the initial entry",
			history: []configv1.UpdateHistory{
				{State: configv1.PartialUpdate, Version: "0.0.10"},
				{State: configv1.PartialUpdate, Version: "0.0.9"},
				{State: configv1.PartialUpdate, Version: "0.0.8"},
				{State: configv1.CompletedUpdate, Version: "0.0.7"},
				{State: configv1.PartialUpdate, Version: "0.0.6"},
			},
			entryIdx:   4,
			maxHistory: 4,
			want:       true,
		},
		{
			name: "is not the initial entry",
			history: []configv1.UpdateHistory{
				{State: configv1.PartialUpdate, Version: "0.0.10"},
				{State: configv1.PartialUpdate, Version: "0.0.9"},
				{State: configv1.PartialUpdate, Version: "0.0.8"},
				{State: configv1.CompletedUpdate, Version: "0.0.7"},
				{State: configv1.PartialUpdate, Version: "0.0.6"},
			},
			entryIdx:   3,
			maxHistory: 4,
			want:       false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			isIt := isTheInitialEntry(tt.entryIdx, tt.maxHistory)
			if !reflect.DeepEqual(tt.want, isIt) {
				t.Fatalf("%s", diff.ObjectReflectDiff(tt.want, isIt))
			}
		})
	}
}

func Test_isAFinalEntry(t *testing.T) {
	tests := []struct {
		name     string
		history  []configv1.UpdateHistory
		entryIdx int
		want     bool
	}{
		{
			name: "is a final entry",
			history: []configv1.UpdateHistory{
				{State: configv1.PartialUpdate, Version: "0.0.10"},
				{State: configv1.PartialUpdate, Version: "0.0.9"},
				{State: configv1.PartialUpdate, Version: "0.0.8"},
				{State: configv1.CompletedUpdate, Version: "0.0.7"},
				{State: configv1.PartialUpdate, Version: "0.0.6"},
				{State: configv1.PartialUpdate, Version: "0.0.5"},
			},
			entryIdx: 4,
			want:     true,
		},
		{
			name: "is not a final entry",
			history: []configv1.UpdateHistory{
				{State: configv1.PartialUpdate, Version: "0.0.10"},
				{State: configv1.PartialUpdate, Version: "0.0.9"},
				{State: configv1.PartialUpdate, Version: "0.0.8"},
				{State: configv1.CompletedUpdate, Version: "0.0.7"},
				{State: configv1.PartialUpdate, Version: "0.0.6"},
				{State: configv1.PartialUpdate, Version: "0.0.5"},
			},
			entryIdx: 5,
			want:     false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			isIt := isAFinalEntry(tt.entryIdx)
			if !reflect.DeepEqual(tt.want, isIt) {
				t.Fatalf("%s", diff.ObjectReflectDiff(tt.want, isIt))
			}
		})
	}
}

func Test_isTheMostRecentCompletedEntry(t *testing.T) {
	tests := []struct {
		name     string
		history  []configv1.UpdateHistory
		entryIdx int
		want     bool
	}{
		{
			name: "is the most recent completed entry",
			history: []configv1.UpdateHistory{
				{State: configv1.PartialUpdate, Version: "0.0.10"},
				{State: configv1.CompletedUpdate, Version: "0.0.9"},
				{State: configv1.PartialUpdate, Version: "0.0.8"},
				{State: configv1.CompletedUpdate, Version: "0.0.7"},
				{State: configv1.PartialUpdate, Version: "0.0.6"},
				{State: configv1.PartialUpdate, Version: "0.0.5"},
			},
			entryIdx: 1,
			want:     true,
		},
		{
			name: "is not the most recent completed entry",
			history: []configv1.UpdateHistory{
				{State: configv1.PartialUpdate, Version: "0.0.10"},
				{State: configv1.CompletedUpdate, Version: "0.0.9"},
				{State: configv1.PartialUpdate, Version: "0.0.8"},
				{State: configv1.CompletedUpdate, Version: "0.0.7"},
				{State: configv1.PartialUpdate, Version: "0.0.6"},
				{State: configv1.PartialUpdate, Version: "0.0.5"},
			},
			entryIdx: 3,
			want:     false,
		},
		{
			name: "is not the most recent completed entry because partial",
			history: []configv1.UpdateHistory{
				{State: configv1.PartialUpdate, Version: "0.0.10"},
				{State: configv1.CompletedUpdate, Version: "0.0.9"},
				{State: configv1.PartialUpdate, Version: "0.0.8"},
				{State: configv1.CompletedUpdate, Version: "0.0.7"},
				{State: configv1.PartialUpdate, Version: "0.0.6"},
				{State: configv1.PartialUpdate, Version: "0.0.5"},
			},
			entryIdx: 2,
			want:     false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mostRecentIdx := getTheMostRecentCompletedEntryIndex(tt.history)
			isIt := isTheMostRecentCompletedEntry(tt.entryIdx, mostRecentIdx)
			if !reflect.DeepEqual(tt.want, isIt) {
				t.Fatalf("%s", diff.ObjectReflectDiff(tt.want, isIt))
			}
		})
	}
}

func Test_isTheFirstOrLastCompletedInAMinor(t *testing.T) {
	tests := []struct {
		name       string
		history    []configv1.UpdateHistory
		entryIdx   int
		maxHistory int
		want       bool
	}{
		{
			name: "is the first completed in a minor",
			history: []configv1.UpdateHistory{
				{State: configv1.CompletedUpdate, Version: "4.3.3"},
				{State: configv1.CompletedUpdate, Version: "4.3.2"},
				{State: configv1.CompletedUpdate, Version: "4.3.1"},
				{State: configv1.CompletedUpdate, Version: "4.2.1"},
				{State: configv1.PartialUpdate, Version: "4.2.1"},
				{State: configv1.PartialUpdate, Version: "4.2.1"},
				{State: configv1.PartialUpdate, Version: "4.2.1"},
				{State: configv1.CompletedUpdate, Version: "4.1.1"},
			},
			entryIdx:   3,
			maxHistory: 7,
			want:       true,
		},
		{
			name: "is the last completed in a minor",
			history: []configv1.UpdateHistory{
				{State: configv1.PartialUpdate, Version: "4.3.3"},
				{State: configv1.CompletedUpdate, Version: "4.3.2"},
				{State: configv1.CompletedUpdate, Version: "4.3.1"},
				{State: configv1.CompletedUpdate, Version: "4.2.1"},
				{State: configv1.PartialUpdate, Version: "4.2.1"},
				{State: configv1.PartialUpdate, Version: "4.2.1"},
				{State: configv1.PartialUpdate, Version: "4.2.1"},
				{State: configv1.CompletedUpdate, Version: "4.1.1"},
			},
			entryIdx:   1,
			maxHistory: 7,
			want:       true,
		},
		{
			name: "is the first completed in a minor because it is the first",
			history: []configv1.UpdateHistory{
				{State: configv1.CompletedUpdate, Version: "4.3.3"},
				{State: configv1.CompletedUpdate, Version: "4.3.2"},
				{State: configv1.CompletedUpdate, Version: "4.3.1"},
				{State: configv1.CompletedUpdate, Version: "4.2.1"},
				{State: configv1.PartialUpdate, Version: "4.2.1"},
				{State: configv1.PartialUpdate, Version: "4.2.1"},
				{State: configv1.PartialUpdate, Version: "4.2.1"},
				{State: configv1.CompletedUpdate, Version: "4.1.1"},
			},
			entryIdx:   7,
			maxHistory: 7,
			want:       true,
		},
		{
			name: "is the last completed in a minor because it is the last",
			history: []configv1.UpdateHistory{
				{State: configv1.CompletedUpdate, Version: "4.3.3"},
				{State: configv1.CompletedUpdate, Version: "4.3.2"},
				{State: configv1.CompletedUpdate, Version: "4.3.1"},
				{State: configv1.CompletedUpdate, Version: "4.2.1"},
				{State: configv1.PartialUpdate, Version: "4.2.1"},
				{State: configv1.PartialUpdate, Version: "4.2.1"},
				{State: configv1.PartialUpdate, Version: "4.2.1"},
				{State: configv1.CompletedUpdate, Version: "4.1.1"},
			},
			entryIdx:   0,
			maxHistory: 7,
			want:       true,
		},
		{
			name: "is not the first or the last completed in a minor, partial",
			history: []configv1.UpdateHistory{
				{State: configv1.CompletedUpdate, Version: "4.3.3"},
				{State: configv1.CompletedUpdate, Version: "4.3.2"},
				{State: configv1.CompletedUpdate, Version: "4.3.1"},
				{State: configv1.CompletedUpdate, Version: "4.2.1"},
				{State: configv1.PartialUpdate, Version: "4.2.1"},
				{State: configv1.PartialUpdate, Version: "4.2.1"},
				{State: configv1.PartialUpdate, Version: "4.2.1"},
				{State: configv1.CompletedUpdate, Version: "4.1.1"},
			},
			entryIdx:   4,
			maxHistory: 7,
			want:       false,
		},
		{
			name: "is not the first or the last completed in a minor",
			history: []configv1.UpdateHistory{
				{State: configv1.CompletedUpdate, Version: "4.3.3"},
				{State: configv1.CompletedUpdate, Version: "4.3.2"},
				{State: configv1.PartialUpdate, Version: "4.4.1"},
				{State: configv1.CompletedUpdate, Version: "4.3.1"},
				{State: configv1.CompletedUpdate, Version: "4.2.1"},
				{State: configv1.PartialUpdate, Version: "4.2.1"},
				{State: configv1.PartialUpdate, Version: "4.2.1"},
				{State: configv1.PartialUpdate, Version: "4.2.1"},
				{State: configv1.CompletedUpdate, Version: "4.1.1"},
			},
			entryIdx:   1,
			maxHistory: 8,
			want:       false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			isIt := isTheFirstOrLastCompletedInAMinor(tt.entryIdx, tt.history, tt.maxHistory)
			if !reflect.DeepEqual(tt.want, isIt) {
				t.Fatalf("%s", diff.ObjectReflectDiff(tt.want, isIt))
			}
		})
	}
}

func Test_isPartialPortionOfMinorTransition(t *testing.T) {
	tests := []struct {
		name       string
		history    []configv1.UpdateHistory
		entryIdx   int
		maxHistory int
		want       bool
	}{
		{
			name: "is the partial portion of a minor transition",
			history: []configv1.UpdateHistory{
				{State: configv1.CompletedUpdate, Version: "4.3.3"},
				{State: configv1.CompletedUpdate, Version: "4.3.2"},
				{State: configv1.CompletedUpdate, Version: "4.3.1"},
				{State: configv1.CompletedUpdate, Version: "4.2.1"},
				{State: configv1.PartialUpdate, Version: "4.2.1"},
				{State: configv1.PartialUpdate, Version: "4.2.1"},
				{State: configv1.PartialUpdate, Version: "4.2.1"},
				{State: configv1.CompletedUpdate, Version: "4.1.1"},
			},
			entryIdx:   6,
			maxHistory: 7,
			want:       true,
		},
		{
			name: "is the partial portion of a minor transition, diff target minor",
			history: []configv1.UpdateHistory{
				{State: configv1.CompletedUpdate, Version: "4.3.3"},
				{State: configv1.CompletedUpdate, Version: "4.3.2"},
				{State: configv1.CompletedUpdate, Version: "4.3.1"},
				{State: configv1.PartialUpdate, Version: "4.2.1"},
				{State: configv1.CompletedUpdate, Version: "4.1.1"},
			},
			entryIdx:   3,
			maxHistory: 5,
			want:       true,
		},
		{
			name: "is the partial portion of a minor transition, same source minor",
			history: []configv1.UpdateHistory{
				{State: configv1.CompletedUpdate, Version: "4.3.3"},
				{State: configv1.CompletedUpdate, Version: "4.3.2"},
				{State: configv1.CompletedUpdate, Version: "4.3.1"},
				{State: configv1.PartialUpdate, Version: "4.2.1"},
				{State: configv1.CompletedUpdate, Version: "4.2.1"},
			},
			entryIdx:   3,
			maxHistory: 5,
			want:       true,
		},
		{
			name: "is not the partial portion of a minor transition because it is the first",
			history: []configv1.UpdateHistory{
				{State: configv1.CompletedUpdate, Version: "4.3.3"},
				{State: configv1.CompletedUpdate, Version: "4.3.2"},
				{State: configv1.CompletedUpdate, Version: "4.3.1"},
				{State: configv1.CompletedUpdate, Version: "4.2.1"},
				{State: configv1.PartialUpdate, Version: "4.2.1"},
				{State: configv1.PartialUpdate, Version: "4.2.1"},
				{State: configv1.PartialUpdate, Version: "4.2.1"},
				{State: configv1.PartialUpdate, Version: "4.1.1"},
			},
			entryIdx:   7,
			maxHistory: 7,
			want:       false,
		},
		{
			name: "is not the partial portion of a minor transition because it is the last",
			history: []configv1.UpdateHistory{
				{State: configv1.PartialUpdate, Version: "4.3.3"},
				{State: configv1.CompletedUpdate, Version: "4.3.2"},
				{State: configv1.CompletedUpdate, Version: "4.3.1"},
				{State: configv1.CompletedUpdate, Version: "4.2.1"},
				{State: configv1.PartialUpdate, Version: "4.2.1"},
				{State: configv1.PartialUpdate, Version: "4.2.1"},
				{State: configv1.PartialUpdate, Version: "4.2.1"},
				{State: configv1.PartialUpdate, Version: "4.1.1"},
			},
			entryIdx:   0,
			maxHistory: 7,
			want:       false,
		},
		{
			name: "is not the partial portion of a minor transition because it is completed",
			history: []configv1.UpdateHistory{
				{State: configv1.CompletedUpdate, Version: "4.3.3"},
				{State: configv1.CompletedUpdate, Version: "4.3.2"},
				{State: configv1.CompletedUpdate, Version: "4.3.1"},
				{State: configv1.CompletedUpdate, Version: "4.2.1"},
				{State: configv1.PartialUpdate, Version: "4.2.1"},
				{State: configv1.PartialUpdate, Version: "4.2.1"},
				{State: configv1.PartialUpdate, Version: "4.2.1"},
				{State: configv1.CompletedUpdate, Version: "4.1.1"},
			},
			entryIdx:   3,
			maxHistory: 7,
			want:       false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			isIt := isPartialPortionOfMinorTransition(tt.entryIdx, tt.history, tt.maxHistory)
			if !reflect.DeepEqual(tt.want, isIt) {
				t.Fatalf("%s", diff.ObjectReflectDiff(tt.want, isIt))
			}
		})
	}
}

func Test_isPartialWithinAZStream(t *testing.T) {
	tests := []struct {
		name       string
		history    []configv1.UpdateHistory
		entryIdx   int
		maxHistory int
		want       bool
	}{
		{
			name: "is the partial portion of a z-stream transition",
			history: []configv1.UpdateHistory{
				{State: configv1.CompletedUpdate, Version: "4.3.3"},
				{State: configv1.CompletedUpdate, Version: "4.3.2"},
				{State: configv1.CompletedUpdate, Version: "4.3.1"},
				{State: configv1.CompletedUpdate, Version: "4.2.2"},
				{State: configv1.PartialUpdate, Version: "4.2.2"},
				{State: configv1.PartialUpdate, Version: "4.2.2"},
				{State: configv1.PartialUpdate, Version: "4.2.2"},
				{State: configv1.CompletedUpdate, Version: "4.2.1"},
			},
			entryIdx:   6,
			maxHistory: 7,
			want:       true,
		},
		{
			name: "is the partial portion of a z-stream transition, diff target micro",
			history: []configv1.UpdateHistory{
				{State: configv1.CompletedUpdate, Version: "4.3.3"},
				{State: configv1.CompletedUpdate, Version: "4.3.2"},
				{State: configv1.CompletedUpdate, Version: "4.2.3"},
				{State: configv1.PartialUpdate, Version: "4.2.2"},
				{State: configv1.CompletedUpdate, Version: "4.2.1"},
			},
			entryIdx:   3,
			maxHistory: 5,
			want:       true,
		},
		{
			name: "is the partial portion of a z-stream transition, same source micro",
			history: []configv1.UpdateHistory{
				{State: configv1.CompletedUpdate, Version: "4.3.3"},
				{State: configv1.CompletedUpdate, Version: "4.3.2"},
				{State: configv1.CompletedUpdate, Version: "4.2.2"},
				{State: configv1.PartialUpdate, Version: "4.2.1"},
				{State: configv1.CompletedUpdate, Version: "4.2.1"},
			},
			entryIdx:   3,
			maxHistory: 5,
			want:       true,
		},
		{
			name: "is not the partial portion of a z-stream transition because it is the first",
			history: []configv1.UpdateHistory{
				{State: configv1.CompletedUpdate, Version: "4.3.3"},
				{State: configv1.CompletedUpdate, Version: "4.3.2"},
				{State: configv1.CompletedUpdate, Version: "4.3.1"},
				{State: configv1.CompletedUpdate, Version: "4.2.1"},
				{State: configv1.PartialUpdate, Version: "4.2.1"},
				{State: configv1.PartialUpdate, Version: "4.2.1"},
				{State: configv1.PartialUpdate, Version: "4.2.1"},
				{State: configv1.PartialUpdate, Version: "4.1.1"},
			},
			entryIdx:   7,
			maxHistory: 7,
			want:       false,
		},
		{
			name: "is not the partial portion of a z-stream transition because it is the last",
			history: []configv1.UpdateHistory{
				{State: configv1.PartialUpdate, Version: "4.3.3"},
				{State: configv1.CompletedUpdate, Version: "4.3.2"},
				{State: configv1.CompletedUpdate, Version: "4.3.1"},
				{State: configv1.CompletedUpdate, Version: "4.2.1"},
				{State: configv1.PartialUpdate, Version: "4.2.1"},
				{State: configv1.PartialUpdate, Version: "4.2.1"},
				{State: configv1.PartialUpdate, Version: "4.2.1"},
				{State: configv1.PartialUpdate, Version: "4.1.1"},
			},
			entryIdx:   0,
			maxHistory: 7,
			want:       false,
		},
		{
			name: "is not the partial portion of a z-stream transition because it is completed",
			history: []configv1.UpdateHistory{
				{State: configv1.CompletedUpdate, Version: "4.3.3"},
				{State: configv1.CompletedUpdate, Version: "4.3.2"},
				{State: configv1.CompletedUpdate, Version: "4.3.1"},
				{State: configv1.CompletedUpdate, Version: "4.2.1"},
				{State: configv1.PartialUpdate, Version: "4.2.1"},
				{State: configv1.PartialUpdate, Version: "4.2.1"},
				{State: configv1.PartialUpdate, Version: "4.2.1"},
				{State: configv1.CompletedUpdate, Version: "4.1.1"},
			},
			entryIdx:   3,
			maxHistory: 7,
			want:       false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			isIt := isPartialWithinAZStream(tt.entryIdx, tt.history, tt.maxHistory)
			if !reflect.DeepEqual(tt.want, isIt) {
				t.Fatalf("%s", diff.ObjectReflectDiff(tt.want, isIt))
			}
		})
	}
}

func Test_sameMinorVersion(t *testing.T) {
	tests := []struct {
		name string
		h1   configv1.UpdateHistory
		h2   configv1.UpdateHistory
		want bool
	}{
		{
			name: "is same minor version",
			h1:   configv1.UpdateHistory{Version: "4.3.3"},
			h2:   configv1.UpdateHistory{Version: "4.3.2"},
			want: true,
		},
		{
			name: "is same minor version, no major",
			h1:   configv1.UpdateHistory{Version: "4.3.3"},
			h2:   configv1.UpdateHistory{Version: ".3.2"},
			want: true,
		},
		{
			name: "is same minor version, no micro",
			h1:   configv1.UpdateHistory{Version: "4.3.3"},
			h2:   configv1.UpdateHistory{Version: "4.3"},
			want: true,
		},
		{
			name: "is not same minor version",
			h1:   configv1.UpdateHistory{Version: "4.4.3"},
			h2:   configv1.UpdateHistory{Version: "4.3.2"},
			want: false,
		},
		{
			name: "is not same minor version, empty",
			h1:   configv1.UpdateHistory{Version: "4.4.3"},
			h2:   configv1.UpdateHistory{Version: ""},
			want: false,
		},
		{
			name: "is not same minor version, no dots",
			h1:   configv1.UpdateHistory{Version: "4.3.3"},
			h2:   configv1.UpdateHistory{Version: "432"},
			want: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			isIt := sameMinorVersion(tt.h1, tt.h2)
			if !reflect.DeepEqual(tt.want, isIt) {
				t.Fatalf("%s", diff.ObjectReflectDiff(tt.want, isIt))
			}
		})
	}
}
func Test_sameZStreamVersion(t *testing.T) {
	tests := []struct {
		name string
		h1   configv1.UpdateHistory
		h2   configv1.UpdateHistory
		want bool
	}{
		{
			name: "is same z-stream version",
			h1:   configv1.UpdateHistory{Version: "4.3.3"},
			h2:   configv1.UpdateHistory{Version: "4.3.3"},
			want: true,
		},
		{
			name: "is same z-stream version, no major",
			h1:   configv1.UpdateHistory{Version: "4.3.3"},
			h2:   configv1.UpdateHistory{Version: ".3.3"},
			want: true,
		},
		{
			name: "is not same z-stream version",
			h1:   configv1.UpdateHistory{Version: "4.4.3"},
			h2:   configv1.UpdateHistory{Version: "4.4.2"},
			want: false,
		},
		{
			name: "is not same z-stream version, no minor",
			h1:   configv1.UpdateHistory{Version: "4.3.3"},
			h2:   configv1.UpdateHistory{Version: "4..3"},
			want: false,
		},
		{
			name: "is not same z-stream version, diff minor",
			h1:   configv1.UpdateHistory{Version: "4.4.3"},
			h2:   configv1.UpdateHistory{Version: "4.3.3"},
			want: false,
		},
		{
			name: "is not same z-stream version, empty",
			h1:   configv1.UpdateHistory{Version: "4.4.3"},
			h2:   configv1.UpdateHistory{Version: ""},
			want: false,
		},
		{
			name: "is not same z-stream version, no dots",
			h1:   configv1.UpdateHistory{Version: "4.3.3"},
			h2:   configv1.UpdateHistory{Version: "432"},
			want: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			isIt := sameZStreamVersion(tt.h1, tt.h2)
			if !reflect.DeepEqual(tt.want, isIt) {
				t.Fatalf("%s", diff.ObjectReflectDiff(tt.want, isIt))
			}
		})
	}
}

func TestGetEffectiveMinor(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "empty",
			input:    "",
			expected: "",
		},
		{
			name:     "invalid",
			input:    "something@very-differe",
			expected: "",
		},
		{
			name:     "multidot",
			input:    "v4.7.12.3+foo",
			expected: "7",
		},
		{
			name:     "single",
			input:    "v4.7",
			expected: "7",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			actual := getEffectiveMinor(tc.input)
			if tc.expected != actual {
				t.Error(actual)
			}
		})
	}
}

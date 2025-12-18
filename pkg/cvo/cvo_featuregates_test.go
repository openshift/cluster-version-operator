package cvo

import (
	"testing"

	configv1 "github.com/openshift/api/config/v1"
	"k8s.io/apimachinery/pkg/util/sets"
)

func TestOperator_extractEnabledGates(t *testing.T) {
	tests := []struct {
		name        string
		featureGate *configv1.FeatureGate
		release     configv1.Release
		expected    sets.Set[string]
	}{
		{
			name: "extract gates for matching version",
			featureGate: &configv1.FeatureGate{
				Status: configv1.FeatureGateStatus{
					FeatureGates: []configv1.FeatureGateDetails{
						{
							Version: "4.14.0",
							Enabled: []configv1.FeatureGateAttributes{
								{Name: "TechPreviewFeatureGate"},
								{Name: "ExperimentalFeature"},
							},
						},
						{
							Version: "4.13.0",
							Enabled: []configv1.FeatureGateAttributes{
								{Name: "OldFeature"},
							},
						},
					},
				},
			},
			release:  configv1.Release{Version: "4.14.0"},
			expected: sets.New[string]("TechPreviewFeatureGate", "ExperimentalFeature"),
		},
		{
			name: "no matching version - return empty",
			featureGate: &configv1.FeatureGate{
				Status: configv1.FeatureGateStatus{
					FeatureGates: []configv1.FeatureGateDetails{
						{
							Version: "4.13.0",
							Enabled: []configv1.FeatureGateAttributes{
								{Name: "OldFeature"},
							},
						},
					},
				},
			},
			release:  configv1.Release{Version: "4.14.0"},
			expected: sets.Set[string]{},
		},
		{
			name: "empty enabled gates",
			featureGate: &configv1.FeatureGate{
				Status: configv1.FeatureGateStatus{
					FeatureGates: []configv1.FeatureGateDetails{
						{
							Version: "4.14.0",
							Enabled: []configv1.FeatureGateAttributes{},
						},
					},
				},
			},
			release:  configv1.Release{Version: "4.14.0"},
			expected: sets.Set[string]{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			optr := &Operator{
				release: tt.release,
				enabledFeatureGates: fakeRiFlags{
					desiredVersion: tt.release.Version,
				},
			}

			result := optr.extractEnabledGates(tt.featureGate)

			if !result.Equal(tt.expected) {
				t.Errorf("extractEnabledGates() = %v, expected %v", sets.List(result), sets.List(tt.expected))
			}
		})
	}
}

func TestOperator_getEnabledFeatureGates(t *testing.T) {
	optr := &Operator{
		enabledManifestFeatureGates: sets.New[string]("gate1", "gate2"),
	}

	result := optr.getEnabledFeatureGates()
	expected := sets.New[string]("gate1", "gate2")

	if !result.Equal(expected) {
		t.Errorf("getEnabledFeatureGates() = %v, expected %v", sets.List(result), sets.List(expected))
	}

	// Verify it returns a copy by modifying the result
	result.Insert("gate3")
	result2 := optr.getEnabledFeatureGates()

	if result2.Has("gate3") {
		t.Error("getEnabledFeatureGates() should return a copy, but original was modified")
	}
}

func TestOperator_updateEnabledFeatureGates(t *testing.T) {
	tests := []struct {
		name         string
		obj          interface{}
		expectUpdate bool
	}{
		{
			name: "valid FeatureGate object",
			obj: &configv1.FeatureGate{
				Status: configv1.FeatureGateStatus{
					FeatureGates: []configv1.FeatureGateDetails{
						{
							Version: "4.14.0",
							Enabled: []configv1.FeatureGateAttributes{
								{Name: "NewGate"},
							},
						},
					},
				},
			},
			expectUpdate: true,
		},
		{
			name:         "invalid object type",
			obj:          "not-a-feature-gate",
			expectUpdate: false,
		},
		{
			name:         "nil object",
			obj:          nil,
			expectUpdate: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			optr := &Operator{
				enabledManifestFeatureGates: sets.New[string]("oldgate"),
				release:                     configv1.Release{Version: "4.14.0"},
				enabledFeatureGates: fakeRiFlags{
					desiredVersion: "4.14.0",
				},
			}

			originalGates := optr.getEnabledFeatureGates()
			optr.updateEnabledFeatureGates(tt.obj)
			newGates := optr.getEnabledFeatureGates()

			if tt.expectUpdate {
				if newGates.Equal(originalGates) {
					t.Error("updateEnabledFeatureGates() expected gates to be updated")
				}
				if !newGates.Has("NewGate") {
					t.Error("updateEnabledFeatureGates() expected NewGate to be enabled")
				}
			} else {
				if !newGates.Equal(originalGates) {
					t.Error("updateEnabledFeatureGates() should not update gates for invalid object")
				}
			}
		})
	}
}

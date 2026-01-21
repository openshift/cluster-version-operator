package cvo

import (
	"testing"

	configv1 "github.com/openshift/api/config/v1"
	"github.com/openshift/library-go/pkg/manifest"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/util/sets"
)

// TestFeatureGateManifestFiltering tests the end-to-end feature gate filtering pipeline
func TestFeatureGateManifestFiltering(t *testing.T) {
	tests := []struct {
		name                string
		enabledGates        sets.Set[string]
		manifestAnnotations map[string]string
		shouldInclude       bool
		expectedError       string
	}{
		{
			name:         "include manifest with matching feature gate",
			enabledGates: sets.New[string]("TechPreviewFeatureGate"),
			manifestAnnotations: map[string]string{
				"release.openshift.io/feature-gate": "TechPreviewFeatureGate",
			},
			shouldInclude: true,
		},
		{
			name:         "exclude manifest with disabled feature gate",
			enabledGates: sets.New[string]("SomeOtherGate"),
			manifestAnnotations: map[string]string{
				"release.openshift.io/feature-gate": "TechPreviewFeatureGate",
			},
			shouldInclude: false,
			expectedError: "feature gate TechPreviewFeatureGate is required but not enabled",
		},
		{
			name:         "include manifest when exclusion gate is disabled",
			enabledGates: sets.New[string]("TechPreviewFeatureGate"),
			manifestAnnotations: map[string]string{
				"release.openshift.io/feature-gate": "-DisabledFeature",
			},
			shouldInclude: true,
		},
		{
			name:         "exclude manifest when exclusion gate is enabled",
			enabledGates: sets.New[string]("DisabledFeature"),
			manifestAnnotations: map[string]string{
				"release.openshift.io/feature-gate": "-DisabledFeature",
			},
			shouldInclude: false,
			expectedError: "feature gate DisabledFeature is enabled but manifest requires it to be disabled",
		},
		{
			name:         "complex filtering - AND logic",
			enabledGates: sets.New[string]("FeatureA"),
			manifestAnnotations: map[string]string{
				"release.openshift.io/feature-gate": "FeatureA,-FeatureB",
			},
			shouldInclude: true,
		},
		{
			name:         "complex filtering - failed AND logic",
			enabledGates: sets.New[string]("FeatureA", "FeatureB"),
			manifestAnnotations: map[string]string{
				"release.openshift.io/feature-gate": "FeatureA,-FeatureB",
			},
			shouldInclude: false,
			expectedError: "feature gate FeatureB is enabled but manifest requires it to be disabled",
		},
		{
			name:         "manifest with no feature gate annotation",
			enabledGates: sets.New[string]("AnyGate"),
			manifestAnnotations: map[string]string{
				"some.other.annotation": "value",
			},
			shouldInclude: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create a mock manifest with the test annotations
			obj := &unstructured.Unstructured{
				Object: map[string]interface{}{
					"apiVersion": "v1",
					"kind":       "ConfigMap",
					"metadata": map[string]interface{}{
						"name":      "test-configmap",
						"namespace": "test-namespace",
					},
				},
			}
			// Use SetAnnotations to ensure proper annotation handling
			obj.SetAnnotations(tt.manifestAnnotations)

			manifest := &manifest.Manifest{
				Obj: obj,
			}

			// Test the manifest inclusion logic
			err := manifest.Include(nil, nil, nil, nil, nil, tt.enabledGates)

			if tt.shouldInclude {
				if err != nil {
					t.Errorf("Expected manifest to be included, but got error: %v", err)
				}
			} else {
				if err == nil {
					t.Error("Expected manifest to be excluded, but no error occurred")
				} else if tt.expectedError != "" && err.Error() != tt.expectedError {
					t.Errorf("Expected error %q, got %q", tt.expectedError, err.Error())
				}
			}
		})
	}
}

// TestSyncWorkIntegration tests that feature gates are properly passed through the SyncWork pipeline
func TestSyncWorkIntegration(t *testing.T) {
	work := &SyncWork{
		Generation:          1,
		Desired:             configv1.Update{Image: "test-image"},
		EnabledFeatureGates: sets.New[string]("TestGate1", "TestGate2"),
	}

	// Test that the SyncWork can be used for manifest filtering
	testObj := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "v1",
			"kind":       "ConfigMap",
			"metadata": map[string]interface{}{
				"name": "test-configmap",
			},
		},
	}
	testObj.SetAnnotations(map[string]string{
		"release.openshift.io/feature-gate": "TestGate1",
	})

	manifest := &manifest.Manifest{
		Obj: testObj,
	}

	err := manifest.Include(nil, nil, nil, nil, nil, work.EnabledFeatureGates)
	if err != nil {
		t.Errorf("Expected manifest to be included with TestGate1 enabled, got error: %v", err)
	}

	// Test with a gate that's not enabled
	manifest.Obj.SetAnnotations(map[string]string{
		"release.openshift.io/feature-gate": "DisabledGate",
	})

	err = manifest.Include(nil, nil, nil, nil, nil, work.EnabledFeatureGates)
	if err == nil {
		t.Error("Expected manifest to be excluded with DisabledGate not enabled, but no error occurred")
	}
}

// TestFeatureGateEventHandling tests the feature gate event handler
func TestFeatureGateEventHandling(t *testing.T) {
	// Create a simple operator with feature gate management capabilities
	optr := &Operator{
		release: configv1.Release{Version: "4.14.0"},
		enabledCVOFeatureGates: fakeRiFlags{
			desiredVersion: "4.14.0",
		},
	}

	// Initialize feature gates to empty.
	optr.updateEnabledFeatureGates(&configv1.FeatureGate{})

	// Test that initial state is empty
	gates := optr.getEnabledFeatureGates()
	if gates.Len() != 0 {
		t.Errorf("Expected empty initial feature gates, got %v", sets.List(gates))
	}

	// Test updating with a FeatureGate object
	featureGate := &configv1.FeatureGate{
		Status: configv1.FeatureGateStatus{
			FeatureGates: []configv1.FeatureGateDetails{
				{
					Version: "4.14.0",
					Enabled: []configv1.FeatureGateAttributes{
						{Name: "NewFeature"},
						{Name: "ExperimentalFeature"},
					},
				},
			},
		},
	}

	optr.updateEnabledFeatureGates(featureGate)

	// Verify gates were updated
	gates = optr.getEnabledFeatureGates()
	expected := sets.New[string]("NewFeature", "ExperimentalFeature")
	if !gates.Equal(expected) {
		t.Errorf("After update, feature gates = %v, expected %v", sets.List(gates), sets.List(expected))
	}
}

// TestManifestFilteringExamples tests real-world usage examples
func TestManifestFilteringExamples(t *testing.T) {
	examples := []struct {
		name               string
		description        string
		enabledGates       sets.Set[string]
		manifestAnnotation string
		shouldInclude      bool
	}{
		{
			name:               "TechPreview feature deployment",
			description:        "Deploy experimental ConfigMap only in TechPreviewFeatureGate enabled clusters",
			enabledGates:       sets.New("TechPreviewFeatureGate"),
			manifestAnnotation: "TechPreviewFeatureGate",
			shouldInclude:      true,
		},
		{
			name:               "Production cluster excludes TechPreview",
			description:        "Production cluster should exclude TechPreviewFeatureGate enabled manifests",
			enabledGates:       sets.New[string](),
			manifestAnnotation: "TechPreviewFeatureGate",
			shouldInclude:      false,
		},
		{
			name:               "Alternative implementation selection",
			description:        "Use new storage API when enabled, exclude legacy API",
			enabledGates:       sets.New("NewStorageAPI"),
			manifestAnnotation: "NewStorageAPI,-LegacyStorageAPI",
			shouldInclude:      true,
		},
		{
			name:               "Legacy implementation when new API disabled",
			description:        "Use legacy implementation when new API is not enabled",
			enabledGates:       sets.New[string](),
			manifestAnnotation: "-NewStorageAPI",
			shouldInclude:      true,
		},
	}

	for _, example := range examples {
		t.Run(example.name, func(t *testing.T) {
			obj := &unstructured.Unstructured{
				Object: map[string]interface{}{
					"apiVersion": "v1",
					"kind":       "ConfigMap",
					"metadata": map[string]interface{}{
						"name": "example-manifest",
					},
				},
			}
			// Use SetAnnotations to ensure proper annotation handling
			obj.SetAnnotations(map[string]string{
				"release.openshift.io/feature-gate": example.manifestAnnotation,
			})

			manifest := &manifest.Manifest{
				Obj: obj,
			}

			err := manifest.Include(nil, nil, nil, nil, nil, example.enabledGates)

			if example.shouldInclude {
				if err != nil {
					t.Errorf("%s: Expected manifest to be included, but got error: %v",
						example.description, err)
				}
			} else {
				if err == nil {
					t.Errorf("%s: Expected manifest to be excluded, but no error occurred",
						example.description)
				}
			}
		})
	}
}

package payload

import (
	"os"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/utils/ptr"
)

func TestRenderManifest(t *testing.T) {

	tests := []struct {
		name                 string
		config               manifestRenderConfig
		manifestFile         string
		expectedManifestFile string
		expectedErr          error
	}{
		{
			name: "cvo deployment",
			config: manifestRenderConfig{
				ReleaseImage:   "quay.io/cvo/release:latest",
				ClusterProfile: "some-profile",
			},
			manifestFile:         "../../install/0000_00_cluster-version-operator_03_deployment.yaml",
			expectedManifestFile: "./testdata/TestRenderManifest_expected_cvo_deployment.yaml",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			bytes, err := os.ReadFile(tt.manifestFile)
			if err != nil {
				t.Fatalf("failed to read manifest file: %v", err)
			}
			actual, actualErr := renderManifest(tt.config, bytes)
			if strings.ToLower(os.Getenv("UPDATE")) == "true" || actualErr != nil {
				if err := os.WriteFile(tt.expectedManifestFile, actual, 0644); err != nil {
					t.Fatalf("failed to update manifest file: %v", err)
				}
			}
			if diff := cmp.Diff(tt.expectedErr, actualErr, cmp.Comparer(func(x, y error) bool {
				if x == nil || y == nil {
					return x == nil && y == nil
				}
				return x.Error() == y.Error()
			})); diff != "" {
				t.Errorf("%s: the actual error differs from the expected (-want, +got): %s", tt.name, diff)
			} else {
				expected, err := os.ReadFile(tt.expectedManifestFile)
				if err != nil {
					t.Fatalf("failed to read expected manifest file: %v", err)
				}
				if diff := cmp.Diff(expected, actual); diff != "" {
					t.Errorf("%s: the actual does not match the expected (-want, +got): %s", tt.name, diff)
				}
			}
		})
	}
}

func TestRenderDirWithMajorVersionFiltering(t *testing.T) {
	tests := []struct {
		name               string
		manifests          []testManifest
		majorVersion       *uint64
		expectedInclusions []string // Names of manifests that should be included
		expectedExclusions []string // Names of manifests that should be excluded
	}{
		{
			name: "major version 4 includes version 4 manifests",
			manifests: []testManifest{
				{
					name: "version4-manifest",
					annotations: map[string]string{
						"release.openshift.io/major-version": "4",
					},
				},
				{
					name: "version5-manifest",
					annotations: map[string]string{
						"release.openshift.io/major-version": "5",
					},
				},
				{
					name:        "no-version-manifest",
					annotations: map[string]string{},
				},
			},
			majorVersion:       ptr.To(uint64(4)),
			expectedInclusions: []string{"version4-manifest", "no-version-manifest"},
			expectedExclusions: []string{"version5-manifest"},
		},
		{
			name: "major version 5 includes version 5 manifests",
			manifests: []testManifest{
				{
					name: "version4-manifest",
					annotations: map[string]string{
						"release.openshift.io/major-version": "4",
					},
				},
				{
					name: "version5-manifest",
					annotations: map[string]string{
						"release.openshift.io/major-version": "5",
					},
				},
			},
			majorVersion:       ptr.To(uint64(5)),
			expectedInclusions: []string{"version5-manifest"},
			expectedExclusions: []string{"version4-manifest"},
		},
		{
			name: "nil major version includes all manifests",
			manifests: []testManifest{
				{
					name: "version4-manifest",
					annotations: map[string]string{
						"release.openshift.io/major-version": "4",
					},
				},
				{
					name: "version5-manifest",
					annotations: map[string]string{
						"release.openshift.io/major-version": "5",
					},
				},
			},
			majorVersion:       nil,
			expectedInclusions: []string{"version4-manifest", "version5-manifest"},
			expectedExclusions: []string{},
		},
		{
			name: "exclusion filtering with major version",
			manifests: []testManifest{
				{
					name: "not-for-version3-manifest",
					annotations: map[string]string{
						"release.openshift.io/major-version": "-3",
					},
				},
				{
					name: "only-for-version4-manifest",
					annotations: map[string]string{
						"release.openshift.io/major-version": "4",
					},
				},
			},
			majorVersion:       ptr.To(uint64(4)),
			expectedInclusions: []string{"not-for-version3-manifest", "only-for-version4-manifest"},
			expectedExclusions: []string{},
		},
		{
			name: "complex major version filtering",
			manifests: []testManifest{
				{
					name: "version4-or-5-manifest",
					annotations: map[string]string{
						"release.openshift.io/major-version": "4,5",
					},
				},
				{
					name: "exclude-version3-manifest",
					annotations: map[string]string{
						"release.openshift.io/major-version": "-3",
					},
				},
				{
					name: "version6-only-manifest",
					annotations: map[string]string{
						"release.openshift.io/major-version": "6",
					},
				},
			},
			majorVersion:       ptr.To(uint64(4)),
			expectedInclusions: []string{"version4-or-5-manifest", "exclude-version3-manifest"},
			expectedExclusions: []string{"version6-only-manifest"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create temporary directory structure
			tempDir := t.TempDir()
			inputDir := tempDir + "/input"
			outputDir := tempDir + "/output"

			if err := os.MkdirAll(inputDir, 0755); err != nil {
				t.Fatalf("failed to create input directory: %v", err)
			}
			if err := os.MkdirAll(outputDir, 0755); err != nil {
				t.Fatalf("failed to create output directory: %v", err)
			}

			// Write test manifests to input directory
			for _, manifest := range tt.manifests {
				manifestContent := createTestManifestYAML("apiextensions.k8s.io/v1", "CustomResourceDefinition", manifest.name, manifest.annotations)
				manifestPath := inputDir + "/" + manifest.name + ".yaml"
				if err := os.WriteFile(manifestPath, []byte(manifestContent), 0644); err != nil {
					t.Fatalf("failed to write manifest %s: %v", manifest.name, err)
				}
			}

			// Setup render configuration
			renderConfig := manifestRenderConfig{
				ReleaseImage:   "test-image:latest",
				ClusterProfile: "test-profile",
			}

			requiredFeatureSet := "test"
			enabledFeatureGates := sets.Set[string]{}
			clusterProfile := "test-profile"

			// Call renderDir with major version
			err := renderDir(
				renderConfig,
				inputDir,
				outputDir,
				&requiredFeatureSet,
				enabledFeatureGates,
				&clusterProfile,
				tt.majorVersion,
				false,                        // processTemplate
				sets.Set[string]{},           // skipFiles
				sets.Set[schema.GroupKind]{}, // filterGroupKind
			)

			if err != nil {
				t.Fatalf("renderDir failed: %v", err)
			}

			// Check which manifests were included in output
			outputFiles, err := os.ReadDir(outputDir)
			if err != nil {
				t.Fatalf("failed to read output directory: %v", err)
			}

			includedManifests := make(map[string]bool)
			for _, file := range outputFiles {
				if strings.HasSuffix(file.Name(), ".yaml") {
					manifestName := strings.TrimSuffix(file.Name(), ".yaml")
					includedManifests[manifestName] = true
				}
			}

			// Verify expected inclusions
			for _, expectedName := range tt.expectedInclusions {
				if !includedManifests[expectedName] {
					t.Errorf("Expected manifest %s to be included, but it was not found in output", expectedName)
				}
			}

			// Verify expected exclusions
			for _, expectedExcludedName := range tt.expectedExclusions {
				if includedManifests[expectedExcludedName] {
					t.Errorf("Expected manifest %s to be excluded, but it was found in output", expectedExcludedName)
				}
			}
		})
	}
}

// testManifest represents a test manifest with its annotations
type testManifest struct {
	name        string
	annotations map[string]string
}

// createTestManifestYAML creates a YAML representation of a test manifest
func createTestManifestYAML(apiVersion, kind, name string, annotations map[string]string) string {
	yaml := `apiVersion: ` + apiVersion + `
kind: ` + kind + `
metadata:
  name: ` + name + `
  namespace: test-namespace`

	// Always add required cluster profile annotation
	allAnnotations := make(map[string]string)
	allAnnotations["include.release.openshift.io/test-profile"] = "true"

	// Add provided annotations
	for k, v := range annotations {
		allAnnotations[k] = v
	}

	if len(allAnnotations) > 0 {
		yaml += "\n  annotations:"
		for k, v := range allAnnotations {
			yaml += "\n    " + k + ": \"" + v + "\""
		}
	}

	yaml += `
data:
  test: "data"`

	return yaml
}

package payload

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/openshift/library-go/pkg/manifest"
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

func Test_cvoManifests(t *testing.T) {
	installDir := filepath.Join("../../install")
	files, err := os.ReadDir(installDir)
	if err != nil {
		t.Fatalf("failed to read directory: %v", err)
	}

	if len(files) == 0 {
		t.Fatalf("no files found in %s", installDir)
	}

	var manifestsWithoutIncludeAnnotation []manifest.Manifest
	const prefix = "include.release.openshift.io/"
	for _, manifestFile := range files {
		if manifestFile.IsDir() {
			continue
		}
		filePath := filepath.Join(installDir, manifestFile.Name())
		manifests, err := manifest.ManifestsFromFiles([]string{filePath})
		if err != nil {
			t.Fatalf("failed to load manifests: %v", err)
		}

		for _, m := range manifests {
			var found bool
			for k := range m.Obj.GetAnnotations() {
				if strings.HasPrefix(k, prefix) {
					found = true
					break
				}
			}
			if !found {
				manifestsWithoutIncludeAnnotation = append(manifestsWithoutIncludeAnnotation, m)
			}
		}
	}

	if len(manifestsWithoutIncludeAnnotation) > 0 {
		var messages []string
		for _, m := range manifestsWithoutIncludeAnnotation {
			messages = append(messages, fmt.Sprintf("%s/%s/%s/%s", m.OriginalFilename, m.GVK, m.Obj.GetName(), m.Obj.GetNamespace()))
		}
		t.Fatalf("Those manifests have no annotation with prefix %q and will not beinstalled by CVO: %s", prefix, strings.Join(messages, "', '"))
	}
}

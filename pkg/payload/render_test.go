package payload

import (
	"bytes"
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
	config := manifestRenderConfig{
		ReleaseImage:   "quay.io/cvo/release:latest",
		ClusterProfile: "some-profile",
	}

	tests := []struct {
		name string
		dir  string
	}{
		{
			name: "install dir",
			dir:  filepath.Join("../../install"),
		},
		{
			name: "bootstrap dir",
			dir:  filepath.Join("../../bootstrap"),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			files, err := os.ReadDir(tt.dir)
			if err != nil {
				t.Fatalf("failed to read directory: %v", err)
			}

			if len(files) == 0 {
				t.Fatalf("no files found in %s", tt.dir)
			}

			var manifestsWithoutIncludeAnnotation []manifest.Manifest
			const prefix = "include.release.openshift.io/"
			for _, manifestFile := range files {
				if manifestFile.IsDir() {
					continue
				}
				filePath := filepath.Join(tt.dir, manifestFile.Name())
				data, err := os.ReadFile(filePath)
				if err != nil {
					t.Fatalf("failed to read manifest file: %v", err)
				}
				data, err = renderManifest(config, data)
				if err != nil {
					t.Fatalf("failed to render manifest: %v", err)
				}
				manifests, err := manifest.ParseManifests(bytes.NewReader(data))
				if err != nil {
					t.Fatalf("failed to load manifests: %v", err)
				}

				for _, m := range manifests {
					m.OriginalFilename = filePath
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
		})
	}
}

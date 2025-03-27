package payload

import (
	"os"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
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

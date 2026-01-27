package payload

import (
	"os"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	configv1 "github.com/openshift/api/config/v1"
	"k8s.io/apimachinery/pkg/util/sets"
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

func Test_cvoKnownFeatureSets(t *testing.T) {
	unknown := sets.New[string]()
	known := cvoSkipFeatureSets.Clone().Insert(string(configv1.Default))
	for _, fs := range append(configv1.AllFixedFeatureSets, configv1.CustomNoUpgrade) {
		candidate := string(fs)
		if !known.Has(candidate) {
			unknown.Insert(candidate)
		}
	}
	if unknown.Len() != 0 {
		t.Errorf("found unknown feature sets [%s] not recognized by CVO. If it is a result of bump o/api e.g., "+
			"https://github.com/openshift/cluster-version-operator/pull/1302/commits/dd3b8335f7f443b5ab6ffcfd78236832cb922753 "+
			"and it led to failing CVO/Hypershift e2e tests, please try to fix them by pulls like "+
			"https://github.com/openshift/hypershift/pull/7557 and "+
			"https://github.com/openshift/cluster-version-operator/pull/1302/commits/981361e85b58ec77dada253e63f79f8ef78a8bd1. "+
			"Otherwise, adding them to cvoSkipFeatureSets fixes this unit test", strings.Join(unknown.UnsortedList(), ", "))
	}
}

package payload

import (
	"io/ioutil"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"

	configv1 "github.com/openshift/api/config/v1"
	imagev1 "github.com/openshift/api/image/v1"

	"github.com/openshift/library-go/pkg/manifest"
)

func Test_loadUpdatePayload(t *testing.T) {
	type args struct {
		dir          string
		releaseImage string
	}
	tests := []struct {
		name    string
		args    args
		want    *Update
		wantErr bool
	}{
		{
			name: "ignore files without extensions, load metadata",
			args: args{
				dir:          filepath.Join("..", "cvo", "testdata", "payloadtest"),
				releaseImage: "image:1",
			},
			want: &Update{
				Release: configv1.Release{
					Version:  "1.0.0-abc",
					Image:    "image:1",
					URL:      configv1.URL("https://example.com/v1.0.0-abc"),
					Channels: []string{"channel-a", "channel-b", "channel-c"},
				},
				ImageRef: &imagev1.ImageStream{
					TypeMeta: metav1.TypeMeta{
						Kind:       "ImageStream",
						APIVersion: "image.openshift.io/v1",
					},
					ObjectMeta: metav1.ObjectMeta{
						Name: "1.0.0-abc",
					},
				},
				ManifestHash: "DL-FFQ2Uem8=",
				Manifests: []manifest.Manifest{
					{
						OriginalFilename: "0000_10_a_file.json",
						Raw:              mustRead(filepath.Join("..", "cvo", "testdata", "payloadtest", "release-manifests", "0000_10_a_file.json")),
						GVK: schema.GroupVersionKind{
							Kind:    "Test",
							Version: "v1",
						},
						Obj: &unstructured.Unstructured{
							Object: map[string]interface{}{
								"kind":       "Test",
								"apiVersion": "v1",
								"metadata": map[string]interface{}{
									"name":        "file-json",
									"annotations": map[string]interface{}{"include.release.openshift.io/self-managed-high-availability": "true"},
								},
							},
						},
					},
					{
						OriginalFilename: "0000_10_a_file.yaml",
						Raw:              []byte(`{"apiVersion":"v1","kind":"Test","metadata":{"annotations":{"include.release.openshift.io/self-managed-high-availability":"true"},"name":"file-yaml"}}`),
						GVK: schema.GroupVersionKind{
							Kind:    "Test",
							Version: "v1",
						},
						Obj: &unstructured.Unstructured{
							Object: map[string]interface{}{
								"kind":       "Test",
								"apiVersion": "v1",
								"metadata": map[string]interface{}{
									"name":        "file-yaml",
									"annotations": map[string]interface{}{"include.release.openshift.io/self-managed-high-availability": "true"},
								},
							},
						},
					},
					{
						OriginalFilename: "0000_10_a_file.yml",
						Raw:              []byte(`{"apiVersion":"v1","kind":"Test","metadata":{"annotations":{"include.release.openshift.io/self-managed-high-availability":"true"},"name":"file-yml"}}`),
						GVK: schema.GroupVersionKind{
							Kind:    "Test",
							Version: "v1",
						},
						Obj: &unstructured.Unstructured{
							Object: map[string]interface{}{
								"kind":       "Test",
								"apiVersion": "v1",
								"metadata": map[string]interface{}{
									"name":        "file-yml",
									"annotations": map[string]interface{}{"include.release.openshift.io/self-managed-high-availability": "true"},
								},
							},
						},
					},
				},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := LoadUpdate(tt.args.dir, tt.args.releaseImage, "exclude-test", false, DefaultClusterProfile)
			if (err != nil) != tt.wantErr {
				t.Errorf("loadUpdatePayload() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			// Manifest holds an unexported type of 'resourceID' with a field name of 'id'
			// DeepEqual fails so here we use cmp.Diff to ignore just that field to avoid false postives
			stringDiff := cmp.Diff(tt.want, got, cmpopts.IgnoreFields(manifest.Manifest{}, "id"))
			if !reflect.DeepEqual(got, tt.want) && stringDiff != "" {
				t.Errorf("loadUpdatePayload() = %s", stringDiff)
			}
		})
	}
}

func mustRead(path string) []byte {
	data, err := ioutil.ReadFile(path)
	if err != nil {
		panic(err)
	}
	return data
}

func Test_Exclude(t *testing.T) {
	tests := []struct {
		name               string
		exclude            string
		includeTechPreview bool
		profile            string
		annotations        map[string]interface{}

		isExcluded bool
	}{
		{
			name:    "exclusion identifier set",
			exclude: "identifier",
			profile: DefaultClusterProfile,
			annotations: map[string]interface{}{
				"exclude.release.openshift.io/identifier":                     "true",
				"include.release.openshift.io/self-managed-high-availability": "true"},
			isExcluded: true,
		},
		{
			name:        "profile selection works",
			profile:     "single-node",
			annotations: map[string]interface{}{"include.release.openshift.io/self-managed-high-availability": "true"},
			isExcluded:  true,
		},
		{
			name:        "profile selection works included",
			profile:     DefaultClusterProfile,
			annotations: map[string]interface{}{"include.release.openshift.io/self-managed-high-availability": "true"},
		},
		{
			name:    "correct techpreview value is excluded if techpreview off",
			profile: DefaultClusterProfile,
			annotations: map[string]interface{}{
				"include.release.openshift.io/self-managed-high-availability": "true",
				"release.openshift.io/feature-gate":                           "TechPreviewNoUpgrade",
			},
			isExcluded: true,
		},
		{
			name:               "correct techpreview value is included if techpreview on",
			includeTechPreview: true,
			profile:            DefaultClusterProfile,
			annotations: map[string]interface{}{
				"include.release.openshift.io/self-managed-high-availability": "true",
				"release.openshift.io/feature-gate":                           "TechPreviewNoUpgrade",
			},
			isExcluded: false,
		},
		{
			name:    "incorrect techpreview value is not excluded if techpreview off",
			profile: DefaultClusterProfile,
			annotations: map[string]interface{}{
				"include.release.openshift.io/self-managed-high-availability": "true",
				"release.openshift.io/feature-gate":                           "Other",
			},
			isExcluded: true,
		},
		{
			name:               "incorrect techpreview value is not excluded if techpreview on",
			includeTechPreview: true,
			profile:            DefaultClusterProfile,
			annotations: map[string]interface{}{
				"include.release.openshift.io/self-managed-high-availability": "true",
				"release.openshift.io/feature-gate":                           "Other",
			},
			isExcluded: true,
		},
		{
			name:        "default profile selection excludes without annotation",
			profile:     DefaultClusterProfile,
			annotations: map[string]interface{}{},
			isExcluded:  true,
		},
		{
			name:        "default profile selection excludes with no annotation",
			profile:     DefaultClusterProfile,
			annotations: nil,
			isExcluded:  true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			metadata := map[string]interface{}{}
			if tt.annotations != nil {
				metadata["annotations"] = tt.annotations
			}
			ret := shouldExclude(tt.exclude, tt.includeTechPreview, tt.profile, &manifest.Manifest{
				Obj: &unstructured.Unstructured{
					Object: map[string]interface{}{
						"metadata": metadata,
					},
				},
			})
			if ret != tt.isExcluded {
				t.Errorf("(exclude: %v, profile: %v, annotations: %v) %v != %v", tt.exclude, tt.profile, tt.annotations, tt.isExcluded, ret)
			}
		})
	}
}

package payload

import (
	"io/ioutil"
	"path/filepath"
	"reflect"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/diff"

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
			got, err := LoadUpdate(tt.args.dir, tt.args.releaseImage, "exclude-test", nil, DefaultClusterProfile)
			if (err != nil) != tt.wantErr {
				t.Errorf("loadUpdatePayload() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("loadUpdatePayload() = %s", diff.ObjectReflectDiff(tt.want, got))
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
		exclude     string
		profile     string
		annotations map[string]interface{}

		isExcluded bool
	}{
		{
			exclude: "identifier",
			profile: DefaultClusterProfile,
			annotations: map[string]interface{}{
				"exclude.release.openshift.io/identifier":                     "true",
				"include.release.openshift.io/self-managed-high-availability": "true"},
			isExcluded: true,
		},
		{
			profile:     "single-node",
			annotations: map[string]interface{}{"include.release.openshift.io/self-managed-high-availability": "true"},
			isExcluded:  true,
		},
		{
			profile:     DefaultClusterProfile,
			annotations: map[string]interface{}{"include.release.openshift.io/self-managed-high-availability": "true"},
		},
		{
			profile:     DefaultClusterProfile,
			annotations: map[string]interface{}{},
			isExcluded:  true,
		},
		{
			profile:     DefaultClusterProfile,
			annotations: nil,
			isExcluded:  true,
		},
	}
	for _, tt := range tests {
		metadata := map[string]interface{}{}
		if tt.annotations != nil {
			metadata["annotations"] = tt.annotations
		}
		ret := shouldExclude(tt.exclude, tt.profile, &manifest.Manifest{
			Obj: &unstructured.Unstructured{
				Object: map[string]interface{}{
					"metadata": metadata,
				},
			},
		}, false)
		if ret != tt.isExcluded {
			t.Errorf("(exclude: %v, profile: %v, annotations: %v) %v != %v", tt.exclude, tt.profile, tt.annotations, tt.isExcluded, ret)
		}
	}
}

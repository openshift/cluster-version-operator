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

	imagev1 "github.com/openshift/api/image/v1"

	"github.com/openshift/cluster-version-operator/lib"
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
				ReleaseImage:   "image:1",
				ReleaseVersion: "1.0.0-abc",
				ImageRef: &imagev1.ImageStream{
					TypeMeta: metav1.TypeMeta{
						Kind:       "ImageStream",
						APIVersion: "image.openshift.io/v1",
					},
					ObjectMeta: metav1.ObjectMeta{
						Name: "1.0.0-abc",
					},
				},
				ManifestHash: "6GC9TkkG9PA=",
				Manifests: []lib.Manifest{
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
									"name": "file-json",
								},
							},
						},
					},
					{
						OriginalFilename: "0000_10_a_file.yaml",
						Raw:              []byte(`{"apiVersion":"v1","kind":"Test","metadata":{"name":"file-yaml"}}`),
						GVK: schema.GroupVersionKind{
							Kind:    "Test",
							Version: "v1",
						},
						Obj: &unstructured.Unstructured{
							Object: map[string]interface{}{
								"kind":       "Test",
								"apiVersion": "v1",
								"metadata": map[string]interface{}{
									"name": "file-yaml",
								},
							},
						},
					},
					{
						OriginalFilename: "0000_10_a_file.yml",
						Raw:              []byte(`{"apiVersion":"v1","kind":"Test","metadata":{"name":"file-yml"}}`),
						GVK: schema.GroupVersionKind{
							Kind:    "Test",
							Version: "v1",
						},
						Obj: &unstructured.Unstructured{
							Object: map[string]interface{}{
								"kind":       "Test",
								"apiVersion": "v1",
								"metadata": map[string]interface{}{
									"name": "file-yml",
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
			got, err := LoadUpdate(tt.args.dir, tt.args.releaseImage, "")
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

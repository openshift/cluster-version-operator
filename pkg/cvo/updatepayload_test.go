package cvo

import (
	"io/ioutil"
	"path/filepath"
	"reflect"
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	imagev1 "github.com/openshift/api/image/v1"
	"github.com/openshift/cluster-version-operator/lib"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/diff"
)

func Test_loadUpdatePayload(t *testing.T) {
	type args struct {
		dir          string
		releaseImage string
	}
	tests := []struct {
		name    string
		args    args
		want    *updatePayload
		wantErr bool
	}{
		{
			name: "ignore files without extensions, load metadata",
			args: args{
				dir:          filepath.Join("testdata", "payloadtest"),
				releaseImage: "image:1",
			},
			want: &updatePayload{
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
						Raw: mustRead(filepath.Join("testdata", "payloadtest", "release-manifests", "file.json")),
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
						Raw: []byte(`{"apiVersion":"v1","kind":"Test","metadata":{"name":"file-yaml"}}`),
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
						Raw: []byte(`{"apiVersion":"v1","kind":"Test","metadata":{"name":"file-yml"}}`),
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
			got, err := loadUpdatePayload(tt.args.dir, tt.args.releaseImage)
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

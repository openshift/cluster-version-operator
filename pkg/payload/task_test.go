package payload

import (
	"reflect"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/openshift/library-go/pkg/manifest"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

func TestTaskString(t *testing.T) {
	tests := []struct {
		name string
		task *Task
		want string
	}{
		{
			name: "Manifest with name",
			task: &Task{Manifest: &manifest.Manifest{
				OriginalFilename: "a",
				Obj: &unstructured.Unstructured{
					Object: map[string]interface{}{
						"kind":       "Test",
						"apiVersion": "v1",
						"metadata": map[string]interface{}{
							"name": "file-json",
						},
					},
				},
			}},
			want: ` "file-json" (0 of 0)`,
		},
		{
			name: "Manifest with GVK",
			task: &Task{Manifest: &manifest.Manifest{
				GVK: schema.GroupVersionKind{Group: "extensions", Version: "v1beta1", Kind: "Ingress"},
				Obj: &unstructured.Unstructured{
					Object: map[string]interface{}{
						"kind":       "Test",
						"apiVersion": "v1",
						"metadata": map[string]interface{}{
							"name": "file-json",
						},
					},
				},
			}},
			want: `ingress "file-json" (0 of 0)`,
		},
		{
			name: "Manifest with name and namespace",
			task: &Task{Manifest: &manifest.Manifest{
				OriginalFilename: "a",
				Obj: &unstructured.Unstructured{
					Object: map[string]interface{}{
						"kind":       "Test",
						"apiVersion": "v1",
						"metadata": map[string]interface{}{
							"name":      "file-json",
							"namespace": "foo",
						},
					},
				},
			}},
			want: ` "foo/file-json" (0 of 0)`,
		},
		{
			name: "Manifest with GVK and namespace",
			task: &Task{Manifest: &manifest.Manifest{
				GVK: schema.GroupVersionKind{Group: "extensions", Version: "v1beta1", Kind: "Ingress"},
				Obj: &unstructured.Unstructured{
					Object: map[string]interface{}{
						"kind":       "Test",
						"apiVersion": "v1",
						"metadata": map[string]interface{}{
							"name":      "file-json",
							"namespace": "foo",
						},
					},
				},
			}},
			want: `ingress "foo/file-json" (0 of 0)`,
		},
		{
			name: "index and total",
			task: &Task{
				Manifest: &manifest.Manifest{
					GVK: schema.GroupVersionKind{Group: "extensions", Version: "v1beta1", Kind: "Ingress"},
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
				Index: 3,
				Total: 7,
			},
			want: `ingress "file-json" (3 of 7)`,
		},
		{
			name: "Namespaced, index and total",
			task: &Task{
				Manifest: &manifest.Manifest{
					GVK: schema.GroupVersionKind{Group: "extensions", Version: "v1beta1", Kind: "Ingress"},
					Obj: &unstructured.Unstructured{
						Object: map[string]interface{}{
							"kind":       "Test",
							"apiVersion": "v1",
							"metadata": map[string]interface{}{
								"name":      "file-json",
								"namespace": "foo",
							},
						},
					},
				},
				Index: 5,
				Total: 10,
			},
			want: `ingress "foo/file-json" (5 of 10)`,
		},
		{
			name: "List",
			task: &Task{Manifest: &manifest.Manifest{
				OriginalFilename: "list_manifest.yaml",
				Obj: &unstructured.Unstructured{
					Object: map[string]interface{}{
						"kind":       "List",
						"apiVersion": "v1",
						"items": []interface{}{
							"foo",
							"bar",
						},
					},
				},
			}},
			want: ` "list_manifest.yaml" (0 of 0)`,
		},
		{
			name: "List and GVK",
			task: &Task{Manifest: &manifest.Manifest{
				OriginalFilename: "list_manifest.yaml",
				GVK:              schema.GroupVersionKind{Group: "", Version: "v1", Kind: "List"},
				Obj: &unstructured.Unstructured{
					Object: map[string]interface{}{
						"kind":       "List",
						"apiVersion": "v1",
						"items": []interface{}{
							"foo",
							"bar",
						},
					},
				},
			}},
			want: `list "list_manifest.yaml" (0 of 0)`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.task.String(); !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("%s", cmp.Diff(tt.want, got))
			}
		})
	}
}

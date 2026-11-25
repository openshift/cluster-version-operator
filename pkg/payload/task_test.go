package payload

import (
	"reflect"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/pkg/errors"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/validation/field"

	"github.com/openshift/library-go/pkg/manifest"
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

func TestReasonForPayloadSyncErrorInvalid(t *testing.T) {
	gk := schema.GroupKind{Group: "apiextensions.k8s.io", Kind: "CustomResourceDefinition"}
	ownerRefs := field.NewPath("metadata", "ownerReferences")
	tests := []struct {
		name string
		err  error
		want string
	}{
		{
			name: "invalid without causes",
			err:  apierrors.NewInvalid(gk, "clusterserviceversions.operators.coreos.com", nil),
			want: "the object is invalid, possibly due to local cluster configuration",
		},
		{
			name: "invalid with one cause",
			err: apierrors.NewInvalid(gk, "clusterserviceversions.operators.coreos.com", field.ErrorList{
				field.Invalid(ownerRefs, "x", `Only one reference can have Controller set to true. Found "true" in references for ClusterServiceVersion/rhsso-operator.7.6.9-opr-002 and ClusterVersion/version`),
			}),
			want: `the object is invalid, possibly due to local cluster configuration (metadata.ownerReferences: Invalid value: "x": Only one reference can have Controller set to true. Found "true" in references for ClusterServiceVersion/rhsso-operator.7.6.9-opr-002 and ClusterVersion/version)`,
		},
		{
			name: "invalid with two causes",
			err: apierrors.NewInvalid(gk, "a", field.ErrorList{
				field.Required(field.NewPath("spec", "group"), ""),
				field.Invalid(field.NewPath("spec", "scope"), "Bad", "unsupported"),
			}),
			want: `the object is invalid, possibly due to local cluster configuration (spec.group: Required value, spec.scope: Invalid value: "Bad": unsupported)`,
		},
		{
			name: "wrapped invalid",
			err: errors.Wrap(apierrors.NewInvalid(gk, "a", field.ErrorList{
				field.Required(field.NewPath("spec", "group"), ""),
			}), "error running apply"),
			want: "the object is invalid, possibly due to local cluster configuration (spec.group: Required value)",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reason, got := reasonForPayloadSyncError(tt.err)
			if reason != "UpdatePayloadResourceInvalid" {
				t.Errorf("reason = %q, want UpdatePayloadResourceInvalid", reason)
			}
			if got != tt.want {
				t.Errorf("message = %q\nwant      %q", got, tt.want)
			}
		})
	}
}

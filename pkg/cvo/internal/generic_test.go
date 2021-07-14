package internal

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/openshift/library-go/pkg/manifest"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic/fake"
	clientgotesting "k8s.io/client-go/testing"
)

func TestCreateOnlyCreate(t *testing.T) {
	ctx := context.Background()
	feature := `{
  "kind": "FeatureGate",
  "apiVersion": "config.openshift.io/v1",
  "metadata": {
     "name": "cluster",
     "annotations": {
       "release.openshift.io/create-only": "true"
     }
  }
}`
	obj, err := runtime.Decode(unstructured.UnstructuredJSONScheme, []byte(feature))
	if err != nil {
		t.Fatal(err)
	}

	fakeClient := fake.NewSimpleDynamicClient(runtime.NewScheme())
	_, modified, err := applyUnstructured(
		ctx,
		fakeClient.Resource(schema.GroupVersionResource{Group: "config.openshift.io", Version: "v1", Resource: "featuregates"}),
		obj.(*unstructured.Unstructured))
	if err != nil {
		t.Fatal(err)
	}
	if !modified {
		t.Error("should have created")
	}
}

func TestCreateOnlyUpdate(t *testing.T) {
	ctx := context.Background()
	feature := `{
  "kind": "FeatureGate",
  "apiVersion": "config.openshift.io/v1",
  "metadata": {
     "name": "cluster",
     "annotations": {
       "release.openshift.io/create-only": "true",
       "change": "here"
     }
  }
}`
	existing := `{
  "kind": "FeatureGate",
  "apiVersion": "config.openshift.io/v1",
  "metadata": {
     "name": "cluster",
     "annotations": {
       "release.openshift.io/create-only": "true"
     }
  }
}`
	obj, err := runtime.Decode(unstructured.UnstructuredJSONScheme, []byte(feature))
	if err != nil {
		t.Fatal(err)
	}
	existingObj, err := runtime.Decode(unstructured.UnstructuredJSONScheme, []byte(existing))
	if err != nil {
		t.Fatal(err)
	}

	fakeClient := fake.NewSimpleDynamicClient(runtime.NewScheme(), existingObj)
	_, modified, err := applyUnstructured(
		ctx,
		fakeClient.Resource(schema.GroupVersionResource{Group: "config.openshift.io", Version: "v1", Resource: "featuregates"}),
		obj.(*unstructured.Unstructured))
	if err != nil {
		t.Fatal(err)
	}
	if modified {
		t.Error("should not have updated")
	}
}

func TestBuilderDoNoMatchError(t *testing.T) {
	for _, tc := range []struct {
		name      string
		resource  string
		expectErr bool
	}{
		{
			name: "basic",
			resource: `{
  "kind": "UnknownType",
  "apiVersion": "config.openshift.io/v1",
  "metadata": {
     "name": "cluster"
  }
}`,
			expectErr: true,
		},

		{
			name: "delete annotation",
			resource: `{
  "kind": "UnknownType",
  "apiVersion": "config.openshift.io/v1",
  "metadata": {
     "name": "cluster",
     "annotations": {
        "release.openshift.io/delete": "true"
     }
  }
}`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			var m manifest.Manifest
			err := json.Unmarshal([]byte(tc.resource), &m)
			if err != nil {
				t.Fatal(err)
			}

			fakeClient := &fake.FakeDynamicClient{}
			// client always returns a NoKindMatchError
			fakeClient.AddReactor("*", "*", func(action clientgotesting.Action) (handled bool, ret runtime.Object, err error) {
				return true, nil, &meta.NoKindMatchError{
					GroupKind: schema.GroupKind{
						Group: action.GetResource().Group,
						Kind:  "UnknownType",
					},
				}
			})
			builder, err := NewGenericBuilder(fakeClient.Resource(schema.GroupVersionResource{
				Group:    m.GVK.Group,
				Version:  m.GVK.Version,
				Resource: "UnknownType",
			}), m)
			if err != nil {
				t.Fatal(err)
			}
			err = builder.Do(ctx)
			if tc.expectErr {
				if err == nil {
					t.Fatal("expected error, got none")
				}
				if !meta.IsNoMatchError(err) {
					t.Fatalf("expected error of type IsNoMatchError, instead got: %T", err)
				}
			} else if err != nil {
				t.Fatal(err)
			}
		})
	}
}

package internal

import (
	"context"
	"testing"

	"k8s.io/apimachinery/pkg/runtime/schema"

	"k8s.io/apimachinery/pkg/runtime"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"k8s.io/client-go/dynamic/fake"
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
		fakeClient.Resource(
			schema.GroupVersionResource{Group: "config.openshift.io", Version: "v1", Resource: "featuregates"},
		),
		obj.(*unstructured.Unstructured),
	)
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
		fakeClient.Resource(
			schema.GroupVersionResource{Group: "config.openshift.io", Version: "v1", Resource: "featuregates"},
		),
		obj.(*unstructured.Unstructured),
	)
	if err != nil {
		t.Fatal(err)
	}
	if modified {
		t.Error("should not have updated")
	}
}

package cvo

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"k8s.io/apimachinery/pkg/runtime"

	"github.com/davecgh/go-spew/spew"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/wait"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	"k8s.io/client-go/rest"
	clientgotesting "k8s.io/client-go/testing"

	configv1 "github.com/openshift/api/config/v1"
	"github.com/openshift/cluster-version-operator/lib"
	"github.com/openshift/cluster-version-operator/lib/resourcebuilder"
	"github.com/openshift/cluster-version-operator/pkg/cvo/internal"
)

func TestHasRequeueOnErrorAnnotation(t *testing.T) {
	tests := []struct {
		annos map[string]string

		exp     bool
		experrs []string
	}{{
		annos:   nil,
		exp:     false,
		experrs: nil,
	}, {
		annos:   map[string]string{"dummy": "dummy"},
		exp:     false,
		experrs: nil,
	}, {
		annos:   map[string]string{RequeueOnErrorAnnotationKey: "NoMatch"},
		exp:     true,
		experrs: []string{"NoMatch"},
	}, {
		annos:   map[string]string{RequeueOnErrorAnnotationKey: "NoMatch,NotFound"},
		exp:     true,
		experrs: []string{"NoMatch", "NotFound"},
	}}
	for idx, test := range tests {
		t.Run(fmt.Sprintf("test#%d", idx), func(t *testing.T) {
			got, goterrs := hasRequeueOnErrorAnnotation(test.annos)
			if got != test.exp {
				t.Fatalf("expected %v got %v", test.exp, got)
			}
			if !reflect.DeepEqual(goterrs, test.experrs) {
				t.Fatalf("expected %v got %v", test.exp, got)
			}
		})
	}
}

func TestShouldRequeueOnErr(t *testing.T) {
	tests := []struct {
		err      error
		manifest string
		exp      bool
	}{{
		err: nil,
		manifest: `{
			"apiVersion": "v1",
			"kind": "ConfigMap"
		}`,

		exp: false,
	}, {
		err: fmt.Errorf("random error"),
		manifest: `{
			"apiVersion": "v1",
			"kind": "ConfigMap"
		}`,

		exp: false,
	}, {
		err: &meta.NoResourceMatchError{},
		manifest: `{
			"apiVersion": "v1",
			"kind": "ConfigMap"
		}`,

		exp: false,
	}, {
		err: &updateError{cause: &meta.NoResourceMatchError{}},
		manifest: `{
			"apiVersion": "v1",
			"kind": "ConfigMap"
		}`,

		exp: false,
	}, {
		err: &meta.NoResourceMatchError{},
		manifest: `{
			"apiVersion": "v1",
			"kind": "ConfigMap",
			"metadata": {
				"annotations": {
					"v1.cluster-version-operator.operators.openshift.io/requeue-on-error": "NoMatch"
				}
			}
		}`,

		exp: true,
	}, {
		err: &updateError{cause: &meta.NoResourceMatchError{}},
		manifest: `{
			"apiVersion": "v1",
			"kind": "ConfigMap",
			"metadata": {
				"annotations": {
					"v1.cluster-version-operator.operators.openshift.io/requeue-on-error": "NoMatch"
				}
			}
		}`,

		exp: true,
	}, {
		err: &meta.NoResourceMatchError{},
		manifest: `{
			"apiVersion": "v1",
			"kind": "ConfigMap",
			"metadata": {
				"annotations": {
					"v1.cluster-version-operator.operators.openshift.io/requeue-on-error": "NotFound"
				}
			}
		}`,

		exp: false,
	}, {
		err: &updateError{cause: &meta.NoResourceMatchError{}},
		manifest: `{
			"apiVersion": "v1",
			"kind": "ConfigMap",
			"metadata": {
				"annotations": {
					"v1.cluster-version-operator.operators.openshift.io/requeue-on-error": "NotFound"
				}
			}
		}`,

		exp: false,
	}, {
		err: apierrors.NewInternalError(fmt.Errorf("dummy")),
		manifest: `{
			"apiVersion": "v1",
			"kind": "ConfigMap",
			"metadata": {
				"annotations": {
					"v1.cluster-version-operator.operators.openshift.io/requeue-on-error": "NoMatch"
				}
			}
		}`,

		exp: false,
	}, {
		err: &updateError{cause: apierrors.NewInternalError(fmt.Errorf("dummy"))},
		manifest: `{
			"apiVersion": "v1",
			"kind": "ConfigMap",
			"metadata": {
				"annotations": {
					"v1.cluster-version-operator.operators.openshift.io/requeue-on-error": "NoMatch"
				}
			}
		}`,

		exp: false,
	}, {
		err: &updateError{cause: &resourcebuilder.RetryLaterError{}},
		manifest: `{
			"apiVersion": "v1",
			"kind": "ConfigMap"
		}`,

		exp: true,
	}}
	for idx, test := range tests {
		t.Run(fmt.Sprintf("test#%d", idx), func(t *testing.T) {
			var manifest lib.Manifest
			if err := json.Unmarshal([]byte(test.manifest), &manifest); err != nil {
				t.Fatal(err)
			}
			if got := shouldRequeueOnErr(test.err, &manifest); got != test.exp {
				t.Fatalf("expected %v got %v", test.exp, got)
			}
		})
	}
}

func TestSyncUpdatePayload(t *testing.T) {
	tests := []struct {
		manifests []string
		reactors  map[action]error

		check   func(*testing.T, []action)
		wantErr bool
	}{{
		manifests: []string{
			`{
				"apiVersion": "test.cvo.io/v1",
				"kind": "TestA",
				"metadata": {
					"namespace": "default",
					"name": "testa"
				}
			}`,
			`{
				"apiVersion": "test.cvo.io/v1",
				"kind": "TestB",
				"metadata": {
					"namespace": "default",
					"name": "testb"
				}
			}`,
		},
		reactors: map[action]error{},
		check: func(t *testing.T, actions []action) {
			if len(actions) != 2 {
				spew.Dump(actions)
				t.Fatalf("unexpected %d actions", len(actions))
			}

			if got, exp := actions[0], (newAction(schema.GroupVersionKind{"test.cvo.io", "v1", "TestA"}, "default", "testa")); !reflect.DeepEqual(got, exp) {
				t.Fatalf("expected: %s got: %s", spew.Sdump(exp), spew.Sdump(got))
			}
			if got, exp := actions[1], (newAction(schema.GroupVersionKind{"test.cvo.io", "v1", "TestB"}, "default", "testb")); !reflect.DeepEqual(got, exp) {
				t.Fatalf("expected: %s got: %s", spew.Sdump(exp), spew.Sdump(got))
			}
		},
	}, {
		manifests: []string{
			`{
				"apiVersion": "test.cvo.io/v1",
				"kind": "TestA",
				"metadata": {
					"namespace": "default",
					"name": "testa"
				}
			}`,
			`{
				"apiVersion": "test.cvo.io/v1",
				"kind": "TestB",
				"metadata": {
					"namespace": "default",
					"name": "testb"
				}
			}`,
		},
		reactors: map[action]error{
			newAction(schema.GroupVersionKind{"test.cvo.io", "v1", "TestA"}, "default", "testa"): &meta.NoResourceMatchError{},
		},
		wantErr: true,
		check: func(t *testing.T, actions []action) {
			if len(actions) != 3 {
				spew.Dump(actions)
				t.Fatalf("unexpected %d actions", len(actions))
			}

			if got, exp := actions[0], (newAction(schema.GroupVersionKind{"test.cvo.io", "v1", "TestA"}, "default", "testa")); !reflect.DeepEqual(got, exp) {
				t.Fatalf("expected: %s got: %s", spew.Sdump(exp), spew.Sdump(got))
			}
		},
	}, {
		manifests: []string{
			`{
				"apiVersion": "test.cvo.io/v1",
				"kind": "TestA",
				"metadata": {
					"namespace": "default",
					"name": "testa",
					"annotations": {
						"v1.cluster-version-operator.operators.openshift.io/requeue-on-error": "NoMatch"
					}
				}
			}`,
			`{
				"apiVersion": "test.cvo.io/v1",
				"kind": "TestB",
				"metadata": {
					"namespace": "default",
					"name": "testb"
				}
			}`,
		},
		reactors: map[action]error{
			newAction(schema.GroupVersionKind{"test.cvo.io", "v1", "TestA"}, "default", "testa"): &meta.NoResourceMatchError{},
		},
		wantErr: true,
		check: func(t *testing.T, actions []action) {
			if len(actions) != 7 {
				spew.Dump(actions)
				t.Fatalf("unexpected %d actions", len(actions))
			}

			if got, exp := actions[0], (newAction(schema.GroupVersionKind{"test.cvo.io", "v1", "TestA"}, "default", "testa")); !reflect.DeepEqual(got, exp) {
				t.Fatalf("expected: %s got: %s", spew.Sdump(exp), spew.Sdump(got))
			}
			if got, exp := actions[3], (newAction(schema.GroupVersionKind{"test.cvo.io", "v1", "TestB"}, "default", "testb")); !reflect.DeepEqual(got, exp) {
				t.Fatalf("expected: %s got: %s", spew.Sdump(exp), spew.Sdump(got))
			}
			if got, exp := actions[4], (newAction(schema.GroupVersionKind{"test.cvo.io", "v1", "TestA"}, "default", "testa")); !reflect.DeepEqual(got, exp) {
				t.Fatalf("expected: %s got: %s", spew.Sdump(exp), spew.Sdump(got))
			}
		},
	}, {
		manifests: []string{
			`{
				"apiVersion": "test.cvo.io/v1",
				"kind": "TestA",
				"metadata": {
					"namespace": "default",
					"name": "testa",
					"annotations": {
						"v1.cluster-version-operator.operators.openshift.io/requeue-on-error": "NoMatch"
					}
				}
			}`,
			`{
				"apiVersion": "test.cvo.io/v1",
				"kind": "TestB",
				"metadata": {
					"namespace": "default",
					"name": "testb",
					"annotations": {
						"v1.cluster-version-operator.operators.openshift.io/requeue-on-error": "NoMatch"
					}
				}
			}`,
		},
		reactors: map[action]error{
			newAction(schema.GroupVersionKind{"test.cvo.io", "v1", "TestA"}, "default", "testa"): &meta.NoResourceMatchError{},
			newAction(schema.GroupVersionKind{"test.cvo.io", "v1", "TestB"}, "default", "testb"): &meta.NoResourceMatchError{},
		},
		wantErr: true,
		check: func(t *testing.T, actions []action) {
			if len(actions) != 9 {
				spew.Dump(actions)
				t.Fatalf("unexpected %d actions", len(actions))
			}

			if got, exp := actions[0], (newAction(schema.GroupVersionKind{"test.cvo.io", "v1", "TestA"}, "default", "testa")); !reflect.DeepEqual(got, exp) {
				t.Fatalf("expected: %s got: %s", spew.Sdump(exp), spew.Sdump(got))
			}
			if got, exp := actions[3], (newAction(schema.GroupVersionKind{"test.cvo.io", "v1", "TestB"}, "default", "testb")); !reflect.DeepEqual(got, exp) {
				t.Fatalf("expected: %s got: %s", spew.Sdump(exp), spew.Sdump(got))
			}
			if got, exp := actions[6], (newAction(schema.GroupVersionKind{"test.cvo.io", "v1", "TestA"}, "default", "testa")); !reflect.DeepEqual(got, exp) {
				t.Fatalf("expected: %s got: %s", spew.Sdump(exp), spew.Sdump(got))
			}
		},
	}}
	for idx, test := range tests {
		t.Run(fmt.Sprintf("test#%d", idx), func(t *testing.T) {
			var manifests []lib.Manifest
			for _, s := range test.manifests {
				m := lib.Manifest{}
				if err := json.Unmarshal([]byte(s), &m); err != nil {
					t.Fatal(err)
				}
				manifests = append(manifests, m)
			}

			up := &updatePayload{ReleaseImage: "test", ReleaseVersion: "v0.0.0", Manifests: manifests}
			op := &Operator{}
			op.resourceBuilder = op.defaultResourceBuilder
			op.syncBackoff = wait.Backoff{Steps: 3}
			config := &configv1.ClusterVersion{}
			r := &recorder{}
			testMapper := resourcebuilder.NewResourceMapper()
			testMapper.RegisterGVK(schema.GroupVersionKind{"test.cvo.io", "v1", "TestA"}, newTestBuilder(r, test.reactors))
			testMapper.RegisterGVK(schema.GroupVersionKind{"test.cvo.io", "v1", "TestB"}, newTestBuilder(r, test.reactors))
			testMapper.AddToMap(resourcebuilder.Mapper)

			err := op.syncUpdatePayload(config, up)
			test.check(t, r.actions)

			if (err != nil) != test.wantErr {
				t.Fatalf("unexpected err: %v", err)
			}
		})
	}
}

func TestSyncUpdatePayloadGeneric(t *testing.T) {
	tests := []struct {
		manifests []string
		modifiers []resourcebuilder.MetaV1ObjectModifierFunc

		check func(t *testing.T, client *dynamicfake.FakeDynamicClient)
	}{
		{
			manifests: []string{
				`{
				"apiVersion": "test.cvo.io/v1",
				"kind": "TestA",
				"metadata": {
					"namespace": "default",
					"name": "testa"
				}
			}`,
				`{
				"apiVersion": "test.cvo.io/v1",
				"kind": "TestB",
				"metadata": {
					"namespace": "default",
					"name": "testb"
				}
			}`,
			},
			check: func(t *testing.T, client *dynamicfake.FakeDynamicClient) {
				actions := client.Actions()
				if len(actions) != 4 {
					spew.Dump(actions)
					t.Fatal("expected only 4 actions")
				}

				got := actions[1].(clientgotesting.CreateAction).GetObject()
				exp := &unstructured.Unstructured{
					Object: map[string]interface{}{
						"apiVersion": "test.cvo.io/v1",
						"kind":       "TestA",
						"metadata": map[string]interface{}{
							"name":      "testa",
							"namespace": "default",
						},
					},
				}
				if !reflect.DeepEqual(got, exp) {
					t.Fatalf("expected: %s got: %s", spew.Sdump(exp), spew.Sdump(got))
				}

				got = actions[3].(clientgotesting.CreateAction).GetObject()
				exp = &unstructured.Unstructured{
					Object: map[string]interface{}{
						"apiVersion": "test.cvo.io/v1",
						"kind":       "TestB",
						"metadata": map[string]interface{}{
							"name":      "testb",
							"namespace": "default",
						},
					},
				}
				if !reflect.DeepEqual(got, exp) {
					t.Fatalf("expected: %s got: %s", spew.Sdump(exp), spew.Sdump(got))
				}
			},
		},
		{
			modifiers: []resourcebuilder.MetaV1ObjectModifierFunc{
				func(obj metav1.Object) {
					m := obj.GetLabels()
					if m == nil {
						m = make(map[string]string)
					}
					m["test/label"] = "a"
					obj.SetLabels(m)
				},
			},
			manifests: []string{
				`{
					"apiVersion": "test.cvo.io/v1",
					"kind": "TestA",
					"metadata": {
						"namespace": "default",
						"name": "testa"
					}
				}`,
				`{
					"apiVersion": "test.cvo.io/v1",
					"kind": "TestB",
					"metadata": {
						"namespace": "default",
						"name": "testb"
					}
				}`,
			},
			check: func(t *testing.T, client *dynamicfake.FakeDynamicClient) {
				actions := client.Actions()
				if len(actions) != 4 {
					spew.Dump(actions)
					t.Fatalf("got %d actions", len(actions))
				}

				got := actions[1].(clientgotesting.CreateAction).GetObject()
				exp := &unstructured.Unstructured{
					Object: map[string]interface{}{
						"apiVersion": "test.cvo.io/v1",
						"kind":       "TestA",
						"metadata": map[string]interface{}{
							"name":      "testa",
							"namespace": "default",
							"labels":    map[string]interface{}{"test/label": "a"},
						},
					},
				}
				if !reflect.DeepEqual(got, exp) {
					t.Fatalf("expected: %s got: %s", spew.Sdump(exp), spew.Sdump(got))
				}

				got = actions[3].(clientgotesting.CreateAction).GetObject()
				exp = &unstructured.Unstructured{
					Object: map[string]interface{}{
						"apiVersion": "test.cvo.io/v1",
						"kind":       "TestB",
						"metadata": map[string]interface{}{
							"name":      "testb",
							"namespace": "default",
							"labels":    map[string]interface{}{"test/label": "a"},
						},
					},
				}
				if !reflect.DeepEqual(got, exp) {
					t.Fatalf("expected: %s got: %s", spew.Sdump(exp), spew.Sdump(got))
				}
			},
		},
	}
	for idx, test := range tests {
		t.Run(fmt.Sprintf("test#%d", idx), func(t *testing.T) {
			var manifests []lib.Manifest
			for _, s := range test.manifests {
				m := lib.Manifest{}
				if err := json.Unmarshal([]byte(s), &m); err != nil {
					t.Fatal(err)
				}
				manifests = append(manifests, m)
			}

			dynamicScheme := runtime.NewScheme()
			dynamicScheme.AddKnownTypeWithName(schema.GroupVersionKind{Group: "test.cvo.io", Version: "v1", Kind: "TestA"}, &unstructured.Unstructured{})
			dynamicScheme.AddKnownTypeWithName(schema.GroupVersionKind{Group: "test.cvo.io", Version: "v1", Kind: "TestB"}, &unstructured.Unstructured{})
			dynamicClient := dynamicfake.NewSimpleDynamicClient(dynamicScheme)

			up := &updatePayload{ReleaseImage: "test", ReleaseVersion: "v0.0.0", Manifests: manifests}
			op := &Operator{}
			op.resourceBuilder = func(version string) ResourceBuilder {
				return &testResourceBuilder{
					client:    dynamicClient,
					modifiers: test.modifiers,
				}
			}
			op.syncBackoff = wait.Backoff{Steps: 3}
			config := &configv1.ClusterVersion{}

			err := op.syncUpdatePayload(config, up)
			if err != nil {
				t.Fatal(err)
			}
			test.check(t, dynamicClient)
		})
	}
}

// testResourceBuilder uses a fake dynamic client to exercise the generic builder in tests.
type testResourceBuilder struct {
	client    *dynamicfake.FakeDynamicClient
	modifiers []resourcebuilder.MetaV1ObjectModifierFunc
}

func (b *testResourceBuilder) Apply(m *lib.Manifest) error {
	ns := m.Object().GetNamespace()
	fakeGVR := schema.GroupVersionResource{Group: m.GVK.Group, Version: m.GVK.Version, Resource: strings.ToLower(m.GVK.Kind)}
	client := b.client.Resource(fakeGVR).Namespace(ns)
	builder, err := internal.NewGenericBuilder(client, *m)
	if err != nil {
		return err
	}
	for _, m := range b.modifiers {
		builder = builder.WithModifier(m)
	}
	return builder.Do()
}

type testBuilder struct {
	*recorder
	reactors  map[action]error
	modifiers []resourcebuilder.MetaV1ObjectModifierFunc

	m *lib.Manifest
}

func (t *testBuilder) WithModifier(m resourcebuilder.MetaV1ObjectModifierFunc) resourcebuilder.Interface {
	t.modifiers = append(t.modifiers, m)
	return t
}

func (t *testBuilder) Do() error {
	a := t.recorder.Invoke(t.m.GVK, t.m.Object().GetNamespace(), t.m.Object().GetName())
	return t.reactors[a]
}

func newTestBuilder(r *recorder, rts map[action]error) resourcebuilder.NewInteraceFunc {
	return func(_ *rest.Config, m lib.Manifest) resourcebuilder.Interface {
		return &testBuilder{recorder: r, reactors: rts, m: &m}
	}
}

type recorder struct {
	actions []action
}

func (r *recorder) Invoke(gvk schema.GroupVersionKind, namespace, name string) action {
	action := action{GVK: gvk, Namespace: namespace, Name: name}
	r.actions = append(r.actions, action)
	return action
}

type action struct {
	GVK       schema.GroupVersionKind
	Namespace string
	Name      string
}

func newAction(gvk schema.GroupVersionKind, namespace, name string) action {
	return action{GVK: gvk, Namespace: namespace, Name: name}
}

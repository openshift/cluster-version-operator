package cvo

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"testing"

	"k8s.io/client-go/tools/record"

	"github.com/davecgh/go-spew/spew"

	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/diff"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	"k8s.io/client-go/rest"
	clientgotesting "k8s.io/client-go/testing"
	"k8s.io/klog/v2"

	configv1 "github.com/openshift/api/config/v1"
	"github.com/openshift/cluster-version-operator/lib/resourcebuilder"
	"github.com/openshift/cluster-version-operator/pkg/cvo/internal"
	"github.com/openshift/cluster-version-operator/pkg/payload"
	"github.com/openshift/cluster-version-operator/pkg/payload/precondition"
	"github.com/openshift/library-go/pkg/manifest"
)

func init() {
	klog.InitFlags(flag.CommandLine)
	_ = flag.CommandLine.Lookup("v").Value.Set("2")
	_ = flag.CommandLine.Lookup("alsologtostderr").Value.Set("true")
}

func Test_SyncWorker_apply(t *testing.T) {
	tests := []struct {
		name        string
		manifests   []string
		reactors    map[action]error
		cancelAfter int

		check   func(*testing.T, []action)
		wantErr bool
	}{{
		name: "successful creation",
		manifests: []string{
			`{
				"apiVersion": "test.cvo.io/v1",
				"kind": "TestA",
				"metadata": {
					"namespace": "default",
					"name": "testa",
					"annotations": {
						"include.release.openshift.io/self-managed-high-availability": "true"
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
						"include.release.openshift.io/self-managed-high-availability": "true"
					}
				}
			}`,
		},
		reactors: map[action]error{},
		check: func(t *testing.T, actions []action) {
			if len(actions) != 2 {
				spew.Dump(actions)
				t.Fatalf("unexpected %d actions", len(actions))
			}

			if got, exp := actions[0], (newAction(schema.GroupVersionKind{Group: "test.cvo.io", Version: "v1", Kind: "TestA"}, "default", "testa")); !reflect.DeepEqual(got, exp) {
				t.Fatalf("%s", diff.ObjectReflectDiff(exp, got))
			}
			if got, exp := actions[1], (newAction(schema.GroupVersionKind{Group: "test.cvo.io", Version: "v1", Kind: "TestB"}, "default", "testb")); !reflect.DeepEqual(got, exp) {
				t.Fatalf("%s", diff.ObjectReflectDiff(exp, got))
			}
		},
	}, {
		name: "unknown resource failures",
		manifests: []string{
			`{
				"apiVersion": "test.cvo.io/v1",
				"kind": "TestA",
				"metadata": {
					"namespace": "default",
					"name": "testa",
					"annotations": {
						"include.release.openshift.io/self-managed-high-availability": "true"
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
						"include.release.openshift.io/self-managed-high-availability": "true"
					}
				}
			}`,
		},
		reactors: map[action]error{
			newAction(schema.GroupVersionKind{Group: "test.cvo.io", Version: "v1", Kind: "TestA"}, "default", "testa"): &meta.NoResourceMatchError{},
		},
		cancelAfter: 2,
		wantErr:     true,
		check: func(t *testing.T, actions []action) {
			if len(actions) < 1 {
				spew.Dump(actions)
				t.Fatalf("unexpected %d actions", len(actions))
			}

			exp := newAction(schema.GroupVersionKind{Group: "test.cvo.io", Version: "v1", Kind: "TestA"}, "default", "testa")
			for i, got := range actions {
				if !reflect.DeepEqual(got, exp) {
					t.Fatalf("unexpected action %d: %s", i, diff.ObjectReflectDiff(exp, got))
				}
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
                                                "include.release.openshift.io/self-managed-high-availability": "true",
						"capability.openshift.io/name": "openshift-samples+baremetal"
                                        }
                                }
                        }`,
		},
		reactors: map[action]error{},
		check: func(t *testing.T, actions []action) {
			if len(actions) != 0 {
				spew.Dump(actions)
				t.Fatalf("unexpected %d actions", len(actions))
			}
		},
	}}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var manifests []manifest.Manifest
			for _, s := range test.manifests {
				m := manifest.Manifest{}
				if err := json.Unmarshal([]byte(s), &m); err != nil {
					t.Fatal(err)
				}
				manifests = append(manifests, m)
			}

			up := &payload.Update{
				Release: configv1.Release{
					Version: "v0.0.0",
					Image:   "test",
				},
				Manifests: manifests,
			}
			r := &recorder{}
			testMapper := resourcebuilder.NewResourceMapper()
			testMapper.RegisterGVK(schema.GroupVersionKind{Group: "test.cvo.io", Version: "v1", Kind: "TestA"}, newTestBuilder(r, test.reactors))
			testMapper.RegisterGVK(schema.GroupVersionKind{Group: "test.cvo.io", Version: "v1", Kind: "TestB"}, newTestBuilder(r, test.reactors))
			testMapper.AddToMap(resourcebuilder.Mapper)

			worker := &SyncWorker{eventRecorder: record.NewFakeRecorder(100)}
			worker.backoff.Steps = 2
			worker.builder = NewResourceBuilder(nil, nil, nil, nil)

			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			worker.builder = &cancelAfterErrorBuilder{
				builder:         worker.builder,
				cancel:          cancel,
				remainingErrors: test.cancelAfter,
			}
			worker.payload = up

			err := worker.apply(ctx, &SyncWork{}, 1, worker.Status())
			if !test.wantErr && err != nil {
				t.Fatal(err)
			}
			if test.wantErr && err == nil {
				t.Fatal("expected error but apply was successful")
			}
			test.check(t, r.actions)
		})
	}
}

type cancelAfterErrorBuilder struct {
	builder         payload.ResourceBuilder
	cancel          func()
	remainingErrors int
}

func (b *cancelAfterErrorBuilder) Apply(ctx context.Context, m *manifest.Manifest, state payload.State) error {
	err := b.builder.Apply(ctx, m, state)
	if err != nil {
		if b.remainingErrors == 0 {
			b.cancel()
		} else {
			b.remainingErrors--
		}
	}
	return err
}

func Test_SyncWorker_apply_generic(t *testing.T) {
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
					"name": "testa",
					"annotations": {
						"include.release.openshift.io/self-managed-high-availability": "true"
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
						"include.release.openshift.io/self-managed-high-availability": "true"
					}
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
							"annotations": map[string]interface{}{
								"include.release.openshift.io/self-managed-high-availability": "true",
							},
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
							"annotations": map[string]interface{}{
								"include.release.openshift.io/self-managed-high-availability": "true",
							},
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
						"name": "testa",
						"annotations": {
							"include.release.openshift.io/self-managed-high-availability": "true"
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
							"include.release.openshift.io/self-managed-high-availability": "true"
						}
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
							"annotations": map[string]interface{}{
								"include.release.openshift.io/self-managed-high-availability": "true",
							},
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
							"annotations": map[string]interface{}{
								"include.release.openshift.io/self-managed-high-availability": "true",
							},
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
			var manifests []manifest.Manifest
			for _, s := range test.manifests {
				m := manifest.Manifest{}
				if err := json.Unmarshal([]byte(s), &m); err != nil {
					t.Fatal(err)
				}
				manifests = append(manifests, m)
			}

			dynamicScheme := runtime.NewScheme()
			dynamicScheme.AddKnownTypeWithName(schema.GroupVersionKind{Group: "test.cvo.io", Version: "v1", Kind: "TestA"}, &unstructured.Unstructured{})
			dynamicScheme.AddKnownTypeWithName(schema.GroupVersionKind{Group: "test.cvo.io", Version: "v1", Kind: "TestB"}, &unstructured.Unstructured{})
			dynamicClient := dynamicfake.NewSimpleDynamicClient(dynamicScheme)

			up := &payload.Update{
				Release: configv1.Release{
					Version: "v0.0.0",
					Image:   "test",
				},
				Manifests: manifests,
			}
			worker := &SyncWorker{eventRecorder: record.NewFakeRecorder(100)}
			worker.backoff.Steps = 1
			worker.builder = &testResourceBuilder{
				client:    dynamicClient,
				modifiers: test.modifiers,
			}
			worker.payload = up
			ctx := context.Background()
			err := worker.apply(ctx, &SyncWork{}, 1, worker.Status())
			if err != nil {
				t.Fatal(err)
			}
			test.check(t, dynamicClient)
		})
	}
}

type testBuilder struct {
	*recorder
	reactors  map[action]error
	modifiers []resourcebuilder.MetaV1ObjectModifierFunc
	mode      resourcebuilder.Mode

	m *manifest.Manifest
}

func (t *testBuilder) WithMode(m resourcebuilder.Mode) resourcebuilder.Interface {
	t.mode = m
	return t
}

func (t *testBuilder) WithModifier(m resourcebuilder.MetaV1ObjectModifierFunc) resourcebuilder.Interface {
	t.modifiers = append(t.modifiers, m)
	return t
}

func (t *testBuilder) Do(_ context.Context) error {
	a := t.recorder.Invoke(t.m.GVK, t.m.Obj.GetNamespace(), t.m.Obj.GetName())
	return t.reactors[a]
}

func newTestBuilder(r *recorder, rts map[action]error) resourcebuilder.NewInterfaceFunc {
	return func(_ *rest.Config, m manifest.Manifest) resourcebuilder.Interface {
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

type fakeSyncRecorder struct {
	Returns         *SyncWorkerStatus
	Updates         []configv1.Update
	initializedFunc func() bool
}

func (r *fakeSyncRecorder) Initialized() bool {
	return r.initializedFunc == nil || r.initializedFunc()
}

func (r *fakeSyncRecorder) StatusCh() <-chan SyncWorkerStatus {
	ch := make(chan SyncWorkerStatus)
	close(ch)
	return ch
}

// Inform the sync worker about activity for a managed resource.
func (r *fakeSyncRecorder) NotifyAboutManagedResourceActivity(message string) {
}

func (r *fakeSyncRecorder) Start(ctx context.Context, maxWorkers int) {
}

func (r *fakeSyncRecorder) Update(ctx context.Context, generation int64, desired configv1.Update, config *configv1.ClusterVersion, state payload.State) *SyncWorkerStatus {
	r.Updates = append(r.Updates, desired)
	return r.Returns
}

type fakeDirectoryRetriever struct {
	lock sync.Mutex

	Info PayloadInfo
	Err  error
}

func (r *fakeDirectoryRetriever) Set(info PayloadInfo, err error) {
	r.lock.Lock()
	defer r.lock.Unlock()
	r.Info = info
	r.Err = err
}

func (r *fakeDirectoryRetriever) RetrievePayload(ctx context.Context, update configv1.Update) (PayloadInfo, error) {
	r.lock.Lock()
	defer r.lock.Unlock()
	return r.Info, r.Err
}

// testResourceBuilder uses a fake dynamic client to exercise the generic builder in tests.
type testResourceBuilder struct {
	client    *dynamicfake.FakeDynamicClient
	modifiers []resourcebuilder.MetaV1ObjectModifierFunc
}

func (b *testResourceBuilder) Apply(ctx context.Context, m *manifest.Manifest, state payload.State) error {
	ns := m.Obj.GetNamespace()
	fakeGVR := schema.GroupVersionResource{Group: m.GVK.Group, Version: m.GVK.Version, Resource: strings.ToLower(m.GVK.Kind)}
	client := b.client.Resource(fakeGVR).Namespace(ns)
	builder, err := internal.NewGenericBuilder(client, *m)
	if err != nil {
		return err
	}
	for _, m := range b.modifiers {
		builder = builder.WithModifier(m)
	}
	return builder.Do(ctx)
}

type testPrecondition struct {
	attempt      int
	SuccessAfter int
}

func (pf *testPrecondition) Name() string {
	return fmt.Sprintf("TestPrecondition SuccessAfter: %d", pf.SuccessAfter)
}

func (pf *testPrecondition) Run(_ context.Context, _ precondition.ReleaseContext) error {
	if pf.SuccessAfter == 0 {
		return nil
	}
	pf.attempt++
	if pf.attempt >= pf.SuccessAfter {
		return nil
	}
	return &precondition.Error{
		Nested:  nil,
		Reason:  "CheckFailure",
		Message: fmt.Sprintf("failing, attempt: %d will succeed after %d attempt", pf.attempt, pf.SuccessAfter),
		Name:    pf.Name(),
	}
}

type testPreconditionAlwaysFail struct {
	PreConditionName string
}

func (pf *testPreconditionAlwaysFail) Name() string {
	return pf.PreConditionName
}

func (pf *testPreconditionAlwaysFail) Run(_ context.Context, _ precondition.ReleaseContext) error {
	return &precondition.Error{
		Nested:  nil,
		Reason:  "CheckFailure",
		Message: fmt.Sprintf("%s will always fail.", pf.Name()),
		Name:    pf.Name(),
	}
}

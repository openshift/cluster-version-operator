package agenticrun

import (
	"context"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	kerrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/apimachinery/pkg/watch"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	clienttesting "k8s.io/client-go/testing"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	configv1 "github.com/openshift/api/config/v1"
	agenticrunv1alpha1 "github.com/openshift/lightspeed-agentic-operator/api/v1alpha1"

	"github.com/openshift/cluster-version-operator/pkg/readiness"
)

func TestRunInformer(t *testing.T) {
	for _, absentInitially := range []bool{false, true} {
		t.Run(fmt.Sprintf("API initially absent=%t", absentInitially), func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			gvr := agenticrunv1alpha1.GroupVersion.WithResource("agenticruns")
			dc := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(), map[schema.GroupVersionResource]string{gvr: "AgenticRunList"})
			var lists atomic.Int32
			dc.PrependReactor("list", "agenticruns", func(action clienttesting.Action) (bool, runtime.Object, error) {
				if action.GetNamespace() != "custom-namespace" || action.(clienttesting.ListAction).GetListRestrictions().Labels.String() != "agentic.openshift.io/source=cluster-version-operator" {
					return true, nil, fmt.Errorf("unexpected list scope: %#v", action)
				}
				if lists.Add(1) == 1 && absentInitially {
					return true, nil, kerrors.NewNotFound(gvr.GroupResource(), "")
				}
				return true, &unstructured.UnstructuredList{Object: map[string]interface{}{"metadata": map[string]interface{}{"resourceVersion": "1"}}}, nil
			})
			watches := make(chan *watch.RaceFreeFakeWatcher, 4)
			dc.PrependWatchReactor("agenticruns", func(action clienttesting.Action) (bool, watch.Interface, error) {
				if action.GetNamespace() != "custom-namespace" || action.(clienttesting.WatchAction).GetWatchRestrictions().Labels.String() != "agentic.openshift.io/source=cluster-version-operator" {
					return true, nil, fmt.Errorf("unexpected watch scope: %#v", action)
				}
				w := watch.NewRaceFreeFake()
				watches <- w
				return true, w, nil
			})
			c := NewController(nil, nil, dc, nil, nil, nil)
			c.config.Namespace = "custom-namespace"
			defer c.queue.ShutDown()
			done := make(chan error, 1)
			go func() { done <- c.RunInformer(ctx) }()
			nextWatch := func() *watch.RaceFreeFakeWatcher {
				t.Helper()
				select {
				case w := <-watches:
					return w
				case <-ctx.Done():
					t.Fatal("watch did not start or reconnect")
					return nil
				}
			}
			w := nextWatch()
			obj := &unstructured.Unstructured{Object: map[string]interface{}{"apiVersion": "agentic.openshift.io/v1alpha1", "kind": "AgenticRun", "metadata": map[string]interface{}{"name": "test", "namespace": c.config.Namespace, "resourceVersion": "2"}}}
			w.Add(obj)
			w.Modify(obj)
			// Adds and status updates must not launch another reconciliation.
			select {
			case <-time.After(50 * time.Millisecond):
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			}
			if c.queue.Len() != 0 {
				t.Fatal("add or update enqueued reconciliation")
			}
			w.Delete(obj)
			if err := wait.PollUntilContextCancel(ctx, time.Millisecond, true, func(context.Context) (bool, error) { return c.queue.Len() == 1, nil }); err != nil {
				t.Fatal("deletion did not enqueue reconciliation", err)
			}
			key, _ := c.queue.Get()
			c.queue.Done(key)
			if key != c.queueKey {
				t.Fatalf("queued %v, want %s", key, c.queueKey)
			}
			// Closing the stream must not disable deletion notifications.
			w.Stop()
			w = nextWatch()
			obj.SetResourceVersion("3")
			w.Add(obj)
			w.Delete(obj)
			if err := wait.PollUntilContextCancel(ctx, time.Millisecond, true, func(context.Context) (bool, error) { return c.queue.Len() == 1, nil }); err != nil {
				t.Fatal("deletion after reconnect did not enqueue reconciliation", err)
			}
			key, _ = c.queue.Get()
			c.queue.Done(key)
			// A relist after an expired resourceVersion must also report a run
			// that disappeared while disconnected (a deletion tombstone).
			obj.SetResourceVersion("4")
			w.Add(obj)
			w.Error(&metav1.Status{Status: metav1.StatusFailure, Reason: metav1.StatusReasonExpired, Code: 410})
			if err := wait.PollUntilContextCancel(ctx, time.Millisecond, true, func(context.Context) (bool, error) { return c.queue.Len() == 1, nil }); err != nil {
				t.Fatal("relist did not reconcile the missed deletion", err)
			}
			cancel()
			select {
			case err := <-done:
				if err != nil {
					t.Fatal(err)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("informer did not stop")
			}
		})
	}
}

func TestDeletionRecreatesRunWithoutUpdateRefresh(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	run, err := getAgenticRun("openshift-lightspeed", "4.15.3", "4.16.0", "stable-4.16", updateKindRecommended, prompt, "{}", nil, testSkillsImage)
	if err != nil {
		t.Fatal(err)
	}
	run.UID, run.CreationTimestamp = "original", metav1.Now()
	client := fake.NewClientBuilder().WithObjects(run).Build()
	objMap, err := runtime.DefaultUnstructuredConverter.ToUnstructured(run)
	if err != nil {
		t.Fatal(err)
	}
	obj := &unstructured.Unstructured{Object: objMap}
	obj.SetAPIVersion("agentic.openshift.io/v1alpha1")
	obj.SetKind("AgenticRun")
	dc := dynamicfake.NewSimpleDynamicClient(runtime.NewScheme(), obj)
	w := watch.NewRaceFreeFake()
	watchStarted := make(chan struct{})
	dc.PrependWatchReactor("agenticruns", func(clienttesting.Action) (bool, watch.Interface, error) {
		close(watchStarted)
		return true, w, nil
	})
	c := NewController(func() ([]configv1.Release, []configv1.ConditionalUpdate, error) {
		// This cached target never changes and no update-service fetch occurs.
		return []configv1.Release{{Version: "4.16.0"}}, nil, nil
	}, client, dc, func(string) (*configv1.ClusterVersion, error) {
		return &configv1.ClusterVersion{Spec: configv1.ClusterVersionSpec{Channel: "stable-4.16"}}, nil
	}, func() string { return "4.15.3" }, nil)
	c.config.SkillsImage = testSkillsImage
	c.crdAvailableCache, c.crdLastChecked = true, time.Now()
	defer c.queue.ShutDown()
	check := &recordingReadinessCheck{}
	originalChecks := readiness.AllChecks
	readiness.AllChecks = func() []readiness.Check { return []readiness.Check{check} }
	defer func() { readiness.AllChecks = originalChecks }()
	done := make(chan error, 1)
	go func() { done <- c.RunInformer(ctx) }()
	select {
	case <-watchStarted:
	case <-ctx.Done():
		t.Fatal("watch did not start")
	}
	if err := client.Delete(ctx, run); err != nil {
		t.Fatal(err)
	}
	w.Delete(obj)
	if err := wait.PollUntilContextCancel(ctx, time.Millisecond, true, func(context.Context) (bool, error) { return c.queue.Len() == 1, nil }); err != nil {
		t.Fatal(err)
	}
	key, _ := c.queue.Get()
	err = c.Sync(ctx, key.(string))
	c.queue.Done(key)
	if err != nil {
		t.Fatal(err)
	}
	replacement := &agenticrunv1alpha1.AgenticRun{}
	if err := client.Get(ctx, ctrlruntimeclient.ObjectKeyFromObject(run), replacement); err != nil {
		t.Fatal(err)
	}
	if replacement.UID == run.UID || len(check.targets) != 1 || !strings.Contains(replacement.Spec.Request, `"snapshot":"fresh"`) {
		t.Fatalf("replacement is not a fresh assessment: %+v", replacement)
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

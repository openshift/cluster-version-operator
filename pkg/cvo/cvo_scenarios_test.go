package cvo

import (
	"context"
	"fmt"
	"reflect"
	"strconv"
	"testing"
	"time"

	"github.com/davecgh/go-spew/spew"
	"github.com/google/uuid"

	"k8s.io/apimachinery/pkg/util/diff"
	"k8s.io/apimachinery/pkg/util/wait"

	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	clientgotesting "k8s.io/client-go/testing"
	"k8s.io/client-go/util/workqueue"

	configv1 "github.com/openshift/api/config/v1"
	"github.com/openshift/client-go/config/clientset/versioned/fake"

	"github.com/openshift/cluster-version-operator/lib"
	"github.com/openshift/cluster-version-operator/pkg/payload"
	"github.com/openshift/cluster-version-operator/pkg/payload/precondition"
)

func setupCVOTest(payloadDir string) (*Operator, map[string]runtime.Object, *fake.Clientset, *dynamicfake.FakeDynamicClient, func()) {
	client := &fake.Clientset{}
	client.AddReactor("*", "*", func(action clientgotesting.Action) (handled bool, ret runtime.Object, err error) {
		return false, nil, fmt.Errorf("unexpected client action: %#v", action)
	})
	cvs := make(map[string]runtime.Object)
	client.AddReactor("*", "clusterversions", func(action clientgotesting.Action) (handled bool, ret runtime.Object, err error) {
		switch a := action.(type) {
		case clientgotesting.GetAction:
			obj, ok := cvs[a.GetName()]
			if !ok {
				return true, nil, errors.NewNotFound(schema.GroupResource{Resource: "clusterversions"}, a.GetName())
			}
			return true, obj.DeepCopyObject(), nil
		case clientgotesting.CreateAction:
			obj := a.GetObject().DeepCopyObject().(*configv1.ClusterVersion)
			obj.Generation = 1
			cvs[obj.Name] = obj
			return true, obj, nil
		case clientgotesting.UpdateAction:
			obj := a.GetObject().DeepCopyObject().(*configv1.ClusterVersion)
			existing := cvs[obj.Name].DeepCopyObject().(*configv1.ClusterVersion)
			rv, _ := strconv.Atoi(existing.ResourceVersion)
			nextRV := strconv.Itoa(rv + 1)
			if a.GetSubresource() == "status" {
				existing.Status = obj.Status
			} else {
				existing.Spec = obj.Spec
				existing.ObjectMeta = obj.ObjectMeta
				obj.Generation++
			}
			existing.ResourceVersion = nextRV
			cvs[existing.Name] = existing
			return true, existing, nil
		}
		return false, nil, fmt.Errorf("unexpected client action: %#v", action)
	})

	o := &Operator{
		namespace:                   "test",
		name:                        "version",
		enableDefaultClusterVersion: true,
		queue:                       workqueue.NewNamedRateLimitingQueue(workqueue.DefaultControllerRateLimiter(), "cvo-loop-test"),
		client:                      client,
		cvLister:                    &clientCVLister{client: client},
	}

	dynamicScheme := runtime.NewScheme()
	//dynamicScheme.AddKnownTypeWithName(schema.GroupVersionKind{Group: "test.cvo.io", Version: "v1", Kind: "TestA"}, &unstructured.Unstructured{})
	dynamicScheme.AddKnownTypeWithName(schema.GroupVersionKind{Group: "test.cvo.io", Version: "v1", Kind: "TestB"}, &unstructured.Unstructured{})
	dynamicClient := dynamicfake.NewSimpleDynamicClient(dynamicScheme)

	worker := NewSyncWorker(
		&fakeDirectoryRetriever{Info: PayloadInfo{Directory: payloadDir}},
		&testResourceBuilder{client: dynamicClient},
		time.Second/2,
		wait.Backoff{
			Steps: 1,
		},
		"",
	)
	o.configSync = worker

	return o, cvs, client, dynamicClient, func() { o.queue.ShutDown() }
}

func TestCVO_StartupAndSync(t *testing.T) {
	o, cvs, client, _, shutdownFn := setupCVOTest("testdata/payloadtest")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	defer shutdownFn()
	worker := o.configSync.(*SyncWorker)
	go worker.Start(ctx, 1)

	// Step 1: Verify the CVO creates the initial Cluster Version object
	//
	client.ClearActions()
	err := o.sync(o.queueKey())
	if err != nil {
		t.Fatal(err)
	}
	actions := client.Actions()
	if len(actions) != 3 {
		t.Fatalf("%s", spew.Sdump(actions))
	}
	// read from lister
	expectGet(t, actions[0], "clusterversions", "", "version")
	// read before create
	expectGet(t, actions[1], "clusterversions", "", "version")
	// create initial version
	actual := cvs["version"].(*configv1.ClusterVersion)
	expectCreate(t, actions[2], "clusterversions", "", &configv1.ClusterVersion{
		ObjectMeta: metav1.ObjectMeta{
			Name: "version",
		},
		Spec: configv1.ClusterVersionSpec{
			ClusterID: actual.Spec.ClusterID,
			Channel:   "fast",
		},
	})
	verifyAllStatus(t, worker.StatusCh())

	// Step 2: Ensure the CVO reports a status error if it has nothing to sync
	//
	client.ClearActions()
	err = o.sync(o.queueKey())
	if err != nil {
		t.Fatal(err)
	}
	actions = client.Actions()
	if len(actions) != 2 {
		t.Fatalf("%s", spew.Sdump(actions))
	}
	expectGet(t, actions[0], "clusterversions", "", "version")
	actual = cvs["version"].(*configv1.ClusterVersion)
	expectUpdateStatus(t, actions[1], "clusterversions", "", &configv1.ClusterVersion{
		ObjectMeta: metav1.ObjectMeta{
			Name:       "version",
			Generation: 1,
		},
		Spec: configv1.ClusterVersionSpec{
			ClusterID: actual.Spec.ClusterID,
			Channel:   "fast",
		},
		Status: configv1.ClusterVersionStatus{
			History: []configv1.UpdateHistory{
				// empty because the operator release image is not set, so we have no input
			},
			Conditions: []configv1.ClusterOperatorStatusCondition{
				{Type: configv1.OperatorAvailable, Status: configv1.ConditionFalse},
				// report back to the user that we don't have enough info to proceed
				{Type: ClusterStatusFailing, Status: configv1.ConditionTrue, Message: "No configured operator version, unable to update cluster"},
				{Type: configv1.OperatorProgressing, Status: configv1.ConditionTrue, Message: "Unable to apply <unknown>: an error occurred"},
				{Type: configv1.RetrievedUpdates, Status: configv1.ConditionFalse},
			},
		},
	})
	verifyAllStatus(t, worker.StatusCh())

	// Step 3: Given an operator image, begin synchronizing
	//
	o.releaseImage = "image/image:1"
	o.releaseVersion = "4.0.1"
	desired := configv1.Update{Version: "4.0.1", Image: "image/image:1"}
	//
	client.ClearActions()
	err = o.sync(o.queueKey())
	if err != nil {
		t.Fatal(err)
	}
	actions = client.Actions()
	if len(actions) != 2 {
		t.Fatalf("%s", spew.Sdump(actions))
	}
	expectGet(t, actions[0], "clusterversions", "", "version")
	actual = cvs["version"].(*configv1.ClusterVersion)
	expectUpdateStatus(t, actions[1], "clusterversions", "", &configv1.ClusterVersion{
		ObjectMeta: metav1.ObjectMeta{
			Name:       "version",
			Generation: 1,
		},
		Spec: configv1.ClusterVersionSpec{
			ClusterID: actual.Spec.ClusterID,
			Channel:   "fast",
		},
		Status: configv1.ClusterVersionStatus{
			ObservedGeneration: 1,
			Desired:            desired,
			History: []configv1.UpdateHistory{
				{State: configv1.PartialUpdate, Image: "image/image:1", Version: "4.0.1", StartedTime: defaultStartedTime},
			},
			Conditions: []configv1.ClusterOperatorStatusCondition{
				{Type: configv1.OperatorAvailable, Status: configv1.ConditionFalse},
				// cleared failing status and set progressing
				{Type: ClusterStatusFailing, Status: configv1.ConditionFalse},
				{Type: configv1.OperatorProgressing, Status: configv1.ConditionTrue, Message: "Working towards 4.0.1"},
				{Type: configv1.RetrievedUpdates, Status: configv1.ConditionFalse},
			},
		},
	})
	verifyAllStatus(t, worker.StatusCh(),
		SyncWorkerStatus{
			Generation: 1,
			Step:       "RetrievePayload",
			Initial:    true,
			// the desired version is briefly incorrect (user provided) until we retrieve the image
			Actual: configv1.Update{Version: "4.0.1", Image: "image/image:1"},
		},
		SyncWorkerStatus{
			Generation:   1,
			Step:         "ApplyResources",
			Initial:      true,
			VersionHash:  "6GC9TkkG9PA=",
			Actual:       configv1.Update{Version: "1.0.0-abc", Image: "image/image:1"},
			LastProgress: time.Unix(1, 0),
		},
		SyncWorkerStatus{
			Generation:   1,
			Fraction:     float32(1) / 3,
			Step:         "ApplyResources",
			Initial:      true,
			VersionHash:  "6GC9TkkG9PA=",
			Actual:       configv1.Update{Version: "1.0.0-abc", Image: "image/image:1"},
			LastProgress: time.Unix(2, 0),
		},
		SyncWorkerStatus{
			Generation:   1,
			Fraction:     float32(2) / 3,
			Initial:      true,
			Step:         "ApplyResources",
			VersionHash:  "6GC9TkkG9PA=",
			Actual:       configv1.Update{Version: "1.0.0-abc", Image: "image/image:1"},
			LastProgress: time.Unix(3, 0),
		},
		SyncWorkerStatus{
			Generation:   1,
			Reconciling:  true,
			Completed:    1,
			Fraction:     1,
			VersionHash:  "6GC9TkkG9PA=",
			Actual:       configv1.Update{Version: "1.0.0-abc", Image: "image/image:1"},
			LastProgress: time.Unix(4, 0),
		},
	)

	// Step 4: Now that sync is complete, verify status is updated to represent image contents
	//
	client.ClearActions()
	err = o.sync(o.queueKey())
	if err != nil {
		t.Fatal(err)
	}
	actions = client.Actions()
	if len(actions) != 2 {
		t.Fatalf("%s", spew.Sdump(actions))
	}
	expectGet(t, actions[0], "clusterversions", "", "version")
	// update the status to indicate we are synced, available, and report versions
	actual = cvs["version"].(*configv1.ClusterVersion)
	expectUpdateStatus(t, actions[1], "clusterversions", "", &configv1.ClusterVersion{
		ObjectMeta: metav1.ObjectMeta{
			Name:       "version",
			Generation: 1,
		},
		Spec: configv1.ClusterVersionSpec{
			ClusterID: actual.Spec.ClusterID,
			Channel:   "fast",
		},
		Status: configv1.ClusterVersionStatus{
			ObservedGeneration: 1,
			// Prefers the image version over the operator's version (although in general they will remain in sync)
			Desired:     configv1.Update{Version: "1.0.0-abc", Image: "image/image:1"},
			VersionHash: "6GC9TkkG9PA=",
			History: []configv1.UpdateHistory{
				// Because image and operator had mismatched versions, we get two entries (which shouldn't happen unless there is a bug in the CVO)
				{State: configv1.CompletedUpdate, Image: "image/image:1", Version: "1.0.0-abc", StartedTime: defaultStartedTime, CompletionTime: &defaultCompletionTime},
				{State: configv1.PartialUpdate, Image: "image/image:1", Version: "4.0.1", StartedTime: defaultStartedTime, CompletionTime: &defaultCompletionTime},
			},
			Conditions: []configv1.ClusterOperatorStatusCondition{
				{Type: configv1.OperatorAvailable, Status: configv1.ConditionTrue, Message: "Done applying 1.0.0-abc"},
				{Type: ClusterStatusFailing, Status: configv1.ConditionFalse},
				{Type: configv1.OperatorProgressing, Status: configv1.ConditionFalse, Message: "Cluster version is 1.0.0-abc"},
				{Type: configv1.RetrievedUpdates, Status: configv1.ConditionFalse},
			},
		},
	})

	// Step 5: Wait for the SyncWorker to trigger a reconcile (500ms after the first)
	//
	verifyAllStatus(t, worker.StatusCh(),
		SyncWorkerStatus{
			Generation:  1,
			Reconciling: true,
			Step:        "ApplyResources",
			VersionHash: "6GC9TkkG9PA=",
			Actual:      configv1.Update{Version: "1.0.0-abc", Image: "image/image:1"},
		},
		SyncWorkerStatus{
			Generation:  1,
			Reconciling: true,
			Fraction:    float32(1) / 3,
			Step:        "ApplyResources",
			VersionHash: "6GC9TkkG9PA=",
			Actual:      configv1.Update{Version: "1.0.0-abc", Image: "image/image:1"},
		},
		SyncWorkerStatus{
			Generation:  1,
			Reconciling: true,
			Fraction:    float32(2) / 3,
			Step:        "ApplyResources",
			VersionHash: "6GC9TkkG9PA=",
			Actual:      configv1.Update{Version: "1.0.0-abc", Image: "image/image:1"},
		},
		SyncWorkerStatus{
			Generation:   1,
			Reconciling:  true,
			Completed:    2,
			Fraction:     1,
			VersionHash:  "6GC9TkkG9PA=",
			Actual:       configv1.Update{Version: "1.0.0-abc", Image: "image/image:1"},
			LastProgress: time.Unix(1, 0),
		},
	)

	// Step 6: After a reconciliation, there should be no status change because the state is the same
	//
	client.ClearActions()
	err = o.sync(o.queueKey())
	if err != nil {
		t.Fatal(err)
	}
	actions = client.Actions()
	if len(actions) != 1 {
		t.Fatalf("%s", spew.Sdump(actions))
	}
	expectGet(t, actions[0], "clusterversions", "", "version")
}

func TestCVO_StartupAndSyncUnverifiedPayload(t *testing.T) {
	o, cvs, client, _, shutdownFn := setupCVOTest("testdata/payloadtest")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	defer shutdownFn()

	// make the image report unverified
	payloadErr := &payload.UpdateError{
		Reason:  "ImageVerificationFailed",
		Message: fmt.Sprintf("The update cannot be verified: some random error"),
		Nested:  fmt.Errorf("some random error"),
	}
	if !isImageVerificationError(payloadErr) {
		t.Fatal("not the correct error type")
	}
	worker := o.configSync.(*SyncWorker)
	worker.retriever.(*fakeDirectoryRetriever).Info = PayloadInfo{
		Directory: "testdata/payloadtest",
		Local:     true,

		VerificationError: payloadErr,
	}

	go worker.Start(ctx, 1)

	// Step 1: Verify the CVO creates the initial Cluster Version object
	//
	client.ClearActions()
	err := o.sync(o.queueKey())
	if err != nil {
		t.Fatal(err)
	}
	actions := client.Actions()
	if len(actions) != 3 {
		t.Fatalf("%s", spew.Sdump(actions))
	}
	// read from lister
	expectGet(t, actions[0], "clusterversions", "", "version")
	// read before create
	expectGet(t, actions[1], "clusterversions", "", "version")
	// create initial version
	actual := cvs["version"].(*configv1.ClusterVersion)
	expectCreate(t, actions[2], "clusterversions", "", &configv1.ClusterVersion{
		ObjectMeta: metav1.ObjectMeta{
			Name: "version",
		},
		Spec: configv1.ClusterVersionSpec{
			ClusterID: actual.Spec.ClusterID,
			Channel:   "fast",
		},
	})
	verifyAllStatus(t, worker.StatusCh())

	// Step 2: Ensure the CVO reports a status error if it has nothing to sync
	//
	client.ClearActions()
	err = o.sync(o.queueKey())
	if err != nil {
		t.Fatal(err)
	}
	actions = client.Actions()
	if len(actions) != 2 {
		t.Fatalf("%s", spew.Sdump(actions))
	}
	expectGet(t, actions[0], "clusterversions", "", "version")
	actual = cvs["version"].(*configv1.ClusterVersion)
	expectUpdateStatus(t, actions[1], "clusterversions", "", &configv1.ClusterVersion{
		ObjectMeta: metav1.ObjectMeta{
			Name:       "version",
			Generation: 1,
		},
		Spec: configv1.ClusterVersionSpec{
			ClusterID: actual.Spec.ClusterID,
			Channel:   "fast",
		},
		Status: configv1.ClusterVersionStatus{
			History: []configv1.UpdateHistory{
				// empty because the operator release image is not set, so we have no input
			},
			Conditions: []configv1.ClusterOperatorStatusCondition{
				{Type: configv1.OperatorAvailable, Status: configv1.ConditionFalse},
				// report back to the user that we don't have enough info to proceed
				{Type: ClusterStatusFailing, Status: configv1.ConditionTrue, Message: "No configured operator version, unable to update cluster"},
				{Type: configv1.OperatorProgressing, Status: configv1.ConditionTrue, Message: "Unable to apply <unknown>: an error occurred"},
				{Type: configv1.RetrievedUpdates, Status: configv1.ConditionFalse},
			},
		},
	})
	verifyAllStatus(t, worker.StatusCh())

	// Step 3: Given an operator image, begin synchronizing
	//
	o.releaseImage = "image/image:1"
	o.releaseVersion = "4.0.1"
	desired := configv1.Update{Version: "4.0.1", Image: "image/image:1"}
	//
	client.ClearActions()
	err = o.sync(o.queueKey())
	if err != nil {
		t.Fatal(err)
	}
	actions = client.Actions()
	if len(actions) != 2 {
		t.Fatalf("%s", spew.Sdump(actions))
	}
	expectGet(t, actions[0], "clusterversions", "", "version")
	actual = cvs["version"].(*configv1.ClusterVersion)
	expectUpdateStatus(t, actions[1], "clusterversions", "", &configv1.ClusterVersion{
		ObjectMeta: metav1.ObjectMeta{
			Name:       "version",
			Generation: 1,
		},
		Spec: configv1.ClusterVersionSpec{
			ClusterID: actual.Spec.ClusterID,
			Channel:   "fast",
		},
		Status: configv1.ClusterVersionStatus{
			Desired:            desired,
			ObservedGeneration: 1,
			History: []configv1.UpdateHistory{
				{State: configv1.PartialUpdate, Image: "image/image:1", Version: "4.0.1", StartedTime: defaultStartedTime},
			},
			Conditions: []configv1.ClusterOperatorStatusCondition{
				{Type: configv1.OperatorAvailable, Status: configv1.ConditionFalse},
				// cleared failing status and set progressing
				{Type: ClusterStatusFailing, Status: configv1.ConditionFalse},
				{Type: configv1.OperatorProgressing, Status: configv1.ConditionTrue, Message: "Working towards 4.0.1"},
				{Type: configv1.RetrievedUpdates, Status: configv1.ConditionFalse},
			},
		},
	})
	verifyAllStatus(t, worker.StatusCh(),
		SyncWorkerStatus{
			Step:    "RetrievePayload",
			Initial: true,
			// the desired version is briefly incorrect (user provided) until we retrieve the image
			Actual:     configv1.Update{Version: "4.0.1", Image: "image/image:1"},
			Generation: 1,
		},
		SyncWorkerStatus{
			Step:         "ApplyResources",
			Initial:      true,
			VersionHash:  "6GC9TkkG9PA=",
			Actual:       configv1.Update{Version: "1.0.0-abc", Image: "image/image:1"},
			LastProgress: time.Unix(1, 0),
			Generation:   1,
		},
		SyncWorkerStatus{
			Fraction:     float32(1) / 3,
			Step:         "ApplyResources",
			Initial:      true,
			VersionHash:  "6GC9TkkG9PA=",
			Actual:       configv1.Update{Version: "1.0.0-abc", Image: "image/image:1"},
			LastProgress: time.Unix(2, 0),
			Generation:   1,
		},
		SyncWorkerStatus{
			Fraction:     float32(2) / 3,
			Initial:      true,
			Step:         "ApplyResources",
			VersionHash:  "6GC9TkkG9PA=",
			Actual:       configv1.Update{Version: "1.0.0-abc", Image: "image/image:1"},
			LastProgress: time.Unix(3, 0),
			Generation:   1,
		},
		SyncWorkerStatus{
			Reconciling:  true,
			Completed:    1,
			Fraction:     1,
			VersionHash:  "6GC9TkkG9PA=",
			Actual:       configv1.Update{Version: "1.0.0-abc", Image: "image/image:1"},
			LastProgress: time.Unix(4, 0),
			Generation:   1,
		},
	)

	// Step 4: Now that sync is complete, verify status is updated to represent image contents
	//
	client.ClearActions()
	err = o.sync(o.queueKey())
	if err != nil {
		t.Fatal(err)
	}
	actions = client.Actions()
	if len(actions) != 2 {
		t.Fatalf("%s", spew.Sdump(actions))
	}
	expectGet(t, actions[0], "clusterversions", "", "version")
	// update the status to indicate we are synced, available, and report versions
	actual = cvs["version"].(*configv1.ClusterVersion)
	expectUpdateStatus(t, actions[1], "clusterversions", "", &configv1.ClusterVersion{
		ObjectMeta: metav1.ObjectMeta{
			Name:       "version",
			Generation: 1,
		},
		Spec: configv1.ClusterVersionSpec{
			ClusterID: actual.Spec.ClusterID,
			Channel:   "fast",
		},
		Status: configv1.ClusterVersionStatus{
			ObservedGeneration: 1,
			// Prefers the image version over the operator's version (although in general they will remain in sync)
			Desired:     configv1.Update{Version: "1.0.0-abc", Image: "image/image:1"},
			VersionHash: "6GC9TkkG9PA=",
			History: []configv1.UpdateHistory{
				// Because image and operator had mismatched versions, we get two entries (which shouldn't happen unless there is a bug in the CVO)
				{State: configv1.CompletedUpdate, Image: "image/image:1", Version: "1.0.0-abc", StartedTime: defaultStartedTime, CompletionTime: &defaultCompletionTime},
				{State: configv1.PartialUpdate, Image: "image/image:1", Version: "4.0.1", StartedTime: defaultStartedTime, CompletionTime: &defaultCompletionTime},
			},
			Conditions: []configv1.ClusterOperatorStatusCondition{
				{Type: configv1.OperatorAvailable, Status: configv1.ConditionTrue, Message: "Done applying 1.0.0-abc"},
				{Type: ClusterStatusFailing, Status: configv1.ConditionFalse},
				{Type: configv1.OperatorProgressing, Status: configv1.ConditionFalse, Message: "Cluster version is 1.0.0-abc"},
				{Type: configv1.RetrievedUpdates, Status: configv1.ConditionFalse},
			},
		},
	})

	// Step 5: Wait for the SyncWorker to trigger a reconcile (500ms after the first)
	//
	verifyAllStatus(t, worker.StatusCh(),
		SyncWorkerStatus{
			Reconciling: true,
			Step:        "ApplyResources",
			VersionHash: "6GC9TkkG9PA=",
			Actual:      configv1.Update{Version: "1.0.0-abc", Image: "image/image:1"},
			Generation:  1,
		},
		SyncWorkerStatus{
			Reconciling: true,
			Fraction:    float32(1) / 3,
			Step:        "ApplyResources",
			VersionHash: "6GC9TkkG9PA=",
			Actual:      configv1.Update{Version: "1.0.0-abc", Image: "image/image:1"},
			Generation:  1,
		},
		SyncWorkerStatus{
			Reconciling: true,
			Fraction:    float32(2) / 3,
			Step:        "ApplyResources",
			VersionHash: "6GC9TkkG9PA=",
			Actual:      configv1.Update{Version: "1.0.0-abc", Image: "image/image:1"},
			Generation:  1,
		},
		SyncWorkerStatus{
			Reconciling:  true,
			Completed:    2,
			Fraction:     1,
			VersionHash:  "6GC9TkkG9PA=",
			Actual:       configv1.Update{Version: "1.0.0-abc", Image: "image/image:1"},
			LastProgress: time.Unix(1, 0),
			Generation:   1,
		},
	)

	// Step 6: After a reconciliation, there should be no status change because the state is the same
	//
	client.ClearActions()
	err = o.sync(o.queueKey())
	if err != nil {
		t.Fatal(err)
	}
	actions = client.Actions()
	if len(actions) != 1 {
		t.Fatalf("%s", spew.Sdump(actions))
	}
	expectGet(t, actions[0], "clusterversions", "", "version")
}

func TestCVO_StartupAndSyncPreconditionFailing(t *testing.T) {
	o, cvs, client, _, shutdownFn := setupCVOTest("testdata/payloadtest")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	defer shutdownFn()

	worker := o.configSync.(*SyncWorker)
	// Need the precondition check to fail permanently, so setting failure until 100 attempt to simulate that.
	worker.preconditions = []precondition.Precondition{&testPrecondition{SuccessAfter: 100}}
	worker.retriever.(*fakeDirectoryRetriever).Info = PayloadInfo{
		Directory: "testdata/payloadtest",
		Local:     true,
	}
	go worker.Start(ctx, 1)

	// Step 1: Verify the CVO creates the initial Cluster Version object
	//
	client.ClearActions()
	err := o.sync(o.queueKey())
	if err != nil {
		t.Fatal(err)
	}
	actions := client.Actions()
	if len(actions) != 3 {
		t.Fatalf("%s", spew.Sdump(actions))
	}
	// read from lister
	expectGet(t, actions[0], "clusterversions", "", "version")
	// read before create
	expectGet(t, actions[1], "clusterversions", "", "version")
	// create initial version
	actual := cvs["version"].(*configv1.ClusterVersion)
	expectCreate(t, actions[2], "clusterversions", "", &configv1.ClusterVersion{
		ObjectMeta: metav1.ObjectMeta{
			Name: "version",
		},
		Spec: configv1.ClusterVersionSpec{
			ClusterID: actual.Spec.ClusterID,
			Channel:   "fast",
		},
	})
	verifyAllStatus(t, worker.StatusCh())

	// Step 2: Ensure the CVO reports a status error if it has nothing to sync
	//
	client.ClearActions()
	err = o.sync(o.queueKey())
	if err != nil {
		t.Fatal(err)
	}
	actions = client.Actions()
	if len(actions) != 2 {
		t.Fatalf("%s", spew.Sdump(actions))
	}
	expectGet(t, actions[0], "clusterversions", "", "version")
	actual = cvs["version"].(*configv1.ClusterVersion)
	expectUpdateStatus(t, actions[1], "clusterversions", "", &configv1.ClusterVersion{
		ObjectMeta: metav1.ObjectMeta{
			Name:       "version",
			Generation: 1,
		},
		Spec: configv1.ClusterVersionSpec{
			ClusterID: actual.Spec.ClusterID,
			Channel:   "fast",
		},
		Status: configv1.ClusterVersionStatus{
			History: []configv1.UpdateHistory{
				// empty because the operator release image is not set, so we have no input
			},
			Conditions: []configv1.ClusterOperatorStatusCondition{
				{Type: configv1.OperatorAvailable, Status: configv1.ConditionFalse},
				// report back to the user that we don't have enough info to proceed
				{Type: ClusterStatusFailing, Status: configv1.ConditionTrue, Message: "No configured operator version, unable to update cluster"},
				{Type: configv1.OperatorProgressing, Status: configv1.ConditionTrue, Message: "Unable to apply <unknown>: an error occurred"},
				{Type: configv1.RetrievedUpdates, Status: configv1.ConditionFalse},
			},
		},
	})
	verifyAllStatus(t, worker.StatusCh())

	// Step 3: Given an operator image, begin synchronizing
	//
	o.releaseImage = "image/image:1"
	o.releaseVersion = "4.0.1"
	desired := configv1.Update{Version: "4.0.1", Image: "image/image:1"}
	//
	client.ClearActions()
	err = o.sync(o.queueKey())
	if err != nil {
		t.Fatal(err)
	}
	actions = client.Actions()
	if len(actions) != 2 {
		t.Fatalf("%s", spew.Sdump(actions))
	}
	expectGet(t, actions[0], "clusterversions", "", "version")
	actual = cvs["version"].(*configv1.ClusterVersion)
	expectUpdateStatus(t, actions[1], "clusterversions", "", &configv1.ClusterVersion{
		ObjectMeta: metav1.ObjectMeta{
			Name:       "version",
			Generation: 1,
		},
		Spec: configv1.ClusterVersionSpec{
			ClusterID: actual.Spec.ClusterID,
			Channel:   "fast",
		},
		Status: configv1.ClusterVersionStatus{
			Desired:            desired,
			ObservedGeneration: 1,
			History: []configv1.UpdateHistory{
				{State: configv1.PartialUpdate, Image: "image/image:1", Version: "4.0.1", StartedTime: defaultStartedTime},
			},
			Conditions: []configv1.ClusterOperatorStatusCondition{
				{Type: configv1.OperatorAvailable, Status: configv1.ConditionFalse},
				// cleared failing status and set progressing
				{Type: ClusterStatusFailing, Status: configv1.ConditionFalse},
				{Type: configv1.OperatorProgressing, Status: configv1.ConditionTrue, Message: "Working towards 4.0.1"},
				{Type: configv1.RetrievedUpdates, Status: configv1.ConditionFalse},
			},
		},
	})
	verifyAllStatus(t, worker.StatusCh(),
		SyncWorkerStatus{
			Step:    "RetrievePayload",
			Initial: true,
			// the desired version is briefly incorrect (user provided) until we retrieve the image
			Actual:     configv1.Update{Version: "4.0.1", Image: "image/image:1"},
			Generation: 1,
		},
		SyncWorkerStatus{
			Step:         "ApplyResources",
			Initial:      true,
			VersionHash:  "6GC9TkkG9PA=",
			Actual:       configv1.Update{Version: "1.0.0-abc", Image: "image/image:1"},
			LastProgress: time.Unix(1, 0),
			Generation:   1,
		},
		SyncWorkerStatus{
			Fraction:     float32(1) / 3,
			Step:         "ApplyResources",
			Initial:      true,
			VersionHash:  "6GC9TkkG9PA=",
			Actual:       configv1.Update{Version: "1.0.0-abc", Image: "image/image:1"},
			LastProgress: time.Unix(2, 0),
			Generation:   1,
		},
		SyncWorkerStatus{
			Fraction:     float32(2) / 3,
			Initial:      true,
			Step:         "ApplyResources",
			VersionHash:  "6GC9TkkG9PA=",
			Actual:       configv1.Update{Version: "1.0.0-abc", Image: "image/image:1"},
			LastProgress: time.Unix(3, 0),
			Generation:   1,
		},
		SyncWorkerStatus{
			Reconciling:  true,
			Completed:    1,
			Fraction:     1,
			VersionHash:  "6GC9TkkG9PA=",
			Actual:       configv1.Update{Version: "1.0.0-abc", Image: "image/image:1"},
			LastProgress: time.Unix(4, 0),
			Generation:   1,
		},
	)

	// Step 4: Now that sync is complete, verify status is updated to represent image contents
	//
	client.ClearActions()
	err = o.sync(o.queueKey())
	if err != nil {
		t.Fatal(err)
	}
	actions = client.Actions()
	if len(actions) != 2 {
		t.Fatalf("%s", spew.Sdump(actions))
	}
	expectGet(t, actions[0], "clusterversions", "", "version")
	// update the status to indicate we are synced, available, and report versions
	actual = cvs["version"].(*configv1.ClusterVersion)
	expectUpdateStatus(t, actions[1], "clusterversions", "", &configv1.ClusterVersion{
		ObjectMeta: metav1.ObjectMeta{
			Name:       "version",
			Generation: 1,
		},
		Spec: configv1.ClusterVersionSpec{
			ClusterID: actual.Spec.ClusterID,
			Channel:   "fast",
		},
		Status: configv1.ClusterVersionStatus{
			ObservedGeneration: 1,
			// Prefers the image version over the operator's version (although in general they will remain in sync)
			Desired:     configv1.Update{Version: "1.0.0-abc", Image: "image/image:1"},
			VersionHash: "6GC9TkkG9PA=",
			History: []configv1.UpdateHistory{
				// Because image and operator had mismatched versions, we get two entries (which shouldn't happen unless there is a bug in the CVO)
				{State: configv1.CompletedUpdate, Image: "image/image:1", Version: "1.0.0-abc", StartedTime: defaultStartedTime, CompletionTime: &defaultCompletionTime},
				{State: configv1.PartialUpdate, Image: "image/image:1", Version: "4.0.1", StartedTime: defaultStartedTime, CompletionTime: &defaultCompletionTime},
			},
			Conditions: []configv1.ClusterOperatorStatusCondition{
				{Type: configv1.OperatorAvailable, Status: configv1.ConditionTrue, Message: "Done applying 1.0.0-abc"},
				{Type: ClusterStatusFailing, Status: configv1.ConditionFalse},
				{Type: configv1.OperatorProgressing, Status: configv1.ConditionFalse, Message: "Cluster version is 1.0.0-abc"},
				{Type: configv1.RetrievedUpdates, Status: configv1.ConditionFalse},
			},
		},
	})

	// Step 5: Wait for the SyncWorker to trigger a reconcile (500ms after the first)
	//
	verifyAllStatus(t, worker.StatusCh(),
		SyncWorkerStatus{
			Reconciling: true,
			Step:        "ApplyResources",
			VersionHash: "6GC9TkkG9PA=",
			Actual:      configv1.Update{Version: "1.0.0-abc", Image: "image/image:1"},
			Generation:  1,
		},
		SyncWorkerStatus{
			Reconciling: true,
			Fraction:    float32(1) / 3,
			Step:        "ApplyResources",
			VersionHash: "6GC9TkkG9PA=",
			Actual:      configv1.Update{Version: "1.0.0-abc", Image: "image/image:1"},
			Generation:  1,
		},
		SyncWorkerStatus{
			Reconciling: true,
			Fraction:    float32(2) / 3,
			Step:        "ApplyResources",
			VersionHash: "6GC9TkkG9PA=",
			Actual:      configv1.Update{Version: "1.0.0-abc", Image: "image/image:1"},
			Generation:  1,
		},
		SyncWorkerStatus{
			Reconciling:  true,
			Completed:    2,
			Fraction:     1,
			VersionHash:  "6GC9TkkG9PA=",
			Actual:       configv1.Update{Version: "1.0.0-abc", Image: "image/image:1"},
			LastProgress: time.Unix(1, 0),
			Generation:   1,
		},
	)

	// Step 6: After a reconciliation, there should be no status change because the state is the same
	//
	client.ClearActions()
	err = o.sync(o.queueKey())
	if err != nil {
		t.Fatal(err)
	}
	actions = client.Actions()
	if len(actions) != 1 {
		t.Fatalf("%s", spew.Sdump(actions))
	}
	expectGet(t, actions[0], "clusterversions", "", "version")
}

func TestCVO_UpgradeUnverifiedPayload(t *testing.T) {
	o, cvs, client, _, shutdownFn := setupCVOTest("testdata/payloadtest-2")

	// Setup: a successful sync from a previous run, and the operator at the same image as before
	//
	o.releaseImage = "image/image:0"
	o.releaseVersion = "1.0.0-abc"
	desired := configv1.Update{Version: "1.0.1-abc", Image: "image/image:1"}
	uid, _ := uuid.NewRandom()
	clusterUID := configv1.ClusterID(uid.String())
	cvs["version"] = &configv1.ClusterVersion{
		ObjectMeta: metav1.ObjectMeta{
			Name:            "version",
			ResourceVersion: "1",
		},
		Spec: configv1.ClusterVersionSpec{
			ClusterID:     clusterUID,
			Channel:       "fast",
			DesiredUpdate: &desired,
		},
		Status: configv1.ClusterVersionStatus{
			// Prefers the image version over the operator's version (although in general they will remain in sync)
			Desired:     desired,
			VersionHash: "6GC9TkkG9PA=",
			History: []configv1.UpdateHistory{
				{State: configv1.CompletedUpdate, Image: "image/image:0", Version: "1.0.0-abc", Verified: true, StartedTime: defaultStartedTime, CompletionTime: &defaultCompletionTime},
			},
			Conditions: []configv1.ClusterOperatorStatusCondition{
				{Type: configv1.OperatorAvailable, Status: configv1.ConditionTrue, Message: "Done applying 1.0.0-abc"},
				{Type: ClusterStatusFailing, Status: configv1.ConditionFalse},
				{Type: configv1.OperatorProgressing, Status: configv1.ConditionFalse, Message: "Cluster version is 1.0.0-abc"},
				{Type: configv1.RetrievedUpdates, Status: configv1.ConditionFalse},
			},
		},
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	defer shutdownFn()

	// make the image report unverified
	payloadErr := &payload.UpdateError{
		Reason:  "ImageVerificationFailed",
		Message: fmt.Sprintf("The update cannot be verified: some random error"),
		Nested:  fmt.Errorf("some random error"),
	}
	if !isImageVerificationError(payloadErr) {
		t.Fatal("not the correct error type")
	}
	worker := o.configSync.(*SyncWorker)
	retriever := worker.retriever.(*fakeDirectoryRetriever)
	retriever.Set(PayloadInfo{}, payloadErr)

	go worker.Start(ctx, 1)

	// Step 1: The operator should report that it is blocked on unverified content
	//
	client.ClearActions()
	err := o.sync(o.queueKey())
	if err != nil {
		t.Fatal(err)
	}
	actions := client.Actions()
	if len(actions) != 2 {
		t.Fatalf("%s", spew.Sdump(actions))
	}

	verifyAllStatus(t, worker.StatusCh(),
		SyncWorkerStatus{
			Step:   "RetrievePayload",
			Actual: configv1.Update{Version: "1.0.1-abc", Image: "image/image:1"},
		},
		SyncWorkerStatus{
			Step:    "RetrievePayload",
			Failure: payloadErr,
			Actual:  configv1.Update{Version: "1.0.1-abc", Image: "image/image:1"},
		},
	)

	client.ClearActions()
	err = o.sync(o.queueKey())
	if err != nil {
		t.Fatal(err)
	}
	actions = client.Actions()
	if len(actions) != 2 {
		t.Fatalf("%s", spew.Sdump(actions))
	}
	expectGet(t, actions[0], "clusterversions", "", "version")
	actual := cvs["version"].(*configv1.ClusterVersion)
	expectUpdateStatus(t, actions[1], "clusterversions", "", &configv1.ClusterVersion{
		ObjectMeta: metav1.ObjectMeta{
			Name:            "version",
			ResourceVersion: "1",
			Generation:      1,
		},
		Spec: configv1.ClusterVersionSpec{
			ClusterID:     clusterUID,
			Channel:       "fast",
			DesiredUpdate: &desired,
		},
		Status: configv1.ClusterVersionStatus{
			// Prefers the image version over the operator's version (although in general they will remain in sync)
			Desired:     desired,
			VersionHash: "6GC9TkkG9PA=",
			History: []configv1.UpdateHistory{
				{State: configv1.PartialUpdate, Image: "image/image:1", Version: "1.0.1-abc", StartedTime: defaultStartedTime},
				{State: configv1.CompletedUpdate, Image: "image/image:0", Version: "1.0.0-abc", Verified: true, StartedTime: defaultStartedTime, CompletionTime: &defaultCompletionTime},
			},
			Conditions: []configv1.ClusterOperatorStatusCondition{
				{Type: configv1.OperatorAvailable, Status: configv1.ConditionTrue, Message: "Done applying 1.0.0-abc"},
				// cleared failing status and set progressing
				{Type: ClusterStatusFailing, Status: configv1.ConditionTrue, Reason: "ImageVerificationFailed", Message: "The update cannot be verified: some random error"},
				{Type: configv1.OperatorProgressing, Status: configv1.ConditionTrue, Reason: "ImageVerificationFailed", Message: "Unable to apply 1.0.1-abc: the image may not be safe to use"},
				{Type: configv1.RetrievedUpdates, Status: configv1.ConditionFalse},
			},
		},
	})

	// Step 2: Set allowUnverifiedImages to true, trigger a sync and the operator should apply the payload
	//
	// set an updtae
	copied := desired
	copied.Force = true
	actual.Spec.DesiredUpdate = &copied
	retriever.Set(PayloadInfo{Directory: "testdata/payloadtest-2", VerificationError: payloadErr}, nil)
	//
	// ensure the sync worker tells the sync loop about it
	err = o.sync(o.queueKey())
	if err != nil {
		t.Fatal(err)
	}

	// wait until we see the new payload show up
	count := 0
	for {
		var status SyncWorkerStatus
		select {
		case status = <-worker.StatusCh():
		case <-time.After(3 * time.Second):
			t.Fatalf("never saw expected sync event")
		}
		if status.Step == "RetrievePayload" && reflect.DeepEqual(configv1.Update{Version: "1.0.1-abc", Image: "image/image:1", Force: true}, status.Actual) {
			break
		}
		t.Logf("Unexpected status waiting to see first retrieve: %#v", status)
		count++
		if count > 8 {
			t.Fatalf("saw too many sync events of the wrong form")
		}
	}
	verifyAllStatus(t, worker.StatusCh(),
		SyncWorkerStatus{
			Step:         "ApplyResources",
			VersionHash:  "6GC9TkkG9PA=",
			Actual:       configv1.Update{Version: "1.0.1-abc", Image: "image/image:1", Force: true},
			LastProgress: time.Unix(1, 0),
			Generation:   1,
		},
		SyncWorkerStatus{
			Fraction:     float32(1) / 3,
			Step:         "ApplyResources",
			VersionHash:  "6GC9TkkG9PA=",
			Actual:       configv1.Update{Version: "1.0.1-abc", Image: "image/image:1", Force: true},
			LastProgress: time.Unix(2, 0),
			Generation:   1,
		},
		SyncWorkerStatus{
			Fraction:     float32(2) / 3,
			Step:         "ApplyResources",
			VersionHash:  "6GC9TkkG9PA=",
			Actual:       configv1.Update{Version: "1.0.1-abc", Image: "image/image:1", Force: true},
			LastProgress: time.Unix(3, 0),
			Generation:   1,
		},
		SyncWorkerStatus{
			Reconciling:  true,
			Completed:    1,
			Fraction:     1,
			VersionHash:  "6GC9TkkG9PA=",
			Actual:       configv1.Update{Version: "1.0.1-abc", Image: "image/image:1", Force: true},
			LastProgress: time.Unix(4, 0),
			Generation:   1,
		},
	)
	client.ClearActions()
	err = o.sync(o.queueKey())
	if err != nil {
		t.Fatal(err)
	}
	actions = client.Actions()
	if len(actions) != 2 {
		t.Fatalf("%s", spew.Sdump(actions))
	}
	expectGet(t, actions[0], "clusterversions", "", "version")
	expectUpdateStatus(t, actions[1], "clusterversions", "", &configv1.ClusterVersion{
		ObjectMeta: metav1.ObjectMeta{
			Name:            "version",
			ResourceVersion: "1",
			Generation:      1,
		},
		Spec: configv1.ClusterVersionSpec{
			ClusterID:     actual.Spec.ClusterID,
			Channel:       "fast",
			DesiredUpdate: &copied,
		},
		Status: configv1.ClusterVersionStatus{
			ObservedGeneration: 1,
			Desired:            configv1.Update{Version: "1.0.1-abc", Image: "image/image:1", Force: true},
			VersionHash:        "6GC9TkkG9PA=",
			History: []configv1.UpdateHistory{
				{State: configv1.CompletedUpdate, Image: "image/image:1", Version: "1.0.1-abc", StartedTime: defaultStartedTime, CompletionTime: &defaultCompletionTime},
				{State: configv1.CompletedUpdate, Image: "image/image:0", Version: "1.0.0-abc", Verified: true, StartedTime: defaultStartedTime, CompletionTime: &defaultCompletionTime},
			},
			Conditions: []configv1.ClusterOperatorStatusCondition{
				{Type: configv1.OperatorAvailable, Status: configv1.ConditionTrue, Message: "Done applying 1.0.1-abc"},
				{Type: ClusterStatusFailing, Status: configv1.ConditionFalse},
				{Type: configv1.OperatorProgressing, Status: configv1.ConditionFalse, Message: "Cluster version is 1.0.1-abc"},
				{Type: configv1.RetrievedUpdates, Status: configv1.ConditionFalse},
			},
		},
	})
}

func TestCVO_UpgradeUnverifiedPayloadRetriveOnce(t *testing.T) {
	o, cvs, client, _, shutdownFn := setupCVOTest("testdata/payloadtest-2")

	// Setup: a successful sync from a previous run, and the operator at the same image as before
	//
	o.releaseImage = "image/image:0"
	o.releaseVersion = "1.0.0-abc"
	desired := configv1.Update{Version: "1.0.1-abc", Image: "image/image:1"}
	uid, _ := uuid.NewRandom()
	clusterUID := configv1.ClusterID(uid.String())
	cvs["version"] = &configv1.ClusterVersion{
		ObjectMeta: metav1.ObjectMeta{
			Name:            "version",
			ResourceVersion: "1",
		},
		Spec: configv1.ClusterVersionSpec{
			ClusterID:     clusterUID,
			Channel:       "fast",
			DesiredUpdate: &desired,
		},
		Status: configv1.ClusterVersionStatus{
			// Prefers the image version over the operator's version (although in general they will remain in sync)
			Desired:     desired,
			VersionHash: "6GC9TkkG9PA=",
			History: []configv1.UpdateHistory{
				{State: configv1.CompletedUpdate, Image: "image/image:0", Version: "1.0.0-abc", Verified: true, StartedTime: defaultStartedTime, CompletionTime: &defaultCompletionTime},
			},
			Conditions: []configv1.ClusterOperatorStatusCondition{
				{Type: configv1.OperatorAvailable, Status: configv1.ConditionTrue, Message: "Done applying 1.0.0-abc"},
				{Type: ClusterStatusFailing, Status: configv1.ConditionFalse},
				{Type: configv1.OperatorProgressing, Status: configv1.ConditionFalse, Message: "Cluster version is 1.0.0-abc"},
				{Type: configv1.RetrievedUpdates, Status: configv1.ConditionFalse},
			},
		},
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	defer shutdownFn()

	// make the image report unverified
	payloadErr := &payload.UpdateError{
		Reason:  "ImageVerificationFailed",
		Message: fmt.Sprintf("The update cannot be verified: some random error"),
		Nested:  fmt.Errorf("some random error"),
	}
	if !isImageVerificationError(payloadErr) {
		t.Fatal("not the correct error type")
	}
	worker := o.configSync.(*SyncWorker)
	retriever := worker.retriever.(*fakeDirectoryRetriever)
	retriever.Set(PayloadInfo{}, payloadErr)

	go worker.Start(ctx, 1)

	// Step 1: The operator should report that it is blocked on unverified content
	//
	client.ClearActions()
	err := o.sync(o.queueKey())
	if err != nil {
		t.Fatal(err)
	}
	actions := client.Actions()
	if len(actions) != 2 {
		t.Fatalf("%s", spew.Sdump(actions))
	}

	verifyAllStatus(t, worker.StatusCh(),
		SyncWorkerStatus{
			Step:   "RetrievePayload",
			Actual: configv1.Update{Version: "1.0.1-abc", Image: "image/image:1"},
		},
		SyncWorkerStatus{
			Step:    "RetrievePayload",
			Failure: payloadErr,
			Actual:  configv1.Update{Version: "1.0.1-abc", Image: "image/image:1"},
		},
	)

	client.ClearActions()
	err = o.sync(o.queueKey())
	if err != nil {
		t.Fatal(err)
	}
	actions = client.Actions()
	if len(actions) != 2 {
		t.Fatalf("%s", spew.Sdump(actions))
	}
	expectGet(t, actions[0], "clusterversions", "", "version")
	actual := cvs["version"].(*configv1.ClusterVersion)
	expectUpdateStatus(t, actions[1], "clusterversions", "", &configv1.ClusterVersion{
		ObjectMeta: metav1.ObjectMeta{
			Name:            "version",
			ResourceVersion: "1",
			Generation:      1,
		},
		Spec: configv1.ClusterVersionSpec{
			ClusterID:     clusterUID,
			Channel:       "fast",
			DesiredUpdate: &desired,
		},
		Status: configv1.ClusterVersionStatus{
			// Prefers the image version over the operator's version (although in general they will remain in sync)
			Desired:     desired,
			VersionHash: "6GC9TkkG9PA=",
			History: []configv1.UpdateHistory{
				{State: configv1.PartialUpdate, Image: "image/image:1", Version: "1.0.1-abc", StartedTime: defaultStartedTime},
				{State: configv1.CompletedUpdate, Image: "image/image:0", Version: "1.0.0-abc", Verified: true, StartedTime: defaultStartedTime, CompletionTime: &defaultCompletionTime},
			},
			Conditions: []configv1.ClusterOperatorStatusCondition{
				{Type: configv1.OperatorAvailable, Status: configv1.ConditionTrue, Message: "Done applying 1.0.0-abc"},
				// cleared failing status and set progressing
				{Type: ClusterStatusFailing, Status: configv1.ConditionTrue, Reason: "ImageVerificationFailed", Message: "The update cannot be verified: some random error"},
				{Type: configv1.OperatorProgressing, Status: configv1.ConditionTrue, Reason: "ImageVerificationFailed", Message: "Unable to apply 1.0.1-abc: the image may not be safe to use"},
				{Type: configv1.RetrievedUpdates, Status: configv1.ConditionFalse},
			},
		},
	})

	// Step 2: Set allowUnverifiedImages to true, trigger a sync and the operator should apply the payload
	//
	// set an updtae
	copied := desired
	copied.Force = true
	actual.Spec.DesiredUpdate = &copied
	retriever.Set(PayloadInfo{Directory: "testdata/payloadtest-2", VerificationError: payloadErr}, nil)
	//
	// ensure the sync worker tells the sync loop about it
	err = o.sync(o.queueKey())
	if err != nil {
		t.Fatal(err)
	}

	// wait until we see the new payload show up
	count := 0
	for {
		var status SyncWorkerStatus
		select {
		case status = <-worker.StatusCh():
		case <-time.After(3 * time.Second):
			t.Fatalf("never saw expected sync event")
		}
		if status.Step == "RetrievePayload" && reflect.DeepEqual(configv1.Update{Version: "1.0.1-abc", Image: "image/image:1", Force: true}, status.Actual) {
			break
		}
		t.Logf("Unexpected status waiting to see first retrieve: %#v", status)
		count++
		if count > 8 {
			t.Fatalf("saw too many sync events of the wrong form")
		}
	}
	verifyAllStatus(t, worker.StatusCh(),
		SyncWorkerStatus{
			Step:         "ApplyResources",
			VersionHash:  "6GC9TkkG9PA=",
			Actual:       configv1.Update{Version: "1.0.1-abc", Image: "image/image:1", Force: true},
			LastProgress: time.Unix(1, 0),
			Generation:   1,
		},
		SyncWorkerStatus{
			Fraction:     float32(1) / 3,
			Step:         "ApplyResources",
			VersionHash:  "6GC9TkkG9PA=",
			Actual:       configv1.Update{Version: "1.0.1-abc", Image: "image/image:1", Force: true},
			LastProgress: time.Unix(2, 0),
			Generation:   1,
		},
		SyncWorkerStatus{
			Fraction:     float32(2) / 3,
			Step:         "ApplyResources",
			VersionHash:  "6GC9TkkG9PA=",
			Actual:       configv1.Update{Version: "1.0.1-abc", Image: "image/image:1", Force: true},
			LastProgress: time.Unix(3, 0),
			Generation:   1,
		},
		SyncWorkerStatus{
			Reconciling:  true,
			Completed:    1,
			Fraction:     1,
			VersionHash:  "6GC9TkkG9PA=",
			Actual:       configv1.Update{Version: "1.0.1-abc", Image: "image/image:1", Force: true},
			LastProgress: time.Unix(4, 0),
			Generation:   1,
		},
	)
	client.ClearActions()
	err = o.sync(o.queueKey())
	if err != nil {
		t.Fatal(err)
	}
	actions = client.Actions()
	if len(actions) != 2 {
		t.Fatalf("%s", spew.Sdump(actions))
	}
	expectGet(t, actions[0], "clusterversions", "", "version")
	expectUpdateStatus(t, actions[1], "clusterversions", "", &configv1.ClusterVersion{
		ObjectMeta: metav1.ObjectMeta{
			Name:            "version",
			ResourceVersion: "1",
			Generation:      1,
		},
		Spec: configv1.ClusterVersionSpec{
			ClusterID:     actual.Spec.ClusterID,
			Channel:       "fast",
			DesiredUpdate: &copied,
		},
		Status: configv1.ClusterVersionStatus{
			ObservedGeneration: 1,
			Desired:            configv1.Update{Version: "1.0.1-abc", Image: "image/image:1", Force: true},
			VersionHash:        "6GC9TkkG9PA=",
			History: []configv1.UpdateHistory{
				{State: configv1.CompletedUpdate, Image: "image/image:1", Version: "1.0.1-abc", StartedTime: defaultStartedTime, CompletionTime: &defaultCompletionTime},
				{State: configv1.CompletedUpdate, Image: "image/image:0", Version: "1.0.0-abc", Verified: true, StartedTime: defaultStartedTime, CompletionTime: &defaultCompletionTime},
			},
			Conditions: []configv1.ClusterOperatorStatusCondition{
				{Type: configv1.OperatorAvailable, Status: configv1.ConditionTrue, Message: "Done applying 1.0.1-abc"},
				{Type: ClusterStatusFailing, Status: configv1.ConditionFalse},
				{Type: configv1.OperatorProgressing, Status: configv1.ConditionFalse, Message: "Cluster version is 1.0.1-abc"},
				{Type: configv1.RetrievedUpdates, Status: configv1.ConditionFalse},
			},
		},
	})

	// Step 5: Wait for the SyncWorker to trigger a reconcile (500ms after the first)
	//
	verifyAllStatus(t, worker.StatusCh(),
		SyncWorkerStatus{
			Reconciling: true,
			Step:        "ApplyResources",
			VersionHash: "6GC9TkkG9PA=",
			Actual:      configv1.Update{Version: "1.0.1-abc", Image: "image/image:1", Force: true},
			Generation:  1,
		},
		SyncWorkerStatus{
			Reconciling: true,
			Fraction:    float32(1) / 3,
			Step:        "ApplyResources",
			VersionHash: "6GC9TkkG9PA=",
			Actual:      configv1.Update{Version: "1.0.1-abc", Image: "image/image:1", Force: true},
			Generation:  1,
		},
		SyncWorkerStatus{
			Reconciling: true,
			Fraction:    float32(2) / 3,
			Step:        "ApplyResources",
			VersionHash: "6GC9TkkG9PA=",
			Actual:      configv1.Update{Version: "1.0.1-abc", Image: "image/image:1", Force: true},
			Generation:  1,
		},
		SyncWorkerStatus{
			Reconciling:  true,
			Completed:    2,
			Fraction:     1,
			VersionHash:  "6GC9TkkG9PA=",
			Actual:       configv1.Update{Version: "1.0.1-abc", Image: "image/image:1", Force: true},
			LastProgress: time.Unix(1, 0),
			Generation:   1,
		},
	)
}

func TestCVO_UpgradePreconditionFailing(t *testing.T) {
	o, cvs, client, _, shutdownFn := setupCVOTest("testdata/payloadtest-2")

	// Setup: a successful sync from a previous run, and the operator at the same image as before
	//
	o.releaseImage = "image/image:0"
	o.releaseVersion = "1.0.0-abc"
	desired := configv1.Update{Version: "1.0.1-abc", Image: "image/image:1"}
	uid, _ := uuid.NewRandom()
	clusterUID := configv1.ClusterID(uid.String())
	cvs["version"] = &configv1.ClusterVersion{
		ObjectMeta: metav1.ObjectMeta{
			Name:            "version",
			ResourceVersion: "1",
		},
		Spec: configv1.ClusterVersionSpec{
			ClusterID:     clusterUID,
			Channel:       "fast",
			DesiredUpdate: &desired,
		},
		Status: configv1.ClusterVersionStatus{
			// Prefers the image version over the operator's version (although in general they will remain in sync)
			Desired:     desired,
			VersionHash: "6GC9TkkG9PA=",
			History: []configv1.UpdateHistory{
				{State: configv1.CompletedUpdate, Image: "image/image:0", Version: "1.0.0-abc", Verified: true, StartedTime: defaultStartedTime, CompletionTime: &defaultCompletionTime},
			},
			Conditions: []configv1.ClusterOperatorStatusCondition{
				{Type: configv1.OperatorAvailable, Status: configv1.ConditionTrue, Message: "Done applying 1.0.0-abc"},
				{Type: ClusterStatusFailing, Status: configv1.ConditionFalse},
				{Type: configv1.OperatorProgressing, Status: configv1.ConditionFalse, Message: "Cluster version is 1.0.0-abc"},
				{Type: configv1.RetrievedUpdates, Status: configv1.ConditionFalse},
			},
		},
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	defer shutdownFn()

	worker := o.configSync.(*SyncWorker)
	worker.preconditions = []precondition.Precondition{&testPrecondition{SuccessAfter: 3}}

	go worker.Start(ctx, 1)

	// Step 1: The operator should report that it is blocked on precondition checks failing
	//
	client.ClearActions()
	err := o.sync(o.queueKey())
	if err != nil {
		t.Fatal(err)
	}
	actions := client.Actions()
	if len(actions) != 2 {
		t.Fatalf("%s", spew.Sdump(actions))
	}

	verifyAllStatus(t, worker.StatusCh(),
		SyncWorkerStatus{
			Step:   "RetrievePayload",
			Actual: configv1.Update{Version: "1.0.1-abc", Image: "image/image:1"},
		},
		SyncWorkerStatus{
			Step:   "PreconditionChecks",
			Actual: configv1.Update{Version: "1.0.1-abc", Image: "image/image:1"},
		},
		SyncWorkerStatus{
			Step:    "PreconditionChecks",
			Failure: &payload.UpdateError{Reason: "UpgradePreconditionCheckFailed", Message: "Precondition \"TestPrecondition SuccessAfter: 3\" failed because of \"CheckFailure\": failing, attempt: 1 will succeed after 3 attempt", Name: "PreconditionCheck"},
			Actual:  configv1.Update{Version: "1.0.1-abc", Image: "image/image:1"},
		},
	)

	client.ClearActions()
	err = o.sync(o.queueKey())
	if err != nil {
		t.Fatal(err)
	}
	actions = client.Actions()
	if len(actions) != 2 {
		t.Fatalf("%s", spew.Sdump(actions))
	}
	expectGet(t, actions[0], "clusterversions", "", "version")
	actual := cvs["version"].(*configv1.ClusterVersion)
	expectUpdateStatus(t, actions[1], "clusterversions", "", &configv1.ClusterVersion{
		ObjectMeta: metav1.ObjectMeta{
			Name:            "version",
			ResourceVersion: "1",
			Generation:      1,
		},
		Spec: configv1.ClusterVersionSpec{
			ClusterID:     clusterUID,
			Channel:       "fast",
			DesiredUpdate: &desired,
		},
		Status: configv1.ClusterVersionStatus{
			// Prefers the image version over the operator's version (although in general they will remain in sync)
			Desired:     desired,
			VersionHash: "6GC9TkkG9PA=",
			History: []configv1.UpdateHistory{
				{State: configv1.PartialUpdate, Image: "image/image:1", Version: "1.0.1-abc", StartedTime: defaultStartedTime},
				{State: configv1.CompletedUpdate, Image: "image/image:0", Version: "1.0.0-abc", Verified: true, StartedTime: defaultStartedTime, CompletionTime: &defaultCompletionTime},
			},
			Conditions: []configv1.ClusterOperatorStatusCondition{
				{Type: configv1.OperatorAvailable, Status: configv1.ConditionTrue, Message: "Done applying 1.0.0-abc"},
				// cleared failing status and set progressing
				{Type: ClusterStatusFailing, Status: configv1.ConditionTrue, Reason: "UpgradePreconditionCheckFailed", Message: "Precondition \"TestPrecondition SuccessAfter: 3\" failed because of \"CheckFailure\": failing, attempt: 1 will succeed after 3 attempt"},
				{Type: configv1.OperatorProgressing, Status: configv1.ConditionTrue, Reason: "UpgradePreconditionCheckFailed", Message: "Unable to apply 1.0.1-abc: it may not be safe to apply this update"},
				{Type: configv1.RetrievedUpdates, Status: configv1.ConditionFalse},
			},
		},
	})

	// Step 2: Set allowUnverifiedImages to true, trigger a sync and the operator should apply the payload
	//
	// set an updtae
	copied := desired
	copied.Force = true
	actual.Spec.DesiredUpdate = &copied
	//
	// ensure the sync worker tells the sync loop about it
	err = o.sync(o.queueKey())
	if err != nil {
		t.Fatal(err)
	}

	// wait until we see the new payload show up
	count := 0
	for {
		var status SyncWorkerStatus
		select {
		case status = <-worker.StatusCh():
		case <-time.After(3 * time.Second):
			t.Fatalf("never saw expected sync event")
		}
		if status.Step == "RetrievePayload" && reflect.DeepEqual(configv1.Update{Version: "1.0.1-abc", Image: "image/image:1", Force: true}, status.Actual) {
			break
		}
		t.Logf("Unexpected status waiting to see first retrieve: %#v", status)
		count++
		if count > 8 {
			t.Fatalf("saw too many sync events of the wrong form")
		}
	}
	verifyAllStatus(t, worker.StatusCh(),
		SyncWorkerStatus{
			Step:         "PreconditionChecks",
			Actual:       configv1.Update{Version: "1.0.1-abc", Image: "image/image:1", Force: true},
			LastProgress: time.Unix(1, 0),
			Generation:   1,
		},
		SyncWorkerStatus{
			Step:         "ApplyResources",
			VersionHash:  "6GC9TkkG9PA=",
			Actual:       configv1.Update{Version: "1.0.1-abc", Image: "image/image:1", Force: true},
			LastProgress: time.Unix(2, 0),
			Generation:   1,
		},
		SyncWorkerStatus{
			Fraction:     float32(1) / 3,
			Step:         "ApplyResources",
			VersionHash:  "6GC9TkkG9PA=",
			Actual:       configv1.Update{Version: "1.0.1-abc", Image: "image/image:1", Force: true},
			LastProgress: time.Unix(3, 0),
			Generation:   1,
		},
		SyncWorkerStatus{
			Fraction:     float32(2) / 3,
			Step:         "ApplyResources",
			VersionHash:  "6GC9TkkG9PA=",
			Actual:       configv1.Update{Version: "1.0.1-abc", Image: "image/image:1", Force: true},
			LastProgress: time.Unix(4, 0),
			Generation:   1,
		},
		SyncWorkerStatus{
			Reconciling:  true,
			Completed:    1,
			Fraction:     1,
			VersionHash:  "6GC9TkkG9PA=",
			Actual:       configv1.Update{Version: "1.0.1-abc", Image: "image/image:1", Force: true},
			LastProgress: time.Unix(5, 0),
			Generation:   1,
		},
	)
	client.ClearActions()
	err = o.sync(o.queueKey())
	if err != nil {
		t.Fatal(err)
	}
	actions = client.Actions()
	if len(actions) != 2 {
		t.Fatalf("%s", spew.Sdump(actions))
	}
	expectGet(t, actions[0], "clusterversions", "", "version")
	expectUpdateStatus(t, actions[1], "clusterversions", "", &configv1.ClusterVersion{
		ObjectMeta: metav1.ObjectMeta{
			Name:            "version",
			ResourceVersion: "1",
			Generation:      1,
		},
		Spec: configv1.ClusterVersionSpec{
			ClusterID:     actual.Spec.ClusterID,
			Channel:       "fast",
			DesiredUpdate: &copied,
		},
		Status: configv1.ClusterVersionStatus{
			ObservedGeneration: 1,
			Desired:            configv1.Update{Version: "1.0.1-abc", Image: "image/image:1", Force: true},
			VersionHash:        "6GC9TkkG9PA=",
			History: []configv1.UpdateHistory{
				{State: configv1.CompletedUpdate, Image: "image/image:1", Version: "1.0.1-abc", StartedTime: defaultStartedTime, CompletionTime: &defaultCompletionTime},
				{State: configv1.CompletedUpdate, Image: "image/image:0", Version: "1.0.0-abc", Verified: true, StartedTime: defaultStartedTime, CompletionTime: &defaultCompletionTime},
			},
			Conditions: []configv1.ClusterOperatorStatusCondition{
				{Type: configv1.OperatorAvailable, Status: configv1.ConditionTrue, Message: "Done applying 1.0.1-abc"},
				{Type: ClusterStatusFailing, Status: configv1.ConditionFalse},
				{Type: configv1.OperatorProgressing, Status: configv1.ConditionFalse, Message: "Cluster version is 1.0.1-abc"},
				{Type: configv1.RetrievedUpdates, Status: configv1.ConditionFalse},
			},
		},
	})
}

func TestCVO_UpgradeVerifiedPayload(t *testing.T) {
	o, cvs, client, _, shutdownFn := setupCVOTest("testdata/payloadtest-2")

	// Setup: a successful sync from a previous run, and the operator at the same image as before
	//
	o.releaseImage = "image/image:0"
	o.releaseVersion = "1.0.0-abc"
	desired := configv1.Update{Version: "1.0.1-abc", Image: "image/image:1"}
	uid, _ := uuid.NewRandom()
	clusterUID := configv1.ClusterID(uid.String())
	cvs["version"] = &configv1.ClusterVersion{
		ObjectMeta: metav1.ObjectMeta{
			Name:            "version",
			ResourceVersion: "1",
			Generation:      1,
		},
		Spec: configv1.ClusterVersionSpec{
			ClusterID:     clusterUID,
			Channel:       "fast",
			DesiredUpdate: &desired,
		},
		Status: configv1.ClusterVersionStatus{
			// Prefers the image version over the operator's version (although in general they will remain in sync)
			Desired:     desired,
			VersionHash: "6GC9TkkG9PA=",
			History: []configv1.UpdateHistory{
				{State: configv1.CompletedUpdate, Image: "image/image:0", Version: "1.0.0-abc", Verified: true, StartedTime: defaultStartedTime, CompletionTime: &defaultCompletionTime},
			},
			Conditions: []configv1.ClusterOperatorStatusCondition{
				{Type: configv1.OperatorAvailable, Status: configv1.ConditionTrue, Message: "Done applying 1.0.0-abc"},
				{Type: ClusterStatusFailing, Status: configv1.ConditionFalse},
				{Type: configv1.OperatorProgressing, Status: configv1.ConditionFalse, Message: "Cluster version is 1.0.0-abc"},
				{Type: configv1.RetrievedUpdates, Status: configv1.ConditionFalse},
			},
		},
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	defer shutdownFn()

	// make the image report unverified
	payloadErr := &payload.UpdateError{
		Reason:  "ImageVerificationFailed",
		Message: fmt.Sprintf("The update cannot be verified: some random error"),
		Nested:  fmt.Errorf("some random error"),
	}
	if !isImageVerificationError(payloadErr) {
		t.Fatal("not the correct error type")
	}
	worker := o.configSync.(*SyncWorker)
	retriever := worker.retriever.(*fakeDirectoryRetriever)
	retriever.Set(PayloadInfo{}, payloadErr)

	go worker.Start(ctx, 1)

	// Step 1: The operator should report that it is blocked on unverified content
	//
	client.ClearActions()
	err := o.sync(o.queueKey())
	if err != nil {
		t.Fatal(err)
	}
	actions := client.Actions()
	if len(actions) != 2 {
		t.Fatalf("%s", spew.Sdump(actions))
	}

	verifyAllStatus(t, worker.StatusCh(),
		SyncWorkerStatus{
			Step:       "RetrievePayload",
			Actual:     configv1.Update{Version: "1.0.1-abc", Image: "image/image:1"},
			Generation: 1,
		},
		SyncWorkerStatus{
			Step:       "RetrievePayload",
			Failure:    payloadErr,
			Actual:     configv1.Update{Version: "1.0.1-abc", Image: "image/image:1"},
			Generation: 1,
		},
	)

	client.ClearActions()
	err = o.sync(o.queueKey())
	if err != nil {
		t.Fatal(err)
	}
	actions = client.Actions()
	if len(actions) != 2 {
		t.Fatalf("%s", spew.Sdump(actions))
	}
	expectGet(t, actions[0], "clusterversions", "", "version")
	actual := cvs["version"].(*configv1.ClusterVersion)
	expectUpdateStatus(t, actions[1], "clusterversions", "", &configv1.ClusterVersion{
		ObjectMeta: metav1.ObjectMeta{
			Name:            "version",
			ResourceVersion: "1",
			Generation:      1,
		},
		Spec: configv1.ClusterVersionSpec{
			ClusterID:     clusterUID,
			Channel:       "fast",
			DesiredUpdate: &desired,
		},
		Status: configv1.ClusterVersionStatus{
			ObservedGeneration: 1,
			// Prefers the image version over the operator's version (although in general they will remain in sync)
			Desired:     desired,
			VersionHash: "6GC9TkkG9PA=",
			History: []configv1.UpdateHistory{
				{State: configv1.PartialUpdate, Image: "image/image:1", Version: "1.0.1-abc", StartedTime: defaultStartedTime},
				{State: configv1.CompletedUpdate, Image: "image/image:0", Version: "1.0.0-abc", Verified: true, StartedTime: defaultStartedTime, CompletionTime: &defaultCompletionTime},
			},
			Conditions: []configv1.ClusterOperatorStatusCondition{
				{Type: configv1.OperatorAvailable, Status: configv1.ConditionTrue, Message: "Done applying 1.0.0-abc"},
				// cleared failing status and set progressing
				{Type: ClusterStatusFailing, Status: configv1.ConditionTrue, Reason: "ImageVerificationFailed", Message: "The update cannot be verified: some random error"},
				{Type: configv1.OperatorProgressing, Status: configv1.ConditionTrue, Reason: "ImageVerificationFailed", Message: "Unable to apply 1.0.1-abc: the image may not be safe to use"},
				{Type: configv1.RetrievedUpdates, Status: configv1.ConditionFalse},
			},
		},
	})

	// Step 2: Simulate a verified payload being retrieved and ensure the operator sets verified
	//
	copied := desired
	actual.ObjectMeta.Generation = 2
	actual.Spec.DesiredUpdate = &copied
	retriever.Set(PayloadInfo{Directory: "testdata/payloadtest-2", Verified: true}, nil)
	//
	client.ClearActions()
	err = o.sync(o.queueKey())
	if err != nil {
		t.Fatal(err)
	}
	actions = client.Actions()
	if len(actions) != 1 {
		t.Fatalf("%s", spew.Sdump(actions))
	}
	expectGet(t, actions[0], "clusterversions", "", "version")

	verifyAllStatus(t, worker.StatusCh(),
		SyncWorkerStatus{
			Step:       "RetrievePayload",
			Actual:     configv1.Update{Version: "1.0.1-abc", Image: "image/image:1"},
			Generation: 2,
		},
		SyncWorkerStatus{
			Step:        "ApplyResources",
			VersionHash: "6GC9TkkG9PA=",
			Actual:      configv1.Update{Version: "1.0.1-abc", Image: "image/image:1"},
			Verified:    true,
			Generation:  2,
		},
		SyncWorkerStatus{
			Fraction:     float32(1) / 3,
			Step:         "ApplyResources",
			VersionHash:  "6GC9TkkG9PA=",
			Actual:       configv1.Update{Version: "1.0.1-abc", Image: "image/image:1"},
			LastProgress: time.Unix(1, 0),
			Verified:     true,
			Generation:   2,
		},
		SyncWorkerStatus{
			Fraction:     float32(2) / 3,
			Step:         "ApplyResources",
			VersionHash:  "6GC9TkkG9PA=",
			Actual:       configv1.Update{Version: "1.0.1-abc", Image: "image/image:1"},
			LastProgress: time.Unix(2, 0),
			Verified:     true,
			Generation:   2,
		},
		SyncWorkerStatus{
			Reconciling:  true,
			Completed:    1,
			Fraction:     1,
			VersionHash:  "6GC9TkkG9PA=",
			Actual:       configv1.Update{Version: "1.0.1-abc", Image: "image/image:1"},
			LastProgress: time.Unix(3, 0),
			Verified:     true,
			Generation:   2,
		},
	)
	client.ClearActions()
	err = o.sync(o.queueKey())
	if err != nil {
		t.Fatal(err)
	}
	actions = client.Actions()
	if len(actions) != 2 {
		t.Fatalf("%s", spew.Sdump(actions))
	}
	expectGet(t, actions[0], "clusterversions", "", "version")
	expectUpdateStatus(t, actions[1], "clusterversions", "", &configv1.ClusterVersion{
		ObjectMeta: metav1.ObjectMeta{
			Name:            "version",
			ResourceVersion: "1",
			Generation:      2,
		},
		Spec: configv1.ClusterVersionSpec{
			ClusterID:     actual.Spec.ClusterID,
			Channel:       "fast",
			DesiredUpdate: &copied,
		},
		Status: configv1.ClusterVersionStatus{
			ObservedGeneration: 2,
			Desired:            configv1.Update{Version: "1.0.1-abc", Image: "image/image:1"},
			VersionHash:        "6GC9TkkG9PA=",
			History: []configv1.UpdateHistory{
				{State: configv1.CompletedUpdate, Image: "image/image:1", Version: "1.0.1-abc", Verified: true, StartedTime: defaultStartedTime, CompletionTime: &defaultCompletionTime},
				{State: configv1.CompletedUpdate, Image: "image/image:0", Version: "1.0.0-abc", Verified: true, StartedTime: defaultStartedTime, CompletionTime: &defaultCompletionTime},
			},
			Conditions: []configv1.ClusterOperatorStatusCondition{
				{Type: configv1.OperatorAvailable, Status: configv1.ConditionTrue, Message: "Done applying 1.0.1-abc"},
				{Type: ClusterStatusFailing, Status: configv1.ConditionFalse},
				{Type: configv1.OperatorProgressing, Status: configv1.ConditionFalse, Message: "Cluster version is 1.0.1-abc"},
				{Type: configv1.RetrievedUpdates, Status: configv1.ConditionFalse},
			},
		},
	})
}
func TestCVO_RestartAndReconcile(t *testing.T) {
	o, cvs, client, _, shutdownFn := setupCVOTest("testdata/payloadtest")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	defer shutdownFn()
	worker := o.configSync.(*SyncWorker)

	// Setup: a successful sync from a previous run, and the operator at the same image as before
	//
	o.releaseImage = "image/image:1"
	o.releaseVersion = "1.0.0-abc"
	desired := configv1.Update{Version: "1.0.0-abc", Image: "image/image:1"}
	uid, _ := uuid.NewRandom()
	clusterUID := configv1.ClusterID(uid.String())
	cvs["version"] = &configv1.ClusterVersion{
		ObjectMeta: metav1.ObjectMeta{
			Name:            "version",
			ResourceVersion: "1",
		},
		Spec: configv1.ClusterVersionSpec{
			ClusterID: clusterUID,
			Channel:   "fast",
		},
		Status: configv1.ClusterVersionStatus{
			// Prefers the image version over the operator's version (although in general they will remain in sync)
			Desired:     desired,
			VersionHash: "6GC9TkkG9PA=",
			History: []configv1.UpdateHistory{
				// TODO: this is wrong, should be single partial entry
				{State: configv1.CompletedUpdate, Image: "image/image:1", Version: "1.0.0-abc", Verified: true, StartedTime: defaultStartedTime, CompletionTime: &defaultCompletionTime},
				{State: configv1.PartialUpdate, Image: "image/image:1", Version: "4.0.1", StartedTime: defaultStartedTime, CompletionTime: &defaultCompletionTime},
				{State: configv1.PartialUpdate, StartedTime: defaultStartedTime, CompletionTime: &defaultCompletionTime},
				{State: configv1.PartialUpdate, StartedTime: defaultStartedTime, CompletionTime: &defaultCompletionTime},
			},
			Conditions: []configv1.ClusterOperatorStatusCondition{
				{Type: configv1.OperatorAvailable, Status: configv1.ConditionTrue, Message: "Done applying 1.0.0-abc"},
				{Type: ClusterStatusFailing, Status: configv1.ConditionFalse},
				{Type: configv1.OperatorProgressing, Status: configv1.ConditionFalse, Message: "Cluster version is 1.0.0-abc"},
				{Type: configv1.RetrievedUpdates, Status: configv1.ConditionFalse},
			},
		},
	}

	// Step 1: The sync loop starts and triggers a sync, but does not update status
	//
	client.ClearActions()
	err := o.sync(o.queueKey())
	if err != nil {
		t.Fatal(err)
	}
	actions := client.Actions()
	if len(actions) != 1 {
		t.Fatalf("%s", spew.Sdump(actions))
	}
	expectGet(t, actions[0], "clusterversions", "", "version")

	// check the worker status is initially set to reconciling
	if status := worker.Status(); !status.Reconciling || status.Completed != 0 {
		t.Fatalf("The worker should be reconciling from the beginning: %#v", status)
	}
	if worker.work.State != payload.ReconcilingPayload {
		t.Fatalf("The worker should be reconciling: %v", worker.work)
	}

	// Step 2: Start the sync worker and verify the sequence of events, and then verify
	//         the status does not change
	//
	go worker.Start(ctx, 1)
	//
	verifyAllStatus(t, worker.StatusCh(),
		SyncWorkerStatus{
			Reconciling: true,
			Step:        "RetrievePayload",
			Actual:      configv1.Update{Version: "1.0.0-abc", Image: "image/image:1"},
		},
		SyncWorkerStatus{
			Reconciling: true,
			Step:        "ApplyResources",
			VersionHash: "6GC9TkkG9PA=",
			Actual:      configv1.Update{Version: "1.0.0-abc", Image: "image/image:1"},
		},
		SyncWorkerStatus{
			Reconciling:  true,
			Fraction:     float32(1) / 3,
			Step:         "ApplyResources",
			VersionHash:  "6GC9TkkG9PA=",
			Actual:       configv1.Update{Version: "1.0.0-abc", Image: "image/image:1"},
			LastProgress: time.Unix(1, 0),
		},
		SyncWorkerStatus{
			Reconciling:  true,
			Fraction:     float32(2) / 3,
			Step:         "ApplyResources",
			VersionHash:  "6GC9TkkG9PA=",
			Actual:       configv1.Update{Version: "1.0.0-abc", Image: "image/image:1"},
			LastProgress: time.Unix(2, 0),
		},
		SyncWorkerStatus{
			Reconciling:  true,
			Completed:    1,
			Fraction:     1,
			VersionHash:  "6GC9TkkG9PA=",
			Actual:       configv1.Update{Version: "1.0.0-abc", Image: "image/image:1"},
			LastProgress: time.Unix(3, 0),
		},
	)
	client.ClearActions()
	err = o.sync(o.queueKey())
	if err != nil {
		t.Fatal(err)
	}
	actions = client.Actions()
	if len(actions) != 1 {
		t.Fatalf("%s", spew.Sdump(actions))
	}
	expectGet(t, actions[0], "clusterversions", "", "version")

	// Step 3: Wait until the next resync is triggered, and then verify that status does
	//         not change
	//
	verifyAllStatus(t, worker.StatusCh(),
		// note that the image is not retrieved a second time
		SyncWorkerStatus{
			Reconciling: true,
			Step:        "ApplyResources",
			VersionHash: "6GC9TkkG9PA=",
			Actual:      configv1.Update{Version: "1.0.0-abc", Image: "image/image:1"},
		},
		SyncWorkerStatus{
			Reconciling: true,
			Fraction:    float32(1) / 3,
			Step:        "ApplyResources",
			VersionHash: "6GC9TkkG9PA=",
			Actual:      configv1.Update{Version: "1.0.0-abc", Image: "image/image:1"},
		},
		SyncWorkerStatus{
			Reconciling: true,
			Fraction:    float32(2) / 3,
			Step:        "ApplyResources",
			VersionHash: "6GC9TkkG9PA=",
			Actual:      configv1.Update{Version: "1.0.0-abc", Image: "image/image:1"},
		},
		SyncWorkerStatus{
			Reconciling:  true,
			Completed:    2,
			Fraction:     1,
			VersionHash:  "6GC9TkkG9PA=",
			Actual:       configv1.Update{Version: "1.0.0-abc", Image: "image/image:1"},
			LastProgress: time.Unix(1, 0),
		},
	)
	client.ClearActions()
	err = o.sync(o.queueKey())
	if err != nil {
		t.Fatal(err)
	}
	actions = client.Actions()
	if len(actions) != 1 {
		t.Fatalf("%s", spew.Sdump(actions))
	}
	expectGet(t, actions[0], "clusterversions", "", "version")
}

func TestCVO_ErrorDuringReconcile(t *testing.T) {
	o, cvs, client, _, shutdownFn := setupCVOTest("testdata/payloadtest")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	defer shutdownFn()
	worker := o.configSync.(*SyncWorker)
	b := newBlockingResourceBuilder()
	worker.builder = b

	// Setup: a successful sync from a previous run, and the operator at the same image as before
	//
	o.releaseImage = "image/image:1"
	o.releaseVersion = "1.0.0-abc"
	desired := configv1.Update{Version: "1.0.0-abc", Image: "image/image:1"}
	uid, _ := uuid.NewRandom()
	clusterUID := configv1.ClusterID(uid.String())
	cvs["version"] = &configv1.ClusterVersion{
		ObjectMeta: metav1.ObjectMeta{
			Name:            "version",
			ResourceVersion: "1",
		},
		Spec: configv1.ClusterVersionSpec{
			ClusterID: clusterUID,
			Channel:   "fast",
		},
		Status: configv1.ClusterVersionStatus{
			// Prefers the image version over the operator's version (although in general they will remain in sync)
			Desired:     desired,
			VersionHash: "6GC9TkkG9PA=",
			History: []configv1.UpdateHistory{
				{State: configv1.CompletedUpdate, Image: "image/image:1", Version: "1.0.0-abc", Verified: true, StartedTime: defaultStartedTime, CompletionTime: &defaultCompletionTime},
			},
			Conditions: []configv1.ClusterOperatorStatusCondition{
				{Type: configv1.OperatorAvailable, Status: configv1.ConditionTrue, Message: "Done applying 1.0.0-abc"},
				{Type: ClusterStatusFailing, Status: configv1.ConditionFalse},
				{Type: configv1.OperatorProgressing, Status: configv1.ConditionFalse, Message: "Cluster version is 1.0.0-abc"},
				{Type: configv1.RetrievedUpdates, Status: configv1.ConditionFalse},
			},
		},
	}

	// Step 1: The sync loop starts and triggers a sync, but does not update status
	//
	client.ClearActions()
	err := o.sync(o.queueKey())
	if err != nil {
		t.Fatal(err)
	}
	actions := client.Actions()
	if len(actions) != 1 {
		t.Fatalf("%s", spew.Sdump(actions))
	}
	expectGet(t, actions[0], "clusterversions", "", "version")

	// check the worker status is initially set to reconciling
	if status := worker.Status(); !status.Reconciling || status.Completed != 0 {
		t.Fatalf("The worker should be reconciling from the beginning: %#v", status)
	}
	if worker.work.State != payload.ReconcilingPayload {
		t.Fatalf("The worker should be reconciling: %v", worker.work)
	}

	// Step 2: Start the sync worker and verify the sequence of events
	//
	go worker.Start(ctx, 1)
	//
	verifyAllStatus(t, worker.StatusCh(),
		SyncWorkerStatus{
			Reconciling: true,
			Step:        "RetrievePayload",
			Actual:      configv1.Update{Version: "1.0.0-abc", Image: "image/image:1"},
		},
		SyncWorkerStatus{
			Reconciling: true,
			Step:        "ApplyResources",
			VersionHash: "6GC9TkkG9PA=",
			Actual:      configv1.Update{Version: "1.0.0-abc", Image: "image/image:1"},
		},
	)
	// verify we haven't observed any other events
	verifyAllStatus(t, worker.StatusCh())

	// Step 3: Simulate a sync being triggered while we are partway through our first
	//         reconcile sync and verify status is not updated
	//
	client.ClearActions()
	err = o.sync(o.queueKey())
	if err != nil {
		t.Fatal(err)
	}
	actions = client.Actions()
	if len(actions) != 1 {
		t.Fatalf("%s", spew.Sdump(actions))
	}
	expectGet(t, actions[0], "clusterversions", "", "version")

	// Step 4: Unblock the first item from being applied
	//
	b.Send(nil)
	//
	// verify we observe the remaining changes in the first sync
	verifyAllStatus(t, worker.StatusCh(),
		SyncWorkerStatus{
			Reconciling:  true,
			Fraction:     float32(1) / 3,
			Step:         "ApplyResources",
			VersionHash:  "6GC9TkkG9PA=",
			Actual:       configv1.Update{Version: "1.0.0-abc", Image: "image/image:1"},
			LastProgress: time.Unix(1, 0),
		},
	)
	verifyAllStatus(t, worker.StatusCh())

	// Step 5: Unblock the first item from being applied
	//
	b.Send(nil)
	//
	// verify we observe the remaining changes in the first sync
	verifyAllStatus(t, worker.StatusCh(),
		SyncWorkerStatus{
			Reconciling:  true,
			Fraction:     float32(2) / 3,
			Step:         "ApplyResources",
			VersionHash:  "6GC9TkkG9PA=",
			Actual:       configv1.Update{Version: "1.0.0-abc", Image: "image/image:1"},
			LastProgress: time.Unix(1, 0),
		},
	)
	verifyAllStatus(t, worker.StatusCh())

	// Step 6: Send an error, then verify it shows up in status
	//
	b.Send(fmt.Errorf("unable to proceed"))

	go func() {
		for len(b.ch) != 0 {
			time.Sleep(time.Millisecond)
		}
		cancel()
		for len(b.ch) == 0 || len(worker.StatusCh()) == 0 {
			time.Sleep(time.Millisecond)
		}
	}()

	//
	// verify we see the update after the context times out
	verifyAllStatus(t, worker.StatusCh(),
		SyncWorkerStatus{
			Reconciling: true,
			Step:        "ApplyResources",
			Fraction:    float32(2) / 3,
			VersionHash: "6GC9TkkG9PA=",
			Failure: &payload.UpdateError{
				Nested:  fmt.Errorf("unable to proceed"),
				Reason:  "UpdatePayloadFailed",
				Message: "Could not update test \"file-yml\" (3 of 3)",
				Task:    &payload.Task{Index: 3, Total: 3, Manifest: &worker.payload.Manifests[2]},
			},
			Actual:       configv1.Update{Version: "1.0.0-abc", Image: "image/image:1"},
			LastProgress: time.Unix(1, 0),
		},
	)
	client.ClearActions()
	err = o.sync(o.queueKey())
	if err != nil {
		t.Fatal(err)
	}
	actions = client.Actions()
	if len(actions) != 2 {
		t.Fatalf("%s", spew.Sdump(actions))
	}
	expectGet(t, actions[0], "clusterversions", "", "version")
	expectUpdateStatus(t, actions[1], "clusterversions", "", &configv1.ClusterVersion{
		ObjectMeta: metav1.ObjectMeta{
			Name:            "version",
			ResourceVersion: "1",
		},
		Spec: configv1.ClusterVersionSpec{
			ClusterID: clusterUID,
			Channel:   "fast",
		},
		Status: configv1.ClusterVersionStatus{
			// Prefers the image version over the operator's version (although in general they will remain in sync)
			Desired:     configv1.Update{Version: "1.0.0-abc", Image: "image/image:1"},
			VersionHash: "6GC9TkkG9PA=",
			History: []configv1.UpdateHistory{
				{State: configv1.CompletedUpdate, Image: "image/image:1", Version: "1.0.0-abc", Verified: true, StartedTime: defaultStartedTime, CompletionTime: &defaultCompletionTime},
			},
			Conditions: []configv1.ClusterOperatorStatusCondition{
				{Type: configv1.OperatorAvailable, Status: configv1.ConditionTrue, Message: "Done applying 1.0.0-abc"},
				{Type: ClusterStatusFailing, Status: configv1.ConditionTrue, Reason: "UpdatePayloadFailed", Message: "Could not update test \"file-yml\" (3 of 3)"},
				{Type: configv1.OperatorProgressing, Status: configv1.ConditionFalse, Reason: "UpdatePayloadFailed", Message: "Error while reconciling 1.0.0-abc: the update could not be applied"},
				{Type: configv1.RetrievedUpdates, Status: configv1.ConditionFalse},
			},
		},
	})
}

func TestCVO_ParallelError(t *testing.T) {
	o, cvs, client, _, shutdownFn := setupCVOTest("testdata/paralleltest")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	defer shutdownFn()
	worker := o.configSync.(*SyncWorker)
	b := &errorResourceBuilder{errors: map[string]error{
		"0000_10_a_file.yaml": &payload.UpdateError{
			Reason: "ClusterOperatorNotAvailable",
			Name:   "operator-1",
		},
		"0000_20_a_file.yaml": nil,
		"0000_20_b_file.yaml": &payload.UpdateError{
			Reason: "ClusterOperatorNotAvailable",
			Name:   "operator-2",
		},
	}}
	worker.builder = b

	// Setup: an initializing cluster version which will run in parallel
	//
	o.releaseImage = "image/image:1"
	o.releaseVersion = "1.0.0-abc"
	desired := configv1.Update{Version: "1.0.0-abc", Image: "image/image:1"}
	uid, _ := uuid.NewRandom()
	clusterUID := configv1.ClusterID(uid.String())
	cvs["version"] = &configv1.ClusterVersion{
		ObjectMeta: metav1.ObjectMeta{
			Name:            "version",
			ResourceVersion: "1",
		},
		Spec: configv1.ClusterVersionSpec{
			ClusterID: clusterUID,
			Channel:   "fast",
		},
		Status: configv1.ClusterVersionStatus{
			// Prefers the image version over the operator's version (although in general they will remain in sync)
			Desired:    desired,
			History:    []configv1.UpdateHistory{},
			Conditions: []configv1.ClusterOperatorStatusCondition{},
		},
	}

	// Step 1: Write initial status
	//
	client.ClearActions()
	err := o.sync(o.queueKey())
	if err != nil {
		t.Fatal(err)
	}
	actions := client.Actions()
	if len(actions) != 2 {
		t.Fatalf("%s", spew.Sdump(actions))
	}
	expectGet(t, actions[0], "clusterversions", "", "version")

	// check the worker status is initially set to reconciling
	if status := worker.Status(); status.Reconciling || status.Completed != 0 {
		t.Fatalf("The worker should be reconciling from the beginning: %#v", status)
	}
	if worker.work.State != payload.InitializingPayload {
		t.Fatalf("The worker should be reconciling: %v", worker.work)
	}

	// Step 2: Start the sync worker and verify the sequence of events
	//
	cancellable, cancel := context.WithCancel(ctx)
	defer cancel()
	go worker.Start(cancellable, 1)
	//
	verifyAllStatus(t, worker.StatusCh(),
		SyncWorkerStatus{
			Initial: true,
			Step:    "RetrievePayload",
			Actual:  configv1.Update{Version: "1.0.0-abc", Image: "image/image:1"},
		},
		SyncWorkerStatus{
			Initial:     true,
			Step:        "ApplyResources",
			VersionHash: "7m-gGRrpkDU=",
			Actual:      configv1.Update{Version: "1.0.0-abc", Image: "image/image:1"},
		},
	)

	// Step 3: Cancel after we've accumulated 2/3 errors
	//
	time.Sleep(100 * time.Millisecond)
	cancel()
	//
	// verify we observe the remaining changes in the first sync
	for status := range worker.StatusCh() {
		if status.Failure == nil {
			if status.Fraction == 0 || status.Fraction == 1/3 {
				if !reflect.DeepEqual(status, SyncWorkerStatus{
					Initial:     true,
					Fraction:    status.Fraction,
					Step:        "ApplyResources",
					VersionHash: "7m-gGRrpkDU=",
					Actual:      configv1.Update{Version: "1.0.0-abc", Image: "image/image:1"},
				}) {
					t.Fatalf("unexpected status: %v", status)
				}
			}
			continue
		}
		err := status.Failure
		uErr, ok := err.(*payload.UpdateError)
		if !ok || uErr.Reason != "ClusterOperatorsNotAvailable" || uErr.Message != "Some cluster operators are still updating: operator-1, operator-2" {
			t.Fatalf("unexpected error: %v", err)
		}
		if status.LastProgress.IsZero() {
			t.Fatalf("unexpected last progress: %v", status.LastProgress)
		}
		if !reflect.DeepEqual(status, SyncWorkerStatus{
			Initial:      true,
			Failure:      err,
			Fraction:     float32(1) / 3,
			Step:         "ApplyResources",
			VersionHash:  "7m-gGRrpkDU=",
			Actual:       configv1.Update{Version: "1.0.0-abc", Image: "image/image:1"},
			LastProgress: status.LastProgress,
		}) {
			t.Fatalf("unexpected final: %v", status)
		}
		break
	}
	verifyAllStatus(t, worker.StatusCh())

	client.ClearActions()
	err = o.sync(o.queueKey())
	if err != nil {
		t.Fatal(err)
	}
	actions = client.Actions()
	if len(actions) != 2 {
		t.Fatalf("%s", spew.Sdump(actions))
	}
	expectGet(t, actions[0], "clusterversions", "", "version")
	expectUpdateStatus(t, actions[1], "clusterversions", "", &configv1.ClusterVersion{
		ObjectMeta: metav1.ObjectMeta{
			Name:            "version",
			Generation:      1,
			ResourceVersion: "1",
		},
		Spec: configv1.ClusterVersionSpec{
			ClusterID: clusterUID,
			Channel:   "fast",
		},
		Status: configv1.ClusterVersionStatus{
			// Prefers the image version over the operator's version (although in general they will remain in sync)
			Desired:     configv1.Update{Version: "1.0.0-abc", Image: "image/image:1"},
			VersionHash: "7m-gGRrpkDU=",
			History: []configv1.UpdateHistory{
				{State: configv1.PartialUpdate, Image: "image/image:1", Version: "1.0.0-abc", StartedTime: defaultStartedTime},
			},
			Conditions: []configv1.ClusterOperatorStatusCondition{
				{Type: configv1.OperatorAvailable, Status: configv1.ConditionFalse},
				{Type: ClusterStatusFailing, Status: configv1.ConditionFalse},
				{Type: configv1.OperatorProgressing, Status: configv1.ConditionTrue, Reason: "ClusterOperatorsNotAvailable", Message: "Working towards 1.0.0-abc: 33% complete, waiting on operator-1, operator-2"},
				{Type: configv1.RetrievedUpdates, Status: configv1.ConditionFalse},
			},
		},
	})
}

func TestCVO_VerifyInitializingPayloadState(t *testing.T) {
	o, cvs, client, _, shutdownFn := setupCVOTest("testdata/payloadtest")
	stopCh := make(chan struct{})
	defer close(stopCh)
	defer shutdownFn()
	worker := o.configSync.(*SyncWorker)
	b := newBlockingResourceBuilder()
	worker.builder = b

	// Setup: a successful sync from a previous run, and the operator at the same image as before
	//
	o.releaseImage = "image/image:1"
	o.releaseVersion = "1.0.0-abc"
	desired := configv1.Update{Version: "1.0.0-abc", Image: "image/image:1"}
	uid, _ := uuid.NewRandom()
	clusterUID := configv1.ClusterID(uid.String())
	cvs["version"] = &configv1.ClusterVersion{
		ObjectMeta: metav1.ObjectMeta{
			Name:            "version",
			ResourceVersion: "1",
		},
		Spec: configv1.ClusterVersionSpec{
			ClusterID: clusterUID,
			Channel:   "fast",
		},
		Status: configv1.ClusterVersionStatus{
			// Prefers the image version over the operator's version (although in general they will remain in sync)
			Desired:     desired,
			VersionHash: "6GC9TkkG9PA=",
			History: []configv1.UpdateHistory{
				{State: configv1.PartialUpdate, Image: "image/image:1", Version: "1.0.0-abc", StartedTime: defaultStartedTime},
			},
			Conditions: []configv1.ClusterOperatorStatusCondition{
				{Type: configv1.OperatorAvailable, Status: configv1.ConditionTrue, Message: "Done applying 1.0.0-abc"},
				{Type: ClusterStatusFailing, Status: configv1.ConditionFalse},
				{Type: configv1.OperatorProgressing, Status: configv1.ConditionFalse, Message: "Cluster version is 1.0.0-abc"},
				{Type: configv1.RetrievedUpdates, Status: configv1.ConditionFalse},
			},
		},
	}

	// Step 1: The sync loop starts and triggers a sync, but does not update status
	//
	client.ClearActions()
	err := o.sync(o.queueKey())
	if err != nil {
		t.Fatal(err)
	}

	// check the worker status is initially set to reconciling
	if status := worker.Status(); status.Reconciling || status.Completed != 0 {
		t.Fatalf("The worker should be initializing from the beginning: %#v", status)
	}
	if worker.work.State != payload.InitializingPayload {
		t.Fatalf("The worker should be initializing: %v", worker.work)
	}
}

func TestCVO_VerifyUpdatingPayloadState(t *testing.T) {
	o, cvs, client, _, shutdownFn := setupCVOTest("testdata/payloadtest")
	stopCh := make(chan struct{})
	defer close(stopCh)
	defer shutdownFn()
	worker := o.configSync.(*SyncWorker)
	b := newBlockingResourceBuilder()
	worker.builder = b

	// Setup: a successful sync from a previous run, and the operator at the same image as before
	//
	o.releaseImage = "image/image:1"
	o.releaseVersion = "1.0.0-abc"
	desired := configv1.Update{Version: "1.0.0-abc", Image: "image/image:1"}
	uid, _ := uuid.NewRandom()
	clusterUID := configv1.ClusterID(uid.String())
	cvs["version"] = &configv1.ClusterVersion{
		ObjectMeta: metav1.ObjectMeta{
			Name:            "version",
			ResourceVersion: "1",
		},
		Spec: configv1.ClusterVersionSpec{
			ClusterID: clusterUID,
			Channel:   "fast",
		},
		Status: configv1.ClusterVersionStatus{
			// Prefers the image version over the operator's version (although in general they will remain in sync)
			Desired:     desired,
			VersionHash: "6GC9TkkG9PA=",
			History: []configv1.UpdateHistory{
				{State: configv1.PartialUpdate, Image: "image/image:1", Version: "1.0.0-abc", StartedTime: defaultStartedTime},
				{State: configv1.CompletedUpdate, Image: "image/image:0", Version: "1.0.0-abc.0", StartedTime: defaultStartedTime, CompletionTime: &defaultCompletionTime},
			},
			Conditions: []configv1.ClusterOperatorStatusCondition{
				{Type: configv1.OperatorAvailable, Status: configv1.ConditionTrue, Message: "Done applying 1.0.0-abc"},
				{Type: ClusterStatusFailing, Status: configv1.ConditionFalse},
				{Type: configv1.OperatorProgressing, Status: configv1.ConditionFalse, Message: "Cluster version is 1.0.0-abc"},
				{Type: configv1.RetrievedUpdates, Status: configv1.ConditionFalse},
			},
		},
	}

	// Step 1: The sync loop starts and triggers a sync, but does not update status
	//
	client.ClearActions()
	err := o.sync(o.queueKey())
	if err != nil {
		t.Fatal(err)
	}

	// check the worker status is initially set to reconciling
	if status := worker.Status(); status.Reconciling || status.Completed != 0 {
		t.Fatalf("The worker should be updating from the beginning: %#v", status)
	}
	if worker.work.State != payload.UpdatingPayload {
		t.Fatalf("The worker should be updating: %v", worker.work)
	}
}

func verifyAllStatus(t *testing.T, ch <-chan SyncWorkerStatus, items ...SyncWorkerStatus) {
	t.Helper()
	if len(items) == 0 {
		if len(ch) > 0 {
			t.Fatalf("expected status to empty, got %#v", <-ch)
		}
		return
	}
	var lastTime time.Time
	count := int64(1)
	for i, expect := range items {
		actual, ok := <-ch
		if !ok {
			t.Fatalf("channel closed after reading only %d items", i)
		}

		if nextTime := actual.LastProgress; !nextTime.Equal(lastTime) {
			actual.LastProgress = time.Unix(count, 0)
			count++
		} else if !lastTime.IsZero() {
			actual.LastProgress = time.Unix(count, 0)
		}

		if !reflect.DeepEqual(expect, actual) {
			t.Fatalf("unexpected status item %d: %s", i, diff.ObjectReflectDiff(expect, actual))
		}
	}
}

func waitFor(t *testing.T, fn func() bool) {
	t.Helper()
	err := wait.PollImmediate(100*time.Millisecond, 1*time.Second, func() (bool, error) {
		return fn(), nil
	})
	if err == wait.ErrWaitTimeout {
		t.Fatalf("Worker condition was not reached within timeout")
	}
	if err != nil {
		t.Fatal(err)
	}
}

// blockingResourceBuilder controls how quickly Apply() is executed and allows error
// injection.
type blockingResourceBuilder struct {
	ch chan error
}

func newBlockingResourceBuilder() *blockingResourceBuilder {
	return &blockingResourceBuilder{
		ch: make(chan error),
	}
}

func (b *blockingResourceBuilder) Send(err error) {
	b.ch <- err
}

func (b *blockingResourceBuilder) Apply(ctx context.Context, m *lib.Manifest, state payload.State) error {
	return <-b.ch
}

type errorResourceBuilder struct {
	errors map[string]error
}

func (b *errorResourceBuilder) Apply(ctx context.Context, m *lib.Manifest, state payload.State) error {
	if err, ok := b.errors[m.OriginalFilename]; ok {
		return err
	}
	return fmt.Errorf("unknown file %s", m.OriginalFilename)
}

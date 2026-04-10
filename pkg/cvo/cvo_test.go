package cvo

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strconv"
	"testing"
	"time"

	"github.com/davecgh/go-spew/spew"
	"github.com/google/go-cmp/cmp"
	"github.com/google/uuid"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	apiruntime "k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	kfake "k8s.io/client-go/kubernetes/fake"
	ktesting "k8s.io/client-go/testing"
	"k8s.io/client-go/tools/record"
	"k8s.io/client-go/util/workqueue"
	"k8s.io/klog/v2"

	configv1 "github.com/openshift/api/config/v1"
	clientset "github.com/openshift/client-go/config/clientset/versioned"
	"github.com/openshift/client-go/config/clientset/versioned/fake"
	"github.com/openshift/library-go/pkg/manifest"
	"github.com/openshift/library-go/pkg/verify/store/serial"
	"github.com/openshift/library-go/pkg/verify/store/sigstore"

	"github.com/openshift/cluster-version-operator/pkg/clusterconditions"
	"github.com/openshift/cluster-version-operator/pkg/clusterconditions/always"
	"github.com/openshift/cluster-version-operator/pkg/featuregates"
	"github.com/openshift/cluster-version-operator/pkg/internal"
	"github.com/openshift/cluster-version-operator/pkg/payload"
	mockrisk "github.com/openshift/cluster-version-operator/pkg/risk/mock"
)

var (
	// defaultStartedTime is a shorthand for verifying a start time is set
	defaultStartedTime = metav1.Time{Time: time.Unix(1, 0)}
	// defaultCompletionTime is a shorthand for verifying a completion time is set
	defaultCompletionTime = metav1.Time{Time: time.Unix(2, 0)}
)

type clientProxyLister struct {
	client clientset.Interface
}

func (c *clientProxyLister) Get(name string) (*configv1.Proxy, error) {
	ctx := context.TODO()
	return c.client.ConfigV1().Proxies().Get(ctx, name, metav1.GetOptions{})
}

func (c *clientProxyLister) List(selector labels.Selector) (ret []*configv1.Proxy, err error) {
	ctx := context.TODO()
	list, err := c.client.ConfigV1().Proxies().List(ctx, metav1.ListOptions{LabelSelector: selector.String()})
	if err != nil {
		return nil, err
	}
	var items []*configv1.Proxy
	for i := range list.Items {
		items = append(items, &list.Items[i])
	}
	return items, nil
}

type clientCVLister struct {
	client clientset.Interface
}

func (c *clientCVLister) Get(name string) (*configv1.ClusterVersion, error) {
	ctx := context.TODO()
	return c.client.ConfigV1().ClusterVersions().Get(ctx, name, metav1.GetOptions{})
}
func (c *clientCVLister) List(selector labels.Selector) (ret []*configv1.ClusterVersion, err error) {
	ctx := context.TODO()
	list, err := c.client.ConfigV1().ClusterVersions().List(ctx, metav1.ListOptions{LabelSelector: selector.String()})
	if err != nil {
		return nil, err
	}
	var items []*configv1.ClusterVersion
	for i := range list.Items {
		items = append(items, &list.Items[i])
	}
	return items, nil
}

type clientCOLister struct {
	client clientset.Interface
}

func (c *clientCOLister) Get(name string) (*configv1.ClusterOperator, error) {
	ctx := context.TODO()
	return c.client.ConfigV1().ClusterOperators().Get(ctx, name, metav1.GetOptions{})
}

func (c *clientCOLister) List(selector labels.Selector) (ret []*configv1.ClusterOperator, err error) {
	ctx := context.TODO()
	list, err := c.client.ConfigV1().ClusterOperators().List(ctx, metav1.ListOptions{LabelSelector: selector.String()})
	if err != nil {
		return nil, err
	}
	var items []*configv1.ClusterOperator
	for i := range list.Items {
		items = append(items, &list.Items[i])
	}
	return items, nil
}

type cvLister struct {
	Err   error
	Items []*configv1.ClusterVersion
}

func (r *cvLister) List(selector labels.Selector) (ret []*configv1.ClusterVersion, err error) {
	return r.Items, r.Err
}
func (r *cvLister) Get(name string) (*configv1.ClusterVersion, error) {
	for _, s := range r.Items {
		if s.Name == name {
			return s, nil
		}
	}
	return nil, errors.NewNotFound(schema.GroupResource{}, name)
}

type coLister struct {
	Err   error
	Items []*configv1.ClusterOperator
}

func (r *coLister) List(selector labels.Selector) (ret []*configv1.ClusterOperator, err error) {
	return r.Items, r.Err
}

func (r *coLister) Get(name string) (*configv1.ClusterOperator, error) {
	for _, s := range r.Items {
		if s.Name == name {
			return s, nil
		}
	}
	return nil, errors.NewNotFound(schema.GroupResource{}, name)
}

type cmConfigLister struct {
	Err   error
	Items []*corev1.ConfigMap
}

func (l *cmConfigLister) List(selector labels.Selector) ([]*corev1.ConfigMap, error) {
	return l.Items, l.Err
}

func (l *cmConfigLister) Get(name string) (*corev1.ConfigMap, error) {
	if l.Err != nil {
		return nil, l.Err
	}
	for _, cm := range l.Items {
		if cm.Name == name {
			return cm, nil
		}
	}
	return nil, errors.NewNotFound(schema.GroupResource{}, name)
}

func TestOperator_sync(t *testing.T) {
	id := uuid.Must(uuid.NewRandom()).String()

	tests := []struct {
		name        string
		key         string
		syncStatus  *SyncWorkerStatus
		optr        *Operator
		init        func(optr *Operator)
		want        bool
		wantErr     func(*testing.T, error)
		wantActions func(*testing.T, *Operator)
		wantSync    []configv1.Update
	}{
		{
			name: "progressing and previously failed, not reconciling",
			syncStatus: &SyncWorkerStatus{
				Reconciling: false,
				Actual:      configv1.Release{Version: "0.0.1-abc", Image: "image/image:v4.0.1"},
				Failure: &payload.UpdateError{
					Reason:  "UpdatePayloadIntegrity",
					Message: "unable to apply object",
				},
			},
			optr: &Operator{
				release: configv1.Release{
					Version: "4.0.1",
					Image:   "image/image:v4.0.1",
				},
				namespace: "test",
				name:      "default",
				client: fakeClientsetWithUpdates(
					&configv1.ClusterVersion{
						ObjectMeta: metav1.ObjectMeta{
							Name: "default",
						},
						Spec: configv1.ClusterVersionSpec{
							Channel: "fast",
						},
						Status: configv1.ClusterVersionStatus{
							History: []configv1.UpdateHistory{
								{Version: "4.0.1", Image: "image/image:v4.0.1", StartedTime: defaultStartedTime},
							},
							Desired:     configv1.Release{Version: "4.0.1", Image: "image/image:v4.0.1"},
							VersionHash: "",
							Conditions: []configv1.ClusterOperatorStatusCondition{
								{Type: configv1.OperatorAvailable, Status: configv1.ConditionFalse},
								{Type: internal.ClusterStatusFailing, Status: configv1.ConditionTrue, Reason: "UpdatePayloadIntegrity", Message: "unable to apply object"},
								{Type: configv1.OperatorProgressing, Status: configv1.ConditionTrue, Message: "Working towards 4.0.1"},
								{Type: configv1.RetrievedUpdates, Status: configv1.ConditionFalse},
							},
						},
					},
				),
			},
			wantActions: func(t *testing.T, optr *Operator) {
				f := optr.client.(*fake.Clientset)
				act := f.Actions()
				if len(act) != 2 {
					t.Fatalf("unknown actions: %d %#v", len(act), act)
				}
				expectGet(t, act[0], "clusterversions", "", "default")
				expectUpdateStatus(t, act[1], "clusterversions", "", &configv1.ClusterVersion{
					ObjectMeta: metav1.ObjectMeta{
						Name: "default",
					},
					Spec: configv1.ClusterVersionSpec{
						Channel: "fast",
					},
					Status: configv1.ClusterVersionStatus{
						History: []configv1.UpdateHistory{
							{State: configv1.PartialUpdate, Version: "0.0.1-abc", Image: "image/image:v4.0.1", StartedTime: defaultStartedTime},
							{State: configv1.PartialUpdate, Version: "4.0.1", Image: "image/image:v4.0.1", StartedTime: defaultStartedTime, CompletionTime: &defaultCompletionTime},
						},
						Desired:     configv1.Release{Version: "0.0.1-abc", Image: "image/image:v4.0.1"},
						VersionHash: "",
						Conditions: []configv1.ClusterOperatorStatusCondition{
							{Type: configv1.OperatorAvailable, Status: configv1.ConditionFalse},
							{Type: internal.ClusterStatusFailing, Status: configv1.ConditionTrue, Reason: "UpdatePayloadIntegrity", Message: "unable to apply object"},
							{Type: configv1.OperatorProgressing, Status: configv1.ConditionTrue, Reason: "UpdatePayloadIntegrity", Message: "Unable to apply 0.0.1-abc: the contents of the update are invalid"},
							{Type: configv1.RetrievedUpdates, Status: configv1.ConditionFalse},
							{Type: internal.ImplicitlyEnabledCapabilities, Status: "False", Reason: "AsExpected", Message: "Capabilities match configured spec"},
						},
					},
				})
			},
		},
		{
			name: "progressing and previously failed, reconciling",
			optr: &Operator{
				release: configv1.Release{
					Version: "4.0.1",
					Image:   "image/image:v4.0.1",
				},
				namespace: "test",
				name:      "default",
				configSync: &fakeSyncRecorder{
					Returns: &SyncWorkerStatus{
						Reconciling: true,
						Actual:      configv1.Release{Version: "0.0.1-abc", Image: "image/image:v4.0.1"},
						Failure: &payload.UpdateError{
							Reason:  "UpdatePayloadIntegrity",
							Message: "unable to apply object",
						},
					},
				},
				client: fakeClientsetWithUpdates(
					&configv1.ClusterVersion{
						ObjectMeta: metav1.ObjectMeta{
							Name: "default",
						},
						Spec: configv1.ClusterVersionSpec{
							Channel: "fast",
						},
						Status: configv1.ClusterVersionStatus{
							History: []configv1.UpdateHistory{
								{Version: "4.0.1", Image: "image/image:v4.0.1", StartedTime: defaultStartedTime},
							},
							Desired:     configv1.Release{Version: "4.0.1", Image: "image/image:v4.0.1"},
							VersionHash: "",
							Conditions: []configv1.ClusterOperatorStatusCondition{
								{Type: configv1.OperatorAvailable, Status: configv1.ConditionFalse},
								{Type: internal.ClusterStatusFailing, Status: configv1.ConditionTrue, Reason: "UpdatePayloadIntegrity", Message: "unable to apply object"},
								{Type: configv1.OperatorProgressing, Status: configv1.ConditionTrue, Message: "Working towards 4.0.1"},
								{Type: configv1.RetrievedUpdates, Status: configv1.ConditionFalse},
								{Type: internal.ImplicitlyEnabledCapabilities, Status: "False", Reason: "AsExpected", Message: "Capabilities match configured spec"},
							},
						},
					},
				),
			},
			wantActions: func(t *testing.T, optr *Operator) {
				f := optr.client.(*fake.Clientset)
				act := f.Actions()
				if len(act) != 2 {
					t.Fatalf("unknown actions: %d %#v", len(act), act)
				}
				expectGet(t, act[0], "clusterversions", "", "default")
				expectUpdateStatus(t, act[1], "clusterversions", "", &configv1.ClusterVersion{
					ObjectMeta: metav1.ObjectMeta{
						Name: "default",
					},
					Spec: configv1.ClusterVersionSpec{
						Channel: "fast",
					},
					Status: configv1.ClusterVersionStatus{
						History: []configv1.UpdateHistory{
							{State: configv1.PartialUpdate, Version: "0.0.1-abc", Image: "image/image:v4.0.1", StartedTime: defaultStartedTime},
							{State: configv1.PartialUpdate, Version: "4.0.1", Image: "image/image:v4.0.1", StartedTime: defaultStartedTime, CompletionTime: &defaultCompletionTime},
						},
						Desired:     configv1.Release{Version: "0.0.1-abc", Image: "image/image:v4.0.1"},
						VersionHash: "",
						Conditions: []configv1.ClusterOperatorStatusCondition{
							{Type: configv1.OperatorAvailable, Status: configv1.ConditionFalse},
							{Type: internal.ClusterStatusFailing, Status: configv1.ConditionTrue, Reason: "UpdatePayloadIntegrity", Message: "unable to apply object"},
							{Type: configv1.OperatorProgressing, Status: configv1.ConditionFalse, Reason: "UpdatePayloadIntegrity", Message: "Error while reconciling 0.0.1-abc: the contents of the update are invalid"},
							{Type: configv1.RetrievedUpdates, Status: configv1.ConditionFalse},
							{Type: internal.ImplicitlyEnabledCapabilities, Status: "False", Reason: "AsExpected", Message: "Capabilities match configured spec"},
						},
					},
				})
			},
		},
		{
			name: "progressing and previously failed, reconciling and multiple completions",
			optr: &Operator{
				release: configv1.Release{
					Version: "4.0.1",
					Image:   "image/image:v4.0.1",
				},
				namespace: "test",
				name:      "default",
				configSync: &fakeSyncRecorder{
					Returns: &SyncWorkerStatus{
						Reconciling: true,
						Completed:   2,
						Actual:      configv1.Release{Version: "0.0.1-abc", Image: "image/image:v4.0.1"},
						Failure: &payload.UpdateError{
							Reason:  "UpdatePayloadIntegrity",
							Message: "unable to apply object",
						},
					},
				},
				client: fakeClientsetWithUpdates(
					&configv1.ClusterVersion{
						ObjectMeta: metav1.ObjectMeta{
							Name: "default",
						},
						Spec: configv1.ClusterVersionSpec{
							Channel: "fast",
						},
						Status: configv1.ClusterVersionStatus{
							History: []configv1.UpdateHistory{
								{Version: "4.0.1", Image: "image/image:v4.0.1", StartedTime: defaultStartedTime},
							},
							Desired:     configv1.Release{Version: "4.0.1", Image: "image/image:v4.0.1"},
							VersionHash: "",
							Conditions: []configv1.ClusterOperatorStatusCondition{
								{Type: internal.ImplicitlyEnabledCapabilities, Status: "False", Reason: "AsExpected", Message: "Capabilities match configured spec"},
								{Type: configv1.OperatorAvailable, Status: configv1.ConditionFalse},
								{Type: internal.ClusterStatusFailing, Status: configv1.ConditionTrue, Reason: "UpdatePayloadIntegrity", Message: "unable to apply object"},
								{Type: configv1.OperatorProgressing, Status: configv1.ConditionTrue, Message: "Working towards 4.0.1"},
								{Type: configv1.RetrievedUpdates, Status: configv1.ConditionFalse},
							},
						},
					},
				),
			},
			wantActions: func(t *testing.T, optr *Operator) {
				f := optr.client.(*fake.Clientset)
				act := f.Actions()
				if len(act) != 2 {
					t.Fatalf("unknown actions: %d %#v", len(act), act)
				}
				expectGet(t, act[0], "clusterversions", "", "default")
				expectUpdateStatus(t, act[1], "clusterversions", "", &configv1.ClusterVersion{
					ObjectMeta: metav1.ObjectMeta{
						Name: "default",
					},
					Spec: configv1.ClusterVersionSpec{
						Channel: "fast",
					},
					Status: configv1.ClusterVersionStatus{
						History: []configv1.UpdateHistory{
							{State: configv1.CompletedUpdate, Version: "0.0.1-abc", Image: "image/image:v4.0.1", StartedTime: defaultStartedTime, CompletionTime: &defaultCompletionTime},
							{State: configv1.PartialUpdate, Version: "4.0.1", Image: "image/image:v4.0.1", StartedTime: defaultStartedTime, CompletionTime: &defaultCompletionTime},
						},
						Desired:     configv1.Release{Version: "0.0.1-abc", Image: "image/image:v4.0.1"},
						VersionHash: "",
						Conditions: []configv1.ClusterOperatorStatusCondition{
							{Type: internal.ImplicitlyEnabledCapabilities, Status: "False", Reason: "AsExpected", Message: "Capabilities match configured spec"},
							{Type: configv1.OperatorAvailable, Status: configv1.ConditionTrue, Message: "Done applying 0.0.1-abc"},
							{Type: internal.ClusterStatusFailing, Status: configv1.ConditionTrue, Reason: "UpdatePayloadIntegrity", Message: "unable to apply object"},
							{Type: configv1.OperatorProgressing, Status: configv1.ConditionFalse, Reason: "UpdatePayloadIntegrity", Message: "Error while reconciling 0.0.1-abc: the contents of the update are invalid"},
							{Type: configv1.RetrievedUpdates, Status: configv1.ConditionFalse},
						},
					},
				})
			},
		},
		{
			name: "progressing and encounters error during image sync",
			optr: &Operator{
				release: configv1.Release{
					Version: "4.0.1",
					Image:   "image/image:v4.0.1",
				},
				namespace: "test",
				name:      "default",
				configSync: &fakeSyncRecorder{
					Returns: &SyncWorkerStatus{
						Actual:      configv1.Release{Version: "0.0.1-abc", Image: "image/image:v4.0.1"},
						Failure:     fmt.Errorf("injected error"),
						VersionHash: "foo",
					},
				},
				client: fake.NewClientset(
					&configv1.ClusterVersion{
						ObjectMeta: metav1.ObjectMeta{
							Name: "default",
						},
						Spec: configv1.ClusterVersionSpec{
							Channel: "fast",
						},
						Status: configv1.ClusterVersionStatus{
							History: []configv1.UpdateHistory{
								{Version: "4.0.1", Image: "image/image:v4.0.1"},
							},
							VersionHash: "",
							Conditions: []configv1.ClusterOperatorStatusCondition{
								{Type: internal.ImplicitlyEnabledCapabilities, Status: "False", Reason: "AsExpected", Message: "Capabilities match configured spec"},
								{Type: configv1.OperatorAvailable, Status: configv1.ConditionFalse},
								{Type: internal.ClusterStatusFailing, Status: configv1.ConditionTrue, Message: "unable to apply object"},
								{Type: configv1.OperatorProgressing, Status: configv1.ConditionTrue, Message: "Working towards 4.0.1"},
								{Type: configv1.RetrievedUpdates, Status: configv1.ConditionFalse},
							},
						},
					},
				),
			},
			wantActions: func(t *testing.T, optr *Operator) {
				f := optr.client.(*fake.Clientset)
				act := f.Actions()
				if len(act) != 2 {
					t.Fatalf("unknown actions: %d %#v", len(act), act)
				}
				expectGet(t, act[0], "clusterversions", "", "default")
				// syncing config status
				expectUpdateStatus(t, act[1], "clusterversions", "", &configv1.ClusterVersion{
					ObjectMeta: metav1.ObjectMeta{
						Name: "default",
					},
					Spec: configv1.ClusterVersionSpec{
						Channel: "fast",
					},
					Status: configv1.ClusterVersionStatus{
						Desired: configv1.Release{Version: "0.0.1-abc", Image: "image/image:v4.0.1"},
						History: []configv1.UpdateHistory{
							{State: configv1.PartialUpdate, Version: "0.0.1-abc", Image: "image/image:v4.0.1", StartedTime: defaultStartedTime},
							{State: configv1.PartialUpdate, Version: "4.0.1", Image: "image/image:v4.0.1", StartedTime: metav1.Time{Time: time.Unix(0, 0)}, CompletionTime: &defaultCompletionTime},
						},
						VersionHash: "foo",
						Conditions: []configv1.ClusterOperatorStatusCondition{
							{Type: internal.ImplicitlyEnabledCapabilities, Status: "False", Reason: "AsExpected", Message: "Capabilities match configured spec"},
							{Type: configv1.OperatorAvailable, Status: configv1.ConditionFalse},
							{Type: internal.ClusterStatusFailing, Status: configv1.ConditionTrue, Message: "injected error"},
							{Type: configv1.OperatorProgressing, Status: configv1.ConditionTrue, Message: "Unable to apply 0.0.1-abc: an error occurred"},
							{Type: configv1.RetrievedUpdates, Status: configv1.ConditionFalse},
						},
					},
				})
			},
		},
		{
			name: "invalid image reports image error",
			syncStatus: &SyncWorkerStatus{
				Failure: os.ErrNotExist,
				Actual:  configv1.Release{Image: "image/image:v4.0.1", Version: "4.0.1"},
			},
			optr: &Operator{
				release: configv1.Release{
					Version: "4.0.1",
					Image:   "image/image:v4.0.1",
				},
				namespace: "test",
				name:      "default",
				client: fake.NewClientset(
					&configv1.ClusterVersion{
						ObjectMeta: metav1.ObjectMeta{
							Name: "default",
						},
						Spec: configv1.ClusterVersionSpec{
							Channel: "fast",
						},
					},
				),
			},
			wantActions: func(t *testing.T, optr *Operator) {
				f := optr.client.(*fake.Clientset)
				act := f.Actions()
				if len(act) != 2 {
					t.Fatalf("unknown actions: %d %#v", len(act), act)
				}
				expectGet(t, act[0], "clusterversions", "", "default")
				expectUpdateStatus(t, act[1], "clusterversions", "", &configv1.ClusterVersion{
					ObjectMeta: metav1.ObjectMeta{
						Name: "default",
					},
					Spec: configv1.ClusterVersionSpec{
						Channel: "fast",
					},
					Status: configv1.ClusterVersionStatus{
						Desired: configv1.Release{Image: "image/image:v4.0.1", Version: "4.0.1"},
						History: []configv1.UpdateHistory{
							{State: configv1.PartialUpdate, Version: "4.0.1", Image: "image/image:v4.0.1", StartedTime: defaultStartedTime},
						},
						VersionHash: "",
						Conditions: []configv1.ClusterOperatorStatusCondition{
							{Type: internal.ImplicitlyEnabledCapabilities, Status: "False", Reason: "AsExpected", Message: "Capabilities match configured spec"},
							{Type: configv1.OperatorAvailable, Status: configv1.ConditionFalse},
							{Type: internal.ClusterStatusFailing, Status: configv1.ConditionTrue, Message: "file does not exist"},
							{Type: configv1.OperatorProgressing, Status: configv1.ConditionTrue, Message: "Unable to apply 4.0.1: an error occurred"},
							{Type: configv1.RetrievedUpdates, Status: configv1.ConditionFalse},
						},
					},
				})
			},
		},
		{
			name: "invalid image while progressing preserves progressing order and partial history",
			syncStatus: &SyncWorkerStatus{
				Done:    600,
				Total:   1000,
				Failure: os.ErrNotExist,
				Actual:  configv1.Release{Image: "image/image:v4.0.1", Version: "4.0.1"},
			},
			optr: &Operator{
				release: configv1.Release{
					Version: "4.0.1",
					Image:   "image/image:v4.0.1",
				},
				namespace: "test",
				name:      "default",
				client: fake.NewClientset(
					&configv1.ClusterVersion{
						ObjectMeta: metav1.ObjectMeta{
							Name: "default",
						},
						Spec: configv1.ClusterVersionSpec{
							Channel: "fast",
						},
						Status: configv1.ClusterVersionStatus{
							History: []configv1.UpdateHistory{
								// this is a partial history struct, which we will fill out
								{Version: "4.0.1", Image: "image/image:v4.0.1"},
							},
							VersionHash: "",
							Conditions: []configv1.ClusterOperatorStatusCondition{
								{Type: configv1.OperatorProgressing, Status: configv1.ConditionTrue, Message: "Unable to apply 4.0.1: unable to apply object"},
							},
						},
					},
				),
			},
			wantActions: func(t *testing.T, optr *Operator) {
				f := optr.client.(*fake.Clientset)
				act := f.Actions()
				if len(act) != 2 {
					t.Fatalf("unknown actions: %d %#v", len(act), act)
				}
				expectGet(t, act[0], "clusterversions", "", "default")
				expectUpdateStatus(t, act[1], "clusterversions", "", &configv1.ClusterVersion{
					ObjectMeta: metav1.ObjectMeta{
						Name: "default",
					},
					Spec: configv1.ClusterVersionSpec{
						Channel: "fast",
					},
					Status: configv1.ClusterVersionStatus{
						Desired: configv1.Release{Image: "image/image:v4.0.1", Version: "4.0.1"},
						History: []configv1.UpdateHistory{
							// we populate state, but not startedTime
							{State: configv1.PartialUpdate, Version: "4.0.1", Image: "image/image:v4.0.1", StartedTime: metav1.Time{Time: time.Unix(0, 0)}},
						},
						VersionHash: "",
						Conditions: []configv1.ClusterOperatorStatusCondition{
							// the order of progressing in the conditions array is preserved
							{Type: configv1.OperatorProgressing, Status: configv1.ConditionTrue, Message: "Unable to apply 4.0.1: an error occurred"},
							{Type: internal.ImplicitlyEnabledCapabilities, Status: "False", Reason: "AsExpected", Message: "Capabilities match configured spec"},
							{Type: configv1.OperatorAvailable, Status: configv1.ConditionFalse},
							{Type: internal.ClusterStatusFailing, Status: configv1.ConditionTrue, Message: "file does not exist"},
							{Type: configv1.RetrievedUpdates, Status: configv1.ConditionFalse},
						},
					},
				})
			},
		},
		{
			name: "set initial status conditions",
			syncStatus: &SyncWorkerStatus{
				Actual: configv1.Release{Image: "image/image:v4.0.1"},
			},
			optr: &Operator{
				release: configv1.Release{
					Image: "image/image:v4.0.1",
				},
				namespace: "test",
				name:      "default",
				client: fakeClientsetWithUpdates(&configv1.ClusterVersion{
					ObjectMeta: metav1.ObjectMeta{
						Name:            "default",
						ResourceVersion: "1",
					},
					Spec: configv1.ClusterVersionSpec{
						ClusterID: configv1.ClusterID(id),
						Upstream:  configv1.URL("http://localhost:8080/graph"),
						Channel:   "fast",
					},
				}),
			},
			wantActions: func(t *testing.T, optr *Operator) {
				f := optr.client.(*fake.Clientset)
				act := f.Actions()
				if len(act) != 2 {
					t.Fatalf("unknown actions: %d %#v", len(act), act)
				}
				expectGet(t, act[0], "clusterversions", "", "default")
				expectUpdateStatus(t, act[1], "clusterversions", "", &configv1.ClusterVersion{
					ObjectMeta: metav1.ObjectMeta{
						Name:            "default",
						ResourceVersion: "1",
					},
					Spec: configv1.ClusterVersionSpec{
						Upstream: configv1.URL("http://localhost:8080/graph"),
						Channel:  "fast",
					},
					Status: configv1.ClusterVersionStatus{
						History: []configv1.UpdateHistory{
							{
								State:       configv1.PartialUpdate,
								Image:       "image/image:v4.0.1",
								Version:     "", // we don't know our image yet and release.Version is unset
								StartedTime: defaultStartedTime,
							},
						},
						Desired:     configv1.Release{Image: "image/image:v4.0.1"},
						VersionHash: "",
						Conditions: []configv1.ClusterOperatorStatusCondition{
							{Type: internal.ImplicitlyEnabledCapabilities, Status: "False", Reason: "AsExpected", Message: "Capabilities match configured spec"},
							{Type: configv1.OperatorAvailable, Status: configv1.ConditionFalse},
							{Type: internal.ClusterStatusFailing, Status: configv1.ConditionFalse},
							{Type: configv1.OperatorProgressing, Status: configv1.ConditionTrue, Message: "Working towards image/image:v4.0.1"},
							{Type: configv1.RetrievedUpdates, Status: configv1.ConditionFalse},
						},
					},
				})
			},
		},
		{
			name: "record a new version entry if the controller is restarted with a new image",
			syncStatus: &SyncWorkerStatus{
				Actual: configv1.Release{Image: "image/image:v4.0.2", Version: "4.0.2"},
			},
			optr: &Operator{
				release: configv1.Release{
					Version: "4.0.2",
					Image:   "image/image:v4.0.2",
				},
				namespace: "test",
				name:      "default",
				client: fakeClientsetWithUpdates(&configv1.ClusterVersion{
					ObjectMeta: metav1.ObjectMeta{
						Name:            "default",
						ResourceVersion: "1",
					},
					Spec: configv1.ClusterVersionSpec{
						ClusterID: configv1.ClusterID(id),
						Upstream:  configv1.URL("http://localhost:8080/graph"),
						Channel:   "fast",
					},
					Status: configv1.ClusterVersionStatus{
						History: []configv1.UpdateHistory{
							{
								State:       configv1.PartialUpdate,
								Image:       "image/image:v4.0.1",
								Version:     "", // we didn't know our image before
								StartedTime: defaultStartedTime,
							},
						},
						Desired:     configv1.Release{Image: "image/image:v4.0.1"},
						VersionHash: "",
						Conditions: []configv1.ClusterOperatorStatusCondition{
							{Type: internal.ImplicitlyEnabledCapabilities, Status: "False", Reason: "AsExpected", Message: "Capabilities match configured spec"},
							{Type: configv1.OperatorAvailable, Status: configv1.ConditionFalse},
							{Type: internal.ClusterStatusFailing, Status: configv1.ConditionFalse},
							{Type: configv1.OperatorProgressing, Status: configv1.ConditionTrue, Message: "Initializing, will work towards image/image:v4.0.1"},
							{Type: configv1.RetrievedUpdates, Status: configv1.ConditionFalse},
						},
					},
				}),
			},
			wantActions: func(t *testing.T, optr *Operator) {
				f := optr.client.(*fake.Clientset)
				act := f.Actions()
				if len(act) != 2 {
					t.Fatalf("unknown actions: %d %#v", len(act), act)
				}
				expectGet(t, act[0], "clusterversions", "", "default")
				expectUpdateStatus(t, act[1], "clusterversions", "", &configv1.ClusterVersion{
					ObjectMeta: metav1.ObjectMeta{
						Name:            "default",
						ResourceVersion: "1",
					},
					Spec: configv1.ClusterVersionSpec{
						Upstream: configv1.URL("http://localhost:8080/graph"),
						Channel:  "fast",
					},
					Status: configv1.ClusterVersionStatus{
						History: []configv1.UpdateHistory{
							{
								State:       configv1.PartialUpdate,
								Image:       "image/image:v4.0.2",
								Version:     "4.0.2",
								StartedTime: defaultStartedTime,
							},
							{
								State:          configv1.PartialUpdate,
								Image:          "image/image:v4.0.1",
								Version:        "",
								StartedTime:    defaultStartedTime,
								CompletionTime: &defaultCompletionTime,
							},
						},
						Desired:     configv1.Release{Image: "image/image:v4.0.2", Version: "4.0.2"},
						VersionHash: "",
						Conditions: []configv1.ClusterOperatorStatusCondition{
							{Type: internal.ImplicitlyEnabledCapabilities, Status: "False", Reason: "AsExpected", Message: "Capabilities match configured spec"},
							{Type: configv1.OperatorAvailable, Status: configv1.ConditionFalse},
							{Type: internal.ClusterStatusFailing, Status: configv1.ConditionFalse},
							{Type: configv1.OperatorProgressing, Status: configv1.ConditionTrue, Message: "Working towards 4.0.2"},
							{Type: configv1.RetrievedUpdates, Status: configv1.ConditionFalse},
						},
					},
				})
			},
		},
		{
			name: "when user cancels desired update, clear status desired",
			syncStatus: &SyncWorkerStatus{
				// TODO: we can't actually react to spec changes in a single sync round
				// because the sync worker updates desired state and cancels under the
				// lock, so the sync worker loop will never report the status of the
				// update unless we add some sort of delay - which might make clearing status
				// slightly more useful to the user (instead of two status updates you get
				// one).
				Actual: configv1.Release{Image: "image/image:v4.0.1", Version: "4.0.1"},
			},
			optr: &Operator{
				release: configv1.Release{
					Version: "4.0.1",
					Image:   "image/image:v4.0.1",
				},
				namespace: "test",
				name:      "default",
				client: fakeClientsetWithUpdates(&configv1.ClusterVersion{
					ObjectMeta: metav1.ObjectMeta{
						Name:            "default",
						ResourceVersion: "1",
					},
					Spec: configv1.ClusterVersionSpec{
						ClusterID: configv1.ClusterID(id),
						Upstream:  configv1.URL("http://localhost:8080/graph"),
						Channel:   "fast",
					},
					Status: configv1.ClusterVersionStatus{
						History: []configv1.UpdateHistory{
							{
								State:       configv1.PartialUpdate,
								Image:       "image/image:v4.0.2",
								Version:     "4.0.2",
								StartedTime: defaultStartedTime,
							},
							{
								State:          configv1.CompletedUpdate,
								Image:          "image/image:v4.0.1",
								Version:        "4.0.1",
								StartedTime:    defaultStartedTime,
								CompletionTime: &defaultCompletionTime,
							},
						},
						Desired:     configv1.Release{Image: "image/image:v4.0.2"},
						VersionHash: "",
						Conditions: []configv1.ClusterOperatorStatusCondition{
							{Type: internal.ImplicitlyEnabledCapabilities, Status: "False", Reason: "AsExpected", Message: "Capabilities match configured spec"},
							{Type: configv1.OperatorAvailable, Status: configv1.ConditionFalse},
							{Type: internal.ClusterStatusFailing, Status: configv1.ConditionFalse},
							{Type: configv1.OperatorProgressing, Status: configv1.ConditionTrue, Message: "Working towards 4.0.2"},
							{Type: configv1.RetrievedUpdates, Status: configv1.ConditionFalse},
						},
					},
				}),
			},
			wantActions: func(t *testing.T, optr *Operator) {
				f := optr.client.(*fake.Clientset)
				act := f.Actions()
				if len(act) != 2 {
					t.Fatalf("unknown actions: %d %#v", len(act), act)
				}
				expectGet(t, act[0], "clusterversions", "", "default")
				expectUpdateStatus(t, act[1], "clusterversions", "", &configv1.ClusterVersion{
					ObjectMeta: metav1.ObjectMeta{
						Name:            "default",
						ResourceVersion: "1",
					},
					Spec: configv1.ClusterVersionSpec{
						Upstream: configv1.URL("http://localhost:8080/graph"),
						Channel:  "fast",
					},
					Status: configv1.ClusterVersionStatus{
						History: []configv1.UpdateHistory{
							{
								State:       configv1.PartialUpdate,
								Image:       "image/image:v4.0.1",
								Version:     "4.0.1",
								StartedTime: defaultStartedTime,
							},
							{
								State:          configv1.PartialUpdate,
								Image:          "image/image:v4.0.2",
								Version:        "4.0.2",
								StartedTime:    defaultStartedTime,
								CompletionTime: &defaultCompletionTime,
							},
							{
								State:          configv1.CompletedUpdate,
								Image:          "image/image:v4.0.1",
								Version:        "4.0.1",
								StartedTime:    defaultStartedTime,
								CompletionTime: &defaultCompletionTime,
							},
						},
						Desired: configv1.Release{
							Version: "4.0.1",
							Image:   "image/image:v4.0.1",
						},
						VersionHash: "",
						Conditions: []configv1.ClusterOperatorStatusCondition{
							{Type: internal.ImplicitlyEnabledCapabilities, Status: "False", Reason: "AsExpected", Message: "Capabilities match configured spec"},
							{Type: configv1.OperatorAvailable, Status: configv1.ConditionFalse},
							{Type: internal.ClusterStatusFailing, Status: configv1.ConditionFalse},
							// we don't reset the message here until the image is loaded
							{Type: configv1.OperatorProgressing, Status: configv1.ConditionTrue, Message: "Working towards 4.0.1"},
							{Type: configv1.RetrievedUpdates, Status: configv1.ConditionFalse},
						},
					},
				})
			},
		},
		{
			name: "after desired update is cancelled, revert to progressing",
			syncStatus: &SyncWorkerStatus{
				Actual: configv1.Release{Image: "image/image:v4.0.1", Version: "4.0.1"},
				Done:   334,
				Total:  1000,
			},
			optr: &Operator{
				release: configv1.Release{
					Version: "4.0.1",
					Image:   "image/image:v4.0.1",
				},
				namespace: "test",
				name:      "default",
				client: fakeClientsetWithUpdates(&configv1.ClusterVersion{
					ObjectMeta: metav1.ObjectMeta{
						Name:            "default",
						ResourceVersion: "1",
					},
					Spec: configv1.ClusterVersionSpec{
						ClusterID: configv1.ClusterID(id),
						Upstream:  configv1.URL("http://localhost:8080/graph"),
						Channel:   "fast",
					},
					Status: configv1.ClusterVersionStatus{
						History: []configv1.UpdateHistory{
							{
								State:       configv1.PartialUpdate,
								Image:       "image/image:v4.0.1",
								Version:     "4.0.1",
								StartedTime: defaultStartedTime,
							},
							{
								State:          configv1.PartialUpdate,
								Image:          "image/image:v4.0.2",
								Version:        "4.0.2",
								StartedTime:    defaultStartedTime,
								CompletionTime: &defaultCompletionTime,
							},
							{
								State:          configv1.CompletedUpdate,
								Image:          "image/image:v4.0.1",
								Version:        "4.0.1",
								StartedTime:    defaultStartedTime,
								CompletionTime: &defaultCompletionTime,
							},
						},
						Desired:     configv1.Release{Image: "image/image:v4.0.1", Version: "4.0.1"},
						VersionHash: "",
						Conditions: []configv1.ClusterOperatorStatusCondition{
							{Type: internal.ImplicitlyEnabledCapabilities, Status: "False", Reason: "AsExpected", Message: "Capabilities match configured spec"},
							{Type: configv1.OperatorAvailable, Status: configv1.ConditionFalse},
							{Type: internal.ClusterStatusFailing, Status: configv1.ConditionFalse},
							// we don't reset the message here until the image is loaded
							{Type: configv1.OperatorProgressing, Status: configv1.ConditionTrue, Message: "Working towards 4.0.2"},
							{Type: configv1.RetrievedUpdates, Status: configv1.ConditionFalse},
						},
					},
				}),
			},
			wantActions: func(t *testing.T, optr *Operator) {
				f := optr.client.(*fake.Clientset)
				act := f.Actions()
				if len(act) != 2 {
					t.Fatalf("unknown actions: %d %#v", len(act), act)
				}
				expectGet(t, act[0], "clusterversions", "", "default")
				expectUpdateStatus(t, act[1], "clusterversions", "", &configv1.ClusterVersion{
					ObjectMeta: metav1.ObjectMeta{
						Name:            "default",
						ResourceVersion: "1",
					},
					Spec: configv1.ClusterVersionSpec{
						Upstream: configv1.URL("http://localhost:8080/graph"),
						Channel:  "fast",
					},
					Status: configv1.ClusterVersionStatus{
						History: []configv1.UpdateHistory{
							{
								State:       configv1.PartialUpdate,
								Image:       "image/image:v4.0.1",
								Version:     "4.0.1",
								StartedTime: defaultStartedTime,
							},
							{
								State:          configv1.PartialUpdate,
								Image:          "image/image:v4.0.2",
								Version:        "4.0.2",
								StartedTime:    defaultStartedTime,
								CompletionTime: &defaultCompletionTime,
							},
							{
								State:          configv1.CompletedUpdate,
								Image:          "image/image:v4.0.1",
								Version:        "4.0.1",
								StartedTime:    defaultStartedTime,
								CompletionTime: &defaultCompletionTime,
							},
						},
						Desired:     configv1.Release{Image: "image/image:v4.0.1", Version: "4.0.1"},
						VersionHash: "",
						Conditions: []configv1.ClusterOperatorStatusCondition{
							{Type: internal.ImplicitlyEnabledCapabilities, Status: "False", Reason: "AsExpected", Message: "Capabilities match configured spec"},
							{Type: configv1.OperatorAvailable, Status: configv1.ConditionFalse},
							{Type: internal.ClusterStatusFailing, Status: configv1.ConditionFalse},
							// we correct the message that was incorrect from the previous state
							{Type: configv1.OperatorProgressing, Status: configv1.ConditionTrue, Message: "Working towards 4.0.1: 334 of 1000 done (33% complete)"},
							{Type: configv1.RetrievedUpdates, Status: configv1.ConditionFalse},
						},
					},
				})
			},
		},
		{
			name: "report partial retrieved version",
			syncStatus: &SyncWorkerStatus{
				Actual: configv1.Release{Image: "image/image:v4.0.1"},
			},
			optr: &Operator{
				release: configv1.Release{
					Version: "4.0.1",
					Image:   "image/image:v4.0.1",
				},
				namespace: "test",
				name:      "default",
				client: fakeClientsetWithUpdates(&configv1.ClusterVersion{
					ObjectMeta: metav1.ObjectMeta{
						Name:            "default",
						ResourceVersion: "1",
					},
					Spec: configv1.ClusterVersionSpec{
						ClusterID: configv1.ClusterID(id),
						Upstream:  configv1.URL("http://localhost:8080/graph"),
						Channel:   "fast",
					},
					Status: configv1.ClusterVersionStatus{
						History: []configv1.UpdateHistory{
							{
								State:          configv1.PartialUpdate,
								Image:          "image/image:v4.0.2",
								Version:        "4.0.2",
								StartedTime:    defaultStartedTime,
								CompletionTime: &defaultCompletionTime,
							},
							{
								State:          configv1.CompletedUpdate,
								Image:          "image/image:v4.0.1",
								Version:        "4.0.1",
								StartedTime:    defaultStartedTime,
								CompletionTime: &defaultCompletionTime,
							},
						},
						Desired:     configv1.Release{Image: "image/image:v4.0.1", Version: "4.0.1"},
						VersionHash: "",
						Conditions:  []configv1.ClusterOperatorStatusCondition{},
					},
				}),
			},
			wantActions: func(t *testing.T, optr *Operator) {
				f := optr.client.(*fake.Clientset)
				act := f.Actions()
				if len(act) != 2 {
					t.Fatalf("unknown actions: %d %#v", len(act), act)
				}
				expectGet(t, act[0], "clusterversions", "", "default")
				expectUpdateStatus(t, act[1], "clusterversions", "", &configv1.ClusterVersion{
					ObjectMeta: metav1.ObjectMeta{
						Name:            "default",
						ResourceVersion: "1",
					},
					Spec: configv1.ClusterVersionSpec{
						Upstream: configv1.URL("http://localhost:8080/graph"),
						Channel:  "fast",
					},
					Status: configv1.ClusterVersionStatus{
						History: []configv1.UpdateHistory{
							{
								State:       configv1.PartialUpdate,
								Image:       "image/image:v4.0.1",
								Version:     "4.0.1",
								StartedTime: defaultStartedTime,
							},
							{
								State:          configv1.PartialUpdate,
								Image:          "image/image:v4.0.2",
								Version:        "4.0.2",
								StartedTime:    defaultStartedTime,
								CompletionTime: &defaultCompletionTime,
							},
							{
								State:          configv1.CompletedUpdate,
								Image:          "image/image:v4.0.1",
								Version:        "4.0.1",
								StartedTime:    defaultStartedTime,
								CompletionTime: &defaultCompletionTime,
							},
						},
						Desired: configv1.Release{
							Version: "4.0.1",
							Image:   "image/image:v4.0.1",
						},
						VersionHash: "",
						Conditions: []configv1.ClusterOperatorStatusCondition{
							{Type: internal.ImplicitlyEnabledCapabilities, Status: "False", Reason: "AsExpected", Message: "Capabilities match configured spec"},
							{Type: configv1.OperatorAvailable, Status: configv1.ConditionFalse},
							{Type: internal.ClusterStatusFailing, Status: configv1.ConditionFalse},
							// we correct the message that was incorrect from the previous state
							{Type: configv1.OperatorProgressing, Status: configv1.ConditionTrue, Message: "Working towards image/image:v4.0.1"},
							{Type: configv1.RetrievedUpdates, Status: configv1.ConditionFalse},
						},
					},
				})
			},
		},
		{
			name: "after initial status is set, set hash and correct version number",
			syncStatus: &SyncWorkerStatus{
				VersionHash: "xyz",
				Actual:      configv1.Release{Image: "image/image:v4.0.1", Version: "0.0.1-abc"},
			},
			optr: &Operator{
				release: configv1.Release{
					Image: "image/image:v4.0.1",
				},
				namespace: "test",
				name:      "default",
				client: fakeClientsetWithUpdates(&configv1.ClusterVersion{
					ObjectMeta: metav1.ObjectMeta{
						Name:            "default",
						ResourceVersion: "1",
					},
					Spec: configv1.ClusterVersionSpec{
						ClusterID: configv1.ClusterID(id),
						Upstream:  configv1.URL("http://localhost:8080/graph"),
						Channel:   "fast",
					},
					Status: configv1.ClusterVersionStatus{
						History: []configv1.UpdateHistory{
							{
								State:       configv1.PartialUpdate,
								Image:       "image/image:v4.0.1",
								StartedTime: defaultStartedTime,
							},
						},
						Desired:     configv1.Release{Image: "image/image:v4.0.1"},
						VersionHash: "",
						Conditions: []configv1.ClusterOperatorStatusCondition{
							{Type: internal.ImplicitlyEnabledCapabilities, Status: "False", Reason: "AsExpected", Message: "Capabilities match configured spec"},
							{Type: configv1.OperatorAvailable, Status: configv1.ConditionFalse},
							{Type: internal.ClusterStatusFailing, Status: configv1.ConditionFalse},
							{Type: configv1.OperatorProgressing, Status: configv1.ConditionTrue, Message: "Initializing, will work towards image/image:v4.0.1"},
							{Type: configv1.RetrievedUpdates, Status: configv1.ConditionFalse},
						},
					},
				}),
			},
			wantActions: func(t *testing.T, optr *Operator) {
				f := optr.client.(*fake.Clientset)
				act := f.Actions()
				if len(act) != 2 {
					t.Fatalf("unknown actions: %d %#v", len(act), act)
				}
				expectGet(t, act[0], "clusterversions", "", "default")
				// will use the version from content1 (the image) when we set the progressing condition
				expectUpdateStatus(t, act[1], "clusterversions", "", &configv1.ClusterVersion{
					ObjectMeta: metav1.ObjectMeta{
						Name:            "default",
						ResourceVersion: "1",
					},
					Spec: configv1.ClusterVersionSpec{
						Upstream: configv1.URL("http://localhost:8080/graph"),
						Channel:  "fast",
					},
					Status: configv1.ClusterVersionStatus{
						History: []configv1.UpdateHistory{
							{State: configv1.PartialUpdate, Image: "image/image:v4.0.1", Version: "0.0.1-abc", StartedTime: defaultStartedTime},
						},
						Desired:     configv1.Release{Image: "image/image:v4.0.1", Version: "0.0.1-abc"},
						VersionHash: "xyz",
						Conditions: []configv1.ClusterOperatorStatusCondition{
							{Type: internal.ImplicitlyEnabledCapabilities, Status: "False", Reason: "AsExpected", Message: "Capabilities match configured spec"},
							{Type: configv1.OperatorAvailable, Status: configv1.ConditionFalse},
							{Type: internal.ClusterStatusFailing, Status: configv1.ConditionFalse},
							{Type: configv1.OperatorProgressing, Status: configv1.ConditionTrue, Message: "Working towards 0.0.1-abc"},
							{Type: configv1.RetrievedUpdates, Status: configv1.ConditionFalse},
						},
					},
				})
			},
		},
		{
			name: "version is live and was recently synced, do nothing",
			syncStatus: &SyncWorkerStatus{
				Generation:  2,
				Reconciling: true,
				Completed:   1,
				VersionHash: "xyz",
				Actual:      configv1.Release{Image: "image/image:v4.0.1", Version: "0.0.1-abc"},
			},
			optr: &Operator{
				release: configv1.Release{
					Version: "0.0.1-abc",
					Image:   "image/image:v4.0.1",
				},
				namespace: "test",
				name:      "default",
				client: fakeClientsetWithUpdates(
					&configv1.ClusterVersion{
						ObjectMeta: metav1.ObjectMeta{
							Name:       "default",
							Generation: 2,
						},
						Spec: configv1.ClusterVersionSpec{
							ClusterID: configv1.ClusterID(id),
							Upstream:  configv1.URL("http://localhost:8080/graph"),
							Channel:   "fast",
						},
						Status: configv1.ClusterVersionStatus{
							History: []configv1.UpdateHistory{
								{
									State:          configv1.CompletedUpdate,
									Image:          "image/image:v4.0.1",
									Version:        "0.0.1-abc",
									CompletionTime: &defaultStartedTime,
								},
							},
							Desired: configv1.Release{
								Version: "0.0.1-abc",
								Image:   "image/image:v4.0.1",
							},
							VersionHash:        "xyz",
							ObservedGeneration: 2,
							Conditions: []configv1.ClusterOperatorStatusCondition{
								{Type: internal.ImplicitlyEnabledCapabilities, Status: "False", Reason: "AsExpected", Message: "Capabilities match configured spec"},
								{Type: configv1.OperatorAvailable, Status: configv1.ConditionTrue, Message: "Done applying 0.0.1-abc"},
								{Type: internal.ClusterStatusFailing, Status: configv1.ConditionFalse},
								{Type: configv1.OperatorProgressing, Status: configv1.ConditionFalse, Message: "Cluster version is 0.0.1-abc"},
								{Type: configv1.RetrievedUpdates, Status: configv1.ConditionFalse},
							},
						},
					},
				),
			},
			wantActions: func(t *testing.T, optr *Operator) {
				f := optr.client.(*fake.Clientset)
				act := f.Actions()
				if len(act) != 1 {
					t.Fatalf("unexpected actions %d: %s", len(act), spew.Sdump(act))
				}
				expectGet(t, act[0], "clusterversions", "", "default")
			},
		},
		{
			name: "new available updates, version is live and was recently synced, sync",
			syncStatus: &SyncWorkerStatus{
				Generation:  2,
				Reconciling: true,
				Completed:   1,
				Actual:      configv1.Release{Image: "image/image:v4.0.1", Version: "0.0.1-abc"},
			},
			optr: &Operator{
				release: configv1.Release{
					Image: "image/image:v4.0.1",
				},
				namespace: "test",
				name:      "default",
				availableUpdates: &availableUpdates{
					UpdateService: "http://localhost:8080/graph",
					Channel:       "fast",
					Current: configv1.Release{
						Version:  "0.0.1-abc",
						Image:    "image/image:v4.0.1",
						URL:      configv1.URL("https://example.com/v4.0.1"),
						Channels: []string{"channel-a", "channel-b", "channel-c"},
					},
					Updates: []configv1.Release{
						{
							Version:  "4.0.2",
							Image:    "test/image:1",
							URL:      configv1.URL("https://example.com/v4.0.2"),
							Channels: []string{"channel-a", "channel-d"},
						},
						{Version: "4.0.3", Image: "test/image:2"},
					},
					Condition: configv1.ClusterOperatorStatusCondition{
						Type:   configv1.RetrievedUpdates,
						Status: configv1.ConditionTrue,
					},
				},
				client: fakeClientsetWithUpdates(&configv1.ClusterVersion{
					ObjectMeta: metav1.ObjectMeta{
						Name:       "default",
						Generation: 2,
					},
					Spec: configv1.ClusterVersionSpec{
						ClusterID: configv1.ClusterID(id),
						Upstream:  configv1.URL("http://localhost:8080/graph"),
						Channel:   "fast",
					},
					Status: configv1.ClusterVersionStatus{
						ObservedGeneration: 2,
						Conditions: []configv1.ClusterOperatorStatusCondition{
							{Type: internal.ImplicitlyEnabledCapabilities, Status: "False", Reason: "AsExpected", Message: "Capabilities match configured spec"},
							{Type: configv1.OperatorAvailable, Status: configv1.ConditionFalse},
							{Type: configv1.OperatorProgressing, Status: configv1.ConditionFalse, Message: "Cluster version is 0.0.1-abc"},
							{Type: internal.ClusterStatusFailing, Status: configv1.ConditionFalse},
							{Type: configv1.RetrievedUpdates, Status: configv1.ConditionFalse},
						},
					},
				}),
			},
			wantActions: func(t *testing.T, optr *Operator) {
				f := optr.client.(*fake.Clientset)
				act := f.Actions()
				if len(act) != 2 {
					t.Fatalf("unknown actions: %d %#v", len(act), act)
				}
				expectGet(t, act[0], "clusterversions", "", "default")
				expectUpdateStatus(t, act[1], "clusterversions", "", &configv1.ClusterVersion{
					ObjectMeta: metav1.ObjectMeta{
						Name:       "default",
						Generation: 2,
					},
					Spec: configv1.ClusterVersionSpec{
						ClusterID: configv1.ClusterID(id),
						Upstream:  configv1.URL("http://localhost:8080/graph"),
						Channel:   "fast",
					},
					Status: configv1.ClusterVersionStatus{
						AvailableUpdates: []configv1.Release{
							{
								Version:  "4.0.2",
								Image:    "test/image:1",
								URL:      configv1.URL("https://example.com/v4.0.2"),
								Channels: []string{"channel-a", "channel-d"},
							},
							{Version: "4.0.3", Image: "test/image:2"},
						},
						History: []configv1.UpdateHistory{
							{State: configv1.CompletedUpdate, Version: "0.0.1-abc", Image: "image/image:v4.0.1", StartedTime: defaultStartedTime, CompletionTime: &defaultCompletionTime},
						},
						Desired: configv1.Release{
							Image:    "image/image:v4.0.1",
							Version:  "0.0.1-abc",
							URL:      configv1.URL("https://example.com/v4.0.1"),
							Channels: []string{"channel-a", "channel-b", "channel-c"},
						},
						ObservedGeneration: 2,
						Conditions: []configv1.ClusterOperatorStatusCondition{
							{Type: internal.ImplicitlyEnabledCapabilities, Status: "False", Reason: "AsExpected", Message: "Capabilities match configured spec"},
							{Type: configv1.OperatorAvailable, Status: configv1.ConditionTrue, Message: "Done applying 0.0.1-abc"},
							{Type: configv1.OperatorProgressing, Status: configv1.ConditionFalse, Message: "Cluster version is 0.0.1-abc"},
							{Type: internal.ClusterStatusFailing, Status: configv1.ConditionFalse},
							{Type: configv1.RetrievedUpdates, Status: configv1.ConditionTrue},
						},
					},
				})
			},
		},
		{
			name: "new upgradable conditions, version is live and was recently synced, sync",
			syncStatus: &SyncWorkerStatus{
				Generation:  2,
				Reconciling: true,
				Completed:   1,
				Actual:      configv1.Release{Image: "image/image:v4.0.1", Version: "0.0.1-abc"},
			},
			optr: &Operator{
				release: configv1.Release{
					Image: "image/image:v4.0.1",
				},
				namespace: "test",
				name:      "default",
				upgradeable: &mockrisk.Mock{
					InternalName: "Mock",
					InternalRisks: []configv1.ConditionalUpdateRisk{
						{Name: "RiskA", Message: "MessageA", URL: "https://example.com/a", MatchingRules: []configv1.ClusterCondition{{Type: "Always"}}},
						{Name: "RiskB", Message: "MessageB", URL: "https://example.com/b", MatchingRules: []configv1.ClusterCondition{{Type: "Always"}}},
					},
				},
				client: fakeClientsetWithUpdates(&configv1.ClusterVersion{
					ObjectMeta: metav1.ObjectMeta{
						Name:       "default",
						Generation: 2,
					},
					Spec: configv1.ClusterVersionSpec{
						ClusterID: configv1.ClusterID(id),
						Upstream:  configv1.URL("http://localhost:8080/graph"),
						Channel:   "fast",
					},
					Status: configv1.ClusterVersionStatus{
						ObservedGeneration: 2,
						Conditions: []configv1.ClusterOperatorStatusCondition{
							{Type: internal.ImplicitlyEnabledCapabilities, Status: "False", Reason: "AsExpected", Message: "Capabilities match configured spec"},
							{Type: configv1.OperatorAvailable, Status: configv1.ConditionFalse},
							{Type: configv1.OperatorProgressing, Status: configv1.ConditionFalse, Message: "Cluster version is 0.0.1-abc"},
							{Type: internal.ClusterStatusFailing, Status: configv1.ConditionFalse},
							{Type: configv1.RetrievedUpdates, Status: configv1.ConditionFalse},
						},
					},
				}),
			},
			wantActions: func(t *testing.T, optr *Operator) {
				f := optr.client.(*fake.Clientset)
				act := f.Actions()
				if len(act) != 2 {
					t.Fatalf("unknown actions: %d %#v", len(act), act)
				}
				expectGet(t, act[0], "clusterversions", "", "default")
				expectUpdateStatus(t, act[1], "clusterversions", "", &configv1.ClusterVersion{
					ObjectMeta: metav1.ObjectMeta{
						Name:       "default",
						Generation: 2,
					},
					Spec: configv1.ClusterVersionSpec{
						ClusterID: configv1.ClusterID(id),
						Upstream:  configv1.URL("http://localhost:8080/graph"),
						Channel:   "fast",
					},
					Status: configv1.ClusterVersionStatus{
						History: []configv1.UpdateHistory{
							{State: configv1.CompletedUpdate, Version: "0.0.1-abc", Image: "image/image:v4.0.1", StartedTime: defaultStartedTime, CompletionTime: &defaultCompletionTime},
						},
						Desired:            configv1.Release{Image: "image/image:v4.0.1", Version: "0.0.1-abc"},
						ObservedGeneration: 2,
						Conditions: []configv1.ClusterOperatorStatusCondition{
							{Type: internal.ImplicitlyEnabledCapabilities, Status: "False", Reason: "AsExpected", Message: "Capabilities match configured spec"},
							{Type: configv1.OperatorAvailable, Status: configv1.ConditionTrue, Message: "Done applying 0.0.1-abc"},
							{Type: configv1.OperatorProgressing, Status: configv1.ConditionFalse, Message: "Cluster version is 0.0.1-abc"},
							{Type: internal.ClusterStatusFailing, Status: configv1.ConditionFalse},
							{Type: configv1.RetrievedUpdates, Status: configv1.ConditionFalse},
							{Type: "Upgradeable", Status: configv1.ConditionFalse, Reason: "MultipleReasons", Message: "Cluster should not be upgraded between minor or major versions for multiple reasons:\n* MessageA\n* MessageB"},
						},
					},
				})
			},
		},
		{
			name: "new upgradable conditions with some old ones, version is live and was recently synced, sync",
			syncStatus: &SyncWorkerStatus{
				Generation:  2,
				Reconciling: true,
				Completed:   1,
				Actual:      configv1.Release{Image: "image/image:v4.0.1", Version: "0.0.1-abc"},
			},
			optr: &Operator{
				release: configv1.Release{
					Image: "image/image:v4.0.1",
				},
				namespace: "test",
				name:      "default",
				upgradeable: &mockrisk.Mock{
					InternalName: "Mock",
					InternalRisks: []configv1.ConditionalUpdateRisk{
						{Name: "RiskA", Message: "MessageA", URL: "https://example.com/a", MatchingRules: []configv1.ClusterCondition{{Type: "Always"}}},
						{Name: "RiskB", Message: "MessageB", URL: "https://example.com/b", MatchingRules: []configv1.ClusterCondition{{Type: "Always"}}},
					},
				},
				client: fakeClientsetWithUpdates(&configv1.ClusterVersion{
					ObjectMeta: metav1.ObjectMeta{
						Name:       "default",
						Generation: 2,
					},
					Spec: configv1.ClusterVersionSpec{
						ClusterID: configv1.ClusterID(id),
						Upstream:  configv1.URL("http://localhost:8080/graph"),
						Channel:   "fast",
					},
					Status: configv1.ClusterVersionStatus{
						ObservedGeneration: 2,
						Conditions: []configv1.ClusterOperatorStatusCondition{
							{Type: internal.ImplicitlyEnabledCapabilities, Status: "False", Reason: "AsExpected", Message: "Capabilities match configured spec"},
							{Type: configv1.OperatorAvailable, Status: configv1.ConditionFalse},
							{Type: configv1.OperatorProgressing, Status: configv1.ConditionFalse, Message: "Cluster version is 0.0.1-abc"},
							{Type: internal.ClusterStatusFailing, Status: configv1.ConditionFalse},
							{Type: configv1.RetrievedUpdates, Status: configv1.ConditionFalse},
						},
					},
				}),
			},
			wantActions: func(t *testing.T, optr *Operator) {
				f := optr.client.(*fake.Clientset)
				act := f.Actions()
				if len(act) != 2 {
					t.Fatalf("unknown actions: %d %#v", len(act), act)
				}
				expectGet(t, act[0], "clusterversions", "", "default")
				expectUpdateStatus(t, act[1], "clusterversions", "", &configv1.ClusterVersion{
					ObjectMeta: metav1.ObjectMeta{
						Name:       "default",
						Generation: 2,
					},
					Spec: configv1.ClusterVersionSpec{
						ClusterID: configv1.ClusterID(id),
						Upstream:  configv1.URL("http://localhost:8080/graph"),
						Channel:   "fast",
					},
					Status: configv1.ClusterVersionStatus{
						History: []configv1.UpdateHistory{
							{State: configv1.CompletedUpdate, Version: "0.0.1-abc", Image: "image/image:v4.0.1", StartedTime: defaultStartedTime, CompletionTime: &defaultCompletionTime},
						},
						Desired:            configv1.Release{Image: "image/image:v4.0.1", Version: "0.0.1-abc"},
						ObservedGeneration: 2,
						Conditions: []configv1.ClusterOperatorStatusCondition{
							{Type: internal.ImplicitlyEnabledCapabilities, Status: "False", Reason: "AsExpected", Message: "Capabilities match configured spec"},
							{Type: configv1.OperatorAvailable, Status: configv1.ConditionTrue, Message: "Done applying 0.0.1-abc"},
							{Type: configv1.OperatorProgressing, Status: configv1.ConditionFalse, Message: "Cluster version is 0.0.1-abc"},
							{Type: internal.ClusterStatusFailing, Status: configv1.ConditionFalse},
							{Type: configv1.RetrievedUpdates, Status: configv1.ConditionFalse},
							{Type: "Upgradeable", Status: configv1.ConditionFalse, Reason: "MultipleReasons", Message: "Cluster should not be upgraded between minor or major versions for multiple reasons:\n* MessageA\n* MessageB"},
						},
					},
				})
			},
		},
		{
			name: "no upgradeable conditions, version is live and was recently synced, sync",
			syncStatus: &SyncWorkerStatus{
				Generation:  2,
				Reconciling: true,
				Completed:   1,
				Actual:      configv1.Release{Image: "image/image:v4.0.1", Version: "0.0.1-abc"},
			},
			optr: &Operator{
				release: configv1.Release{
					Image: "image/image:v4.0.1",
				},
				namespace: "test",
				name:      "default",
				client: fakeClientsetWithUpdates(&configv1.ClusterVersion{
					ObjectMeta: metav1.ObjectMeta{
						Name:       "default",
						Generation: 2,
					},
					Spec: configv1.ClusterVersionSpec{
						ClusterID: configv1.ClusterID(id),
						Upstream:  configv1.URL("http://localhost:8080/graph"),
						Channel:   "fast",
					},
					Status: configv1.ClusterVersionStatus{
						ObservedGeneration: 2,
						Conditions: []configv1.ClusterOperatorStatusCondition{
							{Type: internal.ImplicitlyEnabledCapabilities, Status: "False", Reason: "AsExpected", Message: "Capabilities match configured spec"},
							{Type: configv1.OperatorAvailable, Status: configv1.ConditionFalse},
							{Type: configv1.OperatorProgressing, Status: configv1.ConditionFalse, Message: "Cluster version is 0.0.1-abc"},
							{Type: internal.ClusterStatusFailing, Status: configv1.ConditionFalse},
							{Type: configv1.RetrievedUpdates, Status: configv1.ConditionFalse},
						},
					},
				}),
			},
			wantActions: func(t *testing.T, optr *Operator) {
				f := optr.client.(*fake.Clientset)
				act := f.Actions()
				if len(act) != 2 {
					t.Fatalf("unknown actions: %d %#v", len(act), act)
				}
				expectGet(t, act[0], "clusterversions", "", "default")
				expectUpdateStatus(t, act[1], "clusterversions", "", &configv1.ClusterVersion{
					ObjectMeta: metav1.ObjectMeta{
						Name:       "default",
						Generation: 2,
					},
					Spec: configv1.ClusterVersionSpec{
						ClusterID: configv1.ClusterID(id),
						Upstream:  configv1.URL("http://localhost:8080/graph"),
						Channel:   "fast",
					},
					Status: configv1.ClusterVersionStatus{
						History: []configv1.UpdateHistory{
							{State: configv1.CompletedUpdate, Version: "0.0.1-abc", Image: "image/image:v4.0.1", StartedTime: defaultStartedTime, CompletionTime: &defaultCompletionTime},
						},
						Desired:            configv1.Release{Image: "image/image:v4.0.1", Version: "0.0.1-abc"},
						ObservedGeneration: 2,
						Conditions: []configv1.ClusterOperatorStatusCondition{
							{Type: internal.ImplicitlyEnabledCapabilities, Status: "False", Reason: "AsExpected", Message: "Capabilities match configured spec"},
							{Type: configv1.OperatorAvailable, Status: configv1.ConditionTrue, Message: "Done applying 0.0.1-abc"},
							{Type: configv1.OperatorProgressing, Status: configv1.ConditionFalse, Message: "Cluster version is 0.0.1-abc"},
							{Type: internal.ClusterStatusFailing, Status: configv1.ConditionFalse},
							{Type: configv1.RetrievedUpdates, Status: configv1.ConditionFalse},
						},
					},
				})
			},
		},
		{
			name: "new available updates for the default update service URL, client has no update service",
			syncStatus: &SyncWorkerStatus{
				Generation:  2,
				Reconciling: true,
				Completed:   1,
				Actual:      configv1.Release{Image: "image/image:v4.0.1", Version: "0.0.1-abc"},
			},
			optr: &Operator{
				release: configv1.Release{
					Image: "image/image:v4.0.1",
				},
				namespace:     "test",
				name:          "default",
				updateService: "http://localhost:8080/graph",
				availableUpdates: &availableUpdates{
					UpdateService: "",
					Channel:       "fast",
					Updates: []configv1.Release{
						{Version: "4.0.2", Image: "test/image:1"},
						{Version: "4.0.3", Image: "test/image:2"},
					},
					Condition: configv1.ClusterOperatorStatusCondition{
						Type:   configv1.RetrievedUpdates,
						Status: configv1.ConditionTrue,
					},
				},
				client: fakeClientsetWithUpdates(&configv1.ClusterVersion{
					ObjectMeta: metav1.ObjectMeta{
						Name:       "default",
						Generation: 2,
					},
					Spec: configv1.ClusterVersionSpec{
						ClusterID: configv1.ClusterID(id),
						Upstream:  "",
						Channel:   "fast",
					},
					Status: configv1.ClusterVersionStatus{
						ObservedGeneration: 2,
						Conditions: []configv1.ClusterOperatorStatusCondition{
							{Type: internal.ImplicitlyEnabledCapabilities, Status: "False", Reason: "AsExpected", Message: "Capabilities match configured spec"},
							{Type: configv1.OperatorAvailable, Status: configv1.ConditionFalse},
							{Type: configv1.OperatorProgressing, Status: configv1.ConditionFalse, Message: "Cluster version is 0.0.1-abc"},
							{Type: internal.ClusterStatusFailing, Status: configv1.ConditionFalse},
							{Type: configv1.RetrievedUpdates, Status: configv1.ConditionFalse},
						},
					},
				}),
			},
			wantActions: func(t *testing.T, optr *Operator) {
				f := optr.client.(*fake.Clientset)
				act := f.Actions()
				if len(act) != 2 {
					t.Fatalf("unknown actions: %d %#v", len(act), act)
				}
				expectGet(t, act[0], "clusterversions", "", "default")
				expectUpdateStatus(t, act[1], "clusterversions", "", &configv1.ClusterVersion{
					ObjectMeta: metav1.ObjectMeta{
						Name:       "default",
						Generation: 2,
					},
					Spec: configv1.ClusterVersionSpec{
						ClusterID: configv1.ClusterID(id),
						Upstream:  "",
						Channel:   "fast",
					},
					Status: configv1.ClusterVersionStatus{
						AvailableUpdates: []configv1.Release{
							{Version: "4.0.2", Image: "test/image:1"},
							{Version: "4.0.3", Image: "test/image:2"},
						},
						History: []configv1.UpdateHistory{
							{State: configv1.CompletedUpdate, Version: "0.0.1-abc", Image: "image/image:v4.0.1", StartedTime: defaultStartedTime, CompletionTime: &defaultCompletionTime},
						},
						Desired:            configv1.Release{Image: "image/image:v4.0.1", Version: "0.0.1-abc"},
						ObservedGeneration: 2,
						Conditions: []configv1.ClusterOperatorStatusCondition{
							{Type: internal.ImplicitlyEnabledCapabilities, Status: "False", Reason: "AsExpected", Message: "Capabilities match configured spec"},
							{Type: configv1.OperatorAvailable, Status: configv1.ConditionTrue, Message: "Done applying 0.0.1-abc"},
							{Type: configv1.OperatorProgressing, Status: configv1.ConditionFalse, Message: "Cluster version is 0.0.1-abc"},
							{Type: internal.ClusterStatusFailing, Status: configv1.ConditionFalse},
							{Type: configv1.RetrievedUpdates, Status: configv1.ConditionTrue},
						},
					},
				})
			},
		},
		{
			name: "new available updates but for a different channel",
			syncStatus: &SyncWorkerStatus{
				Generation:  2,
				Reconciling: true,
				Completed:   1,
				Actual:      configv1.Release{Image: "image/image:v4.0.1", Version: "0.0.1-abc"},
			},
			optr: &Operator{
				release: configv1.Release{
					Image: "image/image:v4.0.1",
				},
				namespace: "test",
				name:      "default",
				availableUpdates: &availableUpdates{
					UpdateService: "http://localhost:8080/graph",
					Channel:       "fast",
					Updates: []configv1.Release{
						{Version: "4.0.2", Image: "test/image:1"},
						{Version: "4.0.3", Image: "test/image:2"},
					},
					Condition: configv1.ClusterOperatorStatusCondition{
						Type:   configv1.RetrievedUpdates,
						Status: configv1.ConditionTrue,
					},
				},
				client: fakeClientsetWithUpdates(&configv1.ClusterVersion{
					ObjectMeta: metav1.ObjectMeta{
						Name:       "default",
						Generation: 2,
					},
					Spec: configv1.ClusterVersionSpec{
						ClusterID: configv1.ClusterID(id),
						Upstream:  configv1.URL("http://localhost:8080/graph"),
						Channel:   "",
					},
					Status: configv1.ClusterVersionStatus{
						ObservedGeneration: 2,
						Conditions: []configv1.ClusterOperatorStatusCondition{
							{Type: internal.ImplicitlyEnabledCapabilities, Status: "False", Reason: "AsExpected", Message: "Capabilities match configured spec"},
							{Type: configv1.OperatorAvailable, Status: configv1.ConditionFalse},
							{Type: configv1.OperatorProgressing, Status: configv1.ConditionFalse, Message: "Cluster version is 0.0.1-abc"},
							{Type: internal.ClusterStatusFailing, Status: configv1.ConditionFalse},
							{Type: configv1.RetrievedUpdates, Status: configv1.ConditionFalse},
						},
					},
				}),
			},
			wantActions: func(t *testing.T, optr *Operator) {
				f := optr.client.(*fake.Clientset)
				act := f.Actions()
				if len(act) != 2 {
					t.Fatalf("unknown actions: %d %#v", len(act), act)
				}
				expectGet(t, act[0], "clusterversions", "", "default")
				expectUpdateStatus(t, act[1], "clusterversions", "", &configv1.ClusterVersion{
					ObjectMeta: metav1.ObjectMeta{
						Name:       "default",
						Generation: 2,
					},
					Spec: configv1.ClusterVersionSpec{
						ClusterID: configv1.ClusterID(id),
						Upstream:  configv1.URL("http://localhost:8080/graph"),
						Channel:   "",
					},
					Status: configv1.ClusterVersionStatus{
						History: []configv1.UpdateHistory{
							{State: configv1.CompletedUpdate, Version: "0.0.1-abc", Image: "image/image:v4.0.1", StartedTime: defaultStartedTime, CompletionTime: &defaultCompletionTime},
						},
						Desired:            configv1.Release{Image: "image/image:v4.0.1", Version: "0.0.1-abc"},
						ObservedGeneration: 2,
						Conditions: []configv1.ClusterOperatorStatusCondition{
							{Type: internal.ImplicitlyEnabledCapabilities, Status: "False", Reason: "AsExpected", Message: "Capabilities match configured spec"},
							{Type: configv1.OperatorAvailable, Status: configv1.ConditionTrue, Message: "Done applying 0.0.1-abc"},
							{Type: configv1.OperatorProgressing, Status: configv1.ConditionFalse, Message: "Cluster version is 0.0.1-abc"},
							{Type: internal.ClusterStatusFailing, Status: configv1.ConditionFalse},
							{Type: configv1.RetrievedUpdates, Status: configv1.ConditionFalse},
						},
					},
				})
			},
		},
		{
			name: "user requested a version, sync loop hasn't started",
			syncStatus: &SyncWorkerStatus{
				Generation:  2,
				Reconciling: true,
				Completed:   1,
				Actual:      configv1.Release{Image: "image/image:v4.0.1", Version: "4.0.1"},
			},
			optr: &Operator{
				release: configv1.Release{
					Image: "image/image:v4.0.1",
				},
				namespace: "test",
				name:      "default",
				client: fakeClientsetWithUpdates(&configv1.ClusterVersion{
					ObjectMeta: metav1.ObjectMeta{
						Name:       "default",
						Generation: 3,
					},
					Spec: configv1.ClusterVersionSpec{
						ClusterID: configv1.ClusterID(id),
						DesiredUpdate: &configv1.Update{
							Image: "image/image:v4.0.2",
						},
					},
				}),
			},
			wantSync: []configv1.Update{
				{Image: "image/image:v4.0.2", Version: ""},
			},
			wantActions: func(t *testing.T, optr *Operator) {
				f := optr.client.(*fake.Clientset)
				act := f.Actions()
				if len(act) != 2 {
					t.Fatalf("unknown actions: %d %#v", len(act), act)
				}
				expectGet(t, act[0], "clusterversions", "", "default")
				expectUpdateStatus(t, act[1], "clusterversions", "", &configv1.ClusterVersion{
					ObjectMeta: metav1.ObjectMeta{
						Name:       "default",
						Generation: 3,
					},
					Spec: configv1.ClusterVersionSpec{
						ClusterID: configv1.ClusterID(id),
						DesiredUpdate: &configv1.Update{
							Image: "image/image:v4.0.2",
						},
					},
					Status: configv1.ClusterVersionStatus{
						History: []configv1.UpdateHistory{
							{State: configv1.CompletedUpdate, Version: "4.0.1", Image: "image/image:v4.0.1", StartedTime: defaultStartedTime, CompletionTime: &defaultCompletionTime},
						},
						Desired:            configv1.Release{Image: "image/image:v4.0.1", Version: "4.0.1"},
						ObservedGeneration: 2,
						Conditions: []configv1.ClusterOperatorStatusCondition{
							{Type: internal.ImplicitlyEnabledCapabilities, Status: "False", Reason: "AsExpected", Message: "Capabilities match configured spec"},
							{Type: configv1.OperatorAvailable, Status: configv1.ConditionTrue, Message: "Done applying 4.0.1"},
							{Type: internal.ClusterStatusFailing, Status: configv1.ConditionFalse},
							{Type: configv1.OperatorProgressing, Status: configv1.ConditionFalse, Message: "Cluster version is 4.0.1"},
							{Type: configv1.RetrievedUpdates, Status: configv1.ConditionFalse},
						},
					},
				})
			},
		},
		{
			name: "user requested a version that isn't in the updates or history",
			syncStatus: &SyncWorkerStatus{
				Generation:  2,
				Reconciling: true,
				Completed:   1,
				Actual:      configv1.Release{Image: "image/image:v4.0.1", Version: "4.0.1"},
			},
			optr: &Operator{
				release: configv1.Release{
					Image: "image/image:v4.0.1",
				},
				namespace: "test",
				name:      "default",
				client: fakeClientsetWithUpdates(&configv1.ClusterVersion{
					ObjectMeta: metav1.ObjectMeta{
						Name:       "default",
						Generation: 3,
					},
					Spec: configv1.ClusterVersionSpec{
						ClusterID: configv1.ClusterID(id),
						Upstream:  configv1.URL("http://localhost:8080/graph"),
						DesiredUpdate: &configv1.Update{
							Version: "4.0.4",
						},
					},
					Status: configv1.ClusterVersionStatus{
						AvailableUpdates: []configv1.Release{
							{Version: "4.0.2", Image: "test/image:1"},
							{Version: "4.0.3", Image: "test/image:2"},
						},
					},
				}),
			},
			wantActions: func(t *testing.T, optr *Operator) {
				f := optr.client.(*fake.Clientset)
				act := f.Actions()
				if len(act) != 2 {
					t.Fatalf("unknown actions: %d %#v", len(act), act)
				}
				expectGet(t, act[0], "clusterversions", "", "default")
				expectUpdateStatus(t, act[1], "clusterversions", "", &configv1.ClusterVersion{
					ObjectMeta: metav1.ObjectMeta{
						Name:       "default",
						Generation: 3,
					},
					Spec: configv1.ClusterVersionSpec{
						ClusterID: configv1.ClusterID(id),
						Upstream:  configv1.URL("http://localhost:8080/graph"),
						// The object passed to status update is the one with desired update cleared
						// DesiredUpdate: &configv1.Update{
						// 	Version: "4.0.4",
						// },
					},
					Status: configv1.ClusterVersionStatus{
						History: []configv1.UpdateHistory{
							{State: configv1.CompletedUpdate, Version: "4.0.1", Image: "image/image:v4.0.1", StartedTime: defaultStartedTime, CompletionTime: &defaultCompletionTime},
						},
						Desired: configv1.Release{Image: "image/image:v4.0.1", Version: "4.0.1"},
						AvailableUpdates: []configv1.Release{
							{Version: "4.0.2", Image: "test/image:1"},
							{Version: "4.0.3", Image: "test/image:2"},
						},
						ObservedGeneration: 2,
						Conditions: []configv1.ClusterOperatorStatusCondition{
							{Type: internal.ClusterVersionInvalid, Status: configv1.ConditionTrue, Reason: "InvalidClusterVersion", Message: "The cluster version is invalid: spec.desiredUpdate.version: Invalid value: \"4.0.4\": when image is empty the update must be an available update"},
							{Type: internal.ImplicitlyEnabledCapabilities, Status: "False", Reason: "AsExpected", Message: "Capabilities match configured spec"},
							{Type: configv1.OperatorAvailable, Status: configv1.ConditionTrue, Message: "Done applying 4.0.1"},
							{Type: internal.ClusterStatusFailing, Status: configv1.ConditionFalse},
							{Type: configv1.OperatorProgressing, Status: configv1.ConditionFalse, Reason: "InvalidClusterVersion", Message: "Stopped at 4.0.1: the cluster version is invalid"},
							{Type: configv1.RetrievedUpdates, Status: configv1.ConditionFalse},
						},
					},
				})
			},
		},
		{
			name: "user requested a version has duplicates",
			syncStatus: &SyncWorkerStatus{
				Generation:  2,
				Reconciling: true,
				Completed:   1,
				Actual:      configv1.Release{Image: "image/image:v4.0.1", Version: "4.0.1"},
			},
			optr: &Operator{
				release: configv1.Release{
					Image: "image/image:v4.0.1",
				},
				namespace: "test",
				name:      "default",
				client: fakeClientsetWithUpdates(&configv1.ClusterVersion{
					ObjectMeta: metav1.ObjectMeta{
						Name:       "default",
						Generation: 2,
					},
					Spec: configv1.ClusterVersionSpec{
						ClusterID: configv1.ClusterID(id),
						Upstream:  configv1.URL("http://localhost:8080/graph"),
						DesiredUpdate: &configv1.Update{
							Version: "4.0.3",
						},
					},
					Status: configv1.ClusterVersionStatus{
						AvailableUpdates: []configv1.Release{
							{Version: "4.0.2", Image: "test/image:1"},
							{Version: "4.0.3", Image: "test/image:2"},
							{Version: "4.0.3", Image: "test/image:3"},
						},
					},
				}),
			},
			wantActions: func(t *testing.T, optr *Operator) {
				f := optr.client.(*fake.Clientset)
				act := f.Actions()
				if len(act) != 2 {
					t.Fatalf("unknown actions: %d %#v", len(act), act)
				}
				expectGet(t, act[0], "clusterversions", "", "default")
				expectUpdateStatus(t, act[1], "clusterversions", "", &configv1.ClusterVersion{
					ObjectMeta: metav1.ObjectMeta{
						Name:       "default",
						Generation: 2,
					},
					Spec: configv1.ClusterVersionSpec{
						ClusterID: configv1.ClusterID(id),
						Upstream:  configv1.URL("http://localhost:8080/graph"),
						// The object passed to status update is the one with desired update cleared
						// DesiredUpdate: &configv1.Update{
						// 	Version: "4.0.4",
						// },
					},
					Status: configv1.ClusterVersionStatus{
						ObservedGeneration: 2,
						History: []configv1.UpdateHistory{
							{State: configv1.CompletedUpdate, Version: "4.0.1", Image: "image/image:v4.0.1", StartedTime: defaultStartedTime, CompletionTime: &defaultCompletionTime},
						},
						Desired: configv1.Release{Image: "image/image:v4.0.1", Version: "4.0.1"},
						AvailableUpdates: []configv1.Release{
							{Version: "4.0.2", Image: "test/image:1"},
							{Version: "4.0.3", Image: "test/image:2"},
							{Version: "4.0.3", Image: "test/image:3"},
						},
						Conditions: []configv1.ClusterOperatorStatusCondition{
							{Type: internal.ClusterVersionInvalid, Status: configv1.ConditionTrue, Reason: "InvalidClusterVersion", Message: "The cluster version is invalid: spec.desiredUpdate.version: Invalid value: \"4.0.3\": there are multiple possible payloads for this version, specify the exact image"},
							{Type: internal.ImplicitlyEnabledCapabilities, Status: "False", Reason: "AsExpected", Message: "Capabilities match configured spec"},
							{Type: configv1.OperatorAvailable, Status: configv1.ConditionTrue, Message: "Done applying 4.0.1"},
							{Type: internal.ClusterStatusFailing, Status: configv1.ConditionFalse},
							{Type: configv1.OperatorProgressing, Status: configv1.ConditionFalse, Reason: "InvalidClusterVersion", Message: "Stopped at 4.0.1: the cluster version is invalid"},
							{Type: configv1.RetrievedUpdates, Status: configv1.ConditionFalse},
						},
					},
				})
			},
		},
		{
			name: "image hash matches content hash, act as reconcile, no need to apply",
			syncStatus: &SyncWorkerStatus{
				Generation:  2,
				Reconciling: true,
				Completed:   1,
				VersionHash: "y_Kc5IQiIyU=",
				Actual:      configv1.Release{Image: "image/image:v4.0.1", Version: "0.0.1-abc"},
			},
			optr: &Operator{
				release: configv1.Release{
					Version: "0.0.1-abc",
					Image:   "image/image:v4.0.1",
				},
				namespace: "test",
				name:      "default",
				client: fakeClientsetWithUpdates(
					&configv1.ClusterVersion{
						ObjectMeta: metav1.ObjectMeta{
							Name:       "default",
							Generation: 2,
						},
						Spec: configv1.ClusterVersionSpec{
							ClusterID: configv1.ClusterID(id),
							Upstream:  configv1.URL("http://localhost:8080/graph"),
							Channel:   "fast",
						},
						Status: configv1.ClusterVersionStatus{
							History: []configv1.UpdateHistory{
								// loads the version from the image on disk
								{
									State:          configv1.CompletedUpdate,
									Image:          "image/image:v4.0.1",
									Version:        "0.0.1-abc",
									CompletionTime: &defaultCompletionTime,
								},
							},
							Desired: configv1.Release{
								Version: "0.0.1-abc",
								Image:   "image/image:v4.0.1",
							},
							VersionHash:        "y_Kc5IQiIyU=",
							ObservedGeneration: 2,
							Conditions: []configv1.ClusterOperatorStatusCondition{
								{Type: internal.ImplicitlyEnabledCapabilities, Status: "False", Reason: "AsExpected", Message: "Capabilities match configured spec"},
								{Type: configv1.OperatorAvailable, Status: configv1.ConditionTrue, Message: "Done applying 0.0.1-abc"},
								{Type: internal.ClusterStatusFailing, Status: configv1.ConditionFalse},
								{Type: configv1.OperatorProgressing, Status: configv1.ConditionFalse, Message: "Cluster version is 0.0.1-abc"},
								{Type: configv1.RetrievedUpdates, Status: configv1.ConditionFalse},
							},
						},
					},
				),
			},
			wantActions: func(t *testing.T, optr *Operator) {
				f := optr.client.(*fake.Clientset)
				act := f.Actions()
				if len(act) != 1 {
					t.Fatalf("unknown actions: %d %s", len(act), spew.Sdump(act))
				}
				expectGet(t, act[0], "clusterversions", "", "default")
			},
		},
		{
			name: "image hash does not match content hash, act as reconcile, no need to apply",
			syncStatus: &SyncWorkerStatus{
				Generation:  2,
				Reconciling: true,
				Completed:   1,
				VersionHash: "y_Kc5IQiIyU=",
				Actual:      configv1.Release{Image: "image/image:v4.0.1", Version: "0.0.1-abc"},
			},
			optr: &Operator{
				release: configv1.Release{
					Version: "0.0.1-abc",
					Image:   "image/image:v4.0.1",
				},
				namespace: "test",
				name:      "default",
				client: fake.NewClientset(
					&configv1.ClusterVersion{
						ObjectMeta: metav1.ObjectMeta{
							Name:       "default",
							Generation: 2,
						},
						Spec: configv1.ClusterVersionSpec{
							ClusterID: configv1.ClusterID(id),
							Upstream:  configv1.URL("http://localhost:8080/graph"),
							Channel:   "fast",
						},
						Status: configv1.ClusterVersionStatus{
							History: []configv1.UpdateHistory{
								// loads the version from the image on disk
								{
									State:          configv1.CompletedUpdate,
									Image:          "image/image:v4.0.1",
									Version:        "0.0.1-abc",
									CompletionTime: &defaultCompletionTime,
								},
							},
							Desired: configv1.Release{
								Version: "0.0.1-abc",
								Image:   "image/image:v4.0.1",
							},
							VersionHash:        "unknown_hash",
							ObservedGeneration: 2,
							Conditions: []configv1.ClusterOperatorStatusCondition{
								{Type: internal.ImplicitlyEnabledCapabilities, Status: "False", Reason: "AsExpected", Message: "Capabilities match configured spec"},
								{Type: configv1.OperatorAvailable, Status: configv1.ConditionTrue, Message: "Done applying 0.0.1-abc"},
								{Type: internal.ClusterStatusFailing, Status: configv1.ConditionFalse},
								{Type: configv1.OperatorProgressing, Status: configv1.ConditionFalse, Message: "Cluster version is 0.0.1-abc"},
								{Type: configv1.RetrievedUpdates, Status: configv1.ConditionFalse},
							},
						},
					},
				),
			},
			wantActions: func(t *testing.T, optr *Operator) {
				f := optr.client.(*fake.Clientset)
				act := f.Actions()
				if len(act) != 2 {
					t.Fatalf("unknown actions: %d %s", len(act), spew.Sdump(act))
				}
				expectGet(t, act[0], "clusterversions", "", "default")
				expectUpdateStatus(t, act[1], "clusterversions", "", &configv1.ClusterVersion{
					ObjectMeta: metav1.ObjectMeta{
						Name:       "default",
						Generation: 2,
					},
					Spec: configv1.ClusterVersionSpec{
						Upstream: configv1.URL("http://localhost:8080/graph"),
						Channel:  "fast",
					},
					Status: configv1.ClusterVersionStatus{
						History: []configv1.UpdateHistory{
							{
								State:          configv1.CompletedUpdate,
								Image:          "image/image:v4.0.1",
								Version:        "0.0.1-abc",
								CompletionTime: &defaultCompletionTime,
								StartedTime:    metav1.Time{Time: time.Unix(0, 0)},
							},
						},
						Desired:            configv1.Release{Version: "0.0.1-abc", Image: "image/image:v4.0.1"},
						ObservedGeneration: 2,
						VersionHash:        "y_Kc5IQiIyU=",
						Conditions: []configv1.ClusterOperatorStatusCondition{
							{Type: internal.ImplicitlyEnabledCapabilities, Status: "False", Reason: "AsExpected", Message: "Capabilities match configured spec"},
							{Type: configv1.OperatorAvailable, Status: configv1.ConditionTrue, Message: "Done applying 0.0.1-abc"},
							{Type: internal.ClusterStatusFailing, Status: configv1.ConditionFalse},
							{Type: configv1.OperatorProgressing, Status: configv1.ConditionFalse, Message: "Cluster version is 0.0.1-abc"},
							{Type: configv1.RetrievedUpdates, Status: configv1.ConditionFalse},
						},
					},
				})
			},
		},

		{
			name: "detect invalid cluster version",
			syncStatus: &SyncWorkerStatus{
				Reconciling: true,
				Completed:   1,
				Actual:      configv1.Release{Image: "image/image:v4.0.1", Version: "0.0.1-abc"},
			},
			optr: &Operator{
				release: configv1.Release{
					Image: "image/image:v4.0.1",
				},
				namespace: "test",
				name:      "default",
				client: fakeClientsetWithUpdates(&configv1.ClusterVersion{
					ObjectMeta: metav1.ObjectMeta{
						Name:            "default",
						ResourceVersion: "1",
					},
					Spec: configv1.ClusterVersionSpec{
						ClusterID: "not-valid-cluster-id",
						Upstream:  configv1.URL("#%GG"),
						Channel:   "fast",
					},
				}),
			},
			wantActions: func(t *testing.T, optr *Operator) {
				f := optr.client.(*fake.Clientset)
				act := f.Actions()
				if len(act) != 2 {
					t.Fatalf("unknown actions: %d %#v", len(act), act)
				}
				expectGet(t, act[0], "clusterversions", "", "default")
				expectUpdateStatus(t, act[1], "clusterversions", "", &configv1.ClusterVersion{
					ObjectMeta: metav1.ObjectMeta{
						Name:            "default",
						ResourceVersion: "1",
					},
					Spec: configv1.ClusterVersionSpec{
						// The object passed to status has these spec fields cleared
						// ClusterID: "not-valid-cluster-id",
						// Upstream:  configv1.URL("#%GG"),
						Channel: "fast",
					},
					Status: configv1.ClusterVersionStatus{
						History: []configv1.UpdateHistory{
							{State: configv1.CompletedUpdate, Version: "0.0.1-abc", Image: "image/image:v4.0.1", StartedTime: defaultStartedTime, CompletionTime: &defaultCompletionTime},
						},
						Desired: configv1.Release{
							Version: "0.0.1-abc", Image: "image/image:v4.0.1",
						},
						VersionHash: "",
						Conditions: []configv1.ClusterOperatorStatusCondition{
							{Type: internal.ClusterVersionInvalid, Status: configv1.ConditionTrue, Reason: "InvalidClusterVersion", Message: "The cluster version is invalid:\n* spec.upstream: Invalid value: \"#%GG\": must be a valid URL or empty\n* spec.clusterID: Invalid value: \"not-valid-cluster-id\": must be an RFC4122-variant UUID\n"},
							{Type: internal.ImplicitlyEnabledCapabilities, Status: "False", Reason: "AsExpected", Message: "Capabilities match configured spec"},
							{Type: configv1.OperatorAvailable, Status: configv1.ConditionTrue, Message: "Done applying 0.0.1-abc"},
							{Type: internal.ClusterStatusFailing, Status: configv1.ConditionFalse},
							{Type: configv1.OperatorProgressing, Status: configv1.ConditionFalse, Reason: "InvalidClusterVersion", Message: "Stopped at 0.0.1-abc: the cluster version is invalid"},
							{Type: configv1.RetrievedUpdates, Status: configv1.ConditionFalse},
						},
					},
				})
			},
		},

		{
			name: "invalid cluster version should not block initial sync",
			syncStatus: &SyncWorkerStatus{
				Actual: configv1.Release{Image: "image/image:v4.0.1", Version: "0.0.1-abc"},
			},
			optr: &Operator{
				release: configv1.Release{
					Image: "image/image:v4.0.1",
				},
				namespace: "test",
				name:      "default",
				client: fakeClientsetWithUpdates(&configv1.ClusterVersion{
					ObjectMeta: metav1.ObjectMeta{
						Name:            "default",
						ResourceVersion: "1",
					},
					Spec: configv1.ClusterVersionSpec{
						ClusterID: "not-valid-cluster-id",
						Upstream:  configv1.URL("#%GG"),
						Channel:   "fast",
					},
					Status: configv1.ClusterVersionStatus{
						History: []configv1.UpdateHistory{
							{Image: "image/image:v4.0.1", StartedTime: defaultStartedTime},
						},
						Desired:     configv1.Release{Image: "image/image:v4.0.1"},
						VersionHash: "",
						Conditions: []configv1.ClusterOperatorStatusCondition{
							{Type: internal.ImplicitlyEnabledCapabilities, Status: "False", Reason: "AsExpected", Message: "Capabilities match configured spec"},
							{Type: configv1.OperatorAvailable, Status: configv1.ConditionFalse},
							{Type: internal.ClusterStatusFailing, Status: configv1.ConditionFalse},
							{Type: configv1.OperatorProgressing, Status: configv1.ConditionFalse, Reason: "InvalidClusterVersion", Message: "Stopped at image/image:v4.0.1: the cluster version is invalid"},
							{Type: configv1.RetrievedUpdates, Status: configv1.ConditionFalse},
							{Type: internal.ClusterVersionInvalid, Status: configv1.ConditionTrue, Reason: "InvalidClusterVersion", Message: "The cluster version is invalid:\n* spec.upstream: Invalid value: \"#%GG\": must be a valid URL or empty\n* spec.clusterID: Invalid value: \"not-valid-cluster-id\": must be an RFC4122-variant UUID\n"},
						},
					},
				}),
			},
			wantSync: []configv1.Update{
				// set by the operator
				{Image: "image/image:v4.0.1", Version: ""},
			},
			wantActions: func(t *testing.T, optr *Operator) {
				f := optr.client.(*fake.Clientset)
				act := f.Actions()
				if len(act) != 2 {
					t.Fatalf("unknown actions: %d %#v", len(act), act)
				}
				expectGet(t, act[0], "clusterversions", "", "default")
				expectUpdateStatus(t, act[1], "clusterversions", "", &configv1.ClusterVersion{
					ObjectMeta: metav1.ObjectMeta{
						Name:            "default",
						ResourceVersion: "1",
					},
					Spec: configv1.ClusterVersionSpec{
						// fields are cleared when passed to the client (although server will ignore spec changes)
						ClusterID: "",
						Upstream:  configv1.URL(""),

						Channel: "fast",
					},
					Status: configv1.ClusterVersionStatus{
						History: []configv1.UpdateHistory{
							{State: configv1.PartialUpdate, Version: "0.0.1-abc", Image: "image/image:v4.0.1", StartedTime: defaultStartedTime},
						},
						Desired:     configv1.Release{Version: "0.0.1-abc", Image: "image/image:v4.0.1"},
						VersionHash: "",
						Conditions: []configv1.ClusterOperatorStatusCondition{
							{Type: internal.ImplicitlyEnabledCapabilities, Status: "False", Reason: "AsExpected", Message: "Capabilities match configured spec"},
							{Type: configv1.OperatorAvailable, Status: configv1.ConditionFalse},
							{Type: internal.ClusterStatusFailing, Status: configv1.ConditionFalse},
							{Type: configv1.OperatorProgressing, Status: configv1.ConditionTrue, Reason: "InvalidClusterVersion", Message: "Reconciling 0.0.1-abc: the cluster version is invalid"},
							{Type: configv1.RetrievedUpdates, Status: configv1.ConditionFalse},
							{Type: internal.ClusterVersionInvalid, Status: configv1.ConditionTrue, Reason: "InvalidClusterVersion", Message: "The cluster version is invalid:\n* spec.upstream: Invalid value: \"#%GG\": must be a valid URL or empty\n* spec.clusterID: Invalid value: \"not-valid-cluster-id\": must be an RFC4122-variant UUID\n"},
						},
					},
				})
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			optr := tt.optr
			if tt.init != nil {
				tt.init(optr)
			}
			optr.proxyLister = &clientProxyLister{client: optr.client}
			optr.cvLister = &clientCVLister{client: optr.client}
			optr.coLister = &clientCOLister{client: optr.client}
			if optr.configSync == nil {
				expectStatus := tt.syncStatus
				if expectStatus == nil {
					expectStatus = &SyncWorkerStatus{}
				}
				optr.configSync = &fakeSyncRecorder{Returns: expectStatus}
			}
			optr.eventRecorder = record.NewFakeRecorder(100)
			optr.enabledCVOFeatureGates = featuregates.DefaultCvoGates("version")

			registry := clusterconditions.NewConditionRegistry()
			registry.Register("Always", &always.Always{})
			optr.conditionRegistry = registry

			ctx := context.Background()
			err := optr.sync(ctx, optr.queueKey())
			if err != nil && tt.wantErr == nil {
				t.Fatalf("Operator.sync() unexpected error: %v", err)
			}
			if tt.wantErr != nil {
				tt.wantErr(t, err)
			}
			if err != nil {
				return
			}
			if tt.wantActions != nil {
				tt.wantActions(t, optr)
			}
			if tt.wantSync != nil {
				actual := optr.configSync.(*fakeSyncRecorder).Updates
				if !reflect.DeepEqual(tt.wantSync, actual) {
					t.Fatalf("Unexpected updates: %#v", actual)
				}
			}
		})
	}
}

func TestOperator_availableUpdatesSync(t *testing.T) {
	id := uuid.Must(uuid.NewRandom()).String()
	tests := []struct {
		name        string
		key         string
		handler     http.HandlerFunc
		optr        *Operator
		wantErr     func(*testing.T, error)
		wantUpdates *availableUpdates
	}{
		{
			name: "when version is missing, do nothing (other loops should create it)",
			optr: &Operator{
				release: configv1.Release{
					Version: "4.0.1",
					Image:   "image/image:v4.0.1",
				},
				namespace: "test",
				name:      "default",
				client:    fake.NewClientset(),
			},
		},
		{
			name: "no operator or ClusterVersion upstream uses the default update service",
			handler: func(w http.ResponseWriter, req *http.Request) {
				http.Error(w, "bad things", http.StatusInternalServerError)
			},
			optr: &Operator{
				architecture: "amd64",
				release: configv1.Release{
					Version: "4.0.1",
					Image:   "image/image:v4.0.1",
				},
				namespace: "test",
				name:      "default",
				client: fake.NewClientset(
					&configv1.ClusterVersion{
						ObjectMeta: metav1.ObjectMeta{
							Name: "default",
						},
						Spec: configv1.ClusterVersionSpec{
							ClusterID: configv1.ClusterID(id),
							Channel:   "fast",
						},
						Status: configv1.ClusterVersionStatus{
							History: []configv1.UpdateHistory{
								{Image: "image/image:v4.0.1"},
							},
						},
					},
				),
			},
			wantUpdates: &availableUpdates{
				UpdateService: "",
				Channel:       "fast",
				Architecture:  runtime.GOARCH,
				Condition: configv1.ClusterOperatorStatusCondition{
					Type:    configv1.RetrievedUpdates,
					Status:  configv1.ConditionFalse,
					Reason:  "VersionNotFound",
					Message: `Unable to retrieve available updates: currently reconciling cluster version 4.0.1 not found in the "fast" channel`,
				},
			},
		},
		{
			name: "report an error condition when channel isn't set",
			handler: func(w http.ResponseWriter, req *http.Request) {
				http.Error(w, "bad things", http.StatusInternalServerError)
			},
			optr: &Operator{
				updateService: "http://localhost:8080/graph",
				architecture:  "amd64",
				release: configv1.Release{
					Version: "v4.0.0",
					Image:   "image/image:v4.0.1",
				},
				namespace: "test",
				name:      "default",
				client: fake.NewClientset(
					&configv1.ClusterVersion{
						ObjectMeta: metav1.ObjectMeta{
							Name: "default",
						},
						Spec: configv1.ClusterVersionSpec{
							ClusterID: configv1.ClusterID(id),
							Channel:   "",
						},
						Status: configv1.ClusterVersionStatus{
							History: []configv1.UpdateHistory{
								{Image: "image/image:v4.0.1"},
							},
						},
					},
				),
			},
			wantUpdates: &availableUpdates{
				UpdateService: "http://localhost:8080/graph",
				Channel:       "",
				Architecture:  runtime.GOARCH,
				Condition: configv1.ClusterOperatorStatusCondition{
					Type:    configv1.RetrievedUpdates,
					Status:  configv1.ConditionFalse,
					Reason:  noChannel,
					Message: "The update channel has not been configured.",
				},
			},
		},
		{
			name: "report an error condition when no current version is set",
			handler: func(w http.ResponseWriter, req *http.Request) {
				http.Error(w, "bad things", http.StatusInternalServerError)
			},
			optr: &Operator{
				updateService: "http://localhost:8080/graph",
				architecture:  "amd64",
				release: configv1.Release{
					Image: "image/image:v4.0.1",
				},
				namespace: "test",
				name:      "default",
				client: fake.NewClientset(
					&configv1.ClusterVersion{
						ObjectMeta: metav1.ObjectMeta{
							Name: "default",
						},
						Spec: configv1.ClusterVersionSpec{
							ClusterID: configv1.ClusterID(id),
							Channel:   "fast",
						},
						Status: configv1.ClusterVersionStatus{
							History: []configv1.UpdateHistory{
								{Image: "image/image:v4.0.1"},
							},
						},
					},
				),
			},
			wantUpdates: &availableUpdates{
				UpdateService: "http://localhost:8080/graph",
				Channel:       "fast",
				Architecture:  runtime.GOARCH,
				Condition: configv1.ClusterOperatorStatusCondition{
					Type:    configv1.RetrievedUpdates,
					Status:  configv1.ConditionFalse,
					Reason:  "NoCurrentVersion",
					Message: "The cluster version does not have a semantic version assigned and cannot calculate valid upgrades.",
				},
			},
		},
		{
			name: "report an error condition when the http server reports an error",
			handler: func(w http.ResponseWriter, req *http.Request) {
				http.Error(w, "bad things", http.StatusInternalServerError)
			},
			optr: &Operator{
				updateService: "http://localhost:8080/graph",
				architecture:  "amd64",
				release: configv1.Release{
					Version: "4.0.1",
					Image:   "image/image:v4.0.1",
				},
				namespace: "test",
				name:      "default",
				client: fake.NewClientset(
					&configv1.ClusterVersion{
						ObjectMeta: metav1.ObjectMeta{
							Name: "default",
						},
						Spec: configv1.ClusterVersionSpec{
							ClusterID: configv1.ClusterID(id),
							Channel:   "fast",
						},
						Status: configv1.ClusterVersionStatus{
							History: []configv1.UpdateHistory{
								{Image: "image/image:v4.0.1"},
							},
							Conditions: []configv1.ClusterOperatorStatusCondition{
								{Type: internal.ImplicitlyEnabledCapabilities, Status: "False", Reason: "AsExpected", Message: "Capabilities match configured spec"},
								{Type: configv1.OperatorAvailable, Status: configv1.ConditionTrue, Message: "Done applying image/image:v4.0.1"},
								{Type: internal.ClusterStatusFailing, Status: configv1.ConditionFalse},
								{Type: configv1.OperatorProgressing, Status: configv1.ConditionFalse},
							},
						},
					},
				),
			},
			wantUpdates: &availableUpdates{
				UpdateService: "http://localhost:8080/graph",
				Channel:       "fast",
				Architecture:  runtime.GOARCH,
				Condition: configv1.ClusterOperatorStatusCondition{
					Type:    configv1.RetrievedUpdates,
					Status:  configv1.ConditionFalse,
					Reason:  "ResponseFailed",
					Message: "Unable to retrieve available updates: unexpected HTTP status: 500 Internal Server Error",
				},
			},
		},
		{
			name: "set available updates and clear error state when success and empty",
			handler: func(w http.ResponseWriter, req *http.Request) {
				_, _ = fmt.Fprintf(w, `
				{
					"nodes": [
						{
							"version":"4.0.1",
							"payload": "image/image:v4.0.1",
							"metadata": {
								"url": "https://example.com/v4.0.1",
								"io.openshift.upgrades.graph.release.channels": "channel-c,channel-a,channel-b"
							}
						}
					],
					"edges": []
				}
				`)
			},
			optr: &Operator{
				updateService: "http://localhost:8080/graph",
				architecture:  "amd64",
				release: configv1.Release{
					Version: "4.0.1",
					Image:   "image/image:v4.0.1",
				},
				namespace: "test",
				name:      "default",
				client: fake.NewClientset(
					&configv1.ClusterVersion{
						ObjectMeta: metav1.ObjectMeta{
							Name: "default",
						},
						Spec: configv1.ClusterVersionSpec{
							ClusterID: configv1.ClusterID(id),
							Channel:   "fast",
						},
						Status: configv1.ClusterVersionStatus{
							History: []configv1.UpdateHistory{
								{Image: "image/image:v4.0.1"},
							},
							Conditions: []configv1.ClusterOperatorStatusCondition{
								{Type: internal.ImplicitlyEnabledCapabilities, Status: "False", Reason: "AsExpected", Message: "Capabilities match configured spec"},
								{Type: configv1.OperatorAvailable, Status: configv1.ConditionTrue, Message: "Done applying image/image:v4.0.1"},
								{Type: internal.ClusterStatusFailing, Status: configv1.ConditionFalse},
								{Type: configv1.OperatorProgressing, Status: configv1.ConditionFalse},
								{Type: configv1.RetrievedUpdates, Status: configv1.ConditionFalse, Reason: "RemoteFailed", Message: "Unable to retrieve available updates: unexpected HTTP status: 500 Internal Server Error"},
							},
						},
					},
				),
			},
			wantUpdates: &availableUpdates{
				UpdateService: "http://localhost:8080/graph",
				Channel:       "fast",
				Architecture:  runtime.GOARCH,
				Current: configv1.Release{
					Version:  "4.0.1",
					Image:    "image/image:v4.0.1",
					URL:      "https://example.com/v4.0.1",
					Channels: []string{"channel-a", "channel-b", "channel-c"},
				},
				Condition: configv1.ClusterOperatorStatusCondition{
					Type:   configv1.RetrievedUpdates,
					Status: configv1.ConditionTrue,
				},
			},
		},
		{
			name: "calculate available update edges",
			handler: func(w http.ResponseWriter, req *http.Request) {
				_, _ = fmt.Fprintf(w, `
				{
					"nodes": [
						{"version":"4.0.1",            "payload": "image/image:v4.0.1"},
						{"version":"4.0.2-prerelease", "payload": "some.other.registry/image/image:v4.0.2"},
						{"version":"4.0.2",            "payload": "image/image:v4.0.2"}
					],
					"edges": [
						[0, 1],
						[0, 2],
						[1, 2]
					]
				}
				`)
			},
			optr: &Operator{
				updateService: "http://localhost:8080/graph",
				architecture:  "amd64",
				release: configv1.Release{
					Version: "4.0.1",
					Image:   "image/image:v4.0.1",
				},
				namespace: "test",
				name:      "default",
				client: fake.NewClientset(
					&configv1.ClusterVersion{
						ObjectMeta: metav1.ObjectMeta{
							Name: "default",
						},
						Spec: configv1.ClusterVersionSpec{
							ClusterID: configv1.ClusterID(id),
							Channel:   "fast",
						},
						Status: configv1.ClusterVersionStatus{
							History: []configv1.UpdateHistory{
								{Image: "image/image:v4.0.1"},
							},
							Conditions: []configv1.ClusterOperatorStatusCondition{
								{Type: internal.ImplicitlyEnabledCapabilities, Status: "False", Reason: "AsExpected", Message: "Capabilities match configured spec"},
								{Type: configv1.OperatorAvailable, Status: configv1.ConditionTrue, Message: "Done applying image/image:v4.0.1"},
								{Type: internal.ClusterStatusFailing, Status: configv1.ConditionFalse},
								{Type: configv1.OperatorProgressing, Status: configv1.ConditionFalse},
								{Type: configv1.RetrievedUpdates, Status: configv1.ConditionFalse, Reason: "RemoteFailed", Message: "Unable to retrieve available updates: unexpected HTTP status: 500 Internal Server Error"},
							},
						},
					},
				),
			},
			wantUpdates: &availableUpdates{
				UpdateService: "http://localhost:8080/graph",
				Channel:       "fast",
				Architecture:  runtime.GOARCH,
				Current:       configv1.Release{Version: "4.0.1", Image: "image/image:v4.0.1"},
				Updates: []configv1.Release{
					{Version: "4.0.2", Image: "image/image:v4.0.2"},
					{Version: "4.0.2-prerelease", Image: "some.other.registry/image/image:v4.0.2"},
				},
				Condition: configv1.ClusterOperatorStatusCondition{
					Type:   configv1.RetrievedUpdates,
					Status: configv1.ConditionTrue,
				},
			},
		},
		{
			name: "if last successful check time was too recent, do nothing",
			handler: func(w http.ResponseWriter, req *http.Request) {
				http.Error(w, "bad things", http.StatusInternalServerError)
			},
			optr: &Operator{
				updateService:              "http://localhost:8080/graph",
				minimumUpdateCheckInterval: 1 * time.Minute,
				availableUpdates: &availableUpdates{
					UpdateService:          "http://localhost:8080/graph",
					Channel:                "fast",
					Architecture:           runtime.GOARCH,
					LastAttempt:            time.Now(),
					LastSyncOrConfigChange: time.Now(),
				},
				release: configv1.Release{
					Version: "4.0.1",
					Image:   "image/image:v4.0.1",
				},
				namespace: "test",
				name:      "default",
				client: fake.NewClientset(
					&configv1.ClusterVersion{
						ObjectMeta: metav1.ObjectMeta{
							Name:       "default",
							Generation: 2,
						},
						Spec: configv1.ClusterVersionSpec{
							ClusterID: configv1.ClusterID(id),
							Channel:   "fast",
						},
						Status: configv1.ClusterVersionStatus{
							History: []configv1.UpdateHistory{
								{Image: "image/image:v4.0.1"},
							},
							ObservedGeneration: 2,
							Conditions: []configv1.ClusterOperatorStatusCondition{
								{Type: internal.ImplicitlyEnabledCapabilities, Status: "False", Reason: "AsExpected", Message: "Capabilities match configured spec"},
								{Type: configv1.OperatorAvailable, Status: configv1.ConditionTrue, Message: "Done applying image/image:v4.0.1"},
								{Type: internal.ClusterStatusFailing, Status: configv1.ConditionFalse},
								{Type: configv1.OperatorProgressing, Status: configv1.ConditionFalse},
								{Type: configv1.RetrievedUpdates, Status: configv1.ConditionFalse, Reason: "RemoteFailed", Message: "Unable to retrieve available updates: unexpected HTTP status: 500 Internal Server Error"},
							},
						},
					},
				),
			},
		},
		{
			name: "operator update service takes precedence over ClusterVersion upstream",
			handler: func(w http.ResponseWriter, req *http.Request) {
				http.Error(w, "bad things", http.StatusInternalServerError)
			},
			optr: &Operator{
				updateService: "http://localhost:8080/graph",
				architecture:  "amd64",
				release: configv1.Release{
					Version: "4.0.1",
					Image:   "image/image:v4.0.1",
				},
				namespace: "test",
				name:      "default",
				client: fake.NewClientset(
					&configv1.ClusterVersion{
						ObjectMeta: metav1.ObjectMeta{
							Name: "default",
						},
						Spec: configv1.ClusterVersionSpec{
							ClusterID: configv1.ClusterID(id),
							Upstream:  configv1.URL("http://localhost:8080/does-not-exist"),
							Channel:   "fast",
						},
						Status: configv1.ClusterVersionStatus{
							History: []configv1.UpdateHistory{
								{Image: "image/image:v4.0.1"},
							},
							Conditions: []configv1.ClusterOperatorStatusCondition{
								{Type: internal.ImplicitlyEnabledCapabilities, Status: "False", Reason: "AsExpected", Message: "Capabilities match configured spec"},
								{Type: configv1.OperatorAvailable, Status: configv1.ConditionTrue, Message: "Done applying image/image:v4.0.1"},
								{Type: internal.ClusterStatusFailing, Status: configv1.ConditionFalse},
								{Type: configv1.OperatorProgressing, Status: configv1.ConditionFalse},
							},
						},
					},
				),
			},
			wantUpdates: &availableUpdates{
				UpdateService: "http://localhost:8080/graph",
				Channel:       "fast",
				Architecture:  runtime.GOARCH,
				Condition: configv1.ClusterOperatorStatusCondition{
					Type:    configv1.RetrievedUpdates,
					Status:  configv1.ConditionFalse,
					Reason:  "ResponseFailed",
					Message: "Unable to retrieve available updates: unexpected HTTP status: 500 Internal Server Error",
				},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			optr := tt.optr
			optr.queue = workqueue.NewTypedRateLimitingQueue[any](workqueue.DefaultTypedControllerRateLimiter[any]())
			optr.proxyLister = &clientProxyLister{client: optr.client}
			optr.coLister = &clientCOLister{client: optr.client}
			optr.cvLister = &clientCVLister{client: optr.client}
			optr.cmConfigManagedLister = &cmConfigLister{}
			optr.eventRecorder = record.NewFakeRecorder(100)
			optr.enabledCVOFeatureGates = featuregates.DefaultCvoGates("version")

			var updateServiceURI string
			if tt.handler != nil {
				s := httptest.NewServer(http.HandlerFunc(tt.handler))
				defer s.Close()
				updateServiceURI = s.URL
				if optr.updateService == "http://localhost:8080/graph" {
					optr.updateService = updateServiceURI
				}
				if optr.availableUpdates != nil && optr.availableUpdates.UpdateService == "http://localhost:8080/graph" {
					optr.availableUpdates.UpdateService = updateServiceURI
				}
			}
			old := optr.availableUpdates

			ctx := context.Background()
			err := optr.availableUpdatesSync(ctx, optr.queueKey())
			if err != nil && tt.wantErr == nil {
				t.Fatalf("Operator.sync() unexpected error: %v", err)
			}
			if tt.wantErr != nil {
				tt.wantErr(t, err)
			}
			if err != nil {
				return
			}

			if optr.availableUpdates == old {
				optr.availableUpdates = nil
			}

			if optr.availableUpdates != nil {
				optr.availableUpdates.Condition.LastTransitionTime = metav1.Time{}
				optr.availableUpdates.LastAttempt = time.Time{}
				optr.availableUpdates.LastSyncOrConfigChange = time.Time{}
				if updateServiceURI != "" && optr.availableUpdates.UpdateService == updateServiceURI {
					optr.availableUpdates.UpdateService = "http://localhost:8080/graph"
				}
			}

			if diff := cmp.Diff(tt.wantUpdates, optr.availableUpdates, availableUpdatesCmpOpts...); diff != "" {
				t.Fatalf("unexpected: %s", diff)
			}
			if (optr.queue.Len() > 0) != (optr.availableUpdates != nil) {
				t.Fatalf("unexpected queue")
			}
		})
	}
}

func expectGet(t *testing.T, a ktesting.Action, resource, namespace, name string) {
	t.Helper()
	if a.GetVerb() != "get" {
		t.Fatalf("unexpected verb: %s", a.GetVerb())
	}
	switch at := a.(type) {
	case ktesting.GetAction:
		e, a := fmt.Sprintf("%s/%s/%s", resource, namespace, name), fmt.Sprintf("%s/%s/%s", at.GetResource().Resource, at.GetNamespace(), at.GetName())
		if e != a {
			t.Fatalf("unexpected action: %#v", at)
		}
	default:
		t.Fatalf("unknown verb %T", a)
	}
}

// expectFinalUpdateStatus is used if you only care about, and therefore only want to verify, the final CreateAction.
func expectFinalUpdateStatus(t *testing.T, actions []ktesting.Action, resource, namespace string, obj interface{}) {
	t.Helper()
	updateFound := false
	var updateAction ktesting.Action
	for _, a := range actions {
		switch a.(type) {
		case ktesting.CreateAction:
			updateAction = a
			updateFound = true
		}
	}
	if !updateFound {
		t.Fatal("Expected action not found.")
	} else {
		expectMutation(t, updateAction, "update", resource, "status", namespace, obj)
	}
}

func expectUpdateStatus(t *testing.T, a ktesting.Action, resource, namespace string, obj interface{}) {
	t.Helper()
	expectMutation(t, a, "update", resource, "status", namespace, obj)
}

// checkStatus is a generic function used to verify only a portion of a CreateAction as defined by the
// generic type arguments.
func checkStatus[S configv1.ClusterVersionCapabilitiesStatus | []configv1.ClusterOperatorStatusCondition | []configv1.UpdateHistory |
	*configv1.ClusterVersion](t *testing.T, a ktesting.Action, verb string, resource, subresource, namespace string, expect S) {

	t.Helper()
	if verb != a.GetVerb() {
		t.Fatalf("unexpected verb: %s", a.GetVerb())
	}
	if subresource != a.GetSubresource() {
		t.Fatalf("unexpected subresource: %s", a.GetSubresource())
	}
	switch at := a.(type) {
	case ktesting.CreateAction:
		actual := at.GetObject()
		if in, ok := actual.(*configv1.ClusterOperator); ok {
			for i := range in.Status.Conditions {
				in.Status.Conditions[i].LastTransitionTime.Time = time.Time{}
			}
		}
		if in, ok := actual.(*configv1.ClusterVersion); ok {
			for i := range in.Status.Conditions {
				in.Status.Conditions[i].LastTransitionTime.Time = time.Time{}
			}
			for i, item := range in.Status.History {
				if item.StartedTime.IsZero() {
					in.Status.History[i].StartedTime.Time = time.Unix(0, 0)
				} else {
					in.Status.History[i].StartedTime.Time = time.Unix(1, 0)
				}
				if item.CompletionTime != nil {
					in.Status.History[i].CompletionTime.Time = time.Unix(2, 0)
				}
			}

			e, a := fmt.Sprintf("%s/%s", resource, namespace), fmt.Sprintf("%s/%s", at.GetResource().Resource, at.GetNamespace())
			if e != a {
				t.Fatalf("unexpected action: %#v", at)
			}
			if _, ok := any(expect).(*configv1.ClusterVersion); ok {
				if !reflect.DeepEqual(expect, in) {
					t.Fatalf("expected ClusterVersion not equal to actual:\n%v\n%v", expect, in)
				}
			} else if _, ok := any(expect).(configv1.ClusterVersionCapabilitiesStatus); ok {
				if !reflect.DeepEqual(expect, in.Status.Capabilities) {
					t.Fatalf("expected ClusterVersionCapabilitiesStatus not equal to actual:\n%v\n%v", expect, in.Status.Capabilities)
				}
			} else if _, ok := any(expect).([]configv1.UpdateHistory); ok {
				if !reflect.DeepEqual(expect, in.Status.History) {
					t.Fatalf("expected History not equal to actual:\n%v\n%v", expect, in.Status.History)
				}
			} else {
				if !reflect.DeepEqual(expect, in.Status.Conditions) {
					t.Fatalf("expected Conditions not equal to actual:\n%v\n%v", expect, in.Status.Conditions)
				}
			}
		}
	default:
		t.Fatalf("unknown verb %T", a)
	}
}

func expectMutation(t *testing.T, a ktesting.Action, verb string, resource, subresource, namespace string, obj interface{}) {
	t.Helper()
	if verb != a.GetVerb() {
		t.Fatalf("unexpected verb: %s", a.GetVerb())
	}
	if subresource != a.GetSubresource() {
		t.Fatalf("unexpected subresource: %s", a.GetSubresource())
	}
	switch at := a.(type) {
	case ktesting.CreateAction:
		expect, actual := obj.(apiruntime.Object).DeepCopyObject(), at.GetObject()
		// default autogenerated cluster ID
		if in, ok := expect.(*configv1.ClusterVersion); ok {
			if in.Spec.ClusterID == "" {
				in.Spec.ClusterID = actual.(*configv1.ClusterVersion).Spec.ClusterID
			}
		}
		if in, ok := actual.(*configv1.ClusterOperator); ok {
			for i := range in.Status.Conditions {
				in.Status.Conditions[i].LastTransitionTime.Time = time.Time{}
			}
		}
		if in, ok := actual.(*configv1.ClusterVersion); ok {
			for i := range in.Status.Conditions {
				in.Status.Conditions[i].LastTransitionTime.Time = time.Time{}
			}
			for i, item := range in.Status.History {
				if item.StartedTime.IsZero() {
					in.Status.History[i].StartedTime.Time = time.Unix(0, 0)
				} else {
					in.Status.History[i].StartedTime.Time = time.Unix(1, 0)
				}
				if item.CompletionTime != nil {
					in.Status.History[i].CompletionTime.Time = time.Unix(2, 0)
				}
			}
		}

		e, a := fmt.Sprintf("%s/%s", resource, namespace), fmt.Sprintf("%s/%s", at.GetResource().Resource, at.GetNamespace())
		if e != a {
			t.Fatalf("unexpected action: %#v", at)
		}
		if !reflect.DeepEqual(expect, actual) {
			t.Logf("%#v", actual)
			t.Fatalf("unexpected object: %s", cmp.Diff(expect, actual))
		}
	default:
		t.Fatalf("unknown verb %T", a)
	}
}

func fakeClientsetWithUpdates(obj *configv1.ClusterVersion) *fake.Clientset {
	client := &fake.Clientset{}
	client.AddReactor("*", "*", func(action ktesting.Action) (handled bool, ret apiruntime.Object, err error) {
		if action.GetVerb() == "get" {
			return true, obj.DeepCopy(), nil
		}
		if action.GetVerb() == "update" && action.GetSubresource() == "status" {
			update := action.(ktesting.UpdateAction).GetObject().(*configv1.ClusterVersion)
			obj.Status = update.Status
			rv, _ := strconv.Atoi(update.ResourceVersion)
			obj.ResourceVersion = strconv.Itoa(rv + 1)
			klog.V(2).Infof("updated object to %#v", obj)
			return true, obj.DeepCopy(), nil
		}
		return false, nil, fmt.Errorf("unrecognized")
	})
	return client
}

func Test_loadReleaseVerifierFromConfigMap(t *testing.T) {
	const (
		ExpectedError       = "the config map openshift-config-managed/release-verification did not provide any signature stores to read from and cannot be used"
		ExpectedVerifierKey = "verifier-public-key-redhat"
	)

	tests := []struct {
		name           string
		fileName       string
		update         *payload.Update
		expectedError  string
		expectVerifier bool
		expectStore    bool
	}{
		{
			name:     "no-op when no objects are found",
			fileName: "",
			update:   &payload.Update{},
		},
		{
			name:          "no data, error returned",
			fileName:      "requires-data.yaml",
			update:        &payload.Update{},
			expectedError: ExpectedError,
		},
		{
			name:           "loads valid configuration",
			fileName:       "loads-valid.yaml",
			update:         &payload.Update{},
			expectVerifier: true,
			expectStore:    true,
		},
	}
	for _, tt := range tests {
		if tt.fileName != "" {
			raw, err := os.ReadFile(filepath.Join("testdata", "manifests", tt.fileName))
			if err != nil {
				t.Fatal(err)
			}
			ms, err := manifest.ParseManifests(bytes.NewReader(raw))
			if err != nil {
				t.Fatalf("failed to parse file %s as a manifest, error = %v", tt.fileName, err)
			}
			tt.update.Manifests = ms
		}
		t.Run(tt.name, func(t *testing.T) {
			f := kfake.NewClientset()
			got, store, err := loadConfigMapVerifierDataFromUpdate(tt.update, sigstore.DefaultClient, f.CoreV1(), &serial.Store{})
			if err == nil {
				if tt.expectedError != "" {
					t.Fatalf("loadConfigMapVerifierDataFromUpdate succeeded when we expected error \"%s\"", tt.expectedError)
				}
			} else if tt.expectedError == "" {
				t.Fatalf("loadConfigMapVerifierDataFromUpdate failed when we expected success: %v", err)
			} else if tt.expectedError != err.Error() {
				t.Fatalf("loadConfigMapVerifierDataFromUpdate failed with \"%v\" (expected \"%s\")", err, tt.expectedError)
			}

			if got == nil {
				if tt.expectVerifier {
					t.Fatalf("loadConfigMapVerifierDataFromUpdate did not return a verifier when expected")
				}
			} else if !tt.expectVerifier {
				t.Fatalf("loadConfigMapVerifierDataFromUpdate returned a verifer when not expected")
			} else {
				if _, ok := got.Verifiers()[ExpectedVerifierKey]; !ok {
					t.Fatalf("loadConfigMapVerifierDataFromUpdate did not return expected verifier %s", ExpectedVerifierKey)
				}
			}

			if tt.expectStore && store == nil {
				t.Fatalf("loadConfigMapVerifierDataFromUpdate did not return a store when expected")
			}
		})
	}
}

func TestOperator_mergeReleaseMetadata(t *testing.T) {
	for _, testCase := range []struct {
		name             string
		input            configv1.Release
		availableUpdates *availableUpdates
		expected         configv1.Release
	}{
		{
			name: "does not crash with empty inputs",
		},
		{
			name:     "minimal release with no available updates",
			input:    configv1.Release{Image: "image/image:v1.0.0"},
			expected: configv1.Release{Image: "image/image:v1.0.0"},
		},
		{
			name:             "minimal release with empty available updates",
			input:            configv1.Release{Image: "image/image:v1.0.0"},
			availableUpdates: &availableUpdates{},
			expected:         configv1.Release{Image: "image/image:v1.0.0"},
		},
		{
			name:  "minimal release with full, current available update",
			input: configv1.Release{Image: "image/image:v1.0.0"},
			availableUpdates: &availableUpdates{
				Current: configv1.Release{
					Version:  "1.0.1",
					Image:    "image/image:v1.0.0",
					URL:      configv1.URL("https://example.com/v1.0.1"),
					Channels: []string{"channel-a", "channel-b", "channel-c"},
				},
			},
			expected: configv1.Release{
				Version:  "1.0.1",
				Image:    "image/image:v1.0.0",
				URL:      configv1.URL("https://example.com/v1.0.1"),
				Channels: []string{"channel-a", "channel-b", "channel-c"},
			},
		},
		{
			name:  "minimal release with full, next-hop available update",
			input: configv1.Release{Image: "image/image:v1.0.0"},
			availableUpdates: &availableUpdates{
				Updates: []configv1.Release{
					{
						Version:  "1.0.1",
						Image:    "image/image:v1.0.0",
						URL:      configv1.URL("https://example.com/v1.0.1"),
						Channels: []string{"channel-a", "channel-b", "channel-c"},
					},
				},
			},
			expected: configv1.Release{
				Version:  "1.0.1",
				Image:    "image/image:v1.0.0",
				URL:      configv1.URL("https://example.com/v1.0.1"),
				Channels: []string{"channel-a", "channel-b", "channel-c"},
			},
		},
		{
			name:  "minimal release with non-matching available updates",
			input: configv1.Release{Image: "image/image:v1.0.0"},
			availableUpdates: &availableUpdates{
				Current: configv1.Release{
					Version:  "1.0.1",
					Image:    "image/image:v1.0.1",
					URL:      configv1.URL("https://example.com/v1.0.1"),
					Channels: []string{"channel-a", "channel-b", "channel-c"},
				},

				Updates: []configv1.Release{
					{
						Version:  "1.0.2",
						Image:    "image/image:v1.0.2",
						URL:      configv1.URL("https://example.com/v1.0.2"),
						Channels: []string{"channel-a", "channel-b", "channel-c"},
					},
				},
			},
			expected: configv1.Release{
				Image: "image/image:v1.0.0",
			},
		},
		{
			name: "fill release with full, current available update",
			input: configv1.Release{
				Version:  "1.0.0",
				Image:    "image/image:v1.0.0",
				URL:      configv1.URL("https://example.com/v1.0.0"),
				Channels: []string{"channel-z"},
			},
			availableUpdates: &availableUpdates{
				Current: configv1.Release{
					Version:  "1.0.1",
					Image:    "image/image:v1.0.0",
					URL:      configv1.URL("https://example.com/v1.0.1"),
					Channels: []string{"channel-a", "channel-b", "channel-c"},
				},
			},
			expected: configv1.Release{
				Version:  "1.0.0",
				Image:    "image/image:v1.0.0",
				URL:      configv1.URL("https://example.com/v1.0.0"),
				Channels: []string{"channel-z"},
			},
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			optr := Operator{availableUpdates: testCase.availableUpdates}
			actual := mergeReleaseMetadata(testCase.input, optr.getAvailableUpdates)
			if !reflect.DeepEqual(actual, testCase.expected) {
				t.Fatalf("unexpected: %s", cmp.Diff(testCase.expected, actual))
			}
		})
	}
}

func TestOperator_ownerReference(t *testing.T) {
	for _, tc := range []struct {
		name     string
		input    metav1.Object
		expected []metav1.OwnerReference
		cvUID    string
		cvName   string
	}{
		{
			name:   "no CV reference",
			cvName: "version",
			cvUID:  "uuid1",
			input:  &appsv1.Deployment{},
			expected: []metav1.OwnerReference{
				{
					APIVersion: configv1.GroupVersion.Identifier(),
					Kind:       "ClusterVersion",
					Name:       "version",
					UID:        "uuid1",
				},
			},
		},
		{
			name:   "existing CV reference",
			cvName: "version",
			cvUID:  "uuid2",
			input: &appsv1.Deployment{
				ObjectMeta: metav1.ObjectMeta{
					OwnerReferences: []metav1.OwnerReference{
						{
							APIVersion: configv1.GroupVersion.Identifier(),
							Kind:       "ClusterVersion",
							Name:       "version",
							UID:        "uuid1",
						},
					},
				},
			},
			expected: []metav1.OwnerReference{
				{
					APIVersion: configv1.GroupVersion.Identifier(),
					Kind:       "ClusterVersion",
					Name:       "version",
					UID:        "uuid2",
				},
			},
		},
		{
			name:   "existing incorrect CV reference",
			cvName: "version",
			cvUID:  "uuid2",
			input: &appsv1.Deployment{
				ObjectMeta: metav1.ObjectMeta{
					OwnerReferences: []metav1.OwnerReference{
						{
							APIVersion: configv1.GroupVersion.Identifier(),
							Kind:       "ClusterVersion",
							Name:       "user-defined",
							UID:        "uuid2",
						},
					},
				},
			},
			expected: []metav1.OwnerReference{
				{
					APIVersion: configv1.GroupVersion.Identifier(),
					Kind:       "ClusterVersion",
					Name:       "version",
					UID:        "uuid2",
				},
			},
		},
		{
			name:   "existing non-CV owner reference",
			cvName: "version",
			cvUID:  "uuid2",
			input: &appsv1.Deployment{
				ObjectMeta: metav1.ObjectMeta{
					OwnerReferences: []metav1.OwnerReference{
						{
							APIVersion: appsv1.SchemeGroupVersion.Identifier(),
							Kind:       "Deployment",
							Name:       "user-defined",
							UID:        "uuid2",
						},
					},
				},
			},
			expected: []metav1.OwnerReference{
				{
					APIVersion: appsv1.SchemeGroupVersion.Identifier(),
					Kind:       "Deployment",
					Name:       "user-defined",
					UID:        "uuid2",
				},
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			o := &Operator{name: tc.cvName, uid: types.UID(tc.cvUID)}
			o.ownerReferenceModifier(tc.input)
			if len(tc.input.GetOwnerReferences()) != len(tc.expected) {
				t.Fatalf("Expected owner references do not match: %v", cmp.Diff(tc.input.GetOwnerReferences(), tc.expected))
			}
			for i, ref := range tc.input.GetOwnerReferences() {
				expected := tc.expected[i]
				if ref.UID != expected.UID || ref.Name != expected.Name || ref.Kind != expected.Kind {
					t.Errorf("owner reference at %d does not match expected reference: %v", i, cmp.Diff(ref, expected))
				}
			}
		})
	}
}

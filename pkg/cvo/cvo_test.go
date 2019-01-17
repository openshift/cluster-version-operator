package cvo

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"strconv"
	"testing"
	"time"

	"github.com/davecgh/go-spew/spew"
	"github.com/golang/glog"
	"github.com/google/uuid"
	configv1 "github.com/openshift/api/config/v1"
	clientset "github.com/openshift/client-go/config/clientset/versioned"
	"github.com/openshift/client-go/config/clientset/versioned/fake"
	apiextv1beta1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1beta1"
	apiextclientv1 "k8s.io/apiextensions-apiserver/pkg/client/clientset/clientset/typed/apiextensions/v1beta1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/diff"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/rest"
	ktesting "k8s.io/client-go/testing"
	"k8s.io/client-go/util/workqueue"
)

var (
	// defaultStartedTime is a shorthand for verifying a start time is set
	defaultStartedTime = metav1.Time{Time: time.Unix(1, 0)}
	// defaultCompletionTime is a shorthand for verifying a completion time is set
	defaultCompletionTime = metav1.Time{Time: time.Unix(2, 0)}
)

type clientCVLister struct {
	client clientset.Interface
}

func (c *clientCVLister) Get(name string) (*configv1.ClusterVersion, error) {
	return c.client.Config().ClusterVersions().Get(name, metav1.GetOptions{})
}
func (c *clientCVLister) List(selector labels.Selector) (ret []*configv1.ClusterVersion, err error) {
	list, err := c.client.Config().ClusterVersions().List(metav1.ListOptions{LabelSelector: selector.String()})
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
	return c.client.Config().ClusterOperators().Get(name, metav1.GetOptions{})
}
func (c *clientCOLister) List(selector labels.Selector) (ret []*configv1.ClusterOperator, err error) {
	list, err := c.client.Config().ClusterOperators().List(metav1.ListOptions{LabelSelector: selector.String()})
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

type crdLister struct {
	Err   error
	Items []*apiextv1beta1.CustomResourceDefinition
}

func (r *crdLister) Get(name string) (*apiextv1beta1.CustomResourceDefinition, error) {
	for _, s := range r.Items {
		if s.Name == name {
			return s, nil
		}
	}
	return nil, errors.NewNotFound(schema.GroupResource{Resource: "customresourcedefinitions"}, name)
}

func (r *crdLister) List(selector labels.Selector) (ret []*apiextv1beta1.CustomResourceDefinition, err error) {
	return r.Items, r.Err
}

type fakeApiExtClient struct{}

func (c *fakeApiExtClient) Discovery() discovery.DiscoveryInterface {
	panic("not implemented")
}

func (c *fakeApiExtClient) ApiextensionsV1beta1() apiextclientv1.ApiextensionsV1beta1Interface {
	return c
}

func (c *fakeApiExtClient) Apiextensions() apiextclientv1.ApiextensionsV1beta1Interface {
	return c
}

func (c *fakeApiExtClient) RESTClient() rest.Interface { panic("not implemented") }

func (c *fakeApiExtClient) CustomResourceDefinitions() apiextclientv1.CustomResourceDefinitionInterface {
	return c
}
func (c *fakeApiExtClient) Create(crd *apiextv1beta1.CustomResourceDefinition) (*apiextv1beta1.CustomResourceDefinition, error) {
	return crd, nil
}
func (c *fakeApiExtClient) Update(*apiextv1beta1.CustomResourceDefinition) (*apiextv1beta1.CustomResourceDefinition, error) {
	panic("not implemented")
}
func (c *fakeApiExtClient) UpdateStatus(*apiextv1beta1.CustomResourceDefinition) (*apiextv1beta1.CustomResourceDefinition, error) {
	panic("not implemented")
}
func (c *fakeApiExtClient) Delete(name string, options *metav1.DeleteOptions) error {
	panic("not implemented")
}
func (c *fakeApiExtClient) DeleteCollection(options *metav1.DeleteOptions, listOptions metav1.ListOptions) error {
	panic("not implemented")
}
func (c *fakeApiExtClient) Get(name string, options metav1.GetOptions) (*apiextv1beta1.CustomResourceDefinition, error) {
	panic("not implemented")
}
func (c *fakeApiExtClient) List(opts metav1.ListOptions) (*apiextv1beta1.CustomResourceDefinitionList, error) {
	panic("not implemented")
}
func (c *fakeApiExtClient) Watch(opts metav1.ListOptions) (watch.Interface, error) {
	panic("not implemented")
}
func (c *fakeApiExtClient) Patch(name string, pt types.PatchType, data []byte, subresources ...string) (result *apiextv1beta1.CustomResourceDefinition, err error) {
	panic("not implemented")
}

func TestOperator_sync(t *testing.T) {
	id := uuid.Must(uuid.NewRandom()).String()

	tests := []struct {
		name        string
		key         string
		syncStatus  *SyncWorkerStatus
		optr        Operator
		init        func(optr *Operator)
		want        bool
		wantErr     func(*testing.T, error)
		wantActions func(*testing.T, *Operator)
		wantSync    []configv1.Update
	}{
		{
			name: "create version and status",
			optr: Operator{
				releaseVersion: "4.0.1",
				releaseImage:   "payload/image:v4.0.1",
				namespace:      "test",
				name:           "default",
				client:         fake.NewSimpleClientset(),
			},
			wantActions: func(t *testing.T, optr *Operator) {
				f := optr.client.(*fake.Clientset)
				act := f.Actions()
				if len(act) != 3 {
					t.Fatalf("unknown actions: %d %#v", len(act), act)
				}
				expectGet(t, act[0], "clusterversions", "", "default")
				expectGet(t, act[1], "clusterversions", "", "default")
				expectCreate(t, act[2], "clusterversions", "", &configv1.ClusterVersion{
					ObjectMeta: metav1.ObjectMeta{
						Name: "default",
					},
					Spec: configv1.ClusterVersionSpec{
						Channel: "fast",
					},
				})
			},
		},
		{
			name: "progressing and previously failed, not reconciling",
			syncStatus: &SyncWorkerStatus{
				Step:        "Moving",
				Reconciling: false,
				Actual:      configv1.Update{Version: "0.0.1-abc", Payload: "payload/image:v4.0.1"},
				Failure: &updateError{
					Reason:  "UpdatePayloadIntegrity",
					Message: "unable to apply object",
				},
			},
			optr: Operator{
				releaseVersion: "4.0.1",
				releaseImage:   "payload/image:v4.0.1",
				namespace:      "test",
				name:           "default",
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
								{Version: "4.0.1", Payload: "payload/image:v4.0.1", StartedTime: defaultStartedTime},
							},
							Desired:     configv1.Update{Version: "4.0.1", Payload: "payload/image:v4.0.1"},
							VersionHash: "",
							Conditions: []configv1.ClusterOperatorStatusCondition{
								{Type: configv1.OperatorAvailable, Status: configv1.ConditionFalse},
								{Type: configv1.OperatorFailing, Status: configv1.ConditionTrue, Reason: "UpdatePayloadIntegrity", Message: "unable to apply object"},
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
							{State: configv1.PartialUpdate, Version: "0.0.1-abc", Payload: "payload/image:v4.0.1", StartedTime: defaultStartedTime},
							{Version: "4.0.1", Payload: "payload/image:v4.0.1", StartedTime: defaultStartedTime, CompletionTime: &defaultCompletionTime},
						},
						Desired:     configv1.Update{Version: "0.0.1-abc", Payload: "payload/image:v4.0.1"},
						VersionHash: "",
						Conditions: []configv1.ClusterOperatorStatusCondition{
							{Type: configv1.OperatorAvailable, Status: configv1.ConditionFalse},
							{Type: configv1.OperatorFailing, Status: configv1.ConditionTrue, Reason: "UpdatePayloadIntegrity", Message: "unable to apply object"},
							{Type: configv1.OperatorProgressing, Status: configv1.ConditionTrue, Reason: "UpdatePayloadIntegrity", Message: "Unable to apply 0.0.1-abc: the contents of the update are invalid"},
							{Type: configv1.RetrievedUpdates, Status: configv1.ConditionFalse},
						},
					},
				})
			},
		},
		{
			name: "progressing and previously failed, reconciling",
			optr: Operator{
				releaseVersion: "4.0.1",
				releaseImage:   "payload/image:v4.0.1",
				namespace:      "test",
				name:           "default",
				configSync: &fakeSyncRecorder{
					Returns: &SyncWorkerStatus{
						Step:        "Moving",
						Reconciling: true,
						Actual:      configv1.Update{Version: "0.0.1-abc", Payload: "payload/image:v4.0.1"},
						Failure: &updateError{
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
								{Version: "4.0.1", Payload: "payload/image:v4.0.1", StartedTime: defaultStartedTime},
							},
							Desired:     configv1.Update{Version: "4.0.1", Payload: "payload/image:v4.0.1"},
							VersionHash: "",
							Conditions: []configv1.ClusterOperatorStatusCondition{
								{Type: configv1.OperatorAvailable, Status: configv1.ConditionFalse},
								{Type: configv1.OperatorFailing, Status: configv1.ConditionTrue, Reason: "UpdatePayloadIntegrity", Message: "unable to apply object"},
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
							{State: configv1.PartialUpdate, Version: "0.0.1-abc", Payload: "payload/image:v4.0.1", StartedTime: defaultStartedTime},
							{Version: "4.0.1", Payload: "payload/image:v4.0.1", StartedTime: defaultStartedTime, CompletionTime: &defaultCompletionTime},
						},
						Desired:     configv1.Update{Version: "0.0.1-abc", Payload: "payload/image:v4.0.1"},
						VersionHash: "",
						Conditions: []configv1.ClusterOperatorStatusCondition{
							{Type: configv1.OperatorAvailable, Status: configv1.ConditionFalse},
							{Type: configv1.OperatorFailing, Status: configv1.ConditionTrue, Reason: "UpdatePayloadIntegrity", Message: "unable to apply object"},
							{Type: configv1.OperatorProgressing, Status: configv1.ConditionFalse, Reason: "UpdatePayloadIntegrity", Message: "Error while reconciling 0.0.1-abc: the contents of the update are invalid"},
							{Type: configv1.RetrievedUpdates, Status: configv1.ConditionFalse},
						},
					},
				})
			},
		},
		{
			name: "progressing and previously failed, reconciling and multiple completions",
			optr: Operator{
				releaseVersion: "4.0.1",
				releaseImage:   "payload/image:v4.0.1",
				namespace:      "test",
				name:           "default",
				configSync: &fakeSyncRecorder{
					Returns: &SyncWorkerStatus{
						Step:        "Moving",
						Reconciling: true,
						Completed:   2,
						Actual:      configv1.Update{Version: "0.0.1-abc", Payload: "payload/image:v4.0.1"},
						Failure: &updateError{
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
								{Version: "4.0.1", Payload: "payload/image:v4.0.1", StartedTime: defaultStartedTime},
							},
							Desired:     configv1.Update{Version: "4.0.1", Payload: "payload/image:v4.0.1"},
							VersionHash: "",
							Conditions: []configv1.ClusterOperatorStatusCondition{
								{Type: configv1.OperatorAvailable, Status: configv1.ConditionFalse},
								{Type: configv1.OperatorFailing, Status: configv1.ConditionTrue, Reason: "UpdatePayloadIntegrity", Message: "unable to apply object"},
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
							{State: configv1.CompletedUpdate, Version: "0.0.1-abc", Payload: "payload/image:v4.0.1", StartedTime: defaultStartedTime, CompletionTime: &defaultCompletionTime},
							{Version: "4.0.1", Payload: "payload/image:v4.0.1", StartedTime: defaultStartedTime, CompletionTime: &defaultCompletionTime},
						},
						Desired:     configv1.Update{Version: "0.0.1-abc", Payload: "payload/image:v4.0.1"},
						VersionHash: "",
						Conditions: []configv1.ClusterOperatorStatusCondition{
							{Type: configv1.OperatorAvailable, Status: configv1.ConditionTrue, Message: "Done applying 0.0.1-abc"},
							{Type: configv1.OperatorFailing, Status: configv1.ConditionTrue, Reason: "UpdatePayloadIntegrity", Message: "unable to apply object"},
							{Type: configv1.OperatorProgressing, Status: configv1.ConditionFalse, Reason: "UpdatePayloadIntegrity", Message: "Error while reconciling 0.0.1-abc: the contents of the update are invalid"},
							{Type: configv1.RetrievedUpdates, Status: configv1.ConditionFalse},
						},
					},
				})
			},
		},
		{
			name: "progressing and encounters error during payload sync",
			optr: Operator{
				releaseVersion: "4.0.1",
				releaseImage:   "payload/image:v4.0.1",
				namespace:      "test",
				name:           "default",
				configSync: &fakeSyncRecorder{
					Returns: &SyncWorkerStatus{
						Step:        "Moving",
						Actual:      configv1.Update{Version: "0.0.1-abc", Payload: "payload/image:v4.0.1"},
						Failure:     fmt.Errorf("injected error"),
						VersionHash: "foo",
					},
				},
				client: fake.NewSimpleClientset(
					&configv1.ClusterVersion{
						ObjectMeta: metav1.ObjectMeta{
							Name: "default",
						},
						Spec: configv1.ClusterVersionSpec{
							Channel: "fast",
						},
						Status: configv1.ClusterVersionStatus{
							History: []configv1.UpdateHistory{
								{Version: "4.0.1", Payload: "payload/image:v4.0.1"},
							},
							VersionHash: "",
							Conditions: []configv1.ClusterOperatorStatusCondition{
								{Type: configv1.OperatorAvailable, Status: configv1.ConditionFalse},
								{Type: configv1.OperatorFailing, Status: configv1.ConditionTrue, Message: "unable to apply object"},
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
						Desired: configv1.Update{Version: "0.0.1-abc", Payload: "payload/image:v4.0.1"},
						History: []configv1.UpdateHistory{
							{State: configv1.PartialUpdate, Version: "0.0.1-abc", Payload: "payload/image:v4.0.1", StartedTime: defaultStartedTime},
							{Version: "4.0.1", Payload: "payload/image:v4.0.1", StartedTime: metav1.Time{Time: time.Unix(0, 0)}, CompletionTime: &defaultCompletionTime},
						},
						VersionHash: "foo",
						Conditions: []configv1.ClusterOperatorStatusCondition{
							{Type: configv1.OperatorAvailable, Status: configv1.ConditionFalse},
							{Type: configv1.OperatorFailing, Status: configv1.ConditionTrue, Message: "injected error"},
							{Type: configv1.OperatorProgressing, Status: configv1.ConditionTrue, Message: "Unable to apply 0.0.1-abc: an error occurred"},
							{Type: configv1.RetrievedUpdates, Status: configv1.ConditionFalse},
						},
					},
				})
			},
		},
		{
			name: "invalid payload reports payload error",
			syncStatus: &SyncWorkerStatus{
				Failure: os.ErrNotExist,
				Actual:  configv1.Update{Payload: "payload/image:v4.0.1", Version: "4.0.1"},
			},
			optr: Operator{
				releaseVersion: "4.0.1",
				releaseImage:   "payload/image:v4.0.1",
				namespace:      "test",
				name:           "default",
				client: fake.NewSimpleClientset(
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
						Desired: configv1.Update{Payload: "payload/image:v4.0.1", Version: "4.0.1"},
						History: []configv1.UpdateHistory{
							{State: configv1.PartialUpdate, Version: "4.0.1", Payload: "payload/image:v4.0.1", StartedTime: defaultStartedTime},
						},
						VersionHash: "",
						Conditions: []configv1.ClusterOperatorStatusCondition{
							{Type: configv1.OperatorAvailable, Status: configv1.ConditionFalse},
							{Type: configv1.OperatorFailing, Status: configv1.ConditionTrue, Message: "file does not exist"},
							{Type: configv1.OperatorProgressing, Status: configv1.ConditionTrue, Message: "Unable to apply 4.0.1: an error occurred"},
							{Type: configv1.RetrievedUpdates, Status: configv1.ConditionFalse},
						},
					},
				})
			},
		},
		{
			name: "invalid payload while progressing preserves progressing order and partial history",
			syncStatus: &SyncWorkerStatus{
				Step:     "Working",
				Fraction: 0.6,
				Failure:  os.ErrNotExist,
				Actual:   configv1.Update{Payload: "payload/image:v4.0.1", Version: "4.0.1"},
			},
			optr: Operator{
				releaseVersion: "4.0.1",
				releaseImage:   "payload/image:v4.0.1",
				namespace:      "test",
				name:           "default",
				client: fake.NewSimpleClientset(
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
								{Version: "4.0.1", Payload: "payload/image:v4.0.1"},
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
						Desired: configv1.Update{Payload: "payload/image:v4.0.1", Version: "4.0.1"},
						History: []configv1.UpdateHistory{
							// we populate state, but not startedTime
							{State: configv1.PartialUpdate, Version: "4.0.1", Payload: "payload/image:v4.0.1", StartedTime: metav1.Time{time.Unix(0, 0)}},
						},
						VersionHash: "",
						Conditions: []configv1.ClusterOperatorStatusCondition{
							// the order of progressing in the conditions array is preserved
							{Type: configv1.OperatorProgressing, Status: configv1.ConditionTrue, Message: "Unable to apply 4.0.1: an error occurred"},
							{Type: configv1.OperatorAvailable, Status: configv1.ConditionFalse},
							{Type: configv1.OperatorFailing, Status: configv1.ConditionTrue, Message: "file does not exist"},
							{Type: configv1.RetrievedUpdates, Status: configv1.ConditionFalse},
						},
					},
				})
			},
		},
		{
			name: "set initial status conditions",
			syncStatus: &SyncWorkerStatus{
				Actual: configv1.Update{Payload: "payload/image:v4.0.1", Version: ""},
			},
			optr: Operator{
				releaseImage: "payload/image:v4.0.1",
				namespace:    "test",
				name:         "default",
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
								Payload:     "payload/image:v4.0.1",
								Version:     "", // we don't know our payload yet and releaseVersion is unset
								StartedTime: defaultStartedTime,
							},
						},
						Desired:     configv1.Update{Version: "", Payload: "payload/image:v4.0.1"},
						VersionHash: "",
						Conditions: []configv1.ClusterOperatorStatusCondition{
							{Type: configv1.OperatorAvailable, Status: configv1.ConditionFalse},
							{Type: configv1.OperatorFailing, Status: configv1.ConditionFalse},
							{Type: configv1.OperatorProgressing, Status: configv1.ConditionTrue, Message: "Working towards payload/image:v4.0.1"},
							{Type: configv1.RetrievedUpdates, Status: configv1.ConditionFalse},
						},
					},
				})
			},
		},
		{
			name: "record a new version entry if the controller is restarted with a new image",
			syncStatus: &SyncWorkerStatus{
				Actual: configv1.Update{Payload: "payload/image:v4.0.2", Version: "4.0.2"},
			},
			optr: Operator{
				releaseImage:   "payload/image:v4.0.2",
				releaseVersion: "4.0.2",
				namespace:      "test",
				name:           "default",
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
								Payload:     "payload/image:v4.0.1",
								Version:     "", // we didn't know our payload before
								StartedTime: defaultStartedTime,
							},
						},
						Desired:     configv1.Update{Payload: "payload/image:v4.0.1"},
						VersionHash: "",
						Conditions: []configv1.ClusterOperatorStatusCondition{
							{Type: configv1.OperatorAvailable, Status: configv1.ConditionFalse},
							{Type: configv1.OperatorFailing, Status: configv1.ConditionFalse},
							{Type: configv1.OperatorProgressing, Status: configv1.ConditionTrue, Message: "Initializing, will work towards payload/image:v4.0.1"},
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
								Payload:     "payload/image:v4.0.2",
								Version:     "4.0.2",
								StartedTime: defaultStartedTime,
							},
							{
								State:          configv1.PartialUpdate,
								Payload:        "payload/image:v4.0.1",
								Version:        "",
								StartedTime:    defaultStartedTime,
								CompletionTime: &defaultCompletionTime,
							},
						},
						Desired:     configv1.Update{Payload: "payload/image:v4.0.2", Version: "4.0.2"},
						VersionHash: "",
						Conditions: []configv1.ClusterOperatorStatusCondition{
							{Type: configv1.OperatorAvailable, Status: configv1.ConditionFalse},
							{Type: configv1.OperatorFailing, Status: configv1.ConditionFalse},
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
				Actual: configv1.Update{Payload: "payload/image:v4.0.1", Version: "4.0.1"},
			},
			optr: Operator{
				releaseImage:   "payload/image:v4.0.1",
				releaseVersion: "4.0.1",
				namespace:      "test",
				name:           "default",
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
								Payload:     "payload/image:v4.0.2",
								Version:     "4.0.2",
								StartedTime: defaultStartedTime,
							},
							{
								State:          configv1.CompletedUpdate,
								Payload:        "payload/image:v4.0.1",
								Version:        "4.0.1",
								StartedTime:    defaultStartedTime,
								CompletionTime: &defaultCompletionTime,
							},
						},
						Desired:     configv1.Update{Payload: "payload/image:v4.0.2"},
						VersionHash: "",
						Conditions: []configv1.ClusterOperatorStatusCondition{
							{Type: configv1.OperatorAvailable, Status: configv1.ConditionFalse},
							{Type: configv1.OperatorFailing, Status: configv1.ConditionFalse},
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
								Payload:     "payload/image:v4.0.1",
								Version:     "4.0.1",
								StartedTime: defaultStartedTime,
							},
							{
								State:          configv1.PartialUpdate,
								Payload:        "payload/image:v4.0.2",
								Version:        "4.0.2",
								StartedTime:    defaultStartedTime,
								CompletionTime: &defaultCompletionTime,
							},
							{
								State:          configv1.CompletedUpdate,
								Payload:        "payload/image:v4.0.1",
								Version:        "4.0.1",
								StartedTime:    defaultStartedTime,
								CompletionTime: &defaultCompletionTime,
							},
						},
						Desired: configv1.Update{
							Version: "4.0.1",
							Payload: "payload/image:v4.0.1",
						},
						VersionHash: "",
						Conditions: []configv1.ClusterOperatorStatusCondition{
							{Type: configv1.OperatorAvailable, Status: configv1.ConditionFalse},
							{Type: configv1.OperatorFailing, Status: configv1.ConditionFalse},
							// we don't reset the message here until the payload is loaded
							{Type: configv1.OperatorProgressing, Status: configv1.ConditionTrue, Message: "Working towards 4.0.1"},
							{Type: configv1.RetrievedUpdates, Status: configv1.ConditionFalse},
						},
					},
				})
			},
		},
		{
			name: "after desired update is cancelled, go to reconciling",
			syncStatus: &SyncWorkerStatus{
				Actual: configv1.Update{Payload: "payload/image:v4.0.1", Version: "4.0.1"},
			},
			optr: Operator{
				releaseImage:   "payload/image:v4.0.1",
				releaseVersion: "4.0.1",
				namespace:      "test",
				name:           "default",
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
								Payload:     "payload/image:v4.0.1",
								Version:     "4.0.1",
								StartedTime: defaultStartedTime,
							},
							{
								State:          configv1.PartialUpdate,
								Payload:        "payload/image:v4.0.2",
								Version:        "4.0.2",
								StartedTime:    defaultStartedTime,
								CompletionTime: &defaultCompletionTime,
							},
							{
								State:          configv1.CompletedUpdate,
								Payload:        "payload/image:v4.0.1",
								Version:        "4.0.1",
								StartedTime:    defaultStartedTime,
								CompletionTime: &defaultCompletionTime,
							},
						},
						Desired:     configv1.Update{Payload: "payload/image:v4.0.1", Version: "4.0.1"},
						VersionHash: "",
						Conditions: []configv1.ClusterOperatorStatusCondition{
							{Type: configv1.OperatorAvailable, Status: configv1.ConditionFalse},
							{Type: configv1.OperatorFailing, Status: configv1.ConditionFalse},
							// we don't reset the message here until the payload is loaded
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
								Payload:     "payload/image:v4.0.1",
								Version:     "4.0.1",
								StartedTime: defaultStartedTime,
							},
							{
								State:          configv1.PartialUpdate,
								Payload:        "payload/image:v4.0.2",
								Version:        "4.0.2",
								StartedTime:    defaultStartedTime,
								CompletionTime: &defaultCompletionTime,
							},
							{
								State:          configv1.CompletedUpdate,
								Payload:        "payload/image:v4.0.1",
								Version:        "4.0.1",
								StartedTime:    defaultStartedTime,
								CompletionTime: &defaultCompletionTime,
							},
						},
						Desired:     configv1.Update{Payload: "payload/image:v4.0.1", Version: "4.0.1"},
						VersionHash: "",
						Conditions: []configv1.ClusterOperatorStatusCondition{
							{Type: configv1.OperatorAvailable, Status: configv1.ConditionFalse},
							{Type: configv1.OperatorFailing, Status: configv1.ConditionFalse},
							// we correct the message that was incorrect from the previous state
							{Type: configv1.OperatorProgressing, Status: configv1.ConditionTrue, Message: "Working towards 4.0.1"},
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
				Actual:      configv1.Update{Payload: "payload/image:v4.0.1", Version: "0.0.1-abc"},
			},
			optr: Operator{
				releaseImage: "payload/image:v4.0.1",
				namespace:    "test",
				name:         "default",
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
								Payload:     "payload/image:v4.0.1",
								StartedTime: defaultStartedTime,
							},
						},
						Desired:     configv1.Update{Payload: "payload/image:v4.0.1"},
						VersionHash: "",
						Conditions: []configv1.ClusterOperatorStatusCondition{
							{Type: configv1.OperatorAvailable, Status: configv1.ConditionFalse},
							{Type: configv1.OperatorFailing, Status: configv1.ConditionFalse},
							{Type: configv1.OperatorProgressing, Status: configv1.ConditionTrue, Message: "Initializing, will work towards payload/image:v4.0.1"},
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
				// will use the version from content1 (the payload) when we set the progressing condition
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
							{State: configv1.PartialUpdate, Payload: "payload/image:v4.0.1", Version: "0.0.1-abc", StartedTime: defaultStartedTime},
						},
						Desired:     configv1.Update{Payload: "payload/image:v4.0.1", Version: "0.0.1-abc"},
						VersionHash: "xyz",
						Conditions: []configv1.ClusterOperatorStatusCondition{
							{Type: configv1.OperatorAvailable, Status: configv1.ConditionFalse},
							{Type: configv1.OperatorFailing, Status: configv1.ConditionFalse},
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
				Reconciling: true,
				Completed:   1,
				VersionHash: "xyz",
				Actual:      configv1.Update{Payload: "payload/image:v4.0.1", Version: "0.0.1-abc"},
			},
			optr: Operator{
				releaseImage:   "payload/image:v4.0.1",
				releaseVersion: "0.0.1-abc",
				namespace:      "test",
				name:           "default",
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
									Payload:        "payload/image:v4.0.1",
									Version:        "0.0.1-abc",
									CompletionTime: &defaultStartedTime,
								},
							},
							Desired: configv1.Update{
								Version: "0.0.1-abc",
								Payload: "payload/image:v4.0.1",
							},
							VersionHash: "xyz",
							Generation:  2,
							Conditions: []configv1.ClusterOperatorStatusCondition{
								{Type: configv1.OperatorAvailable, Status: configv1.ConditionTrue, Message: "Done applying 0.0.1-abc"},
								{Type: configv1.OperatorFailing, Status: configv1.ConditionFalse},
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
				Reconciling: true,
				Completed:   1,
				Actual:      configv1.Update{Payload: "payload/image:v4.0.1", Version: "0.0.1-abc"},
			},
			optr: Operator{
				releaseImage: "payload/image:v4.0.1",
				namespace:    "test",
				name:         "default",
				availableUpdates: &availableUpdates{
					Upstream: "http://localhost:8080/graph",
					Channel:  "fast",
					Updates: []configv1.Update{
						{Version: "4.0.2", Payload: "test/image:1"},
						{Version: "4.0.3", Payload: "test/image:2"},
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
						Generation: 2,
						Conditions: []configv1.ClusterOperatorStatusCondition{
							{Type: configv1.OperatorAvailable, Status: configv1.ConditionFalse},
							{Type: configv1.OperatorProgressing, Status: configv1.ConditionFalse, Message: "Cluster version is 0.0.1-abc"},
							{Type: configv1.OperatorFailing, Status: configv1.ConditionFalse},
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
						AvailableUpdates: []configv1.Update{
							{Version: "4.0.2", Payload: "test/image:1"},
							{Version: "4.0.3", Payload: "test/image:2"},
						},
						History: []configv1.UpdateHistory{
							{State: configv1.CompletedUpdate, Version: "0.0.1-abc", Payload: "payload/image:v4.0.1", StartedTime: defaultStartedTime, CompletionTime: &defaultCompletionTime},
						},
						Desired:    configv1.Update{Payload: "payload/image:v4.0.1", Version: "0.0.1-abc"},
						Generation: 2,
						Conditions: []configv1.ClusterOperatorStatusCondition{
							{Type: configv1.OperatorAvailable, Status: configv1.ConditionTrue, Message: "Done applying 0.0.1-abc"},
							{Type: configv1.OperatorProgressing, Status: configv1.ConditionFalse, Message: "Cluster version is 0.0.1-abc"},
							{Type: configv1.OperatorFailing, Status: configv1.ConditionFalse},
							{Type: configv1.RetrievedUpdates, Status: configv1.ConditionTrue},
						},
					},
				})
			},
		},
		{
			name: "new available updates for the default upstream URL, client has no upstream",
			syncStatus: &SyncWorkerStatus{
				Reconciling: true,
				Completed:   1,
				Actual:      configv1.Update{Payload: "payload/image:v4.0.1", Version: "0.0.1-abc"},
			},
			optr: Operator{
				releaseImage:          "payload/image:v4.0.1",
				namespace:             "test",
				name:                  "default",
				defaultUpstreamServer: "http://localhost:8080/graph",
				availableUpdates: &availableUpdates{
					Upstream: "",
					Channel:  "fast",
					Updates: []configv1.Update{
						{Version: "4.0.2", Payload: "test/image:1"},
						{Version: "4.0.3", Payload: "test/image:2"},
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
						Generation: 2,
						Conditions: []configv1.ClusterOperatorStatusCondition{
							{Type: configv1.OperatorAvailable, Status: configv1.ConditionFalse},
							{Type: configv1.OperatorProgressing, Status: configv1.ConditionFalse, Message: "Cluster version is 0.0.1-abc"},
							{Type: configv1.OperatorFailing, Status: configv1.ConditionFalse},
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
						AvailableUpdates: []configv1.Update{
							{Version: "4.0.2", Payload: "test/image:1"},
							{Version: "4.0.3", Payload: "test/image:2"},
						},
						History: []configv1.UpdateHistory{
							{State: configv1.CompletedUpdate, Version: "0.0.1-abc", Payload: "payload/image:v4.0.1", StartedTime: defaultStartedTime, CompletionTime: &defaultCompletionTime},
						},
						Desired:    configv1.Update{Payload: "payload/image:v4.0.1", Version: "0.0.1-abc"},
						Generation: 2,
						Conditions: []configv1.ClusterOperatorStatusCondition{
							{Type: configv1.OperatorAvailable, Status: configv1.ConditionTrue, Message: "Done applying 0.0.1-abc"},
							{Type: configv1.OperatorProgressing, Status: configv1.ConditionFalse, Message: "Cluster version is 0.0.1-abc"},
							{Type: configv1.OperatorFailing, Status: configv1.ConditionFalse},
							{Type: configv1.RetrievedUpdates, Status: configv1.ConditionTrue},
						},
					},
				})
			},
		},
		{
			name: "new available updates but for a different channel",
			syncStatus: &SyncWorkerStatus{
				Reconciling: true,
				Completed:   1,
				Actual:      configv1.Update{Payload: "payload/image:v4.0.1", Version: "0.0.1-abc"},
			},
			optr: Operator{
				releaseImage: "payload/image:v4.0.1",
				namespace:    "test",
				name:         "default",
				availableUpdates: &availableUpdates{
					Upstream: "http://localhost:8080/graph",
					Channel:  "fast",
					Updates: []configv1.Update{
						{Version: "4.0.2", Payload: "test/image:1"},
						{Version: "4.0.3", Payload: "test/image:2"},
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
						Generation: 2,
						Conditions: []configv1.ClusterOperatorStatusCondition{
							{Type: configv1.OperatorAvailable, Status: configv1.ConditionFalse},
							{Type: configv1.OperatorProgressing, Status: configv1.ConditionFalse, Message: "Cluster version is 0.0.1-abc"},
							{Type: configv1.OperatorFailing, Status: configv1.ConditionFalse},
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
							{State: configv1.CompletedUpdate, Version: "0.0.1-abc", Payload: "payload/image:v4.0.1", StartedTime: defaultStartedTime, CompletionTime: &defaultCompletionTime},
						},
						Desired:    configv1.Update{Payload: "payload/image:v4.0.1", Version: "0.0.1-abc"},
						Generation: 2,
						Conditions: []configv1.ClusterOperatorStatusCondition{
							{Type: configv1.OperatorAvailable, Status: configv1.ConditionTrue, Message: "Done applying 0.0.1-abc"},
							{Type: configv1.OperatorProgressing, Status: configv1.ConditionFalse, Message: "Cluster version is 0.0.1-abc"},
							{Type: configv1.OperatorFailing, Status: configv1.ConditionFalse},
							{Type: configv1.RetrievedUpdates, Status: configv1.ConditionFalse},
						},
					},
				})
			},
		},
		{
			name: "user requested a version, sync loop hasn't started",
			syncStatus: &SyncWorkerStatus{
				Reconciling: true,
				Completed:   1,
				Actual:      configv1.Update{Payload: "payload/image:v4.0.1", Version: "4.0.1"},
			},
			optr: Operator{
				releaseImage: "payload/image:v4.0.1",
				namespace:    "test",
				name:         "default",
				client: fakeClientsetWithUpdates(&configv1.ClusterVersion{
					ObjectMeta: metav1.ObjectMeta{
						Name:       "default",
						Generation: 2,
					},
					Spec: configv1.ClusterVersionSpec{
						ClusterID: configv1.ClusterID(id),
						DesiredUpdate: &configv1.Update{
							Payload: "payload/image:v4.0.2",
						},
					},
				}),
			},
			wantSync: []configv1.Update{
				{Payload: "payload/image:v4.0.2", Version: ""},
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
						DesiredUpdate: &configv1.Update{
							Payload: "payload/image:v4.0.2",
						},
					},
					Status: configv1.ClusterVersionStatus{
						History: []configv1.UpdateHistory{
							{State: configv1.CompletedUpdate, Version: "4.0.1", Payload: "payload/image:v4.0.1", StartedTime: defaultStartedTime, CompletionTime: &defaultCompletionTime},
						},
						Desired:    configv1.Update{Payload: "payload/image:v4.0.1", Version: "4.0.1"},
						Generation: 2,
						Conditions: []configv1.ClusterOperatorStatusCondition{
							{Type: configv1.OperatorAvailable, Status: configv1.ConditionTrue, Message: "Done applying 4.0.1"},
							{Type: configv1.OperatorFailing, Status: configv1.ConditionFalse},
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
				Reconciling: true,
				Completed:   1,
				Actual:      configv1.Update{Payload: "payload/image:v4.0.1", Version: "4.0.1"},
			},
			optr: Operator{
				releaseImage: "payload/image:v4.0.1",
				namespace:    "test",
				name:         "default",
				client: fakeClientsetWithUpdates(&configv1.ClusterVersion{
					ObjectMeta: metav1.ObjectMeta{
						Name:       "default",
						Generation: 2,
					},
					Spec: configv1.ClusterVersionSpec{
						ClusterID: configv1.ClusterID(id),
						Upstream:  configv1.URL("http://localhost:8080/graph"),
						DesiredUpdate: &configv1.Update{
							Version: "4.0.4",
						},
					},
					Status: configv1.ClusterVersionStatus{
						AvailableUpdates: []configv1.Update{
							{Version: "4.0.2", Payload: "test/image:1"},
							{Version: "4.0.3", Payload: "test/image:2"},
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
						History: []configv1.UpdateHistory{
							{State: configv1.CompletedUpdate, Version: "4.0.1", Payload: "payload/image:v4.0.1", StartedTime: defaultStartedTime, CompletionTime: &defaultCompletionTime},
						},
						Desired: configv1.Update{Payload: "payload/image:v4.0.1", Version: "4.0.1"},
						AvailableUpdates: []configv1.Update{
							{Version: "4.0.2", Payload: "test/image:1"},
							{Version: "4.0.3", Payload: "test/image:2"},
						},
						Generation: 2,
						Conditions: []configv1.ClusterOperatorStatusCondition{
							{Type: ClusterVersionInvalid, Status: configv1.ConditionTrue, Reason: "InvalidClusterVersion", Message: "The cluster version is invalid: spec.desiredUpdate.version: Invalid value: \"4.0.4\": when payload is empty the update must be a previous version or an available update"},
							{Type: configv1.OperatorAvailable, Status: configv1.ConditionTrue, Message: "Done applying 4.0.1"},
							{Type: configv1.OperatorFailing, Status: configv1.ConditionFalse},
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
				Reconciling: true,
				Completed:   1,
				Actual:      configv1.Update{Payload: "payload/image:v4.0.1", Version: "4.0.1"},
			},
			optr: Operator{
				releaseImage: "payload/image:v4.0.1",
				namespace:    "test",
				name:         "default",
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
						AvailableUpdates: []configv1.Update{
							{Version: "4.0.2", Payload: "test/image:1"},
							{Version: "4.0.3", Payload: "test/image:2"},
							{Version: "4.0.3", Payload: "test/image:3"},
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
						Generation: 2,
						History: []configv1.UpdateHistory{
							{State: configv1.CompletedUpdate, Version: "4.0.1", Payload: "payload/image:v4.0.1", StartedTime: defaultStartedTime, CompletionTime: &defaultCompletionTime},
						},
						Desired: configv1.Update{Payload: "payload/image:v4.0.1", Version: "4.0.1"},
						AvailableUpdates: []configv1.Update{
							{Version: "4.0.2", Payload: "test/image:1"},
							{Version: "4.0.3", Payload: "test/image:2"},
							{Version: "4.0.3", Payload: "test/image:3"},
						},
						Conditions: []configv1.ClusterOperatorStatusCondition{
							{Type: ClusterVersionInvalid, Status: configv1.ConditionTrue, Reason: "InvalidClusterVersion", Message: "The cluster version is invalid: spec.desiredUpdate.version: Invalid value: \"4.0.3\": there are multiple possible payloads for this version, specify the exact payload"},
							{Type: configv1.OperatorAvailable, Status: configv1.ConditionTrue, Message: "Done applying 4.0.1"},
							{Type: configv1.OperatorFailing, Status: configv1.ConditionFalse},
							{Type: configv1.OperatorProgressing, Status: configv1.ConditionFalse, Reason: "InvalidClusterVersion", Message: "Stopped at 4.0.1: the cluster version is invalid"},
							{Type: configv1.RetrievedUpdates, Status: configv1.ConditionFalse},
						},
					},
				})
			},
		},
		{
			name: "payload hash matches content hash, act as reconcile, no need to apply",
			syncStatus: &SyncWorkerStatus{
				Reconciling: true,
				Completed:   1,
				VersionHash: "y_Kc5IQiIyU=",
				Actual:      configv1.Update{Payload: "payload/image:v4.0.1", Version: "0.0.1-abc"},
			},
			optr: Operator{
				releaseImage:   "payload/image:v4.0.1",
				releaseVersion: "0.0.1-abc",
				namespace:      "test",
				name:           "default",
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
								// loads the version from the payload on disk
								{
									State:          configv1.CompletedUpdate,
									Payload:        "payload/image:v4.0.1",
									Version:        "0.0.1-abc",
									CompletionTime: &defaultCompletionTime,
								},
							},
							Desired: configv1.Update{
								Version: "0.0.1-abc",
								Payload: "payload/image:v4.0.1",
							},
							VersionHash: "y_Kc5IQiIyU=",
							Generation:  2,
							Conditions: []configv1.ClusterOperatorStatusCondition{
								{Type: configv1.OperatorAvailable, Status: configv1.ConditionTrue, Message: "Done applying 0.0.1-abc"},
								{Type: configv1.OperatorFailing, Status: configv1.ConditionFalse},
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
			name: "payload hash does not match content hash, act as reconcile, no need to apply",
			syncStatus: &SyncWorkerStatus{
				Reconciling: true,
				Completed:   1,
				VersionHash: "y_Kc5IQiIyU=",
				Actual:      configv1.Update{Payload: "payload/image:v4.0.1", Version: "0.0.1-abc"},
			},
			optr: Operator{
				releaseImage:   "payload/image:v4.0.1",
				releaseVersion: "0.0.1-abc",
				namespace:      "test",
				name:           "default",
				client: fake.NewSimpleClientset(
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
								// loads the version from the payload on disk
								{
									State:          configv1.CompletedUpdate,
									Payload:        "payload/image:v4.0.1",
									Version:        "0.0.1-abc",
									CompletionTime: &defaultCompletionTime,
								},
							},
							Desired: configv1.Update{
								Version: "0.0.1-abc",
								Payload: "payload/image:v4.0.1",
							},
							VersionHash: "unknown_hash",
							Generation:  2,
							Conditions: []configv1.ClusterOperatorStatusCondition{
								{Type: configv1.OperatorAvailable, Status: configv1.ConditionTrue, Message: "Done applying 0.0.1-abc"},
								{Type: configv1.OperatorFailing, Status: configv1.ConditionFalse},
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
								Payload:        "payload/image:v4.0.1",
								Version:        "0.0.1-abc",
								CompletionTime: &defaultCompletionTime,
								StartedTime:    metav1.Time{time.Unix(0, 0)},
							},
						},
						Desired:     configv1.Update{Version: "0.0.1-abc", Payload: "payload/image:v4.0.1"},
						Generation:  2,
						VersionHash: "y_Kc5IQiIyU=",
						Conditions: []configv1.ClusterOperatorStatusCondition{
							{Type: configv1.OperatorAvailable, Status: configv1.ConditionTrue, Message: "Done applying 0.0.1-abc"},
							{Type: configv1.OperatorFailing, Status: configv1.ConditionFalse},
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
				Actual:      configv1.Update{Payload: "payload/image:v4.0.1", Version: "0.0.1-abc"},
			},
			optr: Operator{
				releaseImage: "payload/image:v4.0.1",
				namespace:    "test",
				name:         "default",
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
							{State: configv1.CompletedUpdate, Version: "0.0.1-abc", Payload: "payload/image:v4.0.1", StartedTime: defaultStartedTime, CompletionTime: &defaultCompletionTime},
						},
						Desired: configv1.Update{
							Version: "0.0.1-abc", Payload: "payload/image:v4.0.1",
						},
						VersionHash: "",
						Conditions: []configv1.ClusterOperatorStatusCondition{
							{Type: ClusterVersionInvalid, Status: configv1.ConditionTrue, Reason: "InvalidClusterVersion", Message: "The cluster version is invalid:\n* spec.upstream: Invalid value: \"#%GG\": must be a valid URL or empty\n* spec.clusterID: Invalid value: \"not-valid-cluster-id\": must be an RFC4122-variant UUID\n"},
							{Type: configv1.OperatorAvailable, Status: configv1.ConditionTrue, Message: "Done applying 0.0.1-abc"},
							{Type: configv1.OperatorFailing, Status: configv1.ConditionFalse},
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
				Actual: configv1.Update{Payload: "payload/image:v4.0.1", Version: "0.0.1-abc"},
			},
			optr: Operator{
				releaseImage: "payload/image:v4.0.1",
				namespace:    "test",
				name:         "default",
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
							{Payload: "payload/image:v4.0.1", StartedTime: defaultStartedTime},
						},
						Desired:     configv1.Update{Payload: "payload/image:v4.0.1"},
						VersionHash: "",
						Conditions: []configv1.ClusterOperatorStatusCondition{
							{Type: configv1.OperatorAvailable, Status: configv1.ConditionFalse},
							{Type: configv1.OperatorFailing, Status: configv1.ConditionFalse},
							{Type: configv1.OperatorProgressing, Status: configv1.ConditionFalse, Reason: "InvalidClusterVersion", Message: "Stopped at payload/image:v4.0.1: the cluster version is invalid"},
							{Type: configv1.RetrievedUpdates, Status: configv1.ConditionFalse},
							{Type: ClusterVersionInvalid, Status: configv1.ConditionTrue, Reason: "InvalidClusterVersion", Message: "The cluster version is invalid:\n* spec.upstream: Invalid value: \"#%GG\": must be a valid URL or empty\n* spec.clusterID: Invalid value: \"not-valid-cluster-id\": must be an RFC4122-variant UUID\n"},
						},
					},
				}),
			},
			wantSync: []configv1.Update{
				// set by the operator
				{Payload: "payload/image:v4.0.1", Version: ""},
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
							{State: configv1.PartialUpdate, Payload: "payload/image:v4.0.1", Version: "0.0.1-abc", StartedTime: defaultStartedTime},
						},
						Desired:     configv1.Update{Payload: "payload/image:v4.0.1", Version: "0.0.1-abc"},
						VersionHash: "",
						Conditions: []configv1.ClusterOperatorStatusCondition{
							{Type: configv1.OperatorAvailable, Status: configv1.ConditionFalse},
							{Type: configv1.OperatorFailing, Status: configv1.ConditionFalse},
							{Type: configv1.OperatorProgressing, Status: configv1.ConditionTrue, Reason: "InvalidClusterVersion", Message: "Reconciling 0.0.1-abc: the cluster version is invalid"},
							{Type: configv1.RetrievedUpdates, Status: configv1.ConditionFalse},
							{Type: ClusterVersionInvalid, Status: configv1.ConditionTrue, Reason: "InvalidClusterVersion", Message: "The cluster version is invalid:\n* spec.upstream: Invalid value: \"#%GG\": must be a valid URL or empty\n* spec.clusterID: Invalid value: \"not-valid-cluster-id\": must be an RFC4122-variant UUID\n"},
						},
					},
				})
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			optr := &tt.optr
			if tt.init != nil {
				tt.init(optr)
			}
			optr.cvLister = &clientCVLister{client: optr.client}
			optr.clusterOperatorLister = &clientCOLister{client: optr.client}
			if optr.configSync == nil {
				expectStatus := tt.syncStatus
				if expectStatus == nil {
					expectStatus = &SyncWorkerStatus{}
				}
				optr.configSync = &fakeSyncRecorder{Returns: expectStatus}
			}

			err := optr.sync(optr.queueKey())
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
		optr        Operator
		wantErr     func(*testing.T, error)
		wantUpdates *availableUpdates
	}{
		{
			name: "when version is missing, do nothing (other loops should create it)",
			optr: Operator{
				releaseVersion: "4.0.1",
				releaseImage:   "payload/image:v4.0.1",
				namespace:      "test",
				name:           "default",
				client:         fake.NewSimpleClientset(),
			},
		},
		{
			name: "report an error condition when no upstream is set",
			handler: func(w http.ResponseWriter, req *http.Request) {
				http.Error(w, "bad things", http.StatusInternalServerError)
			},
			optr: Operator{
				releaseVersion: "",
				releaseImage:   "payload/image:v4.0.1",
				namespace:      "test",
				name:           "default",
				client: fake.NewSimpleClientset(
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
								{Payload: "payload/image:v4.0.1"},
							},
						},
					},
				),
			},
			wantUpdates: &availableUpdates{
				Upstream: "",
				Channel:  "fast",
				Condition: configv1.ClusterOperatorStatusCondition{
					Type:    configv1.RetrievedUpdates,
					Status:  configv1.ConditionFalse,
					Reason:  "NoUpstream",
					Message: "No upstream server has been set to retrieve updates.",
				},
			},
		},
		{
			name: "report an error condition when no current version is set",
			handler: func(w http.ResponseWriter, req *http.Request) {
				http.Error(w, "bad things", http.StatusInternalServerError)
			},
			optr: Operator{
				defaultUpstreamServer: "http://localhost:8080/graph",
				releaseVersion:        "",
				releaseImage:          "payload/image:v4.0.1",
				namespace:             "test",
				name:                  "default",
				client: fake.NewSimpleClientset(
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
								{Payload: "payload/image:v4.0.1"},
							},
						},
					},
				),
			},
			wantUpdates: &availableUpdates{
				Upstream: "",
				Channel:  "fast",
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
			optr: Operator{
				defaultUpstreamServer: "http://localhost:8080/graph",
				releaseVersion:        "4.0.1",
				releaseImage:          "payload/image:v4.0.1",
				namespace:             "test",
				name:                  "default",
				client: fake.NewSimpleClientset(
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
								{Payload: "payload/image:v4.0.1"},
							},
							Conditions: []configv1.ClusterOperatorStatusCondition{
								{Type: configv1.OperatorAvailable, Status: configv1.ConditionTrue, Message: "Done applying payload/image:v4.0.1"},
								{Type: configv1.OperatorFailing, Status: configv1.ConditionFalse},
								{Type: configv1.OperatorProgressing, Status: configv1.ConditionFalse},
							},
						},
					},
				),
			},
			wantUpdates: &availableUpdates{
				Upstream: "",
				Channel:  "fast",
				Condition: configv1.ClusterOperatorStatusCondition{
					Type:    configv1.RetrievedUpdates,
					Status:  configv1.ConditionFalse,
					Reason:  "RemoteFailed",
					Message: "Unable to retrieve available updates: unexpected HTTP status: 500 Internal Server Error",
				},
			},
		},
		{
			name: "set available updates and clear error state when success and empty",
			handler: func(w http.ResponseWriter, req *http.Request) {
				fmt.Fprintf(w, "{}")
			},
			optr: Operator{
				defaultUpstreamServer: "http://localhost:8080/graph",
				releaseVersion:        "4.0.1",
				releaseImage:          "payload/image:v4.0.1",
				namespace:             "test",
				name:                  "default",
				client: fake.NewSimpleClientset(
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
								{Payload: "payload/image:v4.0.1"},
							},
							Conditions: []configv1.ClusterOperatorStatusCondition{
								{Type: configv1.OperatorAvailable, Status: configv1.ConditionTrue, Message: "Done applying payload/image:v4.0.1"},
								{Type: configv1.OperatorFailing, Status: configv1.ConditionFalse},
								{Type: configv1.OperatorProgressing, Status: configv1.ConditionFalse},
								{Type: configv1.RetrievedUpdates, Status: configv1.ConditionFalse, Reason: "RemoteFailed", Message: "Unable to retrieve available updates: unexpected HTTP status: 500 Internal Server Error"},
							},
						},
					},
				),
			},
			wantUpdates: &availableUpdates{
				Upstream: "",
				Channel:  "fast",
				Condition: configv1.ClusterOperatorStatusCondition{
					Type:   configv1.RetrievedUpdates,
					Status: configv1.ConditionTrue,
				},
			},
		},
		{
			name: "calculate available update edges",
			handler: func(w http.ResponseWriter, req *http.Request) {
				fmt.Fprintf(w, `
				{
					"nodes": [
						{"version":"4.0.1",            "payload": "payload/image:v4.0.1"},
						{"version":"4.0.2-prerelease", "payload": "some.other.registry/payload/image:v4.0.2"},
						{"version":"4.0.2",            "payload": "payload/image:v4.0.2"}
					],
					"edges": [
						[0, 1],
						[0, 2],
						[1, 2]
					]
				}
				`)
			},
			optr: Operator{
				defaultUpstreamServer: "http://localhost:8080/graph",
				releaseVersion:        "4.0.1",
				releaseImage:          "payload/image:v4.0.1",
				namespace:             "test",
				name:                  "default",
				client: fake.NewSimpleClientset(
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
								{Payload: "payload/image:v4.0.1"},
							},
							Conditions: []configv1.ClusterOperatorStatusCondition{
								{Type: configv1.OperatorAvailable, Status: configv1.ConditionTrue, Message: "Done applying payload/image:v4.0.1"},
								{Type: configv1.OperatorFailing, Status: configv1.ConditionFalse},
								{Type: configv1.OperatorProgressing, Status: configv1.ConditionFalse},
								{Type: configv1.RetrievedUpdates, Status: configv1.ConditionFalse, Reason: "RemoteFailed", Message: "Unable to retrieve available updates: unexpected HTTP status: 500 Internal Server Error"},
							},
						},
					},
				),
			},
			wantUpdates: &availableUpdates{
				Upstream: "",
				Channel:  "fast",
				Updates: []configv1.Update{
					{Version: "4.0.2-prerelease", Payload: "some.other.registry/payload/image:v4.0.2"},
					{Version: "4.0.2", Payload: "payload/image:v4.0.2"},
				},
				Condition: configv1.ClusterOperatorStatusCondition{
					Type:   configv1.RetrievedUpdates,
					Status: configv1.ConditionTrue,
				},
			},
		},
		{
			name: "if last check time was too recent, do nothing",
			handler: func(w http.ResponseWriter, req *http.Request) {
				http.Error(w, "bad things", http.StatusInternalServerError)
			},
			optr: Operator{
				defaultUpstreamServer:      "http://localhost:8080/graph",
				minimumUpdateCheckInterval: 1 * time.Minute,
				availableUpdates: &availableUpdates{
					Upstream: "http://localhost:8080/graph",
					Channel:  "fast",
					At:       time.Now(),
				},

				releaseVersion: "4.0.1",
				releaseImage:   "payload/image:v4.0.1",
				namespace:      "test",
				name:           "default",
				client: fake.NewSimpleClientset(
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
								{Payload: "payload/image:v4.0.1"},
							},
							Generation: 2,
							Conditions: []configv1.ClusterOperatorStatusCondition{
								{Type: configv1.OperatorAvailable, Status: configv1.ConditionTrue, Message: "Done applying payload/image:v4.0.1"},
								{Type: configv1.OperatorFailing, Status: configv1.ConditionFalse},
								{Type: configv1.OperatorProgressing, Status: configv1.ConditionFalse},
								{Type: configv1.RetrievedUpdates, Status: configv1.ConditionFalse, Reason: "RemoteFailed", Message: "Unable to retrieve available updates: unexpected HTTP status: 500 Internal Server Error"},
							},
						},
					},
				),
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			optr := tt.optr
			optr.queue = workqueue.NewRateLimitingQueue(workqueue.DefaultControllerRateLimiter())
			optr.clusterOperatorLister = &clientCOLister{client: optr.client}
			optr.cvLister = &clientCVLister{client: optr.client}

			if tt.handler != nil {
				s := httptest.NewServer(http.HandlerFunc(tt.handler))
				defer s.Close()
				if optr.defaultUpstreamServer == "http://localhost:8080/graph" {
					optr.defaultUpstreamServer = s.URL
				}
				if optr.availableUpdates != nil && optr.availableUpdates.Upstream == "http://localhost:8080/graph" {
					optr.availableUpdates.Upstream = s.URL
				}
			}
			old := optr.availableUpdates

			err := optr.availableUpdatesSync(optr.queueKey())
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
				if optr.availableUpdates.Upstream == optr.defaultUpstreamServer && len(optr.defaultUpstreamServer) > 0 {
					optr.availableUpdates.Upstream = "<default>"
				}
				optr.availableUpdates.Condition.LastTransitionTime = metav1.Time{}
				optr.availableUpdates.At = time.Time{}
			}
			if !reflect.DeepEqual(optr.availableUpdates, tt.wantUpdates) {
				t.Fatalf("unexpected: %s", diff.ObjectReflectDiff(tt.wantUpdates, optr.availableUpdates))
			}
			if (optr.queue.Len() > 0) != (optr.availableUpdates != nil) {
				t.Fatalf("unexpected queue")
			}
		})
	}
}

func expectGet(t *testing.T, a ktesting.Action, resource, namespace, name string) {
	t.Helper()
	if "get" != a.GetVerb() {
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

func expectCreate(t *testing.T, a ktesting.Action, resource, namespace string, obj interface{}) {
	t.Helper()
	expectMutation(t, a, "create", resource, "", namespace, obj)
}

func expectUpdate(t *testing.T, a ktesting.Action, resource, namespace string, obj interface{}) {
	t.Helper()
	expectMutation(t, a, "update", resource, "", namespace, obj)
}

func expectUpdateStatus(t *testing.T, a ktesting.Action, resource, namespace string, obj interface{}) {
	t.Helper()
	expectMutation(t, a, "update", resource, "status", namespace, obj)
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
		expect, actual := obj.(runtime.Object).DeepCopyObject(), at.GetObject()
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
			t.Fatalf("unexpected object: %s", diff.ObjectReflectDiff(expect, actual))
		}
	default:
		t.Fatalf("unknown verb %T", a)
	}
}

func fakeClientsetWithUpdates(obj *configv1.ClusterVersion) *fake.Clientset {
	client := &fake.Clientset{}
	client.AddReactor("*", "*", func(action ktesting.Action) (handled bool, ret runtime.Object, err error) {
		if action.GetVerb() == "get" {
			return true, obj.DeepCopy(), nil
		}
		if action.GetVerb() == "update" && action.GetSubresource() == "status" {
			update := action.(ktesting.UpdateAction).GetObject().(*configv1.ClusterVersion)
			obj.Status = update.Status
			rv, _ := strconv.Atoi(update.ResourceVersion)
			obj.ResourceVersion = strconv.Itoa(rv + 1)
			glog.V(5).Infof("updated object to %#v", obj)
			return true, obj.DeepCopy(), nil
		}
		return false, nil, fmt.Errorf("unrecognized")
	})
	return client
}

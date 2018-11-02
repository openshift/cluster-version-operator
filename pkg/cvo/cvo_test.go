package cvo

import (
	"fmt"
	"io/ioutil"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/diff"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/rest"
	ktesting "k8s.io/client-go/testing"

	"github.com/google/uuid"
	cvv1 "github.com/openshift/cluster-version-operator/pkg/apis/config.openshift.io/v1"
	osv1 "github.com/openshift/cluster-version-operator/pkg/apis/operatorstatus.openshift.io/v1"
	clientset "github.com/openshift/cluster-version-operator/pkg/generated/clientset/versioned"
	"github.com/openshift/cluster-version-operator/pkg/generated/clientset/versioned/fake"
	oslistersv1 "github.com/openshift/cluster-version-operator/pkg/generated/listers/operatorstatus.openshift.io/v1"
	apiextv1beta1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1beta1"
	apiextclientv1 "k8s.io/apiextensions-apiserver/pkg/client/clientset/clientset/typed/apiextensions/v1beta1"
)

type clientCVLister struct {
	client clientset.Interface
}

func (c *clientCVLister) Get(name string) (*cvv1.ClusterVersion, error) {
	return c.client.Config().ClusterVersions().Get(name, metav1.GetOptions{})
}
func (c *clientCVLister) List(selector labels.Selector) (ret []*cvv1.ClusterVersion, err error) {
	list, err := c.client.Config().ClusterVersions().List(metav1.ListOptions{LabelSelector: selector.String()})
	if err != nil {
		return nil, err
	}
	var items []*cvv1.ClusterVersion
	for i := range list.Items {
		items = append(items, &list.Items[i])
	}
	return items, nil
}

type clientCOLister struct {
	client clientset.Interface
	ns     string
}

func (c *clientCOLister) ClusterOperators(namespace string) oslistersv1.ClusterOperatorNamespaceLister {
	copied := *c
	copied.ns = namespace
	return &copied
}
func (c *clientCOLister) Get(name string) (*osv1.ClusterOperator, error) {
	return c.client.Operatorstatus().ClusterOperators(c.ns).Get(name, metav1.GetOptions{})
}
func (c *clientCOLister) List(selector labels.Selector) (ret []*osv1.ClusterOperator, err error) {
	list, err := c.client.Operatorstatus().ClusterOperators(c.ns).List(metav1.ListOptions{LabelSelector: selector.String()})
	if err != nil {
		return nil, err
	}
	var items []*osv1.ClusterOperator
	for i := range list.Items {
		items = append(items, &list.Items[i])
	}
	return items, nil
}

type cvLister struct {
	Err   error
	Items []*cvv1.ClusterVersion
}

func (r *cvLister) List(selector labels.Selector) (ret []*cvv1.ClusterVersion, err error) {
	return r.Items, r.Err
}
func (r *cvLister) Get(name string) (*cvv1.ClusterVersion, error) {
	for _, s := range r.Items {
		if s.Name == name {
			return s, nil
		}
	}
	return nil, errors.NewNotFound(schema.GroupResource{}, name)
}

type coLister struct {
	Err   error
	Items []*osv1.ClusterOperator
}

func (r *coLister) List(selector labels.Selector) (ret []*osv1.ClusterOperator, err error) {
	return r.Items, r.Err
}
func (r *coLister) ClusterOperators(namespace string) oslistersv1.ClusterOperatorNamespaceLister {
	return &nsClusterOperatorLister{r: r, ns: namespace}
}

type nsClusterOperatorLister struct {
	r  *coLister
	ns string
}

func (r *nsClusterOperatorLister) List(selector labels.Selector) (ret []*osv1.ClusterOperator, err error) {
	return r.r.Items, r.r.Err
}
func (r *nsClusterOperatorLister) Get(name string) (*osv1.ClusterOperator, error) {
	for _, s := range r.r.Items {
		if s.Name == name && r.ns == s.Namespace {
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
	content1 := map[string]interface{}{
		"manifests": map[string]interface{}{},
		"release-manifests": map[string]interface{}{
			"image-references": `
			{
				"kind": "ImageStream",
				"apiVersion": "image.openshift.io/v1",
				"metadata": {
					"name": "0.0.1-abc"
				}
			}
			`,
		},
	}

	tests := []struct {
		name        string
		key         string
		content     map[string]interface{}
		optr        Operator
		want        bool
		errFn       func(*testing.T, error)
		wantActions func(*testing.T, *Operator)
	}{
		{
			name:    "create version and status",
			content: content1,
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
				if len(act) != 7 {
					t.Fatalf("unknown actions: %d %#v", len(act), act)
				}
				expectGet(t, act[0], "clusterversions", "", "default")
				expectGet(t, act[1], "clusterversions", "", "default")
				expectCreate(t, act[2], "clusterversions", "", &cvv1.ClusterVersion{
					ObjectMeta: metav1.ObjectMeta{
						Name: "default",
					},
					Spec: cvv1.ClusterVersionSpec{
						Channel: "fast",
					},
				})
				expectGet(t, act[3], "clusterversions", "", "default")
				expectUpdateStatus(t, act[4], "clusterversions", "", &cvv1.ClusterVersion{
					ObjectMeta: metav1.ObjectMeta{
						Name: "default",
					},
					Spec: cvv1.ClusterVersionSpec{
						Channel: "fast",
					},
					Status: cvv1.ClusterVersionStatus{
						Current: cvv1.Update{
							Version: "4.0.1",
							Payload: "payload/image:v4.0.1",
						},
						VersionHash: "",
						Conditions: []osv1.ClusterOperatorStatusCondition{
							{Type: osv1.OperatorAvailable, Status: osv1.ConditionFalse},
							{Type: osv1.OperatorProgressing, Status: osv1.ConditionTrue, Message: "Working towards 4.0.1"},
							{Type: osv1.OperatorFailing, Status: osv1.ConditionFalse},
						},
					},
				})
			},
		},
		{
			name:    "status no-op",
			content: content1,
			optr: Operator{
				releaseImage: "payload/image:v4.0.1",
				namespace:    "test",
				name:         "default",
				client: fake.NewSimpleClientset(
					&cvv1.ClusterVersion{
						ObjectMeta: metav1.ObjectMeta{
							Name: "default",
						},
						Spec: cvv1.ClusterVersionSpec{
							ClusterID: "abcdef01-0000-1111-2222-0123456789abcdef",
							Upstream:  pointerURL("http://localhost:8080/graph"),
							Channel:   "fast",
						},
					},
				),
			},
			wantActions: func(t *testing.T, optr *Operator) {
				f := optr.client.(*fake.Clientset)
				act := f.Actions()
				if len(act) != 5 {
					t.Fatalf("unknown actions: %d %#v", len(act), act)
				}
				expectGet(t, act[0], "clusterversions", "", "default")
				expectGet(t, act[1], "clusterversions", "", "default")
				expectUpdateStatus(t, act[2], "clusterversions", "", &cvv1.ClusterVersion{
					ObjectMeta: metav1.ObjectMeta{
						Name: "default",
					},
					Spec: cvv1.ClusterVersionSpec{
						Upstream: pointerURL("http://localhost:8080/graph"),
						Channel:  "fast",
					},
					Status: cvv1.ClusterVersionStatus{
						Current: cvv1.Update{
							Payload: "payload/image:v4.0.1",
						},
						VersionHash: "",
						Conditions: []osv1.ClusterOperatorStatusCondition{
							{Type: osv1.OperatorAvailable, Status: osv1.ConditionFalse},
							{Type: osv1.OperatorProgressing, Status: osv1.ConditionTrue, Message: "Working towards payload/image:v4.0.1"},
							{Type: osv1.OperatorFailing, Status: osv1.ConditionFalse},
						},
					},
				})
				expectGet(t, act[3], "clusterversions", "", "default")
				expectUpdateStatus(t, act[4], "clusterversions", "", &cvv1.ClusterVersion{
					ObjectMeta: metav1.ObjectMeta{
						Name: "default",
					},
					Spec: cvv1.ClusterVersionSpec{
						Upstream: pointerURL("http://localhost:8080/graph"),
						Channel:  "fast",
					},
					Status: cvv1.ClusterVersionStatus{
						Current: cvv1.Update{
							Payload: "payload/image:v4.0.1",
							// loads the version from the payload on disk
							Version: "0.0.1-abc",
						},
						VersionHash: "y_Kc5IQiIyU=",
						Conditions: []osv1.ClusterOperatorStatusCondition{
							{Type: osv1.OperatorAvailable, Status: osv1.ConditionTrue, Message: "Done applying 0.0.1-abc"},
							{Type: osv1.OperatorProgressing, Status: osv1.ConditionFalse, Message: "Cluster version is 0.0.1-abc"},
							{Type: osv1.OperatorFailing, Status: osv1.ConditionFalse},
						},
					},
				})
			},
		},
		{
			name:    "version is live and was recently synced, do nothing",
			content: content1,
			optr: Operator{
				minimumUpdateCheckInterval: 1 * time.Minute,
				lastSyncAt:                 time.Now(),
				releaseImage:               "payload/image:v4.0.1",
				namespace:                  "test",
				name:                       "default",
				client: fake.NewSimpleClientset(
					&cvv1.ClusterVersion{
						ObjectMeta: metav1.ObjectMeta{
							Name:       "default",
							Generation: 2,
						},
						Spec: cvv1.ClusterVersionSpec{
							ClusterID: "abcdef01-0000-1111-2222-0123456789abcdef",
							Upstream:  pointerURL("http://localhost:8080/graph"),
							Channel:   "fast",
						},
						Status: cvv1.ClusterVersionStatus{
							Generation: 2,
							Conditions: []osv1.ClusterOperatorStatusCondition{
								{Type: osv1.OperatorAvailable, Status: osv1.ConditionTrue},
								{Type: osv1.OperatorProgressing, Status: osv1.ConditionFalse, Message: "Cluster version is 0.0.1-abc"},
								{Type: osv1.OperatorFailing, Status: osv1.ConditionFalse},
							},
						},
					},
				),
			},
			wantActions: func(t *testing.T, optr *Operator) {
				f := optr.client.(*fake.Clientset)
				act := f.Actions()
				if len(act) != 1 {
					t.Fatalf("unknown actions: %d %#v", len(act), act)
				}
				expectGet(t, act[0], "clusterversions", "", "default")
			},
		},
		{
			name:    "payload hash matches content hash, act as reconcile, no need to apply",
			content: content1,
			optr: Operator{
				releaseImage: "payload/image:v4.0.1",
				namespace:    "test",
				name:         "default",
				client: fake.NewSimpleClientset(
					&cvv1.ClusterVersion{
						ObjectMeta: metav1.ObjectMeta{
							Name:       "default",
							Generation: 2,
						},
						Spec: cvv1.ClusterVersionSpec{
							ClusterID: "abcdef01-0000-1111-2222-0123456789abcdef",
							Upstream:  pointerURL("http://localhost:8080/graph"),
							Channel:   "fast",
						},
						Status: cvv1.ClusterVersionStatus{
							Current: cvv1.Update{
								Payload: "payload/image:v4.0.1",
								// loads the version from the payload on disk
								Version: "0.0.1-abc",
							},
							VersionHash: "y_Kc5IQiIyU=",
							Generation:  2,
							Conditions: []osv1.ClusterOperatorStatusCondition{
								{Type: osv1.OperatorAvailable, Status: osv1.ConditionTrue, Message: "Done applying 0.0.1-abc"},
								{Type: osv1.OperatorProgressing, Status: osv1.ConditionFalse, Message: "Cluster version is 0.0.1-abc"},
								{Type: osv1.OperatorFailing, Status: osv1.ConditionFalse},
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
				expectGet(t, act[1], "clusterversions", "", "default")
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			optr := tt.optr
			optr.cvoConfigLister = &clientCVLister{client: optr.client}
			optr.clusterOperatorLister = &clientCOLister{client: optr.client}
			dir, err := ioutil.TempDir("", "cvo-test")
			if err != nil {
				t.Fatal(err)
			}
			defer os.RemoveAll(dir)
			optr.payloadDir = dir
			if err := createContent(dir, tt.content); err != nil {
				t.Fatal(err)
			}
			err = optr.sync(fmt.Sprintf("%s/%s", optr.namespace, optr.name))
			if err != nil && tt.errFn == nil {
				t.Fatalf("Operator.sync() unexpected error: %v", err)
			}
			if tt.errFn != nil {
				tt.errFn(t, err)
			}
			if err != nil {
				return
			}
			if tt.wantActions != nil {
				tt.wantActions(t, &optr)
			}
		})
	}
}

func TestOperator_availableUpdatesSync(t *testing.T) {
	tests := []struct {
		name        string
		key         string
		handler     http.HandlerFunc
		optr        Operator
		want        bool
		errFn       func(*testing.T, error)
		wantActions func(*testing.T, *Operator)
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
			wantActions: func(t *testing.T, optr *Operator) {
				f := optr.client.(*fake.Clientset)
				act := f.Actions()
				if len(act) != 1 {
					t.Fatalf("unknown actions: %d %#v", len(act), act)
				}
				expectGet(t, act[0], "clusterversions", "", "default")
			},
		},
		{
			name: "report an error condition when no current version is set",
			handler: func(w http.ResponseWriter, req *http.Request) {
				http.Error(w, "bad things", http.StatusInternalServerError)
			},
			optr: Operator{
				releaseVersion: "",
				releaseImage:   "payload/image:v4.0.1",
				namespace:      "test",
				name:           "default",
				client: fake.NewSimpleClientset(
					&cvv1.ClusterVersion{
						ObjectMeta: metav1.ObjectMeta{
							Name: "default",
						},
						Spec: cvv1.ClusterVersionSpec{
							ClusterID: cvv1.ClusterID(uuid.Must(uuid.NewRandom()).String()),
							Channel:   "fast",
						},
						Status: cvv1.ClusterVersionStatus{
							Current: cvv1.Update{
								Payload: "payload/image:v4.0.1",
							},
						},
					},
				),
			},
			wantActions: func(t *testing.T, optr *Operator) {
				f := optr.client.(*fake.Clientset)
				act := f.Actions()
				if len(act) != 3 {
					t.Fatalf("unknown actions: %d %#v", len(act), act)
				}
				expectGet(t, act[0], "clusterversions", "", "default")
				expectGet(t, act[1], "clusterversions", "", "default")
				expectUpdateStatus(t, act[2], "clusterversions", "", &cvv1.ClusterVersion{
					ObjectMeta: metav1.ObjectMeta{
						Name: "default",
					},
					Spec: cvv1.ClusterVersionSpec{
						Channel: "fast",
					},
					Status: cvv1.ClusterVersionStatus{
						Current: cvv1.Update{
							Payload: "payload/image:v4.0.1",
						},
						Conditions: []osv1.ClusterOperatorStatusCondition{
							{Type: cvv1.RetrievedUpdates, Status: osv1.ConditionFalse, Reason: "NoCurrentVersion", Message: "The cluster version does not have a semantic version assigned and cannot calculate valid upgrades."},
						},
					},
				})
			},
		},
		{
			name: "report an error condition when the http server reports an error",
			handler: func(w http.ResponseWriter, req *http.Request) {
				http.Error(w, "bad things", http.StatusInternalServerError)
			},
			optr: Operator{
				releaseVersion: "4.0.1",
				releaseImage:   "payload/image:v4.0.1",
				namespace:      "test",
				name:           "default",
				client: fake.NewSimpleClientset(
					&cvv1.ClusterVersion{
						ObjectMeta: metav1.ObjectMeta{
							Name: "default",
						},
						Spec: cvv1.ClusterVersionSpec{
							ClusterID: cvv1.ClusterID(uuid.Must(uuid.NewRandom()).String()),
							Channel:   "fast",
						},
						Status: cvv1.ClusterVersionStatus{
							Current: cvv1.Update{
								Payload: "payload/image:v4.0.1",
							},
							Conditions: []osv1.ClusterOperatorStatusCondition{
								{Type: osv1.OperatorAvailable, Status: osv1.ConditionTrue, Message: "Done applying payload/image:v4.0.1"},
								{Type: osv1.OperatorProgressing, Status: osv1.ConditionFalse},
								{Type: osv1.OperatorFailing, Status: osv1.ConditionFalse},
							},
						},
					},
				),
			},
			wantActions: func(t *testing.T, optr *Operator) {
				f := optr.client.(*fake.Clientset)
				act := f.Actions()
				if len(act) != 3 {
					t.Fatalf("unknown actions: %d %#v", len(act), act)
				}
				expectGet(t, act[0], "clusterversions", "", "default")
				expectGet(t, act[1], "clusterversions", "", "default")
				expectUpdateStatus(t, act[2], "clusterversions", "", &cvv1.ClusterVersion{
					ObjectMeta: metav1.ObjectMeta{
						Name: "default",
					},
					Spec: cvv1.ClusterVersionSpec{
						Channel: "fast",
					},
					Status: cvv1.ClusterVersionStatus{
						Current: cvv1.Update{
							Payload: "payload/image:v4.0.1",
						},
						Conditions: []osv1.ClusterOperatorStatusCondition{
							{Type: osv1.OperatorAvailable, Status: osv1.ConditionTrue, Message: "Done applying payload/image:v4.0.1"},
							{Type: osv1.OperatorProgressing, Status: osv1.ConditionFalse},
							{Type: osv1.OperatorFailing, Status: osv1.ConditionFalse},
							{Type: cvv1.RetrievedUpdates, Status: osv1.ConditionFalse, Reason: "RemoteFailed", Message: "Unable to retrieve available updates: unexpected HTTP status: 500 Internal Server Error"},
						},
					},
				})
			},
		},
		{
			name: "set available updates and clear error state when success and empty",
			handler: func(w http.ResponseWriter, req *http.Request) {
				fmt.Fprintf(w, "{}")
			},
			optr: Operator{
				releaseVersion: "4.0.1",
				releaseImage:   "payload/image:v4.0.1",
				namespace:      "test",
				name:           "default",
				client: fake.NewSimpleClientset(
					&cvv1.ClusterVersion{
						ObjectMeta: metav1.ObjectMeta{
							Name: "default",
						},
						Spec: cvv1.ClusterVersionSpec{
							ClusterID: cvv1.ClusterID(uuid.Must(uuid.NewRandom()).String()),
							Channel:   "fast",
						},
						Status: cvv1.ClusterVersionStatus{
							Current: cvv1.Update{
								Payload: "payload/image:v4.0.1",
							},
							Conditions: []osv1.ClusterOperatorStatusCondition{
								{Type: osv1.OperatorAvailable, Status: osv1.ConditionTrue, Message: "Done applying payload/image:v4.0.1"},
								{Type: osv1.OperatorProgressing, Status: osv1.ConditionFalse},
								{Type: osv1.OperatorFailing, Status: osv1.ConditionFalse},
								{Type: cvv1.RetrievedUpdates, Status: osv1.ConditionFalse, Reason: "RemoteFailed", Message: "Unable to retrieve available updates: unexpected HTTP status: 500 Internal Server Error"},
							},
						},
					},
				),
			},
			wantActions: func(t *testing.T, optr *Operator) {
				f := optr.client.(*fake.Clientset)
				act := f.Actions()
				if len(act) != 3 {
					t.Fatalf("unknown actions: %d %#v", len(act), act)
				}
				expectGet(t, act[0], "clusterversions", "", "default")
				expectGet(t, act[1], "clusterversions", "", "default")
				expectUpdateStatus(t, act[2], "clusterversions", "", &cvv1.ClusterVersion{
					ObjectMeta: metav1.ObjectMeta{
						Name: "default",
					},
					Spec: cvv1.ClusterVersionSpec{
						Channel: "fast",
					},
					Status: cvv1.ClusterVersionStatus{
						Current: cvv1.Update{
							Payload: "payload/image:v4.0.1",
						},
						Conditions: []osv1.ClusterOperatorStatusCondition{
							{Type: osv1.OperatorAvailable, Status: osv1.ConditionTrue, Message: "Done applying payload/image:v4.0.1"},
							{Type: osv1.OperatorProgressing, Status: osv1.ConditionFalse},
							{Type: osv1.OperatorFailing, Status: osv1.ConditionFalse},
							{Type: cvv1.RetrievedUpdates, Status: osv1.ConditionTrue},
						},
					},
				})
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
				releaseVersion: "4.0.1",
				releaseImage:   "payload/image:v4.0.1",
				namespace:      "test",
				name:           "default",
				client: fake.NewSimpleClientset(
					&cvv1.ClusterVersion{
						ObjectMeta: metav1.ObjectMeta{
							Name: "default",
						},
						Spec: cvv1.ClusterVersionSpec{
							ClusterID: cvv1.ClusterID(uuid.Must(uuid.NewRandom()).String()),
							Channel:   "fast",
						},
						Status: cvv1.ClusterVersionStatus{
							Current: cvv1.Update{
								Payload: "payload/image:v4.0.1",
							},
							Conditions: []osv1.ClusterOperatorStatusCondition{
								{Type: osv1.OperatorAvailable, Status: osv1.ConditionTrue, Message: "Done applying payload/image:v4.0.1"},
								{Type: osv1.OperatorProgressing, Status: osv1.ConditionFalse},
								{Type: osv1.OperatorFailing, Status: osv1.ConditionFalse},
								{Type: cvv1.RetrievedUpdates, Status: osv1.ConditionFalse, Reason: "RemoteFailed", Message: "Unable to retrieve available updates: unexpected HTTP status: 500 Internal Server Error"},
							},
						},
					},
				),
			},
			wantActions: func(t *testing.T, optr *Operator) {
				f := optr.client.(*fake.Clientset)
				act := f.Actions()
				if len(act) != 3 {
					t.Fatalf("unknown actions: %d %#v", len(act), act)
				}
				expectGet(t, act[0], "clusterversions", "", "default")
				expectGet(t, act[1], "clusterversions", "", "default")
				expectUpdateStatus(t, act[2], "clusterversions", "", &cvv1.ClusterVersion{
					ObjectMeta: metav1.ObjectMeta{
						Name: "default",
					},
					Spec: cvv1.ClusterVersionSpec{
						Channel: "fast",
					},
					Status: cvv1.ClusterVersionStatus{
						Current: cvv1.Update{
							Payload: "payload/image:v4.0.1",
						},
						AvailableUpdates: []cvv1.Update{
							{Version: "4.0.2-prerelease", Payload: "some.other.registry/payload/image:v4.0.2"},
							{Version: "4.0.2", Payload: "payload/image:v4.0.2"},
						},
						Conditions: []osv1.ClusterOperatorStatusCondition{
							{Type: osv1.OperatorAvailable, Status: osv1.ConditionTrue, Message: "Done applying payload/image:v4.0.1"},
							{Type: osv1.OperatorProgressing, Status: osv1.ConditionFalse},
							{Type: osv1.OperatorFailing, Status: osv1.ConditionFalse},
							{Type: cvv1.RetrievedUpdates, Status: osv1.ConditionTrue},
						},
					},
				})
			},
		},
		{
			name: "if last check time was too recent, do nothing",
			handler: func(w http.ResponseWriter, req *http.Request) {
				http.Error(w, "bad things", http.StatusInternalServerError)
			},
			optr: Operator{
				minimumUpdateCheckInterval: 1 * time.Minute,
				lastRetrieveAt:             time.Now(),

				releaseVersion: "4.0.1",
				releaseImage:   "payload/image:v4.0.1",
				namespace:      "test",
				name:           "default",
				client: fake.NewSimpleClientset(
					&cvv1.ClusterVersion{
						ObjectMeta: metav1.ObjectMeta{
							Name:       "default",
							Generation: 2,
						},
						Spec: cvv1.ClusterVersionSpec{
							ClusterID: cvv1.ClusterID(uuid.Must(uuid.NewRandom()).String()),
							Channel:   "fast",
						},
						Status: cvv1.ClusterVersionStatus{
							Current: cvv1.Update{
								Payload: "payload/image:v4.0.1",
							},
							Generation: 2,
							Conditions: []osv1.ClusterOperatorStatusCondition{
								{Type: osv1.OperatorAvailable, Status: osv1.ConditionTrue, Message: "Done applying payload/image:v4.0.1"},
								{Type: osv1.OperatorProgressing, Status: osv1.ConditionFalse},
								{Type: osv1.OperatorFailing, Status: osv1.ConditionFalse},
								{Type: cvv1.RetrievedUpdates, Status: osv1.ConditionFalse, Reason: "RemoteFailed", Message: "Unable to retrieve available updates: unexpected HTTP status: 500 Internal Server Error"},
							},
						},
					},
				),
			},
			wantActions: func(t *testing.T, optr *Operator) {
				f := optr.client.(*fake.Clientset)
				act := f.Actions()
				if len(act) != 1 {
					t.Fatalf("unknown actions: %d %#v", len(act), act)
				}
				expectGet(t, act[0], "clusterversions", "", "default")
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			optr := tt.optr
			optr.cvoConfigLister = &clientCVLister{client: optr.client}
			optr.clusterOperatorLister = &clientCOLister{client: optr.client}

			if tt.handler != nil {
				s := httptest.NewServer(http.HandlerFunc(tt.handler))
				defer s.Close()
				if len(optr.defaultUpstreamServer) == 0 {
					optr.defaultUpstreamServer = s.URL
				}
			}

			err := optr.availableUpdatesSync(fmt.Sprintf("%s/%s", optr.namespace, optr.name))
			if err != nil && tt.errFn == nil {
				t.Fatalf("Operator.sync() unexpected error: %v", err)
			}
			if tt.errFn != nil {
				tt.errFn(t, err)
			}
			if err != nil {
				return
			}
			if tt.wantActions != nil {
				tt.wantActions(t, &optr)
			}
		})
	}
}

func createContent(baseDir string, content map[string]interface{}) error {
	for k, v := range content {
		switch t := v.(type) {
		case string:
			if err := ioutil.WriteFile(filepath.Join(baseDir, k), []byte(t), 0640); err != nil {
				return err
			}
		case map[string]interface{}:
			dir := filepath.Join(baseDir, k)
			if err := os.Mkdir(dir, 0750); err != nil {
				return err
			}
			if err := createContent(dir, t); err != nil {
				return err
			}
		}
	}
	return nil
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
		// default autogenerated cluster ID
		if in, ok := obj.(*cvv1.ClusterVersion); ok {
			if in.Spec.ClusterID == "" {
				in.Spec.ClusterID = at.GetObject().(*cvv1.ClusterVersion).Spec.ClusterID
			}
		}
		if in, ok := at.GetObject().(*osv1.ClusterOperator); ok {
			for i := range in.Status.Conditions {
				in.Status.Conditions[i].LastTransitionTime.Time = time.Time{}
			}
		}
		if in, ok := at.GetObject().(*cvv1.ClusterVersion); ok {
			for i := range in.Status.Conditions {
				in.Status.Conditions[i].LastTransitionTime.Time = time.Time{}
			}
		}

		e, a := fmt.Sprintf("%s/%s", resource, namespace), fmt.Sprintf("%s/%s", at.GetResource().Resource, at.GetNamespace())
		if e != a {
			t.Fatalf("unexpected action: %#v", at)
		}
		if !reflect.DeepEqual(obj, at.GetObject()) {
			t.Fatalf("unexpected object: %s", diff.ObjectReflectDiff(obj, at.GetObject()))
		}
	default:
		t.Fatalf("unknown verb %T", a)
	}
}

func pointerURL(url string) *cvv1.URL {
	u := cvv1.URL(url)
	return &u
}

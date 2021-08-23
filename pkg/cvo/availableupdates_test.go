package cvo

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
	"time"

	"github.com/google/uuid"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/diff"
	"k8s.io/client-go/tools/record"
	"k8s.io/client-go/util/workqueue"

	configv1 "github.com/openshift/api/config/v1"
	"github.com/openshift/client-go/config/clientset/versioned/fake"
)

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
				client:    fake.NewSimpleClientset(),
			},
		},
		{
			name: "report an error condition when no upstream is set",
			handler: func(w http.ResponseWriter, req *http.Request) {
				http.Error(w, "bad things", http.StatusInternalServerError)
			},
			optr: &Operator{
				release: configv1.Release{
					Image: "image/image:v4.0.1",
				},
				namespace: "test",
				name:      "default",
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
								{Image: "image/image:v4.0.1"},
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
			name: "report an error condition when channel isn't set",
			handler: func(w http.ResponseWriter, req *http.Request) {
				http.Error(w, "bad things", http.StatusInternalServerError)
			},
			optr: &Operator{
				defaultUpstreamServer: "http://localhost:8080/graph",
				release: configv1.Release{
					Version: "v4.0.0",
					Image:   "image/image:v4.0.1",
				},
				namespace: "test",
				name:      "default",
				client: fake.NewSimpleClientset(
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
				Upstream: "",
				Channel:  "",
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
				defaultUpstreamServer: "http://localhost:8080/graph",
				release: configv1.Release{
					Image: "image/image:v4.0.1",
				},
				namespace: "test",
				name:      "default",
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
								{Image: "image/image:v4.0.1"},
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
			optr: &Operator{
				defaultUpstreamServer: "http://localhost:8080/graph",
				release: configv1.Release{
					Version: "4.0.1",
					Image:   "image/image:v4.0.1",
				},
				namespace: "test",
				name:      "default",
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
								{Image: "image/image:v4.0.1"},
							},
							Conditions: []configv1.ClusterOperatorStatusCondition{
								{Type: configv1.OperatorAvailable, Status: configv1.ConditionTrue, Message: "Done applying image/image:v4.0.1"},
								{Type: ClusterStatusFailing, Status: configv1.ConditionFalse},
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
					Reason:  "ResponseFailed",
					Message: "Unable to retrieve available updates: unexpected HTTP status: 500 Internal Server Error",
				},
			},
		},
		{
			name: "set available updates and clear error state when success and empty",
			handler: func(w http.ResponseWriter, req *http.Request) {
				fmt.Fprintf(w, `
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
				defaultUpstreamServer: "http://localhost:8080/graph",
				release: configv1.Release{
					Version: "4.0.1",
					Image:   "image/image:v4.0.1",
				},
				namespace: "test",
				name:      "default",
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
								{Image: "image/image:v4.0.1"},
							},
							Conditions: []configv1.ClusterOperatorStatusCondition{
								{Type: configv1.OperatorAvailable, Status: configv1.ConditionTrue, Message: "Done applying image/image:v4.0.1"},
								{Type: ClusterStatusFailing, Status: configv1.ConditionFalse},
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
				fmt.Fprintf(w, `
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
				defaultUpstreamServer: "http://localhost:8080/graph",
				release: configv1.Release{
					Version: "4.0.1",
					Image:   "image/image:v4.0.1",
				},
				namespace: "test",
				name:      "default",
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
								{Image: "image/image:v4.0.1"},
							},
							Conditions: []configv1.ClusterOperatorStatusCondition{
								{Type: configv1.OperatorAvailable, Status: configv1.ConditionTrue, Message: "Done applying image/image:v4.0.1"},
								{Type: ClusterStatusFailing, Status: configv1.ConditionFalse},
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
				Current:  configv1.Release{Version: "4.0.1", Image: "image/image:v4.0.1"},
				Updates: []configv1.Release{
					{Version: "4.0.2-prerelease", Image: "some.other.registry/image/image:v4.0.2"},
					{Version: "4.0.2", Image: "image/image:v4.0.2"},
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
			optr: &Operator{
				defaultUpstreamServer:      "http://localhost:8080/graph",
				minimumUpdateCheckInterval: 1 * time.Minute,
				availableUpdates: &availableUpdates{
					Upstream:    "http://localhost:8080/graph",
					Channel:     "fast",
					LastAttempt: time.Now(),
				},
				release: configv1.Release{
					Version: "4.0.1",
					Image:   "image/image:v4.0.1",
				},
				namespace: "test",
				name:      "default",
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
								{Image: "image/image:v4.0.1"},
							},
							ObservedGeneration: 2,
							Conditions: []configv1.ClusterOperatorStatusCondition{
								{Type: configv1.OperatorAvailable, Status: configv1.ConditionTrue, Message: "Done applying image/image:v4.0.1"},
								{Type: ClusterStatusFailing, Status: configv1.ConditionFalse},
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
			optr.proxyLister = &clientProxyLister{client: optr.client}
			optr.coLister = &clientCOLister{client: optr.client}
			optr.cvLister = &clientCVLister{client: optr.client}
			optr.cmConfigManagedLister = &cmConfigLister{}
			optr.eventRecorder = record.NewFakeRecorder(100)

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
				if optr.availableUpdates.Upstream == optr.defaultUpstreamServer && len(optr.defaultUpstreamServer) > 0 {
					optr.availableUpdates.Upstream = "<default>"
				}
				optr.availableUpdates.Condition.LastTransitionTime = metav1.Time{}
				optr.availableUpdates.LastAttempt = time.Time{}
				optr.availableUpdates.LastSyncOrConfigChange = time.Time{}
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

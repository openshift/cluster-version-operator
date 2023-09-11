package cvo

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime/schema"

	configv1 "github.com/openshift/api/config/v1"
	"github.com/openshift/cluster-version-operator/pkg/clusterconditions"
	"github.com/openshift/cluster-version-operator/pkg/clusterconditions/always"
	"github.com/openshift/cluster-version-operator/pkg/clusterconditions/mock"
)

type queueStub struct{}

func (q queueStub) Add(interface{}) {}

func (q queueStub) Len() int {
	panic("implement me")
}

func (q queueStub) Get() (interface{}, bool) {
	panic("implement me")
}

func (q queueStub) Done(interface{}) {
	panic("implement me")
}

func (q queueStub) ShutDown() {
	panic("implement me")
}

func (q queueStub) ShutDownWithDrain() {
	panic("implement me")
}

func (q queueStub) ShuttingDown() bool {
	panic("implement me")
}

func (q queueStub) AddAfter(interface{}, time.Duration) {
	panic("implement me")
}

func (q queueStub) AddRateLimited(interface{}) {
	panic("implement me")
}

func (q queueStub) Forget(interface{}) {
	panic("implement me")
}

func (q queueStub) NumRequeues(interface{}) int {
	panic("implement me")
}

// notFoundProxyLister is a stub for ProxyLister
type notFoundProxyLister struct{}

func (n notFoundProxyLister) List(labels.Selector) ([]*configv1.Proxy, error) {
	return nil, nil
}

func (n notFoundProxyLister) Get(name string) (*configv1.Proxy, error) {
	return nil, errors.NewNotFound(schema.GroupResource{Group: configv1.GroupName, Resource: "proxy"}, name)
}

type notFoundConfigMapLister struct{}

func (n notFoundConfigMapLister) List(labels.Selector) ([]*corev1.ConfigMap, error) {
	return nil, nil
}

func (n notFoundConfigMapLister) Get(name string) (*corev1.ConfigMap, error) {
	return nil, errors.NewNotFound(schema.GroupResource{Group: "", Resource: "configmap"}, name)
}

// osusWithSingleConditionalEdge helper returns:
//  1. mock osus server that serves a simple conditional path between two versions.
//  2. mock condition that always evaluates to match
//  3. expected []ConditionalUpdate data after evaluation of the data served by mock osus server
//     (assuming the mock condition (2) was used)
//  4. current version of the cluster that would issue the request to the mock osus server
func osusWithSingleConditionalEdge() (*httptest.Server, clusterconditions.Condition, []configv1.ConditionalUpdate, string) {
	from := "4.5.5"
	to := "4.5.6"
	osus := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, `{
  "nodes": [{"version": "%s", "payload": "payload/%s"}, {"version": "%s", "payload": "payload/%s"}],
  "conditionalEdges": [
    {
      "edges": [{"from": "%s", "to": "%s"}],
      "risks": [
        {
          "url": "https://example.com/%s",
          "name": "FourFiveSix",
          "message": "Four Five Five is just fine",
          "matchingRules": [{"type": "PromQL", "promql": { "promql": "this is a query"}}]
        }
      ]
    }
  ]
}
`, from, from, to, to, from, to, to)
	}))

	updates := []configv1.ConditionalUpdate{
		{
			Release: configv1.Release{Version: to, Image: "payload/" + to},
			Risks: []configv1.ConditionalUpdateRisk{
				{
					URL:     "https://example.com/" + to,
					Name:    "FourFiveSix",
					Message: "Four Five Five is just fine",
					MatchingRules: []configv1.ClusterCondition{
						{
							Type:   "PromQL",
							PromQL: &configv1.PromQLClusterCondition{PromQL: "this is a query"},
						},
					},
				},
			},
			Conditions: []metav1.Condition{
				{
					Type:    "Recommended",
					Status:  metav1.ConditionFalse,
					Reason:  "FourFiveSix",
					Message: "Four Five Five is just fine https://example.com/" + to,
				},
			},
		},
	}
	mockPromql := &mock.Mock{
		ValidQueue: []error{nil},
		MatchQueue: []mock.MatchResult{{Match: true, Error: nil}},
	}

	return osus, mockPromql, updates, from
}

func newOperator(url, version string, promqlMock clusterconditions.Condition) (*availableUpdates, *Operator) {
	currentRelease := configv1.Release{Version: version, Image: "payload/" + version}
	registry := clusterconditions.NewConditionRegistry()
	registry.Register("Always", &always.Always{})
	registry.Register("PromQL", promqlMock)
	operator := &Operator{
		defaultUpstreamServer: url,
		architecture:          "amd64",
		proxyLister:           notFoundProxyLister{},
		cmConfigManagedLister: notFoundConfigMapLister{},
		conditionRegistry:     registry,
		queue:                 queueStub{},
		release:               currentRelease,
	}
	availableUpdates := &availableUpdates{
		Architecture: "amd64",
		Current:      configv1.Release{Version: version, Image: "payload/" + version},
	}
	return availableUpdates, operator
}

var cvFixture = &configv1.ClusterVersion{
	Spec: configv1.ClusterVersionSpec{
		ClusterID: "897f0a22-33ca-4106-a2c4-29b75250255a",
		Channel:   "channel",
	},
}

var availableUpdatesCmpOpts = []cmp.Option{
	cmpopts.IgnoreTypes(time.Time{}),
	cmpopts.IgnoreInterfaces(struct {
		clusterconditions.ConditionRegistry
	}{}),
}

func TestSyncAvailableUpdates(t *testing.T) {
	fakeOsus, mockPromql, expectedConditionalUpdates, version := osusWithSingleConditionalEdge()
	defer fakeOsus.Close()
	expectedAvailableUpdates, optr := newOperator(fakeOsus.URL, version, mockPromql)
	expectedAvailableUpdates.ConditionalUpdates = expectedConditionalUpdates
	expectedAvailableUpdates.Channel = cvFixture.Spec.Channel
	expectedAvailableUpdates.Condition = configv1.ClusterOperatorStatusCondition{
		Type:   configv1.RetrievedUpdates,
		Status: configv1.ConditionTrue,
	}

	err := optr.syncAvailableUpdates(context.Background(), cvFixture)

	if err != nil {
		t.Fatalf("syncAvailableUpdates() unexpected error: %v", err)
	}
	if diff := cmp.Diff(expectedAvailableUpdates, optr.availableUpdates, availableUpdatesCmpOpts...); diff != "" {
		t.Fatalf("available updates differ from expected:\n%s", diff)
	}
}

func TestSyncAvailableUpdates_ConditionalUpdateRecommendedConditions(t *testing.T) {
	testCases := []struct {
		name                string
		modifyOriginalState func(condition *metav1.Condition)
		expectTimeChange    bool
	}{
		{
			name:                "lastTransitionTime is not updated when nothing changes",
			modifyOriginalState: func(condition *metav1.Condition) {},
		},
		{
			name: "lastTransitionTime is not updated when changed but status is identical",
			modifyOriginalState: func(condition *metav1.Condition) {
				condition.Reason = "OldReason"
				condition.Message = "This message should be changed to something else"
			},
		},
		{
			name: "lastTransitionTime is updated when status changes",
			modifyOriginalState: func(condition *metav1.Condition) {
				condition.Status = metav1.ConditionUnknown
			},
			expectTimeChange: true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			fakeOsus, mockPromql, conditionalUpdates, version := osusWithSingleConditionalEdge()
			defer fakeOsus.Close()
			availableUpdates, optr := newOperator(fakeOsus.URL, version, mockPromql)
			optr.availableUpdates = availableUpdates
			optr.availableUpdates.ConditionalUpdates = conditionalUpdates
			expectedConditions := []metav1.Condition{{}}
			conditionalUpdates[0].Conditions[0].DeepCopyInto(&expectedConditions[0])
			tc.modifyOriginalState(&optr.availableUpdates.ConditionalUpdates[0].Conditions[0])

			err := optr.syncAvailableUpdates(context.Background(), cvFixture)

			if err != nil {
				t.Fatalf("syncAvailableUpdates() unexpected error: %v", err)
			}
			if optr.availableUpdates == nil || len(optr.availableUpdates.ConditionalUpdates) == 0 {
				t.Fatalf("syncAvailableUpdates() did not properly set available updates")
			}
			if diff := cmp.Diff(expectedConditions, optr.availableUpdates.ConditionalUpdates[0].Conditions, cmpopts.IgnoreTypes(time.Time{})); diff != "" {
				t.Errorf("conditions on conditional updates differ from expected:\n%s", diff)
			}
			timeBefore := expectedConditions[0].LastTransitionTime
			timeAfter := optr.availableUpdates.ConditionalUpdates[0].Conditions[0].LastTransitionTime

			if tc.expectTimeChange && timeBefore == timeAfter {
				t.Errorf("lastTransitionTime was not updated as expected: before=%s after=%s", timeBefore, timeAfter)
			}
			if !tc.expectTimeChange && timeBefore != timeAfter {
				t.Errorf("lastTransitionTime was updated but was not expected to: before=%s after=%s", timeBefore, timeAfter)
			}
		})
	}
}

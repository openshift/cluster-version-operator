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

type fakeConditionRegistry struct{}

func (f fakeConditionRegistry) Register(string, clusterconditions.Condition) {
	panic("implement me")
}

func (f fakeConditionRegistry) PruneInvalid(ctx context.Context, matchingRules []configv1.ClusterCondition) ([]configv1.ClusterCondition, error) {
	return matchingRules, nil
}

type clusterConditionRuleType string

const (
	ruleTypeAlways clusterConditionRuleType = "Always"
	ruleTypePromQL clusterConditionRuleType = "PromQL"
)

type clusterConditionRuleFakePromql string

const (
	evalToYes   clusterConditionRuleFakePromql = "YES"
	evalToNo    clusterConditionRuleFakePromql = "NO"
	errorOnEval clusterConditionRuleFakePromql = "ERROR"
	skip        clusterConditionRuleFakePromql = "SKIP"
)

func (f fakeConditionRegistry) Match(ctx context.Context, matchingRules []configv1.ClusterCondition) (bool, error) {
	for _, rule := range matchingRules {
		switch clusterConditionRuleType(rule.Type) {
		case ruleTypeAlways:
			return true, nil
		case ruleTypePromQL:
			switch clusterConditionRuleFakePromql(rule.PromQL.PromQL) {
			case evalToYes:
				return true, nil
			case evalToNo:
				return false, nil
			case errorOnEval:
				return false, fmt.Errorf("ERROR")
			case skip:
				continue
			default:
				panic("This fake works only with YES, NO, ERROR, and SKIP")
			}
		default:
			panic("This fake works only with Always and PromQL risks")
		}
	}
	return false, nil
}

func osusWithSingleConditionalEdge(from, to string, ruleType clusterConditionRuleType, promql clusterConditionRuleFakePromql) ([]configv1.ConditionalUpdate, *httptest.Server) {
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
          "matchingRules": [{"type": "%s", "promql": { "promql": "%s"}}]
        }
      ]
    }
  ]
}
`, from, from, to, to, from, to, to, ruleType, promql)
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
							PromQL: &configv1.PromQLClusterCondition{PromQL: string(evalToYes)},
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
	return updates, osus
}

func newOperator(url, version string) (*availableUpdates, *Operator) {
	currentRelease := configv1.Release{Version: version, Image: "payload/" + version}
	operator := &Operator{
		defaultUpstreamServer: url,
		architecture:          "amd64",
		proxyLister:           notFoundProxyLister{},
		cmConfigManagedLister: notFoundConfigMapLister{},
		conditionRegistry:     fakeConditionRegistry{},
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
	expectedConditionalUpdates, fakeOsus := osusWithSingleConditionalEdge("4.5.5", "4.5.6", ruleTypePromQL, evalToYes)
	defer fakeOsus.Close()
	expectedAvailableUpdates, optr := newOperator(fakeOsus.URL, "4.5.5")
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
			conditionalUpdates, fakeOsus := osusWithSingleConditionalEdge("4.5.5", "4.5.6", ruleTypePromQL, evalToYes)
			defer fakeOsus.Close()
			availableUpdates, optr := newOperator(fakeOsus.URL, "4.5.5")
			availableUpdates.ConditionalUpdates = conditionalUpdates
			availableUpdates.Channel = cvFixture.Spec.Channel
			availableUpdates.Condition = configv1.ClusterOperatorStatusCondition{
				Type:   configv1.RetrievedUpdates,
				Status: configv1.ConditionTrue,
			}
			optr.availableUpdates = availableUpdates
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

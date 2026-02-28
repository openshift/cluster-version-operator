package cvo

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"runtime"
	"testing"
	"time"

	"github.com/blang/semver/v4"
	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"

	corev1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime/schema"
	utilerrors "k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/client-go/util/workqueue"

	configv1 "github.com/openshift/api/config/v1"

	"github.com/openshift/cluster-version-operator/pkg/alert"
	"github.com/openshift/cluster-version-operator/pkg/clusterconditions"
	"github.com/openshift/cluster-version-operator/pkg/clusterconditions/always"
	"github.com/openshift/cluster-version-operator/pkg/clusterconditions/mock"
	"github.com/openshift/cluster-version-operator/pkg/featuregates"
)

// notFoundProxyLister is a stub for ProxyLister
type notFoundProxyLister struct{}

func (n notFoundProxyLister) List(labels.Selector) ([]*configv1.Proxy, error) {
	return nil, nil
}

func (n notFoundProxyLister) Get(name string) (*configv1.Proxy, error) {
	return nil, k8serrors.NewNotFound(schema.GroupResource{Group: configv1.GroupName, Resource: "proxy"}, name)
}

type notFoundConfigMapLister struct{}

func (n notFoundConfigMapLister) List(labels.Selector) ([]*corev1.ConfigMap, error) {
	return nil, nil
}

func (n notFoundConfigMapLister) Get(name string) (*corev1.ConfigMap, error) {
	return nil, k8serrors.NewNotFound(schema.GroupResource{Group: "", Resource: "configmap"}, name)
}

type release struct {
	version string
	image   string
}

type conditionalEdgeTestData struct {
	from release
	to   release
}

// newConditionalEdgeTestData constructs test data for a conditional edge between two versions.
func newConditionalEdgeTestData(from, to string) conditionalEdgeTestData {
	return conditionalEdgeTestData{
		from: release{
			version: from,
			image:   fmt.Sprintf("payload/%s", from),
		},
		to: release{
			version: to,
			image:   fmt.Sprintf("payload/%s", to),
		},
	}
}

// defaultConditionalEdgeTestData returns test data for a conditional edge from 4.5.5 to 4.5.6.
func defaultConditionalEdgeTestData() conditionalEdgeTestData {
	return newConditionalEdgeTestData("4.5.5", "4.5.6")
}

// expectedConditionalUpdatesFor returns expected ConditionalUpdate data after evaluation
// (assuming the returned mock condition is used) and the mock PromQL condition checker itself.
func expectedConditionalUpdatesFor(data conditionalEdgeTestData) ([]configv1.ConditionalUpdate, *mock.Mock) {
	return []configv1.ConditionalUpdate{
			{
				Release: configv1.Release{Version: data.to.version, Image: data.to.image},
				Risks: []configv1.ConditionalUpdateRisk{
					{
						URL:     "https://example.com/" + data.to.version,
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
						Type:               "Recommended",
						Status:             metav1.ConditionFalse,
						Reason:             "FourFiveSix",
						Message:            "Four Five Five is just fine https://example.com/" + data.to.version,
						LastTransitionTime: metav1.Now(),
					},
				},
			},
		}, &mock.Mock{
			ValidQueue: []error{nil},
			MatchQueue: []mock.MatchResult{{Match: true, Error: nil}},
		}
}

// newMockOSUSServer returns an OSUS server and query parameters used in the last call to the OSUS server.
func newMockOSUSServer(data conditionalEdgeTestData) (*httptest.Server, *url.Values) {
	var params url.Values
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		params = r.URL.Query()
		_, _ = fmt.Fprintf(w, `{
  "nodes": [{"version": "%s", "payload": "%s"}, {"version": "%s", "payload": "%s"}],
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
`, data.from.version, data.from.image, data.to.version, data.to.image, data.from.version,
			data.to.version, data.to.version)
	})), &params
}

type testFixture struct {
	// Mock osus server
	osus *httptest.Server
	// Query parameters used in the last call to the osus server
	lastQueryParams *url.Values

	// Expected updates after evaluation of the mockCondition
	expectedConditionalUpdates []configv1.ConditionalUpdate
	mockCondition              clusterconditions.Condition

	currentRelease release
}

// osusWithSingleConditionalEdge helper returns:
//  1. mock osus server that serves a simple conditional path between two versions.
//  2. mock condition that always evaluates to match
//  3. expected []ConditionalUpdate data after evaluation of the data served by mock osus server
//     (assuming the mock condition (2) was used)
//  4. current version of the cluster that would issue the request to the mock osus server
//  5. current image of the cluster
//  6. query parameters used in the last call to the osus server
func osusWithSingleConditionalEdge(data conditionalEdgeTestData) testFixture {
	server, lastQueryParams := newMockOSUSServer(data)
	expectedUpdates, mockPromql := expectedConditionalUpdatesFor(data)
	return testFixture{
		osus:                       server,
		lastQueryParams:            lastQueryParams,
		expectedConditionalUpdates: expectedUpdates,
		mockCondition:              mockPromql,
		currentRelease: release{
			version: data.from.version,
			image:   data.from.image,
		},
	}
}

func newOperator(url string, cluster release, promqlMock clusterconditions.Condition, arch string) (*availableUpdates, *Operator) {
	var currentReleaseArch configv1.ClusterVersionArchitecture
	if arch == string(configv1.ClusterVersionArchitectureMulti) {
		currentReleaseArch = configv1.ClusterVersionArchitectureMulti
	}
	currentRelease := configv1.Release{Version: cluster.version, Image: cluster.image, Architecture: currentReleaseArch}

	registry := clusterconditions.NewConditionRegistry()
	registry.Register("Always", &always.Always{})
	registry.Register("PromQL", promqlMock)
	operator := &Operator{
		updateService:         url,
		architecture:          arch,
		proxyLister:           notFoundProxyLister{},
		cmConfigManagedLister: notFoundConfigMapLister{},
		conditionRegistry:     registry,
		queue:                 workqueue.NewTypedRateLimitingQueue[any](workqueue.DefaultTypedControllerRateLimiter[any]()),
		release:               currentRelease,
	}
	availableUpdates := &availableUpdates{
		Current: configv1.Release{Version: cluster.version, Image: cluster.image},
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
	cmpopts.IgnoreFields(availableUpdates{}, "ShouldReconcileAcceptRisks"),
	cmpopts.IgnoreTypes(time.Time{}),
	cmpopts.IgnoreInterfaces(struct {
		clusterconditions.ConditionRegistry
		AlertGetter
	}{}),
}

func TestSyncAvailableUpdates(t *testing.T) {
	fixture := osusWithSingleConditionalEdge(defaultConditionalEdgeTestData())
	defer fixture.osus.Close()
	expectedAvailableUpdates, optr := newOperator(
		fixture.osus.URL,
		fixture.currentRelease,
		fixture.mockCondition,
		runtime.GOARCH,
	)
	expectedAvailableUpdates.UpdateService = fixture.osus.URL
	expectedAvailableUpdates.ConditionalUpdates = fixture.expectedConditionalUpdates
	expectedAvailableUpdates.Channel = cvFixture.Spec.Channel
	expectedAvailableUpdates.Architecture = runtime.GOARCH
	expectedAvailableUpdates.Condition = configv1.ClusterOperatorStatusCondition{
		Type:   configv1.RetrievedUpdates,
		Status: configv1.ConditionTrue,
	}
	expectedAvailableUpdates.RiskConditions = map[string][]metav1.Condition{"FourFiveSix": {{Type: "Applies", Status: "True", Reason: "Match"}}}

	optr.enabledCVOFeatureGates = featuregates.DefaultCvoGates("version")
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
		modifyOriginalState func(optr *Operator)
		modifyCV            func(cv *configv1.ClusterVersion, update configv1.ConditionalUpdate)
		expectTimeChange    bool
	}{
		{
			name:                "lastTransitionTime is not updated when nothing changes",
			modifyOriginalState: func(optr *Operator) {},
			modifyCV:            func(cv *configv1.ClusterVersion, update configv1.ConditionalUpdate) {},
		},
		{
			name: "lastTransitionTime is not updated when changed but status is identical",
			modifyOriginalState: func(optr *Operator) {
				optr.availableUpdates.ConditionalUpdates[0].Conditions[0].Reason = "OldReason"
				optr.availableUpdates.ConditionalUpdates[0].Conditions[0].Message = "This message should be changed to something else"
			},
			modifyCV: func(cv *configv1.ClusterVersion, update configv1.ConditionalUpdate) {},
		},
		{
			name: "lastTransitionTime is updated when status changes",
			modifyOriginalState: func(optr *Operator) {
				optr.availableUpdates.ConditionalUpdates[0].Conditions[0].Status = metav1.ConditionUnknown
			},
			modifyCV:         func(cv *configv1.ClusterVersion, update configv1.ConditionalUpdate) {},
			expectTimeChange: true,
		},
		{
			name: "lastTransitionTime is updated on first fetch with empty CV status",
			modifyOriginalState: func(optr *Operator) {
				optr.availableUpdates = nil
			},
			modifyCV:         func(cv *configv1.ClusterVersion, update configv1.ConditionalUpdate) {},
			expectTimeChange: true,
		},
		{
			name: "lastTransitionTime is updated on first fetch when condition status in CV status differs from fetched status",
			modifyOriginalState: func(optr *Operator) {
				optr.availableUpdates = nil
			},
			modifyCV: func(cv *configv1.ClusterVersion, update configv1.ConditionalUpdate) {
				cv.Status.ConditionalUpdates = []configv1.ConditionalUpdate{*update.DeepCopy()}
				cv.Status.ConditionalUpdates[0].Conditions[0].Status = metav1.ConditionUnknown
			},
			expectTimeChange: true,
		},
		{
			name: "lastTransitionTime is not updated on first fetch when condition status in CV status matches fetched status",
			modifyOriginalState: func(optr *Operator) {
				optr.availableUpdates = nil
			},
			modifyCV: func(cv *configv1.ClusterVersion, update configv1.ConditionalUpdate) {
				cv.Status.ConditionalUpdates = []configv1.ConditionalUpdate{*update.DeepCopy()}
			},
			expectTimeChange: false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			fixture := osusWithSingleConditionalEdge(defaultConditionalEdgeTestData())
			defer fixture.osus.Close()
			availableUpdates, optr := newOperator(
				fixture.osus.URL,
				fixture.currentRelease,
				fixture.mockCondition,
				runtime.GOARCH,
			)
			availableUpdates.Architecture = runtime.GOARCH
			optr.availableUpdates = availableUpdates
			optr.availableUpdates.ConditionalUpdates = fixture.expectedConditionalUpdates
			expectedConditions := []metav1.Condition{{}}
			fixture.expectedConditionalUpdates[0].Conditions[0].DeepCopyInto(&expectedConditions[0])
			cv := cvFixture.DeepCopy()
			tc.modifyOriginalState(optr)
			tc.modifyCV(cv, fixture.expectedConditionalUpdates[0])

			optr.enabledCVOFeatureGates = featuregates.DefaultCvoGates("version")
			err := optr.syncAvailableUpdates(context.Background(), cv)

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

func TestEvaluateConditionalUpdate(t *testing.T) {
	testcases := []struct {
		name                       string
		risks                      []configv1.ConditionalUpdateRisk
		acceptRisks                sets.Set[string]
		shouldReconcileAcceptRisks func() bool
		riskConditions             map[string][]metav1.Condition
		expected                   metav1.Condition
	}{
		{
			name: "no risks",
			expected: metav1.Condition{
				Type:    "Recommended",
				Status:  metav1.ConditionTrue,
				Reason:  recommendedReasonRisksNotExposed,
				Message: "The update is recommended, because none of the conditional update risks apply to this cluster.",
			},
		},
		{
			name:           "one risk that does not match",
			risks:          []configv1.ConditionalUpdateRisk{{Name: "ShouldNotApply"}},
			riskConditions: map[string][]metav1.Condition{"ShouldNotApply": {{Type: "Applies", Status: metav1.ConditionFalse, Reason: "NotMatch"}}},
			expected: metav1.Condition{
				Type:    "Recommended",
				Status:  metav1.ConditionTrue,
				Reason:  recommendedReasonRisksNotExposed,
				Message: "The update is recommended, because none of the conditional update risks apply to this cluster.",
			},
		},
		{
			name: "one risk that matches",
			risks: []configv1.ConditionalUpdateRisk{
				{
					URL:     "https://match.es",
					Name:    "RiskThatApplies",
					Message: "This is a risk!",
				},
			},
			riskConditions: map[string][]metav1.Condition{"RiskThatApplies": {{Type: "Applies", Status: metav1.ConditionTrue, Reason: "Match"}}},
			expected: metav1.Condition{
				Type:    "Recommended",
				Status:  metav1.ConditionFalse,
				Reason:  "RiskThatApplies",
				Message: "This is a risk! https://match.es",
			},
		},
		{
			name:                       "one risk that matches and is accepted",
			risks:                      []configv1.ConditionalUpdateRisk{{Name: "RiskThatApplies"}},
			acceptRisks:                sets.New[string]("RiskThatApplies", "not-important"),
			shouldReconcileAcceptRisks: func() bool { return true },
			riskConditions:             map[string][]metav1.Condition{"RiskThatApplies": {{Type: "Applies", Status: metav1.ConditionTrue, Reason: "Match"}}},
			expected: metav1.Condition{
				Type:    "Recommended",
				Status:  metav1.ConditionTrue,
				Reason:  "AllExposedRisksAccepted",
				Message: "The update is recommended, because either risk does not apply to this cluster or it is accepted by cluster admins.",
			},
		},
		{
			name: "matching risk with name that cannot be used as a condition reason",
			risks: []configv1.ConditionalUpdateRisk{
				{
					URL:           "https://match.es",
					Name:          "RISK-THAT-APPLIES", // Condition reasons are CamelCase names, must not contain dashes
					Message:       "This is a risk!",
					MatchingRules: []configv1.ClusterCondition{{Type: "PromQL"}},
				},
			},
			riskConditions: map[string][]metav1.Condition{"RISK-THAT-APPLIES": {{Type: "Applies", Status: metav1.ConditionTrue, Reason: "Match"}}},
			expected: metav1.Condition{
				Type:    "Recommended",
				Status:  metav1.ConditionFalse,
				Reason:  recommendedReasonExposed,
				Message: "This is a risk! https://match.es",
			},
		},
		{
			name: "two risks that match",
			risks: []configv1.ConditionalUpdateRisk{
				{
					URL:     "https://match.es",
					Name:    "RiskThatApplies",
					Message: "This is a risk!",
				},
				{
					URL:     "https://doesnotmat.ch",
					Name:    "ShouldNotApply",
					Message: "ShouldNotApply",
				},
				{
					URL:     "https://match.es/too",
					Name:    "RiskThatAppliesToo",
					Message: "This is a risk too!",
				},
			},
			riskConditions: map[string][]metav1.Condition{"RiskThatApplies": {{Type: "Applies", Status: metav1.ConditionTrue, Reason: "Match"}},
				"ShouldNotApply":     {{Type: "Applies", Status: metav1.ConditionFalse, Reason: "NotMatch"}},
				"RiskThatAppliesToo": {{Type: "Applies", Status: metav1.ConditionTrue, Reason: "Match"}}},
			expected: metav1.Condition{
				Type:    "Recommended",
				Status:  metav1.ConditionFalse,
				Reason:  recommendedReasonMultiple,
				Message: "This is a risk! https://match.es\n\nThis is a risk too! https://match.es/too",
			},
		},
		{
			name: "first risk matches, second fails to evaluate",
			risks: []configv1.ConditionalUpdateRisk{
				{
					URL:     "https://match.es",
					Name:    "RiskThatApplies",
					Message: "This is a risk!",
				},
				{
					URL:     "https://whokno.ws",
					Name:    "RiskThatFailsToEvaluate",
					Message: "This is a risk too!",
				},
			},
			riskConditions: map[string][]metav1.Condition{"RiskThatApplies": {{Type: "Applies", Status: metav1.ConditionTrue, Reason: "Match"}},
				"RiskThatFailsToEvaluate": {{Type: "Applies", Status: metav1.ConditionUnknown, Reason: "EvaluationFailed", Message: "Could not evaluate exposure to update risk RiskThatFailsToEvaluate (ERROR)\n  RiskThatFailsToEvaluate description: This is a risk too!\n  RiskThatFailsToEvaluate URL: https://whokno.ws"}}},
			expected: metav1.Condition{
				Type:   "Recommended",
				Status: metav1.ConditionFalse,
				Reason: recommendedReasonMultiple,
				Message: "This is a risk! https://match.es\n\n" +
					"Could not evaluate exposure to update risk RiskThatFailsToEvaluate (ERROR)\n" +
					"  RiskThatFailsToEvaluate description: This is a risk too!\n" +
					"  RiskThatFailsToEvaluate URL: https://whokno.ws",
			},
		},
		{
			name: "one risk that fails to evaluate",
			risks: []configv1.ConditionalUpdateRisk{
				{
					URL:     "https://whokno.ws",
					Name:    "RiskThatFailsToEvaluate",
					Message: "This is a risk!",
				},
			},
			riskConditions: map[string][]metav1.Condition{"RiskThatFailsToEvaluate": {{Type: "Applies", Status: metav1.ConditionUnknown, Reason: "EvaluationFailed", Message: "Could not evaluate exposure to update risk RiskThatFailsToEvaluate (ERROR)\n  RiskThatFailsToEvaluate description: This is a risk!\n  RiskThatFailsToEvaluate URL: https://whokno.ws"}}},
			expected: metav1.Condition{
				Type:   "Recommended",
				Status: metav1.ConditionUnknown,
				Reason: recommendedReasonEvaluationFailed,
				Message: "Could not evaluate exposure to update risk RiskThatFailsToEvaluate (ERROR)\n" +
					"  RiskThatFailsToEvaluate description: This is a risk!\n" +
					"  RiskThatFailsToEvaluate URL: https://whokno.ws",
			},
		},
	}
	for _, tc := range testcases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.shouldReconcileAcceptRisks == nil {
				tc.shouldReconcileAcceptRisks = func() bool {
					return false
				}
			}
			if tc.riskConditions == nil {
				tc.riskConditions = map[string][]metav1.Condition{}
			}
			actual := evaluateConditionalUpdate(tc.risks, tc.acceptRisks, tc.shouldReconcileAcceptRisks, tc.riskConditions)
			if diff := cmp.Diff(tc.expected, actual); diff != "" {
				t.Errorf("actual condition differs from expected:\n%s", diff)
			}
		})
	}
}

func TestSyncAvailableUpdatesDesiredUpdate(t *testing.T) {
	data := newConditionalEdgeTestData("4.5.5", "4.5.6")

	// used only by relevant test cases where the evaluation of conditional updates is expected
	expectedConditionalUpdates, _ := expectedConditionalUpdatesFor(data)

	type args struct {
		operatorArchitecture string
		desiredUpdate        *configv1.Update
	}
	type expected struct {
		arch           string
		queryParamArch string

		updates            []configv1.Release
		conditionalUpdates []configv1.ConditionalUpdate
	}
	tests := []struct {
		name                   string
		args                   args
		expected               expected
		expectedRiskConditions map[string][]metav1.Condition
	}{
		// -------------------------------- Valid set desiredUpdate field combinations --------------------------------
		// Some combinations, such as all fields being set, are omitted due to them causing API validation errors;
		// as such, they are not possible and are not explicitly tested here.
		//
		// ---------------- Cases where the operator is multi arch
		{
			name: "operator is multi, image is specified, version is specified, architecture is not specified",
			args: args{
				operatorArchitecture: "Multi",
				desiredUpdate: &configv1.Update{
					Version: data.to.version,
					Image:   data.to.image,
				},
			},
			expected: expected{
				arch:               "Multi",
				conditionalUpdates: expectedConditionalUpdates,
				queryParamArch:     "multi",
			},
			expectedRiskConditions: map[string][]metav1.Condition{"FourFiveSix": {{Type: "Applies", Status: "True", Reason: "Match"}}},
		},
		{
			name: "operator is multi, image is specified, version is not specified, architecture is not specified",
			args: args{
				operatorArchitecture: "Multi",
				desiredUpdate: &configv1.Update{
					Image: data.to.image,
				},
			},
			expected: expected{
				arch:               "Multi",
				conditionalUpdates: expectedConditionalUpdates,
				queryParamArch:     "multi",
			},
			expectedRiskConditions: map[string][]metav1.Condition{"FourFiveSix": {{Type: "Applies", Status: "True", Reason: "Match"}}},
		},
		{
			name: "operator is multi, image is not specified, version is specified, architecture is specified",
			args: args{
				operatorArchitecture: "Multi",
				desiredUpdate: &configv1.Update{
					Architecture: "Multi",
					Version:      data.to.version,
				},
			},
			expected: expected{
				arch:               "Multi",
				conditionalUpdates: expectedConditionalUpdates,
				queryParamArch:     "multi",
			},
			expectedRiskConditions: map[string][]metav1.Condition{"FourFiveSix": {{Type: "Applies", Status: "True", Reason: "Match"}}},
		},
		{
			name: "operator is multi, image is not specified, version is specified, architecture is not specified",
			args: args{
				operatorArchitecture: "Multi",
				desiredUpdate: &configv1.Update{
					Version: data.to.version,
				},
			},
			expected: expected{
				arch:               "Multi",
				conditionalUpdates: expectedConditionalUpdates,
				queryParamArch:     "multi",
			},
			expectedRiskConditions: map[string][]metav1.Condition{"FourFiveSix": {{Type: "Applies", Status: "True", Reason: "Match"}}},
		},
		// ---------------- Cases where the operator is single arch
		{
			name: "operator is not multi, image is specified, version is specified, architecture is not specified",
			args: args{
				operatorArchitecture: runtime.GOARCH,
				desiredUpdate: &configv1.Update{
					Version: data.to.version,
					Image:   data.to.image,
				},
			},
			expected: expected{
				arch:               runtime.GOARCH,
				conditionalUpdates: expectedConditionalUpdates,
				queryParamArch:     runtime.GOARCH,
			},
			expectedRiskConditions: map[string][]metav1.Condition{"FourFiveSix": {{Type: "Applies", Status: "True", Reason: "Match"}}},
		},
		{
			name: "operator is not multi, image is specified, version is not specified, architecture is not specified",
			args: args{
				operatorArchitecture: runtime.GOARCH,
				desiredUpdate: &configv1.Update{
					Image: data.to.image,
				},
			},
			expected: expected{
				arch:               runtime.GOARCH,
				conditionalUpdates: expectedConditionalUpdates,
				queryParamArch:     runtime.GOARCH,
			},
			expectedRiskConditions: map[string][]metav1.Condition{"FourFiveSix": {{Type: "Applies", Status: "True", Reason: "Match"}}},
		},
		{
			name: "operator is not multi, image is not specified, version is specified, architecture is specified - migration to multi arch issued",
			args: args{
				operatorArchitecture: runtime.GOARCH,
				desiredUpdate: &configv1.Update{
					// Migration to multi-arch is issued
					Architecture: "Multi",
					Version:      data.from.version,
				},
			},
			expected: expected{
				arch: "Multi",
				// Migrating from single to multi architecture.
				// Only valid update for required heterogeneous graph is heterogeneous version of current version.
				//
				// Note: Testing utilises an OSUS with a single conditional edge and not individual graphs for
				// individual architectures. To bypass the need for a more extensive testing logic, which would contain
				// different release images for the same versions, there is a present logic to test for the query parameters.
				// If the actual provided parameters are correct, we can act as if the graph references another images.
				//
				// However, it will not exercise any possible "my.image == update.image" conditions.
				updates:        []configv1.Release{{Version: data.from.version, Image: data.from.image}},
				queryParamArch: "multi",
			},
		},
		{
			name: "operator is not multi, image is not specified, version is specified, architecture is specified - migration && update",
			args: args{
				operatorArchitecture: runtime.GOARCH,
				desiredUpdate: &configv1.Update{
					// Migration in combination with an update to a new version is issued
					Architecture: "Multi",
					Version:      data.to.version,
				},
			},
			expected: expected{
				arch: "Multi",
				// The `to` version is not present in the available updates as only migration is available
				updates:        []configv1.Release{{Version: data.from.version, Image: data.from.image}},
				queryParamArch: "multi",
			},
		},
		{
			name: "operator is not multi, image is not specified, version is specified, architecture is not specified",
			args: args{
				operatorArchitecture: runtime.GOARCH,
				desiredUpdate: &configv1.Update{
					Version: data.to.version,
				},
			},
			expected: expected{
				arch:               runtime.GOARCH,
				conditionalUpdates: expectedConditionalUpdates,
				queryParamArch:     runtime.GOARCH,
			},
			expectedRiskConditions: map[string][]metav1.Condition{"FourFiveSix": {{Type: "Applies", Status: "True", Reason: "Match"}}},
		},
		// -------------------------------- Desired Update Is NOT set --------------------------------
		// ---------------- The operator is multi arch
		{
			name: "operator is multi, desired update is not specified",
			args: args{
				operatorArchitecture: "Multi",
				desiredUpdate:        nil,
			},
			expected: expected{
				arch:               "Multi",
				conditionalUpdates: expectedConditionalUpdates,
				queryParamArch:     "multi",
			},
			expectedRiskConditions: map[string][]metav1.Condition{"FourFiveSix": {{Type: "Applies", Status: "True", Reason: "Match"}}},
		},
		// ---------------- The operator is single arch
		{
			name: "operator is not multi, desired update is not specified",
			args: args{
				operatorArchitecture: runtime.GOARCH,
				desiredUpdate:        nil,
			},
			expected: expected{
				arch:               runtime.GOARCH,
				conditionalUpdates: expectedConditionalUpdates,
				queryParamArch:     runtime.GOARCH,
			},
			expectedRiskConditions: map[string][]metav1.Condition{"FourFiveSix": {{Type: "Applies", Status: "True", Reason: "Match"}}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fixture := osusWithSingleConditionalEdge(data)
			defer fixture.osus.Close()

			expectedAvailableUpdates, optr := newOperator(
				fixture.osus.URL,
				fixture.currentRelease,
				fixture.mockCondition,
				tt.args.operatorArchitecture,
			)
			expectedAvailableUpdates.Architecture = tt.expected.arch
			expectedAvailableUpdates.Updates = tt.expected.updates
			expectedAvailableUpdates.ConditionalUpdates = tt.expected.conditionalUpdates
			expectedAvailableUpdates.UpdateService = fixture.osus.URL
			expectedAvailableUpdates.Channel = cvFixture.Spec.Channel
			expectedAvailableUpdates.Condition = configv1.ClusterOperatorStatusCondition{
				Type:   configv1.RetrievedUpdates,
				Status: configv1.ConditionTrue,
			}
			expectedAvailableUpdates.RiskConditions = tt.expectedRiskConditions

			expectedQueryParams := url.Values{
				"arch":    {tt.expected.queryParamArch},
				"id":      {string(cvFixture.Spec.ClusterID)},
				"channel": {cvFixture.Spec.Channel},
				"version": {fixture.currentRelease.version},
			}

			cv := cvFixture.DeepCopy()
			cv.Spec.DesiredUpdate = tt.args.desiredUpdate
			optr.enabledCVOFeatureGates = featuregates.DefaultCvoGates("version")
			if err := optr.syncAvailableUpdates(context.Background(), cv); err != nil {
				t.Fatalf("syncAvailableUpdates() unexpected error: %v", err)
			}

			if diff := cmp.Diff(&expectedQueryParams, fixture.lastQueryParams); diff != "" {
				t.Fatalf("actual query parameters differ from expected:\n%s", diff)
			}

			if diff := cmp.Diff(expectedAvailableUpdates, optr.availableUpdates, availableUpdatesCmpOpts...); diff != "" {
				t.Fatalf("available updates differ from expected:\n%s", diff)
			}
		})
	}
}

func Test_sanityCheck(t *testing.T) {
	tests := []struct {
		name       string
		updates    []configv1.ConditionalUpdate
		alertRisks []configv1.ConditionalUpdateRisk
		expected   error
	}{
		{
			name: "good",
			updates: []configv1.ConditionalUpdate{
				{Risks: []configv1.ConditionalUpdateRisk{{Name: "riskA"}}},
				{Risks: []configv1.ConditionalUpdateRisk{{Name: "riskB"}}},
			},
			alertRisks: []configv1.ConditionalUpdateRisk{{Name: "SomeAlert"}},
		},
		{
			name: "invalid risk name",
			updates: []configv1.ConditionalUpdate{
				{Risks: []configv1.ConditionalUpdateRisk{{Name: "riskA"}, {Name: "", URL: "some"}}},
			},
			expected: utilerrors.NewAggregate([]error{fmt.Errorf("found invalid name on risk {[] some   []}")}),
		},
		{
			name: "bad in one update",
			updates: []configv1.ConditionalUpdate{
				{Risks: []configv1.ConditionalUpdateRisk{{Name: "riskA"}, {Name: "riskA", URL: "some"}}},
			},
			expected: utilerrors.NewAggregate([]error{fmt.Errorf("found collision on risk riskA: {[]  riskA  []} and {[] some riskA  []}")}),
		},
		{
			name: "bad in two updates",
			updates: []configv1.ConditionalUpdate{
				{Risks: []configv1.ConditionalUpdateRisk{{Name: "riskA"}}},
				{Risks: []configv1.ConditionalUpdateRisk{{Name: "riskA", URL: "some"}}},
			},
			expected: utilerrors.NewAggregate([]error{fmt.Errorf("found collision on risk riskA: {[]  riskA  []} and {[] some riskA  []}")}),
		},
		{
			name: "alert risk and conditional update risk conflict",
			updates: []configv1.ConditionalUpdate{
				{Risks: []configv1.ConditionalUpdateRisk{{Name: "riskA"}}},
				{Risks: []configv1.ConditionalUpdateRisk{{Name: "riskB"}}},
			},
			alertRisks: []configv1.ConditionalUpdateRisk{{Name: "riskA"}},
			expected:   utilerrors.NewAggregate([]error{fmt.Errorf("found alert risk and conditional update risk share the name: riskA")}),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			actual := sanityCheck(tt.updates, tt.alertRisks)
			if diff := cmp.Diff(tt.expected, actual, cmp.Comparer(func(x, y error) bool {
				if x == nil || y == nil {
					return x == nil && y == nil
				}
				return x.Error() == y.Error()
			})); diff != "" {
				t.Errorf("sanityCheck() mismatch (-want +got):\n%s", diff)
			}

		})
	}
}

func Test_loadRiskVersions(t *testing.T) {
	testcases := []struct {
		name               string
		conditionalUpdates []configv1.ConditionalUpdate
		expected           map[string]riskWithVersion
	}{
		{
			name: "no conditional updates",
		},
		{
			name: "some conditional updates",
			conditionalUpdates: []configv1.ConditionalUpdate{
				{Release: configv1.Release{Version: "4.20.1"},
					Risks: []configv1.ConditionalUpdateRisk{{Name: "riskA"}, {Name: "riskB"}}},
				{Release: configv1.Release{Version: "4.20.3"},
					Risks: []configv1.ConditionalUpdateRisk{{Name: "riskA"}, {Name: "riskC"}}},
				{Release: configv1.Release{Version: "4.22.1"},
					Risks: []configv1.ConditionalUpdateRisk{{Name: "riskD"}, {Name: "riskB"}}},
				{Release: configv1.Release{Version: "5.0.1"},
					Risks: []configv1.ConditionalUpdateRisk{{Name: "riskB"}, {Name: "riskC"}}},
			},
			expected: map[string]riskWithVersion{
				"riskA": {version: semver.MustParse("4.20.3"), risk: configv1.ConditionalUpdateRisk{Name: "riskA"}},
				"riskB": {version: semver.MustParse("5.0.1"), risk: configv1.ConditionalUpdateRisk{Name: "riskB"}},
				"riskC": {version: semver.MustParse("5.0.1"), risk: configv1.ConditionalUpdateRisk{Name: "riskC"}},
				"riskD": {version: semver.MustParse("4.22.1"), risk: configv1.ConditionalUpdateRisk{Name: "riskD"}},
			},
		},
	}
	for _, tt := range testcases {
		t.Run(tt.name, func(t *testing.T) {
			actual := loadRiskVersions(tt.conditionalUpdates)
			if diff := cmp.Diff(tt.expected, actual, cmp.AllowUnexported(riskWithVersion{})); diff != "" {
				t.Errorf("%s: loadRiskVersions() mismatch (-want +got):\n%s", tt.name, diff)
			}
		})
	}
}

func Test_risksInOrder(t *testing.T) {
	testcases := []struct {
		name         string
		riskVersions map[string]riskWithVersion
		expected     []string
	}{
		{
			name: "no risks",
		},
		{
			name: "some risks",
			riskVersions: map[string]riskWithVersion{
				"riskA": {version: semver.MustParse("4.20.3"), risk: configv1.ConditionalUpdateRisk{Name: "riskA"}},
				"riskB": {version: semver.MustParse("5.0.1"), risk: configv1.ConditionalUpdateRisk{Name: "riskB"}},
				"riskC": {version: semver.MustParse("5.0.1"), risk: configv1.ConditionalUpdateRisk{Name: "riskC"}},
				"riskD": {version: semver.MustParse("4.22.1"), risk: configv1.ConditionalUpdateRisk{Name: "riskD"}},
			},
			expected: []string{"riskB", "riskC", "riskD", "riskA"},
		},
	}
	for _, tt := range testcases {
		t.Run(tt.name, func(t *testing.T) {
			actual := risksInOrder(tt.riskVersions)
			if diff := cmp.Diff(tt.expected, actual); diff != "" {
				t.Errorf("%s: risksInOrder() mismatch (-want +got):\n%s", tt.name, diff)
			}
		})
	}
}

func Test_loadRiskConditions(t *testing.T) {
	testcases := []struct {
		name         string
		risks        []string
		riskVersions map[string]riskWithVersion
		mockPromql   clusterconditions.Condition
		expected     map[string][]metav1.Condition
	}{
		{
			name:  "basic case",
			risks: []string{"riskB", "riskC", "riskD", "riskA"},
			mockPromql: &mock.Mock{
				ValidQueue: []error{nil},
				MatchQueue: []mock.MatchResult{
					{Error: errors.New("ERROR1")},
					{Match: true},
					{},
					{Match: true},
				}},
			riskVersions: map[string]riskWithVersion{
				"riskA": {version: semver.MustParse("4.20.3"), risk: configv1.ConditionalUpdateRisk{Name: "riskA", MatchingRules: []configv1.ClusterCondition{{Type: "PromQL"}}}},
				"riskB": {version: semver.MustParse("5.0.1"), risk: configv1.ConditionalUpdateRisk{Name: "riskB", MatchingRules: []configv1.ClusterCondition{{Type: "PromQL"}}}},
				"riskC": {version: semver.MustParse("5.0.1"), risk: configv1.ConditionalUpdateRisk{Name: "riskC", MatchingRules: []configv1.ClusterCondition{{Type: "PromQL"}}}},
				"riskD": {version: semver.MustParse("4.22.1"), risk: configv1.ConditionalUpdateRisk{Name: "riskD", MatchingRules: []configv1.ClusterCondition{{Type: "PromQL"}}}},
			},
			expected: map[string][]metav1.Condition{
				"riskA": {{Type: "Applies", Status: "True", Reason: "Match"}},
				"riskB": {
					{
						Type:    "Applies",
						Status:  "Unknown",
						Reason:  "EvaluationFailed",
						Message: "Could not evaluate exposure to update risk riskB (ERROR1)\n  riskB description: \n  riskB URL: ",
					},
				},
				"riskC": {{Type: "Applies", Status: "True", Reason: "Match"}},
				"riskD": {{Type: "Applies", Status: "False", Reason: "NotMatch"}},
			},
		},
	}
	for _, tt := range testcases {
		t.Run(tt.name, func(t *testing.T) {
			registry := clusterconditions.NewConditionRegistry()
			registry.Register("PromQL", tt.mockPromql)
			actual := loadRiskConditions(context.Background(), tt.risks, tt.riskVersions, registry)
			if diff := cmp.Diff(tt.expected, actual); diff != "" {
				t.Errorf("%s: loadRiskConditions() mismatch (-want +got):\n%s", tt.name, diff)
			}
		})
	}
}

type mockAlertGetter struct {
	ret alert.DataAndStatus
}

func (m *mockAlertGetter) Get(ctx context.Context) (*alert.DataAndStatus, error) {
	return &m.ret, nil
}

func Test_evaluateAlertConditions(t *testing.T) {
	t1 := time.Now()
	t2 := time.Now().Add(-3 * time.Minute)
	tests := []struct {
		name               string
		u                  *availableUpdates
		expected           error
		expectedAlertRisks []configv1.ConditionalUpdateRisk
	}{
		{
			name: "basic case",
			u: &availableUpdates{
				AlertGetter: &mockAlertGetter{
					ret: alert.DataAndStatus{
						Data: alert.Data{
							Alerts: []alert.Alert{
								{
									Labels: alert.AlertLabels{
										AlertName:           "PodDisruptionBudgetLimit",
										Severity:            "critical",
										Namespace:           "namespace",
										PodDisruptionBudget: "some-pdb",
									},
									State: "firing",
									Annotations: alert.AlertAnnotations{
										Summary:     "summary",
										Description: "description",
										Message:     "message",
										Runbook:     "http://runbook.example.com/runbooks/abc.md",
									},
									ActiveAt: t1,
								},
								{
									Labels: alert.AlertLabels{
										AlertName: "not-important",
									},
									State: "pending",
								},
								{
									Labels: alert.AlertLabels{
										AlertName:           "PodDisruptionBudgetAtLimit",
										Severity:            "severity",
										Namespace:           "namespace",
										PodDisruptionBudget: "some-pdb",
									},
									State: "firing",
									Annotations: alert.AlertAnnotations{
										Summary:     "summary",
										Description: "description",
										Message:     "message",
										Runbook:     "http://runbook.example.com/runbooks/bbb.md",
									},
									ActiveAt: t1,
								},
								{
									Labels: alert.AlertLabels{
										AlertName:           "PodDisruptionBudgetAtLimit",
										Severity:            "severity",
										Namespace:           "namespace",
										PodDisruptionBudget: "another-pdb",
									},
									State: "firing",
									Annotations: alert.AlertAnnotations{
										Summary:     "summary",
										Description: "description",
										Message:     "message",
										Runbook:     "http://runbook.example.com/runbooks/bbb.md",
									},
									ActiveAt: t2,
								},
							},
						},
					},
				},
			},
			expectedAlertRisks: []configv1.ConditionalUpdateRisk{
				{
					Name:    "PodDisruptionBudgetAtLimit",
					Message: "summary.",
					URL:     "todo-url",
					MatchingRules: []configv1.ClusterCondition{
						{
							Type: "PromQL",
							PromQL: &configv1.PromQLClusterCondition{
								PromQL: "todo-expression",
							},
						},
					},
					Conditions: []metav1.Condition{{
						Type:               "Applies",
						Status:             "True",
						Reason:             "Alert:firing",
						Message:            "severity alert PodDisruptionBudgetAtLimit firing, which might slow node drains. Namespace=namespace, PodDisruptionBudget=some-pdb. summary. The alert description is: description | message http://runbook.example.com/runbooks/bbb.md; severity alert PodDisruptionBudgetAtLimit firing, which might slow node drains. Namespace=namespace, PodDisruptionBudget=another-pdb. summary. The alert description is: description | message http://runbook.example.com/runbooks/bbb.md",
						LastTransitionTime: metav1.NewTime(t2),
					}},
				},
				{
					Name:    "PodDisruptionBudgetLimit",
					Message: "summary.",
					URL:     "todo-url",
					MatchingRules: []configv1.ClusterCondition{
						{
							Type: "PromQL",
							PromQL: &configv1.PromQLClusterCondition{
								PromQL: "todo-expression",
							},
						},
					},
					Conditions: []metav1.Condition{{
						Type:               "Applies",
						Status:             "True",
						Reason:             "Alert:firing",
						Message:            "critical alert PodDisruptionBudgetLimit firing, suggesting significant cluster issues worth investigating. Namespace=namespace, PodDisruptionBudget=some-pdb. summary. The alert description is: description | message http://runbook.example.com/runbooks/abc.md",
						LastTransitionTime: metav1.NewTime(t1),
					}},
				},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			actual := tt.u.evaluateAlertRisks(context.TODO())
			if diff := cmp.Diff(tt.expected, actual, cmp.Comparer(func(x, y error) bool {
				if x == nil || y == nil {
					return x == nil && y == nil
				}
				return x.Error() == y.Error()
			})); diff != "" {
				t.Errorf("evaluateAlertConditions() mismatch (-want +got):\n%s", diff)
			}

			if actual == nil {
				if diff := cmp.Diff(tt.expectedAlertRisks, tt.u.AlertRisks); diff != "" {
					t.Errorf("AlertRisks mismatch (-want +got):\n%s", diff)
				}
			}
		})
	}
}

func Test_newRecommendedReason(t *testing.T) {
	tests := []struct {
		name     string
		now      string
		want     string
		expected string
	}{
		{
			name:     "recommendedReasonRisksNotExposed to recommendedReasonAllExposedRisksAccepted",
			now:      recommendedReasonRisksNotExposed,
			want:     recommendedReasonAllExposedRisksAccepted,
			expected: recommendedReasonAllExposedRisksAccepted,
		},
		{
			name:     "recommendedReasonRisksNotExposed to recommendedReasonEvaluationFailed",
			now:      recommendedReasonRisksNotExposed,
			want:     recommendedReasonEvaluationFailed,
			expected: recommendedReasonEvaluationFailed,
		},
		{
			name:     "recommendedReasonRisksNotExposed to recommendedReasonMultiple",
			now:      recommendedReasonRisksNotExposed,
			want:     recommendedReasonMultiple,
			expected: recommendedReasonMultiple,
		},
		{
			name:     "recommendedReasonRisksNotExposed to recommendedReasonExposed",
			now:      recommendedReasonRisksNotExposed,
			want:     recommendedReasonExposed,
			expected: recommendedReasonExposed,
		},
		{
			name:     "recommendedReasonAllExposedRisksAccepted to recommendedReasonRisksNotExposed",
			now:      recommendedReasonAllExposedRisksAccepted,
			want:     recommendedReasonRisksNotExposed,
			expected: recommendedReasonAllExposedRisksAccepted,
		},
		{
			name:     "recommendedReasonAllExposedRisksAccepted to recommendedReasonEvaluationFailed",
			now:      recommendedReasonAllExposedRisksAccepted,
			want:     recommendedReasonEvaluationFailed,
			expected: recommendedReasonEvaluationFailed,
		},
		{
			name:     "recommendedReasonAllExposedRisksAccepted to recommendedReasonMultiple",
			now:      recommendedReasonAllExposedRisksAccepted,
			want:     recommendedReasonMultiple,
			expected: recommendedReasonMultiple,
		},
		{
			name:     "recommendedReasonAllExposedRisksAccepted to recommendedReasonExposed",
			now:      recommendedReasonAllExposedRisksAccepted,
			want:     recommendedReasonExposed,
			expected: recommendedReasonExposed,
		},
		{
			name:     "recommendedReasonEvaluationFailed to recommendedReasonRisksNotExposed",
			now:      recommendedReasonEvaluationFailed,
			want:     recommendedReasonRisksNotExposed,
			expected: recommendedReasonEvaluationFailed,
		},
		{
			name:     "recommendedReasonEvaluationFailed to recommendedReasonAllExposedRisksAccepted",
			now:      recommendedReasonEvaluationFailed,
			want:     recommendedReasonAllExposedRisksAccepted,
			expected: recommendedReasonEvaluationFailed,
		},
		{
			name:     "recommendedReasonEvaluationFailed to recommendedReasonMultiple",
			now:      recommendedReasonEvaluationFailed,
			want:     recommendedReasonMultiple,
			expected: recommendedReasonMultiple,
		},
		{
			name:     "recommendedReasonEvaluationFailed to recommendedReasonExposed",
			now:      recommendedReasonEvaluationFailed,
			want:     recommendedReasonExposed,
			expected: recommendedReasonExposed,
		},
		{
			name:     "recommendedReasonMultiple to recommendedReasonRisksNotExposed",
			now:      recommendedReasonMultiple,
			want:     recommendedReasonRisksNotExposed,
			expected: recommendedReasonMultiple,
		},
		{
			name:     "recommendedReasonMultiple to recommendedReasonAllExposedRisksAccepted",
			now:      recommendedReasonMultiple,
			want:     recommendedReasonAllExposedRisksAccepted,
			expected: recommendedReasonMultiple,
		},
		{
			name:     "recommendedReasonMultiple to recommendedReasonEvaluationFailed",
			now:      recommendedReasonMultiple,
			want:     recommendedReasonEvaluationFailed,
			expected: recommendedReasonMultiple,
		},
		{
			name:     "recommendedReasonMultiple to recommendedReasonExposed",
			now:      recommendedReasonMultiple,
			want:     recommendedReasonExposed,
			expected: recommendedReasonMultiple,
		},
		{
			name:     "recommendedReasonExposed to recommendedReasonRisksNotExposed",
			now:      recommendedReasonExposed,
			want:     recommendedReasonRisksNotExposed,
			expected: recommendedReasonExposed,
		},
		{
			name:     "recommendedReasonExposed to recommendedReasonAllExposedRisksAccepted",
			now:      recommendedReasonExposed,
			want:     recommendedReasonAllExposedRisksAccepted,
			expected: recommendedReasonExposed,
		},
		{
			name:     "recommendedReasonExposed to recommendedReasonEvaluationFailed",
			now:      recommendedReasonExposed,
			want:     recommendedReasonEvaluationFailed,
			expected: recommendedReasonExposed,
		},
		{
			name:     "recommendedReasonExposed to recommendedReasonMultiple",
			now:      recommendedReasonExposed,
			want:     recommendedReasonMultiple,
			expected: recommendedReasonMultiple,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			actual := newRecommendedReason(tt.now, tt.want)
			if diff := cmp.Diff(tt.expected, actual); diff != "" {
				t.Errorf("newRecommendedReason mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

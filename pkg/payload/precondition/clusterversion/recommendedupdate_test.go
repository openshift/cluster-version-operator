package clusterversion

import (
	"context"
	"testing"

	configv1 "github.com/openshift/api/config/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/openshift/cluster-version-operator/pkg/payload/precondition"
)

func TestRecommendedUpdate(t *testing.T) {
	ctx := context.Background()

	targetVersion := "4.3.2"
	tests := []struct {
		name                   string
		channel                string
		acceptRisks            []configv1.AcceptRisk
		availableUpdates       []configv1.Release
		conditionalUpdates     []configv1.ConditionalUpdate
		conditionalUpdateRisks []configv1.ConditionalUpdateRisk
		conditions             []configv1.ClusterOperatorStatusCondition
		expected               string
	}{
		{
			name:             "recommended",
			availableUpdates: []configv1.Release{{Version: targetVersion}},
		},
		{
			name:     "no relevant data",
			channel:  "stable-4.3",
			expected: "No RetrievedUpdates, so the recommended status of updating from 4.3.0 to 4.3.2 is unknown.",
		},
		{
			name: "Recommended=True",
			conditionalUpdates: []configv1.ConditionalUpdate{{
				Release: configv1.Release{Version: targetVersion},
				Conditions: []metav1.Condition{{
					Type:    "Recommended",
					Status:  metav1.ConditionTrue,
					Reason:  "RecommendedReason",
					Message: "For some reason, this update is recommended.",
				}},
			}},
		},
		{
			name: "Recommended=False",
			conditionalUpdates: []configv1.ConditionalUpdate{{
				Release: configv1.Release{Version: targetVersion},
				Conditions: []metav1.Condition{{
					Type:    "Recommended",
					Status:  metav1.ConditionFalse,
					Reason:  "FalseReason",
					Message: "For some reason, this update is not recommended.",
				}},
			}},
			expected: "Update from 4.3.0 to 4.3.2 is not recommended:\n\nFor some reason, this update is not recommended.",
		},
		{
			name:        "Recommended=False and alert risk ABC is accepted",
			acceptRisks: []configv1.AcceptRisk{{Name: "ABC"}},
			conditionalUpdateRisks: []configv1.ConditionalUpdateRisk{{
				Name: "ABC",
				Conditions: []metav1.Condition{{
					Type:    "Applies",
					Status:  metav1.ConditionTrue,
					Reason:  "Alert:firing",
					Message: "message",
				}},
			}},
			conditionalUpdates: []configv1.ConditionalUpdate{{
				Release: configv1.Release{Version: targetVersion},
				Conditions: []metav1.Condition{{
					Type:    "Recommended",
					Status:  metav1.ConditionFalse,
					Reason:  "FalseReason",
					Message: "For some reason, this update is not recommended.",
				}},
			}},
			expected: "Update from 4.3.0 to 4.3.2 is not recommended:\n\nFor some reason, this update is not recommended.",
		},
		{
			name: "Recommended=False and bug risk ABC is not accepted",
			conditionalUpdateRisks: []configv1.ConditionalUpdateRisk{{
				Name: "ABC",
				Conditions: []metav1.Condition{{
					Type:   "Applies",
					Status: metav1.ConditionTrue,
					Reason: "not-alert",
				}},
			}},
			conditionalUpdates: []configv1.ConditionalUpdate{{
				Release: configv1.Release{Version: targetVersion},
				Conditions: []metav1.Condition{{
					Type:    "Recommended",
					Status:  metav1.ConditionFalse,
					Reason:  "FalseReason",
					Message: "For some reason, this update is not recommended.",
				}},
			}},
			expected: "Update from 4.3.0 to 4.3.2 is not recommended:\n\nFor some reason, this update is not recommended.",
		},
		{
			name:        "Recommended=False and alert risk ABC is not accepted",
			acceptRisks: []configv1.AcceptRisk{{Name: "BBB"}},
			conditionalUpdateRisks: []configv1.ConditionalUpdateRisk{{
				Name: "ABC",
				Conditions: []metav1.Condition{{
					Type:    "Applies",
					Status:  metav1.ConditionTrue,
					Reason:  "Alert:firing",
					Message: "message",
				}},
			}},
			conditionalUpdates: []configv1.ConditionalUpdate{{
				Release: configv1.Release{Version: targetVersion},
				Conditions: []metav1.Condition{{
					Type:    "Recommended",
					Status:  metav1.ConditionFalse,
					Reason:  "FalseReason",
					Message: "For some reason, this update is not recommended.",
				}},
			}},
			expected: "Update from 4.3.0 to 4.3.2 is not recommended:\n\nThose alerts have to be accepted as risks before updates: ABC\nUpdate from 4.3.0 to 4.3.2 is not recommended:\n\nFor some reason, this update is not recommended.",
		},
		{
			name: "Recommended=Unknown",
			conditionalUpdates: []configv1.ConditionalUpdate{{
				Release: configv1.Release{Version: targetVersion},
				Conditions: []metav1.Condition{{
					Type:    "Recommended",
					Status:  metav1.ConditionUnknown,
					Reason:  "UnknownReason",
					Message: "For some reason, we cannot decide if this update is recommended.",
				}},
			}},
			expected: "Update from 4.3.0 to 4.3.2 is Recommended=Unknown: UnknownReason: For some reason, we cannot decide if this update is recommended.",
		},
		{
			name: "Unknown condition type",
			conditionalUpdates: []configv1.ConditionalUpdate{{
				Release: configv1.Release{Version: targetVersion},
				Conditions: []metav1.Condition{{
					Type:    "Unknown",
					Status:  metav1.ConditionTrue,
					Reason:  "NotUsed",
					Message: "Not used.",
				}},
			}},
			expected: "Update from 4.3.0 to 4.3.2 has a status.conditionalUpdates entry, but no Recommended condition.",
		},
		{
			name:    "RetrievedUpdates=True",
			channel: "stable-4.3",
			conditions: []configv1.ClusterOperatorStatusCondition{{
				Type:    configv1.RetrievedUpdates,
				Status:  configv1.ConditionTrue,
				Reason:  "TrueReason",
				Message: "Not used.",
			}},
			expected: "RetrievedUpdates=True (TrueReason), so the update from 4.3.0 to 4.3.2 is probably neither recommended nor supported.",
		},
		{
			name:    "RetrievedUpdates=False",
			channel: "stable-4.3",
			conditions: []configv1.ClusterOperatorStatusCondition{{
				Type:    configv1.RetrievedUpdates,
				Status:  configv1.ConditionFalse,
				Reason:  "FalseReason",
				Message: "For some reason, we cannot retrieve update recommendations.",
			}},
			expected: "RetrievedUpdates=False (FalseReason), so the recommended status of updating from 4.3.0 to 4.3.2 is unknown.",
		},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			clusterVersion := &configv1.ClusterVersion{
				ObjectMeta: metav1.ObjectMeta{Name: "version"},
				Spec:       configv1.ClusterVersionSpec{Channel: testCase.channel, DesiredUpdate: &configv1.Update{AcceptRisks: testCase.acceptRisks}},
				Status: configv1.ClusterVersionStatus{
					Desired:                configv1.Release{Version: "4.3.0"},
					AvailableUpdates:       testCase.availableUpdates,
					ConditionalUpdates:     testCase.conditionalUpdates,
					Conditions:             testCase.conditions,
					ConditionalUpdateRisks: testCase.conditionalUpdateRisks,
				},
			}
			cvLister := fakeClusterVersionLister(t, clusterVersion)
			instance := NewRecommendedUpdate(cvLister)
			err := instance.Run(ctx, precondition.ReleaseContext{DesiredVersion: targetVersion})
			switch {
			case err != nil && len(testCase.expected) == 0:
				t.Error(err)
			case err != nil && err.Error() == testCase.expected:
			case err != nil && err.Error() != testCase.expected:
				t.Errorf("got %q, but expected %q", err, testCase.expected)
			case err == nil && len(testCase.expected) == 0:
			case err == nil && len(testCase.expected) != 0:
				t.Errorf("got %q, but expected %q", err, testCase.expected)
			}

		})
	}
}

package cvo

import (
	"testing"
	"time"

	configv1 "github.com/openshift/api/config/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestUpgradeableCheckIntervalsThrottlePeriod(t *testing.T) {
	intervals := defaultUpgradeableCheckIntervals()
	testCases := []struct {
		name      string
		condition *configv1.ClusterOperatorStatusCondition
		expected  time.Duration
	}{
		{
			name:     "no condition",
			expected: intervals.min,
		},
		{
			name:      "passing preconditions",
			condition: &configv1.ClusterOperatorStatusCondition{Type: DesiredReleaseAccepted, Reason: "PreconditionChecks", Status: configv1.ConditionTrue, LastTransitionTime: metav1.Now()},
			expected:  intervals.min,
		},
		{
			name:      "failing but not precondition",
			condition: &configv1.ClusterOperatorStatusCondition{Type: DesiredReleaseAccepted, Reason: "NotPreconditionChecks", Status: configv1.ConditionFalse, LastTransitionTime: metav1.Now()},
			expected:  intervals.min,
		},
		{
			name: "failing preconditions but too long ago",
			condition: &configv1.ClusterOperatorStatusCondition{
				Type:               DesiredReleaseAccepted,
				Reason:             "PreconditionChecks",
				Status:             configv1.ConditionFalse,
				LastTransitionTime: metav1.NewTime(time.Now().Add(-(intervals.afterPreconditionsFailed + time.Hour))),
			},
			expected: intervals.min,
		},
		{
			name:      "failing preconditions recently",
			condition: &configv1.ClusterOperatorStatusCondition{Type: DesiredReleaseAccepted, Reason: "PreconditionChecks", Status: configv1.ConditionFalse, LastTransitionTime: metav1.Now()},
			expected:  intervals.minOnFailedPreconditions,
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			cv := &configv1.ClusterVersion{Status: configv1.ClusterVersionStatus{Conditions: []configv1.ClusterOperatorStatusCondition{}}}
			if tc.condition != nil {
				cv.Status.Conditions = append(cv.Status.Conditions, *tc.condition)
			}
			if actual := intervals.throttlePeriod(cv); actual != tc.expected {
				t.Errorf("throttlePeriod() = %v, want %v", actual, tc.expected)
			}
		})
	}
}

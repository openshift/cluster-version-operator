package cvo

import (
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	configv1 "github.com/openshift/api/config/v1"
	"github.com/openshift/client-go/config/clientset/versioned/fake"
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

func TestUpgradeInProgressUpgradeable(t *testing.T) {
	testCases := []struct {
		name     string
		cv       *configv1.ClusterVersion
		expected *configv1.ClusterOperatorStatusCondition
	}{
		{
			name: "empty conditions",
			cv: &configv1.ClusterVersion{
				ObjectMeta: metav1.ObjectMeta{
					Name: "unit-test",
				},
			},
		},
		{
			name: "progressing is true",
			cv: &configv1.ClusterVersion{
				ObjectMeta: metav1.ObjectMeta{
					Name: "unit-test",
				},
				Status: configv1.ClusterVersionStatus{
					Conditions: []configv1.ClusterOperatorStatusCondition{
						{
							Type:    configv1.OperatorProgressing,
							Status:  configv1.ConditionTrue,
							Reason:  "a",
							Message: "b",
						},
					},
				},
			},
			expected: &configv1.ClusterOperatorStatusCondition{
				Type:    "UpgradeableUpgradeInProgress",
				Status:  configv1.ConditionTrue,
				Reason:  "a",
				Message: "b",
			},
		},
		{
			name: "progressing is false",
			cv: &configv1.ClusterVersion{
				ObjectMeta: metav1.ObjectMeta{
					Name: "unit-test",
				},
				Status: configv1.ClusterVersionStatus{
					Conditions: []configv1.ClusterOperatorStatusCondition{
						{
							Type:    configv1.OperatorProgressing,
							Status:  configv1.ConditionFalse,
							Reason:  "a",
							Message: "b",
						},
					},
				},
			},
		},
		{
			name: "progressing is missing",
			cv: &configv1.ClusterVersion{
				ObjectMeta: metav1.ObjectMeta{
					Name: "unit-test",
				},
				Status: configv1.ClusterVersionStatus{
					Conditions: []configv1.ClusterOperatorStatusCondition{
						{
							Type:    configv1.OperatorProgressing,
							Reason:  "a",
							Message: "b",
						},
					},
				},
			},
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			client := fake.NewSimpleClientset(tc.cv)
			unit := upgradeInProgressUpgradeable{name: "unit-test", cvLister: &clientCVLister{client: client}}
			actual := unit.Check()
			if diff := cmp.Diff(tc.expected, actual); diff != "" {
				t.Errorf("%s differs from expected:\n%s", tc.name, diff)
			}
		})
	}
}

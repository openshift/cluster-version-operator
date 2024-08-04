package updatestatus

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	configv1 "github.com/openshift/api/config/v1"
	configv1alpha "github.com/openshift/api/config/v1alpha1"
	configv1listers "github.com/openshift/client-go/config/listers/config/v1"
	"github.com/openshift/library-go/pkg/controller/factory"
	"github.com/openshift/library-go/pkg/operator/events"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/util/workqueue"
)

var allowUnexported = cmp.AllowUnexported(controlPlaneUpdateStatus{}, versions{})
var ignoreMutexes = cmpopts.IgnoreUnexported(sync.Mutex{})

func makeTestController(t *testing.T, cv *configv1.ClusterVersion) *controlPlaneUpdateInformer {
	t.Helper()
	cvIndexer := cache.NewIndexer(cache.MetaNamespaceKeyFunc, cache.Indexers{})
	if cv != nil {
		if err := cvIndexer.Add(cv); err != nil {
			t.Fatal(err)
		}
	}

	cvLister := configv1listers.NewClusterVersionLister(cvIndexer)
	controller := &controlPlaneUpdateInformer{
		clusterVersionLister: cvLister,
	}
	return controller
}

func Test_ControlPlaneUpdateInformer_Sync_Conditions_UpdateProgressing(t *testing.T) {
	tenMinutesAgo := metav1.NewTime(time.Now().Add(-10 * time.Minute))
	fiveMinutesAgo := metav1.NewTime(time.Now().Add(-5 * time.Minute))

	testCases := []struct {
		name string

		cvProgressing         *configv1.ClusterOperatorStatusCondition
		mostRecentHistoryItem *configv1.UpdateHistory

		expectedUpdateProgressing *metav1.Condition
	}{
		{
			name: "Progressing=True CV results in UpdateProgressing=True",
			cvProgressing: &configv1.ClusterOperatorStatusCondition{
				Type:               configv1.OperatorProgressing,
				Status:             configv1.ConditionTrue,
				LastTransitionTime: tenMinutesAgo,
				Reason:             "CVReason",
				Message:            "An upgrade is in progress. Working towards 4.17.0-ec.2: 782 of 960 done (81% complete), waiting on dns, network",
			},
			expectedUpdateProgressing: &metav1.Condition{
				Type:               string(configv1alpha.UpdateProgressing),
				Status:             metav1.ConditionTrue,
				LastTransitionTime: tenMinutesAgo,
				Reason:             "ClusterVersionProgressing",
				Message:            "An upgrade is in progress. Working towards 4.17.0-ec.2: 782 of 960 done (81% complete), waiting on dns, network",
			},
		},
		{
			name: "Progressing=True CV results in UpdateProgressing=True, history time overrides condition time",
			cvProgressing: &configv1.ClusterOperatorStatusCondition{
				Type:               configv1.OperatorProgressing,
				Status:             configv1.ConditionTrue,
				LastTransitionTime: tenMinutesAgo,
				Reason:             "CVReason",
				Message:            "An upgrade is in progress. Working towards 4.17.0-ec.2: 782 of 960 done (81% complete), waiting on dns, network",
			},
			mostRecentHistoryItem: &configv1.UpdateHistory{
				State:       "PartialUpdate",
				StartedTime: fiveMinutesAgo,
				Version:     "4.17.0-ec.2",
			},
			expectedUpdateProgressing: &metav1.Condition{
				Type:               string(configv1alpha.UpdateProgressing),
				Status:             metav1.ConditionTrue,
				LastTransitionTime: fiveMinutesAgo,
				Reason:             "ClusterVersionProgressing",
				Message:            "An upgrade is in progress. Working towards 4.17.0-ec.2: 782 of 960 done (81% complete), waiting on dns, network",
			},
		},
		{
			name: "Progressing=False CV results in UpdateProgressing=False",
			cvProgressing: &configv1.ClusterOperatorStatusCondition{
				Type:               configv1.OperatorProgressing,
				Status:             configv1.ConditionFalse,
				LastTransitionTime: tenMinutesAgo,
				Message:            "Cluster version is 4.17.0-ec.2",
			},
			expectedUpdateProgressing: &metav1.Condition{
				Type:               string(configv1alpha.UpdateProgressing),
				Status:             metav1.ConditionFalse,
				LastTransitionTime: tenMinutesAgo,
				Reason:             "ClusterVersionNotProgressing",
				Message:            "Cluster version is 4.17.0-ec.2",
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			cv := &configv1.ClusterVersion{
				ObjectMeta: metav1.ObjectMeta{Name: "version"},
			}
			if tc.cvProgressing != nil {
				cv.Status.Conditions = append(cv.Status.Conditions, *tc.cvProgressing)
			}
			if tc.mostRecentHistoryItem != nil {
				cv.Status.History = append([]configv1.UpdateHistory{*tc.mostRecentHistoryItem}, cv.Status.History...)
			}

			controller := makeTestController(t, cv)

			err := controller.sync(context.TODO(), cvSyncContext(t, cv))
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			status := controller.getControlPlaneUpdateStatus()
			if diff := cmp.Diff(tc.expectedUpdateProgressing, status.updating, allowUnexported); diff != "" {
				t.Fatalf("unexpected status (-expected +got):\n%s", diff)
			}
		})
	}
}

func Test_controlPlaneUpdateInformer_sync_versions(t *testing.T) {
	var minutesAgo [60]metav1.Time
	for i := range minutesAgo {
		minutesAgo[i] = metav1.NewTime(time.Now().Add(time.Duration(-i) * time.Minute))
	}

	testCases := []struct {
		name             string
		history          []configv1.UpdateHistory
		expectedVersions versions
		expectedInsights []configv1alpha.UpdateInsight
	}{
		{
			name: "Installation: Single version in progress",
			history: []configv1.UpdateHistory{
				{
					State:       configv1.PartialUpdate,
					StartedTime: minutesAgo[10],
					Version:     "v0-Installed",
				},
			},
			expectedVersions: versions{
				target:          "v0-Installed",
				isTargetInstall: true,
			},
		},
		{
			name: "Installation complete: Single version installed",
			history: []configv1.UpdateHistory{
				{
					State:          configv1.CompletedUpdate,
					StartedTime:    minutesAgo[10],
					CompletionTime: &minutesAgo[5],
					Version:        "v0-installed",
				},
			},
			expectedVersions: versions{
				target:          "v0-installed",
				isTargetInstall: true,
			},
		},
		{
			name: "Update in progress from a completed version",
			history: []configv1.UpdateHistory{
				{
					State:          configv1.PartialUpdate,
					StartedTime:    minutesAgo[10],
					CompletionTime: &minutesAgo[5],
					Version:        "v1-updating",
				},
				{
					State:          configv1.CompletedUpdate,
					StartedTime:    minutesAgo[20],
					CompletionTime: &minutesAgo[15],
					Version:        "v0-installed",
				},
			},
			expectedVersions: versions{
				target:   "v1-updating",
				previous: "v0-installed",
			},
		},
		{
			name: "Update in progress from a partial update",
			history: []configv1.UpdateHistory{
				{
					State:          configv1.PartialUpdate,
					StartedTime:    minutesAgo[10],
					CompletionTime: &minutesAgo[5],
					Version:        "v2-updating",
				},
				{
					State:          configv1.PartialUpdate,
					StartedTime:    minutesAgo[20],
					CompletionTime: &minutesAgo[15],
					Version:        "v1-partial",
				},
				{
					State:          configv1.CompletedUpdate,
					StartedTime:    minutesAgo[30],
					Version:        "v0-installed",
					CompletionTime: &minutesAgo[25],
				},
			},
			expectedVersions: versions{
				target:            "v2-updating",
				previous:          "v1-partial",
				isPreviousPartial: true,
			},
			expectedInsights: []configv1alpha.UpdateInsight{
				{
					StartedAt: minutesAgo[10],
					Scope: configv1alpha.UpdateInsightScope{
						Type:      configv1alpha.ScopeTypeControlPlane,
						Resources: []configv1alpha.ResourceRef{{APIGroup: "config.openshift.io/v1", Kind: "ClusterVersion", Name: "version"}},
					},
					Impact: configv1alpha.UpdateInsightImpact{
						Level:       configv1alpha.WarningImpactLevel,
						Type:        configv1alpha.NoneImpactType,
						Summary:     "Previous update to v1-partial never completed, last complete update was v0-installed",
						Description: "Current update to v2-updating was initiated while the previous update to version v1-partial was still in progress",
					},
					Remediation: configv1alpha.UpdateInsightRemediation{
						Reference: "https://docs.openshift.com/container-platform/latest/updating/troubleshooting_updates/gathering-data-cluster-update.html#gathering-clusterversion-history-cli_troubleshooting_updates",
					},
				},
			},
		},
		{
			name: "Update completed from a partial update",
			history: []configv1.UpdateHistory{
				{
					State:          configv1.CompletedUpdate,
					StartedTime:    minutesAgo[10],
					CompletionTime: &minutesAgo[5],
					Version:        "v2-updating",
				},
				{
					State:          configv1.PartialUpdate,
					StartedTime:    minutesAgo[20],
					CompletionTime: &minutesAgo[15],
					Version:        "v1-partial",
				},
				{
					State:          configv1.CompletedUpdate,
					StartedTime:    minutesAgo[30],
					Version:        "v0-installed",
					CompletionTime: &minutesAgo[25],
				},
			},
			expectedVersions: versions{
				target:            "v2-updating",
				previous:          "v1-partial",
				isPreviousPartial: true,
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			progressingStatus := configv1.ConditionFalse
			if len(tc.history) > 0 && tc.history[0].State == configv1.PartialUpdate {
				progressingStatus = configv1.ConditionTrue
			}
			cv := &configv1.ClusterVersion{
				ObjectMeta: metav1.ObjectMeta{Name: "version"},
				Status: configv1.ClusterVersionStatus{
					Conditions: []configv1.ClusterOperatorStatusCondition{
						{
							Type:               configv1.OperatorProgressing,
							Status:             progressingStatus,
							LastTransitionTime: minutesAgo[10],
							Reason:             "SomeReason",
							Message:            "Progressing",
						},
					},
					History: tc.history,
				},
			}

			controller := makeTestController(t, cv)

			err := controller.sync(context.TODO(), cvSyncContext(t, cv))
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			status := controller.getControlPlaneUpdateStatus()
			if diff := cmp.Diff(tc.expectedVersions, status.versions, allowUnexported); diff != "" {
				t.Fatalf("unexpected status (-expected +got):\n%s", diff)
			}
			if diff := cmp.Diff(tc.expectedInsights, controller.getInsights()); diff != "" {
				t.Fatalf("unexpected insights (-expected +got):\n%s", diff)
			}
		})

	}
}

func Test_controlPlaneUpdateInformer_sync_cv(t *testing.T) {
	twentyMinutesAgo := metav1.NewTime(time.Now().Add(-20 * time.Minute))
	fifteenMinutesAgo := metav1.NewTime(time.Now().Add(-15 * time.Minute))
	tenMinutesAgo := metav1.NewTime(time.Now().Add(-10 * time.Minute))
	fiveMinutesAgo := metav1.NewTime(time.Now().Add(-5 * time.Minute))

	testCases := []struct {
		name     string
		cv       *configv1.ClusterVersion
		expected *controlPlaneUpdateStatus
	}{
		{
			name: "cluster finished upgrading control plane",
			cv: &configv1.ClusterVersion{
				ObjectMeta: metav1.ObjectMeta{Name: "version"},
				Status: configv1.ClusterVersionStatus{
					Conditions: []configv1.ClusterOperatorStatusCondition{
						{
							Type:               configv1.OperatorProgressing,
							Status:             configv1.ConditionFalse,
							LastTransitionTime: tenMinutesAgo,
							Reason:             "SomeReason",
							Message:            "Cluster version is 4.17.0-ec.2",
						},
					},
					History: []configv1.UpdateHistory{
						{
							State:          configv1.CompletedUpdate,
							StartedTime:    tenMinutesAgo,
							CompletionTime: &fiveMinutesAgo,
							Version:        "v1-updated",
						},
						{
							State:          configv1.CompletedUpdate,
							StartedTime:    twentyMinutesAgo,
							CompletionTime: &fifteenMinutesAgo,
							Version:        "v0-installed",
						},
					},
				},
			},
			expected: &controlPlaneUpdateStatus{
				versions: versions{
					target:   "v1-updated",
					previous: "v0-installed",
				},
				updating: &metav1.Condition{
					Type:               string(configv1alpha.UpdateProgressing),
					Status:             metav1.ConditionFalse,
					LastTransitionTime: fiveMinutesAgo,
					Reason:             "ClusterVersionNotProgressing",
					Message:            "Cluster version is 4.17.0-ec.2",
				},
			},
		},
		{
			name: "cluster is upgrading control plane",
			cv: &configv1.ClusterVersion{
				ObjectMeta: metav1.ObjectMeta{Name: "version"},
				Status: configv1.ClusterVersionStatus{
					Conditions: []configv1.ClusterOperatorStatusCondition{
						{
							Type:               configv1.OperatorProgressing,
							Status:             configv1.ConditionTrue,
							LastTransitionTime: tenMinutesAgo,
							Reason:             "SomeReason",
							Message:            "Cluster is progressing towards 4.17.0-ec.2",
						},
					},
					History: []configv1.UpdateHistory{
						{
							State:       configv1.PartialUpdate,
							StartedTime: tenMinutesAgo,
							Version:     "v1-updated",
						},
						{
							State:          configv1.CompletedUpdate,
							StartedTime:    twentyMinutesAgo,
							CompletionTime: &fifteenMinutesAgo,
							Version:        "v0-installed",
						},
					},
				},
			},
			expected: &controlPlaneUpdateStatus{
				versions: versions{
					target:   "v1-updated",
					previous: "v0-installed",
				},
				updating: &metav1.Condition{
					Type:               string(configv1alpha.UpdateProgressing),
					Status:             metav1.ConditionTrue,
					LastTransitionTime: tenMinutesAgo,
					Reason:             "ClusterVersionProgressing",
					Message:            "Cluster is progressing towards 4.17.0-ec.2",
				},
			},
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			controller := makeTestController(t, tc.cv)
			err := controller.sync(context.TODO(), cvSyncContext(t, tc.cv))
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			status := controller.getControlPlaneUpdateStatus()
			if diff := cmp.Diff(tc.expected, &status, allowUnexported, ignoreMutexes); diff != "" {
				t.Fatalf("unexpected status (-expected +got):\n%s", diff)
			}
		})
	}
}

type testSyncContext struct {
	queueKey      string
	eventRecorder events.Recorder
}

func (t testSyncContext) Queue() workqueue.RateLimitingInterface {
	return nil
}

func (t testSyncContext) QueueKey() string {
	return t.queueKey
}

func (t testSyncContext) Recorder() events.Recorder {
	return t.eventRecorder
}

func cvSyncContext(t *testing.T, cv *configv1.ClusterVersion) factory.SyncContext {
	t.Helper()
	keys := queueKeys(cv)
	if len(keys) != 1 {
		t.Fatalf("unexpected queue keys for %v: expected 1, got %v", cv, keys)
	}

	return testSyncContext{
		queueKey:      keys[0],
		eventRecorder: events.NewInMemoryRecorder("test"),
	}
}

func Test_controlPlaneUpdateStatus_updateForClusterOperator(t *testing.T) {
	var minutesAgo [90]metav1.Time
	for i := 0; i < 90; i++ {
		minutesAgo[i] = metav1.NewTime(time.Now().Add(time.Duration(-i) * time.Minute))
	}

	testCases := []struct {
		name  string
		state controlPlaneUpdateStatus
		co    *configv1.ClusterOperator

		expected         map[string]metav1.Condition
		expectedInsights []configv1alpha.UpdateInsight
	}{
		{
			name: "updated operator",
			state: controlPlaneUpdateStatus{
				updating: &metav1.Condition{
					Type:               string(configv1alpha.UpdateProgressing),
					Status:             metav1.ConditionTrue,
					LastTransitionTime: minutesAgo[20],
					Reason:             "ClusterVersionProgressing",
					Message:            "Cluster is progressing towards 4.17.1",
				},
				versions: versions{
					target:   "4.17.1",
					previous: "4.17.0",
				},
				now: func() metav1.Time { return minutesAgo[0] },
			},
			co: &configv1.ClusterOperator{
				ObjectMeta: metav1.ObjectMeta{Name: "updated-operator"},
				Status: configv1.ClusterOperatorStatus{
					Versions: []configv1.OperandVersion{
						{
							Name:    "operator",
							Version: "4.17.1",
						},
					},
					Conditions: []configv1.ClusterOperatorStatusCondition{
						{
							Type:               configv1.OperatorProgressing,
							Status:             configv1.ConditionTrue,
							LastTransitionTime: minutesAgo[2],
							Reason:             "SomethingAfterUpdate",
							Message:            "Operator already updated but Progressing=True",
						},
					},
				},
			},
			expected: map[string]metav1.Condition{
				"updated-operator": {
					Type:               string(configv1alpha.UpdateProgressing),
					Status:             metav1.ConditionFalse,
					LastTransitionTime: minutesAgo[0],
					Reason:             "Updated",
					Message:            "Operator finished updating to 4.17.1",
				},
			},
		},
		{
			name: "updating operator",
			state: controlPlaneUpdateStatus{
				updating: &metav1.Condition{
					Type:               string(configv1alpha.UpdateProgressing),
					Status:             metav1.ConditionTrue,
					LastTransitionTime: minutesAgo[20],
					Reason:             "ClusterVersionProgressing",
					Message:            "Cluster is progressing towards 4.17.1",
				},
				versions: versions{
					target:   "4.17.1",
					previous: "4.17.0",
				},
				now: func() metav1.Time { return minutesAgo[0] },
			},
			co: &configv1.ClusterOperator{
				ObjectMeta: metav1.ObjectMeta{Name: "updating-operator"},
				Status: configv1.ClusterOperatorStatus{
					Versions: []configv1.OperandVersion{
						{
							Name:    "operator",
							Version: "4.17.0",
						},
					},
					Conditions: []configv1.ClusterOperatorStatusCondition{
						{
							Type:               configv1.OperatorProgressing,
							Status:             configv1.ConditionTrue,
							LastTransitionTime: minutesAgo[2],
							Reason:             "Updating",
							Message:            "Operator is updating to 4.17.1",
						},
					},
				},
			},
			expected: map[string]metav1.Condition{
				"updating-operator": {
					Type:               string(configv1alpha.UpdateProgressing),
					Status:             metav1.ConditionTrue,
					LastTransitionTime: minutesAgo[2],
					Reason:             "Updating",
					Message:            "Operator is updating to 4.17.1",
				},
			},
		},
		{
			name: "pending operator",
			state: controlPlaneUpdateStatus{
				updating: &metav1.Condition{
					Type:               string(configv1alpha.UpdateProgressing),
					Status:             metav1.ConditionTrue,
					LastTransitionTime: minutesAgo[20],
					Reason:             "ClusterVersionProgressing",
					Message:            "Cluster is progressing towards 4.17.1",
				},
				versions: versions{
					target:   "4.17.1",
					previous: "4.17.0",
				},
				now: func() metav1.Time { return minutesAgo[0] },
			},
			co: &configv1.ClusterOperator{
				ObjectMeta: metav1.ObjectMeta{Name: "pending-operator"},
				Status: configv1.ClusterOperatorStatus{
					Versions: []configv1.OperandVersion{
						{
							Name:    "operator",
							Version: "4.17.0",
						},
					},
					Conditions: []configv1.ClusterOperatorStatusCondition{
						{
							Type:               configv1.OperatorProgressing,
							Status:             configv1.ConditionFalse,
							LastTransitionTime: minutesAgo[40],
							Reason:             "AllIsWell",
							Message:            "Operator is chilling on 4.17.0",
						},
					},
				},
			},
			expected: map[string]metav1.Condition{
				"pending-operator": {
					Type:               string(configv1alpha.UpdateProgressing),
					Status:             metav1.ConditionFalse,
					LastTransitionTime: minutesAgo[20],
					Reason:             "Pending",
					Message:            "Operator is pending an update to 4.17.1",
				},
			},
		},
		// {
		// 	name: "not a core operator",
		// },
		// {
		// 	name: "cluster is not updating",
		// },
		{
			name: "no version data yet",
			state: controlPlaneUpdateStatus{
				updating: &metav1.Condition{
					Type:               string(configv1alpha.UpdateProgressing),
					Status:             metav1.ConditionTrue,
					LastTransitionTime: minutesAgo[20],
					Reason:             "ClusterVersionProgressing",
					Message:            "Cluster is progressing towards 4.17.1",
				},
				now: func() metav1.Time { return minutesAgo[0] },
			},
			co: &configv1.ClusterOperator{
				ObjectMeta: metav1.ObjectMeta{Name: "operador"},
				Status: configv1.ClusterOperatorStatus{
					Conditions: []configv1.ClusterOperatorStatusCondition{
						{
							Type:               configv1.OperatorProgressing,
							Status:             configv1.ConditionTrue,
							LastTransitionTime: minutesAgo[2],
							Reason:             "Updating",
							Message:            "Operator is updating to 4.17.1",
						},
					},
					Versions: []configv1.OperandVersion{
						{
							Name:    "operator",
							Version: "4.17.1",
						},
					},
				},
			},
			expected: map[string]metav1.Condition{
				"operador": {
					Type:               string(configv1alpha.UpdateProgressing),
					Status:             metav1.ConditionUnknown,
					LastTransitionTime: minutesAgo[0],
					Reason:             "ClusterVersionUnknown",
					Message:            "Unable to determine current cluster version",
				},
			},
		},
		// {
		// 	name: "no progressing condition yet",
		// },
		{
			name: "co status is missing operator version",
			state: controlPlaneUpdateStatus{
				updating: &metav1.Condition{
					Type:               string(configv1alpha.UpdateProgressing),
					Status:             metav1.ConditionTrue,
					LastTransitionTime: minutesAgo[20],
					Reason:             "ClusterVersionProgressing",
					Message:            "Cluster is progressing towards 4.17.1",
				},
				versions: versions{
					target:   "4.17.1",
					previous: "4.17.0",
				},
				now: func() metav1.Time { return minutesAgo[0] },
			},
			co: &configv1.ClusterOperator{
				ObjectMeta: metav1.ObjectMeta{Name: "missing-operator-version"},
				Status: configv1.ClusterOperatorStatus{
					Conditions: []configv1.ClusterOperatorStatusCondition{
						{
							Type:               configv1.OperatorProgressing,
							Status:             configv1.ConditionTrue,
							LastTransitionTime: minutesAgo[2],
							Reason:             "Updating",
							Message:            "Operator is updating to 4.17.1",
						},
					},
					Versions: []configv1.OperandVersion{
						{
							Name:    "operand",
							Version: "4.17.1",
						},
					},
				},
			},
			expected: map[string]metav1.Condition{
				"missing-operator-version": {
					Type:               string(configv1alpha.UpdateProgressing),
					Status:             metav1.ConditionUnknown,
					LastTransitionTime: minutesAgo[0],
					Reason:             "ClusterOperatorVersionMissing",
					Message:            "ClusterOperator status is missing an operator version",
				},
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			insights, err := tc.state.updateForClusterOperator(tc.co)

			// TODO: Test & handle errors
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if diff := cmp.Diff(tc.expected, tc.state.operators, allowUnexported); diff != "" {
				t.Errorf("unexpected status (-expected +got):\n%s", diff)
			}

			if diff := cmp.Diff(tc.expectedInsights, insights, allowUnexported); diff != "" {
				t.Errorf("unexpected insights (-expected +got):\n%s", diff)
			}
		})
	}
}

func Test_controlPlaneUpdateStatus_updateForClusterVersion(t *testing.T) {
	var minutesAgo [90]metav1.Time
	for i := 0; i < 90; i++ {
		minutesAgo[i] = metav1.NewTime(time.Now().Add(time.Duration(-i) * time.Minute))
	}

	historyItemInstallation := configv1.UpdateHistory{
		State:          configv1.CompletedUpdate,
		StartedTime:    minutesAgo[70],
		CompletionTime: &minutesAgo[60],
		Version:        "4.17.0-ec.2",
	}

	historyItemPartialUpdate := configv1.UpdateHistory{
		State:          configv1.PartialUpdate,
		StartedTime:    minutesAgo[40],
		CompletionTime: &minutesAgo[30],
		Version:        "4.17.1",
	}

	historyItemCompletedUpdate := configv1.UpdateHistory{
		State:          configv1.CompletedUpdate,
		StartedTime:    minutesAgo[30],
		CompletionTime: &minutesAgo[25],
		Version:        "4.17.2",
	}

	historyItemCurrentUpdate := configv1.UpdateHistory{
		State:       configv1.PartialUpdate,
		StartedTime: minutesAgo[10],
		Version:     "4.17.3",
	}

	testCases := []struct {
		name string

		// TODO: Changes from nonempty initial state

		cvProgressing *configv1.ClusterOperatorStatusCondition
		cvHistory     []configv1.UpdateHistory

		expectedUpdateProgressing *metav1.Condition
		expectedVersions          *versions

		expectedInsights []configv1alpha.UpdateInsight
	}{
		{
			name: "Installation still in progress",
			cvProgressing: &configv1.ClusterOperatorStatusCondition{
				Type:               configv1.OperatorProgressing,
				Status:             configv1.ConditionTrue,
				LastTransitionTime: minutesAgo[10],
				Reason:             "SomeReason",
				Message:            "Cluster is progressing towards 4.17.0-ec.2",
			},
			cvHistory: []configv1.UpdateHistory{
				{
					State:       configv1.PartialUpdate,
					StartedTime: minutesAgo[8],
					Version:     "4.17.0-ec.2",
				},
			},
			expectedUpdateProgressing: &metav1.Condition{
				Type:               string(configv1alpha.UpdateProgressing),
				Status:             metav1.ConditionTrue,
				LastTransitionTime: minutesAgo[8], // history StartedTime wins
				Reason:             "ClusterVersionProgressing",
				Message:            "Cluster is progressing towards 4.17.0-ec.2",
			},
			expectedVersions: &versions{
				target:          "4.17.0-ec.2",
				isTargetInstall: true,
			},
		},
		{
			name: "Installation completed",
			cvProgressing: &configv1.ClusterOperatorStatusCondition{
				Type:               configv1.OperatorProgressing,
				Status:             configv1.ConditionFalse,
				LastTransitionTime: minutesAgo[2],
				Reason:             "FinishedInstallation",
				Message:            "Cluster is now at 4.17.0-ec.2",
			},
			cvHistory: []configv1.UpdateHistory{
				historyItemInstallation,
			},
			expectedUpdateProgressing: &metav1.Condition{
				Type:               string(configv1alpha.UpdateProgressing),
				Status:             metav1.ConditionFalse,
				LastTransitionTime: *historyItemInstallation.CompletionTime,
				Reason:             "ClusterVersionNotProgressing",
				Message:            "Cluster is now at 4.17.0-ec.2",
			},
			expectedVersions: &versions{
				target:          "4.17.0-ec.2",
				isTargetInstall: true,
			},
		},
		{
			name: "Update in progress",
			cvProgressing: &configv1.ClusterOperatorStatusCondition{
				Type:               configv1.OperatorProgressing,
				Status:             configv1.ConditionTrue,
				LastTransitionTime: minutesAgo[40],
				Reason:             "SomeReason",
				Message:            "Applying 4.17.3",
			},
			cvHistory: []configv1.UpdateHistory{
				historyItemCurrentUpdate,
				historyItemInstallation,
			},
			expectedUpdateProgressing: &metav1.Condition{
				Type:               string(configv1alpha.UpdateProgressing),
				Status:             metav1.ConditionTrue,
				LastTransitionTime: historyItemCurrentUpdate.StartedTime, // history StartedTime wins
				Reason:             "ClusterVersionProgressing",
				Message:            "Applying 4.17.3",
			},
			expectedVersions: &versions{
				target:   historyItemCurrentUpdate.Version,
				previous: historyItemInstallation.Version,
			},
		},
		{
			name: "Update from partial in progress",
			cvProgressing: &configv1.ClusterOperatorStatusCondition{
				Type:               configv1.OperatorProgressing,
				Status:             configv1.ConditionTrue,
				LastTransitionTime: minutesAgo[40],
				Reason:             "SomeReason",
				Message:            "Applying 4.17.3",
			},
			cvHistory: []configv1.UpdateHistory{
				historyItemCurrentUpdate,
				historyItemPartialUpdate,
				historyItemInstallation,
			},
			expectedUpdateProgressing: &metav1.Condition{
				Type:               string(configv1alpha.UpdateProgressing),
				Status:             metav1.ConditionTrue,
				LastTransitionTime: historyItemCurrentUpdate.StartedTime, // history StartedTime wins
				Reason:             "ClusterVersionProgressing",
				Message:            "Applying 4.17.3",
			},
			expectedVersions: &versions{
				target:            historyItemCurrentUpdate.Version,
				previous:          historyItemPartialUpdate.Version,
				isPreviousPartial: true,
			},
			expectedInsights: []configv1alpha.UpdateInsight{
				{
					StartedAt: historyItemCurrentUpdate.StartedTime,
					Scope: configv1alpha.UpdateInsightScope{
						Type: configv1alpha.ScopeTypeControlPlane,
						Resources: []configv1alpha.ResourceRef{
							{APIGroup: "config.openshift.io/v1", Kind: "ClusterVersion", Name: "version"},
						},
					},
					Impact: configv1alpha.UpdateInsightImpact{
						Level:       configv1alpha.WarningImpactLevel,
						Type:        configv1alpha.NoneImpactType,
						Summary:     "Previous update to 4.17.1 never completed, last complete update was 4.17.0-ec.2",
						Description: "Current update to 4.17.3 was initiated while the previous update to version 4.17.1 was still in progress",
					},
					Remediation: configv1alpha.UpdateInsightRemediation{
						Reference: "https://docs.openshift.com/container-platform/latest/updating/troubleshooting_updates/gathering-data-cluster-update.html#gathering-clusterversion-history-cli_troubleshooting_updates",
					},
				},
			},
		},
		{
			name: "Update completed",
			cvProgressing: &configv1.ClusterOperatorStatusCondition{
				Type:               configv1.OperatorProgressing,
				Status:             configv1.ConditionFalse,
				LastTransitionTime: minutesAgo[2],
				Reason:             "FinishedUpdate",
				Message:            "Cluster is now at 4.17.3",
			},
			cvHistory: []configv1.UpdateHistory{
				historyItemCompletedUpdate,
				historyItemInstallation,
			},
			expectedUpdateProgressing: &metav1.Condition{
				Type:               string(configv1alpha.UpdateProgressing),
				Status:             metav1.ConditionFalse,
				LastTransitionTime: *historyItemCompletedUpdate.CompletionTime,
				Reason:             "ClusterVersionNotProgressing",
				Message:            "Cluster is now at 4.17.3",
			},
			expectedVersions: &versions{target: historyItemCompletedUpdate.Version, previous: historyItemInstallation.Version},
		},
		{
			name: "Update from partial completed",
			cvProgressing: &configv1.ClusterOperatorStatusCondition{
				Type:               configv1.OperatorProgressing,
				Status:             configv1.ConditionFalse,
				LastTransitionTime: minutesAgo[2],
				Reason:             "FinishedUpdate",
				Message:            "Cluster is now at 4.17.3",
			},
			cvHistory: []configv1.UpdateHistory{
				historyItemCompletedUpdate,
				historyItemPartialUpdate,
				historyItemInstallation,
			},
			expectedUpdateProgressing: &metav1.Condition{
				Type:               string(configv1alpha.UpdateProgressing),
				Status:             metav1.ConditionFalse,
				LastTransitionTime: *historyItemCompletedUpdate.CompletionTime,
				Reason:             "ClusterVersionNotProgressing",
				Message:            "Cluster is now at 4.17.3",
			},
			expectedVersions: &versions{
				target:            historyItemCompletedUpdate.Version,
				previous:          historyItemPartialUpdate.Version,
				isPreviousPartial: true,
			},
		},
		{
			name: "Progressing unknown",
			cvProgressing: &configv1.ClusterOperatorStatusCondition{
				Type:               configv1.OperatorProgressing,
				Status:             configv1.ConditionUnknown,
				LastTransitionTime: minutesAgo[2],
				Reason:             "Unknown",
				Message:            "Cluster Version Operator set this to Unknown",
			},
			cvHistory: []configv1.UpdateHistory{
				historyItemCompletedUpdate,
				historyItemInstallation,
			},
			expectedUpdateProgressing: &metav1.Condition{
				Type:               string(configv1alpha.UpdateProgressing),
				Status:             metav1.ConditionUnknown,
				LastTransitionTime: minutesAgo[2],
				Reason:             "ClusterVersionProgressingUnknown",
				Message:            "Cluster Version Operator set this to Unknown",
			},
			expectedVersions: &versions{target: historyItemCompletedUpdate.Version, previous: historyItemInstallation.Version},
		},
		{
			name:          "Progressing missing",
			cvProgressing: nil,
			cvHistory: []configv1.UpdateHistory{
				historyItemCompletedUpdate,
				historyItemInstallation,
			},
			expectedUpdateProgressing: &metav1.Condition{
				Type:               string(configv1alpha.UpdateProgressing),
				Status:             metav1.ConditionUnknown,
				LastTransitionTime: minutesAgo[0],
				Reason:             "ClusterVersionProgressingMissing",
				Message:            "ClusterVersion resource does not have a Progressing condition",
			},
			expectedVersions: &versions{target: historyItemCompletedUpdate.Version, previous: historyItemInstallation.Version},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {

			state := controlPlaneUpdateStatus{
				now: func() metav1.Time { return minutesAgo[0] },
			}

			cv := &configv1.ClusterVersion{
				ObjectMeta: metav1.ObjectMeta{Name: "version"},
				Status: configv1.ClusterVersionStatus{
					Conditions: []configv1.ClusterOperatorStatusCondition{},
					History:    tc.cvHistory,
				},
			}
			if tc.cvProgressing != nil {
				cv.Status.Conditions = append(cv.Status.Conditions, *tc.cvProgressing)
			}

			insights, err := state.updateForClusterVersion(cv)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if tc.expectedUpdateProgressing != nil {
				if diff := cmp.Diff(tc.expectedUpdateProgressing, state.updating); diff != "" {
					t.Errorf("unexpected updating condition (-expected +got):\n%s", diff)
				}
			}

			if tc.expectedVersions != nil {
				if diff := cmp.Diff(tc.expectedVersions, &state.versions, allowUnexported); diff != "" {
					t.Errorf("unexpected versions (-expected +got):\n%s", diff)
				}
			}

			// TODO: Handle expect / dont expect
			if diff := cmp.Diff(tc.expectedInsights, insights); diff != "" {
				t.Errorf("unexpected insights (-expected +got):\n%s", diff)
			}
		})
	}
}

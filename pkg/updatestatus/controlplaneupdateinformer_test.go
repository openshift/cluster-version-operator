package updatestatus

import (
	"context"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	configv1 "github.com/openshift/api/config/v1"
	configv1listers "github.com/openshift/client-go/config/listers/config/v1"
	"github.com/openshift/library-go/pkg/controller/factory"
	"github.com/openshift/library-go/pkg/operator/events"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/util/workqueue"
)

var allowUnexported = cmp.AllowUnexported(controlPlaneUpdateStatus{}, versions{})

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
				Type:               "UpdateProgressing",
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
				Type:               "UpdateProgressing",
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
				Type:               "UpdateProgressing",
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
			progressing := meta.FindStatusCondition(status.conditions, "UpdateProgressing")
			if diff := cmp.Diff(tc.expectedUpdateProgressing, progressing, allowUnexported); diff != "" {
				t.Fatalf("unexpected status (-expected +got):\n%s", diff)
			}
		})
	}
}

func Test_controlPlaneUpdateInformer_sync_versions(t *testing.T) {
	twentyMinutesAgo := metav1.NewTime(time.Now().Add(-20 * time.Minute))
	fifteenMinutesAgo := metav1.NewTime(time.Now().Add(-15 * time.Minute))
	tenMinutesAgo := metav1.NewTime(time.Now().Add(-10 * time.Minute))
	fiveMinutesAgo := metav1.NewTime(time.Now().Add(-5 * time.Minute))

	testCases := []struct {
		name             string
		history          []configv1.UpdateHistory
		expectedVersions versions
	}{
		{
			name: "Installation: Single version in progress",
			history: []configv1.UpdateHistory{
				{
					State:       configv1.PartialUpdate,
					StartedTime: tenMinutesAgo,
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
					StartedTime:    tenMinutesAgo,
					CompletionTime: &fiveMinutesAgo,
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
					StartedTime:    tenMinutesAgo,
					CompletionTime: &fiveMinutesAgo,
					Version:        "v1-updating",
				},
				{
					State:          configv1.CompletedUpdate,
					StartedTime:    twentyMinutesAgo,
					CompletionTime: &fifteenMinutesAgo,
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
					StartedTime:    tenMinutesAgo,
					CompletionTime: &fiveMinutesAgo,
					Version:        "v1-updating",
				},
				{
					State:          configv1.PartialUpdate,
					StartedTime:    twentyMinutesAgo,
					CompletionTime: &fifteenMinutesAgo,
					Version:        "v0-installed",
				},
			},
			expectedVersions: versions{
				target:            "v1-updating",
				previous:          "v0-installed",
				isPreviousPartial: true,
			},
		},
		{
			name: "Update completed from a partial update",
			history: []configv1.UpdateHistory{
				{
					State:          configv1.CompletedUpdate,
					StartedTime:    tenMinutesAgo,
					CompletionTime: &fiveMinutesAgo,
					Version:        "v1-updating",
				},
				{
					State:          configv1.PartialUpdate,
					StartedTime:    twentyMinutesAgo,
					CompletionTime: &fifteenMinutesAgo,
					Version:        "v0-installed",
				},
			},
			expectedVersions: versions{
				target:            "v1-updating",
				previous:          "v0-installed",
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
							LastTransitionTime: tenMinutesAgo,
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
		expected controlPlaneUpdateStatus
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
			expected: controlPlaneUpdateStatus{
				versions: versions{
					target:   "v1-updated",
					previous: "v0-installed",
				},
				conditions: []metav1.Condition{
					{
						Type:               "UpdateProgressing",
						Status:             metav1.ConditionFalse,
						LastTransitionTime: tenMinutesAgo,
						Reason:             "ClusterVersionNotProgressing",
						Message:            "Cluster version is 4.17.0-ec.2",
					},
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
			expected: controlPlaneUpdateStatus{
				versions: versions{
					target:   "v1-updated",
					previous: "v0-installed",
				},
				conditions: []metav1.Condition{
					{
						Type:               "UpdateProgressing",
						Status:             metav1.ConditionTrue,
						LastTransitionTime: tenMinutesAgo,
						Reason:             "ClusterVersionProgressing",
						Message:            "Cluster is progressing towards 4.17.0-ec.2",
					},
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
			if diff := cmp.Diff(tc.expected, status, allowUnexported); diff != "" {
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

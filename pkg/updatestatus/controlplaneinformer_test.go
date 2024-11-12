package updatestatus

import (
	"context"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"gopkg.in/yaml.v3"
	"k8s.io/utils/ptr"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/util/workqueue"

	configv1 "github.com/openshift/api/config/v1"
	configv1listers "github.com/openshift/client-go/config/listers/config/v1"
	"github.com/openshift/library-go/pkg/controller/factory"
	"github.com/openshift/library-go/pkg/operator/events"
)

func newClusterVersionStatusInsightUpdating(status metav1.ConditionStatus, reason ClusterVersionStatusInsightUpdatingReason, message string, lastTransitionTime metav1.Time) metav1.Condition {
	return metav1.Condition{
		Type:               string(ClusterVersionStatusInsightUpdating),
		Status:             status,
		Reason:             string(reason),
		Message:            message,
		LastTransitionTime: lastTransitionTime,
	}
}

func Test_sync(t *testing.T) {
	now := metav1.Now()
	var minutesAgo [120]metav1.Time
	for i := range minutesAgo {
		minutesAgo[i] = metav1.NewTime(now.Time.Add(-time.Duration(i) * time.Minute))
	}

	progressingTrue := configv1.ClusterOperatorStatusCondition{
		Type:    configv1.OperatorProgressing,
		Status:  configv1.ConditionTrue,
		Reason:  "ProgressingTrue",
		Message: "Cluster is progressing",
	}
	progressingFalse := configv1.ClusterOperatorStatusCondition{
		Type:    configv1.OperatorProgressing,
		Status:  configv1.ConditionFalse,
		Reason:  "ProgressingFalse",
		Message: "Cluster is on version 4.X.0",
	}

	inProgress418 := configv1.UpdateHistory{
		State:       configv1.PartialUpdate,
		StartedTime: minutesAgo[90],
		Version:     "4.18.0",
		Image:       "pullspec-4.18.0",
	}
	var completed418 configv1.UpdateHistory
	inProgress418.DeepCopyInto(&completed418)
	completed418.State = configv1.CompletedUpdate
	completed418.CompletionTime = &minutesAgo[60]

	inProgress419 := configv1.UpdateHistory{
		State:       configv1.PartialUpdate,
		StartedTime: minutesAgo[60],
		Version:     "4.19.0",
		Image:       "pullspec-4.19.0",
	}
	var completed419 configv1.UpdateHistory
	inProgress419.DeepCopyInto(&completed419)
	completed419.State = configv1.CompletedUpdate
	completed419.CompletionTime = &minutesAgo[30]

	cvRef := ResourceRef{Resource: "clusterversions", Group: "config.openshift.io", Name: "version"}

	testCases := []struct {
		name          string
		cvProgressing *configv1.ClusterOperatorStatusCondition
		cvHistory     []configv1.UpdateHistory

		expectedMsgs map[string]Insight
	}{
		{
			name:          "Cluster during installation",
			cvProgressing: &progressingTrue,
			cvHistory:     []configv1.UpdateHistory{inProgress418},
			expectedMsgs: map[string]Insight{
				"usc-cv-version": {
					UID:        "usc-cv-version",
					AcquiredAt: now,
					InsightUnion: InsightUnion{
						Type: ClusterVersionStatusInsightType,
						ClusterVersionStatusInsight: &ClusterVersionStatusInsight{
							Resource:   cvRef,
							Assessment: ControlPlaneAssessmentProgressing,
							Versions: ControlPlaneUpdateVersions{
								Target: Version{
									Version:  "4.18.0",
									Metadata: []VersionMetadata{{Key: InstallationMetadata}},
								},
							},
							Completion:           0,
							StartedAt:            minutesAgo[90],
							EstimatedCompletedAt: &minutesAgo[30],
							Conditions: []metav1.Condition{
								newClusterVersionStatusInsightUpdating(
									metav1.ConditionTrue,
									ClusterVersionProgressing,
									"ClusterVersion has Progressing=True(Reason=ProgressingTrue) | Message='Cluster is progressing'",
									now,
								),
							},
						},
					},
				},
			},
		},
		{
			name:          "Cluster after installation",
			cvProgressing: &progressingFalse,
			cvHistory:     []configv1.UpdateHistory{completed418},
			expectedMsgs: map[string]Insight{
				"usc-cv-version": {
					UID:        "usc-cv-version",
					AcquiredAt: now,
					InsightUnion: InsightUnion{
						Type: ClusterVersionStatusInsightType,
						ClusterVersionStatusInsight: &ClusterVersionStatusInsight{
							Resource:   cvRef,
							Assessment: ControlPlaneAssessmentCompleted,
							Versions: ControlPlaneUpdateVersions{
								Target: Version{
									Version:  "4.18.0",
									Metadata: []VersionMetadata{{Key: InstallationMetadata}},
								},
							},
							Completion:           100,
							StartedAt:            minutesAgo[90],
							CompletedAt:          &minutesAgo[60],
							EstimatedCompletedAt: &minutesAgo[30],
							Conditions: []metav1.Condition{
								newClusterVersionStatusInsightUpdating(
									metav1.ConditionFalse,
									ClusterVersionNotProgressing,
									"ClusterVersion has Progressing=False(Reason=ProgressingFalse) | Message='Cluster is on version 4.X.0'",
									now,
								),
							},
						},
					},
				},
			},
		},
		{
			name:          "Cluster during a standard update",
			cvProgressing: &progressingTrue,
			cvHistory:     []configv1.UpdateHistory{inProgress419, completed418},
			expectedMsgs: map[string]Insight{
				"usc-cv-version": {
					UID:        "usc-cv-version",
					AcquiredAt: now,
					InsightUnion: InsightUnion{
						Type: ClusterVersionStatusInsightType,
						ClusterVersionStatusInsight: &ClusterVersionStatusInsight{
							Resource:   cvRef,
							Assessment: ControlPlaneAssessmentProgressing,
							Versions: ControlPlaneUpdateVersions{
								Target:   Version{Version: "4.19.0"},
								Previous: Version{Version: "4.18.0"},
							},
							Completion:           0,
							StartedAt:            minutesAgo[60],
							EstimatedCompletedAt: &now,
							Conditions: []metav1.Condition{
								newClusterVersionStatusInsightUpdating(
									metav1.ConditionTrue,
									ClusterVersionProgressing,
									"ClusterVersion has Progressing=True(Reason=ProgressingTrue) | Message='Cluster is progressing'",
									now,
								),
							},
						},
					},
				},
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			cv := makeTestClusterVersion(tc.cvProgressing, tc.cvHistory)

			cvIndexer := cache.NewIndexer(cache.MetaNamespaceKeyFunc, cache.Indexers{})
			if err := cvIndexer.Add(cv); err != nil {
				t.Fatalf("Failed to add ClusterVersion to indexer: %v", err)
			}
			cvLister := configv1listers.NewClusterVersionLister(cvIndexer)

			var actualMsgs []informerMsg
			var sendInsight sendInsightFn = func(insight informerMsg) {
				actualMsgs = append(actualMsgs, insight)
			}

			controller := controlPlaneInformerController{
				clusterVersions: cvLister,
				sendInsight:     sendInsight,
				now:             func() metav1.Time { return now },
			}

			queueKey := configApiQueueKeys(cv)[0]

			err := controller.sync(context.Background(), newTestSyncContext(queueKey))
			if err != nil {
				t.Fatalf("unexpected error from sync(): %v", err)
			}

			var expectedMsgs []informerMsg
			for uid, insight := range tc.expectedMsgs {
				raw, err := yaml.Marshal(insight)
				if err != nil {
					t.Fatalf("Failed to marshal expected insight: %v", err)
				}
				expectedMsgs = append(expectedMsgs, informerMsg{
					uid:     uid,
					insight: raw,
				})
			}

			if diff := cmp.Diff(expectedMsgs, actualMsgs, cmp.AllowUnexported(informerMsg{})); diff != "" {
				t.Errorf("Sync messages differ from expected:\n%s", diff)
			}
		})
	}
}

func makeTestClusterVersion(progressing *configv1.ClusterOperatorStatusCondition, history []configv1.UpdateHistory) *configv1.ClusterVersion {
	cv := &configv1.ClusterVersion{
		ObjectMeta: metav1.ObjectMeta{Name: "version"},
		Status: configv1.ClusterVersionStatus{
			Conditions: []configv1.ClusterOperatorStatusCondition{},
			History:    []configv1.UpdateHistory{},
		},
	}

	if progressing != nil {
		cv.Status.Conditions = append(cv.Status.Conditions, *progressing)
	}
	cv.Status.History = append(cv.Status.History, history...)

	return cv
}

type testSyncContext struct {
	queueKey      string
	eventRecorder events.Recorder
	queue         workqueue.TypedRateLimitingInterface[any]
}

func (c testSyncContext) Queue() workqueue.RateLimitingInterface { //nolint:staticcheck
	return c.queue
}

func (c testSyncContext) QueueKey() string {
	return c.queueKey
}

func (c testSyncContext) Recorder() events.Recorder {
	return c.eventRecorder
}

func newTestSyncContext(queueKey string) factory.SyncContext {
	return testSyncContext{
		queueKey:      queueKey,
		eventRecorder: events.NewInMemoryRecorder("test"),
	}
}

func Test_assessClusterVersion(t *testing.T) {
	now := metav1.Now()
	var minutesAgo [120]metav1.Time
	for i := range minutesAgo {
		minutesAgo[i] = metav1.NewTime(now.Add(-time.Duration(i) * time.Minute))
	}

	cvReference := ResourceRef{
		Resource: "clusterversions",
		Group:    "config.openshift.io",
		Name:     "version",
	}
	testCases := []struct {
		name string

		cvHistory     []configv1.UpdateHistory
		cvProgressing configv1.ClusterOperatorStatusCondition

		expected *ClusterVersionStatusInsight
	}{
		{
			name: "ClusterVersion during installation",
			cvHistory: []configv1.UpdateHistory{
				{
					State:       configv1.PartialUpdate,
					StartedTime: minutesAgo[30],
					Version:     "4.18.0",
					Image:       "pullspec-4.18.0",
				},
			},
			cvProgressing: configv1.ClusterOperatorStatusCondition{
				Type:               configv1.OperatorProgressing,
				Status:             configv1.ConditionTrue,
				LastTransitionTime: minutesAgo[30],
				Reason:             "WhateverCVOHasHereWhenInstalling",
				Message:            "Whatever CVO has as a message while installing",
			},
			expected: &ClusterVersionStatusInsight{
				Resource:   cvReference,
				Assessment: ControlPlaneAssessmentProgressing,
				Versions: ControlPlaneUpdateVersions{
					Target: Version{
						Version:  "4.18.0",
						Metadata: []VersionMetadata{{Key: InstallationMetadata}},
					},
				},
				StartedAt:            minutesAgo[30],
				EstimatedCompletedAt: ptr.To[metav1.Time](metav1.NewTime(now.Add(30 * time.Minute))),
				Conditions: []metav1.Condition{
					newClusterVersionStatusInsightUpdating(
						metav1.ConditionTrue,
						ClusterVersionProgressing,
						"ClusterVersion has Progressing=True(Reason=WhateverCVOHasHereWhenInstalling) | Message='Whatever CVO has as a message while installing'",
						now,
					),
				},
			},
		},
		{
			name: "ClusterVersion after installation",
			cvHistory: []configv1.UpdateHistory{
				{
					State:          configv1.CompletedUpdate,
					StartedTime:    minutesAgo[60],
					CompletionTime: &minutesAgo[30],
					Version:        "4.18.0",
					Image:          "pullspec-4.18.0",
				},
			},
			cvProgressing: configv1.ClusterOperatorStatusCondition{
				Type:               configv1.OperatorProgressing,
				Status:             configv1.ConditionFalse,
				LastTransitionTime: minutesAgo[30],
				// CVO does not set up a Reason when Progressing=False
				Message: "Cluster version is 4.18.0",
			},
			expected: &ClusterVersionStatusInsight{
				Resource:   cvReference,
				Assessment: ControlPlaneAssessmentCompleted,
				Versions: ControlPlaneUpdateVersions{
					Target: Version{
						Version: "4.18.0",
						Metadata: []VersionMetadata{
							{
								Key: InstallationMetadata,
							},
						},
					},
				},
				Completion:           100,
				StartedAt:            minutesAgo[60],
				CompletedAt:          &minutesAgo[30],
				EstimatedCompletedAt: &now,
				Conditions: []metav1.Condition{
					newClusterVersionStatusInsightUpdating(
						metav1.ConditionFalse,
						ClusterVersionNotProgressing,
						"ClusterVersion has Progressing=False(Reason=) | Message='Cluster version is 4.18.0'",
						now,
					),
				},
			},
		},
		{
			name: "ClusterVersion during a standard update",
			cvHistory: []configv1.UpdateHistory{
				{
					State:       configv1.PartialUpdate,
					StartedTime: minutesAgo[20],
					Version:     "4.19.0",
					Image:       "pullspec-4.19.0",
				},
				{
					State:          configv1.CompletedUpdate,
					StartedTime:    minutesAgo[60],
					CompletionTime: &minutesAgo[30],
					Version:        "4.18.0",
					Image:          "pullspec-4.18.0",
				},
			},
			cvProgressing: configv1.ClusterOperatorStatusCondition{
				Type:               configv1.OperatorProgressing,
				Status:             configv1.ConditionTrue,
				LastTransitionTime: minutesAgo[20],
				Reason:             "WhateverCVOHasHereWhenUpdating",
				Message:            "Whatever CVO has as a message while updating",
			},
			expected: &ClusterVersionStatusInsight{
				Resource:   cvReference,
				Assessment: ControlPlaneAssessmentProgressing,
				Versions: ControlPlaneUpdateVersions{
					Target:   Version{Version: "4.19.0"},
					Previous: Version{Version: "4.18.0"},
				},
				StartedAt:            minutesAgo[20],
				EstimatedCompletedAt: ptr.To[metav1.Time](metav1.NewTime(now.Add(40 * time.Minute))),
				Conditions: []metav1.Condition{
					newClusterVersionStatusInsightUpdating(
						metav1.ConditionTrue,
						ClusterVersionProgressing,
						"ClusterVersion has Progressing=True(Reason=WhateverCVOHasHereWhenUpdating) | Message='Whatever CVO has as a message while updating'",
						now,
					),
				},
			},
		},
		{
			name: "ClusterVersion after a standard update",
			cvHistory: []configv1.UpdateHistory{
				{
					State:          configv1.CompletedUpdate,
					StartedTime:    minutesAgo[20],
					CompletionTime: &minutesAgo[10],
					Version:        "4.19.0",
					Image:          "pullspec-4.19.0",
				},
				{
					State:          configv1.CompletedUpdate,
					StartedTime:    minutesAgo[60],
					CompletionTime: &minutesAgo[30],
					Version:        "4.18.0",
					Image:          "pullspec-4.18.0",
				},
			},
			cvProgressing: configv1.ClusterOperatorStatusCondition{
				Type:               configv1.OperatorProgressing,
				Status:             configv1.ConditionFalse,
				LastTransitionTime: minutesAgo[10],
				// CVO does not set up a Reason when Progressing=False
				Message: "Cluster version is 4.19.0",
			},
			expected: &ClusterVersionStatusInsight{
				Resource:   cvReference,
				Assessment: ControlPlaneAssessmentCompleted,
				Completion: 100,
				Versions: ControlPlaneUpdateVersions{
					Target:   Version{Version: "4.19.0"},
					Previous: Version{Version: "4.18.0"},
				},
				StartedAt:            minutesAgo[20],
				CompletedAt:          &minutesAgo[10],
				EstimatedCompletedAt: ptr.To[metav1.Time](metav1.NewTime(now.Add(40 * time.Minute))),
				Conditions: []metav1.Condition{
					newClusterVersionStatusInsightUpdating(
						metav1.ConditionFalse,
						ClusterVersionNotProgressing,
						"ClusterVersion has Progressing=False(Reason=) | Message='Cluster version is 4.19.0'",
						now,
					),
				},
			},
		},
		{
			name: "ClusterVersion during an update from partial version",
			cvHistory: []configv1.UpdateHistory{
				{
					State:       configv1.PartialUpdate,
					StartedTime: minutesAgo[20],
					Version:     "4.19.1",
					Image:       "pullspec-4.19.1",
				},
				{
					State:          configv1.PartialUpdate,
					StartedTime:    minutesAgo[40],
					CompletionTime: &minutesAgo[20],
					Version:        "4.19.0",
					Image:          "pullspec-4.19.0",
				},
				{
					State:          configv1.CompletedUpdate,
					StartedTime:    minutesAgo[60],
					CompletionTime: &minutesAgo[50],
					Version:        "4.18.0",
					Image:          "pullspec-4.18.0",
				},
			},
			cvProgressing: configv1.ClusterOperatorStatusCondition{
				Type:               configv1.OperatorProgressing,
				Status:             configv1.ConditionTrue,
				LastTransitionTime: minutesAgo[20],
				Reason:             "WhateverCVOHasHereWhenUpdating",
				Message:            "Whatever CVO has as a message while updating",
			},
			expected: &ClusterVersionStatusInsight{
				Resource:   cvReference,
				Assessment: ControlPlaneAssessmentProgressing,
				Versions: ControlPlaneUpdateVersions{
					Target: Version{Version: "4.19.1"},
					Previous: Version{
						Version:  "4.19.0",
						Metadata: []VersionMetadata{{Key: PartialMetadata}},
					},
				},
				StartedAt:            minutesAgo[20],
				EstimatedCompletedAt: ptr.To[metav1.Time](metav1.NewTime(now.Add(40 * time.Minute))),
				Conditions: []metav1.Condition{
					newClusterVersionStatusInsightUpdating(
						metav1.ConditionTrue,
						ClusterVersionProgressing,
						"ClusterVersion has Progressing=True(Reason=WhateverCVOHasHereWhenUpdating) | Message='Whatever CVO has as a message while updating'",
						now,
					),
				},
			},
		},
		{
			name: "ClusterVersion after an update from partial version",
			cvHistory: []configv1.UpdateHistory{
				{
					State:          configv1.CompletedUpdate,
					StartedTime:    minutesAgo[20],
					CompletionTime: &minutesAgo[10],
					Version:        "4.19.1",
					Image:          "pullspec-4.19.1",
				},
				{
					State:          configv1.PartialUpdate,
					StartedTime:    minutesAgo[40],
					CompletionTime: &minutesAgo[20],
					Version:        "4.19.0",
					Image:          "pullspec-4.19.0",
				},
				{
					State:          configv1.CompletedUpdate,
					StartedTime:    minutesAgo[60],
					CompletionTime: &minutesAgo[50],
					Version:        "4.18.0",
					Image:          "pullspec-4.18.0",
				},
			},
			cvProgressing: configv1.ClusterOperatorStatusCondition{
				Type:               configv1.OperatorProgressing,
				Status:             configv1.ConditionFalse,
				LastTransitionTime: minutesAgo[10],
				// CVO does not set up a Reason when Progressing=False
				Message: "Cluster version is 4.19.1",
			},
			expected: &ClusterVersionStatusInsight{
				Resource:   cvReference,
				Assessment: ControlPlaneAssessmentCompleted,
				Completion: 100,
				Versions: ControlPlaneUpdateVersions{
					Target: Version{Version: "4.19.1"},
					Previous: Version{
						Version:  "4.19.0",
						Metadata: []VersionMetadata{{Key: PartialMetadata}},
					},
				},
				StartedAt:            minutesAgo[20],
				CompletedAt:          &minutesAgo[10],
				EstimatedCompletedAt: ptr.To[metav1.Time](metav1.NewTime(now.Add(40 * time.Minute))),
				Conditions: []metav1.Condition{
					newClusterVersionStatusInsightUpdating(
						metav1.ConditionFalse,
						ClusterVersionNotProgressing,
						"ClusterVersion has Progressing=False(Reason=) | Message='Cluster version is 4.19.1'",
						now,
					),
				},
			},
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			cv := &configv1.ClusterVersion{
				ObjectMeta: metav1.ObjectMeta{Name: "version"},
				Status: configv1.ClusterVersionStatus{
					History:    tc.cvHistory,
					Conditions: []configv1.ClusterOperatorStatusCondition{tc.cvProgressing},
				},
			}
			actual := assessClusterVersion(cv, now)
			if diff := cmp.Diff(tc.expected, actual, cmpopts.IgnoreFields(metav1.Condition{}, "LastTransitionTime")); diff != "" {
				t.Errorf("CV Status Insight differs from expected:\n%s", diff)
			}
		})
	}
}

func Test_isControlPlaneUpdating(t *testing.T) {
	now := time.Now()
	var minutesAgo [120]metav1.Time
	for i := range minutesAgo {
		minutesAgo[i] = metav1.NewTime(now.Add(-time.Duration(i) * time.Minute))
	}

	progressingTrue := configv1.ClusterOperatorStatusCondition{
		Type:               configv1.OperatorProgressing,
		Status:             configv1.ConditionTrue,
		LastTransitionTime: minutesAgo[30],
		Reason:             "WhateverCVOHasHereWhenUpdating",
		Message:            "Cluster is updating to 4.19.0",
	}
	progressingFalse := configv1.ClusterOperatorStatusCondition{
		Type:               configv1.OperatorProgressing,
		Status:             configv1.ConditionFalse,
		LastTransitionTime: minutesAgo[30],
		Message:            "Cluster version is 4.19.0",
	}
	completedUpdate := configv1.UpdateHistory{
		State:          configv1.CompletedUpdate,
		StartedTime:    minutesAgo[60],
		CompletionTime: &minutesAgo[30],
		Version:        "4.18.0",
		Image:          "pullspec-4.18.0",
	}
	partialUpdate := configv1.UpdateHistory{
		State:       configv1.PartialUpdate,
		StartedTime: minutesAgo[60],
		Version:     "4.19.0",
		Image:       "pullspec-4.19.0",
	}

	testCases := []struct {
		name string

		cvProgressing   *configv1.ClusterOperatorStatusCondition
		lastHistoryItem *configv1.UpdateHistory

		expectedCondition metav1.Condition
		expectedStarted   metav1.Time
		expectedCompleted metav1.Time
	}{
		{
			// ---GOOD CASES ---
			name:            "Progressing=True, last history item is Partial",
			cvProgressing:   &progressingTrue,
			lastHistoryItem: &partialUpdate,
			expectedCondition: metav1.Condition{
				Status:  metav1.ConditionTrue,
				Reason:  "ClusterVersionProgressing",
				Message: "ClusterVersion has Progressing=True(Reason=WhateverCVOHasHereWhenUpdating) | Message='Cluster is updating to 4.19.0'",
			},
			expectedStarted:   partialUpdate.StartedTime,
			expectedCompleted: metav1.Time{},
		},
		{
			name:            "Progressing=False, last history item is completed",
			cvProgressing:   &progressingFalse,
			lastHistoryItem: &completedUpdate,
			expectedCondition: metav1.Condition{
				Status:  metav1.ConditionFalse,
				Reason:  "ClusterVersionNotProgressing",
				Message: "ClusterVersion has Progressing=False(Reason=) | Message='Cluster version is 4.19.0'",
			},
			expectedStarted:   completedUpdate.StartedTime,
			expectedCompleted: *completedUpdate.CompletionTime,
		},

		// --- BAD CASES ---
		{
			name:            "no progressing condition",
			cvProgressing:   nil,
			lastHistoryItem: &partialUpdate,
			expectedCondition: metav1.Condition{
				Status:  metav1.ConditionUnknown,
				Reason:  "CannotDetermineUpdating",
				Message: "No Progressing condition in ClusterVersion",
			},
			expectedStarted:   metav1.Time{},
			expectedCompleted: metav1.Time{},
		},
		{
			name:            "no history",
			cvProgressing:   &progressingFalse,
			lastHistoryItem: nil,
			expectedCondition: metav1.Condition{
				Status:  metav1.ConditionUnknown,
				Reason:  "CannotDetermineUpdating",
				Message: "Empty history in ClusterVersion",
			},
		},
		{
			name:          "Progressing=True, but last history item is not Partial",
			cvProgressing: &progressingTrue,
			lastHistoryItem: &configv1.UpdateHistory{
				State:          configv1.CompletedUpdate,
				StartedTime:    minutesAgo[60],
				CompletionTime: &minutesAgo[30],
				Version:        "4.18.0",
				Image:          "pullspec-4.18.0",
			},
			expectedCondition: metav1.Condition{
				Status:  metav1.ConditionUnknown,
				Reason:  "CannotDetermineUpdating",
				Message: "Progressing=True in ClusterVersion but last history item is not Partial",
			},
		},
		{
			name:          "Progressing=True, but last history item has completion time",
			cvProgressing: &progressingTrue,
			lastHistoryItem: &configv1.UpdateHistory{
				State:          configv1.PartialUpdate,
				StartedTime:    minutesAgo[60],
				CompletionTime: &minutesAgo[50],
				Version:        "4.19.0",
				Image:          "pullspec-4.19.0",
			},
			expectedCondition: metav1.Condition{
				Status:  metav1.ConditionUnknown,
				Reason:  "CannotDetermineUpdating",
				Message: "Progressing=True in ClusterVersion but last history item has completion time",
			},
		},
		{
			name:          "Progressing=False, but last history item is not completed",
			cvProgressing: &progressingFalse,
			lastHistoryItem: &configv1.UpdateHistory{
				State:       configv1.PartialUpdate,
				StartedTime: minutesAgo[60],
				Version:     "4.19.0",
				Image:       "pullspec-4.19.0",
			},
			expectedCondition: metav1.Condition{
				Status:  metav1.ConditionUnknown,
				Reason:  "CannotDetermineUpdating",
				Message: "Progressing=False in ClusterVersion but last history item is not completed",
			},
		},
		{
			name:          "Progressing=False, but last history item has no completion time",
			cvProgressing: &progressingFalse,
			lastHistoryItem: &configv1.UpdateHistory{
				State:       configv1.CompletedUpdate,
				StartedTime: minutesAgo[60],
				Version:     "4.19.0",
				Image:       "pullspec-4.19.0",
			},
			expectedCondition: metav1.Condition{
				Status:  metav1.ConditionUnknown,
				Reason:  "CannotDetermineUpdating",
				Message: "Progressing=False in ClusterVersion but not no completion in last history item",
			},
		},
		{
			name: "Progressing=Unknown",
			cvProgressing: &configv1.ClusterOperatorStatusCondition{
				Type:    configv1.OperatorProgressing,
				Status:  configv1.ConditionUnknown,
				Reason:  "WhoKnows",
				Message: "Message about Progressing=Unknown",
			},
			lastHistoryItem: &partialUpdate,
			expectedCondition: metav1.Condition{
				Status:  metav1.ConditionUnknown,
				Reason:  "CannotDetermineUpdating",
				Message: "ClusterVersion has Progressing=Unknown(Reason=WhoKnows) | Message='Message about Progressing=Unknown'",
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			tc.expectedCondition.Type = string(ClusterVersionStatusInsightUpdating)

			actualStatus, actualStarted, actualCompleted := isControlPlaneUpdating(tc.cvProgressing, tc.lastHistoryItem)
			if diff := cmp.Diff(tc.expectedCondition, actualStatus); diff != "" {
				t.Errorf("Status differs from expected:\n%s", diff)
			}
			if diff := cmp.Diff(tc.expectedStarted, actualStarted); diff != "" {
				t.Errorf("Started differs from expected:\n%s", diff)
			}
			if diff := cmp.Diff(tc.expectedCompleted, actualCompleted); diff != "" {
				t.Errorf("Completed differs from expected:\n%s", diff)
			}
		})
	}
}

func Test_cvProgressingToUpdating(t *testing.T) {
	testCases := []struct {
		name        string
		progressing configv1.ClusterOperatorStatusCondition

		expectedStatus  metav1.ConditionStatus
		expectedReason  string
		expectedMessage string
	}{
		{
			name: "Progressing=True",
			progressing: configv1.ClusterOperatorStatusCondition{
				Status:  configv1.ConditionTrue,
				Reason:  "ReasonTrue",
				Message: "MessageTrue",
			},
			expectedStatus:  metav1.ConditionTrue,
			expectedReason:  "ClusterVersionProgressing",
			expectedMessage: "ClusterVersion has Progressing=True(Reason=ReasonTrue) | Message='MessageTrue'",
		},
		{
			name: "Progressing=False",
			progressing: configv1.ClusterOperatorStatusCondition{
				Status:  configv1.ConditionFalse,
				Reason:  "ReasonFalse",
				Message: "MessageFalse",
			},
			expectedStatus:  metav1.ConditionFalse,
			expectedReason:  "ClusterVersionNotProgressing",
			expectedMessage: "ClusterVersion has Progressing=False(Reason=ReasonFalse) | Message='MessageFalse'",
		},
		{
			name: "Progressing=Unknown",
			progressing: configv1.ClusterOperatorStatusCondition{
				Status:  configv1.ConditionUnknown,
				Reason:  "ReasonUnknown",
				Message: "MessageUnknown",
			},
			expectedStatus:  metav1.ConditionUnknown,
			expectedReason:  "CannotDetermineUpdating",
			expectedMessage: "ClusterVersion has Progressing=Unknown(Reason=ReasonUnknown) | Message='MessageUnknown'",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			actualStatus, actualReason, actualMessage := cvProgressingToUpdating(tc.progressing)
			if diff := cmp.Diff(tc.expectedStatus, actualStatus); diff != "" {
				t.Errorf("Status differs from expected:\n%s", diff)
			}
			if diff := cmp.Diff(tc.expectedReason, actualReason); diff != "" {
				t.Errorf("Reason differs from expected:\n%s", diff)
			}
			if diff := cmp.Diff(tc.expectedMessage, actualMessage); diff != "" {
				t.Errorf("Message differs from expected:\n%s", diff)
			}
		})
	}
}

func Test_versionsFromHistory(t *testing.T) {
	testCases := []struct {
		name     string
		history  []configv1.UpdateHistory
		expected ControlPlaneUpdateVersions
	}{
		{
			name:     "no history",
			history:  nil,
			expected: ControlPlaneUpdateVersions{},
		},
		{
			name:     "empty history",
			history:  []configv1.UpdateHistory{},
			expected: ControlPlaneUpdateVersions{},
		},
		{
			name: "single history item => installation",
			history: []configv1.UpdateHistory{
				{
					State:       configv1.CompletedUpdate,
					StartedTime: metav1.NewTime(time.Now()),
					Version:     "4.18.0",
					Image:       "pullspec-4.18.0",
					CompletionTime: &metav1.Time{
						Time: time.Now().Add(30 * time.Minute),
					},
				},
			},
			expected: ControlPlaneUpdateVersions{
				Target: Version{
					Version:  "4.18.0",
					Metadata: []VersionMetadata{{Key: InstallationMetadata}},
				},
			},
		},
		{
			name: "two history items => standard update",
			history: []configv1.UpdateHistory{
				{
					State:       configv1.PartialUpdate,
					StartedTime: metav1.NewTime(time.Now()),
					Version:     "4.19.0",
					Image:       "pullspec-4.19.0",
				},
				{
					State:          configv1.CompletedUpdate,
					StartedTime:    metav1.NewTime(time.Now().Add(-30 * time.Minute)),
					CompletionTime: &metav1.Time{Time: time.Now().Add(-20 * time.Minute)},
					Version:        "4.18.0",
					Image:          "pullspec-4.18.0",
				},
			},
			expected: ControlPlaneUpdateVersions{
				Target:   Version{Version: "4.19.0"},
				Previous: Version{Version: "4.18.0"},
			},
		},
		{
			name: "update from partial version",
			history: []configv1.UpdateHistory{
				{
					State:       configv1.PartialUpdate,
					StartedTime: metav1.NewTime(time.Now()),
					Version:     "4.19.1",
					Image:       "pullspec-4.19.1",
				},
				{
					State:          configv1.PartialUpdate,
					StartedTime:    metav1.NewTime(time.Now().Add(-20 * time.Minute)),
					CompletionTime: &metav1.Time{Time: time.Now().Add(-10 * time.Minute)},
					Version:        "4.19.0",
					Image:          "pullspec-4.19.0",
				},
			},
			expected: ControlPlaneUpdateVersions{
				Target: Version{Version: "4.19.1"},
				Previous: Version{
					Version:  "4.19.0",
					Metadata: []VersionMetadata{{Key: PartialMetadata}},
				},
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			actual := versionsFromHistory(tc.history)
			if diff := cmp.Diff(tc.expected, actual); diff != "" {
				t.Errorf("Versions differ from expected:\n%s", diff)
			}
		})
	}
}

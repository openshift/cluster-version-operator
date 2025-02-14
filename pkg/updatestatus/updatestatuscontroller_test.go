package updatestatus

import (
	"context"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	configv1 "github.com/openshift/api/config/v1"
	updatestatus "github.com/openshift/api/update/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/util/workqueue"
	clocktesting "k8s.io/utils/clock/testing"

	fakeupdateclient "github.com/openshift/client-go/update/clientset/versioned/fake"
	"github.com/openshift/library-go/pkg/controller/factory"
	"github.com/openshift/library-go/pkg/operator/events"
)

var compareOnlyStatus = cmpopts.IgnoreFields(updatestatus.UpdateStatus{}, "TypeMeta", "ObjectMeta", "Spec")

func Test_updateStatusController(t *testing.T) {
	now := metav1.Now()
	cvVersionResource := updatestatus.ResourceRef{
		Group:    configv1.GroupName,
		Resource: "clusterversions",
		Name:     "version",
	}

	cvuid := "cv-version"
	completedClusterVersionInsight := updatestatus.ControlPlaneInsight{
		UID:        cvuid,
		AcquiredAt: now,
		ControlPlaneInsightUnion: updatestatus.ControlPlaneInsightUnion{
			Type: updatestatus.ClusterVersionStatusInsightType,
			ClusterVersionStatusInsight: &updatestatus.ClusterVersionStatusInsight{
				Resource:   cvVersionResource,
				Assessment: updatestatus.ControlPlaneAssessmentCompleted,
			},
		},
	}
	progressingClusterVersionInsight := updatestatus.ControlPlaneInsight{
		UID:        cvuid,
		AcquiredAt: now,
		ControlPlaneInsightUnion: updatestatus.ControlPlaneInsightUnion{
			Type: updatestatus.ClusterVersionStatusInsightType,
			ClusterVersionStatusInsight: &updatestatus.ClusterVersionStatusInsight{
				Resource:   cvVersionResource,
				Assessment: updatestatus.ControlPlaneAssessmentProgressing,
			},
		},
	}

	degradedClusterVersionInsight := updatestatus.ControlPlaneInsight{
		UID:        cvuid,
		AcquiredAt: now,
		ControlPlaneInsightUnion: updatestatus.ControlPlaneInsightUnion{
			Type: updatestatus.ClusterVersionStatusInsightType,
			ClusterVersionStatusInsight: &updatestatus.ClusterVersionStatusInsight{
				Resource:   cvVersionResource,
				Assessment: updatestatus.ControlPlaneAssessmentDegraded,
			},
		},
	}

	healthyMonitoringCOInsight := updatestatus.ControlPlaneInsight{
		UID:        "co-monitoring",
		AcquiredAt: now,
		ControlPlaneInsightUnion: updatestatus.ControlPlaneInsightUnion{
			Type: updatestatus.ClusterOperatorStatusInsightType,
			ClusterOperatorStatusInsight: &updatestatus.ClusterOperatorStatusInsight{
				Conditions: []metav1.Condition{
					{
						Type:    string(updatestatus.ClusterOperatorStatusInsightHealthy),
						Status:  metav1.ConditionTrue,
						Reason:  "MonitoringHealthy",
						Message: "Monitoring is healthy",
					},
				},
				Name: "monitoring",
				Resource: updatestatus.ResourceRef{
					Group:    configv1.GroupName,
					Resource: "clusteroperators",
					Name:     "monitoring",
				},
			},
		},
	}

	unhealthyNetworkCOInsight := updatestatus.ControlPlaneInsight{
		UID:        "co-network",
		AcquiredAt: now,
		ControlPlaneInsightUnion: updatestatus.ControlPlaneInsightUnion{
			Type: updatestatus.ClusterOperatorStatusInsightType,
			ClusterOperatorStatusInsight: &updatestatus.ClusterOperatorStatusInsight{
				Conditions: []metav1.Condition{
					{
						Type:    string(updatestatus.ClusterOperatorStatusInsightHealthy),
						Status:  metav1.ConditionFalse,
						Reason:  "NetworkNotHealthy",
						Message: "Network is not healthy",
					},
				},
				Name: "network",
				Resource: updatestatus.ResourceRef{
					Group:    configv1.GroupName,
					Resource: "clusteroperators",
					Name:     "network",
				},
			},
		},
	}

	randomHealthInsight := updatestatus.ControlPlaneInsight{
		UID:        "abcdef",
		AcquiredAt: now,
		ControlPlaneInsightUnion: updatestatus.ControlPlaneInsightUnion{
			Type: updatestatus.HealthInsightType,
			HealthInsight: &updatestatus.HealthInsight{
				Scope: updatestatus.InsightScope{Type: updatestatus.ControlPlaneScope},
				Impact: updatestatus.InsightImpact{
					Level:       updatestatus.CriticalInfoLevel,
					Type:        updatestatus.NoneImpactType,
					Summary:     "Summary",
					Description: "Description",
				},
				Remediation: updatestatus.InsightRemediation{Reference: "file:///path/to/remediation"},
			},
		},
	}

	randomWorkerPoolHealthInsight := updatestatus.WorkerPoolInsight{
		UID:        "asdfgh",
		AcquiredAt: now,
		WorkerPoolInsightUnion: updatestatus.WorkerPoolInsightUnion{
			Type: updatestatus.HealthInsightType,
			HealthInsight: &updatestatus.HealthInsight{
				Scope: updatestatus.InsightScope{Type: updatestatus.WorkerPoolScope},
				Impact: updatestatus.InsightImpact{
					Level:       updatestatus.CriticalInfoLevel,
					Type:        updatestatus.UnknownImpactType,
					Summary:     "Summary for WP",
					Description: "Description for WP",
				},
				Remediation: updatestatus.InsightRemediation{Reference: "file:///path/to/remediation-for-wp"},
			},
		},
	}

	testCases := []struct {
		name string

		initialState  *updatestatus.UpdateStatus
		informerMsg   []informerMsg
		expectedState *updatestatus.UpdateStatus
	}{
		{
			name:          "no messages, no state -> no state",
			initialState:  nil,
			informerMsg:   []informerMsg{},
			expectedState: nil,
		},
		{
			name:          "no messages, empty state -> empty state",
			initialState:  &updatestatus.UpdateStatus{},
			expectedState: &updatestatus.UpdateStatus{},
		},
		{
			name: "no messages, state -> unchanged state",
			initialState: &updatestatus.UpdateStatus{
				Status: updatestatus.UpdateStatusStatus{
					ControlPlane: updatestatus.ControlPlane{
						Resource: cvVersionResource,
						Informers: []updatestatus.ControlPlaneInformer{
							{
								Name:     "cpi",
								Insights: []updatestatus.ControlPlaneInsight{completedClusterVersionInsight},
							},
						},
					},
				},
			},
			expectedState: &updatestatus.UpdateStatus{
				Status: updatestatus.UpdateStatusStatus{
					ControlPlane: updatestatus.ControlPlane{
						Resource: cvVersionResource,
						Informers: []updatestatus.ControlPlaneInformer{
							{
								Name:     "cpi",
								Insights: []updatestatus.ControlPlaneInsight{completedClusterVersionInsight},
							},
						},
					},
				},
			},
		},
		{
			name:         "one message, no state -> initialize from message",
			initialState: nil,
			informerMsg: []informerMsg{
				{
					informer:  "cpi",
					uid:       completedClusterVersionInsight.UID,
					cpInsight: completedClusterVersionInsight.DeepCopy(),
				},
			},
			expectedState: &updatestatus.UpdateStatus{
				Status: updatestatus.UpdateStatusStatus{
					ControlPlane: updatestatus.ControlPlane{
						Resource: cvVersionResource,
						Informers: []updatestatus.ControlPlaneInformer{
							{
								Name:     "cpi",
								Insights: []updatestatus.ControlPlaneInsight{completedClusterVersionInsight},
							},
						},
					},
				},
			},
		},
		{
			name: "messages over time build state over old state",
			initialState: &updatestatus.UpdateStatus{
				Status: updatestatus.UpdateStatusStatus{
					ControlPlane: updatestatus.ControlPlane{
						Resource: cvVersionResource,
						Informers: []updatestatus.ControlPlaneInformer{
							{
								Name: "cpi",
								Insights: []updatestatus.ControlPlaneInsight{
									healthyMonitoringCOInsight,     // kept
									completedClusterVersionInsight, // overwritten
								},
							},
						},
					},
				},
			},
			informerMsg: []informerMsg{
				{
					informer:      "cpi",
					uid:           unhealthyNetworkCOInsight.UID, // new-item
					cpInsight:     unhealthyNetworkCOInsight.DeepCopy(),
					knownInsights: []string{healthyMonitoringCOInsight.UID, cvuid},
				},
				{
					informer:      "cpi",
					uid:           cvuid,
					cpInsight:     progressingClusterVersionInsight.DeepCopy(),
					knownInsights: []string{healthyMonitoringCOInsight.UID, unhealthyNetworkCOInsight.UID},
				},
				{
					informer:      "cpi",
					uid:           randomHealthInsight.UID, // another
					cpInsight:     randomHealthInsight.DeepCopy(),
					knownInsights: []string{healthyMonitoringCOInsight.UID, cvuid, unhealthyNetworkCOInsight.UID},
				},
				{
					informer:      "cpi",
					uid:           cvuid,
					cpInsight:     degradedClusterVersionInsight.DeepCopy(),
					knownInsights: []string{healthyMonitoringCOInsight.UID, unhealthyNetworkCOInsight.UID, randomHealthInsight.UID},
				},
			},
			expectedState: &updatestatus.UpdateStatus{
				Status: updatestatus.UpdateStatusStatus{
					ControlPlane: updatestatus.ControlPlane{
						Resource: cvVersionResource,
						Informers: []updatestatus.ControlPlaneInformer{
							{
								Name: "cpi",
								Insights: []updatestatus.ControlPlaneInsight{
									healthyMonitoringCOInsight,
									degradedClusterVersionInsight,
									unhealthyNetworkCOInsight,
									randomHealthInsight,
								},
							},
						},
					},
				},
			},
		},
		{
			name: "messages can come from different informers",
			informerMsg: []informerMsg{
				{
					informer:  "one",
					uid:       cvuid,
					cpInsight: completedClusterVersionInsight.DeepCopy(),
				},
				{
					informer:  "two",
					uid:       randomHealthInsight.UID,
					cpInsight: randomHealthInsight.DeepCopy(),
				},
				{
					informer:  "three",
					uid:       unhealthyNetworkCOInsight.UID,
					cpInsight: unhealthyNetworkCOInsight.DeepCopy(),
				},
			},
			expectedState: &updatestatus.UpdateStatus{
				Status: updatestatus.UpdateStatusStatus{
					ControlPlane: updatestatus.ControlPlane{
						Resource: cvVersionResource,
						Informers: []updatestatus.ControlPlaneInformer{
							{Name: "one", Insights: []updatestatus.ControlPlaneInsight{completedClusterVersionInsight}},
							{Name: "two", Insights: []updatestatus.ControlPlaneInsight{randomHealthInsight}},
							{Name: "three", Insights: []updatestatus.ControlPlaneInsight{unhealthyNetworkCOInsight}},
						},
					},
				},
			},
		},
		{
			name:         "empty informer -> message gets dropped",
			initialState: nil,
			informerMsg: []informerMsg{
				{
					informer:  "",
					uid:       cvuid,
					cpInsight: completedClusterVersionInsight.DeepCopy(),
				},
			},
			expectedState: nil,
		},
		{
			name:         "empty uid -> message gets dropped",
			initialState: nil,
			informerMsg: []informerMsg{
				{
					informer:  "one",
					uid:       "",
					cpInsight: completedClusterVersionInsight.DeepCopy(),
				},
			},
			expectedState: nil,
		},
		{
			name:         "nil insight payload -> message gets dropped",
			initialState: nil,
			informerMsg: []informerMsg{
				{
					informer:  "one",
					uid:       cvuid,
					cpInsight: nil,
					wpInsight: nil,
				},
			},
			expectedState: nil,
		},
		{
			name:         "both cp & wp insights payload -> message gets dropped",
			initialState: nil,
			informerMsg: []informerMsg{
				{
					informer:  "one",
					uid:       cvuid,
					cpInsight: completedClusterVersionInsight.DeepCopy(),
					wpInsight: randomWorkerPoolHealthInsight.DeepCopy(),
				},
			},
			expectedState: nil,
		},
		{
			name: "unknown message gets removed from state",
			initialState: &updatestatus.UpdateStatus{
				Status: updatestatus.UpdateStatusStatus{
					ControlPlane: updatestatus.ControlPlane{
						Resource: cvVersionResource,
						Informers: []updatestatus.ControlPlaneInformer{
							{
								Name:     "one",
								Insights: []updatestatus.ControlPlaneInsight{randomHealthInsight},
							},
						},
					},
				},
			},
			informerMsg: []informerMsg{{
				informer:      "one",
				uid:           cvuid,
				cpInsight:     completedClusterVersionInsight.DeepCopy(),
				knownInsights: nil,
			}},
			expectedState: &updatestatus.UpdateStatus{
				Status: updatestatus.UpdateStatusStatus{
					ControlPlane: updatestatus.ControlPlane{
						Resource: cvVersionResource,
						Informers: []updatestatus.ControlPlaneInformer{
							{
								Name:     "one",
								Insights: []updatestatus.ControlPlaneInsight{completedClusterVersionInsight},
							},
						},
					},
				},
			},
		},
	}

	for _, tc := range testCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			updateStatusClient := fakeupdateclient.NewClientset()

			controller := updateStatusController{
				updateStatuses: updateStatusClient.UpdateV1alpha1().UpdateStatuses(),
			}
			controller.statusApi.Lock()
			controller.statusApi.us = tc.initialState
			controller.statusApi.Unlock()

			startInsightReceiver, sendInsight := controller.setupInsightReceiver()

			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			go func() {
				_ = startInsightReceiver(ctx, newTestSyncContextWithQueue())
			}()

			for _, msg := range tc.informerMsg {
				sendInsight(msg)
			}

			expectedProcessed := len(tc.informerMsg)
			var sawProcessed int
			var diff string
			backoff := wait.Backoff{Duration: 5 * time.Millisecond, Factor: 2, Steps: 10}
			if err := wait.ExponentialBackoff(backoff, func() (bool, error) {
				controller.statusApi.Lock()
				defer controller.statusApi.Unlock()

				sawProcessed = controller.statusApi.processed
				diff = cmp.Diff(tc.expectedState, controller.statusApi.us, compareOnlyStatus)

				return diff == "" && sawProcessed == expectedProcessed, nil
			}); err != nil {
				if diff != "" {
					t.Errorf("controller config map differs from expectedState:\n%s", diff)
				}
				if controller.statusApi.processed != len(tc.informerMsg) {
					t.Errorf("controller processed %d messages, expected %d", controller.statusApi.processed, len(tc.informerMsg))
				}
			}
		})
	}
}

func newTestSyncContextWithQueue() factory.SyncContext {
	return testSyncContext{
		eventRecorder: events.NewInMemoryRecorder("test", clocktesting.NewFakePassiveClock(time.Now())),
		queue:         workqueue.NewTypedRateLimitingQueue(workqueue.DefaultTypedControllerRateLimiter[any]()),
	}
}

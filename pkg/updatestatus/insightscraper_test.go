package updatestatus

import (
	"sync"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/openshift/api/config/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func Test_updateInsightScraper_controlPlane_sync(t *testing.T) {
	var minutesAgo [90]metav1.Time
	for i := 0; i < 90; i++ {
		minutesAgo[i] = metav1.NewTime(time.Now().Add(time.Duration(-i) * time.Minute))
	}

	testCases := []struct {
		name string

		scrapedStatus   controlPlaneUpdateStatus
		scrapedInsights map[string][]v1alpha1.UpdateInsight

		expectedUpdateStatus *v1alpha1.UpdateStatusStatus
	}{
		{
			name: "installation",
			scrapedStatus: controlPlaneUpdateStatus{
				updating: &metav1.Condition{
					Type:               string(v1alpha1.UpdateProgressing),
					Status:             metav1.ConditionTrue,
					LastTransitionTime: minutesAgo[10],
					Reason:             "ClusterVersionProgressing",
					Message:            "Cluster is installing version 4.17.0",
				},
				versions: versions{
					target:          "4.17.0",
					isTargetInstall: true,
				},
				operators: map[string]metav1.Condition{
					"installed": {
						Type:               string(v1alpha1.UpdateProgressing),
						Status:             metav1.ConditionFalse,
						LastTransitionTime: minutesAgo[8],
						Reason:             "Updated",
						Message:            "ClusterOperator version is 4.17.0",
					},
					"installing": {
						Type:               string(v1alpha1.UpdateProgressing),
						Status:             metav1.ConditionTrue,
						LastTransitionTime: minutesAgo[5],
						Reason:             "Updating",
						Message:            "ClusterOperator is installing version 4.17.0",
					},
					"pending": {
						Type:               string(v1alpha1.UpdateProgressing),
						Status:             metav1.ConditionFalse,
						LastTransitionTime: minutesAgo[10],
						Reason:             "Pending",
						Message:            "ClusterOperator will be updated to 4.17.0 but have not started yet",
					},
				},
			},
			expectedUpdateStatus: &v1alpha1.UpdateStatusStatus{
				ControlPlane: v1alpha1.ControlPlaneUpdateStatus{
					ControlPlaneUpdateStatusSummary: v1alpha1.ControlPlaneUpdateStatusSummary{
						Assessment: "Progressing",
						Versions: v1alpha1.ControlPlaneUpdateVersions{
							Target:          "4.17.0",
							IsTargetInstall: true,
						},
						Completion: 33,
						StartedAt:  minutesAgo[10],
					},
					Conditions: []metav1.Condition{
						{
							Type:               string(v1alpha1.UpdateProgressing),
							Status:             metav1.ConditionTrue,
							LastTransitionTime: minutesAgo[10],
							Reason:             "ClusterVersionProgressing",
							Message:            "Cluster is installing version 4.17.0",
						},
					},
				},
			},
		},
		// {
		// 	name: "not updating",
		// },
		// {
		// 	name: "updating",
		// },
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			var insightGetters map[string]getInsightsFunc
			for name, response := range tc.scrapedInsights {
				response := response
				insightGetters[name] = func() []v1alpha1.UpdateInsight {
					return response
				}
			}

			updateStatus := lockedUpdateStatus{
				UpdateStatusStatus: &v1alpha1.UpdateStatusStatus{},
				Mutex:              &sync.Mutex{},
			}

			controller := updateInsightScraper{
				controlPlaneUpdateStatus: controlPlaneUpdateStatus{
					now: func() metav1.Time { return minutesAgo[0] },
				},
				getControlPlaneUpdateStatus: func() controlPlaneUpdateStatus { return tc.scrapedStatus },
				insightsGetters:             insightGetters,
				getUpdateStatus: func() lockedUpdateStatus {
					return updateStatus
				},
			}

			err := controller.sync(nil, nil)
			if err != nil {
				// TODO: Handle errors
				t.Fatalf("unexpected error: %v", err)
			}

			updateStatus.Lock()
			defer updateStatus.Unlock()
			if diff := cmp.Diff(tc.expectedUpdateStatus, updateStatus.UpdateStatusStatus); diff != "" {
				t.Errorf("status differs from expected (-expected +got):\n%s", diff)
			}
		})
	}
}

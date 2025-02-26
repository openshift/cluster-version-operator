package updatestatus

import (
	"context"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/util/workqueue"
	clocktesting "k8s.io/utils/clock/testing"

	updatestatus "github.com/openshift/api/update/v1alpha1"
	fakeupdateclient "github.com/openshift/client-go/update/clientset/versioned/fake"

	"github.com/openshift/library-go/pkg/controller/factory"
	"github.com/openshift/library-go/pkg/operator/events"
)

func Test_updateStatusController(t *testing.T) {
	var now = time.Now()
	var minus90sec = now.Add(-90 * time.Second)
	var minus30sec = now.Add(-30 * time.Second)
	var plus30sec = now.Add(30 * time.Second)
	var plus60sec = now.Add(1 * time.Minute)

	testCases := []struct {
		name string

		before *updateStatusApi

		informerMsg []informerMsg

		expected *updateStatusApi
	}{
		{
			name:        "no messages, no state -> no state",
			before:      &updateStatusApi{cm: nil},
			informerMsg: []informerMsg{},
			expected:    &updateStatusApi{cm: nil},
		},
		{
			name: "no messages, empty state -> empty state",
			before: &updateStatusApi{
				cm: &corev1.ConfigMap{
					Data: map[string]string{},
				},
			},
			expected: &updateStatusApi{
				cm: &corev1.ConfigMap{
					Data: map[string]string{},
				},
			},
		},
		{
			name: "no messages, state -> unchanged state",
			before: &updateStatusApi{
				cm: &corev1.ConfigMap{
					Data: map[string]string{
						"usc.cpi.cv-version": "value",
					},
				},
			},
			expected: &updateStatusApi{
				cm: &corev1.ConfigMap{
					Data: map[string]string{
						"usc.cpi.cv-version": "value",
					},
				},
			},
		},
		{
			name: "one message, no state -> initialize from message",
			before: &updateStatusApi{
				cm: nil,
			},
			informerMsg: []informerMsg{
				{
					informer:  "cpi",
					uid:       "cv-version",
					cpInsight: &updatestatus.ControlPlaneInsight{UID: "cv-version"},
				},
			},
			expected: &updateStatusApi{
				cm: &corev1.ConfigMap{
					Data: map[string]string{
						"usc.cpi.cv-version": "cv-version from cpi",
					},
				},
			},
		},
		{
			name: "messages over time build state over old state",
			before: &updateStatusApi{
				cm: &corev1.ConfigMap{
					Data: map[string]string{
						"usc.cpi.kept":        "kept",
						"usc.cpi.overwritten": "old",
					},
				},
			},
			informerMsg: []informerMsg{
				{
					informer:      "cpi",
					uid:           "new-item",
					cpInsight:     &updatestatus.ControlPlaneInsight{UID: "new-item"},
					knownInsights: []string{"kept", "overwritten"},
				},
				{
					informer:      "cpi",
					uid:           "overwritten",
					cpInsight:     &updatestatus.ControlPlaneInsight{UID: "overwritten"},
					knownInsights: []string{"kept", "new-item"},
				},
				{
					informer:      "cpi",
					uid:           "another",
					cpInsight:     &updatestatus.ControlPlaneInsight{UID: "another"},
					knownInsights: []string{"kept", "overwritten", "new-item"},
				},
				{
					informer:      "cpi",
					uid:           "overwritten",
					cpInsight:     &updatestatus.ControlPlaneInsight{UID: "overwritten"},
					knownInsights: []string{"kept", "new-item", "another"},
				},
			},
			expected: &updateStatusApi{
				cm: &corev1.ConfigMap{
					Data: map[string]string{
						"usc.cpi.kept":        "kept",
						"usc.cpi.new-item":    "new-item from cpi",
						"usc.cpi.another":     "another from cpi",
						"usc.cpi.overwritten": "overwritten from cpi",
					},
				},
			},
		},
		{
			name:   "messages can come from different informers",
			before: &updateStatusApi{},
			informerMsg: []informerMsg{
				{
					informer:  "one",
					uid:       "item",
					cpInsight: &updatestatus.ControlPlaneInsight{UID: "item"},
				},
				{
					informer:  "two",
					uid:       "item",
					cpInsight: &updatestatus.ControlPlaneInsight{UID: "item"},
				},
				{
					informer:  "three",
					uid:       "item",
					wpInsight: &updatestatus.WorkerPoolInsight{UID: "item"},
				},
			},
			expected: &updateStatusApi{
				cm: &corev1.ConfigMap{
					Data: map[string]string{
						"usc.one.item":   "item from one",
						"usc.two.item":   "item from two",
						"usc.three.item": "item from three",
					},
				},
			},
		},
		{
			name: "empty informer -> message gets dropped",
			before: &updateStatusApi{
				cm: nil,
			},
			informerMsg: []informerMsg{
				{
					informer:  "",
					uid:       "item",
					cpInsight: &updatestatus.ControlPlaneInsight{UID: "item"},
				},
			},
			expected: &updateStatusApi{
				cm: nil,
			},
		},
		{
			name: "empty uid -> message gets dropped",
			before: &updateStatusApi{
				cm: nil,
			},
			informerMsg: []informerMsg{
				{
					informer:  "one",
					uid:       "",
					cpInsight: &updatestatus.ControlPlaneInsight{UID: ""},
				},
			},
			expected: &updateStatusApi{
				cm: nil,
			},
		},
		{
			name: "nil insight payload -> message gets dropped",
			before: &updateStatusApi{
				cm: nil,
			},
			informerMsg: []informerMsg{
				{
					informer:  "one",
					uid:       "item",
					cpInsight: nil,
					wpInsight: nil,
				},
			},
			expected: &updateStatusApi{
				cm: nil,
			},
		},
		{
			name: "both cp & wp insights payload -> message gets dropped",
			before: &updateStatusApi{
				cm: nil,
			},
			informerMsg: []informerMsg{
				{
					informer:  "one",
					uid:       "item",
					cpInsight: &updatestatus.ControlPlaneInsight{UID: "item"},
					wpInsight: &updatestatus.WorkerPoolInsight{UID: "item"},
				},
			},
			expected: &updateStatusApi{
				cm: nil,
			},
		},
		{
			name: "unknown insight -> not removed from state immediately but set for expiration",
			before: &updateStatusApi{
				cm: &corev1.ConfigMap{
					Data: map[string]string{
						"usc.one.old": "payload",
					},
				},
			},
			informerMsg: []informerMsg{{
				informer:      "one",
				uid:           "new",
				cpInsight:     &updatestatus.ControlPlaneInsight{UID: "item"},
				knownInsights: nil,
			}},
			expected: &updateStatusApi{
				cm: &corev1.ConfigMap{
					Data: map[string]string{
						"usc.one.old": "payload",
						"usc.one.new": "item from one",
					},
				},
				unknownInsightExpirations: map[string]insightExpirations{
					"one": {"old": plus60sec},
				},
			},
		},
		{
			name: "unknown insight already set for expiration -> not removed from state while not expired yet",
			before: &updateStatusApi{
				cm: &corev1.ConfigMap{
					Data: map[string]string{
						"usc.one.old": "payload",
					},
				},
				unknownInsightExpirations: map[string]insightExpirations{
					"one": {"old": plus30sec},
				},
			},
			informerMsg: []informerMsg{{
				informer:      "one",
				uid:           "new",
				cpInsight:     &updatestatus.ControlPlaneInsight{UID: "item"},
				knownInsights: nil,
			}},
			expected: &updateStatusApi{
				cm: &corev1.ConfigMap{
					Data: map[string]string{
						"usc.one.old": "payload",
						"usc.one.new": "item from one",
					},
				},
				unknownInsightExpirations: map[string]insightExpirations{
					"one": {"old": plus30sec},
				},
			},
		},
		{
			name: "previously unknown insight set for expiration is known again -> kept in state and expire dropped",
			before: &updateStatusApi{
				cm: &corev1.ConfigMap{
					Data: map[string]string{
						"usc.one.old": "payload",
					},
				},
				unknownInsightExpirations: map[string]insightExpirations{
					"one": {"old": minus30sec},
				},
			},
			informerMsg: []informerMsg{{
				informer:      "one",
				uid:           "new",
				cpInsight:     &updatestatus.ControlPlaneInsight{UID: "item"},
				knownInsights: []string{"old"},
			}},
			expected: &updateStatusApi{
				cm: &corev1.ConfigMap{
					Data: map[string]string{
						"usc.one.old": "payload",
						"usc.one.new": "item from one",
					},
				},
				unknownInsightExpirations: nil,
			},
		},
		{
			name: "previously unknown insight expired and never became known again -> dropped from state and expire dropped",
			before: &updateStatusApi{
				cm: &corev1.ConfigMap{
					Data: map[string]string{
						"usc.one.old": "payload",
					},
				},
				unknownInsightExpirations: map[string]insightExpirations{
					"one": {"old": minus90sec},
				},
			},
			informerMsg: []informerMsg{{
				informer:      "one",
				uid:           "new",
				cpInsight:     &updatestatus.ControlPlaneInsight{UID: "item"},
				knownInsights: nil,
			}},
			expected: &updateStatusApi{
				cm: &corev1.ConfigMap{
					Data: map[string]string{
						"usc.one.new": "item from one",
					},
				},
				unknownInsightExpirations: nil,
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			updateClient := fakeupdateclient.NewClientset()

			controller := updateStatusController{
				updateStatuses: updateClient.UpdateV1alpha1().UpdateStatuses(),
				state: updateStatusApi{
					cm:                        tc.before.cm,
					unknownInsightExpirations: tc.before.unknownInsightExpirations,
					now:                       func() time.Time { return now },
				},
			}

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
			var diffConfigMap string
			var diffExpirations string
			backoff := wait.Backoff{Duration: 5 * time.Millisecond, Factor: 2, Steps: 10}
			if err := wait.ExponentialBackoff(backoff, func() (bool, error) {
				controller.state.Lock()
				defer controller.state.Unlock()

				sawProcessed = controller.state.processed
				diffConfigMap = cmp.Diff(tc.expected.cm, controller.state.cm)
				diffExpirations = cmp.Diff(tc.expected.unknownInsightExpirations, controller.state.unknownInsightExpirations)

				return diffConfigMap == "" && diffExpirations == "" && sawProcessed == expectedProcessed, nil
			}); err != nil {
				if diffConfigMap != "" {
					t.Errorf("controller config map differs from expected:\n%s", diffConfigMap)
				}
				if diffExpirations != "" {
					t.Errorf("expirations differ from expected:\n%s", diffExpirations)
				}
				if controller.state.processed != len(tc.informerMsg) {
					t.Errorf("controller processed %d messages, expected %d", controller.state.processed, len(tc.informerMsg))
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

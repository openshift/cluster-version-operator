package updatestatus

import (
	"context"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/openshift/library-go/pkg/controller/factory"
	"github.com/openshift/library-go/pkg/operator/events"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/kubernetes/fake"
	"k8s.io/client-go/util/workqueue"
	clocktesting "k8s.io/utils/clock/testing"
)

func Test_updateStatusController(t *testing.T) {
	var now = time.Now()
	var minus90sec = now.Add(-90 * time.Second)
	var minus30sec = now.Add(-30 * time.Second)
	var plus30sec = now.Add(30 * time.Second)
	var plus60sec = now.Add(1 * time.Minute)

	testCases := []struct {
		name string

		controllerConfigMap *corev1.ConfigMap
		unknownExpirations  map[string]insightExpirations

		informerMsg []informerMsg

		expectedControllerConfigMap *corev1.ConfigMap
		expectedUnknownExpirations  map[string]insightExpirations
	}{
		{
			name:                        "no messages, no state -> no state",
			controllerConfigMap:         nil,
			informerMsg:                 []informerMsg{},
			expectedControllerConfigMap: nil,
		},
		{
			name: "no messages, empty state -> empty state",
			controllerConfigMap: &corev1.ConfigMap{
				Data: map[string]string{},
			},
			expectedControllerConfigMap: &corev1.ConfigMap{
				Data: map[string]string{},
			},
		},
		{
			name: "no messages, state -> unchanged state",
			controllerConfigMap: &corev1.ConfigMap{
				Data: map[string]string{
					"usc.cpi.cv-version": "value",
				},
			},
			expectedControllerConfigMap: &corev1.ConfigMap{
				Data: map[string]string{
					"usc.cpi.cv-version": "value",
				},
			},
		},
		{
			name:                "one message, no state -> initialize from message",
			controllerConfigMap: nil,
			informerMsg: []informerMsg{
				{
					informer: "cpi",
					uid:      "cv-version",
					insight:  []byte("value"),
				},
			},
			expectedControllerConfigMap: &corev1.ConfigMap{
				Data: map[string]string{
					"usc.cpi.cv-version": "value",
				},
			},
		},
		{
			name: "messages over time build state over old state",
			controllerConfigMap: &corev1.ConfigMap{
				Data: map[string]string{
					"usc.cpi.kept":        "kept",
					"usc.cpi.overwritten": "old",
				},
			},
			informerMsg: []informerMsg{
				{
					informer:      "cpi",
					uid:           "new-item",
					insight:       []byte("msg1"),
					knownInsights: []string{"kept", "overwritten"},
				},
				{
					informer:      "cpi",
					uid:           "overwritten",
					insight:       []byte("msg2 (overwritten intermediate)"),
					knownInsights: []string{"kept", "new-item"},
				},
				{
					informer:      "cpi",
					uid:           "another",
					insight:       []byte("msg3"),
					knownInsights: []string{"kept", "overwritten", "new-item"},
				},
				{
					informer:      "cpi",
					uid:           "overwritten",
					insight:       []byte("msg4 (overwritten final)"),
					knownInsights: []string{"kept", "new-item", "another"},
				},
			},
			expectedControllerConfigMap: &corev1.ConfigMap{
				Data: map[string]string{
					"usc.cpi.kept":        "kept",
					"usc.cpi.new-item":    "msg1",
					"usc.cpi.another":     "msg3",
					"usc.cpi.overwritten": "msg4 (overwritten final)",
				},
			},
		},
		{
			name: "messages can come from different informers",
			informerMsg: []informerMsg{
				{
					informer: "one",
					uid:      "item",
					insight:  []byte("msg from informer one"),
				},
				{
					informer: "two",
					uid:      "item",
					insight:  []byte("msg from informer two"),
				},
			},
			expectedControllerConfigMap: &corev1.ConfigMap{
				Data: map[string]string{
					"usc.one.item": "msg from informer one",
					"usc.two.item": "msg from informer two",
				},
			},
		},
		{
			name:                "empty informer -> message gets dropped",
			controllerConfigMap: nil,
			informerMsg: []informerMsg{
				{
					informer: "",
					uid:      "item",
					insight:  []byte("msg from informer one"),
				},
			},
			expectedControllerConfigMap: nil,
		},
		{
			name:                "empty uid -> message gets dropped",
			controllerConfigMap: nil,
			informerMsg: []informerMsg{
				{
					informer: "one",
					uid:      "",
					insight:  []byte("msg from informer one"),
				},
			},
			expectedControllerConfigMap: nil,
		},
		{
			name:                "empty insight payload -> message gets dropped",
			controllerConfigMap: nil,
			informerMsg: []informerMsg{
				{
					informer: "one",
					uid:      "item",
					insight:  []byte{},
				},
			},
			expectedControllerConfigMap: nil,
		},
		{
			name:                "nil insight payload -> message gets dropped",
			controllerConfigMap: nil,
			informerMsg: []informerMsg{
				{
					informer: "one",
					uid:      "item",
					insight:  nil,
				},
			},
			expectedControllerConfigMap: nil,
		},
		{
			name: "unknown insight -> not removed from state immediately but set for expiration",
			controllerConfigMap: &corev1.ConfigMap{
				Data: map[string]string{
					"usc.one.old": "payload",
				},
			},
			informerMsg: []informerMsg{{
				informer:      "one",
				uid:           "new",
				insight:       []byte("new payload"),
				knownInsights: nil,
			}},
			expectedControllerConfigMap: &corev1.ConfigMap{
				Data: map[string]string{
					"usc.one.old": "payload",
					"usc.one.new": "new payload",
				},
			},
			expectedUnknownExpirations: map[string]insightExpirations{
				"one": {"old": plus60sec},
			},
		},
		{
			name: "unknown insight already set for expiration -> not removed from state while not expired yet",
			controllerConfigMap: &corev1.ConfigMap{
				Data: map[string]string{
					"usc.one.old": "payload",
				},
			},
			unknownExpirations: map[string]insightExpirations{
				"one": {"old": plus30sec},
			},
			informerMsg: []informerMsg{{
				informer:      "one",
				uid:           "new",
				insight:       []byte("new payload"),
				knownInsights: nil,
			}},
			expectedControllerConfigMap: &corev1.ConfigMap{
				Data: map[string]string{
					"usc.one.old": "payload",
					"usc.one.new": "new payload",
				},
			},
			expectedUnknownExpirations: map[string]insightExpirations{
				"one": {"old": plus30sec},
			},
		},
		{
			name: "previously unknown insight set for expiration is known again -> kept in state and expire dropped",
			controllerConfigMap: &corev1.ConfigMap{
				Data: map[string]string{
					"usc.one.old": "payload",
				},
			},
			unknownExpirations: map[string]insightExpirations{
				"one": {"old": minus30sec},
			},
			informerMsg: []informerMsg{{
				informer:      "one",
				uid:           "new",
				insight:       []byte("new payload"),
				knownInsights: []string{"old"},
			}},
			expectedControllerConfigMap: &corev1.ConfigMap{
				Data: map[string]string{
					"usc.one.old": "payload",
					"usc.one.new": "new payload",
				},
			},
			expectedUnknownExpirations: nil,
		},
		{
			name: "previously unknown insight expired and never became known again -> dropped from state and expire dropped",
			controllerConfigMap: &corev1.ConfigMap{
				Data: map[string]string{
					"usc.one.old": "payload",
				},
			},
			unknownExpirations: map[string]insightExpirations{
				"one": {"old": minus90sec},
			},
			informerMsg: []informerMsg{{
				informer:      "one",
				uid:           "new",
				insight:       []byte("new payload"),
				knownInsights: nil,
			}},
			expectedControllerConfigMap: &corev1.ConfigMap{
				Data: map[string]string{
					"usc.one.new": "new payload",
				},
			},
			expectedUnknownExpirations: nil,
		},
	}

	for _, tc := range testCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			kubeClient := fake.NewClientset()

			controller := updateStatusController{
				configMaps: kubeClient.CoreV1().ConfigMaps(uscNamespace),
				now:        func() time.Time { return now },
			}
			controller.statusApi.Lock()
			controller.statusApi.cm = tc.controllerConfigMap
			for informer, expirations := range tc.unknownExpirations {
				if controller.statusApi.unknownInsightExpirations == nil {
					controller.statusApi.unknownInsightExpirations = make(map[string]insightExpirations)
				}
				controller.statusApi.unknownInsightExpirations[informer] = expirations
			}
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
			var diffConfigMap string
			var diffExpirations string
			backoff := wait.Backoff{Duration: 5 * time.Millisecond, Factor: 2, Steps: 10}
			if err := wait.ExponentialBackoff(backoff, func() (bool, error) {
				controller.statusApi.Lock()
				defer controller.statusApi.Unlock()

				sawProcessed = controller.statusApi.processed
				diffConfigMap = cmp.Diff(tc.expectedControllerConfigMap, controller.statusApi.cm)
				diffExpirations = cmp.Diff(tc.expectedUnknownExpirations, controller.statusApi.unknownInsightExpirations)

				return diffConfigMap == "" && diffExpirations == "" && sawProcessed == expectedProcessed, nil
			}); err != nil {
				if diffConfigMap != "" {
					t.Errorf("controller config map differs from expected:\n%s", diffConfigMap)
				}
				if diffExpirations != "" {
					t.Errorf("expirations differ from expected:\n%s", diffExpirations)
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

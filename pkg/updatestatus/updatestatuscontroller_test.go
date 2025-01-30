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
)

func Test_updateStatusController(t *testing.T) {
	testCases := []struct {
		name                string
		controllerConfigMap *corev1.ConfigMap

		informerMsg []informerMsg
		expected    *corev1.ConfigMap
	}{
		{
			name:                "no messages, no state -> no state",
			controllerConfigMap: nil,
			informerMsg:         []informerMsg{},
			expected:            nil,
		},
		{
			name: "no messages, empty state -> empty state",
			controllerConfigMap: &corev1.ConfigMap{
				Data: map[string]string{},
			},
			expected: &corev1.ConfigMap{
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
			expected: &corev1.ConfigMap{
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
			expected: &corev1.ConfigMap{
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
					informer: "cpi",
					uid:      "new-item",
					insight:  []byte("msg1"),
				},
				{
					informer: "cpi",
					uid:      "overwritten",
					insight:  []byte("msg2 (overwritten intermediate)"),
				},
				{
					informer: "cpi",
					uid:      "another",
					insight:  []byte("msg3"),
				},
				{
					informer: "cpi",
					uid:      "overwritten",
					insight:  []byte("msg4 (overwritten final)"),
				},
			},
			expected: &corev1.ConfigMap{
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
			expected: &corev1.ConfigMap{
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
			expected: nil,
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
			expected: nil,
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
			expected: nil,
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
			expected: nil,
		},
	}

	for _, tc := range testCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			kubeClient := fake.NewClientset()

			controller := updateStatusController{
				configMaps: kubeClient.CoreV1().ConfigMaps(uscNamespace),
			}
			controller.statusApi.Lock()
			controller.statusApi.cm = tc.controllerConfigMap
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
				diff = cmp.Diff(tc.expected, controller.statusApi.cm)

				return diff == "" && sawProcessed == expectedProcessed, nil
			}); err != nil {
				if diff != "" {
					t.Errorf("controller config map differs from expected:\n%s", diff)
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
		eventRecorder: events.NewInMemoryRecorder("test"),
		queue:         workqueue.NewTypedRateLimitingQueue(workqueue.DefaultTypedControllerRateLimiter[any]()),
	}
}

package cvo

import (
	"fmt"
	"testing"
	"time"

	configv1 "github.com/openshift/api/config/v1"
)

func Test_statusWrapper_Report(t *testing.T) {
	tests := []struct {
		name     string
		previous SyncWorkerStatus
		next     SyncWorkerStatus
		want     bool
	}{
		{
			name:     "skip updates that clear an error and are at an earlier fraction",
			previous: SyncWorkerStatus{Failure: fmt.Errorf("a"), Actual: configv1.Update{Image: "testing"}, Fraction: 0.1},
			next:     SyncWorkerStatus{Actual: configv1.Update{Image: "testing"}},
			want:     false,
		},
		{
			previous: SyncWorkerStatus{Failure: fmt.Errorf("a"), Actual: configv1.Update{Image: "testing"}, Fraction: 0.1},
			next:     SyncWorkerStatus{Actual: configv1.Update{Image: "testing2"}},
			want:     true,
		},
		{
			previous: SyncWorkerStatus{Failure: fmt.Errorf("a"), Actual: configv1.Update{Image: "testing"}},
			next:     SyncWorkerStatus{Actual: configv1.Update{Image: "testing"}},
			want:     true,
		},
		{
			previous: SyncWorkerStatus{Failure: fmt.Errorf("a"), Actual: configv1.Update{Image: "testing"}, Fraction: 0.1},
			next:     SyncWorkerStatus{Failure: fmt.Errorf("a"), Actual: configv1.Update{Image: "testing"}},
			want:     true,
		},
		{
			previous: SyncWorkerStatus{Failure: fmt.Errorf("a"), Actual: configv1.Update{Image: "testing"}, Fraction: 0.1},
			next:     SyncWorkerStatus{Failure: fmt.Errorf("b"), Actual: configv1.Update{Image: "testing"}, Fraction: 0.1},
			want:     true,
		},
		{
			previous: SyncWorkerStatus{Failure: fmt.Errorf("a"), Actual: configv1.Update{Image: "testing"}, Fraction: 0.1},
			next:     SyncWorkerStatus{Failure: fmt.Errorf("b"), Actual: configv1.Update{Image: "testing"}, Fraction: 0.2},
			want:     true,
		},
		{
			previous: SyncWorkerStatus{Actual: configv1.Update{Image: "testing"}},
			next:     SyncWorkerStatus{Actual: configv1.Update{Image: "testing"}},
			want:     true,
		},
		{
			next: SyncWorkerStatus{Actual: configv1.Update{Image: "testing"}},
			want: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := &statusWrapper{
				previousStatus: &tt.previous,
			}
			w.w = &SyncWorker{report: make(chan SyncWorkerStatus, 1)}
			w.Report(tt.next)
			close(w.w.report)
			if tt.want {
				select {
				case evt, ok := <-w.w.report:
					if !ok {
						t.Fatalf("no event")
					}
					if evt != tt.next {
						t.Fatalf("unexpected: %#v", evt)
					}
				}
			} else {
				select {
				case evt, ok := <-w.w.report:
					if ok {
						t.Fatalf("unexpected event: %#v", evt)
					}
				}
			}
		})
	}
}

func Test_runThrottledStatusNotifier(t *testing.T) {
	stopCh := make(chan struct{})
	defer close(stopCh)
	in := make(chan SyncWorkerStatus)
	out := make(chan struct{}, 100)

	go runThrottledStatusNotifier(stopCh, 30*time.Second, 1, in, func() { out <- struct{}{} })

	in <- SyncWorkerStatus{Actual: configv1.Update{Image: "test"}}
	select {
	case <-out:
	case <-time.After(100 * time.Millisecond):
		t.Fatalf("should have not throttled")
	}

	in <- SyncWorkerStatus{Reconciling: true, Actual: configv1.Update{Image: "test"}}
	select {
	case <-out:
	case <-time.After(100 * time.Millisecond):
		t.Fatalf("should have not throttled")
	}

	in <- SyncWorkerStatus{Failure: fmt.Errorf("a"), Reconciling: true, Actual: configv1.Update{Image: "test"}}
	select {
	case <-out:
	case <-time.After(100 * time.Millisecond):
		t.Fatalf("should have not throttled")
	}

	in <- SyncWorkerStatus{Failure: fmt.Errorf("a"), Reconciling: true, Actual: configv1.Update{Image: "test"}}
	select {
	case <-out:
	case <-time.After(100 * time.Millisecond):
		t.Fatalf("should have not throttled")
	}

	in <- SyncWorkerStatus{Failure: fmt.Errorf("a"), Reconciling: true, Actual: configv1.Update{Image: "test"}}
	select {
	case <-out:
		t.Fatalf("should have throttled")
	case <-time.After(100 * time.Millisecond):
	}
}

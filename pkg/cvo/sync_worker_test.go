package cvo

import (
	"context"
	"fmt"
	"reflect"
	"testing"
	"time"

	"k8s.io/client-go/tools/record"

	configv1 "github.com/openshift/api/config/v1"
)

func Test_statusWrapper_ReportProgress(t *testing.T) {
	tests := []struct {
		name         string
		previous     SyncWorkerStatus
		next         SyncWorkerStatus
		want         bool
		wantProgress bool
	}{
		{
			name:     "skip updates that clear an error and are at an earlier fraction",
			previous: SyncWorkerStatus{Failure: fmt.Errorf("a"), Actual: configv1.Release{Image: "testing"}, Fraction: 0.1},
			next:     SyncWorkerStatus{Actual: configv1.Release{Image: "testing"}},
			want:     false,
		},
		{
			previous:     SyncWorkerStatus{Failure: fmt.Errorf("a"), Actual: configv1.Release{Image: "testing"}, Fraction: 0.1},
			next:         SyncWorkerStatus{Actual: configv1.Release{Image: "testing2"}},
			want:         true,
			wantProgress: true,
		},
		{
			previous: SyncWorkerStatus{Failure: fmt.Errorf("a"), Actual: configv1.Release{Image: "testing"}},
			next:     SyncWorkerStatus{Actual: configv1.Release{Image: "testing"}},
			want:     true,
		},
		{
			previous: SyncWorkerStatus{Failure: fmt.Errorf("a"), Actual: configv1.Release{Image: "testing"}, Fraction: 0.1},
			next:     SyncWorkerStatus{Failure: fmt.Errorf("a"), Actual: configv1.Release{Image: "testing"}},
			want:     true,
		},
		{
			previous: SyncWorkerStatus{Failure: fmt.Errorf("a"), Actual: configv1.Release{Image: "testing"}, Fraction: 0.1},
			next:     SyncWorkerStatus{Failure: fmt.Errorf("b"), Actual: configv1.Release{Image: "testing"}, Fraction: 0.1},
			want:     true,
		},
		{
			previous:     SyncWorkerStatus{Failure: fmt.Errorf("a"), Actual: configv1.Release{Image: "testing"}, Fraction: 0.1},
			next:         SyncWorkerStatus{Failure: fmt.Errorf("b"), Actual: configv1.Release{Image: "testing"}, Fraction: 0.2},
			want:         true,
			wantProgress: true,
		},
		{
			previous:     SyncWorkerStatus{Actual: configv1.Release{Image: "testing"}, Completed: 1},
			next:         SyncWorkerStatus{Actual: configv1.Release{Image: "testing"}, Completed: 2},
			want:         true,
			wantProgress: true,
		},
		{
			previous:     SyncWorkerStatus{Actual: configv1.Release{Image: "testing-1"}, Completed: 1},
			next:         SyncWorkerStatus{Actual: configv1.Release{Image: "testing-2"}, Completed: 1},
			want:         true,
			wantProgress: true,
		},
		{
			previous: SyncWorkerStatus{Actual: configv1.Release{Image: "testing"}},
			next:     SyncWorkerStatus{Actual: configv1.Release{Image: "testing"}},
			want:     true,
		},
		{
			next:         SyncWorkerStatus{Actual: configv1.Release{Image: "testing"}},
			want:         true,
			wantProgress: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := &statusWrapper{
				previousStatus: &tt.previous,
			}
			w.w = &SyncWorker{report: make(chan SyncWorkerStatus, 1), eventRecorder: record.NewFakeRecorder(100)}
			w.Report(tt.next)
			close(w.w.report)
			if tt.want {
				select {
				case evt, ok := <-w.w.report:
					if !ok {
						t.Fatalf("no event")
					}
					if tt.wantProgress != (!evt.LastProgress.IsZero()) {
						t.Errorf("unexpected progress timestamp: %#v", evt)
					}
					evt.LastProgress = time.Time{}
					if !reflect.DeepEqual(evt, tt.next) {
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

func Test_statusWrapper_ReportGeneration(t *testing.T) {
	tests := []struct {
		name     string
		previous SyncWorkerStatus
		next     SyncWorkerStatus
		want     int64
	}{{
		previous: SyncWorkerStatus{Generation: 1, Step: "Apply", Fraction: 0.1},
		next:     SyncWorkerStatus{Step: "RetreivePayload"},
		want:     1,
	}, {
		previous: SyncWorkerStatus{Generation: 1, Step: "Apply", Fraction: 0.1},
		next:     SyncWorkerStatus{Generation: 2, Step: "Apply", Fraction: 0.5},
		want:     2,
	}, {
		previous: SyncWorkerStatus{Generation: 5, Step: "Apply", Fraction: 0.7},
		next:     SyncWorkerStatus{Generation: 2, Step: "Apply", Fraction: 0.5},
		want:     2,
	}}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := &statusWrapper{
				previousStatus: &tt.previous,
			}
			w.w = &SyncWorker{report: make(chan SyncWorkerStatus, 1), eventRecorder: record.NewFakeRecorder(100)}
			w.Report(tt.next)
			close(w.w.report)

			select {
			case evt := <-w.w.report:
				if tt.want != evt.Generation {
					t.Fatalf("mismatch: expected generation: %d, got generation: %d", tt.want, evt.Generation)
				}
			}
		})
	}
}
func Test_runThrottledStatusNotifier(t *testing.T) {
	in := make(chan SyncWorkerStatus)
	out := make(chan struct{}, 100)

	ctx := context.Background()
	go runThrottledStatusNotifier(ctx, 30*time.Second, 1, in, func() { out <- struct{}{} })

	in <- SyncWorkerStatus{Actual: configv1.Release{Image: "test"}}
	select {
	case <-out:
	case <-time.After(100 * time.Millisecond):
		t.Fatalf("should have not throttled")
	}

	in <- SyncWorkerStatus{Reconciling: true, Actual: configv1.Release{Image: "test"}}
	select {
	case <-out:
	case <-time.After(100 * time.Millisecond):
		t.Fatalf("should have not throttled")
	}

	in <- SyncWorkerStatus{Failure: fmt.Errorf("a"), Reconciling: true, Actual: configv1.Release{Image: "test"}}
	select {
	case <-out:
	case <-time.After(100 * time.Millisecond):
		t.Fatalf("should have not throttled")
	}

	in <- SyncWorkerStatus{Failure: fmt.Errorf("a"), Reconciling: true, Actual: configv1.Release{Image: "test"}}
	select {
	case <-out:
	case <-time.After(100 * time.Millisecond):
		t.Fatalf("should have not throttled")
	}

	in <- SyncWorkerStatus{Failure: fmt.Errorf("a"), Reconciling: true, Actual: configv1.Release{Image: "test"}}
	select {
	case <-out:
		t.Fatalf("should have throttled")
	case <-time.After(100 * time.Millisecond):
	}
}

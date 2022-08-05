package cvo

import (
	"context"
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/davecgh/go-spew/spew"
	configv1 "github.com/openshift/api/config/v1"
	"github.com/openshift/library-go/pkg/manifest"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/diff"
	"k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/client-go/tools/record"

	"github.com/openshift/cluster-version-operator/pkg/payload"
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
			previous: SyncWorkerStatus{Failure: fmt.Errorf("a"), Actual: configv1.Release{Image: "testing"}, Done: 10, Total: 100},
			next:     SyncWorkerStatus{Actual: configv1.Release{Image: "testing"}},
			want:     false,
		},
		{
			previous:     SyncWorkerStatus{Failure: fmt.Errorf("a"), Actual: configv1.Release{Image: "testing"}, Done: 10, Total: 100},
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
			previous: SyncWorkerStatus{Failure: fmt.Errorf("a"), Actual: configv1.Release{Image: "testing"}, Done: 10, Total: 100},
			next:     SyncWorkerStatus{Failure: fmt.Errorf("a"), Actual: configv1.Release{Image: "testing"}},
			want:     true,
		},
		{
			previous: SyncWorkerStatus{Failure: fmt.Errorf("a"), Actual: configv1.Release{Image: "testing"}, Done: 10, Total: 100},
			next:     SyncWorkerStatus{Failure: fmt.Errorf("b"), Actual: configv1.Release{Image: "testing"}, Done: 10, Total: 100},
			want:     true,
		},
		{
			previous:     SyncWorkerStatus{Failure: fmt.Errorf("a"), Actual: configv1.Release{Image: "testing"}, Done: 10, Total: 100},
			next:         SyncWorkerStatus{Failure: fmt.Errorf("b"), Actual: configv1.Release{Image: "testing"}, Done: 20, Total: 100},
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
				evt, ok := <-w.w.report
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
			} else {
				evt, ok := <-w.w.report
				if ok {
					t.Fatalf("unexpected event: %#v", evt)
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
		previous: SyncWorkerStatus{Generation: 1, Done: 10, Total: 100},
		want:     1,
	}, {
		previous: SyncWorkerStatus{Generation: 1, Done: 10, Total: 100},
		next:     SyncWorkerStatus{Generation: 2, Done: 50, Total: 100},
		want:     2,
	}, {
		previous: SyncWorkerStatus{Generation: 5, Done: 70, Total: 100},
		next:     SyncWorkerStatus{Generation: 2, Done: 50, Total: 100},
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

			evt := <-w.w.report
			if tt.want != evt.Generation {
				t.Fatalf("mismatch: expected generation: %d, got generation: %d", tt.want, evt.Generation)
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

func task(name string, gvk schema.GroupVersionKind) *payload.Task {
	return &payload.Task{
		Manifest: &manifest.Manifest{
			OriginalFilename: fmt.Sprintf("%s.yaml", name),
			GVK:              gvk,
			Obj: &unstructured.Unstructured{
				Object: map[string]interface{}{
					"group":      gvk.Group,
					"apiVersion": gvk.Version,
					"kind":       gvk.Kind,
					"metadata": map[string]interface{}{
						"name": name,
					},
				},
			},
		},
	}
}

func Test_condenseClusterOperators(t *testing.T) {
	coADegradedNone := &payload.UpdateError{
		UpdateEffect:        payload.UpdateEffectNone,
		Reason:              "ClusterOperatorDegraded",
		PluralReason:        "ClusterOperatorsDegraded",
		Message:             "Cluster operator test-co-A is degraded",
		PluralMessageFormat: "Cluster operators %s are degraded",
		Name:                "test-co-A",
		Task:                task("test-co-A", configv1.SchemeGroupVersion.WithKind("ClusterOperator")),
	}
	coANotAvailable := &payload.UpdateError{
		UpdateEffect:        payload.UpdateEffectFail,
		Reason:              "ClusterOperatorNotAvailable",
		PluralReason:        "ClusterOperatorsNotAvailable",
		Message:             "Cluster operator test-co-A is not available",
		PluralMessageFormat: "Cluster operators %s are not available",
		Name:                "test-co-A",
		Task:                task("test-co-A", configv1.SchemeGroupVersion.WithKind("ClusterOperator")),
	}
	coAUpdating := &payload.UpdateError{
		UpdateEffect:        payload.UpdateEffectNone,
		Reason:              "ClusterOperatorUpdating",
		PluralReason:        "ClusterOperatorsUpdating",
		Message:             "Cluster operator test-co-A is updating versions",
		PluralMessageFormat: "Cluster operators %s are updating versions",
		Name:                "test-co-A",
		Task:                task("test-co-A", configv1.SchemeGroupVersion.WithKind("ClusterOperator")),
	}
	coBDegradedFail := &payload.UpdateError{
		UpdateEffect:        payload.UpdateEffectFail,
		Reason:              "ClusterOperatorDegraded",
		PluralReason:        "ClusterOperatorsDegraded",
		Message:             "Cluster operator test-co-B is degraded",
		PluralMessageFormat: "Cluster operators %s are degraded",
		Name:                "test-co-B",
		Task:                task("test-co-B", configv1.SchemeGroupVersion.WithKind("ClusterOperator")),
	}
	coBUpdating := &payload.UpdateError{
		UpdateEffect:        payload.UpdateEffectNone,
		Reason:              "ClusterOperatorUpdating",
		PluralReason:        "ClusterOperatorsUpdating",
		Message:             "Cluster operator test-co-B is updating versions",
		PluralMessageFormat: "Cluster operators %s are updating versions",
		Name:                "test-co-B",
		Task:                task("test-co-B", configv1.SchemeGroupVersion.WithKind("ClusterOperator")),
	}
	coCDegradedFailAfterInterval := &payload.UpdateError{
		UpdateEffect:        payload.UpdateEffectFailAfterInterval,
		Reason:              "ClusterOperatorDegraded",
		PluralReason:        "ClusterOperatorsDegraded",
		Message:             "Cluster operator test-co-C is degraded",
		PluralMessageFormat: "Cluster operators %s are degraded",
		Name:                "test-co-C",
		Task:                task("test-co-C", configv1.SchemeGroupVersion.WithKind("ClusterOperator")),
	}

	tests := []struct {
		name     string
		input    []error
		expected []error
	}{{
		name:     "no errors",
		expected: []error{},
	}, {
		name: "one ClusterOperator, one API",
		input: []error{
			coAUpdating,
			apierrors.NewNotFound(schema.GroupResource{Group: "", Resource: "clusteroperator"}, "test-co-A"),
		},
		expected: []error{
			apierrors.NewNotFound(schema.GroupResource{Group: "", Resource: "clusteroperator"}, "test-co-A"),
			coAUpdating,
		},
	}, {
		name: "two ClusterOperator with different reasons, one API",
		input: []error{
			coBUpdating,
			coANotAvailable,
			apierrors.NewNotFound(schema.GroupResource{Group: "", Resource: "clusteroperator"}, "test-co"),
		},
		expected: []error{
			apierrors.NewNotFound(schema.GroupResource{Group: "", Resource: "clusteroperator"}, "test-co"),
			coANotAvailable, // NotAvailable sorts before Updating
			coBUpdating,
		},
	}, {
		name: "two ClusterOperator with the same reason",
		input: []error{
			coBUpdating,
			coAUpdating,
		},
		expected: []error{
			&payload.UpdateError{
				Nested:              errors.NewAggregate([]error{coBUpdating, coAUpdating}),
				UpdateEffect:        payload.UpdateEffectNone,
				Reason:              "ClusterOperatorsUpdating",
				PluralReason:        "ClusterOperatorsUpdating",
				Message:             "Cluster operators test-co-A, test-co-B are updating versions",
				PluralMessageFormat: "Cluster operators %s are updating versions",
				Name:                "test-co-A, test-co-B",
				Names:               []string{"test-co-A", "test-co-B"},
			},
		},
	}, {
		name: "two ClusterOperator with the same reason, one different",
		input: []error{
			coBUpdating,
			coAUpdating,
			coCDegradedFailAfterInterval,
		},
		expected: []error{
			coCDegradedFailAfterInterval, // Degraded sorts before Updating
			&payload.UpdateError{
				Nested:              errors.NewAggregate([]error{coBUpdating, coAUpdating}),
				UpdateEffect:        payload.UpdateEffectNone,
				Reason:              "ClusterOperatorsUpdating",
				PluralReason:        "ClusterOperatorsUpdating",
				Message:             "Cluster operators test-co-A, test-co-B are updating versions",
				PluralMessageFormat: "Cluster operators %s are updating versions",
				Name:                "test-co-A, test-co-B",
				Names:               []string{"test-co-A", "test-co-B"},
			},
		},
	}, {
		name: "there ClusterOperator with the same reason but different effects",
		input: []error{
			coBDegradedFail,
			coADegradedNone,
			coCDegradedFailAfterInterval,
		},
		expected: []error{
			&payload.UpdateError{
				Nested:              errors.NewAggregate([]error{coBDegradedFail, coADegradedNone, coCDegradedFailAfterInterval}),
				UpdateEffect:        payload.UpdateEffectFail,
				Reason:              "ClusterOperatorsDegraded",
				PluralReason:        "ClusterOperatorsDegraded",
				Message:             "Cluster operators test-co-A, test-co-B, test-co-C are degraded",
				PluralMessageFormat: "Cluster operators %s are degraded",
				Name:                "test-co-A, test-co-B, test-co-C",
				Names:               []string{"test-co-A", "test-co-B", "test-co-C"},
			},
		},
	}, {
		name: "to ClusterOperator with the same reason but None and FailAfterInterval effects",
		input: []error{
			coADegradedNone,
			coCDegradedFailAfterInterval,
		},
		expected: []error{
			&payload.UpdateError{
				Nested:              errors.NewAggregate([]error{coADegradedNone, coCDegradedFailAfterInterval}),
				UpdateEffect:        payload.UpdateEffectFailAfterInterval,
				Reason:              "ClusterOperatorsDegraded",
				PluralReason:        "ClusterOperatorsDegraded",
				Message:             "Cluster operators test-co-A, test-co-C are degraded",
				PluralMessageFormat: "Cluster operators %s are degraded",
				Name:                "test-co-A, test-co-C",
				Names:               []string{"test-co-A", "test-co-C"},
			},
		},
	}}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			actual := condenseClusterOperators(test.input)
			if !reflect.DeepEqual(test.expected, actual) {
				spew.Config.DisableMethods = true
				t.Fatalf("Incorrect value returned -\ndiff: %s\nexpected: %s\nreturned: %s", diff.ObjectReflectDiff(test.expected, actual), spew.Sdump(test.expected), spew.Sdump(actual))
			}
		})
	}
}

func Test_equalDigest(t *testing.T) {
	for _, testCase := range []struct {
		name      string
		pullspecA string
		pullspecB string
		expected  bool
	}{
		{
			name:      "both empty",
			pullspecA: "",
			pullspecB: "",
			expected:  true,
		},
		{
			name:      "A empty",
			pullspecA: "",
			pullspecB: "example.com@sha256:e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
			expected:  false,
		},
		{
			name:      "B empty",
			pullspecA: "example.com@sha256:e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
			pullspecB: "",
			expected:  false,
		},
		{
			name:      "A implicit tag",
			pullspecA: "example.com",
			pullspecB: "example.com@sha256:e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
			expected:  false,
		},
		{
			name:      "B implicit tag",
			pullspecA: "example.com@sha256:e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
			pullspecB: "example.com",
			expected:  false,
		},
		{
			name:      "A by tag",
			pullspecA: "example.com:e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
			pullspecB: "example.com@sha256:e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
			expected:  false,
		},
		{
			name:      "B by tag",
			pullspecA: "example.com@sha256:e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
			pullspecB: "example.com:e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
			expected:  false,
		},
		{
			name:      "identical by tag",
			pullspecA: "example.com:latest",
			pullspecB: "example.com:latest",
			expected:  true,
		},
		{
			name:      "different repositories, same tag",
			pullspecA: "a.example.com:latest",
			pullspecB: "b.example.com:latest",
			expected:  false,
		},
		{
			name:      "different repositories, same digest",
			pullspecA: "a.example.com@sha256:e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
			pullspecB: "b.example.com@sha256:e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
			expected:  true,
		},
		{
			name:      "same repository, different digests",
			pullspecA: "example.com@sha256:e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
			pullspecB: "example.com@sha256:01ba4719c80b6fe911b091a7c05124b64eeece964e09c058ef8f9805daca546b",
			expected:  false,
		},
		{
			name:      "A empty repository, same digest",
			pullspecA: "@sha256:e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
			pullspecB: "example.com@sha256:e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
			expected:  true,
		},
		{
			name:      "B empty repository, same digest",
			pullspecA: "example.com@sha256:e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
			pullspecB: "@sha256:e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
			expected:  true,
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			actual := equalDigest(testCase.pullspecA, testCase.pullspecB)
			if actual != testCase.expected {
				t.Fatalf("got %t, not the expected %t", actual, testCase.expected)
			}
		})
	}
}

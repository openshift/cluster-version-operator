package payload

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/pkg/errors"
	"github.com/prometheus/client_golang/prometheus"

	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/apimachinery/pkg/util/wait"

	"github.com/openshift/cluster-version-operator/lib"
)

var (
	metricPayloadErrors = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "cluster_operator_payload_errors",
		Help: "Report the number of errors encountered applying the payload.",
	}, []string{"version"})
)

func init() {
	prometheus.MustRegister(
		metricPayloadErrors,
	)
}

// ResourceBuilder abstracts how a manifest is created on the server. Introduced for testing.
type ResourceBuilder interface {
	Apply(context.Context, *lib.Manifest, State) error
}

type Task struct {
	Index    int
	Total    int
	Manifest *lib.Manifest
	Requeued int
	Backoff  wait.Backoff
}

func (st *Task) Copy() *Task {
	return &Task{
		Index:    st.Index,
		Total:    st.Total,
		Manifest: st.Manifest,
		Requeued: st.Requeued,
	}
}

func (st *Task) String() string {
	ns := st.Manifest.Object().GetNamespace()
	if len(ns) == 0 {
		return fmt.Sprintf("%s %q (%d of %d)", strings.ToLower(st.Manifest.GVK.Kind), st.Manifest.Object().GetName(), st.Index, st.Total)
	}
	return fmt.Sprintf("%s \"%s/%s\" (%d of %d)", strings.ToLower(st.Manifest.GVK.Kind), ns, st.Manifest.Object().GetName(), st.Index, st.Total)
}

// Run attempts to create the provided object until it succeeds or context is cancelled. It returns the
// last error if context is cancelled.
func (st *Task) Run(ctx context.Context, version string, builder ResourceBuilder, state State) error {
	var lastErr error
	backoff := st.Backoff
	maxDuration := 15 * time.Second // TODO: fold back into Backoff in 1.13
	for {
		// attempt the apply, waiting as long as necessary
		err := builder.Apply(ctx, st.Manifest, state)
		if err == nil {
			return nil
		}

		lastErr = err
		utilruntime.HandleError(errors.Wrapf(err, "error running apply for %s", st))
		metricPayloadErrors.WithLabelValues(version).Inc()

		// TODO: this code will become easier in Kube 1.13 because Backoff now supports max
		d := time.Duration(float64(backoff.Duration) * backoff.Factor)
		if d > maxDuration {
			d = maxDuration
		}
		d = wait.Jitter(d, backoff.Jitter)

		// sleep or wait for cancellation
		select {
		case <-time.After(d):
			continue
		case <-ctx.Done():
			if uerr, ok := lastErr.(*Error); ok {
				uerr.Task = st.Copy()
				return uerr
			}
			return &Error{
				Nested:  lastErr,
				Reason:  "ApplyManifestError",
				Message: fmt.Sprintf("Could not apply %s: %s", st, messageForError(lastErr)),
				Task: st.Copy(),
			}
		}
	}
}

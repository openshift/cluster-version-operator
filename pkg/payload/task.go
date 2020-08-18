package payload

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/pkg/errors"
	"github.com/prometheus/client_golang/prometheus"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
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
	name := st.Manifest.Object().GetName()
	if len(name) == 0 {
		name = st.Manifest.OriginalFilename
	}
	ns := st.Manifest.Object().GetNamespace()
	if len(ns) == 0 {
		return fmt.Sprintf("%s %q (%d of %d)", strings.ToLower(st.Manifest.GVK.Kind), name, st.Index, st.Total)
	}
	return fmt.Sprintf("%s \"%s/%s\" (%d of %d)", strings.ToLower(st.Manifest.GVK.Kind), ns, name, st.Index, st.Total)
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
			if uerr, ok := lastErr.(*UpdateError); ok {
				uerr.Task = st.Copy()
				return uerr
			}
			reason, cause := reasonForPayloadSyncError(lastErr)
			if len(cause) > 0 {
				cause = ": " + cause
			}
			return &UpdateError{
				Nested:  lastErr,
				Reason:  reason,
				Message: fmt.Sprintf("Could not update %s%s", st, cause),

				Task: st.Copy(),
			}
		}
	}
}

// UpdateError is a wrapper for errors that occur during a payload sync.
type UpdateError struct {
	Nested  error
	Reason  string
	Message string
	Name    string

	Task *Task
}

func (e *UpdateError) Error() string {
	return e.Message
}

func (e *UpdateError) Cause() error {
	return e.Nested
}

// reasonForUpdateError provides a succint explanation of a known error type for use in a human readable
// message during update. Since all objects in the image should be successfully applied, messages
// should direct the reader (likely a cluster administrator) to a possible cause in their own config.
func reasonForPayloadSyncError(err error) (string, string) {
	err = errors.Cause(err)
	switch {
	case apierrors.IsNotFound(err), apierrors.IsAlreadyExists(err):
		return "UpdatePayloadResourceNotFound", "resource may have been deleted"
	case apierrors.IsConflict(err):
		return "UpdatePayloadResourceConflict", "someone else is updating this resource"
	case apierrors.IsTimeout(err), apierrors.IsServiceUnavailable(err), apierrors.IsUnexpectedServerError(err):
		return "UpdatePayloadClusterDown", "the server is down or not responding"
	case apierrors.IsInternalError(err):
		return "UpdatePayloadClusterError", "the server is reporting an internal error"
	case apierrors.IsInvalid(err):
		return "UpdatePayloadResourceInvalid", "the object is invalid, possibly due to local cluster configuration"
	case apierrors.IsUnauthorized(err):
		return "UpdatePayloadClusterUnauthorized", "could not authenticate to the server"
	case apierrors.IsForbidden(err):
		return "UpdatePayloadResourceForbidden", "the server has forbidden updates to this resource"
	case apierrors.IsServerTimeout(err), apierrors.IsTooManyRequests(err):
		return "UpdatePayloadClusterOverloaded", "the server is overloaded and is not accepting updates"
	case meta.IsNoMatchError(err):
		return "UpdatePayloadResourceTypeMissing", "the server does not recognize this resource, check extension API servers"
	default:
		return "UpdatePayloadFailed", ""
	}
}

func SummaryForReason(reason, name string) string {
	switch reason {

	// likely temporary errors
	case "UpdatePayloadResourceNotFound", "UpdatePayloadResourceConflict":
		return "some resources could not be updated"
	case "UpdatePayloadClusterDown":
		return "the control plane is down or not responding"
	case "UpdatePayloadClusterError":
		return "the control plane is reporting an internal error"
	case "UpdatePayloadClusterOverloaded":
		return "the control plane is overloaded and is not accepting updates"
	case "UpdatePayloadClusterUnauthorized":
		return "could not authenticate to the server"
	case "UpdatePayloadRetrievalFailed":
		return "could not download the update"

	// likely a policy or other configuration error due to end user action
	case "UpdatePayloadResourceForbidden":
		return "the server is rejecting updates"

	// the image may not be correct, or the cluster may be in an unexpected
	// state
	case "UpdatePayloadResourceTypeMissing":
		return "a required extension is not available to update"
	case "UpdatePayloadResourceInvalid":
		return "some cluster configuration is invalid"
	case "UpdatePayloadIntegrity":
		return "the contents of the update are invalid"

	case "ImageVerificationFailed":
		return "the image may not be safe to use"

	case "UpgradeInProgress":
		return "an upgrade is in progress"

	case "UpgradePreconditionCheckFailed":
		return "it may not be safe to apply this update"

	case "ClusterOperatorDegraded":
		if len(name) > 0 {
			return fmt.Sprintf("the cluster operator %s is degraded", name)
		}
		return "a cluster operator is degraded"
	case "ClusterOperatorNotAvailable":
		if len(name) > 0 {
			return fmt.Sprintf("the cluster operator %s has not yet successfully rolled out", name)
		}
		return "a cluster operator has not yet rolled out"
	case "ClusterOperatorsNotAvailable":
		return "some cluster operators have not yet rolled out"
	case "WorkloadNotAvailable":
		if len(name) > 0 {
			return fmt.Sprintf("the workload %s has not yet successfully rolled out", name)
		}
		return "a workload has not yet rolled out"
	case "WorkloadNotProgressing":
		if len(name) > 0 {
			return fmt.Sprintf("the workload %s cannot roll out", name)
		}
		return "a workload cannot roll out"
	}

	if strings.HasPrefix(reason, "UpdatePayload") {
		return "the update could not be applied"
	}

	if len(name) > 0 {
		return fmt.Sprintf("%s has an unknown error: %s", name, reason)
	}
	return fmt.Sprintf("an unknown error has occurred: %s", reason)
}

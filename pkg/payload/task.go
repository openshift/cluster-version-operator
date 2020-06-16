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
			if uerr, ok := lastErr.(*UpgradeError); ok {
				uerr.Task = st.Copy()
				return uerr
			}
			reason, cause := reasonForPayloadSyncError(lastErr)
			if len(cause) > 0 {
				cause = ": " + cause
			}
			return &UpgradeError{
				Nested:  lastErr,
				Reason:  reason,
				Message: fmt.Sprintf("Could not upgrade %s%s", st, cause),

				Task: st.Copy(),
			}
		}
	}
}

// UpgradeError is a wrapper for errors that occur during a payload sync.
type UpgradeError struct {
	Nested  error
	Reason  string
	Message string
	Name    string

	Task *Task
}

func (e *UpgradeError) Error() string {
	return e.Message
}

func (e *UpgradeError) Cause() error {
	return e.Nested
}

// reasonForUpgradeError provides a succint explanation of a known error type for use in a human readable
// message during upgrade. Since all objects in the image should be successfully applied, messages
// should direct the reader (likely a cluster administrator) to a possible cause in their own config.
func reasonForPayloadSyncError(err error) (string, string) {
	err = errors.Cause(err)
	switch {
	case apierrors.IsNotFound(err), apierrors.IsAlreadyExists(err):
		return "UpgradePayloadResourceNotFound", "resource may have been deleted"
	case apierrors.IsConflict(err):
		return "UpgradePayloadResourceConflict", "someone else is updating this resource"
	case apierrors.IsTimeout(err), apierrors.IsServiceUnavailable(err), apierrors.IsUnexpectedServerError(err):
		return "UpgradePayloadClusterDown", "the server is down or not responding"
	case apierrors.IsInternalError(err):
		return "UpgradePayloadClusterError", "the server is reporting an internal error"
	case apierrors.IsInvalid(err):
		return "UpgradePayloadResourceInvalid", "the object is invalid, possibly due to local cluster configuration"
	case apierrors.IsUnauthorized(err):
		return "UpgradePayloadClusterUnauthorized", "could not authenticate to the server"
	case apierrors.IsForbidden(err):
		return "UpgradePayloadResourceForbidden", "the server has forbidden upgrades to this resource"
	case apierrors.IsServerTimeout(err), apierrors.IsTooManyRequests(err):
		return "UpgradePayloadClusterOverloaded", "the server is overloaded and is not accepting upgrades"
	case meta.IsNoMatchError(err):
		return "UpgradePayloadResourceTypeMissing", "the server does not recognize this resource, check extension API servers"
	default:
		return "UpgradePayloadFailed", ""
	}
}

func SummaryForReason(reason, name string) string {
	switch reason {

	// likely temporary errors
	case "UpgradePayloadResourceNotFound", "UpgradePayloadResourceConflict":
		return "some resources could not be upgraded"
	case "UpgradePayloadClusterDown":
		return "the control plane is down or not responding"
	case "UpgradePayloadClusterError":
		return "the control plane is reporting an internal error"
	case "UpgradePayloadClusterOverloaded":
		return "the control plane is overloaded and is not accepting upgrades"
	case "UpgradePayloadClusterUnauthorized":
		return "could not authenticate to the server"
	case "UpgradePayloadRetrievalFailed":
		return "could not download the upgrade"

	// likely a policy or other configuration error due to end user action
	case "UpgradePayloadResourceForbidden":
		return "the server is rejecting upgrades"

	// the image may not be correct, or the cluster may be in an unexpected
	// state
	case "UpgradePayloadResourceTypeMissing":
		return "a required extension is not available to upgrade"
	case "UpgradePayloadResourceInvalid":
		return "some cluster configuration is invalid"
	case "UpdatePayloadIntegrity":
		return "the contents of the target release image are invalid"

	case "ImageVerificationFailed":
		return "the image may not be safe to use"

	case "UpgradePreconditionCheckFailed":
		return "it may not be safe to apply this upgrade"

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

	if strings.HasPrefix(reason, "UpgradePayload") {
		return "the upgrade could not be applied"
	}

	if len(name) > 0 {
		return fmt.Sprintf("%s has an unknown error: %s", name, reason)
	}
	return fmt.Sprintf("an unknown error has occurred: %s", reason)
}

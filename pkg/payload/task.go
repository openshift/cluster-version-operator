package payload

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/pkg/errors"
	"github.com/prometheus/client_golang/prometheus"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/apimachinery/pkg/util/wait"

	"github.com/openshift/library-go/pkg/manifest"
)

var (
	metricPayloadErrors = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "cluster_operator_payload_errors",
		Help: "Report the number of errors encountered applying the payload.",
	}, []string{"version"})

	clusterOperatorUpdateStartTimes = struct {
		lock sync.RWMutex
		m    map[string]time.Time
	}{m: make(map[string]time.Time)}
)

func init() {
	prometheus.MustRegister(
		metricPayloadErrors,
	)
}

// InitCOUpdateStartTimes creates the clusterOperatorUpdateStartTimes map thereby resulting
// in an empty map.
func InitCOUpdateStartTimes() {
	clusterOperatorUpdateStartTimes.lock.Lock()
	clusterOperatorUpdateStartTimes.m = make(map[string]time.Time)
	clusterOperatorUpdateStartTimes.lock.Unlock()
}

// COUpdateStartTimesEnsure adds name to clusterOperatorUpdateStartTimes map and sets to
// current time if name does not already exist in map.
func COUpdateStartTimesEnsure(name string) {
	COUpdateStartTimesAt(name, time.Now())
}

// COUpdateStartTimesAt adds name to clusterOperatorUpdateStartTimes map and sets to
// t if name does not already exist in map.
func COUpdateStartTimesAt(name string, t time.Time) {
	clusterOperatorUpdateStartTimes.lock.Lock()
	if _, ok := clusterOperatorUpdateStartTimes.m[name]; !ok {
		clusterOperatorUpdateStartTimes.m[name] = t
	}
	clusterOperatorUpdateStartTimes.lock.Unlock()
}

// COUpdateStartTimesRemove removes name from clusterOperatorUpdateStartTimes
func COUpdateStartTimesRemove(name string) {
	clusterOperatorUpdateStartTimes.lock.Lock()
	delete(clusterOperatorUpdateStartTimes.m, name)
	clusterOperatorUpdateStartTimes.lock.Unlock()
}

// COUpdateStartTimesGet returns name's value from clusterOperatorUpdateStartTimes map.
func COUpdateStartTimesGet(name string) time.Time {
	clusterOperatorUpdateStartTimes.lock.Lock()
	defer clusterOperatorUpdateStartTimes.lock.Unlock()
	return clusterOperatorUpdateStartTimes.m[name]
}

func getManifestResourceId(m manifest.Manifest) string {
	name := m.Obj.GetName()
	if len(name) == 0 {
		name = m.OriginalFilename
	}
	ns := m.Obj.GetNamespace()
	if len(ns) == 0 {
		return fmt.Sprintf("%s %q", strings.ToLower(m.GVK.Kind), name)
	}
	return fmt.Sprintf("%s \"%s/%s\"", strings.ToLower(m.GVK.Kind), ns, name)
}

// ResourceBuilder abstracts how a manifest is created on the server. Introduced for testing.
type ResourceBuilder interface {
	Apply(context.Context, *manifest.Manifest, State) error
}

type Task struct {
	Index    int
	Total    int
	Manifest *manifest.Manifest
	Backoff  wait.Backoff
}

func (st *Task) Copy() *Task {
	return &Task{
		Index:    st.Index,
		Total:    st.Total,
		Manifest: st.Manifest,
	}
}

func (st *Task) String() string {
	manId := getManifestResourceId(*st.Manifest)
	return fmt.Sprintf("%s (%d of %d)", manId, st.Index, st.Total)
}

// Run attempts to create the provided object until it:
//
// * Succeeds, or
// * Fails with an UpdateError, because these are unlikely to improve quickly on retry, or
// * The context is canceled, in which case it returns the most recent error.
func (st *Task) Run(ctx context.Context, version string, builder ResourceBuilder, state State) error {
	var lastErr error
	backoff := st.Backoff
	err := wait.ExponentialBackoffWithContext(ctx, backoff, func(_ context.Context) (done bool, err error) {
		err = builder.Apply(ctx, st.Manifest, state)
		if err == nil {
			return true, nil
		}
		lastErr = err
		utilruntime.HandleError(errors.Wrapf(err, "error running apply for %s", st))
		metricPayloadErrors.WithLabelValues(version).Inc()
		if updateErr, ok := err.(*UpdateError); ok {
			updateErr.Task = st.Copy()
			return false, updateErr // failing fast for UpdateError
		}
		return false, nil
	})
	if err == nil {
		return nil
	}
	if lastErr != nil {
		err = lastErr
	}
	if _, ok := err.(*UpdateError); ok {
		return err
	}
	reason, cause := reasonForPayloadSyncError(err)
	if len(cause) > 0 {
		cause = ": " + cause
	}
	return &UpdateError{
		Nested:  err,
		Reason:  reason,
		Message: fmt.Sprintf("Could not update %s%s", st, cause),
		Task:    st.Copy(),
	}
}

// UpdateEffectType defines the effect an update error has on the overall update state.
type UpdateEffectType string

const (
	// UpdateEffectReport defines an error that requires reporting but does not
	// block reconciliation from completing.
	UpdateEffectReport UpdateEffectType = "Report"

	// UpdateEffectNone defines an error as having no affect on the update state.
	UpdateEffectNone UpdateEffectType = "None"

	// UpdateEffectFail defines an error as indicating the update is failing.
	UpdateEffectFail UpdateEffectType = "Fail"

	// UpdateEffectFailAfterInterval defines an error as one which indicates the update
	// is failing if the error continues for a defined interval.
	UpdateEffectFailAfterInterval UpdateEffectType = "FailAfterInterval"
)

// UpdateError is a wrapper for errors that occur during a payload sync.
type UpdateError struct {
	Nested              error
	UpdateEffect        UpdateEffectType
	Reason              string
	PluralReason        string
	Message             string
	PluralMessageFormat string
	Name                string
	Names               []string

	Task *Task
}

func (e *UpdateError) Error() string {
	return e.Message
}

// Cause supports github.com/pkg/errors.Cause [1].
//
// [1]: https://pkg.go.dev/github.com/pkg/errors#readme-retrieving-the-cause-of-an-error
func (e *UpdateError) Cause() error {
	return e.Nested
}

// Unwrap supports errors.Unwrap [1].
//
// [1]: https://pkg.go.dev/errors#Unwrap
func (e *UpdateError) Unwrap() error {
	return e.Nested
}

// reasonForPayloadSyncError provides a succint explanation of a known error type for use in a human readable
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

	case "UpgradePreconditionCheckFailed":
		return "it may not be safe to apply this update"

	case "ClusterOperatorDegraded":
		if len(name) > 0 {
			return fmt.Sprintf("the cluster operator %s is degraded", name)
		}
		return "a cluster operator is degraded"
	case "ClusterOperatorNotAvailable":
		if len(name) > 0 {
			return fmt.Sprintf("the cluster operator %s is not available", name)
		}
		return "a cluster operator is not available"
	case "ClusterOperatorsNotAvailable":
		return "some cluster operators are not available"
	case "ClusterOperatorNoVersions":
		if len(name) > 0 {
			return fmt.Sprintf("the cluster operator %s does not declare expected versions", name)
		}
		return "a cluster operator does not declare expected versions"
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

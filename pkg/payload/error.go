package payload

import (
	"fmt"
	"strings"

	"github.com/pkg/errors"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
)

// Error is a wrapper for errors that occur during a payload sync.
type Error struct {
	Nested  error
	Reason  string
	Message string
	Name    string

	Task *Task
}

func (e *Error) Error() string {
	return e.Message
}

func (e *Error) Cause() error {
	return e.Nested
}

func (e *Error) Summary() string {
	switch e.Reason {

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
	case "LoadManifestsError":
		return "failed to load manifests from the release image"

	case "ImageVerificationFailed":
		return "the image may not be safe to use"

	case "ClusterOperatorDegraded":
		if len(e.Name) > 0 {
			return fmt.Sprintf("the cluster operator %s is degraded", e.Name)
		}
		return "a cluster operator is degraded"
	case "ClusterOperatorNotAvailable":
		if len(e.Name) > 0 {
			return fmt.Sprintf("the cluster operator %s has not yet successfully rolled out", e.Name)
		}
		return "a cluster operator has not yet rolled out"
	case "ClusterOperatorsNotAvailable":
		return "some cluster operators have not yet rolled out"
	}

	if strings.HasPrefix(e.Reason, "UpdatePayload") {
		return "the update could not be applied"
	}
	return "an unknown error has occurred"
}

// reasonForError provides a succint explanation of a known error type
// for use in a human readable message.  Since all objects in the image
// should be successfully applied, messages should direct the reader
// (likely a cluster administrator) to a possible cause in their own
// config.
func reasonForError(err error) (string, string) {
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

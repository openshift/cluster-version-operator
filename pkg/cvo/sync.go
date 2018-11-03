package cvo

import (
	"fmt"
	"strings"
	"time"

	"github.com/golang/glog"
	"github.com/pkg/errors"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/apimachinery/pkg/util/wait"

	"github.com/openshift/cluster-version-operator/lib"
	"github.com/openshift/cluster-version-operator/lib/resourcebuilder"
	cvv1 "github.com/openshift/cluster-version-operator/pkg/apis/config.openshift.io/v1"
	"github.com/openshift/cluster-version-operator/pkg/cvo/internal"
)

// loadUpdatePayload reads the payload from disk or remote, as necessary.
func (optr *Operator) loadUpdatePayload(config *cvv1.ClusterVersion) (*updatePayload, error) {
	payloadDir, err := optr.updatePayloadDir(config)
	if err != nil {
		return nil, err
	}
	releaseImage := optr.releaseImage
	if config.Spec.DesiredUpdate != nil {
		releaseImage = config.Spec.DesiredUpdate.Payload
	}
	return loadUpdatePayload(payloadDir, releaseImage)
}

// syncUpdatePayload applies the manifests in the payload to the cluster.
func (optr *Operator) syncUpdatePayload(config *cvv1.ClusterVersion, payload *updatePayload) error {
	version := payload.releaseVersion
	if len(version) == 0 {
		version = payload.releaseImage
	}
	for i, manifest := range payload.manifests {
		metricPayload.WithLabelValues(version, "pending").Set(float64(len(payload.manifests) - i))
		metricPayload.WithLabelValues(version, "applied").Set(float64(i))
		taskName := taskName(&manifest, i+1, len(payload.manifests))
		glog.V(4).Infof("Running sync for %s", taskName)
		glog.V(6).Infof("Manifest: %s", string(manifest.Raw))

		ov, ok := getOverrideForManifest(config.Spec.Overrides, manifest)
		if ok && ov.Unmanaged {
			glog.V(4).Infof("Skipping %s as unmanaged", taskName)
			continue
		}

		var lastErr error
		if err := wait.ExponentialBackoff(wait.Backoff{
			Duration: time.Second * 10,
			Factor:   1.3,
			Steps:    3,
		}, func() (bool, error) {
			// build resource builder for manifest
			var b resourcebuilder.Interface
			var err error
			if resourcebuilder.Mapper.Exists(manifest.GVK) {
				b, err = resourcebuilder.New(resourcebuilder.Mapper, optr.restConfig, manifest)
			} else {
				b, err = internal.NewGenericBuilder(optr.restConfig, manifest)
			}
			if err != nil {
				utilruntime.HandleError(fmt.Errorf("error creating resourcebuilder for %s: %v", taskName, err))
				lastErr = err
				metricPayloadErrors.WithLabelValues(version).Inc()
				return false, nil
			}
			// run builder for the manifest
			if err := b.Do(); err != nil {
				utilruntime.HandleError(fmt.Errorf("error running apply for %s: %v", taskName, err))
				lastErr = err
				metricPayloadErrors.WithLabelValues(version).Inc()
				return false, nil
			}
			return true, nil
		}); err != nil {
			reason, cause := reasonForPayloadSyncError(lastErr)
			if len(cause) > 0 {
				cause = ": " + cause
			}
			return &updateError{
				Reason:  reason,
				Message: fmt.Sprintf("Could not update %s%s", taskName, cause),
			}
		}

		glog.V(4).Infof("Done syncing for %s", taskName)
	}
	metricPayload.WithLabelValues(version, "applied").Set(float64(len(payload.manifests)))
	metricPayload.WithLabelValues(version, "pending").Set(0)
	return nil
}

type updateError struct {
	Reason  string
	Message string
}

func (e *updateError) Error() string {
	return e.Message
}

// reasonForUpdateError provides a succint explanation of a known error type for use in a human readable
// message during update. Since all objects in the payload should be successfully applied, messages
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

func summaryForReason(reason string) string {
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

	// the payload may not be correct, or the cluster may be in an unexpected
	// state
	case "UpdatePayloadResourceTypeMissing":
		return "a required extension is not available to update"
	case "UpdatePayloadResourceInvalid":
		return "some cluster configuration is invalid"
	case "UpdatePayloadIntegrity":
		return "the contents of the update are invalid"
	}
	if strings.HasPrefix(reason, "UpdatePayload") {
		return "the update could not be applied"
	}
	return "an unknown error has occurred"
}

func taskName(manifest *lib.Manifest, index, total int) string {
	ns := manifest.Object().GetNamespace()
	if len(ns) == 0 {
		return fmt.Sprintf("%s %q (%s, %d of %d)", strings.ToLower(manifest.GVK.Kind), manifest.Object().GetName(), manifest.GVK.GroupVersion().String(), index, total)
	}
	return fmt.Sprintf("%s \"%s/%s\" (%s, %d of %d)", strings.ToLower(manifest.GVK.Kind), ns, manifest.Object().GetName(), manifest.GVK.GroupVersion().String(), index, total)
}

// getOverrideForManifest returns the override and true when override exists for manifest.
func getOverrideForManifest(overrides []cvv1.ComponentOverride, manifest lib.Manifest) (cvv1.ComponentOverride, bool) {
	for idx, ov := range overrides {
		kind, namespace, name := manifest.GVK.Kind, manifest.Object().GetNamespace(), manifest.Object().GetName()
		if ov.Kind == kind &&
			(namespace == "" || ov.Namespace == namespace) && // cluster-scoped objects don't have namespace.
			ov.Name == name {
			return overrides[idx], true
		}
	}
	return cvv1.ComponentOverride{}, false
}

func ownerRefModifier(config *cvv1.ClusterVersion) resourcebuilder.MetaV1ObjectModifierFunc {
	oref := metav1.NewControllerRef(config, ownerKind)
	return func(obj metav1.Object) {
		obj.SetOwnerReferences([]metav1.OwnerReference{*oref})
	}
}

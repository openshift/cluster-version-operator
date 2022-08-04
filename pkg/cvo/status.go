package cvo

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"k8s.io/klog/v2"

	"k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/diff"
	"k8s.io/apimachinery/pkg/util/validation/field"

	configv1 "github.com/openshift/api/config/v1"
	configclientv1 "github.com/openshift/client-go/config/clientset/versioned/typed/config/v1"

	"github.com/openshift/cluster-version-operator/lib/resourcemerge"
	"github.com/openshift/cluster-version-operator/pkg/payload"
)

const (
	// ClusterStatusFailing is set on the ClusterVersion status when a cluster
	// cannot reach the desired state. It is considered more serious than Degraded
	// and indicates the cluster is not healthy.
	ClusterStatusFailing = configv1.ClusterStatusConditionType("Failing")

	// MaxHistory is the maximum size of ClusterVersion history. Once exceeded
	// ClusterVersion history will be pruned.
	MaxHistory = 50
)

func mergeEqualVersions(current *configv1.UpdateHistory, desired configv1.Release) bool {
	if len(desired.Image) > 0 && desired.Image == current.Image {
		if len(desired.Version) == 0 {
			return true
		}
		if len(current.Version) == 0 || desired.Version == current.Version {
			current.Version = desired.Version
			return true
		}
	}
	if len(desired.Version) > 0 && desired.Version == current.Version {
		if len(current.Image) == 0 || desired.Image == current.Image {
			current.Image = desired.Image
			return true
		}
	}
	return false
}

func mergeOperatorHistory(config *configv1.ClusterVersion, desired configv1.Release, verified bool, now metav1.Time, completed bool) {
	// if we have no image, we cannot reproduce the update later and so it cannot be part of the history
	if len(desired.Image) == 0 {
		// make the array empty
		if config.Status.History == nil {
			config.Status.History = []configv1.UpdateHistory{}
		}
		return
	}

	if len(config.Status.History) == 0 {
		klog.V(2).Infof("initialize new history completed=%t desired=%#v", completed, desired)
		config.Status.History = append(config.Status.History, configv1.UpdateHistory{
			Version: desired.Version,
			Image:   desired.Image,

			State:       configv1.PartialUpdate,
			StartedTime: now,
		})
	}

	last := &config.Status.History[0]

	if len(last.State) == 0 {
		last.State = configv1.PartialUpdate
	}

	if mergeEqualVersions(last, desired) {
		klog.V(2).Infof("merge into existing history completed=%t desired=%#v last=%#v", completed, desired, last)
		if completed {
			last.State = configv1.CompletedUpdate
			if last.CompletionTime == nil {
				last.CompletionTime = &now
			}
		}
	} else {
		klog.V(2).Infof("must add a new history entry completed=%t desired=%#v != last=%#v", completed, desired, last)
		if last.CompletionTime == nil {
			last.CompletionTime = &now
		}
		if completed {
			config.Status.History = append([]configv1.UpdateHistory{
				{
					Version: desired.Version,
					Image:   desired.Image,

					State:          configv1.CompletedUpdate,
					StartedTime:    now,
					CompletionTime: &now,
				},
			}, config.Status.History...)
		} else {
			config.Status.History = append([]configv1.UpdateHistory{
				{
					Version: desired.Version,
					Image:   desired.Image,

					State:       configv1.PartialUpdate,
					StartedTime: now,
				},
			}, config.Status.History...)
		}
	}

	// leave this here in case we find other future history bugs and need to debug it
	if klog.V(2).Enabled() && len(config.Status.History) > 1 {
		if config.Status.History[0].Image == config.Status.History[1].Image && config.Status.History[0].Version == config.Status.History[1].Version {
			data, _ := json.MarshalIndent(config.Status.History, "", "  ")
			panic(fmt.Errorf("tried to update cluster version history to contain duplicate image entries: %s", string(data)))
		}
	}

	// payloads can be verified during sync
	if verified {
		config.Status.History[0].Verified = true
	}

	// TODO: prune Z versions over transitions to Y versions, keep initial installed version
	pruneStatusHistory(config, MaxHistory)

	config.Status.Desired = desired
}

// pruneStatusHistory maintains history size at MaxHistory by removing entry at index MaxHistory
// unless that entry is a completed update in which case entry at MaxHistory-1 is removed thereby
// retaining the initial completed version.
func pruneStatusHistory(config *configv1.ClusterVersion, maxHistory int) {
	if len(config.Status.History) <= maxHistory {
		return
	}
	if config.Status.History[maxHistory].State == configv1.CompletedUpdate {
		item := config.Status.History[maxHistory]
		config.Status.History = config.Status.History[0 : maxHistory-1]
		config.Status.History = append(config.Status.History, item)
	} else {
		config.Status.History = config.Status.History[:maxHistory]
	}
}

// ClusterVersionInvalid indicates that the cluster version has an error that prevents the server from
// taking action. The cluster version operator will only reconcile the current state as long as this
// condition is set.
const ClusterVersionInvalid configv1.ClusterStatusConditionType = "Invalid"

// DesiredReleaseAccepted indicates whether the requested (desired) release payload was successfully loaded
// and no failures occurred during image verification and precondition checking.
const DesiredReleaseAccepted configv1.ClusterStatusConditionType = "ReleaseAccepted"

// ImplicitlyEnabledCapabilities is True if there are enabled capabilities which the user is not currently
// requesting via spec.capabilities, because the cluster version operator does not support uninstalling
// capabilities if any associated resources were previously managed by the CVO or disabling previousily
// enabled capabilities.
const ImplicitlyEnabledCapabilities configv1.ClusterStatusConditionType = "ImplicitlyEnabledCapabilities"

// syncStatus calculates the new status of the ClusterVersion based on the current sync state and any
// validation errors found. We allow the caller to pass the original object to avoid DeepCopying twice.
func (optr *Operator) syncStatus(ctx context.Context, original, config *configv1.ClusterVersion, status *SyncWorkerStatus, validationErrs field.ErrorList) error {
	klog.V(2).Infof("Synchronizing status errs=%#v status=%#v", validationErrs, status)

	cvUpdated := false
	// update the config with the latest available updates
	if updated := optr.getAvailableUpdates().NeedsUpdate(config); updated != nil {
		cvUpdated = true
		config = updated
	}
	// update the config with upgradeable
	if updated := optr.getUpgradeable().NeedsUpdate(config); updated != nil {
		cvUpdated = true
		config = updated
	}
	if !cvUpdated && (original == nil || original == config) {
		original = config.DeepCopy()
	}

	config.Status.ObservedGeneration = status.Generation
	if len(status.VersionHash) > 0 {
		config.Status.VersionHash = status.VersionHash
	}

	now := metav1.Now()
	version := versionStringFromRelease(status.Actual)
	if status.Actual.Image == optr.release.Image {
		// backfill any missing information from the operator (payload).
		if status.Actual.Version == "" {
			status.Actual.Version = optr.release.Version
		}
		if len(status.Actual.URL) == 0 {
			status.Actual.URL = optr.release.URL
		}
		if status.Actual.Channels == nil {
			status.Actual.Channels = append(optr.release.Channels[:0:0], optr.release.Channels...) // copy
		}
	}
	desired := optr.mergeReleaseMetadata(status.Actual)
	mergeOperatorHistory(config, desired, status.Verified, now, status.Completed > 0)

	config.Status.Capabilities = status.CapabilitiesStatus.Status

	// update validation errors
	var reason string
	if len(validationErrs) > 0 {
		buf := &bytes.Buffer{}
		if len(validationErrs) == 1 {
			fmt.Fprintf(buf, "The cluster version is invalid: %s", validationErrs[0].Error())
		} else {
			fmt.Fprintf(buf, "The cluster version is invalid:\n")
			for _, err := range validationErrs {
				fmt.Fprintf(buf, "* %s\n", err.Error())
			}
		}
		reason = "InvalidClusterVersion"

		resourcemerge.SetOperatorStatusCondition(&config.Status.Conditions, configv1.ClusterOperatorStatusCondition{
			Type:               ClusterVersionInvalid,
			Status:             configv1.ConditionTrue,
			Reason:             reason,
			Message:            buf.String(),
			LastTransitionTime: now,
		})
	} else {
		resourcemerge.RemoveOperatorStatusCondition(&config.Status.Conditions, ClusterVersionInvalid)
	}

	// set the implicitly enabled capabilities condition
	setImplicitlyEnabledCapabilitiesCondition(config, status.CapabilitiesStatus.ImplicitlyEnabledCaps, now)

	// set the desired release accepted condition
	setDesiredReleaseAcceptedCondition(config, status.loadPayloadStatus, now)

	// set the available condition
	if status.Completed > 0 {
		resourcemerge.SetOperatorStatusCondition(&config.Status.Conditions, configv1.ClusterOperatorStatusCondition{
			Type:               configv1.OperatorAvailable,
			Status:             configv1.ConditionTrue,
			Message:            fmt.Sprintf("Done applying %s", version),
			LastTransitionTime: now,
		})
	}
	// default the available condition if not set
	if resourcemerge.FindOperatorStatusCondition(config.Status.Conditions, configv1.OperatorAvailable) == nil {
		resourcemerge.SetOperatorStatusCondition(&config.Status.Conditions, configv1.ClusterOperatorStatusCondition{
			Type:               configv1.OperatorAvailable,
			Status:             configv1.ConditionFalse,
			LastTransitionTime: now,
		})
	}

	progressReason, progressMessage, skipFailure := convertErrorToProgressing(config.Status.History, now.Time, status)

	if err := status.Failure; err != nil && !skipFailure {
		var reason string
		msg := progressMessage
		if uErr, ok := err.(*payload.UpdateError); ok {
			reason = uErr.Reason
			if msg == "" {
				msg = payload.SummaryForReason(reason, uErr.Name)
			}
		} else if msg == "" {
			msg = "an error occurred"
		}

		// set the failing condition
		resourcemerge.SetOperatorStatusCondition(&config.Status.Conditions, configv1.ClusterOperatorStatusCondition{
			Type:               ClusterStatusFailing,
			Status:             configv1.ConditionTrue,
			Reason:             reason,
			Message:            err.Error(),
			LastTransitionTime: now,
		})

		// update progressing
		if status.Reconciling {
			resourcemerge.SetOperatorStatusCondition(&config.Status.Conditions, configv1.ClusterOperatorStatusCondition{
				Type:               configv1.OperatorProgressing,
				Status:             configv1.ConditionFalse,
				Reason:             reason,
				Message:            fmt.Sprintf("Error while reconciling %s: %s", version, msg),
				LastTransitionTime: now,
			})
		} else {
			resourcemerge.SetOperatorStatusCondition(&config.Status.Conditions, configv1.ClusterOperatorStatusCondition{
				Type:               configv1.OperatorProgressing,
				Status:             configv1.ConditionTrue,
				Reason:             reason,
				Message:            fmt.Sprintf("Unable to apply %s: %s", version, msg),
				LastTransitionTime: now,
			})
		}

	} else {
		// clear the failure condition
		resourcemerge.SetOperatorStatusCondition(&config.Status.Conditions, configv1.ClusterOperatorStatusCondition{Type: ClusterStatusFailing, Status: configv1.ConditionFalse, LastTransitionTime: now})

		// update progressing
		if status.Reconciling {
			message := fmt.Sprintf("Cluster version is %s", version)
			if len(validationErrs) > 0 {
				message = fmt.Sprintf("Stopped at %s: the cluster version is invalid", version)
			}
			resourcemerge.SetOperatorStatusCondition(&config.Status.Conditions, configv1.ClusterOperatorStatusCondition{
				Type:               configv1.OperatorProgressing,
				Status:             configv1.ConditionFalse,
				Reason:             reason,
				Message:            message,
				LastTransitionTime: now,
			})
		} else {
			var message string
			fractionComplete := float32(status.Done) / float32(status.Total)
			switch {
			case len(validationErrs) > 0:
				message = fmt.Sprintf("Reconciling %s: the cluster version is invalid", version)
			case fractionComplete > 0 && skipFailure:
				reason = progressReason
				message = fmt.Sprintf("Working towards %s: %d of %d done (%.0f%% complete), %s", version,
					status.Done, status.Total, math.Trunc(float64(fractionComplete*100)), progressMessage)
			case fractionComplete > 0:
				message = fmt.Sprintf("Working towards %s: %d of %d done (%.0f%% complete)", version,
					status.Done, status.Total, math.Trunc(float64(fractionComplete*100)))
			case skipFailure:
				reason = progressReason
				message = fmt.Sprintf("Working towards %s: %s", version, progressMessage)
			default:
				message = fmt.Sprintf("Working towards %s", version)
			}
			resourcemerge.SetOperatorStatusCondition(&config.Status.Conditions, configv1.ClusterOperatorStatusCondition{
				Type:               configv1.OperatorProgressing,
				Status:             configv1.ConditionTrue,
				Reason:             reason,
				Message:            message,
				LastTransitionTime: now,
			})
		}
	}

	// default retrieved updates if it is not set
	if resourcemerge.FindOperatorStatusCondition(config.Status.Conditions, configv1.RetrievedUpdates) == nil {
		resourcemerge.SetOperatorStatusCondition(&config.Status.Conditions, configv1.ClusterOperatorStatusCondition{
			Type:               configv1.RetrievedUpdates,
			Status:             configv1.ConditionFalse,
			LastTransitionTime: now,
		})
	}

	if klog.V(6).Enabled() {
		klog.Infof("Apply config: %s", diff.ObjectReflectDiff(original, config))
	}
	updated, err := applyClusterVersionStatus(ctx, optr.client.ConfigV1(), config, original)
	optr.rememberLastUpdate(updated)
	return err
}

func setImplicitlyEnabledCapabilitiesCondition(config *configv1.ClusterVersion, implicitlyEnabled []configv1.ClusterVersionCapability,
	now metav1.Time) {

	if len(implicitlyEnabled) > 0 {
		message := "The following capabilities could not be disabled: "
		caps := make([]string, len(implicitlyEnabled))
		for i, c := range implicitlyEnabled {
			caps[i] = string(c)
		}
		sort.Strings(caps)
		message = message + strings.Join([]string(caps), ", ")

		resourcemerge.SetOperatorStatusCondition(&config.Status.Conditions, configv1.ClusterOperatorStatusCondition{
			Type:               ImplicitlyEnabledCapabilities,
			Status:             configv1.ConditionTrue,
			Reason:             "CapabilitiesImplicitlyEnabled",
			Message:            message,
			LastTransitionTime: now,
		})
	} else {
		resourcemerge.SetOperatorStatusCondition(&config.Status.Conditions, configv1.ClusterOperatorStatusCondition{
			Type:               ImplicitlyEnabledCapabilities,
			Status:             configv1.ConditionFalse,
			Reason:             "AsExpected",
			Message:            "Capabilities match configured spec",
			LastTransitionTime: now,
		})
	}
}

func setDesiredReleaseAcceptedCondition(config *configv1.ClusterVersion, status LoadPayloadStatus, now metav1.Time) {
	if status.Step == "PayloadLoaded" {
		resourcemerge.SetOperatorStatusCondition(&config.Status.Conditions, configv1.ClusterOperatorStatusCondition{
			Type:               DesiredReleaseAccepted,
			Status:             configv1.ConditionTrue,
			Reason:             status.Step,
			Message:            status.Message,
			LastTransitionTime: now,
		})
	} else if status.Step != "" {
		if status.Failure != nil {
			resourcemerge.SetOperatorStatusCondition(&config.Status.Conditions, configv1.ClusterOperatorStatusCondition{
				Type:               DesiredReleaseAccepted,
				Status:             configv1.ConditionFalse,
				Reason:             status.Step,
				Message:            status.Message,
				LastTransitionTime: now,
			})
		} else {
			resourcemerge.SetOperatorStatusCondition(&config.Status.Conditions, configv1.ClusterOperatorStatusCondition{
				Type:               DesiredReleaseAccepted,
				Status:             configv1.ConditionUnknown,
				Reason:             status.Step,
				Message:            status.Message,
				LastTransitionTime: now,
			})
		}
	}
}

// convertErrorToProgressing returns true if the provided status indicates a failure condition can be interpreted as
// still making internal progress. The general error we try to suppress is an operator or operators still being
// unavailable AND the general payload task making progress towards its goal. The error's UpdateEffect determines
// whether an error should be considered a failure and, if so, whether the operator should be given up to 40 minutes
// to recover from the error.
func convertErrorToProgressing(history []configv1.UpdateHistory, now time.Time, status *SyncWorkerStatus) (reason string, message string, ok bool) {
	if len(history) == 0 || status.Failure == nil || status.Reconciling {
		return "", "", false
	}
	uErr, ok := status.Failure.(*payload.UpdateError)
	if !ok {
		return "", "", false
	}
	switch uErr.UpdateEffect {
	case payload.UpdateEffectNone:
		return uErr.Reason, fmt.Sprintf("waiting on %s", uErr.Name), true
	case payload.UpdateEffectFail:
		return "", "", false
	case payload.UpdateEffectFailAfterInterval:
		var exceeded []string
		threshold := now.Add(-(40 * time.Minute))
		for _, name := range strings.Split(uErr.Name, ", ") {
			if payload.COUpdateStartTimesGet(name).Before(threshold) {
				exceeded = append(exceeded, name)
			}
		}
		if len(exceeded) > 0 {
			return uErr.Reason, fmt.Sprintf("wait has exceeded 40 minutes for these operators: %s", strings.Join(exceeded, ", ")), false
		} else {
			return uErr.Reason, fmt.Sprintf("waiting up to 40 minutes on %s", uErr.Name), true
		}
	}
	return "", "", false
}

// syncFailingStatus handles generic errors in the cluster version. It tries to preserve
// all status fields that it can by using the provided config or loading the latest version
// from the cache (instead of clearing the status).
// if ierr is nil, return nil
// if ierr is not nil, update OperatorStatus as Failing and return ierr
func (optr *Operator) syncFailingStatus(ctx context.Context, original *configv1.ClusterVersion, ierr error) error {
	if ierr == nil {
		return nil
	}

	// try to reuse the most recent status if available
	if original == nil {
		original, _ = optr.cvLister.Get(optr.name)
	}
	if original == nil {
		original = &configv1.ClusterVersion{
			ObjectMeta: metav1.ObjectMeta{
				Name: optr.name,
			},
		}
	}

	config := original.DeepCopy()

	now := metav1.Now()
	msg := fmt.Sprintf("Error ensuring the cluster version is up to date: %v", ierr)

	// clear the available condition
	resourcemerge.SetOperatorStatusCondition(&config.Status.Conditions, configv1.ClusterOperatorStatusCondition{Type: configv1.OperatorAvailable, Status: configv1.ConditionFalse, LastTransitionTime: now})

	// reset the failing message
	resourcemerge.SetOperatorStatusCondition(&config.Status.Conditions, configv1.ClusterOperatorStatusCondition{
		Type:               ClusterStatusFailing,
		Status:             configv1.ConditionTrue,
		Message:            ierr.Error(),
		LastTransitionTime: now,
	})

	// preserve the status of the existing progressing condition
	progressingStatus := configv1.ConditionFalse
	if resourcemerge.IsOperatorStatusConditionTrue(config.Status.Conditions, configv1.OperatorProgressing) {
		progressingStatus = configv1.ConditionTrue
	}
	resourcemerge.SetOperatorStatusCondition(&config.Status.Conditions, configv1.ClusterOperatorStatusCondition{
		Type:               configv1.OperatorProgressing,
		Status:             progressingStatus,
		Message:            msg,
		LastTransitionTime: now,
	})

	mergeOperatorHistory(config, optr.currentVersion(), false, now, false)

	updated, err := applyClusterVersionStatus(ctx, optr.client.ConfigV1(), config, original)
	optr.rememberLastUpdate(updated)
	if err != nil {
		return err
	}
	return ierr
}

// applyClusterVersionStatus attempts to overwrite the status subresource of required. If
// original is provided it is compared to required and no update will be made if the
// object does not change. The method will retry a conflict by retrieving the latest live
// version and updating the metadata of required. required is modified if the object on
// the server is newer.
func applyClusterVersionStatus(ctx context.Context, client configclientv1.ClusterVersionsGetter, required, original *configv1.ClusterVersion) (*configv1.ClusterVersion, error) {
	if original != nil && equality.Semantic.DeepEqual(&original.Status, &required.Status) {
		return required, nil
	}
	actual, err := client.ClusterVersions().UpdateStatus(ctx, required, metav1.UpdateOptions{})
	if apierrors.IsConflict(err) {
		existing, cErr := client.ClusterVersions().Get(ctx, required.Name, metav1.GetOptions{})
		if err != nil {
			return nil, cErr
		}
		if existing.UID != required.UID {
			return nil, fmt.Errorf("cluster version was deleted and recreated, cannot update status")
		}
		if equality.Semantic.DeepEqual(&existing.Status, &required.Status) {
			return existing, nil
		}
		required.ObjectMeta = existing.ObjectMeta
		actual, err = client.ClusterVersions().UpdateStatus(ctx, required, metav1.UpdateOptions{})
	}
	if err != nil {
		return nil, err
	}
	required.ObjectMeta = actual.ObjectMeta
	return actual, nil
}

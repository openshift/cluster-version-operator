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
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/diff"
	"k8s.io/apimachinery/pkg/util/validation/field"

	configv1 "github.com/openshift/api/config/v1"
	configclientv1 "github.com/openshift/client-go/config/clientset/versioned/typed/config/v1"

	"github.com/openshift/cluster-version-operator/lib/resourcemerge"
	"github.com/openshift/cluster-version-operator/pkg/featuregates"
	"github.com/openshift/cluster-version-operator/pkg/payload"
)

const (
	// ClusterStatusFailing is set on the ClusterVersion status when a cluster
	// cannot reach the desired state. It is considered more serious than Degraded
	// and indicates the cluster is not healthy.
	ClusterStatusFailing = configv1.ClusterStatusConditionType("Failing")

	// ConditionalUpdateConditionTypeRecommended is a type of the condition present on a conditional update
	// that indicates whether the conditional update is recommended or not
	ConditionalUpdateConditionTypeRecommended = "Recommended"

	// MaxHistory is the maximum size of ClusterVersion history. Once exceeded
	// ClusterVersion history will be pruned. It is declared here and passed
	// into the pruner function to allow easier testing.
	MaxHistory = 100
)

func findRecommendedCondition(conditions []metav1.Condition) *metav1.Condition {
	return meta.FindStatusCondition(conditions, ConditionalUpdateConditionTypeRecommended)
}

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

func mergeOperatorHistory(cvStatus *configv1.ClusterVersionStatus, desired configv1.Release, verified bool, now metav1.Time,
	completed bool, acceptedRisksMsg string, localPayload bool) {

	// if we have no image, we cannot reproduce the update later and so it cannot be part of the history
	if len(desired.Image) == 0 {
		// make the array empty
		if cvStatus.History == nil {
			cvStatus.History = []configv1.UpdateHistory{}
		}
		return
	}

	if len(cvStatus.History) == 0 {
		klog.V(2).Infof("initialize new history completed=%t desired=%#v", completed, desired)
		cvStatus.History = append(cvStatus.History, configv1.UpdateHistory{
			Version: desired.Version,
			Image:   desired.Image,

			State:         configv1.PartialUpdate,
			StartedTime:   now,
			AcceptedRisks: acceptedRisksMsg,
		})
	}

	last := &cvStatus.History[0]

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
		// Don't overwrite accepted risks if local payload since signature verification and preconditions are not
		// performed on a local payload.
		if !localPayload {
			last.AcceptedRisks = acceptedRisksMsg
		}
	} else {
		klog.V(2).Infof("must add a new history entry completed=%t desired=%#v != last=%#v", completed, desired, last)
		if last.CompletionTime == nil {
			last.CompletionTime = &now
		}
		if completed {
			cvStatus.History = append([]configv1.UpdateHistory{
				{
					Version: desired.Version,
					Image:   desired.Image,

					State:          configv1.CompletedUpdate,
					StartedTime:    now,
					CompletionTime: &now,
					AcceptedRisks:  acceptedRisksMsg,
				},
			}, cvStatus.History...)
		} else {
			cvStatus.History = append([]configv1.UpdateHistory{
				{
					Version: desired.Version,
					Image:   desired.Image,

					State:         configv1.PartialUpdate,
					StartedTime:   now,
					AcceptedRisks: acceptedRisksMsg,
				},
			}, cvStatus.History...)
		}
	}

	// leave this here in case we find other future history bugs and need to debug it
	if klog.V(2).Enabled() && len(cvStatus.History) > 1 {
		if cvStatus.History[0].Image == cvStatus.History[1].Image && cvStatus.History[0].Version == cvStatus.History[1].Version {
			data, _ := json.MarshalIndent(cvStatus.History, "", "  ")
			panic(fmt.Errorf("tried to update cluster version history to contain duplicate image entries: %s", string(data)))
		}
	}

	// payloads can be verified during sync
	if verified {
		cvStatus.History[0].Verified = true
	}

	// Prune least informative history entry when at maxHistory.
	cvStatus.History = prune(cvStatus.History, MaxHistory)

	cvStatus.Desired = desired
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
// capabilities if any associated resources were previously managed by the CVO or disabling previously
// enabled capabilities.
const ImplicitlyEnabledCapabilities configv1.ClusterStatusConditionType = "ImplicitlyEnabledCapabilities"

// syncStatus calculates the new status of the ClusterVersion based on the current sync state and any
// validation errors found. We allow the caller to pass the original object to avoid DeepCopying twice.
func (optr *Operator) syncStatus(ctx context.Context, original, config *configv1.ClusterVersion, status *SyncWorkerStatus, validationErrs field.ErrorList) error {
	// Be more verbose when we are syncing something interesting
	verbosityLevel := klog.Level(4)
	if len(validationErrs) != 0 || status.Failure != nil {
		verbosityLevel = klog.Level(2)
	}
	klog.V(verbosityLevel).Infof("Synchronizing status errs=%#v status=%#v", validationErrs, status)

	cvUpdated := false
	// update the config with the latest available updates
	if updated := optr.getAvailableUpdates().NeedsUpdate(config, optr.enabledFeatureGates.StatusReleaseArchitecture()); updated != nil {
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

	updateClusterVersionStatus(&config.Status, status, optr.release, optr.getAvailableUpdates, optr.enabledFeatureGates, validationErrs)

	if klog.V(6).Enabled() {
		klog.Infof("Apply config: %s", diff.ObjectReflectDiff(original, config))
	}

	if progressing := resourcemerge.FindOperatorStatusCondition(config.Status.Conditions, configv1.OperatorProgressing); progressing != nil && progressing.Status == configv1.ConditionTrue {
		if progressingStart.IsZero() {
			progressingStart = time.Now()
			klog.V(2).Infof("===debug===: progressingStart=%s", progressingStart.Format(time.RFC3339))
		}
		if time.Since(progressingStart) < 6*time.Minute {
			progressing.Message = "working towards ${VERSION}: 106 of 841 done (12% complete), waiting on etcd, baremetal, insights, kube-apiserver"
			resourcemerge.SetOperatorStatusCondition(&config.Status.Conditions, *progressing)
			klog.V(2).Infof("===debug===: hijacked message=%s", progressing.Message)
		} else {
			klog.V(2).Infof("===debug===: original message=%s", progressing.Message)
		}
	}

	updated, err := applyClusterVersionStatus(ctx, optr.client.ConfigV1(), config, original)
	optr.rememberLastUpdate(updated)
	return err
}

var progressingStart time.Time

// updateClusterVersionStatus updates the passed cvStatus with the latest status information
func updateClusterVersionStatus(cvStatus *configv1.ClusterVersionStatus, status *SyncWorkerStatus,
	release configv1.Release, getAvailableUpdates func() *availableUpdates, enabledGates featuregates.CvoGateChecker,
	validationErrs field.ErrorList) {

	cvStatus.ObservedGeneration = status.Generation
	if len(status.VersionHash) > 0 {
		cvStatus.VersionHash = status.VersionHash
	}

	now := metav1.Now()
	version := versionStringFromRelease(status.Actual)
	if status.Actual.Image == release.Image {
		// backfill any missing information from the operator (payload).
		if status.Actual.Version == "" {
			status.Actual.Version = release.Version
		}
		if len(status.Actual.URL) == 0 {
			status.Actual.URL = release.URL
		}
		if len(status.Actual.Architecture) == 0 {
			status.Actual.Architecture = release.Architecture
		}
		if status.Actual.Channels == nil {
			status.Actual.Channels = append(release.Channels[:0:0], release.Channels...) // copy
		}
	}
	desired := mergeReleaseMetadata(status.Actual, getAvailableUpdates)
	if !enabledGates.StatusReleaseArchitecture() {
		desired.Architecture = configv1.ClusterVersionArchitecture("")
	}

	risksMsg := ""
	if desired.Image == status.loadPayloadStatus.Update.Image {
		risksMsg = status.loadPayloadStatus.AcceptedRisks
	}

	mergeOperatorHistory(cvStatus, desired, status.Verified, now, status.Completed > 0, risksMsg, status.loadPayloadStatus.Local)

	cvStatus.Capabilities = status.CapabilitiesStatus.Status

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

		resourcemerge.SetOperatorStatusCondition(&cvStatus.Conditions, configv1.ClusterOperatorStatusCondition{
			Type:               ClusterVersionInvalid,
			Status:             configv1.ConditionTrue,
			Reason:             reason,
			Message:            buf.String(),
			LastTransitionTime: now,
		})
	} else {
		resourcemerge.RemoveOperatorStatusCondition(&cvStatus.Conditions, ClusterVersionInvalid)
	}

	// set the implicitly enabled capabilities condition
	setImplicitlyEnabledCapabilitiesCondition(cvStatus, status.CapabilitiesStatus.ImplicitlyEnabledCaps, now)

	// set the desired release accepted condition
	setDesiredReleaseAcceptedCondition(cvStatus, status.loadPayloadStatus, now)

	// set the available condition
	if status.Completed > 0 {
		resourcemerge.SetOperatorStatusCondition(&cvStatus.Conditions, configv1.ClusterOperatorStatusCondition{
			Type:               configv1.OperatorAvailable,
			Status:             configv1.ConditionTrue,
			Message:            fmt.Sprintf("Done applying %s", version),
			LastTransitionTime: now,
		})
	}
	// default the available condition if not set
	if resourcemerge.FindOperatorStatusCondition(cvStatus.Conditions, configv1.OperatorAvailable) == nil {
		resourcemerge.SetOperatorStatusCondition(&cvStatus.Conditions, configv1.ClusterOperatorStatusCondition{
			Type:               configv1.OperatorAvailable,
			Status:             configv1.ConditionFalse,
			LastTransitionTime: now,
		})
	}

	failure := status.Failure
	failingReason, failingMessage := getReasonMessageFromError(failure)
	var skipFailure bool
	var progressReason, progressMessage string
	if !status.Reconciling && len(cvStatus.History) != 0 {
		progressReason, progressMessage, skipFailure = convertErrorToProgressing(now.Time, failure)
		failure = filterErrorForFailingCondition(failure, payload.UpdateEffectNone)
		filteredFailingReason, filteredFailingMessage := getReasonMessageFromError(failure)
		if failingReason != filteredFailingReason {
			klog.Infof("Filtered failure reason changed from '%s' to '%s'", failingReason, filteredFailingReason)
		}
		if failingMessage != filteredFailingMessage {
			klog.Infof("Filtered failure message changed from '%s' to '%s'", failingMessage, filteredFailingMessage)
		}
		failingReason, failingMessage = filteredFailingReason, filteredFailingMessage
	}

	// set the failing condition
	failingCondition := configv1.ClusterOperatorStatusCondition{
		Type:               ClusterStatusFailing,
		Status:             configv1.ConditionFalse,
		LastTransitionTime: now,
	}
	if failure != nil && !skipFailure {
		failingCondition.Status = configv1.ConditionTrue
		failingCondition.Reason = failingReason
		failingCondition.Message = failingMessage
	}
	if failure != nil &&
		strings.HasPrefix(progressReason, slowCOUpdatePrefix) {
		failingCondition.Status = configv1.ConditionUnknown
		failingCondition.Reason = "SlowClusterOperator"
		failingCondition.Message = progressMessage
	}
	progressReason = strings.TrimPrefix(progressReason, slowCOUpdatePrefix)

	resourcemerge.SetOperatorStatusCondition(&cvStatus.Conditions, failingCondition)

	// update progressing
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

		if status.Reconciling {
			resourcemerge.SetOperatorStatusCondition(&cvStatus.Conditions, configv1.ClusterOperatorStatusCondition{
				Type:               configv1.OperatorProgressing,
				Status:             configv1.ConditionFalse,
				Reason:             reason,
				Message:            fmt.Sprintf("Error while reconciling %s: %s", version, msg),
				LastTransitionTime: now,
			})
		} else {
			resourcemerge.SetOperatorStatusCondition(&cvStatus.Conditions, configv1.ClusterOperatorStatusCondition{
				Type:               configv1.OperatorProgressing,
				Status:             configv1.ConditionTrue,
				Reason:             reason,
				Message:            fmt.Sprintf("Unable to apply %s: %s", version, msg),
				LastTransitionTime: now,
			})
		}

	} else {
		// update progressing
		if status.Reconciling {
			message := fmt.Sprintf("Cluster version is %s", version)
			if len(validationErrs) > 0 {
				message = fmt.Sprintf("Stopped at %s: the cluster version is invalid", version)
			}
			resourcemerge.SetOperatorStatusCondition(&cvStatus.Conditions, configv1.ClusterOperatorStatusCondition{
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
			resourcemerge.SetOperatorStatusCondition(&cvStatus.Conditions, configv1.ClusterOperatorStatusCondition{
				Type:               configv1.OperatorProgressing,
				Status:             configv1.ConditionTrue,
				Reason:             reason,
				Message:            message,
				LastTransitionTime: now,
			})
		}
	}

	// default retrieved updates if it is not set
	if resourcemerge.FindOperatorStatusCondition(cvStatus.Conditions, configv1.RetrievedUpdates) == nil {
		resourcemerge.SetOperatorStatusCondition(&cvStatus.Conditions, configv1.ClusterOperatorStatusCondition{
			Type:               configv1.RetrievedUpdates,
			Status:             configv1.ConditionFalse,
			LastTransitionTime: now,
		})
	}
}

// getReasonMessageFromError returns the reason and the message from an error.
// If the reason or message is not available, an empty string is returned.
func getReasonMessageFromError(err error) (reason, message string) {
	if uErr, ok := err.(*payload.UpdateError); ok {
		reason = uErr.Reason
	}
	if err != nil {
		message = err.Error()
	}
	return reason, message
}

// filterErrorForFailingCondition filters out update errors based on the given
// updateEffect from a MultipleError error. If the err has the reason
// MultipleErrors, its immediate nested errors are filtered out and the error
// is recreated. If all nested errors are filtered out, nil is returned.
func filterErrorForFailingCondition(err error, updateEffect payload.UpdateEffectType) error {
	if err == nil {
		return nil
	}
	if uErr, ok := err.(*payload.UpdateError); ok && uErr.Reason == "MultipleErrors" {
		if nested, ok := uErr.Nested.(interface{ Errors() []error }); ok {
			filtered := nested.Errors()
			filtered = filterOutUpdateErrors(filtered, updateEffect)
			return newMultipleError(filtered)
		}
	}
	return err
}

// filterOutUpdateErrors filters out update errors of the given effect.
func filterOutUpdateErrors(errs []error, updateEffect payload.UpdateEffectType) []error {
	filtered := make([]error, 0, len(errs))
	for _, err := range errs {
		if uErr, ok := err.(*payload.UpdateError); ok && uErr.UpdateEffect == updateEffect {
			continue
		}
		filtered = append(filtered, err)
	}
	return filtered
}

func setImplicitlyEnabledCapabilitiesCondition(cvStatus *configv1.ClusterVersionStatus, implicitlyEnabled []configv1.ClusterVersionCapability,
	now metav1.Time) {

	// This is to clean up the condition with type=ImplicitlyEnabled introduced by OCPBUGS-56114
	if c := resourcemerge.FindOperatorStatusCondition(cvStatus.Conditions, "ImplicitlyEnabled"); c != nil {
		klog.V(2).Infof("Remove the condition with type ImplicitlyEnabled")
		resourcemerge.RemoveOperatorStatusCondition(&cvStatus.Conditions, "ImplicitlyEnabled")
	}

	if len(implicitlyEnabled) > 0 {
		message := "The following capabilities could not be disabled: "
		caps := make([]string, len(implicitlyEnabled))
		for i, c := range implicitlyEnabled {
			caps[i] = string(c)
		}
		sort.Strings(caps)
		message = message + strings.Join([]string(caps), ", ")

		resourcemerge.SetOperatorStatusCondition(&cvStatus.Conditions, configv1.ClusterOperatorStatusCondition{
			Type:               ImplicitlyEnabledCapabilities,
			Status:             configv1.ConditionTrue,
			Reason:             "CapabilitiesImplicitlyEnabled",
			Message:            message,
			LastTransitionTime: now,
		})
	} else {
		resourcemerge.SetOperatorStatusCondition(&cvStatus.Conditions, configv1.ClusterOperatorStatusCondition{
			Type:               ImplicitlyEnabledCapabilities,
			Status:             configv1.ConditionFalse,
			Reason:             "AsExpected",
			Message:            "Capabilities match configured spec",
			LastTransitionTime: now,
		})
	}
}

func setDesiredReleaseAcceptedCondition(cvStatus *configv1.ClusterVersionStatus, status LoadPayloadStatus, now metav1.Time) {
	if status.Step == "PayloadLoaded" {
		resourcemerge.SetOperatorStatusCondition(&cvStatus.Conditions, configv1.ClusterOperatorStatusCondition{
			Type:               DesiredReleaseAccepted,
			Status:             configv1.ConditionTrue,
			Reason:             status.Step,
			Message:            status.Message,
			LastTransitionTime: now,
		})
	} else if status.Step != "" {
		if status.Failure != nil {
			resourcemerge.SetOperatorStatusCondition(&cvStatus.Conditions, configv1.ClusterOperatorStatusCondition{
				Type:               DesiredReleaseAccepted,
				Status:             configv1.ConditionFalse,
				Reason:             status.Step,
				Message:            status.Message,
				LastTransitionTime: now,
			})
		} else {
			resourcemerge.SetOperatorStatusCondition(&cvStatus.Conditions, configv1.ClusterOperatorStatusCondition{
				Type:               DesiredReleaseAccepted,
				Status:             configv1.ConditionUnknown,
				Reason:             status.Step,
				Message:            status.Message,
				LastTransitionTime: now,
			})
		}
	}
}

const slowCOUpdatePrefix = "Slow::"

// convertErrorToProgressing returns true if the provided status indicates a failure condition can be interpreted as
// still making internal progress. The general error we try to suppress is an operator or operators still being
// progressing AND the general payload task making progress towards its goal. The error's UpdateEffect determines
// how an update error is interpreted. An error may simply need to be reported but does not indicate the update is
// failing. An error may indicate the update is failing or that if the error continues for a defined interval the
// update is failing.
func convertErrorToProgressing(now time.Time, statusFailure error) (reason string, message string, ok bool) {
	if statusFailure == nil {
		return "", "", false
	}
	uErr, ok := statusFailure.(*payload.UpdateError)
	if !ok {
		return "", "", false
	}
	switch uErr.UpdateEffect {
	case payload.UpdateEffectReport:
		return uErr.Reason, uErr.Error(), false
	case payload.UpdateEffectNone:
		return convertErrorToProgressingForUpdateEffectNone(uErr, now)
	case payload.UpdateEffectFail:
		return "", "", false
	case payload.UpdateEffectFailAfterInterval:
		return convertErrorToProgressingForUpdateEffectFailAfterInterval(uErr, now)
	}
	return "", "", false
}

func convertErrorToProgressingForUpdateEffectNone(uErr *payload.UpdateError, now time.Time) (string, string, bool) {
	var exceeded []string
	names := uErr.Names
	if len(names) == 0 {
		names = []string{uErr.Name}
	}
	var machineConfig bool
	for _, name := range names {
		m := 30 * time.Minute
		// It takes longer to upgrade MCO
		if name == "machine-config" {
			m = 3 * m
		}
		t := payload.COUpdateStartTimesGet(name)
		if (!t.IsZero()) && t.Before(now.Add(-(m))) {
			if name == "machine-config" {
				machineConfig = true
			} else {
				exceeded = append(exceeded, name)
			}
		}
	}
	// returns true in those slow cases because it is still only a suspicion
	if len(exceeded) > 0 && !machineConfig {
		return slowCOUpdatePrefix + uErr.Reason, fmt.Sprintf("waiting on %s over 30 minutes which is longer than expected", strings.Join(exceeded, ", ")), true
	}
	if len(exceeded) > 0 && machineConfig {
		return slowCOUpdatePrefix + uErr.Reason, fmt.Sprintf("waiting on %s over 30 minutes and machine-config over 90 minutes which is longer than expected", strings.Join(exceeded, ", ")), true
	}
	if len(exceeded) == 0 && machineConfig {
		return slowCOUpdatePrefix + uErr.Reason, "waiting on machine-config over 90 minutes which is longer than expected", true
	}
	return uErr.Reason, fmt.Sprintf("waiting on %s", strings.Join(names, ", ")), true
}

func convertErrorToProgressingForUpdateEffectFailAfterInterval(uErr *payload.UpdateError, now time.Time) (string, string, bool) {
	var exceeded []string
	threshold := now.Add(-(40 * time.Minute))
	names := uErr.Names
	if len(names) == 0 {
		names = []string{uErr.Name}
	}
	for _, name := range names {
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

	mergeOperatorHistory(&config.Status, optr.currentVersion(), false, now, false, "", false)

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

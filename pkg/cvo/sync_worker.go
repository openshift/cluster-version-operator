package cvo

import (
	"context"
	"fmt"
	"math/rand"
	"reflect"
	"sort"
	"strings"
	"sync"
	"time"

	configlistersv1 "github.com/openshift/client-go/config/listers/config/v1"
	"github.com/prometheus/client_golang/prometheus"
	"golang.org/x/time/rate"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/errors"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/tools/record"
	"k8s.io/klog/v2"

	configv1 "github.com/openshift/api/config/v1"

	"github.com/openshift/cluster-version-operator/pkg/payload"
	"github.com/openshift/cluster-version-operator/pkg/payload/precondition"
	"github.com/openshift/library-go/pkg/manifest"
)

// ConfigSyncWorker abstracts how the image is synchronized to the server. Introduced for testing.
type ConfigSyncWorker interface {
	Start(ctx context.Context, maxWorkers int, cvoOptrName string, lister configlistersv1.ClusterVersionLister)
	Update(ctx context.Context, generation int64, desired configv1.Update, overrides []configv1.ComponentOverride, state payload.State, cvoOptrName string) *SyncWorkerStatus
	StatusCh() <-chan SyncWorkerStatus
}

// PayloadInfo returns details about the payload when it was retrieved.
type PayloadInfo struct {
	// Directory is the path on disk where the payload is rooted.
	Directory string
	// Local is true if the payload was the payload associated with the operator image.
	Local bool

	// Verified is true if the payload was explicitly verified against the root of trust.
	// If unset and VerificationFailure is nil, the payload should be considered to not have
	// had verification attempted.
	Verified bool
	// VerificationFailure is any error returned by attempting to verify the payload.
	VerificationError error
}

// PayloadRetriever abstracts how a desired version is extracted to disk. Introduced for testing.
type PayloadRetriever interface {
	// RetrievePayload attempts to retrieve the desired payload, returning info about the payload
	// or an error.
	RetrievePayload(ctx context.Context, desired configv1.Update) (PayloadInfo, error)
}

// StatusReporter abstracts how status is reported by the worker run method. Introduced for testing.
type StatusReporter interface {
	Report(status SyncWorkerStatus)
	ReportPayload(payLoadStatus LoadPayloadStatus)
	ValidPayloadStatus(release configv1.Release) bool
}

// SyncWork represents the work that should be done in a sync iteration.
type SyncWork struct {
	Generation int64
	Desired    configv1.Update
	Overrides  []configv1.ComponentOverride
	State      payload.State

	// Completed is the number of times in a row we have synced this payload
	Completed int
	// Attempt is incremented each time we attempt to sync a payload and reset
	// when we change Generation/Desired or successfully synchronize.
	Attempt int
}

// Empty returns true if the image is empty for this work.
func (w SyncWork) Empty() bool {
	return len(w.Desired.Image) == 0
}

type LoadPayloadStatus struct {
	Step               string
	Message            string
	Failure            error
	Release            configv1.Release
	Verified           bool
	LastTransitionTime time.Time
}

// SyncWorkerStatus is the status of the sync worker at a given time.
type SyncWorkerStatus struct {
	Generation int64

	Failure error

	Done  int
	Total int

	Completed   int
	Reconciling bool
	Initial     bool
	VersionHash string

	LastProgress time.Time

	Actual configv1.Release

	// indicates if actual (current) release was verified
	Verified bool

	loadPayloadStatus LoadPayloadStatus
}

// DeepCopy copies the worker status.
func (w SyncWorkerStatus) DeepCopy() *SyncWorkerStatus {
	return &w
}

// SyncWorker retrieves and applies the desired image, tracking the status for the parent to
// monitor. The worker accepts a desired state via Update() and works to keep that state in
// sync. Once a particular image version is synced, it will be updated no more often than
// minimumReconcileInterval.
//
// State transitions:
//
//   Initial: wait for valid Update(), report empty status
//     Update() -> Sync
//   Sync: attempt to invoke the apply() method
//     apply() returns an error -> Error
//     apply() returns nil -> Reconciling
//   Reconciling: invoke apply() no more often than reconcileInterval
//     Update() with different values -> Sync
//     apply() returns an error -> Error
//     apply() returns nil -> Reconciling
//   Error: backoff until we are attempting every reconcileInterval
//     apply() returns an error -> Error
//     apply() returns nil -> Reconciling
//
type SyncWorker struct {
	backoff       wait.Backoff
	retriever     PayloadRetriever
	builder       payload.ResourceBuilder
	preconditions precondition.List
	eventRecorder record.EventRecorder

	// minimumReconcileInterval is the minimum time between reconcile attempts, and is
	// used to define the maximum backoff interval when apply() returns an error.
	minimumReconcileInterval time.Duration

	// coordination between the sync loop and external callers
	notify chan struct{}
	report chan SyncWorkerStatus

	// lock guards changes to these fields
	lock     sync.Mutex
	work     *SyncWork
	cancelFn func()
	status   SyncWorkerStatus

	// updated by the run method only
	payload *payload.Update

	// exclude is an identifier used to determine which
	// manifests should be excluded based on an annotation
	// of the form exclude.release.openshift.io/<identifier>=true
	exclude string

	// includeTechPreview is set to true when the CVO should create resources with the `release.openshift.io/feature-gate=TechPreviewNoUpgrade`
	includeTechPreview bool

	clusterProfile string
}

// NewSyncWorker initializes a ConfigSyncWorker that will retrieve payloads to disk, apply them via builder
// to a server, and obey limits about how often to reconcile or retry on errors.
func NewSyncWorker(retriever PayloadRetriever, builder payload.ResourceBuilder, reconcileInterval time.Duration, backoff wait.Backoff, exclude string, includeTechPreview bool, eventRecorder record.EventRecorder, clusterProfile string) *SyncWorker {
	return &SyncWorker{
		retriever:     retriever,
		builder:       builder,
		backoff:       backoff,
		eventRecorder: eventRecorder,

		minimumReconcileInterval: reconcileInterval,

		notify: make(chan struct{}, 1),
		// report is a large buffered channel to improve local testing - most consumers should invoke
		// Status() or use the result of calling Update() instead because the channel can be out of date
		// if the reader is not fast enough.
		report: make(chan SyncWorkerStatus, 500),

		exclude:            exclude,
		includeTechPreview: includeTechPreview,

		clusterProfile: clusterProfile,
	}
}

// NewSyncWorkerWithPreconditions initializes a ConfigSyncWorker that will retrieve payloads to disk, apply them via builder
// to a server, and obey limits about how often to reconcile or retry on errors.
// It allows providing preconditions for loading payload.
func NewSyncWorkerWithPreconditions(retriever PayloadRetriever, builder payload.ResourceBuilder, preconditions precondition.List, reconcileInterval time.Duration, backoff wait.Backoff, exclude string, includeTechPreview bool, eventRecorder record.EventRecorder, clusterProfile string) *SyncWorker {
	worker := NewSyncWorker(retriever, builder, reconcileInterval, backoff, exclude, includeTechPreview, eventRecorder, clusterProfile)
	worker.preconditions = preconditions
	return worker
}

// StatusCh returns a channel that reports status from the worker. The channel is buffered and events
// can be lost, so this is best used as a trigger to read the latest status.
func (w *SyncWorker) StatusCh() <-chan SyncWorkerStatus {
	return w.report
}

func (w *SyncWorker) syncPayload(ctx context.Context, work *SyncWork, reporter StatusReporter) error {
	desired := configv1.Release{
		Version: work.Desired.Version,
		Image:   work.Desired.Image,
	}
	klog.V(2).Infof("syncPayload: %s (force=%t)", versionString(desired), work.Desired.Force)

	// cache the payload until the release image changes
	validPayload := w.payload
	if validPayload != nil && validPayload.Release.Image == desired.Image {

		// clear payload status if it no longer applies to desired target
		if !reporter.ValidPayloadStatus(desired) {
			klog.V(2).Info("Resetting payload status to currently loaded payload.")
			reporter.ReportPayload(LoadPayloadStatus{
				Failure:            nil,
				Step:               "PayloadLoaded",
				Message:            fmt.Sprintf("Payload loaded version=%q image=%q", desired.Version, desired.Image),
				Verified:           w.status.Verified,
				Release:            desired,
				LastTransitionTime: time.Now(),
			})
		}
		// possibly complain here if Version, etc. diverges from the payload content
		return nil
	} else if validPayload == nil || !equalUpdate(configv1.Update{Image: validPayload.Release.Image}, configv1.Update{Image: desired.Image}) {
		cvoObjectRef := &corev1.ObjectReference{APIVersion: "config.openshift.io/v1", Kind: "ClusterVersion", Name: "version", Namespace: "openshift-cluster-version"}
		msg := fmt.Sprintf("Retrieving and verifying payload version=%q image=%q", desired.Version, desired.Image)
		w.eventRecorder.Eventf(cvoObjectRef, corev1.EventTypeNormal, "RetrievePayload", msg)
		reporter.ReportPayload(LoadPayloadStatus{
			Step:               "RetrievePayload",
			Message:            msg,
			Release:            desired,
			LastTransitionTime: time.Now(),
		})
		info, err := w.retriever.RetrievePayload(ctx, work.Desired)
		if err != nil {
			msg := fmt.Sprintf("Retrieving payload failed version=%q image=%q failure=%v", desired.Version, desired.Image, err)
			w.eventRecorder.Eventf(cvoObjectRef, corev1.EventTypeWarning, "RetrievePayloadFailed", msg)
			reporter.ReportPayload(LoadPayloadStatus{
				Failure:            err,
				Step:               "RetrievePayload",
				Message:            msg,
				Release:            desired,
				LastTransitionTime: time.Now(),
			})
			return err
		}

		w.eventRecorder.Eventf(cvoObjectRef, corev1.EventTypeNormal, "LoadPayload", "Loading payload version=%q image=%q", desired.Version, desired.Image)
		payloadUpdate, err := payload.LoadUpdate(info.Directory, desired.Image, w.exclude, w.includeTechPreview, w.clusterProfile)
		if err != nil {
			msg := fmt.Sprintf("Loading payload failed version=%q image=%q failure=%v", desired.Version, desired.Image, err)
			w.eventRecorder.Eventf(cvoObjectRef, corev1.EventTypeWarning, "LoadPayloadFailed", msg)
			reporter.ReportPayload(LoadPayloadStatus{
				Failure:            err,
				Step:               "LoadPayload",
				Message:            msg,
				Verified:           info.Verified,
				Release:            desired,
				LastTransitionTime: time.Now(),
			})
			return err
		}

		payloadUpdate.VerifiedImage = info.Verified
		payloadUpdate.LoadedAt = time.Now()

		if work.Desired.Version == "" {
			work.Desired.Version = payloadUpdate.Release.Version
			desired.Version = payloadUpdate.Release.Version
		} else if payloadUpdate.Release.Version != work.Desired.Version {
			err = fmt.Errorf("release image version %s does not match the expected upstream version %s", payloadUpdate.Release.Version, work.Desired.Version)
			msg := fmt.Sprintf("Verifying payload failed version=%q image=%q failure=%v", work.Desired.Version, work.Desired.Image, err)
			w.eventRecorder.Eventf(cvoObjectRef, corev1.EventTypeWarning, "VerifyPayloadVersionFailed", msg)
			reporter.ReportPayload(LoadPayloadStatus{
				Failure:            err,
				Step:               "VerifyPayloadVersion",
				Message:            msg,
				Verified:           info.Verified,
				Release:            desired,
				LastTransitionTime: time.Now(),
			})
			return err
		}

		// need to make sure the payload is only set when the preconditions have been successful
		if len(w.preconditions) == 0 {
			klog.V(2).Info("No preconditions configured.")
		} else if info.Local {
			klog.V(2).Info("Skipping preconditions for a local operator image payload.")
		} else {
			if block, err := precondition.Summarize(w.preconditions.RunAll(ctx, precondition.ReleaseContext{
				DesiredVersion: payloadUpdate.Release.Version,
			}), work.Desired.Force); err != nil {
				klog.V(2).Infof("Precondition error (force %t, block %t): %v", work.Desired.Force, block, err)
				if block {
					msg := fmt.Sprintf("Preconditions failed for payload loaded version=%q image=%q: %v", desired.Version, desired.Image, err)
					w.eventRecorder.Eventf(cvoObjectRef, corev1.EventTypeWarning, "PreconditionBlock", msg)
					reporter.ReportPayload(LoadPayloadStatus{
						Failure:            err,
						Step:               "PreconditionChecks",
						Message:            msg,
						Verified:           info.Verified,
						Release:            desired,
						LastTransitionTime: time.Now(),
					})
					return err
				} else {
					w.eventRecorder.Eventf(cvoObjectRef, corev1.EventTypeWarning, "PreconditionWarn", "precondition warning for payload loaded version=%q image=%q: %v", desired.Version, desired.Image, err)
				}
			}
			w.eventRecorder.Eventf(cvoObjectRef, corev1.EventTypeNormal, "PreconditionsPassed", "preconditions passed for payload loaded version=%q image=%q", desired.Version, desired.Image)
		}

		w.payload = payloadUpdate
		msg = fmt.Sprintf("Payload loaded version=%q image=%q", desired.Version, desired.Image)
		w.eventRecorder.Eventf(cvoObjectRef, corev1.EventTypeNormal, "PayloadLoaded", msg)
		reporter.ReportPayload(LoadPayloadStatus{
			Failure:            nil,
			Step:               "PayloadLoaded",
			Message:            msg,
			Verified:           info.Verified,
			Release:            desired,
			LastTransitionTime: time.Now(),
		})
		klog.V(2).Infof("Payload loaded from %s with hash %s", desired.Image, payloadUpdate.ManifestHash)
	}
	return nil
}

// loadUpdatedPayload retrieves the image. If successfully retrieved it updates payload otherwise it returns an error.
func (w *SyncWorker) loadUpdatedPayload(ctx context.Context, work *SyncWork, cvoOptrName string) error {
	// reporter hides status updates that occur earlier than the previous failure,
	// so that we don't fail, then immediately start reporting an earlier status
	reporter := &statusWrapper{w: w, previousStatus: w.status.DeepCopy()}
	if err := w.syncPayload(ctx, work, reporter); err != nil {
		klog.V(2).Infof("loadUpdatedPayload syncPayload err=%v", err)
		return err
	}
	return nil
}

// Update instructs the sync worker to start synchronizing the desired update. The reconciling boolean is
// ignored unless this is the first time that Update has been called. The returned status represents either
// the initial state or whatever the last recorded status was.
// TODO: in the future it may be desirable for changes that alter desired to wait briefly before returning,
//   giving the sync loop the opportunity to observe our change and begin working towards it.
func (w *SyncWorker) Update(ctx context.Context, generation int64, desired configv1.Update, overrides []configv1.ComponentOverride, state payload.State, cvoOptrName string) *SyncWorkerStatus {
	w.lock.Lock()
	defer w.lock.Unlock()

	work := &SyncWork{
		Generation: generation,
		Desired:    desired,
		Overrides:  overrides,
	}

	// the sync worker’s generation should always be latest with every change
	if w.work != nil {
		w.work.Generation = generation
	}

	if work.Empty() {
		klog.V(2).Info("Update work has no release image; ignoring requested change")
		return w.status.DeepCopy()
	}

	versionEqual, overridesEqual := equalSyncWork(w.work, work, fmt.Sprintf("considering cluster version generation %d", generation))

	if versionEqual && overridesEqual {
		klog.V(2).Info("Update work is equal to current target; no change required")
		return w.status.DeepCopy()
	}

	// initialize the reconciliation flag and the status the first time
	// update is invoked
	var oldDesired *configv1.Update
	if w.work == nil {
		work.State = state
		w.status = SyncWorkerStatus{
			Generation:  generation,
			Reconciling: state.Reconciling(),
			Actual: configv1.Release{
				Version: work.Desired.Version,
				Image:   work.Desired.Image,
			},
		}
	} else {
		oldDesired = &w.work.Desired
	}

	w.work = work

	if !versionEqual && oldDesired == nil {
		klog.Infof("Propagating initial target version %v to sync worker loop in state %s.", desired, state)
	} else if !versionEqual && state == payload.InitializingPayload {
		klog.Warningf("Ignoring detected version change from %v to %v during payload initialization", *oldDesired, work.Desired)
		w.work.Desired = *oldDesired
		if overridesEqual {
			return w.status.DeepCopy()
		}
	}

	w.lock.Unlock()
	err := w.loadUpdatedPayload(ctx, work, cvoOptrName)
	w.lock.Lock()
	if err != nil {
		return w.status.DeepCopy()
	}

	// notify the sync loop that we changed config
	if w.cancelFn != nil {
		klog.V(2).Info("Cancel the sync worker's current loop")
		w.cancelFn()
		w.cancelFn = nil
	}
	select {
	case w.notify <- struct{}{}:
		klog.V(2).Info("Notify the sync worker that new work is available")
	default:
		klog.V(2).Info("The sync worker has already been notified that new work is available")
	}

	return w.status.DeepCopy()
}

// Start periodically invokes run, detecting whether content has changed.
// It is edge-triggered when Update() is invoked and level-driven after the
// apply() has succeeded for a given input (we are said to be "reconciling").
func (w *SyncWorker) Start(ctx context.Context, maxWorkers int, cvoOptrName string, lister configlistersv1.ClusterVersionLister) {
	klog.V(2).Infof("Start: starting sync worker")

	work := &SyncWork{}

	wait.Until(func() {
		consecutiveErrors := 0
		errorInterval := w.minimumReconcileInterval / 16

		var next <-chan time.Time
		for {
			waitingToReconcile := work.State == payload.ReconcilingPayload
			select {
			case <-ctx.Done():
				klog.V(2).Infof("Stopped worker")
				return
			case <-next:
				waitingToReconcile = false
				klog.V(2).Infof("Wait finished")
			case <-w.notify:
				klog.V(2).Infof("Work updated")
			}

			// determine whether we need to do work
			changed := w.calculateNext(work)
			if !changed && waitingToReconcile {
				klog.V(2).Infof("No change, waiting")
				continue
			}

			// until Update() has been called at least once, we do nothing
			if work.Empty() {
				next = time.After(w.minimumReconcileInterval)
				klog.V(2).Infof("No work, waiting")
				continue
			}

			// actually apply the image, allowing for calls to be cancelled
			err := func() error {

				var syncTimeout time.Duration
				switch work.State {
				case payload.InitializingPayload:
					// during initialization we want to show what operators are being
					// created, so time out syncs more often to show a snapshot of progress
					// TODO: allow status outside of sync
					syncTimeout = w.minimumReconcileInterval
				case payload.UpdatingPayload:
					// during updates we want to flag failures on any resources that -
					// for cluster operators that are not reporting failing the error
					// message will point users to which operator is upgrading
					syncTimeout = w.minimumReconcileInterval * 2
				default:
					// TODO: make reconciling run in parallel, processing every resource
					//   once and accumulating errors, then reporting a summary of how
					//   much drift we found, and then we can turn down the timeout
					syncTimeout = w.minimumReconcileInterval * 2
				}
				ctx, cancelFn := context.WithTimeout(ctx, syncTimeout)

				w.lock.Lock()
				w.cancelFn = cancelFn
				w.lock.Unlock()
				defer cancelFn()

				// reporter hides status updates that occur earlier than the previous failure,
				// so that we don't fail, then immediately start reporting an earlier status
				reporter := &statusWrapper{w: w, previousStatus: w.Status()}
				klog.V(2).Infof("Previous sync status: %#v", reporter.previousStatus)
				return w.apply(ctx, work, maxWorkers, reporter)
			}()
			if err != nil {
				// backoff wait
				// TODO: replace with wait.Backoff when 1.13 client-go is available
				consecutiveErrors++
				interval := w.minimumReconcileInterval
				if consecutiveErrors < 4 {
					interval = errorInterval
					for i := 0; i < consecutiveErrors; i++ {
						interval *= 2
					}
				}
				next = time.After(wait.Jitter(interval, 0.2))
				work.Completed = 0
				work.Attempt++
				utilruntime.HandleError(fmt.Errorf("unable to synchronize image (waiting %s): %v", interval, err))
				continue
			}
			if work.State != payload.ReconcilingPayload {
				klog.V(2).Infof("Sync succeeded, transitioning from %s to %s", work.State, payload.ReconcilingPayload)
			}

			work.Completed++
			work.Attempt = 0
			work.State = payload.ReconcilingPayload
			next = time.After(w.minimumReconcileInterval)
		}
	}, 10*time.Millisecond, ctx.Done())

	klog.V(2).Infof("Worker shut down")
}

// statusWrapper prevents a newer status update from overwriting a previous
// failure from later in the sync process.
type statusWrapper struct {
	w              *SyncWorker
	previousStatus *SyncWorkerStatus
}

func (w *statusWrapper) ValidPayloadStatus(release configv1.Release) bool {
	return equalDigest(w.previousStatus.loadPayloadStatus.Release.Image, release.Image)
}

func (w *statusWrapper) ReportPayload(payloadStatus LoadPayloadStatus) {
	status := w.previousStatus
	status.loadPayloadStatus = payloadStatus
	w.w.updateStatus(*status)
}

func (w *statusWrapper) Report(status SyncWorkerStatus) {
	p := w.previousStatus
	status.loadPayloadStatus = p.loadPayloadStatus
	var fractionComplete float32
	if status.Total > 0 {
		fractionComplete = float32(status.Done) / float32(status.Total)
	}
	var previousFractionComplete float32
	if p.Total > 0 {
		previousFractionComplete = float32(p.Done) / float32(p.Total)
	}
	if p.Failure != nil && status.Failure == nil {
		if p.Actual.Image == status.Actual.Image {
			if fractionComplete < previousFractionComplete {
				klog.V(2).Infof("Dropping status report from earlier in sync loop")
				return
			}
		}
	}
	if fractionComplete > previousFractionComplete || status.Completed > p.Completed || (status.Failure == nil && status.Actual.Image != p.Actual.Image) {
		status.LastProgress = time.Now()
	}
	if status.Generation == 0 {
		status.Generation = p.Generation
	} else if status.Generation < p.Generation {
		klog.Warningf("Received a Generation(%d) lower than previously known Generation(%d), this is most probably an internal error", status.Generation, p.Generation)
	}
	w.w.updateStatus(status)
}

// calculateNext updates the passed work object with the desired next state and
// returns true if any changes were made. The reconciling flag is set the first
// time work transitions from empty to not empty (as a result of someone invoking
// Update).
func (w *SyncWorker) calculateNext(work *SyncWork) bool {
	w.lock.Lock()
	defer w.lock.Unlock()

	sameVersion, sameOverrides := equalSyncWork(work, w.work, "calculating next work")
	changed := !sameVersion || !sameOverrides

	// if this is the first time through the loop, initialize reconciling to
	// the state Update() calculated (to allow us to start in reconciling)
	if work.Empty() {
		work.State = w.work.State
		work.Attempt = 0
	} else if changed && work.State != payload.InitializingPayload {
		klog.V(2).Infof("Work changed, transitioning from %s to %s", work.State, payload.UpdatingPayload)
		work.State = payload.UpdatingPayload
		work.Attempt = 0
	}

	if w.work != nil {
		work.Desired = w.work.Desired
		work.Overrides = w.work.Overrides
	}

	work.Generation = w.work.Generation

	return changed
}

// equalUpdate returns true if two updates are semantically equivalent.
// It checks if the updates have have the same force and image values.
//
// We require complete pullspec equality, not just digest equality,
// because we want to go through the usual update process even if it's
// only the registry portion of the pullspec which has changed.
func equalUpdate(a, b configv1.Update) bool {
	return a.Force == b.Force && a.Image == b.Image
}

// equalDigest returns true if and only if the two pullspecs are
// by-digest pullspecs and the digests are equal or the two pullspecs
// are identical.
func equalDigest(pullspecA string, pullspecB string) bool {
	if pullspecA == pullspecB {
		return true
	}
	digestA := splitDigest(pullspecA)
	return digestA != "" && digestA == splitDigest(pullspecB)
}

// splitDigest returns the pullspec's digest, or an empty string if
// the pullspec is not by-digest (e.g. it is by-tag, or implicit).
func splitDigest(pullspec string) string {
	parts := strings.SplitN(pullspec, "@", 3)
	if len(parts) != 2 {
		return ""
	}
	return parts[1]
}

// equalSyncWork returns indications of whether release version has changed and whether overrides have changed.
func equalSyncWork(a, b *SyncWork, context string) (equalVersion, equalOverrides bool) {
	// if both `a` and `b` are the same then simply return true
	if a == b {
		return true, true
	}
	// if either `a` or `b` are nil then return false
	if a == nil || b == nil {
		return false, false
	}

	sameVersion := equalUpdate(a.Desired, b.Desired)
	sameOverrides := reflect.DeepEqual(a.Overrides, b.Overrides)

	var detected string
	if !sameVersion && !sameOverrides {
		detected = fmt.Sprintf("version (from %v to %v) and overrides changes", a.Desired, b.Desired)
	} else if !sameVersion {
		detected = fmt.Sprintf("version change (from %v to %v)", a.Desired, b.Desired)
	} else if !sameOverrides {
		detected = fmt.Sprintf("overrides change (%v to %v)", a.Overrides, b.Overrides)
	}
	if detected != "" {
		klog.V(2).Infof("Detected while %s: %s", context, detected)
	}
	return sameVersion, sameOverrides
}

// updateStatus records the current status of the sync action for observation
// by others. It sends a copy of the update to the report channel for improved
// testability.
func (w *SyncWorker) updateStatus(update SyncWorkerStatus) {
	w.lock.Lock()
	defer w.lock.Unlock()

	klog.V(6).Infof("Status change %#v", update)
	w.status = update
	select {
	case w.report <- update:
	default:
		if klog.V(6).Enabled() {
			klog.Infof("Status report channel was full %#v", update)
		}
	}
}

// Desired returns the state the SyncWorker is trying to achieve.
func (w *SyncWorker) Desired() configv1.Update {
	w.lock.Lock()
	defer w.lock.Unlock()
	if w.work == nil {
		return configv1.Update{}
	}
	return w.work.Desired
}

// Status returns a copy of the current worker status.
func (w *SyncWorker) Status() *SyncWorkerStatus {
	w.lock.Lock()
	defer w.lock.Unlock()
	return w.status.DeepCopy()
}

// apply applies the current payload to the server, executing in parallel if maxWorkers is set greater
// than 1, returning an error if the update could not be completely applied. The status is updated as we
// progress. Cancelling the context will abort the execution of apply.
func (w *SyncWorker) apply(ctx context.Context, work *SyncWork, maxWorkers int, reporter StatusReporter) error {
	klog.V(2).Infof("apply: %s on generation %d in state %s at attempt %d", work.Desired.Version, work.Generation, work.State, work.Attempt)

	if work.Attempt == 0 {
		payload.InitCOUpdateStartTimes()
	}
	payloadUpdate := w.payload

	// encapsulate status reporting in a threadsafe updater
	total := len(payloadUpdate.Manifests)
	cr := &consistentReporter{
		status: SyncWorkerStatus{
			Generation:  work.Generation,
			Initial:     work.State.Initializing(),
			Reconciling: work.State.Reconciling(),
			VersionHash: payloadUpdate.ManifestHash,
			Actual:      payloadUpdate.Release,
			Verified:    payloadUpdate.VerifiedImage,
		},
		completed: work.Completed,
		version:   payloadUpdate.Release.Version,
		total:     total,
		reporter:  reporter,
	}

	var tasks []*payload.Task
	backoff := w.backoff
	if backoff.Steps > 1 && work.State == payload.InitializingPayload {
		backoff = wait.Backoff{Steps: 4, Factor: 2, Duration: time.Second}
	}
	for i := range payloadUpdate.Manifests {
		tasks = append(tasks, &payload.Task{
			Index:    i + 1,
			Total:    total,
			Manifest: &payloadUpdate.Manifests[i],
			Backoff:  backoff,
		})
	}
	graph := payload.NewTaskGraph(tasks)
	graph.Split(payload.SplitOnJobs)
	var precreateObjects bool
	switch work.State {
	case payload.InitializingPayload:
		// Create every component in parallel to maximize reaching steady
		// state.
		graph.Parallelize(payload.FlattenByNumberAndComponent)
		maxWorkers = len(graph.Nodes)
		precreateObjects = true
	case payload.ReconcilingPayload:
		// Run the graph in random order during reconcile so that we don't
		// hang on any particular component - we seed from the number of
		// times we've attempted this particular payload, so a particular
		// payload always syncs in a reproducible order. We permute the
		// same way for 8 successive attempts, shifting the tasks for each
		// of those attempts to try to cover as much of the payload as
		// possible within that interval.
		steps := 8
		epoch, iteration := work.Attempt/steps, work.Attempt%steps
		r := rand.New(rand.NewSource(int64(epoch)))
		graph.Parallelize(payload.ShiftOrder(payload.PermuteOrder(payload.FlattenByNumberAndComponent, r), iteration, steps))
		maxWorkers = 2
	default:
		// perform an orderly roll out by payload order, using some parallelization
		// but avoiding out of order creation so components have some base
		graph.Parallelize(payload.ByNumberAndComponent)
		precreateObjects = true
	}

	// update each object
	errs := payload.RunGraph(ctx, graph, maxWorkers, func(ctx context.Context, tasks []*payload.Task) error {
		// in specific modes, attempt to precreate a set of known types (currently ClusterOperator) without
		// retries
		if precreateObjects {
			for _, task := range tasks {
				if err := ctx.Err(); err != nil {
					return cr.ContextError(err)
				}
				if task.Manifest.GVK != configv1.SchemeGroupVersion.WithKind("ClusterOperator") {
					continue
				}
				ov, ok := getOverrideForManifest(work.Overrides, task.Manifest)
				if ok && ov.Unmanaged {
					klog.V(2).Infof("Skipping precreation of %s as unmanaged", task)
					continue
				}
				if err := w.builder.Apply(ctx, task.Manifest, payload.PrecreatingPayload); err != nil {
					klog.V(2).Infof("Unable to precreate resource %s: %v", task, err)
					continue
				}
				klog.V(2).Infof("Precreated resource %s", task)
			}
		}

		for _, task := range tasks {
			if err := ctx.Err(); err != nil {
				return cr.ContextError(err)
			}
			cr.Update()

			klog.V(2).Infof("Running sync for %s", task)

			ov, ok := getOverrideForManifest(work.Overrides, task.Manifest)
			if ok && ov.Unmanaged {
				klog.V(2).Infof("Skipping %s as unmanaged", task)
				continue
			}

			if err := task.Run(ctx, payloadUpdate.Release.Version, w.builder, work.State); err != nil {
				return err
			}
			cr.Inc()
			klog.V(2).Infof("Done syncing for %s", task)
		}
		return nil
	})
	if len(errs) > 0 {
		if err := cr.Errors(errs); err != nil {
			return err
		}
		return errs[0]
	}

	// update the status
	cr.Complete()
	return nil
}

var (
	metricPayload = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "cluster_version_payload",
		Help: "Report the number of entries in the payload.",
	}, []string{"version", "type"})
)

func init() {
	prometheus.MustRegister(
		metricPayload,
	)
}

type errContext struct {
	err error
}

func (e errContext) Error() string { return e.err.Error() }

// consistentReporter hides the details of calculating the status based on the progress
// of the graph runner.
type consistentReporter struct {
	lock      sync.Mutex
	status    SyncWorkerStatus
	version   string
	completed int
	total     int
	done      int
	reporter  StatusReporter
}

func (r *consistentReporter) Inc() {
	r.lock.Lock()
	defer r.lock.Unlock()
	r.done++
}

func (r *consistentReporter) Update() {
	r.lock.Lock()
	defer r.lock.Unlock()
	metricPayload.WithLabelValues(r.version, "pending").Set(float64(r.total - r.done))
	metricPayload.WithLabelValues(r.version, "applied").Set(float64(r.done))
	copied := r.status
	copied.Done = r.done
	copied.Total = r.total
	r.reporter.Report(copied)
}

func (r *consistentReporter) Error(err error) {
	r.lock.Lock()
	defer r.lock.Unlock()
	copied := r.status
	copied.Done = r.done
	copied.Total = r.total
	if !isContextError(err) {
		copied.Failure = err
	}
	r.reporter.Report(copied)
}

func (r *consistentReporter) Errors(errs []error) error {
	err := summarizeTaskGraphErrors(errs)

	r.lock.Lock()
	defer r.lock.Unlock()
	copied := r.status
	copied.Done = r.done
	copied.Total = r.total
	if err != nil {
		copied.Failure = err
	}
	r.reporter.Report(copied)
	return err
}

func (r *consistentReporter) ContextError(err error) error {
	r.lock.Lock()
	defer r.lock.Unlock()
	return errContext{fmt.Errorf("update %s at %d of %d", err, r.done, r.total)}
}

func (r *consistentReporter) Complete() {
	r.lock.Lock()
	defer r.lock.Unlock()
	metricPayload.WithLabelValues(r.version, "pending").Set(float64(r.total - r.done))
	metricPayload.WithLabelValues(r.version, "applied").Set(float64(r.done))
	copied := r.status
	copied.Completed = r.completed + 1
	copied.Initial = false
	copied.Reconciling = true
	copied.Done = r.done
	copied.Total = r.total
	r.reporter.Report(copied)
}

func isContextError(err error) bool {
	if err == nil {
		return false
	}
	_, ok := err.(errContext)
	return ok
}

func isImageVerificationError(err error) bool {
	if err == nil {
		return false
	}
	updateErr, ok := err.(*payload.UpdateError)
	if !ok {
		return false
	}
	return updateErr.Reason == "ImageVerificationFailed"
}

// summarizeTaskGraphErrors takes a set of errors returned by the execution of a graph and attempts
// to reduce them to a single cause or message. This is domain specific to the payload and our update
// algorithms. The return value is the summarized error which may be nil if provided conditions are
// not truly an error (cancellation).
// TODO: take into account install vs upgrade
func summarizeTaskGraphErrors(errs []error) error {
	// we ignore context errors (canceled or timed out) since they don't
	// provide good feedback to users and are an internal detail of the
	// server
	err := errors.FilterOut(errors.NewAggregate(errs), isContextError)
	if err == nil {
		klog.V(2).Infof("All errors were context errors: %v", errs)
		return nil
	}
	agg, ok := err.(errors.Aggregate)
	if !ok {
		errs = []error{err}
	} else {
		errs = agg.Errors()
	}

	// log the errors to assist in debugging future summarization
	if klog.V(2).Enabled() {
		klog.Infof("Summarizing %d errors", len(errs))
		for _, err := range errs {
			if uErr, ok := err.(*payload.UpdateError); ok {
				if uErr.Task != nil {
					klog.Infof("Update error %d of %d: %s %s (%T: %v)", uErr.Task.Index, uErr.Task.Total, uErr.Reason, uErr.Message, uErr.Nested, uErr.Nested)
				} else {
					klog.Infof("Update error: %s %s (%T: %v)", uErr.Reason, uErr.Message, uErr.Nested, uErr.Nested)
				}
			} else {
				klog.Infof("Update error: %T: %v", err, err)
			}
		}
	}

	// collapse into a set of common errors where necessary
	if len(errs) == 1 {
		return errs[0]
	}
	// hide the generic "not available yet" when there are more specific errors present
	if filtered := filterErrors(errs, isClusterOperatorNotAvailable); len(filtered) > 0 {
		return newMultipleError(filtered)
	}
	// if we're only waiting for operators, condense the error down to a singleton
	if err := newClusterOperatorsNotAvailable(errs); err != nil {
		return err
	}
	return newMultipleError(errs)
}

// filterErrors returns only the errors in errs which are false for all fns.
func filterErrors(errs []error, fns ...func(err error) bool) []error {
	var filtered []error
	for _, err := range errs {
		if errorMatches(err, fns...) {
			continue
		}
		filtered = append(filtered, err)
	}
	return filtered
}

func errorMatches(err error, fns ...func(err error) bool) bool {
	for _, fn := range fns {
		if fn(err) {
			return true
		}
	}
	return false
}

// isClusterOperatorNotAvailable returns true if this is a ClusterOperatorNotAvailable error
func isClusterOperatorNotAvailable(err error) bool {
	uErr, ok := err.(*payload.UpdateError)
	return ok && uErr != nil && uErr.Reason == "ClusterOperatorNotAvailable"
}

// newClusterOperatorsNotAvailable unifies multiple ClusterOperatorNotAvailable errors into
// a single error. It returns nil if the provided errors are not of the same type.
func newClusterOperatorsNotAvailable(errs []error) error {
	updateEffect := payload.UpdateEffectNone
	names := make([]string, 0, len(errs))
	for _, err := range errs {
		uErr, ok := err.(*payload.UpdateError)
		if !ok || uErr.Reason != "ClusterOperatorNotAvailable" {
			return nil
		}
		if len(uErr.Name) > 0 {
			names = append(names, uErr.Name)
		}
		switch uErr.UpdateEffect {
		case payload.UpdateEffectNone:
		case payload.UpdateEffectFail:
			updateEffect = payload.UpdateEffectFail
		case payload.UpdateEffectFailAfterInterval:
			if updateEffect != payload.UpdateEffectFail {
				updateEffect = payload.UpdateEffectFailAfterInterval
			}
		}
	}
	if len(names) == 0 {
		return nil
	}

	sort.Strings(names)
	name := strings.Join(names, ", ")
	return &payload.UpdateError{
		Nested:       errors.NewAggregate(errs),
		UpdateEffect: updateEffect,
		Reason:       "ClusterOperatorsNotAvailable",
		Message:      fmt.Sprintf("Some cluster operators are still updating: %s", name),
		Name:         name,
	}
}

// uniqueStrings returns an array with all sequential identical items removed. It modifies the contents
// of arr. Sort the input array before calling to remove all duplicates.
func uniqueStrings(arr []string) []string {
	var last int
	for i := 1; i < len(arr); i++ {
		if arr[i] == arr[last] {
			continue
		}
		last++
		if last != i {
			arr[last] = arr[i]
		}
	}
	if last < len(arr) {
		last++
	}
	return arr[:last]
}

// newMultipleError reports a generic set of errors that block progress. This method expects multiple
// errors but handles singular and empty arrays gracefully. If all errors have the same message, the
// first item is returned.
func newMultipleError(errs []error) error {
	if len(errs) == 0 {
		return nil
	}
	if len(errs) == 1 {
		return errs[0]
	}
	messages := make([]string, 0, len(errs))
	for _, err := range errs {
		messages = append(messages, err.Error())
	}
	sort.Strings(messages)
	messages = uniqueStrings(messages)
	if len(messages) == 0 {
		return errs[0]
	}
	return &payload.UpdateError{
		Nested:  errors.NewAggregate(errs),
		Reason:  "MultipleErrors",
		Message: fmt.Sprintf("Multiple errors are preventing progress:\n* %s", strings.Join(messages, "\n* ")),
	}
}

// getOverrideForManifest returns the override and true when override exists for manifest.
func getOverrideForManifest(overrides []configv1.ComponentOverride, manifest *manifest.Manifest) (configv1.ComponentOverride, bool) {
	for idx, ov := range overrides {
		group, kind, namespace, name := manifest.GVK.Group, manifest.GVK.Kind, manifest.Obj.GetNamespace(), manifest.Obj.GetName()
		if ov.Group == group &&
			ov.Kind == kind &&
			(namespace == "" || ov.Namespace == namespace) && // cluster-scoped objects don't have namespace.
			ov.Name == name {
			return overrides[idx], true
		}
	}
	return configv1.ComponentOverride{}, false
}

// runThrottledStatusNotifier invokes fn every time ch is updated, but no more often than once
// every interval. If bucket is non-zero then the channel is throttled like a rate limiter bucket.
func runThrottledStatusNotifier(ctx context.Context, interval time.Duration, bucket int, ch <-chan SyncWorkerStatus, fn func()) {
	// notify the status change function fairly infrequently to avoid updating
	// the caller status more frequently than is needed
	throttle := rate.NewLimiter(rate.Every(interval), bucket)
	wait.UntilWithContext(ctx, func(ctx context.Context) {
		var last SyncWorkerStatus
		for {
			select {
			case <-ctx.Done():
				return
			case next := <-ch:
				// only throttle if we aren't on an edge
				if next.Generation == last.Generation && next.Actual.Image == last.Actual.Image && next.Reconciling == last.Reconciling && (next.Failure != nil) == (last.Failure != nil) {
					if err := throttle.Wait(ctx); err != nil && err != context.Canceled && err != context.DeadlineExceeded {
						utilruntime.HandleError(fmt.Errorf("unable to throttle status notification: %v", err))
					}
				}
				last = next

				fn()
			}
		}
	}, 1*time.Second)
}

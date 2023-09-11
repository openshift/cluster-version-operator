package cvo

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/blang/semver/v4"
	"github.com/google/uuid"
	"k8s.io/apimachinery/pkg/api/equality"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/klog/v2"

	configv1 "github.com/openshift/api/config/v1"
	"github.com/openshift/cluster-version-operator/lib/resourcemerge"
	"github.com/openshift/cluster-version-operator/pkg/cincinnati"
	"github.com/openshift/cluster-version-operator/pkg/clusterconditions"
)

const noArchitecture string = "NoArchitecture"
const noChannel string = "NoChannel"

// syncAvailableUpdates attempts to retrieve the latest updates and update the status of the ClusterVersion
// object. It will set the RetrievedUpdates condition. Updates are only checked if it has been more than
// the minimumUpdateCheckInterval since the last check.
func (optr *Operator) syncAvailableUpdates(ctx context.Context, config *configv1.ClusterVersion) error {
	usedDefaultUpstream := false
	upstream := string(config.Spec.Upstream)
	if len(upstream) == 0 {
		usedDefaultUpstream = true
		upstream = optr.defaultUpstreamServer
	}

	channel := config.Spec.Channel
	desiredArch := optr.getDesiredArchitecture(config.Spec.DesiredUpdate)
	currentArch := optr.getArchitecture()

	if desiredArch == "" {
		desiredArch = currentArch
	}

	// updates are only checked at most once per minimumUpdateCheckInterval or if the generation changes
	optrAvailableUpdates := optr.getAvailableUpdates()
	if optrAvailableUpdates == nil {
		klog.V(2).Info("First attempt to retrieve available updates")
		optrAvailableUpdates = &availableUpdates{}
	} else if !optrAvailableUpdates.RecentlyChanged(optr.minimumUpdateCheckInterval) {
		klog.V(2).Infof("Retrieving available updates again, because more than %s has elapsed since %s", optr.minimumUpdateCheckInterval, optrAvailableUpdates.LastAttempt.Format(time.RFC3339))
	} else if channel != optrAvailableUpdates.Channel {
		klog.V(2).Infof("Retrieving available updates again, because the channel has changed from %q to %q", optrAvailableUpdates.Channel, channel)
	} else if desiredArch != optrAvailableUpdates.Architecture {
		klog.V(2).Infof("Retrieving available updates again, because the architecture has changed from %q to %q", optrAvailableUpdates.Architecture, desiredArch)
	} else if upstream == optrAvailableUpdates.Upstream || (upstream == optr.defaultUpstreamServer && optrAvailableUpdates.Upstream == "") {
		klog.V(2).Infof("Available updates were recently retrieved, with less than %s elapsed since %s, will try later.", optr.minimumUpdateCheckInterval, optrAvailableUpdates.LastAttempt.Format(time.RFC3339))
		return nil
	} else {
		klog.V(2).Infof("Retrieving available updates again, because the upstream has changed from %q to %q", optrAvailableUpdates.Upstream, config.Spec.Upstream)
	}

	transport, err := optr.getTransport()
	if err != nil {
		return err
	}

	userAgent := optr.getUserAgent()

	current, updates, conditionalUpdates, condition := calculateAvailableUpdatesStatus(ctx, string(config.Spec.ClusterID),
		transport, userAgent, upstream, desiredArch, currentArch, channel, optr.release.Version, optr.conditionRegistry)

	// Populate conditions on conditional updates from operator state
	if optrAvailableUpdates != nil {
		for i := range optrAvailableUpdates.ConditionalUpdates {
			for j := range conditionalUpdates {
				if optrAvailableUpdates.ConditionalUpdates[i].Release.Image == conditionalUpdates[j].Release.Image {
					conditionalUpdates[j].Conditions = optrAvailableUpdates.ConditionalUpdates[i].Conditions
					break
				}
			}
		}
	}

	if usedDefaultUpstream {
		upstream = ""
	}

	optrAvailableUpdates.Upstream = upstream
	optrAvailableUpdates.Channel = channel
	optrAvailableUpdates.Architecture = desiredArch
	optrAvailableUpdates.Current = current
	optrAvailableUpdates.Updates = updates
	optrAvailableUpdates.ConditionalUpdates = conditionalUpdates
	optrAvailableUpdates.ConditionRegistry = optr.conditionRegistry
	optrAvailableUpdates.Condition = condition

	optrAvailableUpdates.evaluateConditionalUpdates(ctx)
	optr.setAvailableUpdates(optrAvailableUpdates)

	// requeue
	optr.queue.Add(optr.queueKey())
	return nil
}

type availableUpdates struct {
	Upstream     string
	Channel      string
	Architecture string

	// LastAttempt records the time of the most recent attempt at update
	// retrieval, regardless of whether it was successful.
	LastAttempt time.Time

	// LastSyncOrConfigChange records the most recent time when any of
	// the following events occurred:
	//
	// * Upstream changed, reflecting a new authority, and obsoleting
	//   any information retrieved from (or failures // retrieving from) the
	//   previous authority.
	// * Channel changes.  Same reasoning as for Upstream.
	// * A slice of Updates was successfully retrieved, even if that
	//   slice was empty.
	LastSyncOrConfigChange time.Time

	Current            configv1.Release
	Updates            []configv1.Release
	ConditionalUpdates []configv1.ConditionalUpdate
	ConditionRegistry  clusterconditions.ConditionRegistry

	Condition configv1.ClusterOperatorStatusCondition
}

func (u *availableUpdates) RecentlyChanged(interval time.Duration) bool {
	return u.LastAttempt.After(time.Now().Add(-interval))
}

func (u *availableUpdates) NeedsUpdate(original *configv1.ClusterVersion) *configv1.ClusterVersion {
	if u == nil {
		return nil
	}
	// Architecture could change but does not reside in ClusterVersion
	if u.Upstream != string(original.Spec.Upstream) || u.Channel != original.Spec.Channel {
		return nil
	}
	if equality.Semantic.DeepEqual(u.Updates, original.Status.AvailableUpdates) &&
		equality.Semantic.DeepEqual(u.ConditionalUpdates, original.Status.ConditionalUpdates) &&
		equality.Semantic.DeepEqual(u.Condition, resourcemerge.FindOperatorStatusCondition(original.Status.Conditions, u.Condition.Type)) {
		return nil
	}

	config := original.DeepCopy()
	resourcemerge.SetOperatorStatusCondition(&config.Status.Conditions, u.Condition)
	config.Status.AvailableUpdates = u.Updates
	config.Status.ConditionalUpdates = u.ConditionalUpdates
	return config
}

// setAvailableUpdates updates the currently calculated version of updates.
func (optr *Operator) setAvailableUpdates(u *availableUpdates) {
	success := false
	if u != nil {
		u.LastAttempt = time.Now()
		if u.Condition.Type == configv1.RetrievedUpdates {
			success = u.Condition.Status == configv1.ConditionTrue
		} else {
			klog.Warningf("Unrecognized condition %s=%s (%s: %s): cannot judge update retrieval success", u.Condition.Type, u.Condition.Status, u.Condition.Reason, u.Condition.Message)
		}

		sort.Slice(u.Updates, func(i, j int) bool {
			vi := semver.MustParse(u.Updates[i].Version)
			vj := semver.MustParse(u.Updates[j].Version)
			return vi.GTE(vj)
		})

		sort.Slice(u.ConditionalUpdates, func(i, j int) bool {
			vi := semver.MustParse(u.ConditionalUpdates[i].Release.Version)
			vj := semver.MustParse(u.ConditionalUpdates[j].Release.Version)
			return vi.GTE(vj)
		})
	}

	optr.statusLock.Lock()
	defer optr.statusLock.Unlock()
	if u != nil && (optr.availableUpdates == nil ||
		optr.availableUpdates.Upstream != u.Upstream ||
		optr.availableUpdates.Channel != u.Channel ||
		optr.availableUpdates.Architecture != u.Architecture ||
		success) {
		u.LastSyncOrConfigChange = u.LastAttempt
	} else if optr.availableUpdates != nil {
		u.LastSyncOrConfigChange = optr.availableUpdates.LastSyncOrConfigChange
	}
	optr.availableUpdates = u
}

// getAvailableUpdates returns the current calculated version of updates. It
// may be nil.
func (optr *Operator) getAvailableUpdates() *availableUpdates {
	optr.statusLock.Lock()
	defer optr.statusLock.Unlock()

	if optr.availableUpdates == nil {
		return nil
	}

	u := &availableUpdates{
		Upstream:               optr.availableUpdates.Upstream,
		Channel:                optr.availableUpdates.Channel,
		Architecture:           optr.availableUpdates.Architecture,
		LastAttempt:            optr.availableUpdates.LastAttempt,
		LastSyncOrConfigChange: optr.availableUpdates.LastSyncOrConfigChange,
		Current:                *optr.availableUpdates.Current.DeepCopy(),
		ConditionRegistry:      optr.availableUpdates.ConditionRegistry, // intentionally not a copy, to preserve cache state
		Condition:              optr.availableUpdates.Condition,
	}

	if optr.availableUpdates.Updates != nil {
		u.Updates = make([]configv1.Release, 0, len(optr.availableUpdates.Updates))
		for _, update := range optr.availableUpdates.Updates {
			u.Updates = append(u.Updates, *update.DeepCopy())
		}
	}

	if optr.availableUpdates.ConditionalUpdates != nil {
		u.ConditionalUpdates = make([]configv1.ConditionalUpdate, 0, len(optr.availableUpdates.ConditionalUpdates))
		for _, conditionalUpdate := range optr.availableUpdates.ConditionalUpdates {
			u.ConditionalUpdates = append(u.ConditionalUpdates, *conditionalUpdate.DeepCopy())
		}
	}

	return u
}

// getArchitecture returns the currently determined cluster architecture.
func (optr *Operator) getArchitecture() string {
	optr.statusLock.Lock()
	defer optr.statusLock.Unlock()
	return optr.architecture
}

// SetArchitecture sets the cluster architecture.
func (optr *Operator) SetArchitecture(architecture string) {
	optr.statusLock.Lock()
	defer optr.statusLock.Unlock()
	optr.architecture = architecture
}

func (optr *Operator) getDesiredArchitecture(update *configv1.Update) string {
	if update != nil {
		return string(update.Architecture)
	}
	return ""
}

func calculateAvailableUpdatesStatus(ctx context.Context, clusterID string, transport *http.Transport, userAgent, upstream, desiredArch,
	currentArch, channel, version string, conditionRegistry clusterconditions.ConditionRegistry) (configv1.Release, []configv1.Release, []configv1.ConditionalUpdate,
	configv1.ClusterOperatorStatusCondition) {

	var cvoCurrent configv1.Release
	if len(upstream) == 0 {
		return cvoCurrent, nil, nil, configv1.ClusterOperatorStatusCondition{
			Type: configv1.RetrievedUpdates, Status: configv1.ConditionFalse, Reason: "NoUpstream",
			Message: "No upstream server has been set to retrieve updates.",
		}
	}

	upstreamURI, err := url.Parse(upstream)
	if err != nil {
		return cvoCurrent, nil, nil, configv1.ClusterOperatorStatusCondition{
			Type: configv1.RetrievedUpdates, Status: configv1.ConditionFalse, Reason: "InvalidURI",
			Message: fmt.Sprintf("failed to parse upstream URL: %s", err),
		}
	}

	uuid, err := uuid.Parse(string(clusterID))
	if err != nil {
		return cvoCurrent, nil, nil, configv1.ClusterOperatorStatusCondition{
			Type: configv1.RetrievedUpdates, Status: configv1.ConditionFalse, Reason: "InvalidID",
			Message: fmt.Sprintf("invalid cluster ID: %s", err),
		}
	}

	if len(desiredArch) == 0 {
		return cvoCurrent, nil, nil, configv1.ClusterOperatorStatusCondition{
			Type: configv1.RetrievedUpdates, Status: configv1.ConditionFalse, Reason: noArchitecture,
			Message: "Architecture has not been configured.",
		}
	}

	if len(version) == 0 {
		return cvoCurrent, nil, nil, configv1.ClusterOperatorStatusCondition{
			Type: configv1.RetrievedUpdates, Status: configv1.ConditionFalse, Reason: "NoCurrentVersion",
			Message: "The cluster version does not have a semantic version assigned and cannot calculate valid upgrades.",
		}
	}

	if len(channel) == 0 {
		return cvoCurrent, nil, nil, configv1.ClusterOperatorStatusCondition{
			Type: configv1.RetrievedUpdates, Status: configv1.ConditionFalse, Reason: noChannel,
			Message: "The update channel has not been configured.",
		}
	}

	currentVersion, err := semver.Parse(version)
	if err != nil {
		klog.V(2).Infof("Unable to parse current semantic version %q: %v", version, err)
		return cvoCurrent, nil, nil, configv1.ClusterOperatorStatusCondition{
			Type: configv1.RetrievedUpdates, Status: configv1.ConditionFalse, Reason: "InvalidCurrentVersion",
			Message: "The current cluster version is not a valid semantic version and cannot be used to calculate upgrades.",
		}
	}

	current, updates, conditionalUpdates, err := cincinnati.NewClient(uuid, transport, userAgent, conditionRegistry).GetUpdates(ctx, upstreamURI, desiredArch,
		currentArch, channel, currentVersion)

	if err != nil {
		klog.V(2).Infof("Upstream server %s could not return available updates: %v", upstream, err)
		if updateError, ok := err.(*cincinnati.Error); ok {
			return cvoCurrent, nil, nil, configv1.ClusterOperatorStatusCondition{
				Type: configv1.RetrievedUpdates, Status: configv1.ConditionFalse, Reason: updateError.Reason,
				Message: fmt.Sprintf("Unable to retrieve available updates: %s", updateError.Message),
			}
		}
		// this should never happen
		return cvoCurrent, nil, nil, configv1.ClusterOperatorStatusCondition{
			Type: configv1.RetrievedUpdates, Status: configv1.ConditionFalse, Reason: "Unknown",
			Message: fmt.Sprintf("Unable to retrieve available updates: %s", err),
		}
	}

	return current, updates, conditionalUpdates, configv1.ClusterOperatorStatusCondition{
		Type:   configv1.RetrievedUpdates,
		Status: configv1.ConditionTrue,

		LastTransitionTime: metav1.Now(),
	}
}

func (u *availableUpdates) evaluateConditionalUpdates(ctx context.Context) {
	if u == nil {
		return
	}

	sort.Slice(u.ConditionalUpdates, func(i, j int) bool {
		vi := semver.MustParse(u.ConditionalUpdates[i].Release.Version)
		vj := semver.MustParse(u.ConditionalUpdates[j].Release.Version)
		return vi.GTE(vj)
	})

	for i, conditionalUpdate := range u.ConditionalUpdates {
		if errorCondition := evaluateConditionalUpdate(ctx, &conditionalUpdate, u.ConditionRegistry); errorCondition != nil {
			meta.SetStatusCondition(&conditionalUpdate.Conditions, *errorCondition)
			u.removeUpdate(conditionalUpdate.Release.Image)
		} else {
			meta.SetStatusCondition(&conditionalUpdate.Conditions, metav1.Condition{
				Type:   "Recommended",
				Status: metav1.ConditionTrue,
				// FIXME: ObservedGeneration?  That would capture upstream/channel, but not necessarily the currently-reconciling version.
				Reason:  "AsExpected",
				Message: "The update is recommended, because none of the conditional update risks apply to this cluster.",
			})
			u.Updates = append(u.Updates, conditionalUpdate.Release)
		}
		u.ConditionalUpdates[i].Conditions = conditionalUpdate.Conditions
	}
}

func (u *availableUpdates) removeUpdate(image string) {
	for i, update := range u.Updates {
		if update.Image == image {
			u.Updates = append(u.Updates[:i], u.Updates[i+1:]...)
		}
	}
}

func unknownExposureMessage(risk configv1.ConditionalUpdateRisk, err error) string {
	template := `Could not evaluate exposure to update risk %s (%v)
  %s description: %s
  %s URL: %s`
	return fmt.Sprintf(template, risk.Name, err, risk.Name, risk.Message, risk.Name, risk.URL)
}

func evaluateConditionalUpdate(ctx context.Context, conditionalUpdate *configv1.ConditionalUpdate, conditionRegistry clusterconditions.ConditionRegistry) *metav1.Condition {
	recommended := &metav1.Condition{
		Type: "Recommended",
	}
	messages := []string{}
	for _, risk := range conditionalUpdate.Risks {
		if match, err := conditionRegistry.Match(ctx, risk.MatchingRules); err != nil {
			if recommended.Status != metav1.ConditionFalse {
				recommended.Status = metav1.ConditionUnknown
			}
			if recommended.Reason == "" || recommended.Reason == "EvaluationFailed" {
				recommended.Reason = "EvaluationFailed"
			} else {
				recommended.Reason = "MultipleReasons"
			}
			messages = append(messages, unknownExposureMessage(risk, err))
		} else if match {
			recommended.Status = metav1.ConditionFalse
			if recommended.Reason == "" {
				recommended.Reason = risk.Name
			} else {
				recommended.Reason = "MultipleReasons"
			}
			messages = append(messages, fmt.Sprintf("%s %s", risk.Message, risk.URL))
		}
	}
	if recommended.Status == "" {
		return nil
	}
	recommended.Message = strings.Join(messages, "\n\n")
	return recommended
}

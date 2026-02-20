package cvo

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/blang/semver/v4"
	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"github.com/google/uuid"
	"k8s.io/apimachinery/pkg/api/equality"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	utilerrors "k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/klog/v2"

	configv1 "github.com/openshift/api/config/v1"
	"github.com/openshift/cluster-version-operator/lib/resourcemerge"
	"github.com/openshift/cluster-version-operator/pkg/cincinnati"
	"github.com/openshift/cluster-version-operator/pkg/clusterconditions"
	"github.com/openshift/cluster-version-operator/pkg/internal"
)

const noArchitecture string = "NoArchitecture"
const noChannel string = "NoChannel"
const defaultUpdateService string = "https://api.openshift.com/api/upgrades_info/v1/graph"

// syncAvailableUpdates attempts to retrieve the latest updates and update the status of the ClusterVersion
// object. It will set the RetrievedUpdates condition. Updates are only checked if it has been more than
// the minimumUpdateCheckInterval since the last check.
func (optr *Operator) syncAvailableUpdates(ctx context.Context, config *configv1.ClusterVersion) error {
	usedDefaultUpdateService := false

	updateService := optr.updateService
	var updateServiceSource string
	if len(updateService) > 0 {
		updateServiceSource = "the --update-service command line option"
	} else if len(config.Spec.Upstream) > 0 {
		updateService = string(config.Spec.Upstream)
		updateServiceSource = "ClusterVersion spec.upstream"
	} else {
		usedDefaultUpdateService = true
		updateService = defaultUpdateService
		updateServiceSource = "the operator's default update service"
	}

	channel := config.Spec.Channel
	desiredArch := optr.getDesiredArchitecture(config.Spec.DesiredUpdate)
	currentArch := optr.getCurrentArchitecture()
	acceptRisks := sets.New[string]()
	if config.Spec.DesiredUpdate != nil {
		for _, risk := range config.Spec.DesiredUpdate.AcceptRisks {
			acceptRisks.Insert(risk.Name)
		}
	}

	// updates are only checked at most once per minimumUpdateCheckInterval or if the generation changes
	optrAvailableUpdates := optr.getAvailableUpdates()
	needFreshFetch := true
	preserveCacheOnFailure := false
	maximumCacheInterval := 24 * time.Hour
	if optrAvailableUpdates == nil {
		klog.V(2).Info("First attempt to retrieve available updates")
		optrAvailableUpdates = &availableUpdates{}
		// Populate known conditional updates from CV status, if present. They will be re-fetched later,
		// but we need to populate existing conditions to avoid bumping lastTransitionTime fields on
		// conditions if their status hasn't changed since previous CVO evaluated them.
		for i := range config.Status.ConditionalUpdates {
			optrAvailableUpdates.ConditionalUpdates = append(optrAvailableUpdates.ConditionalUpdates, *config.Status.ConditionalUpdates[i].DeepCopy())
		}
	} else if channel != optrAvailableUpdates.Channel {
		klog.V(2).Infof("Retrieving available updates again, because the channel has changed from %q to %q", optrAvailableUpdates.Channel, channel)
	} else if desiredArch != optrAvailableUpdates.Architecture {
		klog.V(2).Infof("Retrieving available updates again, because the architecture has changed from %q to %q", optrAvailableUpdates.Architecture, desiredArch)
	} else if !optrAvailableUpdates.RecentlyChanged(maximumCacheInterval) {
		klog.V(2).Infof("Retrieving available updates again, because more than %s has elapsed since last change at %s.  Will clear the cache if this fails.", maximumCacheInterval, optrAvailableUpdates.LastAttempt.Format(time.RFC3339))
	} else if !optrAvailableUpdates.RecentlyAttempted(optr.minimumUpdateCheckInterval) {
		klog.V(2).Infof("Retrieving available updates again, because more than %s has elapsed since last attempt at %s", optr.minimumUpdateCheckInterval, optrAvailableUpdates.LastAttempt.Format(time.RFC3339))
		preserveCacheOnFailure = true
	} else if updateService == optrAvailableUpdates.UpdateService || (updateService == defaultUpdateService && optrAvailableUpdates.UpdateService == "") {
		needsConditionalUpdateEval := false
		preserveCacheOnFailure = true
		for _, conditionalUpdate := range optrAvailableUpdates.ConditionalUpdates {
			if recommended := findRecommendedCondition(conditionalUpdate.Conditions); recommended == nil {
				needsConditionalUpdateEval = true
				break
			} else if recommended.Status != metav1.ConditionTrue && recommended.Status != metav1.ConditionFalse {
				needsConditionalUpdateEval = true
				break
			}
		}
		if !needsConditionalUpdateEval {
			klog.V(2).Infof("Available updates were recently retrieved, with less than %s elapsed since %s, will try later.", optr.minimumUpdateCheckInterval, optrAvailableUpdates.LastAttempt.Format(time.RFC3339))
			return nil
		}
		needFreshFetch = false
	} else {
		klog.V(2).Infof("Retrieving available updates again, because the update service has changed from %q to %q from %s", optrAvailableUpdates.UpdateService, updateService, updateServiceSource)
	}

	if needFreshFetch {
		transport, err := optr.getTransport()
		if err != nil {
			return err
		}

		userAgent := optr.getUserAgent()
		clusterId := string(config.Spec.ClusterID)

		current, updates, conditionalUpdates, condition := calculateAvailableUpdatesStatus(ctx, clusterId,
			transport, userAgent, updateService, desiredArch, currentArch, channel, optr.release.Version, optr.conditionRegistry)

		// Populate conditions on conditional updates from operator state
		for i := range optrAvailableUpdates.ConditionalUpdates {
			for j := range conditionalUpdates {
				if optrAvailableUpdates.ConditionalUpdates[i].Release.Image == conditionalUpdates[j].Release.Image {
					conditionalUpdates[j].Conditions = optrAvailableUpdates.ConditionalUpdates[i].Conditions
					break
				}
			}
		}

		if optr.injectClusterIdIntoPromQL {
			conditionalUpdates = injectClusterIdIntoConditionalUpdates(clusterId, conditionalUpdates)
		}

		if usedDefaultUpdateService {
			updateService = ""
		}

		optrAvailableUpdates.LastAttempt = time.Now()
		optrAvailableUpdates.UpdateService = updateService
		optrAvailableUpdates.Channel = channel
		optrAvailableUpdates.Architecture = desiredArch
		optrAvailableUpdates.ShouldReconcileAcceptRisks = optr.shouldReconcileAcceptRisks
		optrAvailableUpdates.AcceptRisks = acceptRisks
		optrAvailableUpdates.ConditionRegistry = optr.conditionRegistry
		optrAvailableUpdates.Condition = condition

		responseFailed := (condition.Type == configv1.RetrievedUpdates &&
			condition.Status == configv1.ConditionFalse &&
			(condition.Reason == "RemoteFailed" ||
				condition.Reason == "ResponseFailed" ||
				condition.Reason == "ResponseInvalid"))
		if !responseFailed || (responseFailed && !preserveCacheOnFailure) {
			optrAvailableUpdates.Current = current
			optrAvailableUpdates.Updates = updates
			optrAvailableUpdates.ConditionalUpdates = conditionalUpdates
		}
	}

	optrAvailableUpdates.evaluateConditionalUpdates(ctx)

	queueKey := optr.queueKey()
	for _, conditionalUpdate := range optrAvailableUpdates.ConditionalUpdates {
		if recommended := findRecommendedCondition(conditionalUpdate.Conditions); recommended == nil {
			klog.Warningf("Requeue available-update evaluation, because %q lacks a Recommended condition", conditionalUpdate.Release.Version)
			optr.availableUpdatesQueue.AddAfter(queueKey, time.Second)
			break
		} else if recommended.Status != metav1.ConditionTrue && recommended.Status != metav1.ConditionFalse {
			klog.V(2).Infof("Requeue available-update evaluation, because %q is %s=%s: %s: %s", conditionalUpdate.Release.Version, recommended.Type, recommended.Status, recommended.Reason, recommended.Message)
			optr.availableUpdatesQueue.AddAfter(queueKey, time.Second)
			break
		}
	}

	optr.setAvailableUpdates(optrAvailableUpdates)

	// queue optr.sync() to update ClusterVersion status
	optr.queue.Add(queueKey)
	return nil
}

type availableUpdates struct {
	UpdateService              string
	Channel                    string
	Architecture               string
	ShouldReconcileAcceptRisks func() bool
	AcceptRisks                sets.Set[string]

	// LastAttempt records the time of the most recent attempt at update
	// retrieval, regardless of whether it was successful.
	LastAttempt time.Time

	// LastSyncOrConfigChange records the most recent time when any of
	// the following events occurred:
	//
	// * UpdateService changed, reflecting a new authority, and obsoleting
	//   any information retrieved from (or failures // retrieving from) the
	//   previous authority.
	// * Channel changes.  Same reasoning as for UpdateService.
	// * A slice of Updates was successfully retrieved, even if that
	//   slice was empty.
	LastSyncOrConfigChange time.Time

	Current            configv1.Release
	Updates            []configv1.Release
	ConditionalUpdates []configv1.ConditionalUpdate
	ConditionRegistry  clusterconditions.ConditionRegistry

	Condition configv1.ClusterOperatorStatusCondition

	// RiskConditions stores the condition for every risk (name, url, message, matchingRules).
	RiskConditions map[string][]metav1.Condition
}

func (u *availableUpdates) RecentlyAttempted(interval time.Duration) bool {
	return u.LastAttempt.After(time.Now().Add(-interval))
}

func (u *availableUpdates) RecentlyChanged(interval time.Duration) bool {
	return u.LastSyncOrConfigChange.After(time.Now().Add(-interval))
}

func (u *availableUpdates) NeedsUpdate(original *configv1.ClusterVersion, statusReleaseArchitecture bool) *configv1.ClusterVersion {
	if u == nil {
		return nil
	}
	// Architecture could change but does not reside in ClusterVersion
	if u.UpdateService != string(original.Spec.Upstream) || u.Channel != original.Spec.Channel {
		return nil
	}

	var updates []configv1.Release
	var conditionalUpdates []configv1.ConditionalUpdate

	if statusReleaseArchitecture {
		updates = u.Updates
		conditionalUpdates = u.ConditionalUpdates
	} else {
		for _, update := range u.Updates {
			c := update.DeepCopy()
			c.Architecture = configv1.ClusterVersionArchitecture("")
			updates = append(updates, *c)
		}
		for _, conditionalUpdate := range u.ConditionalUpdates {
			c := conditionalUpdate.DeepCopy()
			c.Release.Architecture = configv1.ClusterVersionArchitecture("")
			conditionalUpdates = append(conditionalUpdates, *c)
		}
	}

	if equality.Semantic.DeepEqual(updates, original.Status.AvailableUpdates) &&
		equality.Semantic.DeepEqual(conditionalUpdates, original.Status.ConditionalUpdates) &&
		equality.Semantic.DeepEqual(u.Condition, resourcemerge.FindOperatorStatusCondition(original.Status.Conditions, u.Condition.Type)) {
		return nil
	}

	config := original.DeepCopy()
	resourcemerge.SetOperatorStatusCondition(&config.Status.Conditions, u.Condition)
	config.Status.AvailableUpdates = updates
	config.Status.ConditionalUpdates = conditionalUpdates
	return config
}

// setAvailableUpdates updates the currently calculated version of updates.
func (optr *Operator) setAvailableUpdates(u *availableUpdates) {
	success := false
	if u != nil {
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
		optr.availableUpdates.UpdateService != u.UpdateService ||
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
		UpdateService:              optr.availableUpdates.UpdateService,
		Channel:                    optr.availableUpdates.Channel,
		Architecture:               optr.availableUpdates.Architecture,
		ShouldReconcileAcceptRisks: optr.shouldReconcileAcceptRisks,
		AcceptRisks:                optr.availableUpdates.AcceptRisks,
		RiskConditions:             optr.availableUpdates.RiskConditions,
		LastAttempt:                optr.availableUpdates.LastAttempt,
		LastSyncOrConfigChange:     optr.availableUpdates.LastSyncOrConfigChange,
		Current:                    *optr.availableUpdates.Current.DeepCopy(),
		ConditionRegistry:          optr.availableUpdates.ConditionRegistry, // intentionally not a copy, to preserve cache state
		Condition:                  optr.availableUpdates.Condition,
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

func loadRiskConditions(ctx context.Context, risks []string, riskVersions map[string]riskWithVersion, conditionRegistry clusterconditions.ConditionRegistry) map[string][]metav1.Condition {
	riskConditions := map[string][]metav1.Condition{}
	for _, riskName := range risks {
		risk := riskVersions[riskName].risk
		riskCondition := metav1.Condition{
			Type:   internal.ConditionalUpdateRiskConditionTypeApplies,
			Status: metav1.ConditionFalse,
			Reason: riskConditionReasonNotMatch,
		}
		if match, err := conditionRegistry.Match(ctx, risk.MatchingRules); err != nil {
			msg := unknownExposureMessage(risk, err)
			riskCondition.Status = metav1.ConditionUnknown
			riskCondition.Reason = riskConditionReasonEvaluationFailed
			riskCondition.Message = msg
		} else if match {
			riskCondition.Status = metav1.ConditionTrue
			riskCondition.Reason = riskConditionReasonMatch
		}
		riskConditions[risk.Name] = []metav1.Condition{riskCondition}
	}
	if len(riskConditions) == 0 {
		return nil
	}
	return riskConditions
}

// risksInOrder returns the list of risk names sorted by the associated version
func risksInOrder(riskVersions map[string]riskWithVersion) []string {
	var ret []string
	var temp []riskWithVersion
	var keys []string
	for k := range riskVersions {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		temp = append(temp, riskVersions[k])
	}
	sort.SliceStable(temp, func(i, j int) bool {
		return temp[i].version.GT(temp[j].version)
	})
	for _, v := range temp {
		ret = append(ret, v.risk.Name)
	}
	return ret
}

type riskWithVersion struct {
	version semver.Version
	risk    configv1.ConditionalUpdateRisk
}

// loadRiskVersions returns the map from risk names to riskWithVersion where
// riskWithVersion.version is the latest version that the risk is exposed to
// and riskWithVersion.risk is the risk itself
func loadRiskVersions(conditionalUpdates []configv1.ConditionalUpdate) map[string]riskWithVersion {
	riskVersions := map[string]riskWithVersion{}
	for _, conditionalUpdate := range conditionalUpdates {
		for _, risk := range conditionalUpdate.Risks {
			candidate := semver.MustParse(conditionalUpdate.Release.Version)
			if v, ok := riskVersions[risk.Name]; !ok || candidate.GT(v.version) {
				riskVersions[risk.Name] = riskWithVersion{
					version: candidate,
					risk:    risk,
				}
			}
		}
	}
	if len(riskVersions) == 0 {
		return nil
	}
	return riskVersions
}

func (optr *Operator) getDesiredArchitecture(update *configv1.Update) string {
	if update != nil && len(update.Architecture) > 0 {
		return string(update.Architecture)
	}
	return optr.getCurrentArchitecture()
}

func (optr *Operator) getCurrentArchitecture() string {
	if optr.release.Architecture == configv1.ClusterVersionArchitectureMulti {
		return string(configv1.ClusterVersionArchitectureMulti)
	}
	return runtime.GOARCH
}

func calculateAvailableUpdatesStatus(ctx context.Context, clusterID string, transport *http.Transport, userAgent, updateService, desiredArch,
	currentArch, channel, version string, conditionRegistry clusterconditions.ConditionRegistry) (configv1.Release, []configv1.Release, []configv1.ConditionalUpdate,
	configv1.ClusterOperatorStatusCondition) {

	var cvoCurrent configv1.Release
	if len(updateService) == 0 {
		return cvoCurrent, nil, nil, configv1.ClusterOperatorStatusCondition{
			Type: configv1.RetrievedUpdates, Status: configv1.ConditionFalse, Reason: "NoUpstream",
			Message: "No updateService server has been set to retrieve updates.",
		}
	}

	updateServiceURI, err := url.Parse(updateService)
	if err != nil {
		return cvoCurrent, nil, nil, configv1.ClusterOperatorStatusCondition{
			Type: configv1.RetrievedUpdates, Status: configv1.ConditionFalse, Reason: "InvalidURI",
			Message: fmt.Sprintf("failed to parse update service URL: %s", err),
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

	current, updates, conditionalUpdates, err := cincinnati.NewClient(uuid, transport, userAgent, conditionRegistry).GetUpdates(ctx, updateServiceURI, desiredArch,
		currentArch, channel, currentVersion)

	if err != nil {
		klog.V(2).Infof("Update service %s could not return available updates: %v", updateService, err)
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

	riskVersions := loadRiskVersions(u.ConditionalUpdates)
	risks := risksInOrder(riskVersions)
	u.RiskConditions = loadRiskConditions(ctx, risks, riskVersions, u.ConditionRegistry)

	if err := sanityCheck(u.ConditionalUpdates); err != nil {
		klog.Errorf("Sanity check failed which might impact risk evaluation: %v", err)
	}
	for i, conditionalUpdate := range u.ConditionalUpdates {
		condition := evaluateConditionalUpdate(conditionalUpdate.Risks, u.AcceptRisks, u.ShouldReconcileAcceptRisks, u.RiskConditions)

		if condition.Status == metav1.ConditionTrue {
			u.addUpdate(conditionalUpdate.Release)
		} else {
			u.removeUpdate(conditionalUpdate.Release.Image)
		}

		meta.SetStatusCondition(&conditionalUpdate.Conditions, condition)
		u.ConditionalUpdates[i].Conditions = conditionalUpdate.Conditions

	}
}

func sanityCheck(updates []configv1.ConditionalUpdate) error {
	risks := map[string]configv1.ConditionalUpdateRisk{}
	var errs []error
	for _, update := range updates {
		for _, risk := range update.Risks {
			if v, ok := risks[risk.Name]; ok {
				if diff := cmp.Diff(v, risk, cmpopts.IgnoreFields(configv1.ConditionalUpdateRisk{}, "Conditions")); diff != "" {
					errs = append(errs, fmt.Errorf("found collision on risk %s: %v and %v", risk.Name, v, risk))
				}
			} else if trimmed := strings.TrimSpace(risk.Name); trimmed == "" {
				errs = append(errs, fmt.Errorf("found invalid name on risk %v", risk))
			} else {
				risks[risk.Name] = risk
			}
		}
	}
	return utilerrors.NewAggregate(errs)
}

func (u *availableUpdates) addUpdate(release configv1.Release) {
	for _, update := range u.Updates {
		if update.Image == release.Image {
			return
		}
	}

	u.Updates = append(u.Updates, release)
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

func newRecommendedStatus(now, want metav1.ConditionStatus) metav1.ConditionStatus {
	switch {
	case now == metav1.ConditionFalse || want == metav1.ConditionFalse:
		return metav1.ConditionFalse
	case now == metav1.ConditionUnknown || want == metav1.ConditionUnknown:
		return metav1.ConditionUnknown
	default:
		return want
	}
}

const (
	recommendedReasonRisksNotExposed         = "NotExposedToRisks"
	recommendedReasonAllExposedRisksAccepted = "AllExposedRisksAccepted"
	recommendedReasonEvaluationFailed        = "EvaluationFailed"
	recommendedReasonMultiple                = "MultipleReasons"

	// recommendedReasonExposed is used instead of the original name if it does
	// not match the pattern for a valid k8s condition reason.
	recommendedReasonExposed = "ExposedToRisks"

	riskConditionReasonEvaluationFailed = "EvaluationFailed"
	riskConditionReasonMatch            = "Match"
	riskConditionReasonNotMatch         = "NotMatch"
)

// Reasons follow same pattern as k8s Condition Reasons
// https://github.com/openshift/api/blob/59fa376de7cb668ddb95a7ee4e9879d7f6ca2767/vendor/k8s.io/apimachinery/pkg/apis/meta/v1/types.go#L1535-L1536
var reasonPattern = regexp.MustCompile(`^[A-Za-z]([A-Za-z0-9_,:]*[A-Za-z0-9_])?$`)

func newRecommendedReason(now, want string) string {
	switch {
	case now == recommendedReasonRisksNotExposed ||
		now == recommendedReasonAllExposedRisksAccepted ||
		now == want:
		return want
	case want == recommendedReasonRisksNotExposed:
		return now
	default:
		return recommendedReasonMultiple
	}
}

func evaluateConditionalUpdate(
	risks []configv1.ConditionalUpdateRisk,
	acceptRisks sets.Set[string],
	shouldReconcileAcceptRisks func() bool,
	riskConditions map[string][]metav1.Condition,
) metav1.Condition {
	recommended := metav1.Condition{
		Type:   internal.ConditionalUpdateConditionTypeRecommended,
		Status: metav1.ConditionTrue,
		// FIXME: ObservedGeneration?  That would capture upstream/channel, but not necessarily the currently-reconciling version.
		Reason:  recommendedReasonRisksNotExposed,
		Message: "The update is recommended, because none of the conditional update risks apply to this cluster.",
	}

	var errorMessages []string
	for _, risk := range risks {
		riskCondition := meta.FindStatusCondition(riskConditions[risk.Name], internal.ConditionalUpdateRiskConditionTypeApplies)
		if riskCondition == nil {
			// This should never happen
			riskCondition = &metav1.Condition{
				Status:  metav1.ConditionUnknown,
				Reason:  internal.ConditionalUpdateRiskConditionReasonInternalErrorFoundNoRiskCondition,
				Message: fmt.Sprintf("failed to find risk condition for risk %s", risk.Name),
			}
		}
		switch riskCondition.Status {
		case metav1.ConditionUnknown:
			recommended.Status = newRecommendedStatus(recommended.Status, metav1.ConditionUnknown)
			recommended.Reason = newRecommendedReason(recommended.Reason, recommendedReasonEvaluationFailed)
			errorMessages = append(errorMessages, riskCondition.Message)
		case metav1.ConditionTrue:
			if shouldReconcileAcceptRisks() && acceptRisks.Has(risk.Name) {
				recommended.Status = newRecommendedStatus(recommended.Status, metav1.ConditionTrue)
				recommended.Reason = newRecommendedReason(recommended.Reason, recommendedReasonAllExposedRisksAccepted)
				recommended.Message = "The update is recommended, because either risk does not apply to this cluster or it is accepted by cluster admins."
				klog.V(2).Infof("Risk with name %q is accepted by the cluster admin and thus not in the evaluation of conditional update", risk.Name)
			} else {
				recommended.Status = newRecommendedStatus(recommended.Status, metav1.ConditionFalse)
				wantReason := recommendedReasonExposed
				if reasonPattern.MatchString(risk.Name) {
					wantReason = risk.Name
				}
				recommended.Reason = newRecommendedReason(recommended.Reason, wantReason)
				errorMessages = append(errorMessages, fmt.Sprintf("%s %s", risk.Message, risk.URL))
			}
		}
	}
	if len(errorMessages) > 0 {
		recommended.Message = strings.Join(errorMessages, "\n\n")
	}

	return recommended
}

func injectClusterIdIntoConditionalUpdates(clusterId string, updates []configv1.ConditionalUpdate) []configv1.ConditionalUpdate {
	for i, update := range updates {
		for j, risk := range update.Risks {
			for k, rule := range risk.MatchingRules {
				if rule.Type == "PromQL" {
					newPromQl := injectIdIntoString(clusterId, rule.PromQL.PromQL)
					updates[i].Risks[j].MatchingRules[k].PromQL.PromQL = newPromQl
				}
			}
		}
	}
	return updates
}

func injectIdIntoString(id string, promQL string) string {
	return strings.ReplaceAll(promQL, `_id=""`, fmt.Sprintf(`_id="%s"`, id))
}

package cvo

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"

	configv1 "github.com/openshift/api/config/v1"
	configlistersv1 "github.com/openshift/client-go/config/listers/config/v1"
	"github.com/pkg/errors"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	listerscorev1 "k8s.io/client-go/listers/core/v1"
	"k8s.io/client-go/tools/cache"
	"k8s.io/klog/v2"

	"github.com/openshift/cluster-version-operator/lib/resourcedelete"
	"github.com/openshift/cluster-version-operator/lib/resourcemerge"
	"github.com/openshift/cluster-version-operator/pkg/internal"
	"github.com/openshift/cluster-version-operator/pkg/payload/precondition/clusterversion"
)

const (
	adminAckGateFmt             string = "^ack-[4-5][.]([0-9]{1,})-[^-]"
	upgradeableAdminAckRequired        = configv1.ClusterStatusConditionType("UpgradeableAdminAckRequired")
)

var adminAckGateRegexp = regexp.MustCompile(adminAckGateFmt)

// upgradeableCheckIntervals holds the time intervals that drive how often CVO checks for upgradeability
type upgradeableCheckIntervals struct {
	// min is the base minimum interval between upgradeability checks, applied under normal circumstances
	min time.Duration

	// minOnFailedPreconditions is the minimum interval between upgradeability checks when precondition checks are
	// failing and were recently (see afterPreconditionsFailed) changed. This should be lower than min because we want CVO
	// to check upgradeability more often
	minOnFailedPreconditions time.Duration

	// afterFailingPreconditions is the period of time after preconditions failed when minOnFailedPreconditions is
	// applied instead of min
	afterPreconditionsFailed time.Duration
}

func defaultUpgradeableCheckIntervals() upgradeableCheckIntervals {
	return upgradeableCheckIntervals{
		// 2 minutes are here because it is a lower bound of previously nondeterministicly chosen interval
		// TODO (OTA-860): Investigate our options of reducing this interval. We will need to investigate
		// the API usage patterns of the underlying checks, there is anecdotal evidence that they hit
		// apiserver instead of using local informer cache
		min:                      2 * time.Minute,
		minOnFailedPreconditions: 15 * time.Second,
		afterPreconditionsFailed: 2 * time.Minute,
	}
}

// syncUpgradeable synchronizes the upgradeable status only if the sufficient time passed since its last update. This
// throttling period is dynamic and is driven by upgradeableCheckIntervals.
func (optr *Operator) syncUpgradeable(cv *configv1.ClusterVersion) error {
	if u := optr.getUpgradeable(); u != nil {
		throttleFor := optr.upgradeableCheckIntervals.throttlePeriod(cv)
		if earliestNext := u.At.Add(throttleFor); time.Now().Before(earliestNext) {
			klog.V(2).Infof("Upgradeability last checked %s ago, will not re-check until %s", time.Since(u.At), earliestNext.Format(time.RFC3339))
			return nil
		}
	}
	optr.setUpgradeableConditions()

	// requeue
	optr.queue.Add(optr.queueKey())
	return nil
}

func (optr *Operator) setUpgradeableConditions() {
	now := metav1.Now()
	var conds []configv1.ClusterOperatorStatusCondition
	var reasons []string
	var msgs []string
	klog.V(4).Infof("Checking upgradeability conditions")
	for _, check := range optr.upgradeableChecks {
		if cond := check.Check(); cond != nil {
			reasons = append(reasons, cond.Reason)
			msgs = append(msgs, cond.Message)
			cond.LastTransitionTime = now
			conds = append(conds, *cond)
			klog.V(2).Infof("Upgradeability condition failed (type='%s' reason='%s' message='%s')", cond.Type, cond.Reason, cond.Message)
		}
	}
	if len(conds) == 1 {
		conds = []configv1.ClusterOperatorStatusCondition{{
			Type:               configv1.OperatorUpgradeable,
			Status:             configv1.ConditionFalse,
			Reason:             conds[0].Reason,
			Message:            conds[0].Message,
			LastTransitionTime: now,
		}}
	} else if len(conds) > 1 {
		conds = append(conds, configv1.ClusterOperatorStatusCondition{
			Type:               configv1.OperatorUpgradeable,
			Status:             configv1.ConditionFalse,
			Reason:             "MultipleReasons",
			Message:            fmt.Sprintf("Cluster should not be upgraded between minor versions for multiple reasons: %s\n* %s", strings.Join(reasons, ","), strings.Join(msgs, "\n* ")),
			LastTransitionTime: now,
		})
	} else {
		klog.V(2).Infof("All upgradeability conditions are passing")
	}
	sort.Slice(conds, func(i, j int) bool { return conds[i].Type < conds[j].Type })
	optr.setUpgradeable(&upgradeable{
		Conditions: conds,
	})
}

type upgradeable struct {
	At time.Time

	// these are sorted by Type
	Conditions []configv1.ClusterOperatorStatusCondition
}

func (u *upgradeable) NeedsUpdate(original *configv1.ClusterVersion) *configv1.ClusterVersion {
	if u == nil {
		return nil
	}

	origUpConditions := collectUpgradeableConditions(original.Status.Conditions)
	if equality.Semantic.DeepEqual(u.Conditions, origUpConditions) {
		return nil
	}

	config := original.DeepCopy()
	for _, c := range u.Conditions {
		resourcemerge.SetOperatorStatusCondition(&config.Status.Conditions, c)
	}
	for _, origc := range origUpConditions {
		if c := resourcemerge.FindOperatorStatusCondition(u.Conditions, origc.Type); c == nil {
			resourcemerge.RemoveOperatorStatusCondition(&config.Status.Conditions, origc.Type)
		}
	}
	return config
}

func collectUpgradeableConditions(conditions []configv1.ClusterOperatorStatusCondition) []configv1.ClusterOperatorStatusCondition {
	var ret []configv1.ClusterOperatorStatusCondition
	for _, c := range conditions {
		if strings.HasPrefix(string(c.Type), string(configv1.OperatorUpgradeable)) {
			ret = append(ret, c)
		}
	}
	sort.Slice(ret, func(i, j int) bool { return ret[i].Type < ret[j].Type })
	return ret
}

// setUpgradeable updates the currently calculated status of Upgradeable
func (optr *Operator) setUpgradeable(u *upgradeable) {
	if u != nil {
		u.At = time.Now()
	}

	optr.upgradeableStatusLock.Lock()
	defer optr.upgradeableStatusLock.Unlock()
	optr.upgradeable = u
}

// getUpgradeable returns the current calculated status of upgradeable. It
// may be nil.
func (optr *Operator) getUpgradeable() *upgradeable {
	optr.upgradeableStatusLock.Lock()
	defer optr.upgradeableStatusLock.Unlock()
	return optr.upgradeable
}

type upgradeableCheck interface {
	// returns a not-nil condition when the check fails.
	Check() *configv1.ClusterOperatorStatusCondition
}

type clusterOperatorsUpgradeable struct {
	coLister configlistersv1.ClusterOperatorLister
}

func (check *clusterOperatorsUpgradeable) Check() *configv1.ClusterOperatorStatusCondition {
	cond := &configv1.ClusterOperatorStatusCondition{
		Type:   configv1.ClusterStatusConditionType("UpgradeableClusterOperators"),
		Status: configv1.ConditionFalse,
	}
	ops, err := check.coLister.List(labels.Everything())
	if meta.IsNoMatchError(err) {
		return nil
	}
	if err != nil {
		cond.Reason = "FailedToListClusterOperators"
		cond.Message = errors.Wrap(err, "failed to list cluster operators").Error()
		return cond
	}

	type notUpgradeableCondition struct {
		name      string
		condition *configv1.ClusterOperatorStatusCondition
	}
	var notup []notUpgradeableCondition
	for _, op := range ops {
		if up := resourcemerge.FindOperatorStatusCondition(op.Status.Conditions, configv1.OperatorUpgradeable); up != nil && up.Status == configv1.ConditionFalse {
			notup = append(notup, notUpgradeableCondition{name: op.GetName(), condition: up})
		}
	}

	if len(notup) == 0 {
		return nil
	}
	msg := ""
	reason := ""
	if len(notup) == 1 {
		reason = notup[0].condition.Reason
		msg = fmt.Sprintf("Cluster operator %s should not be upgraded between minor versions: %s", notup[0].name, notup[0].condition.Message)
	} else {
		reason = "ClusterOperatorsNotUpgradeable"
		var msgs []string
		for _, cond := range notup {
			msgs = append(msgs, fmt.Sprintf("Cluster operator %s should not be upgraded between minor versions: %s: %s", cond.name, cond.condition.Reason, cond.condition.Message))
		}
		msg = fmt.Sprintf("Multiple cluster operators should not be upgraded between minor versions:\n* %s", strings.Join(msgs, "\n* "))
	}
	cond.Reason = reason
	cond.Message = msg
	return cond
}

type clusterVersionOverridesUpgradeable struct {
	name     string
	cvLister configlistersv1.ClusterVersionLister
}

func (check *clusterVersionOverridesUpgradeable) Check() *configv1.ClusterOperatorStatusCondition {
	cond := &configv1.ClusterOperatorStatusCondition{
		Type:   configv1.ClusterStatusConditionType("UpgradeableClusterVersionOverrides"),
		Status: configv1.ConditionFalse,
	}

	cv, err := check.cvLister.Get(check.name)
	if meta.IsNoMatchError(err) || apierrors.IsNotFound(err) {
		return nil
	}

	overrides := false
	for _, o := range cv.Spec.Overrides {
		if o.Unmanaged {
			overrides = true
		}
	}
	if !overrides {
		return nil
	}

	cond.Reason = "ClusterVersionOverridesSet"
	cond.Message = "Disabling ownership via cluster version overrides prevents upgrades. Please remove overrides before continuing."
	return cond
}

type clusterManifestDeleteInProgressUpgradeable struct {
}

func (check *clusterManifestDeleteInProgressUpgradeable) Check() *configv1.ClusterOperatorStatusCondition {
	cond := &configv1.ClusterOperatorStatusCondition{
		Type:   configv1.ClusterStatusConditionType("UpgradeableDeletesInProgress"),
		Status: configv1.ConditionFalse,
	}
	if deletes := resourcedelete.DeletesInProgress(); len(deletes) > 0 {
		resources := strings.Join(deletes, ",")
		klog.V(2).Infof("Resource deletions in progress; resources=%s", resources)
		cond.Reason = "ResourceDeletesInProgress"
		cond.Message = fmt.Sprintf("Cluster minor level upgrades are not allowed while resource deletions are in progress; resources=%s", resources)
		return cond
	}
	return nil
}

func gateApplicableToCurrentVersion(gateName string, currentVersion string) (bool, error) {
	var applicable bool
	if ackVersion := adminAckGateRegexp.FindString(gateName); ackVersion == "" {
		return false, fmt.Errorf("%s configmap gate name %s has invalid format; must comply with %q.",
			internal.AdminGatesConfigMap, gateName, adminAckGateFmt)
	} else {
		parts := strings.Split(ackVersion, "-")
		ackMinor := getEffectiveMinor(parts[1])
		cvMinor := getEffectiveMinor(currentVersion)
		if ackMinor == cvMinor {
			applicable = true
		}
	}
	return applicable, nil
}

func checkAdminGate(gateName string, gateValue string, currentVersion string,
	ackConfigmap *corev1.ConfigMap) (string, string) {

	if applies, err := gateApplicableToCurrentVersion(gateName, currentVersion); err == nil {
		if !applies {
			return "", ""
		}
	} else {
		klog.Error(err)
		return "AdminAckConfigMapGateNameError", err.Error()
	}
	if gateValue == "" {
		message := fmt.Sprintf("%s configmap gate %s must contain a non-empty value.", internal.AdminGatesConfigMap, gateName)
		klog.Error(message)
		return "AdminAckConfigMapGateValueError", message
	}
	if val, ok := ackConfigmap.Data[gateName]; !ok || val != "true" {
		return "AdminAckRequired", gateValue
	}
	return "", ""
}

type clusterAdminAcksCompletedUpgradeable struct {
	adminGatesLister listerscorev1.ConfigMapNamespaceLister
	adminAcksLister  listerscorev1.ConfigMapNamespaceLister
	cvLister         configlistersv1.ClusterVersionLister
	cvoName          string
}

func (check *clusterAdminAcksCompletedUpgradeable) Check() *configv1.ClusterOperatorStatusCondition {
	cv, err := check.cvLister.Get(check.cvoName)
	if meta.IsNoMatchError(err) || apierrors.IsNotFound(err) {
		message := fmt.Sprintf("Unable to get ClusterVersion, err=%v.", err)
		klog.Error(message)
		return &configv1.ClusterOperatorStatusCondition{
			Type:    upgradeableAdminAckRequired,
			Status:  configv1.ConditionFalse,
			Reason:  "UnableToGetClusterVersion",
			Message: message,
		}
	}
	currentVersion := clusterversion.GetCurrentVersion(cv.Status.History)

	// This can occur in early start up when the configmap is first added and version history
	// has not yet been populated.
	if currentVersion == "" {
		return nil
	}

	var gateCm *corev1.ConfigMap
	if gateCm, err = check.adminGatesLister.Get(internal.AdminGatesConfigMap); err != nil {
		var message string
		if apierrors.IsNotFound(err) {
			message = fmt.Sprintf("%s configmap not found.", internal.AdminGatesConfigMap)
		} else if err != nil {
			message = fmt.Sprintf("Unable to access configmap %s, err=%v.", internal.AdminGatesConfigMap, err)
		}
		klog.Error(message)
		return &configv1.ClusterOperatorStatusCondition{
			Type:    upgradeableAdminAckRequired,
			Status:  configv1.ConditionFalse,
			Reason:  "UnableToAccessAdminGatesConfigMap",
			Message: message,
		}
	}
	var ackCm *corev1.ConfigMap
	if ackCm, err = check.adminAcksLister.Get(internal.AdminAcksConfigMap); err != nil {
		var message string
		if apierrors.IsNotFound(err) {
			message = fmt.Sprintf("%s configmap not found.", internal.AdminAcksConfigMap)
		} else if err != nil {
			message = fmt.Sprintf("Unable to access configmap %s, err=%v.", internal.AdminAcksConfigMap, err)
		}
		klog.Error(message)
		return &configv1.ClusterOperatorStatusCondition{
			Type:    upgradeableAdminAckRequired,
			Status:  configv1.ConditionFalse,
			Reason:  "UnableToAccessAdminAcksConfigMap",
			Message: message,
		}
	}
	reasons := make(map[string][]string)
	for k, v := range gateCm.Data {
		if reason, message := checkAdminGate(k, v, currentVersion, ackCm); reason != "" {
			reasons[reason] = append(reasons[reason], message)
		}
	}
	var reason string
	var messages []string
	for k, v := range reasons {
		reason = k
		sort.Strings(v)
		messages = append(messages, strings.Join(v, " "))
	}
	if len(reasons) == 1 {
		return &configv1.ClusterOperatorStatusCondition{
			Type:    upgradeableAdminAckRequired,
			Status:  configv1.ConditionFalse,
			Reason:  reason,
			Message: messages[0],
		}
	} else if len(reasons) > 1 {
		sort.Strings(messages)
		return &configv1.ClusterOperatorStatusCondition{
			Type:    upgradeableAdminAckRequired,
			Status:  configv1.ConditionFalse,
			Reason:  "MultipleReasons",
			Message: strings.Join(messages, " "),
		}
	}
	return nil
}

func (optr *Operator) defaultUpgradeableChecks() []upgradeableCheck {
	return []upgradeableCheck{
		&clusterVersionOverridesUpgradeable{name: optr.name, cvLister: optr.cvLister},
		&clusterAdminAcksCompletedUpgradeable{
			adminGatesLister: optr.cmConfigManagedLister,
			adminAcksLister:  optr.cmConfigLister,
			cvLister:         optr.cvLister,
			cvoName:          optr.name,
		},
		&clusterOperatorsUpgradeable{coLister: optr.coLister},
		&clusterManifestDeleteInProgressUpgradeable{},
	}
}

// setUpgradableConditionsIfSynced calls setUpgradableConditions if all informers were synced at least once, otherwise
// it queues a full upgradable sync that will eventually execute
//
// This method is needed because it is called in an informer handler, but setUpgradeableConditions uses a lister
// itself, so it races on startup (it can get triggered while one informer's cache is being synced, but depends
// on another informer's cache already synced
func (optr *Operator) setUpgradableConditionsIfSynced() {
	for _, synced := range optr.cacheSynced {
		if !synced() {
			optr.upgradeableQueue.Add(optr.queueKey())
			return
		}
	}
	optr.setUpgradeableConditions()
}

func (optr *Operator) addFunc(obj interface{}) {
	cm := obj.(*corev1.ConfigMap)
	if cm.Name == internal.AdminGatesConfigMap || cm.Name == internal.AdminAcksConfigMap {
		klog.V(2).Infof("ConfigMap %s/%s added.", cm.Namespace, cm.Name)
		optr.setUpgradableConditionsIfSynced()
	}
}

func (optr *Operator) updateFunc(oldObj, newObj interface{}) {
	cm := newObj.(*corev1.ConfigMap)
	if cm.Name == internal.AdminGatesConfigMap || cm.Name == internal.AdminAcksConfigMap {
		oldCm := oldObj.(*corev1.ConfigMap)
		if !equality.Semantic.DeepEqual(cm, oldCm) {
			klog.V(2).Infof("ConfigMap %s/%s updated.", cm.Namespace, cm.Name)
			optr.setUpgradableConditionsIfSynced()
		}
	}
}

func (optr *Operator) deleteFunc(obj interface{}) {
	if tombstone, ok := obj.(cache.DeletedFinalStateUnknown); ok {
		obj = tombstone.Obj
	}
	cm, ok := obj.(*corev1.ConfigMap)
	if !ok {
		klog.Errorf("Unexpected type %T", obj)
		return
	}
	if cm.Name == internal.AdminGatesConfigMap || cm.Name == internal.AdminAcksConfigMap {
		klog.V(2).Infof("ConfigMap %s/%s deleted.", cm.Namespace, cm.Name)
		optr.setUpgradableConditionsIfSynced()
	}
}

// adminAcksEventHandler handles changes to the admin-acks configmap by re-assessing all
// Upgradeable conditions.
func (optr *Operator) adminAcksEventHandler() cache.ResourceEventHandler {
	return cache.ResourceEventHandlerFuncs{
		AddFunc:    optr.addFunc,
		UpdateFunc: optr.updateFunc,
		DeleteFunc: optr.deleteFunc,
	}
}

// adminGatesEventHandler handles changes to the admin-gates configmap by re-assessing all
// Upgradeable conditions.
func (optr *Operator) adminGatesEventHandler() cache.ResourceEventHandler {
	return cache.ResourceEventHandlerFuncs{
		AddFunc:    optr.addFunc,
		UpdateFunc: optr.updateFunc,
		DeleteFunc: optr.deleteFunc,
	}
}

// throttlePeriod returns the duration for which upgradeable status should be considered recent
// enough and unnecessary to update. The baseline duration is min. When the precondition checks
// on the payload are failing for less than afterPreconditionsFailed we want to synchronize
// the upgradeable status more frequently at beginning of an upgrade and return
// minOnFailedPreconditions which is expected to be lower than min.
//
// The cv parameter is expected to be non-nil.
func (intervals *upgradeableCheckIntervals) throttlePeriod(cv *configv1.ClusterVersion) time.Duration {
	if cond := resourcemerge.FindOperatorStatusCondition(cv.Status.Conditions, DesiredReleaseAccepted); cond != nil {
		deadline := cond.LastTransitionTime.Time.Add(intervals.afterPreconditionsFailed)
		if cond.Reason == "PreconditionChecks" && cond.Status == configv1.ConditionFalse && time.Now().Before(deadline) {
			return intervals.minOnFailedPreconditions
		}
	}
	return intervals.min
}

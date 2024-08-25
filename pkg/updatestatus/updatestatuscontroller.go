package updatestatus

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/openshift/cluster-version-operator/lib/resourcemerge"
	"github.com/openshift/cluster-version-operator/pkg/updatestatus/mco"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/klog/v2"

	coreInformers "k8s.io/client-go/informers"
	corev1listers "k8s.io/client-go/listers/core/v1"

	configInformers "github.com/openshift/client-go/config/informers/externalversions"
	mcfgInformers "github.com/openshift/client-go/machineconfiguration/informers/externalversions"

	configv1listers "github.com/openshift/client-go/config/listers/config/v1"
	mcfgv1listers "github.com/openshift/client-go/machineconfiguration/listers/machineconfiguration/v1"

	configclientv1alpha1 "github.com/openshift/client-go/config/clientset/versioned/typed/config/v1alpha1"

	configv1 "github.com/openshift/api/config/v1"
	configv1alpha1 "github.com/openshift/api/config/v1alpha1"
	mcfgv1 "github.com/openshift/api/machineconfiguration/v1"

	"github.com/openshift/library-go/pkg/controller/factory"
	"github.com/openshift/library-go/pkg/operator/events"
)

type updateStatusController struct {
	recorder events.Recorder

	updateStatuses configclientv1alpha1.UpdateStatusInterface

	clusterVersions    configv1listers.ClusterVersionLister
	clusterOperators   configv1listers.ClusterOperatorLister
	machineConfigPools mcfgv1listers.MachineConfigPoolLister
	machineConfigs     mcfgv1listers.MachineConfigLister
	nodes              corev1listers.NodeLister
}

type queueKeyKind string

const (
	clusterVersionKey    queueKeyKind = "cv"
	clusterOperatorKey   queueKeyKind = "co"
	machineConfigPoolKey queueKeyKind = "mcp"
	machineConfigKey     queueKeyKind = "mc"
	nodeKey              queueKeyKind = "node"
)

func (c queueKeyKind) withName(name string) string {
	return string(c) + "/" + name
}

func kindName(queueKey string) (queueKeyKind, string) {
	items := strings.Split(queueKey, "/")
	if len(items) != 2 {
		klog.Fatalf("invalid queue key: %s", queueKey)
	}

	switch items[0] {
	case string(clusterVersionKey):
		return clusterVersionKey, items[1]
	case string(clusterOperatorKey):
		return clusterOperatorKey, items[1]
	case string(machineConfigPoolKey):
		return machineConfigPoolKey, items[1]
	case string(machineConfigKey):
		return machineConfigKey, items[1]
	case string(nodeKey):
		return nodeKey, items[1]
	}

	klog.Fatalf("unknown queue key kind: %s", items[0])
	return "", ""
}

func queueKeys(obj runtime.Object) []string {
	if obj == nil {
		return nil
	}

	switch o := obj.(type) {
	case *configv1.ClusterVersion:
		return []string{clusterVersionKey.withName(o.Name)}
	case *configv1.ClusterOperator:
		return []string{clusterOperatorKey.withName(o.Name)}
	case *mcfgv1.MachineConfigPool:
		return []string{machineConfigPoolKey.withName(o.Name)}
	case *mcfgv1.MachineConfig:
		return []string{machineConfigKey.withName(o.Name)}
	case *corev1.Node:
		return []string{nodeKey.withName(o.Name)}
	}

	klog.Fatalf("unknown object type: %T", obj)
	return nil
}

func newUpdateStatusController(
	updateStatusClient configclientv1alpha1.UpdateStatusInterface,

	configInformers configInformers.SharedInformerFactory,
	mcfgInformers mcfgInformers.SharedInformerFactory,
	coreInformers coreInformers.SharedInformerFactory,

	eventsRecorder events.Recorder,
) factory.Controller {
	c := updateStatusController{
		updateStatuses: updateStatusClient,

		clusterVersions:  configInformers.Config().V1().ClusterVersions().Lister(),
		clusterOperators: configInformers.Config().V1().ClusterOperators().Lister(),

		machineConfigPools: mcfgInformers.Machineconfiguration().V1().MachineConfigPools().Lister(),
		machineConfigs:     mcfgInformers.Machineconfiguration().V1().MachineConfigs().Lister(),

		nodes: coreInformers.Core().V1().Nodes().Lister(),

		recorder: eventsRecorder,
	}
	controller := factory.New().WithInformersQueueKeysFunc(
		queueKeys,
		configInformers.Config().V1().ClusterVersions().Informer(),
		configInformers.Config().V1().ClusterOperators().Informer(),
		mcfgInformers.Machineconfiguration().V1().MachineConfigPools().Informer(),
		coreInformers.Core().V1().Nodes().Informer(),
	).
		WithSync(c.sync).ToController("UpdateStatusController", eventsRecorder.WithComponentSuffix("update-status-controller"))
	return controller
}

func (c *updateStatusController) sync(ctx context.Context, syncCtx factory.SyncContext) error {
	queueKey := syncCtx.QueueKey()

	klog.Infof("USC :: SYNC :: syncCtx.QueueKey() = %s", queueKey)

	if queueKey == factory.DefaultQueueKey {
		klog.Info("USC :: SYNC :: Full Relist")
		return nil
	}

	original, err := c.updateStatuses.Get(ctx, "cluster", metav1.GetOptions{})
	if err != nil {
		if errors.IsNotFound(err) {
			original = &configv1alpha1.UpdateStatus{ObjectMeta: metav1.ObjectMeta{Name: "cluster"}}
			original, err = c.updateStatuses.Create(ctx, original, metav1.CreateOptions{})
			if err != nil {
				klog.Fatalf("USC :: SYNC :: Failed to create UpdateStatus: %v", err)
			}
		} else {
			klog.Fatalf("USC :: SYNC :: Failed to get UpdateStatus: %v", err)
		}
	}

	kind, name := kindName(queueKey)

	cpStatus := &original.Status.ControlPlane
	var newCpStatus *configv1alpha1.ControlPlaneUpdateStatus
	_ = &original.Status.WorkerPools
	var newWpStatuses *[]configv1alpha1.PoolUpdateStatus

	switch kind {
	case clusterVersionKey:
		cv, err := c.clusterVersions.Get(name)
		if err != nil {
			klog.Fatalf("USC :: SYNC :: Failed to get ClusterVersion %s: %v", name, err)
		}

		newCpStatus = cpStatus.DeepCopy()
		updateStatusForClusterVersion(newCpStatus, cv)
	case clusterOperatorKey:
		co, err := c.clusterOperators.Get(name)
		if err != nil {
			klog.Fatalf("USC :: SYNC :: Failed to get ClusterOperator %s: %v", name, err)
		}
		newCpStatus = cpStatus.DeepCopy()
		updateStatusForClusterOperator(newCpStatus, co)
	case machineConfigPoolKey:
		mcp, err := c.machineConfigPools.Get(name)
		if err != nil {
			klog.Fatalf("USC :: SYNC :: Failed to get MachineConfigPool %s: %v", name, err)
		}
		newCpStatus = cpStatus.DeepCopy()
		updateStatusForMachineConfigPool(newCpStatus, newWpStatuses, mcp)
	case nodeKey:
		node, err := c.nodes.Get(name)
		if err != nil {
			klog.Fatalf("USC :: SYNC :: Failed to get Node %s: %v", name, err)
		}
		newCpStatus = cpStatus.DeepCopy()
		updateStatusForNode(newCpStatus, newWpStatuses, node, c.machineConfigs, c.machineConfigPools)
	}

	if diff := cmp.Diff(cpStatus, newCpStatus); newCpStatus != nil && diff != "" {
		klog.Info(diff)
		us := original.DeepCopy()
		us.Status.ControlPlane = *newCpStatus
		_, err = c.updateStatuses.UpdateStatus(ctx, us, metav1.UpdateOptions{})
		if err != nil {
			klog.Fatalf("USC :: SYNC :: Failed to update UpdateStatus: %v", err)
		}
	}

	return nil
}

func whichPool(master, worker labels.Selector, custom map[string]labels.Selector, node *corev1.Node) string {
	if master.Matches(labels.Set(node.Labels)) {
		return "master"
	}
	for name, selector := range custom {
		if selector.Matches(labels.Set(node.Labels)) {
			return name
		}
	}
	if worker.Matches(labels.Set(node.Labels)) {
		return "worker"
	}
	return ""
}

func updateStatusForNode(cpStatus *configv1alpha1.ControlPlaneUpdateStatus, wpStatuses *[]configv1alpha1.PoolUpdateStatus, node *corev1.Node, mcLister mcfgv1listers.MachineConfigLister, mcpLister mcfgv1listers.MachineConfigPoolLister) {
	pools, err := mcpLister.List(labels.Everything())
	if err != nil {
		klog.Fatalf("USC :: SYNC :: Failed to list MachineConfigPools: %v", err)
	}

	var masterSelector labels.Selector
	var workerSelector labels.Selector
	customSelectors := map[string]labels.Selector{}

	for _, pool := range pools {
		s, err := metav1.LabelSelectorAsSelector(pool.Spec.NodeSelector)
		if err != nil {
			klog.Fatalf("USC :: SYNC :: Failed to parse NodeSelector for MachineConfigPool %s: %v", pool.Name, err)
		}

		switch pool.Name {
		case mco.MachineConfigPoolMaster:
			masterSelector = s
		case mco.MachineConfigPoolWorker:
			workerSelector = s
		default:
			customSelectors[pool.Name] = s
		}
	}

	poolName := whichPool(masterSelector, workerSelector, customSelectors, node)
	pool, err := mcpLister.Get(poolName)
	if err != nil {
		klog.Fatalf("USC :: SYNC :: Failed to get MachineConfigPool %s: %v", poolName, err)
	}

	if poolName == mco.MachineConfigPoolMaster {
		updateStatusForControlPlaneNode(cpStatus, node, pool, mcLister)
	} else {
		updateStatusForWorkerNode(wpStatuses, node, pool, mcLister)
	}
}

func getOpenShiftVersionOfMachineConfig(machineConfigs []*mcfgv1.MachineConfig, name string) (string, bool) {
	for _, mc := range machineConfigs {
		if mc.Name == name {
			openshiftVersion := mc.Annotations[mco.ReleaseImageVersionAnnotationKey]
			return openshiftVersion, openshiftVersion != ""
		}
	}
	return "", false
}

func isNodeUnavailable(node *corev1.Node, pool *mcfgv1.MachineConfigPool) (bool, string) {
	lns := mco.NewLayeredNodeState(node)
	return lns.IsUnavailable(pool), lns.ReasonOfUnavailability
}

func isNodeDegraded(node *corev1.Node) bool {
	// Inspired by: https://github.com/openshift/machine-config-operator/blob/master/pkg/controller/node/status.go
	if node.Annotations == nil {
		return false
	}
	dconfig, ok := node.Annotations[mco.DesiredMachineConfigAnnotationKey]
	if !ok || dconfig == "" {
		return false
	}
	dstate, ok := node.Annotations[mco.MachineConfigDaemonStateAnnotationKey]
	if !ok || dstate == "" {
		return false
	}

	if dstate == mco.MachineConfigDaemonStateDegraded || dstate == mco.MachineConfigDaemonStateUnreconcilable {
		return true
	}
	return false
}

func isNodeDraining(node *corev1.Node, isUpdating bool) bool {
	desiredDrain := node.Annotations[mco.DesiredDrainerAnnotationKey]
	appliedDrain := node.Annotations[mco.LastAppliedDrainerAnnotationKey]

	if appliedDrain == "" || desiredDrain == "" {
		return false
	}

	if desiredDrain != appliedDrain {
		desiredVerb := strings.Split(desiredDrain, "-")[0]
		if desiredVerb == mco.DrainerStateDrain {
			return true
		}
	}

	// Node is supposed to be updating but MCD hasn't had the time to update
	// its state from original `Done` to `Working` and start the drain process.
	// Default to drain process so that we don't report completed.
	mcdState := node.Annotations[mco.MachineConfigDaemonStateAnnotationKey]
	return isUpdating && mcdState == mco.MachineConfigDaemonStateDone
}

func updateStatusForControlPlaneNode(cpStatus *configv1alpha1.ControlPlaneUpdateStatus, node *corev1.Node, pool *mcfgv1.MachineConfigPool, mcLister mcfgv1listers.MachineConfigLister) {
	prototypeInformer := ensurePrototypeInformer(&cpStatus.Informers)
	nodeInsight := ensureNodeStatusInsight(&prototypeInformer.Insights, node)

	nodeInsight.PoolResource = configv1alpha1.PoolResourceRef{
		ResourceRef: configv1alpha1.ResourceRef{
			APIGroup: "machineconfiguration.openshift.io",
			Kind:     "MachineConfigPool",
			Name:     pool.Name,
		},
	}

	cvInsight := findClusterVersionInsight(prototypeInformer.Insights)

	machineConfigs, err := mcLister.List(labels.Everything())
	if err != nil {
		klog.Fatalf("USC :: SYNC :: Failed to list MachineConfigs")
	}
	currentVersion, foundCurrent := getOpenShiftVersionOfMachineConfig(machineConfigs, node.Annotations[mco.CurrentMachineConfigAnnotationKey])
	desiredVersion, foundDesired := getOpenShiftVersionOfMachineConfig(machineConfigs, node.Annotations[mco.DesiredMachineConfigAnnotationKey])

	isUnavailable, reasonOfUnavailability := isNodeUnavailable(node, pool)
	isDegraded := isNodeDegraded(node)
	isUpdated := foundCurrent && cvInsight.Versions.Target == currentVersion

	// foundCurrent makes sure we don't blip phase "updating" for nodes that we are not sure
	// of their actual phase, even though the conservative assumption is that the node is
	// at least updating or is updated.
	isUpdating := !isUpdated && foundCurrent && foundDesired && cvInsight.Versions.Target == desiredVersion

	updatingCondition := metav1.Condition{Type: string(configv1alpha1.NodeStatusInsightConditionTypeUpdating)}
	availableCondition := metav1.Condition{Type: string(configv1alpha1.NodeStatusInsightConditionTypeAvailable)}
	degradedCondition := metav1.Condition{Type: string(configv1alpha1.NodeStatusInsightConditionTypeDegraded)}

	switch {
	case isUpdating && isNodeDraining(node, isUpdating):
		updatingCondition.Status = metav1.ConditionTrue
		updatingCondition.Reason = string(configv1alpha1.NodeStatusInsightUpdatingReasonDraining)
		updatingCondition.Message = "Node is draining"
		nodeInsight.EstToComplete = metav1.Duration{Duration: 10 * time.Minute}
	case isUpdating:
		switch node.Annotations[mco.MachineConfigDaemonStateAnnotationKey] {
		case mco.MachineConfigDaemonStateWorking:
			updatingCondition.Status = metav1.ConditionTrue
			updatingCondition.Reason = string(configv1alpha1.NodeStatusInsightUpdatingReasonUpdating)
			updatingCondition.Message = "Node is updating"
			nodeInsight.EstToComplete = metav1.Duration{Duration: 5 * time.Minute}
		case mco.MachineConfigDaemonStateRebooting:
			updatingCondition.Status = metav1.ConditionTrue
			updatingCondition.Reason = string(configv1alpha1.NodeStatusInsightUpdatingReasonRebooting)
			updatingCondition.Message = "Node is rebooting"
			nodeInsight.EstToComplete = metav1.Duration{Duration: 5 * time.Minute}
		case mco.MachineConfigDaemonStateDone:
			updatingCondition.Status = metav1.ConditionFalse
			updatingCondition.Reason = string(configv1alpha1.NodeStatusInsightUpdatingReasonCompleted)
			updatingCondition.Message = "Node is updated"
		case "":
			updatingCondition.Status = metav1.ConditionTrue
			updatingCondition.Reason = string(configv1alpha1.NodeStatusInsightUpdatingReasonUpdating)
			updatingCondition.Message = "Node is updating"
			nodeInsight.EstToComplete = metav1.Duration{Duration: 5 * time.Minute}
		default:
			updatingCondition.Status = metav1.ConditionTrue
			updatingCondition.Reason = string(configv1alpha1.NodeStatusInsightUpdatingReasonUpdating)
			updatingCondition.Message = "Node is updating"
			nodeInsight.EstToComplete = metav1.Duration{Duration: 5 * time.Minute}
		}
	case isUpdated:
		updatingCondition.Status = metav1.ConditionFalse
		updatingCondition.Reason = string(configv1alpha1.NodeStatusInsightUpdatingReasonCompleted)
		updatingCondition.Message = "Node is updated"
	case pool.Spec.Paused:
		updatingCondition.Status = metav1.ConditionFalse
		updatingCondition.Reason = string(configv1alpha1.NodeStatusInsightUpdatingReasonPaused)
		updatingCondition.Message = "Node's parent MCP is paused"
	default:
		updatingCondition.Status = metav1.ConditionFalse
		updatingCondition.Reason = string(configv1alpha1.NodeStatusInsightUpdatingReasonPending)
		updatingCondition.Message = "Node is pending an update"
	}

	if isUnavailable && !isUpdating {
		availableCondition.Status = metav1.ConditionFalse
		availableCondition.Reason = "SomeUnavailabilityReason"
		availableCondition.Message = reasonOfUnavailability
		nodeInsight.Message = reasonOfUnavailability
		nodeInsight.EstToComplete = metav1.Duration{}
	} else {
		availableCondition.Status = metav1.ConditionTrue
		availableCondition.Reason = "AllIsWell"
		availableCondition.Message = "Node is available"
	}

	if isDegraded {
		degradedCondition.Status = metav1.ConditionTrue
		degradedCondition.Reason = "SomeDegradationReason"
		degradedCondition.Message = node.Annotations[mco.MachineConfigDaemonReasonAnnotationKey]
		nodeInsight.Message = node.Annotations[mco.MachineConfigDaemonReasonAnnotationKey]
	} else {
		degradedCondition.Status = metav1.ConditionFalse
		degradedCondition.Reason = "AllIsWell"
		degradedCondition.Message = "Node is not degraded"
	}

	nodeInsight.Version = currentVersion

	meta.SetStatusCondition(&nodeInsight.Conditions, availableCondition)
	meta.SetStatusCondition(&nodeInsight.Conditions, degradedCondition)
	meta.SetStatusCondition(&nodeInsight.Conditions, updatingCondition)

	// Recompute MCP summaries and assessments
}

func updateStatusForWorkerNode(wpStatuses *[]configv1alpha1.PoolUpdateStatus, node *corev1.Node, pool *mcfgv1.MachineConfigPool, mcLister mcfgv1listers.MachineConfigLister) {

}

func updateStatusForMachineConfigPool(cpStatus *configv1alpha1.ControlPlaneUpdateStatus, wpStatuses *[]configv1alpha1.PoolUpdateStatus, mcp *mcfgv1.MachineConfigPool) {
	if mcp.Name == mco.MachineConfigPoolMaster {
		updateStatusForControlPlaneMachineConfigPool(cpStatus, mcp)
	} else {
		updateStatusForWorkerMachineConfigPool(wpStatuses, mcp)
	}
}

func updateStatusForWorkerMachineConfigPool(wpStatuses *[]configv1alpha1.PoolUpdateStatus, mcp *mcfgv1.MachineConfigPool) {
	wpStatus := ensureWorkerPoolStatus(wpStatuses, mcp)
	prototypeInformer := ensurePrototypeInformer(&wpStatus.Informers)
	mcpInsight := ensureMachineConfigPoolInsight(&prototypeInformer.Insights, mcp)

	mcpInsight.Scope = configv1alpha1.ScopeTypeWorkerPool

	updateMachineConfigPoolStatusInsight(&wpStatus.Informers, mcp, mcpInsight)
}

func updateStatusForControlPlaneMachineConfigPool(cpStatus *configv1alpha1.ControlPlaneUpdateStatus, mcp *mcfgv1.MachineConfigPool) {
	prototypeInformer := ensurePrototypeInformer(&cpStatus.Informers)
	mcpInsight := ensureMachineConfigPoolInsight(&prototypeInformer.Insights, mcp)

	mcpInsight.Scope = configv1alpha1.ScopeTypeControlPlane

	updateMachineConfigPoolStatusInsight(&cpStatus.Informers, mcp, mcpInsight)
}

func updateMachineConfigPoolStatusInsight(informers *[]configv1alpha1.UpdateInformer, mcp *mcfgv1.MachineConfigPool, mcpInsight *configv1alpha1.MachineConfigPoolStatusInsight) {
	var total int32
	var pendingCount int32
	var updatedCount int32
	var availableCount int32
	var degradedCount int32
	var excludedCount int32
	var outdatedCount int32
	var drainingCount int32
	var progressingCount int32

	for _, informer := range *informers {
		for _, insight := range informer.Insights {
			if insight.Type != configv1alpha1.UpdateInsightTypeNodeStatusInsight {
				continue
			}

			nodeInsight := insight.NodeStatusInsight

			if nodeInsight.PoolResource.APIGroup != "machineconfiguration.openshift.io" ||
				nodeInsight.PoolResource.Kind != "MachineConfigPool" ||
				nodeInsight.PoolResource.Name != mcp.Name {
				continue
			}

			total++
			available := meta.FindStatusCondition(nodeInsight.Conditions, string(configv1alpha1.NodeStatusInsightConditionTypeAvailable))
			degraded := meta.FindStatusCondition(nodeInsight.Conditions, string(configv1alpha1.NodeStatusInsightConditionTypeDegraded))
			updating := meta.FindStatusCondition(nodeInsight.Conditions, string(configv1alpha1.NodeStatusInsightConditionTypeUpdating))

			if available != nil && available.Status == metav1.ConditionTrue {
				availableCount++
			}
			if degraded != nil && degraded.Status == metav1.ConditionTrue {
				degradedCount++
			}
			if updating != nil && updating.Status == metav1.ConditionFalse && updating.Reason == string(configv1alpha1.NodeStatusInsightUpdatingReasonPaused) {
				excludedCount++
				outdatedCount++
			}
			if updating != nil && updating.Status == metav1.ConditionFalse && updating.Reason == string(configv1alpha1.NodeStatusInsightUpdatingReasonPending) {
				pendingCount++
				outdatedCount++
			}
			if updating != nil && updating.Status == metav1.ConditionTrue && updating.Reason == string(configv1alpha1.NodeStatusInsightUpdatingReasonDraining) {
				drainingCount++
				outdatedCount++
				if degraded == nil || degraded.Status != metav1.ConditionTrue {
					progressingCount++
				}
			}
			if updating != nil && updating.Status == metav1.ConditionTrue && updating.Reason == string(configv1alpha1.NodeStatusInsightUpdatingReasonUpdating) {
				outdatedCount++
				if degraded == nil || degraded.Status != metav1.ConditionTrue {
					progressingCount++
				}
			}
			if updating != nil && updating.Status == metav1.ConditionTrue && updating.Reason == string(configv1alpha1.NodeStatusInsightUpdatingReasonRebooting) {
				outdatedCount++
				if degraded == nil || degraded.Status != metav1.ConditionTrue {
					progressingCount++
				}
			}
			if updating != nil && updating.Status == metav1.ConditionFalse && updating.Reason == string(configv1alpha1.NodeStatusInsightUpdatingReasonCompleted) {
				updatedCount++
			}
		}
	}

	setMachineConfigPoolSummary(&mcpInsight.Summaries, configv1alpha1.PoolNodesSummaryTypeTotal, total)
	setMachineConfigPoolSummary(&mcpInsight.Summaries, configv1alpha1.PoolNodesSummaryTypeAvailable, availableCount)
	setMachineConfigPoolSummary(&mcpInsight.Summaries, configv1alpha1.PoolNodesSummaryTypeProgressing, progressingCount)
	setMachineConfigPoolSummary(&mcpInsight.Summaries, configv1alpha1.PoolNodesSummaryTypeOutdated, outdatedCount)
	setMachineConfigPoolSummary(&mcpInsight.Summaries, configv1alpha1.PoolNodesSummaryTypeDraining, drainingCount)
	setMachineConfigPoolSummary(&mcpInsight.Summaries, configv1alpha1.PoolNodesSummaryTypeExcluded, excludedCount)
	setMachineConfigPoolSummary(&mcpInsight.Summaries, configv1alpha1.PoolNodesSummaryTypeDegraded, degradedCount)

	switch {
	case updatedCount == total:
		mcpInsight.Assessment = configv1alpha1.PoolUpdateAssessmentCompleted
	case pendingCount == total:
		mcpInsight.Assessment = configv1alpha1.PoolUpdateAssessmentPending
	case degradedCount > 0:
		mcpInsight.Assessment = configv1alpha1.PoolUpdateAssessmentDegraded
	case excludedCount > 0:
		mcpInsight.Assessment = configv1alpha1.PoolUpdateAssessmentExcluded
	default:
		mcpInsight.Assessment = configv1alpha1.PoolUpdateAssessmentProgressing
	}

	mcpInsight.Completion = int32(float64(updatedCount) / float64(total) * 100.0)
}

func setMachineConfigPoolSummary(summaries *[]configv1alpha1.PoolNodesUpdateSummary, summaryType configv1alpha1.PoolNodesSummaryType, count int32) {
	for i := range *summaries {
		if (*summaries)[i].Type == summaryType {
			(*summaries)[i].Count = count
			return
		}
	}
	*summaries = append(*summaries, configv1alpha1.PoolNodesUpdateSummary{Type: summaryType, Count: count})
}

func updateStatusForClusterOperator(cpStatus *configv1alpha1.ControlPlaneUpdateStatus, co *configv1.ClusterOperator) {
	prototypeInformer := findUpdateInformer(cpStatus.Informers, prototypeInformerName)
	if prototypeInformer == nil {
		cpStatus.Informers = append(cpStatus.Informers, configv1alpha1.UpdateInformer{Name: prototypeInformerName})
		prototypeInformer = &cpStatus.Informers[len(cpStatus.Informers)-1]
	}

	coInsight := findClusterOperatorInsight(prototypeInformer.Insights, co)
	if coInsight == nil {
		coInsight = &configv1alpha1.ClusterOperatorStatusInsight{
			Resource: configv1alpha1.ResourceRef{
				Name:     co.Name,
				Kind:     "ClusterOperator",
				APIGroup: "config.openshift.io",
			},
		}
		prototypeInformer.Insights = append(prototypeInformer.Insights, configv1alpha1.UpdateInsight{
			Type:                         configv1alpha1.UpdateInsightTypeClusterOperatorStatusInsight,
			ClusterOperatorStatusInsight: coInsight,
		})
		coInsight = prototypeInformer.Insights[len(prototypeInformer.Insights)-1].ClusterOperatorStatusInsight
	}

	coInsightHealthyType := string(configv1alpha1.ClusterOperatorStatusInsightConditionTypeHealthy)
	coAvailable := resourcemerge.FindOperatorStatusCondition(co.Status.Conditions, configv1.OperatorAvailable)
	coDegraded := resourcemerge.FindOperatorStatusCondition(co.Status.Conditions, configv1.OperatorDegraded)

	coInsightHealthy := metav1.Condition{Type: coInsightHealthyType}

	switch {
	case coAvailable != nil && coAvailable.Status == configv1.ConditionFalse:
		coInsightHealthy.Status = metav1.ConditionFalse
		coInsightHealthy.Reason = string(configv1alpha1.ClusterOperatorUpdateStatusInsightHealthyReasonUnavailable)
		coInsightHealthy.Message = coAvailable.Message
		coInsightHealthy.LastTransitionTime = coAvailable.LastTransitionTime
	case coDegraded != nil && coDegraded.Status == configv1.ConditionTrue:
		coInsightHealthy.Status = metav1.ConditionFalse
		coInsightHealthy.Reason = string(configv1alpha1.ClusterOperatorUpdateStatusInsightHealthyReasonDegraded)
		coInsightHealthy.Message = coDegraded.Message
		coInsightHealthy.LastTransitionTime = coDegraded.LastTransitionTime
	case coAvailable == nil:
		coInsightHealthy.Status = metav1.ConditionUnknown
		coInsightHealthy.Reason = string(configv1alpha1.ClusterOperatorUpdateStatusInsightHealthyReasonMissingAvailable)
		coInsightHealthy.Message = "ClusterOperator does not have an Available condition"
	case coDegraded == nil:
		coInsightHealthy.Status = metav1.ConditionUnknown
		coInsightHealthy.Reason = string(configv1alpha1.ClusterOperatorUpdateStatusInsightHealthyReasonMissingDegraded)
		coInsightHealthy.Message = "ClusterOperator does not have a Degraded condition"
	case coAvailable.Status == configv1.ConditionUnknown:
		coInsightHealthy.Status = metav1.ConditionUnknown
		coInsightHealthy.Reason = coAvailable.Reason
		coInsightHealthy.Message = coAvailable.Message
		coInsightHealthy.LastTransitionTime = coAvailable.LastTransitionTime
	case coDegraded.Status == configv1.ConditionUnknown:
		coInsightHealthy.Status = metav1.ConditionUnknown
		coInsightHealthy.Reason = coDegraded.Reason
		coInsightHealthy.Message = coDegraded.Message
		coInsightHealthy.LastTransitionTime = coDegraded.LastTransitionTime
	case coAvailable.Status == configv1.ConditionTrue && coDegraded.Status == configv1.ConditionFalse:
		coInsightHealthy.Status = metav1.ConditionTrue
		coInsightHealthy.Reason = string(configv1alpha1.ClusterOperatorUpdateStatusInsightHealthyReasonAllIsWell)
		coInsightHealthy.Message = "ClusterOperator is available and is not degraded"
		coInsightHealthy.LastTransitionTime = coAvailable.LastTransitionTime
	}

	meta.SetStatusCondition(&coInsight.Conditions, coInsightHealthy)

	coInsightUpdatingType := string(configv1alpha1.ClusterOperatorStatusInsightConditionTypeUpdating)
	coProgressing := resourcemerge.FindOperatorStatusCondition(co.Status.Conditions, configv1.OperatorProgressing)
	coInsightUpdating := metav1.Condition{Type: coInsightUpdatingType}

	cvInsight := findClusterVersionInsight(prototypeInformer.Insights)

	if cvInsight == nil {
		coInsightUpdating.Status = metav1.ConditionUnknown
		coInsightUpdating.Reason = string(configv1alpha1.ClusterOperatorStatusInsightUpdatingReasonUnknownUpdate)
		coInsightUpdating.Message = "No ClusterVersion status insight found => unknown cluster version"
	} else {
		cvInsightUpdating := meta.FindStatusCondition(cvInsight.Conditions, string(configv1alpha1.ClusterVersionStatusInsightConditionTypeUpdating))
		switch {
		case cvInsightUpdating == nil:
			coInsightUpdating.Status = metav1.ConditionUnknown
			coInsightUpdating.Reason = string(configv1alpha1.ClusterOperatorStatusInsightUpdatingReasonUnknownUpdate)
			coInsightUpdating.Message = "ClusterVersion status insight does not have an updating condition"
		case cvInsightUpdating.Status == metav1.ConditionUnknown:
			coInsightUpdating.Status = metav1.ConditionUnknown
			coInsightUpdating.Reason = string(configv1alpha1.ClusterOperatorStatusInsightUpdatingReasonUnknownUpdate)
			coInsightUpdating.Message = fmt.Sprintf("ClusterVersion insight Updating=Unknown: %s", cvInsightUpdating.Message)
		case cvInsightUpdating.Status == metav1.ConditionFalse:
			coInsightUpdating.Status = metav1.ConditionFalse
			coInsightUpdating.Reason = string(configv1alpha1.ClusterOperatorStatusInsightUpdatingReasonUpdated)
			coInsightUpdating.Message = fmt.Sprintf("Control plane is not updating: %s", cvInsightUpdating.Message)
		case cvInsightUpdating.Status == metav1.ConditionTrue:
			clusterTargetVersion := cvInsight.Versions.Target
			currentOperatorVersion := findOperatorVersion(co.Status.Versions)
			if currentOperatorVersion == nil || currentOperatorVersion.Version == "" {
				coInsightUpdating.Status = metav1.ConditionUnknown
				coInsightUpdating.Reason = string(configv1alpha1.ClusterOperatorStatusInsightUpdatingReasonUnknownVersion)
				coInsightUpdating.Message = "Current operator version is unknown"
			} else {
				if currentOperatorVersion.Version == clusterTargetVersion {
					coInsightUpdating.Status = metav1.ConditionFalse
					coInsightUpdating.Reason = string(configv1alpha1.ClusterOperatorStatusInsightUpdatingReasonUpdated)
					coInsightUpdating.Message = fmt.Sprintf("Operator finished updating to %s", clusterTargetVersion)
				} else {
					if coProgressing != nil && coProgressing.Status == configv1.ConditionTrue {
						coInsightUpdating.Status = metav1.ConditionTrue
						coInsightUpdating.Reason = string(configv1alpha1.ClusterOperatorStatusInsightUpdatingReasonProgressing)
						coInsightUpdating.Message = coProgressing.Message
					} else {
						coInsightUpdating.Status = metav1.ConditionFalse
						coInsightUpdating.Reason = string(configv1alpha1.ClusterOperatorStatusInsightUpdatingReasonPending)
						coInsightUpdating.Message = fmt.Sprintf("Operator is pending an update to %s", clusterTargetVersion)
					}
				}
			}
		}
	}

	meta.SetStatusCondition(&coInsight.Conditions, coInsightUpdating)
}

const prototypeInformerName = "ota-1268-prototype"

func versionsFromHistory(history []configv1.UpdateHistory) configv1alpha1.ControlPlaneUpdateVersions {
	versionData := configv1alpha1.ControlPlaneUpdateVersions{
		Target:   "unknown",
		Previous: "unknown",
	}

	if len(history) > 0 {
		versionData.Target = history[0].Version
	} else {
		return versionData
	}

	if len(history) == 1 {
		versionData.IsTargetInstall = true
		versionData.Previous = ""
		return versionData
	}
	if len(history) > 1 {
		versionData.Previous = history[1].Version
		versionData.IsPreviousPartial = history[1].State == configv1.PartialUpdate
	}

	// var insights []configv1alpha.UpdateInsight
	// controlPlaneCompleted := history[0].State == configv1.CompletedUpdate
	// if !controlPlaneCompleted && versionData.isPreviousPartial {
	// 	lastComplete := "unknown"
	// 	if len(history) > 2 {
	// 		for _, item := range history[2:] {
	// 			if item.State == configv1.CompletedUpdate {
	// 				lastComplete = item.Version
	// 				break
	// 			}
	// 		}
	// 	}
	// 	insights = []configv1alpha.UpdateInsight{
	// 		{
	// 			StartedAt: metav1.NewTime(history[0].StartedTime.Time),
	// 			Scope: configv1alpha.UpdateInsightScope{
	// 				Type:      configv1alpha.ScopeTypeControlPlane,
	// 				Resources: []configv1alpha.ResourceRef{cvScope},
	// 			},
	// 			Impact: configv1alpha.UpdateInsightImpact{
	// 				Level:       configv1alpha.WarningImpactLevel,
	// 				Type:        configv1alpha.NoneImpactType,
	// 				Summary:     fmt.Sprintf("Previous update to %s never completed, last complete update was %s", versionData.previous, lastComplete),
	// 				Description: fmt.Sprintf("Current update to %s was initiated while the previous update to version %s was still in progress", versionData.target, versionData.previous),
	// 			},
	// 			Remediation: configv1alpha.UpdateInsightRemediation{
	// 				Reference: "https://docs.openshift.com/container-platform/latest/updating/troubleshooting_updates/gathering-data-cluster-update.html#gathering-clusterversion-history-cli_troubleshooting_updates",
	// 			},
	// 		},
	// 	}
	// }

	return versionData
}

func updateStatusForClusterVersion(cpStatus *configv1alpha1.ControlPlaneUpdateStatus, cv *configv1.ClusterVersion) {
	prototypeInformer := ensurePrototypeInformer(&cpStatus.Informers)
	cvInsight := ensureClusterVersionInsight(&prototypeInformer.Insights, cv.Name)

	cvInsight.Versions = versionsFromHistory(cv.Status.History)

	if len(cv.Status.History) > 0 {
		cvInsight.StartedAt = metav1.NewTime(cv.Status.History[0].StartedTime.Time)
		if cv.Status.History[0].CompletionTime != nil {
			cvInsight.CompletedAt = metav1.NewTime(cv.Status.History[0].CompletionTime.Time)
		} else {
			cvInsight.CompletedAt = metav1.NewTime(time.Time{})
		}
	}

	cpUpdatingType := string(configv1alpha1.ControlPlaneConditionTypeUpdating)
	cvInsightUpdatingType := string(configv1alpha1.ClusterVersionStatusInsightConditionTypeUpdating)

	// Create one from the other (CP insight from CV insight)
	cpUpdatingCondition := metav1.Condition{Type: cpUpdatingType}
	cvInsightUpdating := metav1.Condition{Type: cvInsightUpdatingType}

	cvProgressing := resourcemerge.FindOperatorStatusCondition(cv.Status.Conditions, configv1.OperatorProgressing)

	if cvProgressing == nil {
		cpUpdatingCondition.Status = metav1.ConditionUnknown
		cpUpdatingCondition.Reason = string(configv1alpha1.ControlPlaneConditionUpdatingReasonClusterVersionWithoutProgressing)
		cpUpdatingCondition.Message = "ClusterVersion does not have a Progressing condition"

		cvInsightUpdating.Status = metav1.ConditionUnknown
		cvInsightUpdating.Reason = string(configv1alpha1.ClusterVersionStatusInsightUpdatingReasonNoProgressing)
		cvInsightUpdating.Message = "ClusterVersion does not have a Progressing condition"

		cvInsight.Assessment = configv1alpha1.ControlPlaneUpdateAssessmentDegraded
	} else {
		cpUpdatingCondition.Message = cvProgressing.Message
		cpUpdatingCondition.LastTransitionTime = cvProgressing.LastTransitionTime

		cvInsightUpdating.Message = cvProgressing.Message
		cvInsightUpdating.LastTransitionTime = cvProgressing.LastTransitionTime

		switch cvProgressing.Status {
		case configv1.ConditionTrue:
			cpUpdatingCondition.Status = metav1.ConditionTrue
			cpUpdatingCondition.Reason = string(configv1alpha1.ControlPlaneConditionUpdatingReasonClusterVersionProgressing)

			cvInsightUpdating.Status = metav1.ConditionTrue
			cvInsightUpdating.Reason = cvProgressing.Reason

			cvInsight.Assessment = configv1alpha1.ControlPlaneUpdateAssessmentProgressing

			cpUpdatingCondition.LastTransitionTime = cvInsight.StartedAt
			cvInsightUpdating.LastTransitionTime = cvInsight.StartedAt

		case configv1.ConditionFalse:
			cpUpdatingCondition.Status = metav1.ConditionFalse
			cpUpdatingCondition.Reason = string(configv1alpha1.ControlPlaneConditionUpdatingReasonClusterVersionNotProgressing)

			cvInsightUpdating.Status = metav1.ConditionFalse
			cvInsightUpdating.Reason = cvProgressing.Reason

			cvInsight.Assessment = configv1alpha1.ControlPlaneUpdateAssessmentCompleted

			cpUpdatingCondition.LastTransitionTime = cvInsight.CompletedAt
			cvInsightUpdating.LastTransitionTime = cvInsight.CompletedAt

		case configv1.ConditionUnknown:
			cpUpdatingCondition.Status = metav1.ConditionUnknown
			cpUpdatingCondition.Reason = string(configv1alpha1.ControlPlaneConditionUpdatingReasonClusterVersionProgressingUnknown)

			cvInsightUpdating.Status = metav1.ConditionUnknown
			cvInsightUpdating.Reason = cvProgressing.Reason

			cvInsight.Assessment = configv1alpha1.ControlPlaneUpdateAssessmentDegraded
		}

		if cpUpdatingCondition.LastTransitionTime.IsZero() {
			cpUpdatingCondition.LastTransitionTime = cvProgressing.LastTransitionTime
		}

		if cvInsightUpdating.LastTransitionTime.IsZero() {
			cvInsightUpdating.LastTransitionTime = cvProgressing.LastTransitionTime
		}

		if cpUpdatingCondition.Reason == "" {
			cpUpdatingCondition.Reason = "UnknownReason"
		}

		if cvInsightUpdating.Reason == "" {
			cvInsightUpdating.Reason = "UnknownReason"
		}
	}

	meta.SetStatusCondition(&cpStatus.Conditions, cpUpdatingCondition)
	meta.SetStatusCondition(&cvInsight.Conditions, cvInsightUpdating)
}

func ensureWorkerPoolStatus(wpStatuses *[]configv1alpha1.PoolUpdateStatus, mcp *mcfgv1.MachineConfigPool) *configv1alpha1.PoolUpdateStatus {
	wpStatus := findWorkerPoolStatus(*wpStatuses, mcp.Name)
	if wpStatus == nil {
		*wpStatuses = append(*wpStatuses, configv1alpha1.PoolUpdateStatus{
			Name: mcp.Name,
			Resource: configv1alpha1.PoolResourceRef{
				ResourceRef: configv1alpha1.ResourceRef{
					APIGroup: "machineconfiguration.openshift.io",
					Kind:     "MachineConfigPool",
					Name:     mcp.Name,
				},
			},
		})
		wpStatus = &(*wpStatuses)[len(*wpStatuses)-1]
	}
	return wpStatus
}

func ensureNodeStatusInsight(insights *[]configv1alpha1.UpdateInsight, node *corev1.Node) *configv1alpha1.NodeStatusInsight {
	nodeInsight := findNodeStatusInsight(*insights, node.Name)
	if nodeInsight == nil {
		nodeInsight = &configv1alpha1.NodeStatusInsight{
			Name: node.Name,
			Resource: configv1alpha1.ResourceRef{
				Name: node.Name,
				Kind: "Node",
			},
		}
		*insights = append(*insights, configv1alpha1.UpdateInsight{
			Type:              configv1alpha1.UpdateInsightTypeNodeStatusInsight,
			NodeStatusInsight: nodeInsight,
		})
		nodeInsight = (*insights)[len(*insights)-1].NodeStatusInsight
	}
	return nodeInsight

}

func ensureMachineConfigPoolInsight(insights *[]configv1alpha1.UpdateInsight, mcp *mcfgv1.MachineConfigPool) *configv1alpha1.MachineConfigPoolStatusInsight {
	mcpInsight := findMachineConfigPoolInsight(*insights, mcp.Name)
	if mcpInsight == nil {
		mcpInsight = &configv1alpha1.MachineConfigPoolStatusInsight{
			Name: mcp.Name,
			Resource: configv1alpha1.ResourceRef{
				Name:     mcp.Name,
				Kind:     "MachineConfigPool",
				APIGroup: "machineconfiguration.openshift.io",
			},
		}
		*insights = append(*insights, configv1alpha1.UpdateInsight{
			Type:                           configv1alpha1.UpdateInsightTypeMachineConfigPoolStatusInsight,
			MachineConfigPoolStatusInsight: mcpInsight,
		})
		mcpInsight = (*insights)[len(*insights)-1].MachineConfigPoolStatusInsight
	}
	return mcpInsight
}

func ensureClusterVersionInsight(insights *[]configv1alpha1.UpdateInsight, cvName string) *configv1alpha1.ClusterVersionStatusInsight {
	cvInsight := findClusterVersionInsight(*insights)
	if cvInsight == nil {
		cvInsight = &configv1alpha1.ClusterVersionStatusInsight{
			Resource: configv1alpha1.ResourceRef{
				Name:     cvName,
				Kind:     "ClusterVersion",
				APIGroup: "config.openshift.io",
			},
		}
		*insights = append(*insights, configv1alpha1.UpdateInsight{
			Type:                        configv1alpha1.UpdateInsightTypeClusterVersionStatusInsight,
			ClusterVersionStatusInsight: cvInsight,
		})
		cvInsight = (*insights)[len(*insights)-1].ClusterVersionStatusInsight
	}
	return cvInsight
}

func ensurePrototypeInformer(informers *[]configv1alpha1.UpdateInformer) *configv1alpha1.UpdateInformer {
	prototypeInformer := findUpdateInformer(*informers, prototypeInformerName)
	if prototypeInformer == nil {
		*informers = append(*informers, configv1alpha1.UpdateInformer{Name: prototypeInformerName})
		last := len(*informers) - 1
		prototypeInformer = &(*informers)[last]
	}
	return prototypeInformer
}

func findWorkerPoolStatus(statuses []configv1alpha1.PoolUpdateStatus, name string) *configv1alpha1.PoolUpdateStatus {
	for i := range statuses {
		if statuses[i].Name == name {
			return &statuses[i]
		}
	}
	return nil
}

func findNodeStatusInsight(insights []configv1alpha1.UpdateInsight, name string) *configv1alpha1.NodeStatusInsight {
	for i := range insights {
		if insights[i].Type == configv1alpha1.UpdateInsightTypeNodeStatusInsight && insights[i].NodeStatusInsight.Name == name {
			return insights[i].NodeStatusInsight
		}
	}
	return nil
}

func findMachineConfigPoolInsight(insights []configv1alpha1.UpdateInsight, name string) *configv1alpha1.MachineConfigPoolStatusInsight {
	for i := range insights {
		if insights[i].Type == configv1alpha1.UpdateInsightTypeMachineConfigPoolStatusInsight && insights[i].MachineConfigPoolStatusInsight.Resource.Name == name {
			return insights[i].MachineConfigPoolStatusInsight
		}
	}
	return nil
}

func findClusterVersionInsight(insights []configv1alpha1.UpdateInsight) *configv1alpha1.ClusterVersionStatusInsight {
	for i := range insights {
		if insights[i].Type == configv1alpha1.UpdateInsightTypeClusterVersionStatusInsight {
			return insights[i].ClusterVersionStatusInsight
		}
	}

	return nil
}

func findClusterOperatorInsight(insights []configv1alpha1.UpdateInsight, co *configv1.ClusterOperator) *configv1alpha1.ClusterOperatorStatusInsight {
	for _, insight := range insights {
		if insight.Type == configv1alpha1.UpdateInsightTypeClusterOperatorStatusInsight && insight.ClusterOperatorStatusInsight.Resource.Name == co.Name {
			return insight.ClusterOperatorStatusInsight
		}
	}

	return nil
}

func findUpdateInformer(informers []configv1alpha1.UpdateInformer, name string) *configv1alpha1.UpdateInformer {
	for i := range informers {
		if informers[i].Name == name {
			return &informers[i]
		}
	}
	return nil
}

func findOperatorVersion(versions []configv1.OperandVersion) *configv1.OperandVersion {
	if versions == nil {
		return nil
	}
	for i := range versions {
		if versions[i].Name == "operator" {
			return &versions[i]
		}
	}
	return nil
}

package updatestatus

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	corev1 "k8s.io/api/core/v1"
	kerrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	kubeinformers "k8s.io/client-go/informers"
	corelistersv1 "k8s.io/client-go/listers/core/v1"
	"k8s.io/client-go/util/workqueue"
	"k8s.io/klog/v2"

	machineconfigv1 "github.com/openshift/api/machineconfiguration/v1"
	configv1client "github.com/openshift/client-go/config/clientset/versioned"
	machineconfiginformers "github.com/openshift/client-go/machineconfiguration/informers/externalversions"
	machineconfigv1listers "github.com/openshift/client-go/machineconfiguration/listers/machineconfiguration/v1"
	"github.com/openshift/library-go/pkg/controller/factory"
	"github.com/openshift/library-go/pkg/operator/events"

	updatestatus "github.com/openshift/api/update/v1alpha1"
	"github.com/openshift/cluster-version-operator/pkg/updatestatus/mco"
)

// nodeInformerController is the controller that monitors health of the node resources
// and produces insights for node update.
type nodeInformerController struct {
	configClient       configv1client.Interface
	machineConfigs     machineconfigv1listers.MachineConfigLister
	machineConfigPools machineconfigv1listers.MachineConfigPoolLister
	nodes              corelistersv1.NodeLister
	recorder           events.Recorder

	// sendInsight should be called to send produced insights to the update status controller
	sendInsight sendInsightFn

	// once does the tasks that need to be executed once and only once at the beginning of its sync function
	// for each nodeInformerController instance, e.g., initializing caches.
	once sync.Once

	// mcpSelectors caches the label selectors converted from the node selectors of the machine config pools by their names.
	mcpSelectors machineConfigPoolSelectorCache

	// machineConfigVersions caches machine config versions which stores the name of MC as the key
	// and the release image version as its value retrieved from the annotation of the MC.
	machineConfigVersions machineConfigVersionCache

	// now is a function that returns the current time, used for testing
	now func() metav1.Time
}

func newNodeInformerController(
	configClient configv1client.Interface,
	coreInformers kubeinformers.SharedInformerFactory,
	machineConfigInformers machineconfiginformers.SharedInformerFactory,
	recorder events.Recorder,
	sendInsight sendInsightFn,
) factory.Controller {
	cpiRecorder := recorder.WithComponentSuffix("node-informer")

	c := &nodeInformerController{
		configClient:       configClient,
		machineConfigs:     machineConfigInformers.Machineconfiguration().V1().MachineConfigs().Lister(),
		machineConfigPools: machineConfigInformers.Machineconfiguration().V1().MachineConfigPools().Lister(),
		nodes:              coreInformers.Core().V1().Nodes().Lister(),
		recorder:           cpiRecorder,
		sendInsight:        sendInsight,

		now: metav1.Now,
	}

	nodeInformer := coreInformers.Core().V1().Nodes().Informer()
	mcInformer := machineConfigInformers.Machineconfiguration().V1().MachineConfigs().Informer()
	mcpInformer := machineConfigInformers.Machineconfiguration().V1().MachineConfigPools().Informer()

	controller := factory.New().
		// call sync on node changes
		WithInformersQueueKeysFunc(nodeInformerControllerQueueKeys, nodeInformer).
		// call sync on machine config changes
		WithInformersQueueKeysFunc(nodeInformerControllerQueueKeys, mcInformer).
		// call sync on machine config pool changes
		WithInformersQueueKeysFunc(nodeInformerControllerQueueKeys, mcpInformer).
		WithSync(c.sync).
		ToController("NodeInformer", c.recorder)

	return controller
}

// sync is called for any controller event. There are two kinds of them:
//   - standard Kubernetes informer events on watched resources such as nodes, machineConfigs, and machineConfigPools
//   - synthetic events such as eventNameReconcileAllNodes. They are generated for reconciling when handling events
//     of the first kind.
//
// A changed node resource produces insights that are sent to the update status controller.
// A changed mc/mcp may produce the synthetic event eventNameReconcileAllNodes which triggers the reconciliation of all nodes.
func (c *nodeInformerController) sync(ctx context.Context, syncCtx factory.SyncContext) error {
	// Warm up controller's caches.
	// This has to be called after informers caches have been synced and before the first event comes in.
	// The existing openshift-library does not provide such a hook.
	// In case any error occurs during cache initialization, we can still proceed which leads to stale insights that
	// will be corrected on the reconciliation when the caches are warmed up.
	c.once.Do(func() {
		if err := c.initializeMachineConfigPools(); err != nil {
			klog.Errorf("Failed to initialize machineConfigPoolSelectorCache: %v", err)
		}
		if err := c.initializeMachineConfigVersions(); err != nil {
			klog.Errorf("Failed to initialize machineConfigVersions: %v", err)
		}
	})

	queueKey := syncCtx.QueueKey()
	klog.V(4).Infof("NI :: Syncing with key %s", queueKey)

	t, name, err := parseNodeInformerControllerQueueKey(queueKey)
	if err != nil {
		return fmt.Errorf("failed to parse queue key: %w", err)
	}

	var msg informerMsg
	switch t {
	case syntheticKeyName:
		return c.syncSyntheticKey(name, syncCtx.Queue())
	case nodeKindName:
		msgP, err := c.syncNode(ctx, name)
		if err != nil {
			return err
		} else if msgP == nil {
			return nil
		}
		msg = *msgP
	case machineConfigKindName:
		return c.syncMachineConfig(name, syncCtx.Queue())
	case machineConfigPoolKindName:
		return c.syncMachineConfigPool(name, syncCtx.Queue())
	default:
		return fmt.Errorf("invalid queue key %s with unexpected type %s", queueKey, t)
	}
	klog.V(2).Infof("NI :: Syncing %s %s", t, name)
	c.sendInsight(msg)
	return nil
}

func (c *nodeInformerController) initializeMachineConfigPools() error {
	pools, err := c.machineConfigPools.List(labels.Everything())
	if err != nil {
		return err
	} else {
		for _, pool := range pools {
			c.mcpSelectors.ingest(pool)
		}
		klog.V(2).Infof("Ingested %d machineConfigPools in the cache", len(pools))
	}
	return nil
}

func (c *nodeInformerController) initializeMachineConfigVersions() error {
	machineConfigs, err := c.machineConfigs.List(labels.Everything())
	if err != nil {
		return err
	} else {
		for _, mc := range machineConfigs {
			c.machineConfigVersions.ingest(mc)
		}
		klog.V(2).Infof("Ingested %d machineConfig versions in the cache", len(machineConfigs))
	}
	return nil
}

type machineConfigVersionCache struct {
	cache sync.Map
}

func (c *machineConfigVersionCache) ingest(mc *machineconfigv1.MachineConfig) (bool, string) {
	if mcVersion, annotated := mc.Annotations[mco.ReleaseImageVersionAnnotationKey]; annotated && mcVersion != "" {
		previous, loaded := c.cache.Swap(mc.Name, mcVersion)
		if !loaded || previous.(string) != mcVersion {
			var previousStr string
			if loaded {
				previousStr = previous.(string)
			}
			return true, fmt.Sprintf("version for MachineConfig %s changed from %s from %s", mc.Name, previousStr, mcVersion)
		} else {
			return false, ""
		}
	}

	_, loaded := c.cache.LoadAndDelete(mc.Name)
	if loaded {
		return true, fmt.Sprintf("the previous version for MachineConfig %s deleted as no version can be found now", mc.Name)
	}
	return false, ""
}

func (c *machineConfigVersionCache) forget(name string) bool {
	_, loaded := c.cache.LoadAndDelete(name)
	return loaded
}

func (c *machineConfigVersionCache) versionFor(key string) (string, bool) {
	v, loaded := c.cache.Load(key)
	if !loaded {
		return "", false
	}
	return v.(string), true
}

type machineConfigPoolSelectorCache struct {
	cache sync.Map
}

func (c *machineConfigPoolSelectorCache) whichMCP(l labels.Labels) string {
	var ret string
	c.cache.Range(func(k, v interface{}) bool {
		s := v.(labels.Selector)
		if k == mco.MachineConfigPoolMaster && s.Matches(l) {
			ret = mco.MachineConfigPoolMaster
			return false
		}
		if s.Matches(l) {
			ret = k.(string)
			return ret == mco.MachineConfigPoolWorker
		}
		return true
	})
	return ret
}

func (c *machineConfigPoolSelectorCache) ingest(pool *machineconfigv1.MachineConfigPool) (bool, string) {
	s, err := metav1.LabelSelectorAsSelector(pool.Spec.NodeSelector)
	if err != nil {
		klog.Errorf("Failed to convert to a label selector from the node selector of MachineConfigPool %s: %v", pool.Name, err)
		v, loaded := c.cache.LoadAndDelete(pool.Name)
		if loaded {
			return true, fmt.Sprintf("the previous selector %s for MachineConfigPool %s deleted as its current node selector cannot be converted to a label selector: %v", v, pool.Name, err)
		} else {
			return false, ""
		}
	}

	previous, loaded := c.cache.Swap(pool.Name, s)
	if !loaded || previous.(labels.Selector).String() != s.String() {
		var vStr string
		if loaded {
			vStr = previous.(labels.Selector).String()
		}
		return true, fmt.Sprintf("selector for MachineConfigPool %s changed from %s to %s", pool.Name, vStr, s.String())
	}
	return false, ""
}

func (c *machineConfigPoolSelectorCache) forget(mcpName string) bool {
	_, loaded := c.cache.LoadAndDelete(mcpName)
	return loaded
}

func queueKeyFoReconcileAllNodes(queue workqueue.TypedRateLimitingInterface[any]) {
	queue.Add(queueKeyFor(syntheticKeyName, eventNameReconcileAllNodes))
}

func (c *nodeInformerController) reconcileAllNodes(queue workqueue.TypedRateLimitingInterface[any]) error {
	nodes, err := c.nodes.List(labels.Everything())
	if err != nil {
		return err
	}
	for _, node := range nodes {
		queue.Add(queueKeyFor(nodeKindName, node.Name))
	}
	return nil
}

func (c *nodeInformerController) syncMachineConfig(name string, q workqueue.TypedRateLimitingInterface[any]) error {
	machineConfig, err := c.machineConfigs.Get(name)
	if err != nil && !kerrors.IsNotFound(err) {
		return err
	}
	var changed bool
	var reason string
	if kerrors.IsNotFound(err) {
		changed = c.machineConfigVersions.forget(name)
		reason = fmt.Sprintf("MachineConfig %s is deleted", name)
	} else {
		changed, reason = c.machineConfigVersions.ingest(machineConfig)
	}
	if changed {
		klog.V(2).Infof("Reconciling all nodes: %s", reason)
		queueKeyFoReconcileAllNodes(q)
	}
	return nil
}

func (c *nodeInformerController) syncMachineConfigPool(name string, q workqueue.TypedRateLimitingInterface[any]) error {
	machineConfigPool, err := c.machineConfigPools.Get(name)
	if err != nil && !kerrors.IsNotFound(err) {
		return err
	}
	var changed bool
	var reason string
	if kerrors.IsNotFound(err) {
		changed = c.mcpSelectors.forget(name)
		reason = fmt.Sprintf("MachineConfigPool %s is deleted", name)
	} else {
		changed, reason = c.mcpSelectors.ingest(machineConfigPool)
	}
	if changed {
		klog.V(2).Infof("Reconciling all nodes: %s", reason)
		queueKeyFoReconcileAllNodes(q)
	}
	return nil
}

func (c *nodeInformerController) syncNode(ctx context.Context, name string) (*informerMsg, error) {
	node, err := c.nodes.Get(name)
	if err != nil {
		if kerrors.IsNotFound(err) {
			// TODO: Handle deletes by deleting the status insight
			return nil, nil
		}
		return nil, err
	}

	mcpName := c.mcpSelectors.whichMCP(labels.Set(node.Labels))
	if mcpName == "" {
		// We assume that every node belongs to an MCP at all time.
		// Although conceptually the assumption might not be true (see https://docs.openshift.com/container-platform/4.17/machine_configuration/index.html#architecture-machine-config-pools_machine-config-overview),
		// we will wait to hear from our users the issues for cluster updates and will handle them accordingly by then.
		klog.V(2).Infof("Ignored node %s as it does not belong to any machine config pool", node.Name)
		return nil, nil
	}
	klog.V(4).Infof("Node %s belongs to machine config pool %s", node.Name, mcpName)

	mcp, err := c.machineConfigPools.Get(mcpName)
	if err != nil {
		if kerrors.IsNotFound(err) {
			// it will be another event if an MCP is deleted
			return nil, nil
		}
		return nil, err
	}

	var mostRecentVersionInCVHistory string
	clusterVersion, err := c.configClient.ConfigV1().ClusterVersions().Get(ctx, "version", metav1.GetOptions{})
	if err != nil {
		return nil, err
	}
	if len(clusterVersion.Status.History) > 0 {
		mostRecentVersionInCVHistory = clusterVersion.Status.History[0].Version
	}

	now := c.now()
	var msg informerMsg
	if insight := assessNode(node, mcp, c.machineConfigVersions.versionFor, mostRecentVersionInCVHistory, now); insight != nil {
		msg, err = makeInsightMsgForNode(insight, now)
		if err != nil {
			klog.Errorf("BUG: Could not create insight message: %v", err)
			return nil, nil
		}
	}
	return &msg, nil
}

func (c *nodeInformerController) syncSyntheticKey(name string, q workqueue.TypedRateLimitingInterface[any]) error {
	if name == eventNameReconcileAllNodes {
		return c.reconcileAllNodes(q)
	}
	klog.Errorf("Got an invalid synthetic key %s", name)
	return nil
}

func makeInsightMsgForNode(nodeInsight *updatestatus.NodeStatusInsight, acquiredAt metav1.Time) (informerMsg, error) {
	insight := updatestatus.WorkerPoolInsight{
		UID:        fmt.Sprintf("node-%s", strings.Replace(nodeInsight.Resource.Name, ".", "-", -1)),
		AcquiredAt: acquiredAt,
		Insight: updatestatus.WorkerPoolInsightUnion{
			Type:              updatestatus.NodeStatusInsightType,
			NodeStatusInsight: nodeInsight,
		},
	}

	return makeWorkerPoolsInsightMsg(insight, nodesInformerName)
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

func determineConditions(pool *machineconfigv1.MachineConfigPool, node *corev1.Node, isUpdating, isUpdated, isUnavailable, isDegraded bool, lns *mco.LayeredNodeState, now metav1.Time) ([]metav1.Condition, string, *metav1.Duration) {
	var estimate *metav1.Duration

	updating := metav1.Condition{
		Type:               string(updatestatus.NodeStatusInsightUpdating),
		Status:             metav1.ConditionUnknown,
		Reason:             string(updatestatus.NodeCannotDetermine),
		Message:            "Cannot determine whether the node is updating",
		LastTransitionTime: now,
	}
	available := metav1.Condition{
		Type:               string(updatestatus.NodeStatusInsightAvailable),
		Status:             metav1.ConditionTrue,
		Reason:             "AsExpected",
		Message:            "The node is available",
		LastTransitionTime: now,
	}
	degraded := metav1.Condition{
		Type:               string(updatestatus.NodeStatusInsightDegraded),
		Status:             metav1.ConditionFalse,
		Reason:             "AsExpected",
		Message:            "The node is not degraded",
		LastTransitionTime: now,
	}

	if isUpdating && isNodeDraining(node, isUpdating) {
		estimate = toPointer(10 * time.Minute)
		updating.Status = metav1.ConditionTrue
		updating.Reason = string(updatestatus.NodeDraining)
		updating.Message = "The node is draining"
	} else if isUpdating {
		state := node.Annotations[mco.MachineConfigDaemonStateAnnotationKey]
		switch state {
		case mco.MachineConfigDaemonStateRebooting:
			estimate = toPointer(10 * time.Minute)
			updating.Status = metav1.ConditionTrue
			updating.Reason = string(updatestatus.NodeRebooting)
			updating.Message = "The node is rebooting"
		case mco.MachineConfigDaemonStateDone:
			estimate = toPointer(time.Duration(0))
			updating.Status = metav1.ConditionFalse
			updating.Reason = string(updatestatus.NodeCompleted)
			updating.Message = "The node is updated"
		default:
			estimate = toPointer(10 * time.Minute)
			updating.Status = metav1.ConditionTrue
			updating.Reason = string(updatestatus.NodeUpdating)
			updating.Message = "The node is updating"
		}

	} else if isUpdated {
		estimate = toPointer(time.Duration(0))
		updating.Status = metav1.ConditionFalse
		updating.Reason = string(updatestatus.NodeCompleted)
		updating.Message = "The node is updated"
	} else if pool.Spec.Paused {
		estimate = toPointer(time.Duration(0))
		updating.Status = metav1.ConditionFalse
		updating.Reason = string(updatestatus.NodePaused)
		updating.Message = "The update of the node is paused"
	} else {
		updating.Status = metav1.ConditionFalse
		updating.Reason = string(updatestatus.NodeUpdatePending)
		updating.Message = "The update of the node is pending"
	}

	// ATM, the insight's message is set only for the interesting cases: (isUnavailable && !isUpdating) || isDegraded
	// Moreover, the degraded message overwrites the unavailable one.
	// Those cases are inherited from the "oc adm upgrade" command as the baseline for the insight's message.
	// https://github.com/openshift/oc/blob/0cd37758b5ebb182ea911c157256c1b812c216c5/pkg/cli/admin/upgrade/status/workerpool.go#L194
	// We may add more cases in the future as needed
	var message string
	if isUnavailable && !isUpdating {
		estimate = nil
		if isUpdated {
			estimate = toPointer(time.Duration(0))
		}
		available.Status = metav1.ConditionFalse
		available.Reason = lns.GetUnavailableReason()
		available.Message = lns.GetUnavailableMessage()
		available.LastTransitionTime = metav1.Time{Time: lns.GetUnavailableSince()}
		message = available.Message
	}

	if isDegraded {
		estimate = nil
		if isUpdated {
			estimate = toPointer(time.Duration(0))
		}
		degraded.Status = metav1.ConditionTrue
		degraded.Reason = node.Annotations[mco.MachineConfigDaemonReasonAnnotationKey]
		degraded.Message = node.Annotations[mco.MachineConfigDaemonReasonAnnotationKey]
		message = degraded.Message
	}

	return []metav1.Condition{updating, available, degraded}, message, estimate
}

func toPointer(d time.Duration) *metav1.Duration {
	v := metav1.Duration{Duration: d}
	return &v
}

func assessNode(node *corev1.Node, mcp *machineconfigv1.MachineConfigPool, machineConfigToVersion func(string) (string, bool), mostRecentVersionInCVHistory string, now metav1.Time) *updatestatus.NodeStatusInsight {
	if node == nil || mcp == nil {
		return nil
	}

	desiredConfig, ok := node.Annotations[mco.DesiredMachineConfigAnnotationKey]
	noDesiredOnNode := !ok
	currentConfig := node.Annotations[mco.CurrentMachineConfigAnnotationKey]
	currentVersion, foundCurrent := machineConfigToVersion(currentConfig)
	desiredVersion, foundDesired := machineConfigToVersion(desiredConfig)

	lns := mco.NewLayeredNodeState(node)
	isUnavailable := lns.IsUnavailable(mcp)

	isDegraded := isNodeDegraded(node)
	isUpdated := foundCurrent && mostRecentVersionInCVHistory == currentVersion &&
		// The following condition is to handle the multi-arch migration because the version number stays the same there
		(noDesiredOnNode || currentConfig == desiredConfig)

	// foundCurrent makes sure we don't blip phase "updating" for nodes that we are not sure
	// of their actual phase, even though the conservative assumption is that the node is
	// at least updating or is updated.
	isUpdating := !isUpdated && foundCurrent && foundDesired && mostRecentVersionInCVHistory == desiredVersion

	conditions, message, estimate := determineConditions(mcp, node, isUpdating, isUpdated, isUnavailable, isDegraded, lns, now)

	scope := updatestatus.WorkerPoolScope
	if mcp.Name == mco.MachineConfigPoolMaster {
		scope = updatestatus.ControlPlaneScope
	}

	return &updatestatus.NodeStatusInsight{
		Name: node.Name,
		Resource: updatestatus.ResourceRef{
			Resource: "nodes",
			Group:    corev1.GroupName,
			Name:     node.Name,
		},
		PoolResource: updatestatus.PoolResourceRef{
			ResourceRef: updatestatus.ResourceRef{
				Resource: "machineconfigpools",
				Group:    machineconfigv1.GroupName,
				Name:     mcp.Name,
			},
		},
		Scope:               scope,
		Version:             currentVersion,
		EstimatedToComplete: estimate,
		Message:             message,
		Conditions:          conditions,
	}
}

const (
	nodeKindName              = "Node"
	machineConfigKindName     = "MachineConfig"
	machineConfigPoolKindName = "MachineConfigPool"

	syntheticKeyName = "synthetic"
	// eventNameReconcileAllNodes presents a synthetic event that is used when nodeInformerController should reconcile
	// all nodes.
	eventNameReconcileAllNodes = "reconcileAllNodes"

	nodesInformerName = "ni"
)

func parseNodeInformerControllerQueueKey(queueKey string) (string, string, error) {
	splits := strings.Split(queueKey, "/")
	if len(splits) != 2 {
		return "", "", fmt.Errorf("invalid queue key: %s", queueKey)
	}
	return splits[0], splits[1], nil
}

func nodeInformerControllerQueueKeys(object runtime.Object) []string {
	if object == nil {
		return nil
	}
	switch o := object.(type) {
	case *corev1.Node:
		return []string{queueKeyFor(nodeKindName, o.Name)}
	case *machineconfigv1.MachineConfig:
		return []string{queueKeyFor(machineConfigKindName, o.Name)}
	case *machineconfigv1.MachineConfigPool:
		return []string{queueKeyFor(machineConfigPoolKindName, o.Name)}
	default:
		panic(fmt.Sprintf("USC :: Unknown object type: %T", object))
	}
}

func queueKeyFor(kind, name string) string {
	return fmt.Sprintf("%s/%s", kind, name)
}

package updatestatus

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"gopkg.in/yaml.v3"

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

	// machineConfigPoolCache caches machine config pools
	machineConfigPoolCache     map[string]machineConfigPoolCacheData
	machineConfigPoolCacheLock sync.Mutex

	// machineConfigVersionCache caches machine config versions
	machineConfigVersionCache     map[string]string
	machineConfigVersionCacheLock sync.Mutex

	// now is a function that returns the current time, used for testing
	now func() metav1.Time
}

type machineConfigPoolCacheData struct {
	pool     *machineconfigv1.MachineConfigPool
	selector labels.Selector
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

		machineConfigPoolCache:    make(map[string]machineConfigPoolCacheData),
		machineConfigVersionCache: make(map[string]string),

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

func (c *nodeInformerController) sync(ctx context.Context, syncCtx factory.SyncContext) error {
	queueKey := syncCtx.QueueKey()
	klog.V(4).Infof("NI :: Syncing with key %s", queueKey)

	t, name, err := parseNodeInformerControllerQueueKey(queueKey)
	if err != nil {
		return fmt.Errorf("failed to parse queue key: %w", err)
	}

	var msg informerMsg
	switch t {
	case nodeKindName:
		node, err := c.nodes.Get(name)
		if err != nil {
			if kerrors.IsNotFound(err) {
				// TODO: Handle deletes by deleting the status insight
				return nil
			}
			return err
		}

		mcp := c.whichMCP(node)
		if mcp == nil {
			return fmt.Errorf("failed to determine which machine config pool the node belongs to")
		}
		klog.V(4).Infof("Node %s belongs to machine config pool %s", node.Name, mcp.Name)

		var mostRecentVersionInCVHistory string
		clusterVersion, err := c.configClient.ConfigV1().ClusterVersions().Get(ctx, "version", metav1.GetOptions{})
		if err != nil {
			return err
		}
		if len(clusterVersion.Status.History) > 0 {
			mostRecentVersionInCVHistory = clusterVersion.Status.History[0].Version
		}

		now := c.now()
		if insight := assessNode(node, mcp, c.machineConfigVersionCache, mostRecentVersionInCVHistory, now); insight != nil {
			msg = makeInsightMsgForNode(insight, now)
		}
	case machineConfigKindName:
		machineConfig, err := c.machineConfigs.Get(name)
		if err != nil && !kerrors.IsNotFound(err) {
			return err
		}
		if kerrors.IsNotFound(err) {
			// The machine config was deleted
			if changed := c.deleteMachineConfigIfExist(name); changed {
				klog.V(2).Infof("Reconciling all nodes as machine config %q is deleted", name)
				return c.reconcileAllNodes(syncCtx.Queue())
			}
			return nil
		}
		if refreshed := c.refreshMachineConfig(machineConfig); refreshed {
			klog.V(2).Infof("Reconciling all nodes as machine config %q is refreshed", name)
			return c.reconcileAllNodes(syncCtx.Queue())
		}
		return nil
	case machineConfigPoolKindName:
		machineConfigPool, err := c.machineConfigPools.Get(name)
		if err != nil && !kerrors.IsNotFound(err) {
			return err
		}
		if kerrors.IsNotFound(err) {
			// The pool was deleted
			if changed := c.deleteMachineConfigPoolIfExist(name); changed {
				klog.V(2).Infof("Reconciling all nodes as machine config pool %q is deleted", name)
				return c.reconcileAllNodes(syncCtx.Queue())
			}
			return nil
		}
		if refreshed := c.refreshMachineConfigPool(machineConfigPool); refreshed {
			klog.V(2).Infof("Reconciling all nodes as machine config pool %q is refreshed", name)
			return c.reconcileAllNodes(syncCtx.Queue())
		}
		return nil
	default:
		return fmt.Errorf("invalid queue key %s with unexpected type %s", queueKey, t)
	}
	var msgForLog string
	if klog.V(4).Enabled() {
		msgForLog = fmt.Sprintf(" | msg=%s", string(msg.insight))
	}
	klog.V(2).Infof("NI :: Syncing %s %s%s", t, name, msgForLog)
	c.sendInsight(msg)
	return nil
}

func (c *nodeInformerController) refreshMachineConfig(mc *machineconfigv1.MachineConfig) bool {
	c.machineConfigVersionCacheLock.Lock()
	defer c.machineConfigVersionCacheLock.Unlock()

	v, ok := c.machineConfigVersionCache[mc.Name]
	if openshiftVersion, exist := mc.Annotations[mco.ReleaseImageVersionAnnotationKey]; exist && openshiftVersion != "" {
		if !ok || v != openshiftVersion {
			c.machineConfigVersionCache[mc.Name] = openshiftVersion
			klog.V(4).Infof("Cached MachineConfig %s with version %s", mc.Name, openshiftVersion)
			return true
		}
	} else if ok {
		delete(c.machineConfigVersionCache, mc.Name)
		klog.V(4).Infof("Removed MachineConfig %s from the cache", mc.Name)
		return true
	}

	return false
}

func (c *nodeInformerController) deleteMachineConfigIfExist(name string) bool {
	c.machineConfigVersionCacheLock.Lock()
	defer c.machineConfigVersionCacheLock.Unlock()
	if _, ok := c.machineConfigVersionCache[name]; ok {
		delete(c.machineConfigVersionCache, name)
		klog.V(4).Infof("Deleted MachineConfig %s from the cache", name)
		return true
	}
	return false
}

func (c *nodeInformerController) refreshMachineConfigPool(pool *machineconfigv1.MachineConfigPool) bool {
	c.machineConfigPoolCacheLock.Lock()
	defer c.machineConfigPoolCacheLock.Unlock()

	v, ok := c.machineConfigPoolCache[pool.Name]
	s, err := metav1.LabelSelectorAsSelector(pool.Spec.NodeSelector)
	if err != nil {
		klog.Errorf("Failed to convert to a label selector from the node selector of MachineConfigPool %s : %v", pool.Name, err)
		if ok {
			delete(c.machineConfigPoolCache, pool.Name)
			klog.V(4).Infof("Removed MachineConfigPool %s from the cache", pool.Name)
			return true
		} else {
			return false
		}
	}
	if !ok || v.selector.String() != s.String() {
		c.machineConfigPoolCache[pool.Name] = machineConfigPoolCacheData{pool: pool, selector: s}
		klog.V(4).Infof("Cached MachineConfigPool %s with selector %s", pool.Name, s.String())
		return true
	}
	return false
}

func (c *nodeInformerController) deleteMachineConfigPoolIfExist(mcpName string) bool {
	c.machineConfigPoolCacheLock.Lock()
	defer c.machineConfigPoolCacheLock.Unlock()
	if _, ok := c.machineConfigPoolCache[mcpName]; ok {
		delete(c.machineConfigPoolCache, mcpName)
		klog.V(4).Infof("Deleted MachineConfigPool %s from the cache", mcpName)
		return true
	}
	return false
}
func (c *nodeInformerController) reconcileAllNodes(queue workqueue.TypedRateLimitingInterface[any]) error {
	nodes, err := c.nodes.List(labels.Everything())
	if err != nil {
		return err
	}
	for _, node := range nodes {
		queue.Add(kindAndNameToQueueKey(nodeKindName, node.Name))
	}
	return nil
}

func makeInsightMsgForNode(nodeInsight *NodeStatusInsight, acquiredAt metav1.Time) informerMsg {
	uid := fmt.Sprintf("usc-node-%s", nodeInsight.Resource.Name)
	insight := WorkerPoolInsight{
		UID:        uid,
		AcquiredAt: acquiredAt,
		WorkerPoolInsightUnion: WorkerPoolInsightUnion{
			Type:              NodeStatusInsightType,
			NodeStatusInsight: nodeInsight,
		},
	}
	// Should handle errors, but ultimately we will have a proper API and won’t need to serialize ourselves
	rawInsight, _ := yaml.Marshal(insight)
	return informerMsg{
		uid:     uid,
		insight: rawInsight,
	}
}

func (c *nodeInformerController) whichMCP(node *corev1.Node) *machineconfigv1.MachineConfigPool {
	c.machineConfigPoolCacheLock.Lock()
	defer c.machineConfigPoolCacheLock.Unlock()

	if v, ok := c.machineConfigPoolCache[mco.MachineConfigPoolMaster]; ok && v.selector.Matches(labels.Set(node.Labels)) {
		return v.pool
	}

	for k, v := range c.machineConfigPoolCache {
		if k == mco.MachineConfigPoolMaster || k == mco.MachineConfigPoolWorker {
			continue
		}
		if v.selector.Matches(labels.Set(node.Labels)) {
			return v.pool
		}
	}

	if v, ok := c.machineConfigPoolCache[mco.MachineConfigPoolWorker]; ok && v.selector.Matches(labels.Set(node.Labels)) {
		return v.pool
	}

	return nil
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
	var message string

	updating := metav1.Condition{
		Type:               string(NodeStatusInsightUpdating),
		Status:             metav1.ConditionUnknown,
		Reason:             string(NodeCannotDetermine),
		LastTransitionTime: now,
	}
	available := metav1.Condition{
		Type:               string(NodeStatusInsightAvailable),
		Status:             metav1.ConditionTrue,
		LastTransitionTime: now,
	}
	degraded := metav1.Condition{
		Type:               string(NodeStatusInsightDegraded),
		Status:             metav1.ConditionFalse,
		LastTransitionTime: now,
	}

	if isUpdating && isNodeDraining(node, isUpdating) {
		estimate = toPointer(10 * time.Minute)
		updating.Status = metav1.ConditionTrue
		updating.Reason = string(NodeDraining)
	} else if isUpdating {
		state := node.Annotations[mco.MachineConfigDaemonStateAnnotationKey]
		switch state {
		case mco.MachineConfigDaemonStateRebooting:
			estimate = toPointer(10 * time.Minute)
			updating.Status = metav1.ConditionTrue
			updating.Reason = string(NodeRebooting)
		case mco.MachineConfigDaemonStateDone:
			estimate = toPointer(time.Duration(0))
			updating.Status = metav1.ConditionFalse
			updating.Reason = string(NodeCompleted)
		default:
			estimate = toPointer(10 * time.Minute)
			updating.Status = metav1.ConditionTrue
			updating.Reason = string(NodeUpdating)
		}

	} else if isUpdated {
		estimate = toPointer(time.Duration(0))
		updating.Status = metav1.ConditionFalse
		updating.Reason = string(NodeCompleted)
	} else if pool.Spec.Paused {
		estimate = toPointer(time.Duration(0))
		updating.Status = metav1.ConditionFalse
		updating.Reason = string(NodePaused)
	} else {
		updating.Status = metav1.ConditionFalse
		updating.Reason = string(NodeUpdatePending)
	}
	message = updating.Message

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

func assessNode(node *corev1.Node, mcp *machineconfigv1.MachineConfigPool, machineConfigVersions map[string]string, mostRecentVersionInCVHistory string, now metav1.Time) *NodeStatusInsight {
	if node == nil || mcp == nil {
		return nil
	}

	desiredConfig, ok := node.Annotations[mco.DesiredMachineConfigAnnotationKey]
	noDesiredOnNode := !ok
	currentConfig := node.Annotations[mco.CurrentMachineConfigAnnotationKey]
	currentVersion, foundCurrent := machineConfigVersions[currentConfig]
	desiredVersion, foundDesired := machineConfigVersions[desiredConfig]

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

	scope := WorkerPoolScope
	if mcp.Name == mco.MachineConfigPoolMaster {
		scope = ControlPlaneScope
	}

	return &NodeStatusInsight{
		Name: node.Name,
		Resource: ResourceRef{
			Resource: "nodes",
			Group:    corev1.GroupName,
			Name:     node.Name,
		},
		PoolResource: PoolResourceRef{
			ResourceRef: ResourceRef{
				Resource: "machineconfigpools",
				Group:    machineconfigv1.GroupName,
				Name:     mcp.Name,
			},
		},
		Scope:         scope,
		Version:       currentVersion,
		EstToComplete: estimate,
		Message:       message,
		Conditions:    conditions,
	}
}

const (
	nodeKindName              = "Node"
	machineConfigKindName     = "MachineConfig"
	machineConfigPoolKindName = "MachineConfigPool"
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
		return []string{kindAndNameToQueueKey(nodeKindName, o.Name)}
	case *machineconfigv1.MachineConfig:
		return []string{kindAndNameToQueueKey(machineConfigKindName, o.Name)}
	case *machineconfigv1.MachineConfigPool:
		return []string{kindAndNameToQueueKey(machineConfigPoolKindName, o.Name)}
	default:
		panic(fmt.Sprintf("USC :: Unknown object type: %T", object))
	}
}

func kindAndNameToQueueKey(kind, name string) string {
	return fmt.Sprintf("%s/%s", kind, name)
}

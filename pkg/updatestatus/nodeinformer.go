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
	kutilerrors "k8s.io/apimachinery/pkg/util/errors"
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

	updatestatus "github.com/openshift/cluster-version-operator/pkg/updatestatus/api"
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

	// machineConfigPoolSelectorCache caches the label selectors converted from the node selectors of the machine config pools by their names.
	machineConfigPoolSelectorCache machineConfigPoolSelectorCache

	// machineConfigVersionCache caches machine config versions
	// The cache stores the name of MC as the key and the release image version as its value which is retrieved from the annotation of the MC.
	machineConfigVersionCache machineConfigVersionCache

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

var once sync.Once

func (c *nodeInformerController) sync(ctx context.Context, syncCtx factory.SyncContext) error {
	// Warm up controller's caches.
	// This has to be called after informers caches have been synced and before the first event comes in.
	// The existing openshift-library does not provide such a hook.
	once.Do(c.initializeCachesNoErrors)

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

		mcpName := c.machineConfigPoolSelectorCache.whichMCP(labels.Set(node.Labels))
		if mcpName == "" {
			// We assume that every node belongs to a MCP at all time.
			// Although conceptually the assumption might not be true (see https://docs.openshift.com/container-platform/4.17/machine_configuration/index.html#architecture-machine-config-pools_machine-config-overview),
			// we will wait to hear from our users the issues for cluster updates and will handle them accordingly by then.
			klog.V(2).Infof("Ignored node %s as it does not belong to any %d machine config pool(s)", node.Name, c.machineConfigPoolSelectorCache.len())
			return nil
		}
		klog.V(4).Infof("Node %s belongs to machine config pool %s", node.Name, mcpName)

		mcp, err := c.machineConfigPools.Get(mcpName)
		if err != nil {
			if kerrors.IsNotFound(err) {
				// it will be another event if a MCP is deleted
				return nil
			}
			return err
		}

		var mostRecentVersionInCVHistory string
		clusterVersion, err := c.configClient.ConfigV1().ClusterVersions().Get(ctx, "version", metav1.GetOptions{})
		if err != nil {
			return err
		}
		if len(clusterVersion.Status.History) > 0 {
			mostRecentVersionInCVHistory = clusterVersion.Status.History[0].Version
		}

		now := c.now()
		if insight := assessNode(node, mcp, c.machineConfigVersionCache.match, mostRecentVersionInCVHistory, now); insight != nil {
			msg, err = makeInsightMsgForNode(insight, now)
			if err != nil {
				klog.Errorf("BUG: Could not create insight message: %v", err)
				return nil
			}
		}
	case machineConfigKindName:
		machineConfig, err := c.machineConfigs.Get(name)
		if err != nil && !kerrors.IsNotFound(err) {
			return err
		}
		if kerrors.IsNotFound(err) {
			// The machine config was deleted
			if changed := c.machineConfigVersionCache.forget(name); changed {

				klog.V(2).Infof("Reconciling all nodes as machine config %q is deleted", name)
				return c.reconcileAllNodes(syncCtx.Queue())
			}
			return nil
		}
		if changed := c.machineConfigVersionCache.ingest(machineConfig); changed {
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
			if changed := c.machineConfigPoolSelectorCache.forget(name); changed {
				klog.V(2).Infof("Reconciling all nodes as machine config pool %q is deleted", name)
				return c.reconcileAllNodes(syncCtx.Queue())
			}
			return nil
		}
		if changed := c.machineConfigPoolSelectorCache.ingest(machineConfigPool); changed {
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

func (c *nodeInformerController) initializeCachesNoErrors() {
	if err := c.initializeCaches(); err != nil {
		klog.Errorf("Failed to initialize caches: %v", err)
	}
}

func (c *nodeInformerController) initializeCaches() error {
	var errs []error

	if pools, err := c.machineConfigPools.List(labels.Everything()); err != nil {
		errs = append(errs, err)
	} else {
		for _, pool := range pools {
			c.machineConfigPoolSelectorCache.ingest(pool)
		}
	}
	klog.V(2).Infof("Stored %d machineConfigPools in the cache", c.machineConfigPoolSelectorCache.len())

	machineConfigs, err := c.machineConfigs.List(labels.Everything())
	if err != nil {
		errs = append(errs, err)
	} else {
		for _, mc := range machineConfigs {
			c.machineConfigVersionCache.ingest(mc)
		}
	}
	klog.V(2).Infof("Stored %d machineConfig versions in the cache", c.machineConfigVersionCache.len())

	return kutilerrors.NewAggregate(errs)
}

type machineConfigVersionCache struct {
	cache map[string]string
	lock  sync.Mutex
}

func (c *machineConfigVersionCache) ingest(mc *machineconfigv1.MachineConfig) bool {
	c.lock.Lock()
	defer c.lock.Unlock()

	v, ok := c.cache[mc.Name]
	if openshiftVersion, exist := mc.Annotations[mco.ReleaseImageVersionAnnotationKey]; exist && openshiftVersion != "" {
		if !ok || v != openshiftVersion {
			if c.cache == nil {
				c.cache = make(map[string]string)
			}
			c.cache[mc.Name] = openshiftVersion
			klog.V(4).Infof("Cached MachineConfig %s with version %s", mc.Name, openshiftVersion)
			return true
		}
	} else if ok {
		delete(c.cache, mc.Name)
		klog.V(4).Infof("Deleted MachineConfig %s from the cache as no version can be found", mc.Name)
		return true
	}

	return false
}

func (c *machineConfigVersionCache) forget(name string) bool {
	c.lock.Lock()
	defer c.lock.Unlock()
	if _, ok := c.cache[name]; ok {
		delete(c.cache, name)
		klog.V(4).Infof("Deleted MachineConfig %s from the cache", name)
		return true
	}
	return false
}

func (c *machineConfigVersionCache) match(config string) (string, bool) {
	c.lock.Lock()
	defer c.lock.Unlock()
	v, ok := c.cache[config]
	return v, ok
}

func (c *machineConfigVersionCache) len() int {
	c.lock.Lock()
	defer c.lock.Unlock()
	return len(c.cache)
}

type machineConfigPoolSelectorCache struct {
	cache map[string]labels.Selector
	lock  sync.Mutex
}

func (c *machineConfigPoolSelectorCache) whichMCP(labels labels.Labels) string {
	c.lock.Lock()
	defer c.lock.Unlock()

	if v, ok := c.cache[mco.MachineConfigPoolMaster]; ok && v.Matches(labels) {
		return mco.MachineConfigPoolMaster
	}

	for k, v := range c.cache {
		if k == mco.MachineConfigPoolMaster || k == mco.MachineConfigPoolWorker {
			continue
		}
		if v.Matches(labels) {
			return k
		}
	}

	if v, ok := c.cache[mco.MachineConfigPoolWorker]; ok && v.Matches(labels) {
		return mco.MachineConfigPoolWorker
	}

	return ""
}

func (c *machineConfigPoolSelectorCache) ingest(pool *machineconfigv1.MachineConfigPool) bool {
	c.lock.Lock()
	defer c.lock.Unlock()

	v, ok := c.cache[pool.Name]
	s, err := metav1.LabelSelectorAsSelector(pool.Spec.NodeSelector)
	if err != nil {
		klog.Errorf("Failed to convert to a label selector from the node selector of MachineConfigPool %s : %v", pool.Name, err)
		if ok {
			delete(c.cache, pool.Name)
			klog.V(4).Infof("Deleted MachineConfigPool %s from the cache", pool.Name)
			return true
		} else {
			return false
		}
	}
	if !ok || v.String() != s.String() {
		if c.cache == nil {
			c.cache = make(map[string]labels.Selector)
		}
		c.cache[pool.Name] = s
		klog.V(4).Infof("Cached MachineConfigPool %s with selector %s", pool.Name, s.String())
		return true
	}
	return false
}

func (c *machineConfigPoolSelectorCache) forget(mcpName string) bool {
	c.lock.Lock()
	defer c.lock.Unlock()
	if _, ok := c.cache[mcpName]; ok {
		delete(c.cache, mcpName)
		klog.V(4).Infof("Deleted MachineConfigPool %s from the cache", mcpName)
		return true
	}
	return false
}

func (c *machineConfigPoolSelectorCache) len() int {
	c.lock.Lock()
	defer c.lock.Unlock()
	return len(c.cache)
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

func makeInsightMsgForNode(nodeInsight *updatestatus.NodeStatusInsight, acquiredAt metav1.Time) (informerMsg, error) {
	insight := updatestatus.WorkerPoolInsight{
		UID:        fmt.Sprintf("node-%s", nodeInsight.Resource.Name),
		AcquiredAt: acquiredAt,
		WorkerPoolInsightUnion: updatestatus.WorkerPoolInsightUnion{
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

func assessNode(node *corev1.Node, mcp *machineconfigv1.MachineConfigPool, machineConfigVersionMatcher func(string) (string, bool), mostRecentVersionInCVHistory string, now metav1.Time) *updatestatus.NodeStatusInsight {
	if node == nil || mcp == nil {
		return nil
	}

	desiredConfig, ok := node.Annotations[mco.DesiredMachineConfigAnnotationKey]
	noDesiredOnNode := !ok
	currentConfig := node.Annotations[mco.CurrentMachineConfigAnnotationKey]
	currentVersion, foundCurrent := machineConfigVersionMatcher(currentConfig)
	desiredVersion, foundDesired := machineConfigVersionMatcher(desiredConfig)

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

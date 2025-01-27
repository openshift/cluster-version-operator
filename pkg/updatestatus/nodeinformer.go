package updatestatus

import (
	"context"
	"fmt"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	corev1 "k8s.io/api/core/v1"
	kerrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	kubeinformers "k8s.io/client-go/informers"
	corelistersv1 "k8s.io/client-go/listers/core/v1"
	"k8s.io/klog/v2"

	machineconfigv1 "github.com/openshift/api/machineconfiguration/v1"
	configv1client "github.com/openshift/client-go/config/clientset/versioned"
	machineconfiginformers "github.com/openshift/client-go/machineconfiguration/informers/externalversions"
	machineconfigv1listers "github.com/openshift/client-go/machineconfiguration/listers/machineconfiguration/v1"
	"github.com/openshift/library-go/pkg/controller/factory"
	"github.com/openshift/library-go/pkg/operator/events"
	mcocontrollercommon "github.com/openshift/machine-config-operator/pkg/controller/common"
	mcodeamonconstants "github.com/openshift/machine-config-operator/pkg/daemon/constants"

	localmco "github.com/openshift/cluster-version-operator/pkg/updatestatus/mco"
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

	controller := factory.New().
		// call sync on node changes
		WithInformersQueueKeysFunc(nodeInformerControllerQueueKeys, nodeInformer).
		WithSync(c.sync).
		ToController("NodeInformer", c.recorder)

	return controller
}

func (c *nodeInformerController) sync(ctx context.Context, syncCtx factory.SyncContext) error {
	queueKey := syncCtx.QueueKey()

	t, name, err := parseQueueKey(queueKey)
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

		pools, err := c.machineConfigPools.List(labels.Everything())
		if err != nil {
			return err
		}
		mcp, err := whichMCP(node, pools)
		if err != nil {
			return fmt.Errorf("failed to determine which machine config pool the node belongs to: %w", err)
		}

		machineConfigs, err := c.machineConfigs.List(labels.Everything())
		if err != nil {
			return err
		}

		machineConfigVersions := map[string]string{}
		for _, mc := range machineConfigs {
			if openshiftVersion, ok := mc.Annotations[mcocontrollercommon.ReleaseImageVersionAnnotationKey]; ok && openshiftVersion != "" {
				machineConfigVersions[mc.Name] = openshiftVersion
			}
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
		if insight := assessNode(node, mcp, machineConfigVersions, mostRecentVersionInCVHistory, now); insight != nil {
			msg = makeInsightMsgForNode(insight, now)
		}
	default:
		return fmt.Errorf("invalid queue key %s with unexpected type %s", queueKey, t)
	}
	var msgForLog string
	if klog.V(4).Enabled() {
		msgForLog = fmt.Sprintf(" | msg=%s", string(msg.insight))
	}
	klog.V(2).Infof("CPI :: Syncing %s %s%s", t, name, msgForLog)
	c.sendInsight(msg)
	return nil
}

func makeInsightMsgForNode(nodeInsight *NodeStatusInsight, acquiredAt metav1.Time) informerMsg {
	uid := fmt.Sprintf("usc-node-%s", nodeInsight.Resource.Name)
	insight := Insight{
		UID:        uid,
		AcquiredAt: acquiredAt,
		InsightUnion: InsightUnion{
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

func whichMCP(node *corev1.Node, pools []*machineconfigv1.MachineConfigPool) (*machineconfigv1.MachineConfigPool, error) {
	var masterSelector labels.Selector
	var workerSelector labels.Selector
	customSelectors := map[string]labels.Selector{}
	poolsMap := make(map[string]*machineconfigv1.MachineConfigPool, len(pools))
	for _, pool := range pools {
		poolsMap[pool.Name] = pool
		s, err := metav1.LabelSelectorAsSelector(pool.Spec.NodeSelector)
		if err != nil {
			return nil, fmt.Errorf("failed to get label selector from the pool %s: %w", pool.Name, err)
		}
		switch pool.Name {
		case mcocontrollercommon.MachineConfigPoolMaster:
			masterSelector = s
		case mcocontrollercommon.MachineConfigPoolWorker:
			workerSelector = s
		default:
			customSelectors[pool.Name] = s
		}
	}

	if masterSelector != nil && masterSelector.Matches(labels.Set(node.Labels)) {
		return poolsMap[mcocontrollercommon.MachineConfigPoolMaster], nil
	}
	for name, selector := range customSelectors {
		if selector.Matches(labels.Set(node.Labels)) {
			return poolsMap[name], nil
		}
	}
	if workerSelector != nil && workerSelector.Matches(labels.Set(node.Labels)) {
		return poolsMap[mcocontrollercommon.MachineConfigPoolWorker], nil
	}
	return nil, fmt.Errorf("failed to find a matching node selector from %d machine config pools", len(pools))
}

func isNodeDegraded(node *corev1.Node) bool {
	// Inspired by: https://github.com/openshift/machine-config-operator/blob/master/pkg/controller/node/status.go
	if node.Annotations == nil {
		return false
	}
	dconfig, ok := node.Annotations[mcodeamonconstants.DesiredMachineConfigAnnotationKey]
	if !ok || dconfig == "" {
		return false
	}
	dstate, ok := node.Annotations[mcodeamonconstants.MachineConfigDaemonStateAnnotationKey]
	if !ok || dstate == "" {
		return false
	}

	if dstate == mcodeamonconstants.MachineConfigDaemonStateDegraded || dstate == mcodeamonconstants.MachineConfigDaemonStateUnreconcilable {
		return true
	}
	return false
}

func isNodeDraining(node *corev1.Node, isUpdating bool) bool {
	desiredDrain := node.Annotations[mcodeamonconstants.DesiredDrainerAnnotationKey]
	appliedDrain := node.Annotations[mcodeamonconstants.LastAppliedDrainerAnnotationKey]

	if appliedDrain == "" || desiredDrain == "" {
		return false
	}

	if desiredDrain != appliedDrain {
		desiredVerb := strings.Split(desiredDrain, "-")[0]
		if desiredVerb == mcodeamonconstants.DrainerStateDrain {
			return true
		}
	}

	// Node is supposed to be updating but MCD hasn't had the time to update
	// its state from original `Done` to `Working` and start the drain process.
	// Default to drain process so that we don't report completed.
	mcdState := node.Annotations[mcodeamonconstants.MachineConfigDaemonStateAnnotationKey]
	return isUpdating && mcdState == mcodeamonconstants.MachineConfigDaemonStateDone
}

func determineConditions(pool *machineconfigv1.MachineConfigPool, node *corev1.Node, isUpdating, isUpdated, isUnavailable, isDegraded bool, lns *localmco.LayeredNodeState, now metav1.Time) ([]metav1.Condition, string, *metav1.Duration) {
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
		state := node.Annotations[mcodeamonconstants.MachineConfigDaemonStateAnnotationKey]
		switch state {
		case mcodeamonconstants.MachineConfigDaemonStateRebooting:
			estimate = toPointer(10 * time.Minute)
			updating.Status = metav1.ConditionTrue
			updating.Reason = string(NodeRebooting)
		case mcodeamonconstants.MachineConfigDaemonStateDone:
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
		degraded.Reason = node.Annotations[mcodeamonconstants.MachineConfigDaemonReasonAnnotationKey]
		degraded.Message = node.Annotations[mcodeamonconstants.MachineConfigDaemonReasonAnnotationKey]
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

	desiredConfig, ok := node.Annotations[mcodeamonconstants.DesiredMachineConfigAnnotationKey]
	currentVersion, foundCurrent := machineConfigVersions[node.Annotations[mcodeamonconstants.CurrentMachineConfigAnnotationKey]]
	desiredVersion, foundDesired := machineConfigVersions[desiredConfig]

	lns := localmco.NewLayeredNodeState(node)
	isUnavailable := lns.IsUnavailable(mcp)

	isDegraded := isNodeDegraded(node)
	isUpdated := foundCurrent && mostRecentVersionInCVHistory == currentVersion &&
		// The following condition is to handle the multi-arch migration because the version number stays the same there
		(!ok || node.Annotations[mcodeamonconstants.CurrentMachineConfigAnnotationKey] == desiredConfig)

	// foundCurrent makes sure we don't blip phase "updating" for nodes that we are not sure
	// of their actual phase, even though the conservative assumption is that the node is
	// at least updating or is updated.
	isUpdating := !isUpdated && foundCurrent && foundDesired && mostRecentVersionInCVHistory == desiredVersion

	conditions, message, estimate := determineConditions(mcp, node, isUpdating, isUpdated, isUnavailable, isDegraded, lns, now)

	scope := WorkerPoolScope
	if mcp.Name == mcocontrollercommon.MachineConfigPoolMaster {
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
	nodeKindName = "Node"
)

func nodeInformerControllerQueueKeys(object runtime.Object) []string {
	if object == nil {
		return nil
	}
	switch o := object.(type) {
	case *corev1.Node:
		return []string{fmt.Sprintf("%s/%s", nodeKindName, o.Name)}
	default:
		panic(fmt.Sprintf("USC :: Unknown object type: %T", object))
	}
}

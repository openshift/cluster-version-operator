// This is a modified https://github.com/openshift/machine-config-operator/blob/11d5151a784c7d4be5255ea41acfbf5092eda592/pkg/controller/common/layered_node_state.go
// TODO: Replace this file with the original MCO code when transitioning to server-side
package mco

import (
	"fmt"

	mcfgv1 "github.com/openshift/api/machineconfiguration/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/klog/v2"
)

// This is intended to provide a singular way to interrogate node objects to
// determine if they're in a specific state. A secondary goal is to provide a
// singular way to mutate node objects for the purposes of updating their
// current configurations.
//
// The eventual goal is to replace all of the node status functions in
// status.go with this code, then repackage this so that it can be used by any
// portion of the MCO which needs to interrogate or mutate node state.
type LayeredNodeState struct {
	node                   *corev1.Node
	ReasonOfUnavailability string
}

func NewLayeredNodeState(n *corev1.Node) *LayeredNodeState {
	return &LayeredNodeState{node: n}
}

// Augements the isNodeDoneAt() check with determining if the current / desired
// image annotations match the pools' values.
func (l *LayeredNodeState) IsDoneAt(mcp *mcfgv1.MachineConfigPool) bool {
	return isNodeDoneAt(l, mcp) && l.isDesiredImageEqualToPool(mcp) && l.isCurrentImageEqualToPool(mcp)
}

// The original behavior of getUnavailableMachines is: getUnavailableMachines
// returns the set of nodes which are either marked unscheduleable, or have a
// MCD actively working. If the MCD is actively working (or hasn't started)
// then the node *may* go unschedulable in the future, so we don't want to
// potentially start another node update exceeding our maxUnavailable. Somewhat
// the opposite of getReadyNodes().
//
// This augments this check by determining if the desired iamge annotation is
// equal to what the pool expects.
func (l *LayeredNodeState) IsUnavailable(mcp *mcfgv1.MachineConfigPool) bool {
	return isNodeUnavailable(l) && l.isDesiredImageEqualToPool(mcp)
}

// Checks that the desired machineconfig and image annotations equal the ones
// specified by the pool.
func (l *LayeredNodeState) IsDesiredEqualToPool(mcp *mcfgv1.MachineConfigPool) bool {
	return l.isDesiredMachineConfigEqualToPool(mcp) && l.isDesiredImageEqualToPool(mcp)
}

// Compares the MachineConfig specified by the MachineConfigPool to the one
// specified by the node's desired MachineConfig annotation.
func (l *LayeredNodeState) isDesiredMachineConfigEqualToPool(mcp *mcfgv1.MachineConfigPool) bool {
	return l.node.Annotations[DesiredMachineConfigAnnotationKey] == mcp.Spec.Configuration.Name
}

// Determines if the nodes desired image is equal to the expected value from
// the MachineConfigPool.
func (l *LayeredNodeState) isDesiredImageEqualToPool(mcp *mcfgv1.MachineConfigPool) bool {
	return l.isImageAnnotationEqualToPool(DesiredImageAnnotationKey, mcp)
}

// Determines if the nodes current image is equal to the expected value from
// the MachineConfigPool.
func (l *LayeredNodeState) isCurrentImageEqualToPool(mcp *mcfgv1.MachineConfigPool) bool {
	return l.isImageAnnotationEqualToPool(CurrentImageAnnotationKey, mcp)
}

// Determines if a nodes' image annotation is equal to the expected value from
// the MachineConfigPool. If the pool is layered, this value should equal the
// OS image value, if the value is available. If the pool is not layered, then
// any image annotations should not be present on the node.
func (l *LayeredNodeState) isImageAnnotationEqualToPool(anno string, mcp *mcfgv1.MachineConfigPool) bool {
	lps := NewLayeredPoolState(mcp)

	val, ok := l.node.Annotations[anno]

	if lps.IsLayered() && lps.HasOSImage() {
		// If the pool is layered and has an OS image, check that it equals the
		// node annotations' value.
		if lps.GetOSImage() == val {
			return true
		}
		// According to https://github.com/openshift/machine-config-operator/pull/4510#issuecomment-2271461847
		// ExperimentalNewestLayeredImageEquivalentConfigAnnotationKey is not used any more and this case should never happen.
		klog.V(5).Infof("Node annotation %s has value %s different from the OS image %s", anno, val, lps.GetOSImage())
		l.ReasonOfUnavailability = fmt.Sprintf("Node has an unexpected annotation %s=%s", ExperimentalNewestLayeredImageEquivalentConfigAnnotationKey, lps.GetOSImage())
		return false
	}

	// If the pool is not layered, this annotation should not exist.
	return val == "" || !ok
}

// Sets the desired annotations from the MachineConfigPool, according to the
// following rules:
//
// 1. The desired MachineConfig annotation will always be set to match the one
// specified in the MachineConfigPool.
// 2. If the pool is layered and has the OS image available, it will set the
// desired image annotation.
// 3. If the pool is not layered and does not have the OS image available, it
// will remove the desired image annotation.
//
// Note: This will create a deep copy of the node object first to avoid
// mutating any underlying caches.
func (l *LayeredNodeState) SetDesiredStateFromPool(mcp *mcfgv1.MachineConfigPool) {
	node := l.Node()
	if node.Annotations == nil {
		node.Annotations = map[string]string{}
	}

	node.Annotations[DesiredMachineConfigAnnotationKey] = mcp.Spec.Configuration.Name

	lps := NewLayeredPoolState(mcp)

	if lps.IsLayered() && lps.HasOSImage() {
		node.Annotations[DesiredImageAnnotationKey] = lps.GetOSImage()
	} else {
		delete(node.Annotations, DesiredImageAnnotationKey)
	}

	l.node = node
}

// Returns a deep copy of the underlying node object.
func (l *LayeredNodeState) Node() *corev1.Node {
	return l.node.DeepCopy()
}

// All functions below this line were copy / pasted from
// pkg/controller/node/status.go. A future cleanup effort will integrate these
// more seamlessly into the above struct.

// isNodeDone returns true if the current == desired and the MCD has marked done.
func isNodeDone(node *corev1.Node) bool {
	if node.Annotations == nil {
		return false
	}

	if !isNodeConfigDone(node) {
		return false
	}

	if !isNodeImageDone(node) {
		return false
	}

	if !isNodeMCDState(node, MachineConfigDaemonStateDone) {
		return false
	}

	return true
}

// Determines if a node's configuration is done based upon the presence and
// equality of the current / desired config annotations.
func isNodeConfigDone(node *corev1.Node) bool {
	cconfig, ok := node.Annotations[CurrentMachineConfigAnnotationKey]
	if !ok || cconfig == "" {
		return false
	}

	dconfig, ok := node.Annotations[DesiredMachineConfigAnnotationKey]
	if !ok || dconfig == "" {
		return false
	}

	return cconfig == dconfig
}

// Determines if a node's image is done based upon the presence of the current
// / desired image annotations. Note: Unlike the above function, if both
// annotations are missing, we return "True" because we do not want to take
// these annotations into consideration. Only when one (or both) of these
// annotations is present should we take them into consideration.
// them into consideration.
func isNodeImageDone(node *corev1.Node) bool {
	desired, desiredOK := node.Annotations[DesiredImageAnnotationKey]
	current, currentOK := node.Annotations[CurrentImageAnnotationKey]

	// If neither annotation exists, we are "done" because there are no image
	// annotations to consider.
	if !desiredOK && !currentOK {
		return true
	}

	// If the desired annotation is empty, we are not "done" yet.
	if desired == "" {
		return false
	}

	// If the current annotation is empty, we are not "done" yet.
	if current == "" {
		return false
	}

	// If the current image equals the desired image and neither are empty, we are done.
	return desired == current
}

// isNodeDoneAt checks whether a node is fully updated to a targetConfig
func isNodeDoneAt(l *LayeredNodeState, pool *mcfgv1.MachineConfigPool) bool {
	node := l.node
	return isNodeDone(node) && node.Annotations[CurrentMachineConfigAnnotationKey] == pool.Spec.Configuration.Name
}

const (
	// ReasonOfUnavailabilityMCDWorkInProgress indicates MCD will fix the state and no user intervention is required.
	ReasonOfUnavailabilityMCDWorkInProgress = "Machine Config Daemon is processing the node"
	ReasonOfUnavailabilityNodeUnschedulable = "Node is marked unschedulable"
)

// isNodeUnavailable is a helper function for getUnavailableMachines
// See the docs of getUnavailableMachines for more info
func isNodeUnavailable(l *LayeredNodeState) bool {
	// Unready nodes are unavailable
	if !isNodeReady(l) {
		return true
	}
	node := l.node
	// Ready nodes are not unavailable
	if isNodeDone(node) {
		return false
	}
	// Now we know the node isn't ready - the current config must not
	// equal target.  We want to further filter down on the MCD state.
	// If a MCD is in a terminal (failing) state then we can safely retarget it.
	// to a different config.  Or to say it another way, a node is unavailable
	// if the MCD is working, or hasn't started work but the configs differ.
	if isNodeMCDState(node, MachineConfigDaemonStateDegraded) ||
		isNodeMCDState(node, MachineConfigDaemonStateUnreconcilable) {
		return false
	}
	klog.V(5).Infof("Unavailable node %s's machine-config daemon state %s is neither %s nor %s", node.Name,
		node.Annotations[MachineConfigDaemonStateAnnotationKey], MachineConfigDaemonStateDegraded, MachineConfigDaemonStateUnreconcilable)
	l.ReasonOfUnavailability = ReasonOfUnavailabilityMCDWorkInProgress
	return true
}

// isNodeMCDState checks the MCD state against the state parameter
func isNodeMCDState(node *corev1.Node, state string) bool {
	dstate, ok := node.Annotations[MachineConfigDaemonStateAnnotationKey]
	if !ok || dstate == "" {
		return false
	}

	return dstate == state
}

func checkNodeReady(l *LayeredNodeState) error {
	node := l.node
	for i := range node.Status.Conditions {
		cond := &node.Status.Conditions[i]
		// We consider the node for scheduling only when its:
		// - NodeReady condition status is ConditionTrue,
		// - NodeDiskPressure condition status is ConditionFalse,
		// - NodeNetworkUnavailable condition status is ConditionFalse.
		if cond.Type == corev1.NodeReady && cond.Status != corev1.ConditionTrue {
			l.ReasonOfUnavailability = fmt.Sprintf("node is reporting NotReady=%v with message=%q", cond.Status, cond.Message)
			return fmt.Errorf("node %s is reporting NotReady=%v", node.Name, cond.Status)
		}
		if cond.Type == corev1.NodeDiskPressure && cond.Status != corev1.ConditionFalse {
			l.ReasonOfUnavailability = fmt.Sprintf("node is reporting OutOfDisk=%v with message=%q", cond.Status, cond.Message)
			return fmt.Errorf("node %s is reporting OutOfDisk=%v", node.Name, cond.Status)
		}
		if cond.Type == corev1.NodeNetworkUnavailable && cond.Status != corev1.ConditionFalse {
			l.ReasonOfUnavailability = fmt.Sprintf("node is reporting NetworkUnavailable=%v with message=%q", cond.Status, cond.Message)
			return fmt.Errorf("node %s is reporting NetworkUnavailable=%v", node.Name, cond.Status)
		}
	}
	// Ignore nodes that are marked unschedulable
	if node.Spec.Unschedulable {
		l.ReasonOfUnavailability = ReasonOfUnavailabilityNodeUnschedulable
		return fmt.Errorf("node %s is reporting Unschedulable", node.Name)
	}
	return nil
}

func isNodeReady(l *LayeredNodeState) bool {
	return checkNodeReady(l) == nil
}

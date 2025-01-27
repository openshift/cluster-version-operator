// This is a modified https://github.com/openshift/machine-config-operator/blob/11d5151a784c7d4be5255ea41acfbf5092eda592/pkg/controller/common/layered_node_state.go
package mco

import (
	"fmt"
	"strings"
	"time"

	mcfgv1 "github.com/openshift/api/machineconfiguration/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/klog/v2"

	mcocontrollercommon "github.com/openshift/machine-config-operator/pkg/controller/common"
	mcodeamonconstants "github.com/openshift/machine-config-operator/pkg/daemon/constants"
)

type LayeredNodeState struct {
	nodeName    string
	lns         *mcocontrollercommon.LayeredNodeState
	unavailable []unavailableCondition
}

type unavailableCondition struct {
	reason             string
	message            string
	lastTransitionTime time.Time
}

// GetUnavailableSince returns the time since when the node has been unavailable.
// The earliest one is picked up if it has more than one condition to make the node unavailable.
func (l *LayeredNodeState) GetUnavailableSince() time.Time {
	var ret time.Time
	for _, c := range l.unavailable {
		if c.lastTransitionTime.IsZero() {
			continue
		}
		if ret.IsZero() || c.lastTransitionTime.Before(ret) {
			ret = c.lastTransitionTime
		}
	}
	return ret
}

// GetUnavailableReason returns the collected reasons of an unavailable node
func (l *LayeredNodeState) GetUnavailableReason() string {
	var reasons []string
	for _, c := range l.unavailable {
		reasons = append(reasons, c.reason)
	}
	return strings.Join(reasons, " | ")
}

// GetUnavailableMessage returns the collected messages of an unavailable node
func (l *LayeredNodeState) GetUnavailableMessage() string {
	var messages []string
	for _, c := range l.unavailable {
		message := c.reason
		if message == reasonOfUnavailabilityNodeNotReady {
			message = fmt.Sprintf("Node %s is not ready", l.nodeName)
		}
		if message == reasonOfUnavailabilityNodeDiskPressure {
			message = fmt.Sprintf("Node %s has disk pressure", l.nodeName)
		}
		if message == reasonOfUnavailabilityNodeNetworkUnavailable {
			message = fmt.Sprintf("Node %s has unavailable network", l.nodeName)
		}
		messages = append(messages, message)
	}
	return strings.Join(messages, " | ")
}

func NewLayeredNodeState(n *corev1.Node) *LayeredNodeState {
	ret := &LayeredNodeState{lns: mcocontrollercommon.NewLayeredNodeState(n), nodeName: n.Name}
	makeUnavailableConditions(ret, n)
	return ret
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
	return l.lns.IsUnavailable(mcp)
}

const (
	// ReasonOfUnavailabilityMCDWorkInProgress indicates MCD will fix the state and no user intervention is required.
	reasonOfUnavailabilityMCDWorkInProgress      = "Machine Config Daemon is processing the node"
	reasonOfUnavailabilityNodeUnschedulable      = "Node is marked unschedulable"
	reasonOfUnavailabilityNodeNotReady           = "Not ready"
	reasonOfUnavailabilityNodeDiskPressure       = "Disk pressure"
	reasonOfUnavailabilityNodeNetworkUnavailable = "Network unavailable"
)

func makeUnavailableConditions(l *LayeredNodeState, node *corev1.Node) {
	if len(l.unavailable) > 0 {
		return
	}

	// Unready nodes are unavailable
	if !isReady(l, node.Status.Conditions, node.Spec.Unschedulable) {
		return
	}

	// Ready nodes are not unavailable
	if isNodeDone(node) {
		return
	}

	// Now we know the node isn't ready - the current config must not
	// equal target.  We want to further filter down on the MCD state.
	// If a MCD is in a terminal (failing) state then we can safely retarget it.
	// to a different config.  Or to say it another way, a node is unavailable
	// if the MCD is working, or hasn't started work but the configs differ.
	if isNodeMCDState(node, mcodeamonconstants.MachineConfigDaemonStateDegraded) ||
		isNodeMCDState(node, mcodeamonconstants.MachineConfigDaemonStateUnreconcilable) {
		return
	}
	klog.V(5).Infof("Unavailable node %s's machine-config daemon state %s is neither %s nor %s",
		l.nodeName,
		node.Annotations[mcodeamonconstants.MachineConfigDaemonStateAnnotationKey],
		mcodeamonconstants.MachineConfigDaemonStateDegraded,
		mcodeamonconstants.MachineConfigDaemonStateUnreconcilable)
	l.unavailable = append(l.unavailable, unavailableCondition{
		reason: reasonOfUnavailabilityMCDWorkInProgress,
	})
}

func isReady(l *LayeredNodeState, conditions []corev1.NodeCondition, unschedulable bool) bool {
	if len(l.unavailable) > 0 {
		return false
	}
	ready := true
	for _, cond := range conditions {
		unavailableCond := unavailableCondition{message: cond.Message, lastTransitionTime: cond.LastTransitionTime.Time}
		// We consider the node for scheduling only when its:
		// - NodeReady condition status is ConditionTrue,
		// - NodeDiskPressure condition status is ConditionFalse,
		// - NodeNetworkUnavailable condition status is ConditionFalse.
		if cond.Type == corev1.NodeReady && cond.Status != corev1.ConditionTrue {
			unavailableCond.reason = reasonOfUnavailabilityNodeNotReady
			l.unavailable = append(l.unavailable, unavailableCond)
			ready = false
		}
		if cond.Type == corev1.NodeDiskPressure && cond.Status != corev1.ConditionFalse {
			unavailableCond.reason = reasonOfUnavailabilityNodeDiskPressure
			l.unavailable = append(l.unavailable, unavailableCond)
			ready = false
		}
		if cond.Type == corev1.NodeNetworkUnavailable && cond.Status != corev1.ConditionFalse {
			unavailableCond.reason = reasonOfUnavailabilityNodeNetworkUnavailable
			l.unavailable = append(l.unavailable, unavailableCond)
			ready = false
		}
	}
	// Ignore nodes that are marked unschedulable
	if unschedulable {
		l.unavailable = append(l.unavailable, unavailableCondition{reason: reasonOfUnavailabilityNodeUnschedulable})
		ready = false
	}
	return ready
}

// The following functions would not exist if isNodeConfigDone is public
// https://github.com/openshift/machine-config-operator/blob/cfdda14b21aabd30b9eaa7da05f36bea4fbca8b8/pkg/controller/common/layered_node_state.go#L188
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

	if !isNodeMCDState(node, mcodeamonconstants.MachineConfigDaemonStateDone) {
		return false
	}

	return true
}

// Determines if a node's configuration is done based upon the presence and
// equality of the current / desired config annotations.
func isNodeConfigDone(node *corev1.Node) bool {
	cconfig, ok := node.Annotations[mcodeamonconstants.CurrentMachineConfigAnnotationKey]
	if !ok || cconfig == "" {
		return false
	}

	dconfig, ok := node.Annotations[mcodeamonconstants.DesiredMachineConfigAnnotationKey]
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
	desired, desiredOK := node.Annotations[mcodeamonconstants.DesiredImageAnnotationKey]
	current, currentOK := node.Annotations[mcodeamonconstants.CurrentImageAnnotationKey]

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

// isNodeMCDState checks the MCD state against the state parameter
func isNodeMCDState(node *corev1.Node, state string) bool {
	dstate, ok := node.Annotations[mcodeamonconstants.MachineConfigDaemonStateAnnotationKey]
	if !ok || dstate == "" {
		return false
	}

	return dstate == state
}

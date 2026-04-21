package readiness

import (
	"context"
	"fmt"

	"k8s.io/client-go/dynamic"
)

// NodeCapacityCheck assesses node readiness and resource headroom.
type NodeCapacityCheck struct{}

func (c *NodeCapacityCheck) Name() string { return "node_capacity" }

func (c *NodeCapacityCheck) Run(ctx context.Context, dc dynamic.Interface, current, target string) (map[string]any, error) {
	result := map[string]any{}

	nodes, err := ListResources(ctx, dc, GVRNode, "")
	if err != nil {
		return nil, fmt.Errorf("failed to list nodes: %w", err)
	}

	totalNodes := len(nodes)
	readyNodes := 0
	unschedulableNodes := 0

	for _, node := range nodes {
		conditions := GetConditions(&node)
		if cond, ok := conditions["Ready"]; ok && cond.Status == ConditionTrue {
			readyNodes++
		}
		if NestedBool(node.Object, "spec", "unschedulable") {
			unschedulableNodes++
		}
	}

	result["total_nodes"] = totalNodes
	result["ready_nodes"] = readyNodes
	result["unschedulable_nodes"] = unschedulableNodes
	result["summary"] = map[string]any{
		"total":          totalNodes,
		"ready":          readyNodes,
		"not_ready":      totalNodes - readyNodes,
		"unschedulable":  unschedulableNodes,
	}

	return result, nil
}

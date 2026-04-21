package readiness

import (
	"context"
	"fmt"

	"k8s.io/client-go/dynamic"
)

// OperatorHealthCheck provides per-operator detail and MCP state.
// CVO already aggregates operator health into the ClusterVersion Upgradeable condition
// (reported in cluster_conditions check). This check adds per-operator breakdown
// and MachineConfigPool status, which CVO does not expose in conditions.
type OperatorHealthCheck struct{}

func (c *OperatorHealthCheck) Name() string { return "operator_health" }

func (c *OperatorHealthCheck) Run(ctx context.Context, dc dynamic.Interface, current, target string) (map[string]any, error) {
	result := map[string]any{}
	var sectionErrors []map[string]any

	// Per-operator breakdown — CVO aggregates this but doesn't expose per-CO detail
	operators, err := ListResources(ctx, dc, GVRClusterOperator, "")
	if err != nil {
		return nil, fmt.Errorf("failed to list ClusterOperators: %w", err)
	}

	notUpgradeable := make([]map[string]any, 0)
	degraded := make([]map[string]any, 0)
	notAvailable := make([]map[string]any, 0)

	for _, co := range operators {
		conditions := GetConditions(&co)
		name := co.GetName()

		if cond, ok := conditions[ConditionUpgradeable]; ok && cond.Status == ConditionFalse {
			notUpgradeable = append(notUpgradeable, map[string]any{
				"name":    name,
				"reason":  cond.Reason,
				"message": cond.Message,
			})
		}
		if cond, ok := conditions[ConditionDegraded]; ok && cond.Status == ConditionTrue {
			degraded = append(degraded, map[string]any{
				"name":    name,
				"reason":  cond.Reason,
				"message": cond.Message,
			})
		}
		if cond, ok := conditions[ConditionAvailable]; ok && cond.Status == ConditionFalse {
			notAvailable = append(notAvailable, map[string]any{
				"name":    name,
				"reason":  cond.Reason,
				"message": cond.Message,
			})
		}
	}

	result["not_upgradeable"] = notUpgradeable
	result["degraded"] = degraded
	result["not_available"] = notAvailable

	// MachineConfigPool status — CVO does NOT track this
	mcps, err := ListResources(ctx, dc, GVRMachineConfigPool, "")
	if err != nil {
		SectionError(&sectionErrors, "machine_config_pools", err)
	} else {
		mcpResults := make([]map[string]any, 0, len(mcps))
		pausedMCPs := 0
		degradedMCPs := 0
		updatingMCPs := 0

		for _, mcp := range mcps {
			paused := NestedBool(mcp.Object, "spec", "paused")
			machineCount := NestedInt64(mcp.Object, "status", "machineCount")
			readyCount := NestedInt64(mcp.Object, "status", "readyMachineCount")
			updatedCount := NestedInt64(mcp.Object, "status", "updatedMachineCount")

			conditions := GetConditions(&mcp)
			isDegraded := false
			isUpdating := false
			if cond, ok := conditions[ConditionDegraded]; ok && cond.Status == ConditionTrue {
				isDegraded = true
				degradedMCPs++
			}
			if cond, ok := conditions[ConditionUpdating]; ok && cond.Status == ConditionTrue {
				isUpdating = true
				updatingMCPs++
			}
			if paused {
				pausedMCPs++
			}

			mcpResults = append(mcpResults, map[string]any{
				"name":          mcp.GetName(),
				"paused":        paused,
				"machine_count": machineCount,
				"ready_count":   readyCount,
				"updated_count": updatedCount,
				"degraded":      isDegraded,
				"updating":      isUpdating,
			})
		}
		result["machine_config_pools"] = mcpResults
		result["mcp_summary"] = map[string]any{
			"paused":   pausedMCPs,
			"degraded": degradedMCPs,
			"updating": updatingMCPs,
		}
	}

	result["summary"] = map[string]any{
		"total_operators":       len(operators),
		"not_upgradeable_count": len(notUpgradeable),
		"degraded_count":        len(degraded),
		"not_available_count":   len(notAvailable),
		"note":                  "CVO's aggregated Upgradeable condition is in the cluster_conditions check",
	}

	if len(sectionErrors) > 0 {
		result["errors"] = sectionErrors
	}

	return result, nil
}

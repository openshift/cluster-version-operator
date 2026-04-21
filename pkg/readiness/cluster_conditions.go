package readiness

import (
	"context"
	"fmt"

	"k8s.io/client-go/dynamic"
)

// ClusterConditionsCheck reads existing CVO-computed conditions from ClusterVersion status.
// This does NOT re-evaluate anything — it reports what CVO has already determined,
// including Upgradeable sub-conditions, RetrievedUpdates, and precondition state.
type ClusterConditionsCheck struct{}

func (c *ClusterConditionsCheck) Name() string { return "cluster_conditions" }

func (c *ClusterConditionsCheck) Run(ctx context.Context, dc dynamic.Interface, current, target string) (map[string]any, error) {
	result := map[string]any{}

	cv, err := GetResource(ctx, dc, GVRClusterVersion, "version")
	if err != nil {
		return nil, fmt.Errorf("failed to get ClusterVersion: %w", err)
	}

	// Read all conditions CVO has already set
	conditions := GetConditions(cv)
	condMap := map[string]any{}
	for k, v := range conditions {
		condMap[k] = v
	}
	result["conditions"] = condMap

	// Extract key signals for the agent
	upgradeable := conditions[ConditionUpgradeable]
	result["upgradeable"] = map[string]any{
		"status":  upgradeable.Status,
		"reason":  upgradeable.Reason,
		"message": upgradeable.Message,
	}

	progressing := conditions[ConditionProgressing]
	result["update_in_progress"] = progressing.Status == ConditionTrue

	// Read update history for context
	history := NestedSlice(cv.Object, "status", "history")
	historyEntries := make([]map[string]any, 0)
	for i, h := range history {
		if i >= 5 {
			break
		}
		entry, ok := h.(map[string]interface{})
		if !ok {
			continue
		}
		historyEntries = append(historyEntries, map[string]any{
			"version":       NestedString(entry, "version"),
			"state":         NestedString(entry, "state"),
			"startedTime":   NestedString(entry, "startedTime"),
			"completionTime": NestedString(entry, "completionTime"),
		})
	}
	result["recent_history"] = historyEntries

	// Channel and upstream
	result["channel"] = NestedString(cv.Object, "spec", "channel")
	result["cluster_id"] = NestedString(cv.Object, "spec", "clusterID")

	// Summary for quick agent parsing
	result["summary"] = map[string]any{
		"upgradeable":        upgradeable.Status == ConditionTrue,
		"update_in_progress": progressing.Status == ConditionTrue,
		"current_version":    current,
	}

	return result, nil
}

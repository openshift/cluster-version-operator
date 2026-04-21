package readiness

import (
	"context"
	"fmt"

	"k8s.io/client-go/dynamic"
)

// PDBDrainCheck assesses PodDisruptionBudgets that could block node drains.
type PDBDrainCheck struct{}

func (c *PDBDrainCheck) Name() string { return "pdb_drain" }

func (c *PDBDrainCheck) Run(ctx context.Context, dc dynamic.Interface, current, target string) (map[string]any, error) {
	result := map[string]any{}

	pdbs, err := ListResources(ctx, dc, GVRPDB, "")
	if err != nil {
		return nil, fmt.Errorf("failed to list PodDisruptionBudgets: %w", err)
	}

	issues := make([]map[string]any, 0)
	for _, pdb := range pdbs {
		// Check for zero-disruption PDBs
		maxUnavailable := NestedString(pdb.Object, "spec", "maxUnavailable")
		minAvailable := NestedString(pdb.Object, "spec", "minAvailable")

		currentHealthy := NestedInt64(pdb.Object, "status", "currentHealthy")
		desiredHealthy := NestedInt64(pdb.Object, "status", "desiredHealthy")
		disruptionsAllowed := NestedInt64(pdb.Object, "status", "disruptionsAllowed")

		if disruptionsAllowed == 0 && currentHealthy > 0 {
			issues = append(issues, map[string]any{
				"name":                pdb.GetName(),
				"namespace":           pdb.GetNamespace(),
				"max_unavailable":     maxUnavailable,
				"min_available":       minAvailable,
				"current_healthy":     currentHealthy,
				"desired_healthy":     desiredHealthy,
				"disruptions_allowed": disruptionsAllowed,
			})
		}
	}

	result["total_pdbs"] = len(pdbs)
	result["blocking_pdbs"] = issues
	result["summary"] = map[string]any{
		"total":    len(pdbs),
		"blocking": len(issues),
	}

	return result, nil
}

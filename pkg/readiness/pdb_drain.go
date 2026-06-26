package readiness

import (
	"context"
	"fmt"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
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
		maxUnavailable, _, _ := unstructured.NestedFieldNoCopy(pdb.Object, "spec", "maxUnavailable")
		minAvailable, _, _ := unstructured.NestedFieldNoCopy(pdb.Object, "spec", "minAvailable")

		currentHealthy := NestedInt64(pdb.Object, "status", "currentHealthy")
		desiredHealthy := NestedInt64(pdb.Object, "status", "desiredHealthy")
		disruptionsAllowed := NestedInt64(pdb.Object, "status", "disruptionsAllowed")

		if disruptionsAllowed == 0 && currentHealthy > 0 {
			issue := map[string]any{
				"name":                pdb.GetName(),
				"namespace":           pdb.GetNamespace(),
				"current_healthy":     currentHealthy,
				"desired_healthy":     desiredHealthy,
				"disruptions_allowed": disruptionsAllowed,
			}
			// Emit the disruption fields only when set so the agent does not
			// see a literal "<nil>" string for an absent field.
			if maxUnavailable != nil {
				issue["max_unavailable"] = maxUnavailable
			}
			if minAvailable != nil {
				issue["min_available"] = minAvailable
			}
			issues = append(issues, issue)
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

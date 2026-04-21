package readiness

import (
	"context"
	"fmt"
	"strings"

	"k8s.io/client-go/dynamic"
)

// APIDeprecationsCheck scans for deprecated or removed API usage.
type APIDeprecationsCheck struct{}

func (c *APIDeprecationsCheck) Name() string { return "api_deprecations" }

func (c *APIDeprecationsCheck) Run(ctx context.Context, dc dynamic.Interface, current, target string) (map[string]any, error) {
	result := map[string]any{}

	// Fetch APIRequestCount resources
	arcs, err := ListResources(ctx, dc, GVRAPIRequestCount, "")
	if err != nil {
		// APIRequestCount may not be available on all clusters
		if strings.Contains(err.Error(), "not found") {
			result["warning"] = "APIRequestCount resource not available"
			result["blocker_apis"] = []any{}
			result["warning_apis"] = []any{}
			result["summary"] = map[string]any{"blockers": 0, "warnings": 0}
			return result, nil
		}
		return nil, fmt.Errorf("failed to list APIRequestCounts: %w", err)
	}

	blockers := make([]map[string]any, 0)
	warnings := make([]map[string]any, 0)

	for _, arc := range arcs {
		conditions := GetConditions(&arc)

		// Check RemovedInRelease annotation
		removedIn := NestedString(arc.Object, "status", "removedInRelease")
		if removedIn != "" && CompareVersions(removedIn, target) <= 0 {
			requestCount := NestedInt64(arc.Object, "status", "requestCount")
			if requestCount > 0 {
				blockers = append(blockers, map[string]any{
					"resource":           arc.GetName(),
					"removed_in_release": removedIn,
					"request_count":      requestCount,
				})
			}
		}

		// Check for deprecation condition
		if dep, ok := conditions["Deprecated"]; ok && dep.Status == ConditionTrue {
			requestCount := NestedInt64(arc.Object, "status", "requestCount")
			if requestCount > 0 {
				warnings = append(warnings, map[string]any{
					"resource":      arc.GetName(),
					"request_count": requestCount,
					"message":       dep.Message,
				})
			}
		}
	}

	result["blocker_apis"] = blockers
	result["warning_apis"] = warnings
	result["summary"] = map[string]any{
		"blockers": len(blockers),
		"warnings": len(warnings),
		"total":    len(arcs),
	}

	return result, nil
}

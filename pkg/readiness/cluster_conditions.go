package readiness

import (
	"context"
	"fmt"

	"github.com/blang/semver/v4"

	"k8s.io/client-go/dynamic"

	configv1 "github.com/openshift/api/config/v1"

	"github.com/openshift/cluster-version-operator/pkg/internal"
)

// ClusterConditionsCheck reads existing CVO-computed conditions from ClusterVersion status.
// This does NOT re-evaluate anything — it reports what CVO has already determined,
// including Upgradeable, Progressing, and Failing conditions.
type ClusterConditionsCheck struct {
}

func (c *ClusterConditionsCheck) Name() string { return "cluster_conditions" }

func (c *ClusterConditionsCheck) Run(ctx context.Context, dc dynamic.Interface, current, target string) (map[string]any, error) {
	result := map[string]any{}

	cv, err := GetResource(ctx, dc, GVRClusterVersion, "version")
	if err != nil {
		return nil, fmt.Errorf("failed to get ClusterVersion: %w", err)
	}

	conditions := GetConditions(cv)
	condMap := map[string]any{}

	for _, key := range []configv1.ClusterStatusConditionType{
		configv1.OperatorAvailable,
		configv1.OperatorProgressing,
		internal.ClusterStatusFailing,
	} {
		if v, ok := conditions[string(key)]; ok {
			v.Message = truncateMessage(v.Message)
			condMap[string(key)] = v
		}
	}

	// slim version of the pkg/payload/precondition/clusterversion/upgradeable.go logic
	currentVersion, err := semver.Parse(current)
	if err != nil {
		return nil, fmt.Errorf("current version %q is not a Semantic Version: %w", current, err)
	}
	targetVersion, err := semver.Parse(target)
	if err != nil {
		return nil, fmt.Errorf("target version %q is not a Semantic Version: %w", target, err)
	}
	patchOnly := targetVersion.Major == currentVersion.Major && targetVersion.Minor == currentVersion.Minor
	if targetVersion.GTE(currentVersion) && !patchOnly {
		if v, ok := conditions[string(configv1.OperatorUpgradeable)]; ok {
			v.Message = truncateMessage(v.Message)
			condMap[string(configv1.OperatorUpgradeable)] = v
		}
	}

	result["conditions"] = condMap

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
			"version":        NestedString(entry, "version"),
			"state":          NestedString(entry, "state"),
			"startedTime":    NestedString(entry, "startedTime"),
			"completionTime": NestedString(entry, "completionTime"),
		})
	}
	result["recent_history"] = historyEntries

	// Channel and cluster identity
	result["channel"] = NestedString(cv.Object, "spec", "channel")
	result["cluster_id"] = NestedString(cv.Object, "spec", "clusterID")

	return result, nil
}

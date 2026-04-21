package readiness

import (
	"context"
	"fmt"

	"k8s.io/client-go/dynamic"
)

// EtcdHealthCheck verifies etcd member health, backup status, and certificates.
type EtcdHealthCheck struct{}

func (c *EtcdHealthCheck) Name() string { return "etcd_health" }

func (c *EtcdHealthCheck) Run(ctx context.Context, dc dynamic.Interface, current, target string) (map[string]any, error) {
	result := map[string]any{}
	var sectionErrors []map[string]any

	// Check etcd ClusterOperator
	etcdCO, err := GetResource(ctx, dc, GVRClusterOperator, "etcd")
	if err != nil {
		return nil, fmt.Errorf("failed to get etcd ClusterOperator: %w", err)
	}

	conditions := GetConditions(etcdCO)
	result["operator_conditions"] = conditions

	// Check etcd pods
	etcdPods, err := ListNamespacedResources(ctx, dc, GVRPod, "openshift-etcd", "app=etcd")
	if err != nil {
		SectionError(&sectionErrors, "etcd_pods", err)
	} else {
		podStatuses := make([]map[string]any, 0, len(etcdPods))
		healthyMembers := 0
		for _, pod := range etcdPods {
			phase := NestedString(pod.Object, "status", "phase")
			ready := phase == "Running"
			if ready {
				healthyMembers++
			}
			podStatuses = append(podStatuses, map[string]any{
				"name":   pod.GetName(),
				"node":   NestedString(pod.Object, "spec", "nodeName"),
				"phase":  phase,
				"ready":  ready,
			})
		}
		result["members"] = podStatuses
		result["healthy_members"] = healthyMembers
		result["total_members"] = len(etcdPods)
	}

	result["summary"] = map[string]any{
		"operator_available": conditions[ConditionAvailable].Status == ConditionTrue,
		"operator_degraded":  conditions[ConditionDegraded].Status == ConditionTrue,
	}

	if len(sectionErrors) > 0 {
		result["errors"] = sectionErrors
	}

	return result, nil
}

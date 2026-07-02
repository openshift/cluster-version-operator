package readiness

import (
	"context"
	"encoding/json"
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	dynamicfake "k8s.io/client-go/dynamic/fake"
)

func newFakeDynamicClient(objects ...runtime.Object) *dynamicfake.FakeDynamicClient {
	scheme := runtime.NewScheme()
	gvrs := map[schema.GroupVersionResource]string{
		GVRClusterVersion:    "ClusterVersionList",
		GVRClusterOperator:   "ClusterOperatorList",
		GVRMachineConfigPool: "MachineConfigPoolList",
		GVRNode:              "NodeList",
		GVRPod:               "PodList",
		GVRPDB:               "PodDisruptionBudgetList",
		GVRSubscription:      "SubscriptionList",
		GVRCSV:               "ClusterServiceVersionList",
		GVRInstallPlan:       "InstallPlanList",
		GVRPackageManifest:   "PackageManifestList",
		GVRAPIRequestCount:   "APIRequestCountList",
		GVRNetwork:           "NetworkList",
		GVRProxy:             "ProxyList",
		GVRAPIServer:         "APIServerList",
	}
	for gvr, listKind := range gvrs {
		gvk := schema.GroupVersionKind{Group: gvr.Group, Version: gvr.Version, Kind: listKind}
		scheme.AddKnownTypeWithName(gvk, &unstructured.UnstructuredList{})
	}
	return dynamicfake.NewSimpleDynamicClientWithCustomListKinds(scheme, gvrs, objects...)
}

func TestNodeCapacityCheck(t *testing.T) {
	nodes := []runtime.Object{
		&unstructured.Unstructured{Object: map[string]interface{}{
			"apiVersion": "v1", "kind": "Node",
			"metadata": map[string]interface{}{"name": "master-0"},
			"status": map[string]interface{}{
				"conditions": []interface{}{
					map[string]interface{}{"type": "Ready", "status": "True"},
				},
			},
		}},
		&unstructured.Unstructured{Object: map[string]interface{}{
			"apiVersion": "v1", "kind": "Node",
			"metadata": map[string]interface{}{"name": "worker-0"},
			"status": map[string]interface{}{
				"conditions": []interface{}{
					map[string]interface{}{"type": "Ready", "status": "True"},
				},
			},
		}},
		&unstructured.Unstructured{Object: map[string]interface{}{
			"apiVersion": "v1", "kind": "Node",
			"metadata": map[string]interface{}{"name": "worker-1"},
			"spec":     map[string]interface{}{"unschedulable": true},
			"status": map[string]interface{}{
				"conditions": []interface{}{
					map[string]interface{}{"type": "Ready", "status": "False"},
				},
			},
		}},
	}

	client := newFakeDynamicClient(nodes...)
	check := &NodeCapacityCheck{}

	result, err := check.Run(context.Background(), client, "4.21.5", "4.21.8")
	if err != nil {
		t.Fatal(err)
	}

	if result["total_nodes"] != 3 {
		t.Errorf("total_nodes = %v, want 3", result["total_nodes"])
	}
	if result["ready_nodes"] != 2 {
		t.Errorf("ready_nodes = %v, want 2", result["ready_nodes"])
	}
	if result["unschedulable_nodes"] != 1 {
		t.Errorf("unschedulable_nodes = %v, want 1", result["unschedulable_nodes"])
	}

	summary, ok := result["summary"].(map[string]any)
	if !ok {
		t.Fatal("summary not a map")
	}
	if summary["not_ready"] != 1 {
		t.Errorf("summary.not_ready = %v, want 1", summary["not_ready"])
	}
}

func TestPDBDrainCheck(t *testing.T) {
	pdbs := []runtime.Object{
		&unstructured.Unstructured{Object: map[string]interface{}{
			"apiVersion": "policy/v1", "kind": "PodDisruptionBudget",
			"metadata": map[string]interface{}{"name": "safe-pdb", "namespace": "default"},
			"spec":     map[string]interface{}{"maxUnavailable": "1"},
			"status": map[string]interface{}{
				"currentHealthy":     int64(3),
				"desiredHealthy":     int64(2),
				"disruptionsAllowed": int64(1),
			},
		}},
		&unstructured.Unstructured{Object: map[string]interface{}{
			"apiVersion": "policy/v1", "kind": "PodDisruptionBudget",
			"metadata": map[string]interface{}{"name": "blocking-pdb", "namespace": "critical"},
			"spec":     map[string]interface{}{"maxUnavailable": "0"},
			"status": map[string]interface{}{
				"currentHealthy":     int64(2),
				"desiredHealthy":     int64(2),
				"disruptionsAllowed": int64(0),
			},
		}},
	}

	client := newFakeDynamicClient(pdbs...)
	check := &PDBDrainCheck{}

	result, err := check.Run(context.Background(), client, "4.21.5", "4.21.8")
	if err != nil {
		t.Fatal(err)
	}

	if result["total_pdbs"] != 2 {
		t.Errorf("total_pdbs = %v, want 2", result["total_pdbs"])
	}

	blocking, ok := result["blocking_pdbs"].([]map[string]any)
	if !ok {
		t.Fatal("blocking_pdbs not a slice")
	}
	if len(blocking) != 1 {
		t.Fatalf("blocking_pdbs len = %d, want 1", len(blocking))
	}
	if blocking[0]["name"] != "blocking-pdb" {
		t.Errorf("blocking pdb name = %v, want blocking-pdb", blocking[0]["name"])
	}
	if blocking[0]["namespace"] != "critical" {
		t.Errorf("blocking pdb namespace = %v, want critical", blocking[0]["namespace"])
	}
}

func TestEtcdHealthCheck(t *testing.T) {
	objects := []runtime.Object{
		// etcd ClusterOperator
		&unstructured.Unstructured{Object: map[string]interface{}{
			"apiVersion": "config.openshift.io/v1", "kind": "ClusterOperator",
			"metadata": map[string]interface{}{"name": "etcd"},
			"status": map[string]interface{}{
				"conditions": []interface{}{
					map[string]interface{}{"type": "Available", "status": "True", "reason": "AsExpected"},
					map[string]interface{}{"type": "Degraded", "status": "False", "reason": "AsExpected"},
					map[string]interface{}{"type": "Upgradeable", "status": "True", "reason": "AsExpected"},
				},
			},
		}},
		// etcd pods
		&unstructured.Unstructured{Object: map[string]interface{}{
			"apiVersion": "v1", "kind": "Pod",
			"metadata": map[string]interface{}{"name": "etcd-master-0", "namespace": "openshift-etcd",
				"labels": map[string]interface{}{"app": "etcd"}},
			"spec": map[string]interface{}{"nodeName": "master-0"},
			"status": map[string]interface{}{"phase": "Running", "conditions": []interface{}{
				map[string]interface{}{"type": "Ready", "status": "True"},
			}},
		}},
		&unstructured.Unstructured{Object: map[string]interface{}{
			"apiVersion": "v1", "kind": "Pod",
			"metadata": map[string]interface{}{"name": "etcd-master-1", "namespace": "openshift-etcd",
				"labels": map[string]interface{}{"app": "etcd"}},
			"spec": map[string]interface{}{"nodeName": "master-1"},
			"status": map[string]interface{}{"phase": "Running", "conditions": []interface{}{
				map[string]interface{}{"type": "Ready", "status": "True"},
			}},
		}},
		&unstructured.Unstructured{Object: map[string]interface{}{
			"apiVersion": "v1", "kind": "Pod",
			"metadata": map[string]interface{}{"name": "etcd-master-2", "namespace": "openshift-etcd",
				"labels": map[string]interface{}{"app": "etcd"}},
			"spec": map[string]interface{}{"nodeName": "master-2"},
			"status": map[string]interface{}{"phase": "Running", "conditions": []interface{}{
				map[string]interface{}{"type": "Ready", "status": "False"},
			}},
		}},
	}

	client := newFakeDynamicClient(objects...)
	check := &EtcdHealthCheck{}

	result, err := check.Run(context.Background(), client, "4.21.5", "4.21.8")
	if err != nil {
		t.Fatal(err)
	}

	if result["total_members"] != 3 {
		t.Errorf("total_members = %v, want 3", result["total_members"])
	}
	if result["healthy_members"] != 2 {
		t.Errorf("healthy_members = %v, want 2", result["healthy_members"])
	}

	summary, ok := result["summary"].(map[string]any)
	if !ok {
		t.Fatal("summary not a map")
	}
	if summary["operator_available"] != true {
		t.Errorf("operator_available = %v, want true", summary["operator_available"])
	}
	if summary["operator_degraded"] != false {
		t.Errorf("operator_degraded = %v, want false", summary["operator_degraded"])
	}
}

func TestOperatorHealthCheck(t *testing.T) {
	objects := []runtime.Object{
		// Healthy operator
		&unstructured.Unstructured{Object: map[string]interface{}{
			"apiVersion": "config.openshift.io/v1", "kind": "ClusterOperator",
			"metadata": map[string]interface{}{"name": "dns"},
			"status": map[string]interface{}{
				"conditions": []interface{}{
					map[string]interface{}{"type": "Available", "status": "True", "reason": "AsExpected"},
					map[string]interface{}{"type": "Degraded", "status": "False", "reason": "AsExpected"},
					map[string]interface{}{"type": "Upgradeable", "status": "True", "reason": "AsExpected"},
				},
			},
		}},
		// Degraded operator
		&unstructured.Unstructured{Object: map[string]interface{}{
			"apiVersion": "config.openshift.io/v1", "kind": "ClusterOperator",
			"metadata": map[string]interface{}{"name": "authentication"},
			"status": map[string]interface{}{
				"conditions": []interface{}{
					map[string]interface{}{"type": "Available", "status": "False", "reason": "OAuthDown", "message": "oauth pods crashlooping"},
					map[string]interface{}{"type": "Degraded", "status": "True", "reason": "OAuthDown", "message": "oauth pods crashlooping"},
					map[string]interface{}{"type": "Upgradeable", "status": "False", "reason": "OAuthDown", "message": "must fix before upgrade"},
				},
			},
		}},
		// MachineConfigPool: healthy master
		&unstructured.Unstructured{Object: map[string]interface{}{
			"apiVersion": "machineconfiguration.openshift.io/v1", "kind": "MachineConfigPool",
			"metadata": map[string]interface{}{"name": "master"},
			"spec":     map[string]interface{}{"paused": false},
			"status": map[string]interface{}{
				"machineCount":        int64(3),
				"readyMachineCount":   int64(3),
				"updatedMachineCount": int64(3),
				"conditions": []interface{}{
					map[string]interface{}{"type": "Degraded", "status": "False"},
					map[string]interface{}{"type": "Updating", "status": "False"},
				},
			},
		}},
		// MachineConfigPool: paused and degraded worker
		&unstructured.Unstructured{Object: map[string]interface{}{
			"apiVersion": "machineconfiguration.openshift.io/v1", "kind": "MachineConfigPool",
			"metadata": map[string]interface{}{"name": "worker"},
			"spec":     map[string]interface{}{"paused": true},
			"status": map[string]interface{}{
				"machineCount":        int64(5),
				"readyMachineCount":   int64(3),
				"updatedMachineCount": int64(3),
				"conditions": []interface{}{
					map[string]interface{}{"type": "Degraded", "status": "True", "reason": "RenderFailed"},
					map[string]interface{}{"type": "Updating", "status": "True", "reason": "InProgress"},
				},
			},
		}},
	}

	client := newFakeDynamicClient(objects...)
	check := &OperatorHealthCheck{}

	result, err := check.Run(context.Background(), client, "4.21.5", "4.21.8")
	if err != nil {
		t.Fatal(err)
	}

	// Operator conditions
	notUpgradeable, ok := result["not_upgradeable"].([]map[string]any)
	if !ok {
		t.Fatal("not_upgradeable not a slice")
	}
	if len(notUpgradeable) != 1 {
		t.Fatalf("not_upgradeable len = %d, want 1", len(notUpgradeable))
	}
	if notUpgradeable[0]["name"] != "authentication" {
		t.Errorf("not_upgradeable[0].name = %v, want authentication", notUpgradeable[0]["name"])
	}

	degraded, ok := result["degraded"].([]map[string]any)
	if !ok {
		t.Fatal("degraded not a slice")
	}
	if len(degraded) != 1 {
		t.Fatalf("degraded len = %d, want 1", len(degraded))
	}
	if degraded[0]["name"] != "authentication" {
		t.Errorf("degraded[0].name = %v, want authentication", degraded[0]["name"])
	}

	notAvailable, ok := result["not_available"].([]map[string]any)
	if !ok {
		t.Fatal("not_available not a slice")
	}
	if len(notAvailable) != 1 {
		t.Fatalf("not_available len = %d, want 1", len(notAvailable))
	}

	// MCP results
	mcps, ok := result["machine_config_pools"].([]map[string]any)
	if !ok {
		t.Fatal("machine_config_pools not a slice")
	}
	if len(mcps) != 2 {
		t.Fatalf("machine_config_pools len = %d, want 2", len(mcps))
	}

	mcpSummary, ok := result["mcp_summary"].(map[string]any)
	if !ok {
		t.Fatal("mcp_summary not a map")
	}
	if mcpSummary["paused"] != 1 {
		t.Errorf("mcp_summary.paused = %v, want 1", mcpSummary["paused"])
	}
	if mcpSummary["degraded"] != 1 {
		t.Errorf("mcp_summary.degraded = %v, want 1", mcpSummary["degraded"])
	}
	if mcpSummary["updating"] != 1 {
		t.Errorf("mcp_summary.updating = %v, want 1", mcpSummary["updating"])
	}

	// Summary
	summary, ok := result["summary"].(map[string]any)
	if !ok {
		t.Fatal("summary not a map")
	}
	if summary["total_operators"] != 2 {
		t.Errorf("total_operators = %v, want 2", summary["total_operators"])
	}
	if summary["not_upgradeable_count"] != 1 {
		t.Errorf("not_upgradeable_count = %v, want 1", summary["not_upgradeable_count"])
	}
	if summary["degraded_count"] != 1 {
		t.Errorf("degraded_count = %v, want 1", summary["degraded_count"])
	}
	if summary["not_available_count"] != 1 {
		t.Errorf("not_available_count = %v, want 1", summary["not_available_count"])
	}
}

func TestOperatorHealthCheck_AllHealthy(t *testing.T) {
	objects := []runtime.Object{
		&unstructured.Unstructured{Object: map[string]interface{}{
			"apiVersion": "config.openshift.io/v1", "kind": "ClusterOperator",
			"metadata": map[string]interface{}{"name": "dns"},
			"status": map[string]interface{}{
				"conditions": []interface{}{
					map[string]interface{}{"type": "Available", "status": "True"},
					map[string]interface{}{"type": "Degraded", "status": "False"},
					map[string]interface{}{"type": "Upgradeable", "status": "True"},
				},
			},
		}},
	}

	client := newFakeDynamicClient(objects...)
	check := &OperatorHealthCheck{}

	result, err := check.Run(context.Background(), client, "4.21.5", "4.21.8")
	if err != nil {
		t.Fatal(err)
	}

	if len(result["not_upgradeable"].([]map[string]any)) != 0 {
		t.Error("expected no not_upgradeable operators")
	}
	if len(result["degraded"].([]map[string]any)) != 0 {
		t.Error("expected no degraded operators")
	}
	if len(result["not_available"].([]map[string]any)) != 0 {
		t.Error("expected no not_available operators")
	}
}

func TestClusterConditionsCheck(t *testing.T) {
	cv := &unstructured.Unstructured{Object: map[string]interface{}{
		"apiVersion": "config.openshift.io/v1", "kind": "ClusterVersion",
		"metadata": map[string]interface{}{"name": "version"},
		"spec": map[string]interface{}{
			"channel":   "stable-4.21",
			"clusterID": "test-cluster-id-123",
		},
		"status": map[string]interface{}{
			"conditions": []interface{}{
				map[string]interface{}{"type": "Available", "status": "True", "reason": "AsExpected"},
				map[string]interface{}{"type": "Progressing", "status": "False", "reason": "AsExpected"},
				map[string]interface{}{"type": "Upgradeable", "status": "True", "reason": "AsExpected", "message": ""},
				map[string]interface{}{"type": "Failing", "status": "False", "reason": "AsExpected"},
			},
			"history": []interface{}{
				map[string]interface{}{
					"version":        "4.21.5",
					"state":          "Completed",
					"startedTime":    "2026-04-10T10:00:00Z",
					"completionTime": "2026-04-10T11:00:00Z",
				},
				map[string]interface{}{
					"version":        "4.21.4",
					"state":          "Completed",
					"startedTime":    "2026-04-01T10:00:00Z",
					"completionTime": "2026-04-01T11:00:00Z",
				},
			},
		},
	}}

	client := newFakeDynamicClient(cv)
	check := &ClusterConditionsCheck{}

	result, err := check.Run(context.Background(), client, "4.21.5", "4.21.8")
	if err != nil {
		t.Fatal(err)
	}

	if result["channel"] != "stable-4.21" {
		t.Errorf("channel = %v, want stable-4.21", result["channel"])
	}
	if result["cluster_id"] != "test-cluster-id-123" {
		t.Errorf("cluster_id = %v, want test-cluster-id-123", result["cluster_id"])
	}
	if result["update_in_progress"] != false {
		t.Errorf("update_in_progress = %v, want false", result["update_in_progress"])
	}

	upgradeable, ok := result["upgradeable"].(map[string]any)
	if !ok {
		t.Fatal("upgradeable not a map")
	}
	if upgradeable["status"] != "True" {
		t.Errorf("upgradeable.status = %v, want True", upgradeable["status"])
	}

	history, ok := result["recent_history"].([]map[string]any)
	if !ok {
		t.Fatal("recent_history not a slice")
	}
	if len(history) != 2 {
		t.Fatalf("recent_history len = %d, want 2", len(history))
	}
	if history[0]["version"] != "4.21.5" {
		t.Errorf("history[0].version = %v, want 4.21.5", history[0]["version"])
	}

	summary, ok := result["summary"].(map[string]any)
	if !ok {
		t.Fatal("summary not a map")
	}
	if summary["upgradeable"] != true {
		t.Errorf("summary.upgradeable = %v, want true", summary["upgradeable"])
	}
	if summary["update_in_progress"] != false {
		t.Errorf("summary.update_in_progress = %v, want false", summary["update_in_progress"])
	}
}

func TestClusterConditionsCheck_ProgressingTrue(t *testing.T) {
	cv := &unstructured.Unstructured{Object: map[string]interface{}{
		"apiVersion": "config.openshift.io/v1", "kind": "ClusterVersion",
		"metadata": map[string]interface{}{"name": "version"},
		"spec":     map[string]interface{}{"channel": "stable-4.21", "clusterID": "abc"},
		"status": map[string]interface{}{
			"conditions": []interface{}{
				map[string]interface{}{"type": "Progressing", "status": "True", "reason": "Updating"},
				map[string]interface{}{"type": "Upgradeable", "status": "False", "reason": "Updating", "message": "update in progress"},
			},
			"history": []interface{}{},
		},
	}}

	client := newFakeDynamicClient(cv)
	check := &ClusterConditionsCheck{}

	result, err := check.Run(context.Background(), client, "4.21.5", "4.21.8")
	if err != nil {
		t.Fatal(err)
	}

	if result["update_in_progress"] != true {
		t.Errorf("update_in_progress = %v, want true", result["update_in_progress"])
	}

	summary := result["summary"].(map[string]any)
	if summary["upgradeable"] != false {
		t.Errorf("summary.upgradeable = %v, want false", summary["upgradeable"])
	}
}

func TestAPIDeprecationsCheck(t *testing.T) {
	objects := []runtime.Object{
		// API removed in target version with active usage — blocker
		&unstructured.Unstructured{Object: map[string]interface{}{
			"apiVersion": "apiserver.openshift.io/v1", "kind": "APIRequestCount",
			"metadata": map[string]interface{}{"name": "flowschemas.v1beta3.flowcontrol.apiserver.k8s.io"},
			"status": map[string]interface{}{
				"removedInRelease": "4.21.8",
				"requestCount":     int64(150),
				"conditions": []interface{}{
					map[string]interface{}{"type": "Deprecated", "status": "True", "message": "deprecated since 4.20"},
				},
			},
		}},
		// Deprecated but not removed — warning
		&unstructured.Unstructured{Object: map[string]interface{}{
			"apiVersion": "apiserver.openshift.io/v1", "kind": "APIRequestCount",
			"metadata": map[string]interface{}{"name": "cronjobs.v1beta1.batch"},
			"status": map[string]interface{}{
				"removedInRelease": "4.25.0",
				"requestCount":     int64(42),
				"conditions": []interface{}{
					map[string]interface{}{"type": "Deprecated", "status": "True", "message": "use v1 instead"},
				},
			},
		}},
		// No usage — should not appear
		&unstructured.Unstructured{Object: map[string]interface{}{
			"apiVersion": "apiserver.openshift.io/v1", "kind": "APIRequestCount",
			"metadata": map[string]interface{}{"name": "unused.v1beta1.example"},
			"status": map[string]interface{}{
				"removedInRelease": "4.21.0",
				"requestCount":     int64(0),
			},
		}},
	}

	client := newFakeDynamicClient(objects...)
	check := &APIDeprecationsCheck{}

	result, err := check.Run(context.Background(), client, "4.21.5", "4.21.8")
	if err != nil {
		t.Fatal(err)
	}

	blockers, ok := result["blocker_apis"].([]map[string]any)
	if !ok {
		t.Fatal("blocker_apis not a slice")
	}
	if len(blockers) != 1 {
		t.Fatalf("blocker_apis len = %d, want 1", len(blockers))
	}
	if blockers[0]["resource"] != "flowschemas.v1beta3.flowcontrol.apiserver.k8s.io" {
		t.Errorf("blocker resource = %v", blockers[0]["resource"])
	}
	if blockers[0]["request_count"] != int64(150) {
		t.Errorf("blocker request_count = %v, want 150", blockers[0]["request_count"])
	}

	warnings, ok := result["warning_apis"].([]map[string]any)
	if !ok {
		t.Fatal("warning_apis not a slice")
	}
	// flowschemas is removed-and-deprecated, so it counts only as a blocker.
	// Only cronjobs (deprecated but not removed in target) is a warning.
	if len(warnings) != 1 {
		t.Fatalf("warning_apis len = %d, want 1", len(warnings))
	}
	if warnings[0]["resource"] != "cronjobs.v1beta1.batch" {
		t.Errorf("warning resource = %v, want cronjobs.v1beta1.batch (blocker must not be double-counted)", warnings[0]["resource"])
	}

	summary, ok := result["summary"].(map[string]any)
	if !ok {
		t.Fatal("summary not a map")
	}
	if summary["blockers"] != 1 {
		t.Errorf("summary.blockers = %v, want 1", summary["blockers"])
	}
	if summary["warnings"] != 1 {
		t.Errorf("summary.warnings = %v, want 1", summary["warnings"])
	}
	if summary["total"] != 3 {
		t.Errorf("summary.total = %v, want 3", summary["total"])
	}
}

func TestAPIDeprecationsCheck_NoBlockers(t *testing.T) {
	objects := []runtime.Object{
		&unstructured.Unstructured{Object: map[string]interface{}{
			"apiVersion": "apiserver.openshift.io/v1", "kind": "APIRequestCount",
			"metadata": map[string]interface{}{"name": "pods.v1."},
			"status": map[string]interface{}{
				"requestCount": int64(500),
			},
		}},
	}

	client := newFakeDynamicClient(objects...)
	check := &APIDeprecationsCheck{}

	result, err := check.Run(context.Background(), client, "4.21.5", "4.21.8")
	if err != nil {
		t.Fatal(err)
	}

	blockers := result["blocker_apis"].([]map[string]any)
	if len(blockers) != 0 {
		t.Errorf("expected no blockers, got %d", len(blockers))
	}

	warnings := result["warning_apis"].([]map[string]any)
	if len(warnings) != 0 {
		t.Errorf("expected no warnings, got %d", len(warnings))
	}
}

func TestNetworkCheck(t *testing.T) {
	objects := []runtime.Object{
		&unstructured.Unstructured{Object: map[string]interface{}{
			"apiVersion": "config.openshift.io/v1", "kind": "Network",
			"metadata": map[string]interface{}{"name": "cluster"},
			"status": map[string]interface{}{
				"networkType": "OpenShiftSDN",
			},
		}},
		&unstructured.Unstructured{Object: map[string]interface{}{
			"apiVersion": "config.openshift.io/v1", "kind": "Proxy",
			"metadata": map[string]interface{}{"name": "cluster"},
			"spec": map[string]interface{}{
				"httpProxy": "http://proxy.example.com:8080",
			},
		}},
		&unstructured.Unstructured{Object: map[string]interface{}{
			"apiVersion": "config.openshift.io/v1", "kind": "APIServer",
			"metadata": map[string]interface{}{"name": "cluster"},
			"spec": map[string]interface{}{
				"tlsSecurityProfile": map[string]interface{}{
					"type": "Old",
				},
			},
		}},
	}

	client := newFakeDynamicClient(objects...)
	check := &NetworkCheck{}

	result, err := check.Run(context.Background(), client, "4.21.5", "4.21.8")
	if err != nil {
		t.Fatal(err)
	}

	if result["network_type"] != "OpenShiftSDN" {
		t.Errorf("network_type = %v, want OpenShiftSDN", result["network_type"])
	}
	if result["sdn_warning"] == nil {
		t.Error("should have sdn_warning for OpenShiftSDN")
	}
	if result["tls_profile"] != "Old" {
		t.Errorf("tls_profile = %v, want Old", result["tls_profile"])
	}

	summary, ok := result["summary"].(map[string]any)
	if !ok {
		t.Fatal("summary not a map")
	}
	if summary["is_sdn"] != true {
		t.Errorf("is_sdn = %v, want true", summary["is_sdn"])
	}
}

// fakeClusterObjects returns a representative set of cluster objects that exercises
// every readiness check with non-trivial data.
func fakeClusterObjects() []runtime.Object {
	return []runtime.Object{
		// --- ClusterVersion (cluster_conditions) ---
		&unstructured.Unstructured{Object: map[string]interface{}{
			"apiVersion": "config.openshift.io/v1", "kind": "ClusterVersion",
			"metadata": map[string]interface{}{"name": "version"},
			"spec":     map[string]interface{}{"channel": "stable-4.21", "clusterID": "test-id"},
			"status": map[string]interface{}{
				"conditions": []interface{}{
					map[string]interface{}{"type": "Available", "status": "True", "reason": "AsExpected"},
					map[string]interface{}{"type": "Progressing", "status": "False", "reason": "AsExpected"},
					map[string]interface{}{"type": "Upgradeable", "status": "True", "reason": "AsExpected"},
				},
				"history": []interface{}{
					map[string]interface{}{"version": "4.21.5", "state": "Completed", "startedTime": "2026-04-10T10:00:00Z", "completionTime": "2026-04-10T11:00:00Z"},
				},
			},
		}},

		// --- ClusterOperators (operator_health + etcd_health) ---
		&unstructured.Unstructured{Object: map[string]interface{}{
			"apiVersion": "config.openshift.io/v1", "kind": "ClusterOperator",
			"metadata": map[string]interface{}{"name": "etcd"},
			"status": map[string]interface{}{
				"conditions": []interface{}{
					map[string]interface{}{"type": "Available", "status": "True"},
					map[string]interface{}{"type": "Degraded", "status": "False"},
					map[string]interface{}{"type": "Upgradeable", "status": "True"},
				},
			},
		}},
		&unstructured.Unstructured{Object: map[string]interface{}{
			"apiVersion": "config.openshift.io/v1", "kind": "ClusterOperator",
			"metadata": map[string]interface{}{"name": "authentication"},
			"status": map[string]interface{}{
				"conditions": []interface{}{
					map[string]interface{}{"type": "Available", "status": "True"},
					map[string]interface{}{"type": "Degraded", "status": "True", "reason": "OAuthFlaky", "message": "intermittent failures"},
					map[string]interface{}{"type": "Upgradeable", "status": "True"},
				},
			},
		}},

		// --- MachineConfigPools (operator_health) ---
		&unstructured.Unstructured{Object: map[string]interface{}{
			"apiVersion": "machineconfiguration.openshift.io/v1", "kind": "MachineConfigPool",
			"metadata": map[string]interface{}{"name": "master"},
			"spec":     map[string]interface{}{"paused": false},
			"status": map[string]interface{}{
				"machineCount": int64(3), "readyMachineCount": int64(3), "updatedMachineCount": int64(3),
				"conditions": []interface{}{
					map[string]interface{}{"type": "Degraded", "status": "False"},
					map[string]interface{}{"type": "Updating", "status": "False"},
				},
			},
		}},
		&unstructured.Unstructured{Object: map[string]interface{}{
			"apiVersion": "machineconfiguration.openshift.io/v1", "kind": "MachineConfigPool",
			"metadata": map[string]interface{}{"name": "worker"},
			"spec":     map[string]interface{}{"paused": false},
			"status": map[string]interface{}{
				"machineCount": int64(3), "readyMachineCount": int64(3), "updatedMachineCount": int64(3),
				"conditions": []interface{}{
					map[string]interface{}{"type": "Degraded", "status": "False"},
					map[string]interface{}{"type": "Updating", "status": "False"},
				},
			},
		}},

		// --- Etcd pods (etcd_health) ---
		&unstructured.Unstructured{Object: map[string]interface{}{
			"apiVersion": "v1", "kind": "Pod",
			"metadata": map[string]interface{}{"name": "etcd-master-0", "namespace": "openshift-etcd", "labels": map[string]interface{}{"app": "etcd"}},
			"spec":     map[string]interface{}{"nodeName": "master-0"}, "status": map[string]interface{}{"phase": "Running", "conditions": []interface{}{map[string]interface{}{"type": "Ready", "status": "True"}}},
		}},
		&unstructured.Unstructured{Object: map[string]interface{}{
			"apiVersion": "v1", "kind": "Pod",
			"metadata": map[string]interface{}{"name": "etcd-master-1", "namespace": "openshift-etcd", "labels": map[string]interface{}{"app": "etcd"}},
			"spec":     map[string]interface{}{"nodeName": "master-1"}, "status": map[string]interface{}{"phase": "Running", "conditions": []interface{}{map[string]interface{}{"type": "Ready", "status": "True"}}},
		}},
		&unstructured.Unstructured{Object: map[string]interface{}{
			"apiVersion": "v1", "kind": "Pod",
			"metadata": map[string]interface{}{"name": "etcd-master-2", "namespace": "openshift-etcd", "labels": map[string]interface{}{"app": "etcd"}},
			"spec":     map[string]interface{}{"nodeName": "master-2"}, "status": map[string]interface{}{"phase": "Running", "conditions": []interface{}{map[string]interface{}{"type": "Ready", "status": "True"}}},
		}},

		// --- Nodes (node_capacity) ---
		&unstructured.Unstructured{Object: map[string]interface{}{
			"apiVersion": "v1", "kind": "Node",
			"metadata": map[string]interface{}{"name": "master-0"},
			"status":   map[string]interface{}{"conditions": []interface{}{map[string]interface{}{"type": "Ready", "status": "True"}}},
		}},
		&unstructured.Unstructured{Object: map[string]interface{}{
			"apiVersion": "v1", "kind": "Node",
			"metadata": map[string]interface{}{"name": "master-1"},
			"status":   map[string]interface{}{"conditions": []interface{}{map[string]interface{}{"type": "Ready", "status": "True"}}},
		}},
		&unstructured.Unstructured{Object: map[string]interface{}{
			"apiVersion": "v1", "kind": "Node",
			"metadata": map[string]interface{}{"name": "master-2"},
			"status":   map[string]interface{}{"conditions": []interface{}{map[string]interface{}{"type": "Ready", "status": "True"}}},
		}},
		&unstructured.Unstructured{Object: map[string]interface{}{
			"apiVersion": "v1", "kind": "Node",
			"metadata": map[string]interface{}{"name": "worker-0"},
			"status":   map[string]interface{}{"conditions": []interface{}{map[string]interface{}{"type": "Ready", "status": "True"}}},
		}},
		&unstructured.Unstructured{Object: map[string]interface{}{
			"apiVersion": "v1", "kind": "Node",
			"metadata": map[string]interface{}{"name": "worker-1"},
			"spec":     map[string]interface{}{"unschedulable": true},
			"status":   map[string]interface{}{"conditions": []interface{}{map[string]interface{}{"type": "Ready", "status": "False"}}},
		}},

		// --- PDBs (pdb_drain) ---
		&unstructured.Unstructured{Object: map[string]interface{}{
			"apiVersion": "policy/v1", "kind": "PodDisruptionBudget",
			"metadata": map[string]interface{}{"name": "etcd-guard", "namespace": "openshift-etcd"},
			"spec":     map[string]interface{}{"maxUnavailable": "1"},
			"status":   map[string]interface{}{"currentHealthy": int64(3), "desiredHealthy": int64(2), "disruptionsAllowed": int64(1)},
		}},
		&unstructured.Unstructured{Object: map[string]interface{}{
			"apiVersion": "policy/v1", "kind": "PodDisruptionBudget",
			"metadata": map[string]interface{}{"name": "zero-budget", "namespace": "app-ns"},
			"spec":     map[string]interface{}{"maxUnavailable": "0"},
			"status":   map[string]interface{}{"currentHealthy": int64(2), "desiredHealthy": int64(2), "disruptionsAllowed": int64(0)},
		}},

		// --- APIRequestCounts (api_deprecations) ---
		&unstructured.Unstructured{Object: map[string]interface{}{
			"apiVersion": "apiserver.openshift.io/v1", "kind": "APIRequestCount",
			"metadata": map[string]interface{}{"name": "flowschemas.v1beta3.flowcontrol.apiserver.k8s.io"},
			"status": map[string]interface{}{
				"removedInRelease": "4.21.8", "requestCount": int64(100),
				"conditions": []interface{}{map[string]interface{}{"type": "Deprecated", "status": "True"}},
			},
		}},

		// --- Network, Proxy, APIServer (network) ---
		&unstructured.Unstructured{Object: map[string]interface{}{
			"apiVersion": "config.openshift.io/v1", "kind": "Network",
			"metadata": map[string]interface{}{"name": "cluster"},
			"status":   map[string]interface{}{"networkType": "OVNKubernetes"},
		}},
		&unstructured.Unstructured{Object: map[string]interface{}{
			"apiVersion": "config.openshift.io/v1", "kind": "Proxy",
			"metadata": map[string]interface{}{"name": "cluster"},
			"spec":     map[string]interface{}{"httpProxy": "http://proxy.corp:8080"},
		}},
		&unstructured.Unstructured{Object: map[string]interface{}{
			"apiVersion": "config.openshift.io/v1", "kind": "APIServer",
			"metadata": map[string]interface{}{"name": "cluster"},
			"spec":     map[string]interface{}{},
		}},

		// --- OLM: Subscription + CSV (olm_operator_lifecycle) ---
		&unstructured.Unstructured{Object: map[string]interface{}{
			"apiVersion": "operators.coreos.com/v1alpha1", "kind": "Subscription",
			"metadata": map[string]interface{}{"name": "elasticsearch-operator", "namespace": "openshift-operators-redhat"},
			"spec": map[string]interface{}{
				"channel": "stable-5.8", "name": "elasticsearch-operator",
				"source": "redhat-operators", "sourceNamespace": "openshift-marketplace",
				"installPlanApproval": "Automatic",
			},
			"status": map[string]interface{}{
				"state":        "AtLatestKnown",
				"installedCSV": "elasticsearch-operator.v5.8.6",
				"currentCSV":   "elasticsearch-operator.v5.8.6",
			},
		}},
		&unstructured.Unstructured{Object: map[string]interface{}{
			"apiVersion": "operators.coreos.com/v1alpha1", "kind": "ClusterServiceVersion",
			"metadata": map[string]interface{}{"name": "elasticsearch-operator.v5.8.6", "namespace": "openshift-operators-redhat"},
			"spec": map[string]interface{}{
				"version":     "5.8.6",
				"displayName": "OpenShift Elasticsearch Operator",
			},
			"status": map[string]interface{}{"phase": "Succeeded"},
		}},
	}
}

func TestRunAllWithFakeCluster(t *testing.T) {
	client := newFakeDynamicClient(fakeClusterObjects()...)
	output := RunAll(context.Background(), client, "4.21.5", "4.21.8")

	if output.CurrentVersion != "4.21.5" {
		t.Errorf("CurrentVersion = %q, want 4.21.5", output.CurrentVersion)
	}
	if output.TargetVersion != "4.21.8" {
		t.Errorf("TargetVersion = %q, want 4.21.8", output.TargetVersion)
	}
	if output.Meta.TotalChecks != 8 {
		t.Errorf("TotalChecks = %d, want 8", output.Meta.TotalChecks)
	}

	for _, name := range []string{
		"cluster_conditions", "operator_health", "api_deprecations",
		"node_capacity", "pdb_drain", "etcd_health", "network",
		"olm_operator_lifecycle",
	} {
		r, ok := output.Checks[name]
		if !ok {
			t.Errorf("missing check result: %s", name)
			continue
		}
		if r.Status != StatusOK {
			t.Errorf("check %s status = %q, error = %q", name, r.Status, r.Error)
		}
	}

	// cluster_conditions: verify CV data flows through
	cc := output.Checks["cluster_conditions"]
	if cc.Data["channel"] != "stable-4.21" {
		t.Errorf("cluster_conditions.channel = %v, want stable-4.21", cc.Data["channel"])
	}

	// operator_health: 2 COs, 1 degraded; 2 MCPs
	oh := output.Checks["operator_health"]
	summary := oh.Data["summary"].(map[string]any)
	if summary["total_operators"] != 2 {
		t.Errorf("operator_health total_operators = %v, want 2", summary["total_operators"])
	}
	if summary["degraded_count"] != 1 {
		t.Errorf("operator_health degraded_count = %v, want 1", summary["degraded_count"])
	}
	mcps := oh.Data["machine_config_pools"].([]map[string]any)
	if len(mcps) != 2 {
		t.Errorf("operator_health MCPs = %d, want 2", len(mcps))
	}

	// etcd_health: 3 running pods
	eh := output.Checks["etcd_health"]
	if eh.Data["total_members"] != 3 {
		t.Errorf("etcd_health total_members = %v, want 3", eh.Data["total_members"])
	}
	if eh.Data["healthy_members"] != 3 {
		t.Errorf("etcd_health healthy_members = %v, want 3", eh.Data["healthy_members"])
	}

	// node_capacity: 5 nodes, 4 ready, 1 unschedulable
	nc := output.Checks["node_capacity"]
	if nc.Data["total_nodes"] != 5 {
		t.Errorf("node_capacity total_nodes = %v, want 5", nc.Data["total_nodes"])
	}
	if nc.Data["ready_nodes"] != 4 {
		t.Errorf("node_capacity ready_nodes = %v, want 4", nc.Data["ready_nodes"])
	}
	if nc.Data["unschedulable_nodes"] != 1 {
		t.Errorf("node_capacity unschedulable_nodes = %v, want 1", nc.Data["unschedulable_nodes"])
	}

	// pdb_drain: 2 PDBs, 1 blocking
	pd := output.Checks["pdb_drain"]
	if pd.Data["total_pdbs"] != 2 {
		t.Errorf("pdb_drain total_pdbs = %v, want 2", pd.Data["total_pdbs"])
	}
	blocking := pd.Data["blocking_pdbs"].([]map[string]any)
	if len(blocking) != 1 {
		t.Errorf("pdb_drain blocking_pdbs = %d, want 1", len(blocking))
	}

	// api_deprecations: 1 blocker API
	ad := output.Checks["api_deprecations"]
	adSummary := ad.Data["summary"].(map[string]any)
	if adSummary["blockers"] != 1 {
		t.Errorf("api_deprecations blockers = %v, want 1", adSummary["blockers"])
	}

	// network: OVN, proxy configured
	nw := output.Checks["network"]
	if nw.Data["network_type"] != "OVNKubernetes" {
		t.Errorf("network type = %v, want OVNKubernetes", nw.Data["network_type"])
	}
	proxy := nw.Data["proxy"].(map[string]any)
	if proxy["http_proxy"] != "http://proxy.corp:8080" {
		t.Errorf("network proxy = %v, want http://proxy.corp:8080", proxy["http_proxy"])
	}

	// olm_operator_lifecycle: 1 subscription
	olm := output.Checks["olm_operator_lifecycle"]
	olmSummary := olm.Data["summary"].(map[string]any)
	if olmSummary["total_operators"] != 1 {
		t.Errorf("olm total_operators = %v, want 1", olmSummary["total_operators"])
	}
	operators := olm.Data["operators"].([]map[string]any)
	if operators[0]["installed_version"] != "5.8.6" {
		t.Errorf("olm installed_version = %v, want 5.8.6", operators[0]["installed_version"])
	}

	// Verify the full output marshals to valid JSON
	b, err := json.Marshal(output)
	if err != nil {
		t.Fatalf("failed to marshal output: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("failed to unmarshal output: %v", err)
	}
	if _, ok := m["checks"]; !ok {
		t.Error("marshaled output missing 'checks' key")
	}
	if _, ok := m["meta"]; !ok {
		t.Error("marshaled output missing 'meta' key")
	}
}

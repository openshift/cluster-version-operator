package readiness

import (
	"context"
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
		GVRCRD:               "CustomResourceDefinitionList",
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
			"spec": map[string]interface{}{"unschedulable": true},
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
			"spec":   map[string]interface{}{"nodeName": "master-0"},
			"status": map[string]interface{}{"phase": "Running"},
		}},
		&unstructured.Unstructured{Object: map[string]interface{}{
			"apiVersion": "v1", "kind": "Pod",
			"metadata": map[string]interface{}{"name": "etcd-master-1", "namespace": "openshift-etcd",
				"labels": map[string]interface{}{"app": "etcd"}},
			"spec":   map[string]interface{}{"nodeName": "master-1"},
			"status": map[string]interface{}{"phase": "Running"},
		}},
		&unstructured.Unstructured{Object: map[string]interface{}{
			"apiVersion": "v1", "kind": "Pod",
			"metadata": map[string]interface{}{"name": "etcd-master-2", "namespace": "openshift-etcd",
				"labels": map[string]interface{}{"app": "etcd"}},
			"spec":   map[string]interface{}{"nodeName": "master-2"},
			"status": map[string]interface{}{"phase": "Failed"},
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
				"machineCount":      int64(3),
				"readyMachineCount": int64(3),
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
				"machineCount":      int64(5),
				"readyMachineCount": int64(3),
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
		"metadata":   map[string]interface{}{"name": "version"},
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
		"metadata":   map[string]interface{}{"name": "version"},
		"spec":       map[string]interface{}{"channel": "stable-4.21", "clusterID": "abc"},
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
	if len(warnings) != 2 {
		t.Fatalf("warning_apis len = %d, want 2", len(warnings))
	}

	summary, ok := result["summary"].(map[string]any)
	if !ok {
		t.Fatal("summary not a map")
	}
	if summary["blockers"] != 1 {
		t.Errorf("summary.blockers = %v, want 1", summary["blockers"])
	}
	if summary["warnings"] != 2 {
		t.Errorf("summary.warnings = %v, want 2", summary["warnings"])
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

func TestCRDCompatCheck(t *testing.T) {
	objects := []runtime.Object{
		// CRD with stored version that is still served — ok
		&unstructured.Unstructured{Object: map[string]interface{}{
			"apiVersion": "apiextensions.k8s.io/v1", "kind": "CustomResourceDefinition",
			"metadata": map[string]interface{}{"name": "widgets.example.com"},
			"spec": map[string]interface{}{
				"versions": []interface{}{
					map[string]interface{}{"name": "v1", "served": true},
					map[string]interface{}{"name": "v1beta1", "served": true},
				},
			},
			"status": map[string]interface{}{
				"storedVersions": []interface{}{"v1", "v1beta1"},
			},
		}},
		// CRD with stored version that is NO LONGER served — issue
		&unstructured.Unstructured{Object: map[string]interface{}{
			"apiVersion": "apiextensions.k8s.io/v1", "kind": "CustomResourceDefinition",
			"metadata": map[string]interface{}{"name": "gadgets.example.com"},
			"spec": map[string]interface{}{
				"versions": []interface{}{
					map[string]interface{}{"name": "v2", "served": true},
					map[string]interface{}{"name": "v1", "served": false},
				},
			},
			"status": map[string]interface{}{
				"storedVersions": []interface{}{"v1"},
			},
		}},
	}

	client := newFakeDynamicClient(objects...)
	check := &CRDCompatCheck{}

	result, err := check.Run(context.Background(), client, "4.21.5", "4.21.8")
	if err != nil {
		t.Fatal(err)
	}

	if result["total_crds"] != 2 {
		t.Errorf("total_crds = %v, want 2", result["total_crds"])
	}

	issues, ok := result["version_issues"].([]map[string]any)
	if !ok {
		t.Fatal("version_issues not a slice")
	}
	if len(issues) != 1 {
		t.Fatalf("version_issues len = %d, want 1", len(issues))
	}
	if issues[0]["crd"] != "gadgets.example.com" {
		t.Errorf("crd = %v, want gadgets.example.com", issues[0]["crd"])
	}
	if issues[0]["stored_version"] != "v1" {
		t.Errorf("stored_version = %v, want v1", issues[0]["stored_version"])
	}

	summary, ok := result["summary"].(map[string]any)
	if !ok {
		t.Fatal("summary not a map")
	}
	if summary["version_issues"] != 1 {
		t.Errorf("summary.version_issues = %v, want 1", summary["version_issues"])
	}
}

func TestCRDCompatCheck_NoIssues(t *testing.T) {
	objects := []runtime.Object{
		&unstructured.Unstructured{Object: map[string]interface{}{
			"apiVersion": "apiextensions.k8s.io/v1", "kind": "CustomResourceDefinition",
			"metadata": map[string]interface{}{"name": "things.example.com"},
			"spec": map[string]interface{}{
				"versions": []interface{}{
					map[string]interface{}{"name": "v1", "served": true},
				},
			},
			"status": map[string]interface{}{
				"storedVersions": []interface{}{"v1"},
			},
		}},
	}

	client := newFakeDynamicClient(objects...)
	check := &CRDCompatCheck{}

	result, err := check.Run(context.Background(), client, "4.21.5", "4.21.8")
	if err != nil {
		t.Fatal(err)
	}

	issues := result["version_issues"].([]map[string]any)
	if len(issues) != 0 {
		t.Errorf("expected no version issues, got %d", len(issues))
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

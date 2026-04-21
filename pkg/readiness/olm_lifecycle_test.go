package readiness

import (
	"context"
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
)

func TestOLMOperatorLifecycleCheck_Basic(t *testing.T) {
	objects := []runtime.Object{
		// Subscription for elasticsearch-operator
		&unstructured.Unstructured{Object: map[string]interface{}{
			"apiVersion": "operators.coreos.com/v1alpha1", "kind": "Subscription",
			"metadata": map[string]interface{}{"name": "elasticsearch-operator", "namespace": "openshift-operators-redhat"},
			"spec": map[string]interface{}{
				"name":                "elasticsearch-operator",
				"channel":             "stable-5.8",
				"source":              "redhat-operators",
				"sourceNamespace":     "openshift-marketplace",
				"installPlanApproval": "Manual",
			},
			"status": map[string]interface{}{
				"state":        "AtLatestKnown",
				"installedCSV": "elasticsearch-operator.v5.8.5",
				"currentCSV":   "elasticsearch-operator.v5.8.5",
			},
		}},
		// CSV for elasticsearch-operator
		&unstructured.Unstructured{Object: map[string]interface{}{
			"apiVersion": "operators.coreos.com/v1alpha1", "kind": "ClusterServiceVersion",
			"metadata": map[string]interface{}{"name": "elasticsearch-operator.v5.8.5", "namespace": "openshift-operators-redhat"},
			"spec": map[string]interface{}{
				"version":     "5.8.5",
				"displayName": "OpenShift Elasticsearch Operator",
			},
			"status": map[string]interface{}{
				"phase": "Succeeded",
			},
		}},
		// PackageManifest for elasticsearch-operator
		&unstructured.Unstructured{Object: map[string]interface{}{
			"apiVersion": "packages.operators.coreos.com/v1", "kind": "PackageManifest",
			"metadata": map[string]interface{}{"name": "elasticsearch-operator"},
			"status": map[string]interface{}{
				"channels": []interface{}{
					map[string]interface{}{
						"name": "stable-5.8",
						"currentCSVDesc": map[string]interface{}{
							"annotations": map[string]interface{}{
								"olm.maxOpenShiftVersion": "4.17",
							},
						},
					},
					map[string]interface{}{
						"name": "stable-6.0",
					},
				},
			},
		}},
	}

	client := newFakeDynamicClient(objects...)
	check := &OLMOperatorLifecycleCheck{}

	result, err := check.Run(context.Background(), client, "4.16.0", "4.17.0")
	if err != nil {
		t.Fatal(err)
	}

	operators, ok := result["operators"].([]map[string]any)
	if !ok {
		t.Fatal("operators not a slice")
	}
	if len(operators) != 1 {
		t.Fatalf("operators len = %d, want 1", len(operators))
	}

	op := operators[0]
	if op["name"] != "elasticsearch-operator" {
		t.Errorf("name = %v, want elasticsearch-operator", op["name"])
	}
	if op["installed_version"] != "5.8.5" {
		t.Errorf("installed_version = %v, want 5.8.5", op["installed_version"])
	}
	if op["csv_phase"] != "Succeeded" {
		t.Errorf("csv_phase = %v, want Succeeded", op["csv_phase"])
	}
	if op["csv_display_name"] != "OpenShift Elasticsearch Operator" {
		t.Errorf("csv_display_name = %v, want OpenShift Elasticsearch Operator", op["csv_display_name"])
	}
	if op["install_plan_approval"] != "Manual" {
		t.Errorf("install_plan_approval = %v, want Manual", op["install_plan_approval"])
	}
	if op["channel"] != "stable-5.8" {
		t.Errorf("channel = %v, want stable-5.8", op["channel"])
	}
	if op["pending_upgrade"] != false {
		t.Errorf("pending_upgrade = %v, want false", op["pending_upgrade"])
	}

	// OCP compat — max is 4.17, target is 4.17, so compatible
	compat, ok := op["ocp_compat"].(map[string]any)
	if !ok {
		t.Fatal("ocp_compat not a map")
	}
	if compat["max"] != "4.17" {
		t.Errorf("ocp_compat.max = %v, want 4.17", compat["max"])
	}
	if op["compatible_with_target"] != true {
		t.Errorf("compatible_with_target = %v, want true", op["compatible_with_target"])
	}

	// Available channels
	channels, ok := op["available_channels"].([]string)
	if !ok {
		t.Fatal("available_channels not a string slice")
	}
	if len(channels) != 2 {
		t.Errorf("available_channels len = %d, want 2", len(channels))
	}

	// Summary
	summary, ok := result["summary"].(map[string]any)
	if !ok {
		t.Fatal("summary not a map")
	}
	if summary["total_operators"] != 1 {
		t.Errorf("total_operators = %v, want 1", summary["total_operators"])
	}
	if summary["manual_approval"] != 1 {
		t.Errorf("manual_approval = %v, want 1", summary["manual_approval"])
	}
	if summary["incompatible_with_target"] != 0 {
		t.Errorf("incompatible_with_target = %v, want 0", summary["incompatible_with_target"])
	}
}

func TestOLMOperatorLifecycleCheck_PendingUpgrade(t *testing.T) {
	objects := []runtime.Object{
		&unstructured.Unstructured{Object: map[string]interface{}{
			"apiVersion": "operators.coreos.com/v1alpha1", "kind": "Subscription",
			"metadata": map[string]interface{}{"name": "kiali-ossm", "namespace": "openshift-operators"},
			"spec": map[string]interface{}{
				"name":    "kiali-ossm",
				"channel": "stable",
				"source":  "redhat-operators",
			},
			"status": map[string]interface{}{
				"state":        "UpgradePending",
				"installedCSV": "kiali-operator.v1.72.0",
				"currentCSV":   "kiali-operator.v1.73.0",
			},
		}},
		// Installed CSV
		&unstructured.Unstructured{Object: map[string]interface{}{
			"apiVersion": "operators.coreos.com/v1alpha1", "kind": "ClusterServiceVersion",
			"metadata": map[string]interface{}{"name": "kiali-operator.v1.72.0", "namespace": "openshift-operators"},
			"spec": map[string]interface{}{
				"version":     "1.72.0",
				"displayName": "Kiali Operator",
			},
			"status": map[string]interface{}{"phase": "Replacing"},
		}},
		// Pending CSV
		&unstructured.Unstructured{Object: map[string]interface{}{
			"apiVersion": "operators.coreos.com/v1alpha1", "kind": "ClusterServiceVersion",
			"metadata": map[string]interface{}{"name": "kiali-operator.v1.73.0", "namespace": "openshift-operators"},
			"spec": map[string]interface{}{
				"version":     "1.73.0",
				"displayName": "Kiali Operator",
			},
			"status": map[string]interface{}{"phase": "InstallReady"},
		}},
	}

	client := newFakeDynamicClient(objects...)
	check := &OLMOperatorLifecycleCheck{}

	result, err := check.Run(context.Background(), client, "4.16.0", "4.17.0")
	if err != nil {
		t.Fatal(err)
	}

	operators := result["operators"].([]map[string]any)
	if len(operators) != 1 {
		t.Fatalf("operators len = %d, want 1", len(operators))
	}

	op := operators[0]
	if op["pending_upgrade"] != true {
		t.Errorf("pending_upgrade = %v, want true", op["pending_upgrade"])
	}
	if op["installed_version"] != "1.72.0" {
		t.Errorf("installed_version = %v, want 1.72.0", op["installed_version"])
	}
	if op["pending_version"] != "1.73.0" {
		t.Errorf("pending_version = %v, want 1.73.0", op["pending_version"])
	}
	if op["pending_csv"] != "kiali-operator.v1.73.0" {
		t.Errorf("pending_csv = %v, want kiali-operator.v1.73.0", op["pending_csv"])
	}

	summary := result["summary"].(map[string]any)
	if summary["pending_upgrades"] != 1 {
		t.Errorf("pending_upgrades = %v, want 1", summary["pending_upgrades"])
	}
}

func TestOLMOperatorLifecycleCheck_IncompatibleWithTarget(t *testing.T) {
	objects := []runtime.Object{
		&unstructured.Unstructured{Object: map[string]interface{}{
			"apiVersion": "operators.coreos.com/v1alpha1", "kind": "Subscription",
			"metadata": map[string]interface{}{"name": "jaeger-product", "namespace": "openshift-operators"},
			"spec": map[string]interface{}{
				"name":    "jaeger-product",
				"channel": "stable",
				"source":  "redhat-operators",
			},
			"status": map[string]interface{}{
				"state":        "AtLatestKnown",
				"installedCSV": "jaeger-operator.v1.51.0",
				"currentCSV":   "jaeger-operator.v1.51.0",
			},
		}},
		&unstructured.Unstructured{Object: map[string]interface{}{
			"apiVersion": "operators.coreos.com/v1alpha1", "kind": "ClusterServiceVersion",
			"metadata": map[string]interface{}{"name": "jaeger-operator.v1.51.0", "namespace": "openshift-operators"},
			"spec": map[string]interface{}{
				"version":     "1.51.0",
				"displayName": "Red Hat OpenShift distributed tracing platform",
			},
			"status": map[string]interface{}{"phase": "Succeeded"},
		}},
		&unstructured.Unstructured{Object: map[string]interface{}{
			"apiVersion": "packages.operators.coreos.com/v1", "kind": "PackageManifest",
			"metadata": map[string]interface{}{"name": "jaeger-product"},
			"status": map[string]interface{}{
				"channels": []interface{}{
					map[string]interface{}{
						"name": "stable",
						"currentCSVDesc": map[string]interface{}{
							"annotations": map[string]interface{}{
								"olm.maxOpenShiftVersion": "4.16",
							},
						},
					},
				},
			},
		}},
	}

	client := newFakeDynamicClient(objects...)
	check := &OLMOperatorLifecycleCheck{}

	// Target is 4.17 but max is 4.16 — incompatible
	result, err := check.Run(context.Background(), client, "4.16.0", "4.17.0")
	if err != nil {
		t.Fatal(err)
	}

	operators := result["operators"].([]map[string]any)
	op := operators[0]
	if op["compatible_with_target"] != false {
		t.Errorf("compatible_with_target = %v, want false", op["compatible_with_target"])
	}

	summary := result["summary"].(map[string]any)
	if summary["incompatible_with_target"] != 1 {
		t.Errorf("incompatible_with_target = %v, want 1", summary["incompatible_with_target"])
	}
}

func TestOLMOperatorLifecycleCheck_NoSubscriptions(t *testing.T) {
	client := newFakeDynamicClient()
	check := &OLMOperatorLifecycleCheck{}

	result, err := check.Run(context.Background(), client, "4.16.0", "4.17.0")
	if err != nil {
		t.Fatal(err)
	}

	operators := result["operators"].([]map[string]any)
	if len(operators) != 0 {
		t.Errorf("operators len = %d, want 0", len(operators))
	}

	summary := result["summary"].(map[string]any)
	if summary["total_operators"] != 0 {
		t.Errorf("total_operators = %v, want 0", summary["total_operators"])
	}
}

func TestOLMOperatorLifecycleCheck_DefaultApproval(t *testing.T) {
	objects := []runtime.Object{
		&unstructured.Unstructured{Object: map[string]interface{}{
			"apiVersion": "operators.coreos.com/v1alpha1", "kind": "Subscription",
			"metadata": map[string]interface{}{"name": "test-op", "namespace": "openshift-operators"},
			"spec": map[string]interface{}{
				"name":    "test-op",
				"channel": "stable",
				"source":  "redhat-operators",
				// no installPlanApproval — defaults to Automatic
			},
			"status": map[string]interface{}{
				"state":        "AtLatestKnown",
				"installedCSV": "test-op.v1.0.0",
				"currentCSV":   "test-op.v1.0.0",
			},
		}},
	}

	client := newFakeDynamicClient(objects...)
	check := &OLMOperatorLifecycleCheck{}

	result, err := check.Run(context.Background(), client, "4.16.0", "4.17.0")
	if err != nil {
		t.Fatal(err)
	}

	operators := result["operators"].([]map[string]any)
	op := operators[0]
	if op["install_plan_approval"] != "Automatic" {
		t.Errorf("install_plan_approval = %v, want Automatic", op["install_plan_approval"])
	}

	summary := result["summary"].(map[string]any)
	if summary["manual_approval"] != 0 {
		t.Errorf("manual_approval = %v, want 0", summary["manual_approval"])
	}
}

func TestExtractChannels(t *testing.T) {
	pm := map[string]interface{}{
		"status": map[string]interface{}{
			"channels": []interface{}{
				map[string]interface{}{"name": "stable-5.8"},
				map[string]interface{}{"name": "stable-6.0"},
				map[string]interface{}{"name": "preview"},
			},
		},
	}

	channels := extractChannels(pm)
	if len(channels) != 3 {
		t.Fatalf("channels len = %d, want 3", len(channels))
	}
	expected := []string{"stable-5.8", "stable-6.0", "preview"}
	for i, want := range expected {
		if channels[i] != want {
			t.Errorf("channels[%d] = %v, want %v", i, channels[i], want)
		}
	}
}

func TestExtractOCPCompat(t *testing.T) {
	t.Run("with maxOpenShiftVersion", func(t *testing.T) {
		pm := map[string]interface{}{
			"status": map[string]interface{}{
				"channels": []interface{}{
					map[string]interface{}{
						"name": "stable",
						"currentCSVDesc": map[string]interface{}{
							"annotations": map[string]interface{}{
								"olm.maxOpenShiftVersion": "4.16",
							},
						},
					},
				},
			},
		}

		compat := extractOCPCompat(pm, "stable")
		if compat == nil {
			t.Fatal("expected non-nil compat")
		}
		if compat["max"] != "4.16" {
			t.Errorf("max = %v, want 4.16", compat["max"])
		}
	})

	t.Run("channel not found", func(t *testing.T) {
		pm := map[string]interface{}{
			"status": map[string]interface{}{
				"channels": []interface{}{
					map[string]interface{}{
						"name": "stable",
					},
				},
			},
		}

		compat := extractOCPCompat(pm, "preview")
		if compat != nil {
			t.Errorf("expected nil compat for missing channel, got %v", compat)
		}
	})

	t.Run("no annotations", func(t *testing.T) {
		pm := map[string]interface{}{
			"status": map[string]interface{}{
				"channels": []interface{}{
					map[string]interface{}{
						"name": "stable",
						"currentCSVDesc": map[string]interface{}{},
					},
				},
			},
		}

		compat := extractOCPCompat(pm, "stable")
		if compat != nil {
			t.Errorf("expected nil compat for no annotations, got %v", compat)
		}
	})
}

func TestParseMinOCPFromProperties(t *testing.T) {
	t.Run("valid olm.minOpenShiftVersion", func(t *testing.T) {
		props := `[{"type":"olm.minOpenShiftVersion","value":"4.14"},{"type":"olm.maxOpenShiftVersion","value":"4.17"}]`
		got := parseMinOCPFromProperties(props)
		if got != "4.14" {
			t.Errorf("got %q, want 4.14", got)
		}
	})

	t.Run("no minOpenShiftVersion", func(t *testing.T) {
		props := `[{"type":"olm.maxOpenShiftVersion","value":"4.17"}]`
		got := parseMinOCPFromProperties(props)
		if got != "" {
			t.Errorf("got %q, want empty", got)
		}
	})

	t.Run("invalid JSON", func(t *testing.T) {
		got := parseMinOCPFromProperties("not json")
		if got != "" {
			t.Errorf("got %q, want empty", got)
		}
	})

	t.Run("empty array", func(t *testing.T) {
		got := parseMinOCPFromProperties("[]")
		if got != "" {
			t.Errorf("got %q, want empty", got)
		}
	})
}

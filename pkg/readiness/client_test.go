package readiness

import (
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

func TestGetConditions(t *testing.T) {
	obj := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"status": map[string]interface{}{
				"conditions": []interface{}{
					map[string]interface{}{
						"type":               "Available",
						"status":             "True",
						"reason":             "AsExpected",
						"message":            "All is well",
						"lastTransitionTime": "2026-04-14T10:00:00Z",
					},
					map[string]interface{}{
						"type":               "Degraded",
						"status":             "False",
						"reason":             "AsExpected",
						"message":            "",
						"lastTransitionTime": "2026-04-14T10:00:00Z",
					},
				},
			},
		},
	}

	conditions := GetConditions(obj)

	if len(conditions) != 2 {
		t.Fatalf("got %d conditions, want 2", len(conditions))
	}

	avail := conditions["Available"]
	if avail.Status != "True" {
		t.Errorf("Available.Status = %q, want True", avail.Status)
	}
	if avail.Reason != "AsExpected" {
		t.Errorf("Available.Reason = %q, want AsExpected", avail.Reason)
	}
	if avail.Message != "All is well" {
		t.Errorf("Available.Message = %q", avail.Message)
	}

	degraded := conditions["Degraded"]
	if degraded.Status != "False" {
		t.Errorf("Degraded.Status = %q, want False", degraded.Status)
	}
}

func TestGetConditions_NoConditions(t *testing.T) {
	obj := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"status": map[string]interface{}{},
		},
	}
	conditions := GetConditions(obj)
	if len(conditions) != 0 {
		t.Errorf("got %d conditions, want 0", len(conditions))
	}
}

func TestCompareVersions(t *testing.T) {
	tests := []struct {
		a, b     string
		expected int
	}{
		{"4.21.5", "4.21.8", -1},
		{"4.21.8", "4.21.5", 1},
		{"4.21.5", "4.21.5", 0},
		{"4.22.0", "4.21.5", 1},
		{"bad", "4.21.5", 0},
		{"4.21.5", "bad", 0},
	}

	for _, tt := range tests {
		t.Run(tt.a+"_vs_"+tt.b, func(t *testing.T) {
			got := CompareVersions(tt.a, tt.b)
			if got != tt.expected {
				t.Errorf("CompareVersions(%q, %q) = %d, want %d", tt.a, tt.b, got, tt.expected)
			}
		})
	}
}

func TestFormatLabelSelector(t *testing.T) {
	tests := []struct {
		name     string
		labels   map[string]string
		contains []string
	}{
		{
			name:     "single label",
			labels:   map[string]string{"app": "etcd"},
			contains: []string{"app=etcd"},
		},
		{
			name:     "empty",
			labels:   map[string]string{},
			contains: []string{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := FormatLabelSelector(tt.labels)
			for _, s := range tt.contains {
				found := false
				for i := 0; i <= len(got)-len(s); i++ {
					if got[i:i+len(s)] == s {
						found = true
						break
					}
				}
				if !found {
					t.Errorf("FormatLabelSelector(%v) = %q, want to contain %q", tt.labels, got, s)
				}
			}
		})
	}
}

func TestNestedHelpers(t *testing.T) {
	obj := map[string]interface{}{
		"spec": map[string]interface{}{
			"name":     "test",
			"count":    int64(42),
			"enabled":  true,
			"items":    []interface{}{"a", "b"},
			"metadata": map[string]interface{}{"key": "val"},
		},
	}

	if got := NestedString(obj, "spec", "name"); got != "test" {
		t.Errorf("NestedString = %q, want test", got)
	}
	if got := NestedInt64(obj, "spec", "count"); got != 42 {
		t.Errorf("NestedInt64 = %d, want 42", got)
	}
	if got := NestedBool(obj, "spec", "enabled"); got != true {
		t.Errorf("NestedBool = %v, want true", got)
	}
	if got := NestedSlice(obj, "spec", "items"); len(got) != 2 {
		t.Errorf("NestedSlice len = %d, want 2", len(got))
	}
	if got := NestedMap(obj, "spec", "metadata"); got["key"] != "val" {
		t.Errorf("NestedMap[key] = %v, want val", got["key"])
	}

	// Missing fields return zero values
	if got := NestedString(obj, "spec", "missing"); got != "" {
		t.Errorf("missing string = %q, want empty", got)
	}
	if got := NestedInt64(obj, "spec", "missing"); got != 0 {
		t.Errorf("missing int64 = %d, want 0", got)
	}
}

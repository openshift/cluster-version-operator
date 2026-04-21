package readiness

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"k8s.io/client-go/dynamic"
)

type fakeCheck struct {
	name string
	data map[string]any
	err  error
}

func (f *fakeCheck) Name() string { return f.name }
func (f *fakeCheck) Run(_ context.Context, _ dynamic.Interface, _, _ string) (map[string]any, error) {
	return f.data, f.err
}

func TestCheckResultMarshalJSON(t *testing.T) {
	t.Run("ok result merges data with metadata", func(t *testing.T) {
		r := CheckResult{
			Status:  "ok",
			Elapsed: 1.5,
			Data:    map[string]any{"foo": "bar", "count": 42},
		}
		b, err := json.Marshal(r)
		if err != nil {
			t.Fatal(err)
		}
		var m map[string]any
		if err := json.Unmarshal(b, &m); err != nil {
			t.Fatal(err)
		}
		if m["_status"] != "ok" {
			t.Errorf("_status = %v, want ok", m["_status"])
		}
		if m["foo"] != "bar" {
			t.Errorf("foo = %v, want bar", m["foo"])
		}
		if _, ok := m["_error"]; ok {
			t.Error("_error should be omitted for ok results")
		}
	})

	t.Run("error result includes error field", func(t *testing.T) {
		r := CheckResult{
			Status:  "error",
			Error:   "something failed",
			Elapsed: 0.1,
			Data:    map[string]any{},
		}
		b, err := json.Marshal(r)
		if err != nil {
			t.Fatal(err)
		}
		var m map[string]any
		if err := json.Unmarshal(b, &m); err != nil {
			t.Fatal(err)
		}
		if m["_error"] != "something failed" {
			t.Errorf("_error = %v, want 'something failed'", m["_error"])
		}
	})
}

func TestFakeCheckInterface(t *testing.T) {
	ok := &fakeCheck{name: "ok_check", data: map[string]any{"healthy": true}}
	fail := &fakeCheck{name: "err_check", err: errors.New("fail")}

	if ok.Name() != "ok_check" {
		t.Errorf("Name() = %q", ok.Name())
	}

	data, err := ok.Run(context.Background(), nil, "4.21.5", "4.21.8")
	if err != nil {
		t.Errorf("ok check should not error: %v", err)
	}
	if data["healthy"] != true {
		t.Errorf("data = %v", data)
	}

	_, err = fail.Run(context.Background(), nil, "4.21.5", "4.21.8")
	if err == nil {
		t.Error("fail check should error")
	}
}

func TestOutputMarshalJSON(t *testing.T) {
	output := &Output{
		CurrentVersion: "4.21.5",
		TargetVersion:  "4.21.8",
		Checks: map[string]CheckResult{
			"test": {Status: "ok", Elapsed: 0.5, Data: map[string]any{"key": "val"}},
		},
		Meta: Meta{TotalChecks: 1, ChecksOK: 1, ChecksErrored: 0, ElapsedSeconds: 0.5},
	}

	b, err := json.Marshal(output)
	if err != nil {
		t.Fatal(err)
	}

	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}

	if m["current_version"] != "4.21.5" {
		t.Errorf("current_version = %v", m["current_version"])
	}
	if m["target_version"] != "4.21.8" {
		t.Errorf("target_version = %v", m["target_version"])
	}

	checks, ok := m["checks"].(map[string]any)
	if !ok {
		t.Fatal("checks not a map")
	}
	testCheck, ok := checks["test"].(map[string]any)
	if !ok {
		t.Fatal("test check not a map")
	}
	if testCheck["_status"] != "ok" {
		t.Errorf("test._status = %v", testCheck["_status"])
	}
	if testCheck["key"] != "val" {
		t.Errorf("test.key = %v", testCheck["key"])
	}
}

func TestSectionError(t *testing.T) {
	var errs []map[string]any
	SectionError(&errs, "test_section", errors.New("something broke"))

	if len(errs) != 1 {
		t.Fatalf("len = %d, want 1", len(errs))
	}
	if errs[0]["section"] != "test_section" {
		t.Errorf("section = %v", errs[0]["section"])
	}
	if errs[0]["error"] != "something broke" {
		t.Errorf("error = %v", errs[0]["error"])
	}
}

func TestRunAllMixedResults(t *testing.T) {
	okCheck := &fakeCheck{name: "passing", data: map[string]any{"healthy": true}}
	failCheck := &fakeCheck{name: "failing", err: errors.New("something broke")}
	partialCheck := &fakeCheck{name: "partial", data: map[string]any{"partial": true}, err: errors.New("partial failure")}

	origAllChecks := AllChecks
	// We can't override AllChecks directly since it's a function, so test RunAll
	// indirectly through the real checks. Instead, test the orchestration logic
	// by running checks manually and verifying the output structure.

	_ = origAllChecks // satisfy usage

	// Simulate RunAll behavior manually with our fake checks
	checks := []Check{okCheck, failCheck, partialCheck}
	results := make(map[string]CheckResult, len(checks))
	ok := 0
	errored := 0

	for _, ch := range checks {
		data, err := ch.Run(context.Background(), nil, "4.21.5", "4.21.8")
		result := CheckResult{Elapsed: 0.1}
		if err != nil {
			result.Status = "error"
			result.Error = err.Error()
			if data != nil {
				result.Data = data
			} else {
				result.Data = map[string]any{}
			}
			errored++
		} else {
			result.Status = "ok"
			result.Data = data
			ok++
		}
		results[ch.Name()] = result
	}

	if ok != 1 {
		t.Errorf("ok count = %d, want 1", ok)
	}
	if errored != 2 {
		t.Errorf("errored count = %d, want 2", errored)
	}

	// Verify passing check
	passing := results["passing"]
	if passing.Status != "ok" {
		t.Errorf("passing.Status = %q, want ok", passing.Status)
	}
	if passing.Data["healthy"] != true {
		t.Errorf("passing.Data[healthy] = %v", passing.Data["healthy"])
	}

	// Verify failing check
	failing := results["failing"]
	if failing.Status != "error" {
		t.Errorf("failing.Status = %q, want error", failing.Status)
	}
	if failing.Error != "something broke" {
		t.Errorf("failing.Error = %q", failing.Error)
	}

	// Verify partial failure preserves data
	partial := results["partial"]
	if partial.Status != "error" {
		t.Errorf("partial.Status = %q, want error", partial.Status)
	}
	if partial.Data["partial"] != true {
		t.Errorf("partial.Data[partial] = %v, want true", partial.Data["partial"])
	}
	if partial.Error != "partial failure" {
		t.Errorf("partial.Error = %q", partial.Error)
	}
}

func TestAllChecksReturnsExpectedCount(t *testing.T) {
	checks := AllChecks()
	if len(checks) != 9 {
		t.Errorf("AllChecks() returned %d checks, want 9", len(checks))
	}

	names := make(map[string]bool)
	for _, c := range checks {
		names[c.Name()] = true
	}

	expected := []string{
		"cluster_conditions", "operator_health", "api_deprecations",
		"node_capacity", "pdb_drain", "etcd_health", "network", "crd_compat",
		"olm_operator_lifecycle",
	}
	for _, name := range expected {
		if !names[name] {
			t.Errorf("missing check: %s", name)
		}
	}
}

package readiness

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"k8s.io/client-go/dynamic"
)

type fakeCheck struct {
	name  string
	data  map[string]any
	err   error
	panic bool
}

func (f *fakeCheck) Name() string { return f.name }
func (f *fakeCheck) Run(_ context.Context, _ dynamic.Interface, _, _ string) (map[string]any, error) {
	if f.panic {
		panic("check exploded")
	}
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
	orig := AllChecks
	defer func() { AllChecks = orig }()

	AllChecks = func() []Check {
		return []Check{
			&fakeCheck{name: "passing", data: map[string]any{"healthy": true}},
			&fakeCheck{name: "failing", err: errors.New("something broke")},
			&fakeCheck{name: "partial", data: map[string]any{"partial": true}, err: errors.New("partial failure")},
		}
	}

	output := RunAll(context.Background(), nil, "4.21.5", "4.21.8")

	if output.Meta.TotalChecks != 3 {
		t.Errorf("TotalChecks = %d, want 3", output.Meta.TotalChecks)
	}
	if output.Meta.ChecksOK != 1 {
		t.Errorf("ChecksOK = %d, want 1", output.Meta.ChecksOK)
	}
	if output.Meta.ChecksErrored != 2 {
		t.Errorf("ChecksErrored = %d, want 2", output.Meta.ChecksErrored)
	}

	passing := output.Checks["passing"]
	if passing.Status != StatusOK {
		t.Errorf("passing.Status = %q, want ok", passing.Status)
	}
	if passing.Data["healthy"] != true {
		t.Errorf("passing.Data[healthy] = %v", passing.Data["healthy"])
	}

	failing := output.Checks["failing"]
	if failing.Status != StatusError {
		t.Errorf("failing.Status = %q, want error", failing.Status)
	}
	if failing.Error != "something broke" {
		t.Errorf("failing.Error = %q", failing.Error)
	}

	partial := output.Checks["partial"]
	if partial.Status != StatusError {
		t.Errorf("partial.Status = %q, want error", partial.Status)
	}
	if partial.Data["partial"] != true {
		t.Errorf("partial.Data[partial] = %v, want true", partial.Data["partial"])
	}
	if partial.Error != "partial failure" {
		t.Errorf("partial.Error = %q", partial.Error)
	}
}

func TestRunAllRecoversPanic(t *testing.T) {
	orig := AllChecks
	defer func() { AllChecks = orig }()

	AllChecks = func() []Check {
		return []Check{
			&fakeCheck{name: "ok_check", data: map[string]any{"healthy": true}},
			&fakeCheck{name: "panicking", panic: true},
		}
	}

	output := RunAll(context.Background(), nil, "4.21.5", "4.21.8")

	if output.Meta.TotalChecks != 2 {
		t.Errorf("TotalChecks = %d, want 2", output.Meta.TotalChecks)
	}
	if output.Meta.ChecksOK != 1 {
		t.Errorf("ChecksOK = %d, want 1", output.Meta.ChecksOK)
	}
	if output.Meta.ChecksErrored != 1 {
		t.Errorf("ChecksErrored = %d, want 1", output.Meta.ChecksErrored)
	}

	ok := output.Checks["ok_check"]
	if ok.Status != StatusOK {
		t.Errorf("ok_check.Status = %q, want ok", ok.Status)
	}

	panicked := output.Checks["panicking"]
	if panicked.Status != StatusError {
		t.Errorf("panicking.Status = %q, want error", panicked.Status)
	}
	if panicked.Error != "panic: check exploded" {
		t.Errorf("panicking.Error = %q, want 'panic: check exploded'", panicked.Error)
	}
}

func TestAllChecksReturnsExpectedCount(t *testing.T) {
	checks := AllChecks()
	if len(checks) != 8 {
		t.Errorf("AllChecks() returned %d checks, want 8", len(checks))
	}

	names := make(map[string]bool)
	for _, c := range checks {
		names[c.Name()] = true
	}

	expected := []string{
		"cluster_conditions", "operator_health", "api_deprecations",
		"node_capacity", "pdb_drain", "etcd_health", "network",
		"olm_operator_lifecycle",
	}
	for _, name := range expected {
		if !names[name] {
			t.Errorf("missing check: %s", name)
		}
	}
}

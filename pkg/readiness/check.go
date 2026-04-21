package readiness

import (
	"context"
	"encoding/json"
	"sync"
	"time"

	"k8s.io/client-go/dynamic"
)

// Check is the interface that each readiness check implements.
type Check interface {
	Name() string
	Run(ctx context.Context, c dynamic.Interface, current, target string) (map[string]any, error)
}

// CheckResult wraps a check's output with metadata.
type CheckResult struct {
	Status  string         `json:"_status"`
	Error   string         `json:"_error,omitempty"`
	Elapsed float64        `json:"_elapsed_seconds"`
	Data    map[string]any `json:"-"`
}

func (r CheckResult) MarshalJSON() ([]byte, error) {
	m := make(map[string]any, len(r.Data)+3)
	for k, v := range r.Data {
		m[k] = v
	}
	m["_status"] = r.Status
	m["_elapsed_seconds"] = r.Elapsed
	if r.Error != "" {
		m["_error"] = r.Error
	}
	return json.Marshal(m)
}

// Output is the top-level readiness report structure.
type Output struct {
	CurrentVersion string                 `json:"current_version"`
	TargetVersion  string                 `json:"target_version"`
	Checks         map[string]CheckResult `json:"checks"`
	Meta           Meta                   `json:"meta"`
}

// Meta contains summary information about the readiness check run.
type Meta struct {
	TotalChecks    int     `json:"total_checks"`
	ChecksOK       int     `json:"checks_ok"`
	ChecksErrored  int     `json:"checks_errored"`
	ElapsedSeconds float64 `json:"elapsed_seconds"`
}

const (
	perCheckTimeout = 60 * time.Second

	StatusOK    = "ok"
	StatusError = "error"
)

// AllChecks returns all registered readiness checks.
// Checks are split into two categories:
//   - cluster_conditions: reads CVO's already-computed state (no re-querying)
//   - everything else: gathers NEW data that CVO doesn't already track
func AllChecks() []Check {
	return []Check{
		&ClusterConditionsCheck{},    // reads existing CVO conditions — no duplication
		&OperatorHealthCheck{},       // per-CO detail + MCPs (CVO only aggregates)
		&APIDeprecationsCheck{},      // new: deprecated API usage
		&NodeCapacityCheck{},         // new: node readiness and headroom
		&PDBDrainCheck{},             // new: PDB drain blockers
		&EtcdHealthCheck{},           // new: deep etcd health (beyond CO condition)
		&NetworkCheck{},              // new: SDN migration, TLS, proxy
		&CRDCompatCheck{},            // new: CRD version mismatches
		&OLMOperatorLifecycleCheck{}, // new: OLM operator lifecycle (OCPSTRAT-2618)
		// Known issues (Jira/KB) are NOT checked here — the agent uses its
		// redhat-support skill to query contextually based on readiness findings.
	}
}

// RunAll executes all readiness checks in parallel with per-check timeouts.
func RunAll(ctx context.Context, c dynamic.Interface, current, target string) *Output {
	checks := AllChecks()
	results := make(map[string]CheckResult, len(checks))

	var mu sync.Mutex
	var wg sync.WaitGroup

	totalStart := time.Now()

	for _, check := range checks {
		wg.Add(1)
		go func(ch Check) {
			defer wg.Done()

			checkCtx, cancel := context.WithTimeout(ctx, perCheckTimeout)
			defer cancel()

			start := time.Now()
			data, err := ch.Run(checkCtx, c, current, target)
			elapsed := time.Since(start).Seconds()

			result := CheckResult{
				Elapsed: elapsed,
			}

			if err != nil {
				result.Status = StatusError
				result.Error = err.Error()
				if data != nil {
					result.Data = data
				} else {
					result.Data = map[string]any{}
				}
			} else {
				result.Status = StatusOK
				result.Data = data
			}

			mu.Lock()
			results[ch.Name()] = result
			mu.Unlock()
		}(check)
	}

	wg.Wait()
	totalElapsed := time.Since(totalStart).Seconds()

	ok := 0
	errored := 0
	for _, r := range results {
		if r.Status == StatusOK {
			ok++
		} else {
			errored++
		}
	}

	return &Output{
		CurrentVersion: current,
		TargetVersion:  target,
		Checks:         results,
		Meta: Meta{
			TotalChecks:    len(checks),
			ChecksOK:       ok,
			ChecksErrored:  errored,
			ElapsedSeconds: totalElapsed,
		},
	}
}

// SectionError appends a section error entry to the errors slice.
func SectionError(errors *[]map[string]any, section string, err error) {
	*errors = append(*errors, map[string]any{
		"section": section,
		"error":   err.Error(),
	})
}

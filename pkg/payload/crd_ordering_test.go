package payload

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"github.com/openshift/library-go/pkg/manifest"
)

// This file guards a payload-wide invariant: a CustomResource (CR) must never be
// applied before the CustomResourceDefinition (CRD) that defines it.
//
// The CVO applies manifests ordered by run-level (the 0000_NN_ filename prefix,
// parsed here with the same reMatchPattern the CVO uses) and will not advance to
// a higher run-level until all lower run-levels complete. Within a single
// run-level, manifests of the *same* component apply serially in filename
// byte-order, but manifests of *different* components apply in parallel. A CR
// that is applied before (or concurrently with) its CRD fails to create; on the
// strict UpdatingPayload path that failure blocks its run-level and deadlocks
// the update (see OCPBUGS-99266, where the empty CRIOCredentialProviderConfig CR
// shipped at run-level 0000_05 while its CRD shipped at 0000_10).
//
// A CRD is guaranteed to be applied (and, thanks to the CVO's CRD-establishment
// wait, established) before a CR when any of the following hold:
//   - the CRD is release.openshift.io/bootstrap-required (created at bootstrap,
//     before the CVO's ordered apply runs, so it already exists in the cluster);
//   - the CRD is at a lower run-level than the CR;
//   - the CRD is at the same run-level and the SAME component, and its filename
//     sorts before the CR's (same-component manifests apply serially).
//
// Same run-level + different component is NOT safe: those apply in parallel with
// no ordering edge between them.
//
// Caveats deliberately encoded here (see pkg/payload/payload.go and
// pkg/payload/task_graph.go): strict CRD-before-CR ordering only holds on the
// UpdatingPayload path; InitializingPayload flattens run-levels and
// ReconcilingPayload permutes, and both skip the establishment wait. The check
// below is intentionally gating-independent (it considers the union of shipped
// manifests) so that a latent deadlock reachable under *any* feature-gate /
// feature-set / cluster-profile combination is caught, not just the combination
// realized by one Include() filter.

const bootstrapRequiredAnnotation = "release.openshift.io/bootstrap-required"

// orderingManifest is the subset of a payload manifest relevant to CRD/CR
// apply-ordering.
type orderingManifest struct {
	filename  string
	runLevel  int    // parsed from the 0000_NN_ prefix; -1 if unparseable
	component string // the NAME in 0000_NN_NAME_; "" if unparseable

	// For a CRD manifest:
	isCRD    bool
	crdGroup string
	crdKind  string

	// For a CR (or any non-CRD) manifest:
	group string
	kind  string

	bootstrapRequired bool
}

// reRunLevel parses just the 0000_NN run-level prefix. Unlike reMatchPattern it
// does not require an operatorOrdering segment, so it also parses single-token
// payload filenames such as 0000_90_openshift-cluster-image-policy.yaml (which
// reMatchPattern, and therefore the CVO's component splitter, cannot parse).
var reRunLevel = regexp.MustCompile(`^0000_(\d+)_`)

// parseRunLevelComponent returns the run-level number (-1 if unparseable) and
// the component (empty if the operatorOrdering-style name is unparseable).
func parseRunLevelComponent(filename string) (int, string) {
	rl := -1
	if m := reRunLevel.FindStringSubmatch(filename); m != nil {
		if n, err := strconv.Atoi(m[1]); err == nil {
			rl = n
		}
	}
	comp := ""
	if m := reMatchPattern.FindStringSubmatch(filename); m != nil {
		comp = m[groupComponent]
	}
	return rl, comp
}

// toOrderingManifest extracts ordering-relevant fields from a parsed manifest.
func toOrderingManifest(m manifest.Manifest) orderingManifest {
	rl, comp := parseRunLevelComponent(m.OriginalFilename)
	om := orderingManifest{
		filename:  m.OriginalFilename,
		runLevel:  rl,
		component: comp,
		group:     m.GVK.Group,
		kind:      m.GVK.Kind,
	}
	if m.Obj != nil {
		anns := m.Obj.GetAnnotations()
		om.bootstrapRequired = strings.EqualFold(anns[bootstrapRequiredAnnotation], "true")
	}
	if m.GVK.Group == "apiextensions.k8s.io" && m.GVK.Kind == "CustomResourceDefinition" && m.Obj != nil {
		om.isCRD = true
		om.crdGroup, _, _ = unstructured.NestedString(m.Obj.Object, "spec", "group")
		om.crdKind, _, _ = unstructured.NestedString(m.Obj.Object, "spec", "names", "kind")
	}
	return om
}

// severity classifies how bad a CR/CRD ordering is.
type severity int

const (
	// severitySafe: the CRD is guaranteed applied and established before the CR.
	severitySafe severity = iota
	// severityNonBlocking: the CR and CRD share a run-level, so the CR may be
	// applied before its CRD, but the run-level barrier lets the failed CR
	// re-apply once the CRD is established later in the same run-level. This
	// self-heals and does not deadlock, though it costs an extra apply pass.
	severityNonBlocking
	// severityBlocking: the CR's run-level is strictly lower than its CRD's, so
	// on the strict UpdatingPayload path the CR's run-level can never complete
	// (its CRD is created at a higher, never-reached run-level) — a deadlock.
	severityBlocking
)

func (s severity) String() string {
	switch s {
	case severitySafe:
		return "SAFE"
	case severityNonBlocking:
		return "NONBLOCKING"
	default:
		return "BLOCKING"
	}
}

// classifyPair returns the ordering severity of a single CR against one of its
// CRD variants.
func classifyPair(crd, cr orderingManifest) severity {
	if crd.bootstrapRequired {
		return severitySafe
	}
	// If either run-level is unparseable we cannot prove a deadlock; downgrade to
	// NONBLOCKING so the pair is surfaced as a warning rather than a hard failure.
	if crd.runLevel < 0 || cr.runLevel < 0 {
		return severityNonBlocking
	}
	switch {
	case crd.runLevel < cr.runLevel:
		return severitySafe
	case crd.runLevel > cr.runLevel:
		return severityBlocking
	default: // same run-level
		// Same component applies serially in filename byte-order (Go string
		// comparison is byte-wise, matching the CVO's C-locale ordering).
		if crd.component == cr.component && crd.filename < cr.filename {
			return severitySafe
		}
		return severityNonBlocking
	}
}

type orderingViolation struct {
	cr       orderingManifest
	crd      orderingManifest // best (least-severe) matching CRD found in the payload
	severity severity
}

type groupKind struct{ group, kind string }

// checkCRDOrdering classifies every CR whose CRD ships in the same manifest set
// by its best-case (least-severe) ordering against that CRD, and returns the
// pairs that are not SAFE. CRs whose CRD is not present in the set (e.g. provided
// out-of-band, or bootstrap-only and not shipped as a payload manifest) are not
// checked.
func checkCRDOrdering(manifests []orderingManifest) []orderingViolation {
	crds := map[groupKind][]orderingManifest{}
	for _, m := range manifests {
		if m.isCRD && m.crdGroup != "" && m.crdKind != "" {
			gk := groupKind{m.crdGroup, m.crdKind}
			crds[gk] = append(crds[gk], m)
		}
	}

	var violations []orderingViolation
	for _, cr := range manifests {
		if cr.isCRD {
			continue
		}
		variants, ok := crds[groupKind{cr.group, cr.kind}]
		if !ok {
			continue
		}
		best := severityBlocking
		var bestCRD orderingManifest
		for _, crd := range variants {
			if s := classifyPair(crd, cr); s <= best {
				best = s
				bestCRD = crd
			}
		}
		if best != severitySafe {
			violations = append(violations, orderingViolation{cr: cr, crd: bestCRD, severity: best})
		}
	}
	return violations
}

// parseOrderingManifests parses raw manifest bytes loaded from filename into
// ordering manifests (a single file may contain multiple documents).
func parseOrderingManifests(t *testing.T, filename string, raw []byte) []orderingManifest {
	t.Helper()
	parsed, err := manifest.ParseManifests(strings.NewReader(string(raw)))
	if err != nil {
		t.Fatalf("parse %s: %v", filename, err)
	}
	out := make([]orderingManifest, 0, len(parsed))
	for i := range parsed {
		parsed[i].OriginalFilename = filepath.Base(filename)
		out = append(out, toOrderingManifest(parsed[i]))
	}
	return out
}

// fixtureManifest is a tiny helper for building an in-memory payload in tests.
type fixtureManifest struct {
	filename string
	yaml     string
}

func loadFixture(t *testing.T, fixtures []fixtureManifest) []orderingManifest {
	t.Helper()
	var all []orderingManifest
	for _, f := range fixtures {
		all = append(all, parseOrderingManifests(t, f.filename, []byte(f.yaml))...)
	}
	return all
}

func crdManifest(filename, group, kind, plural string, bootstrap bool) fixtureManifest {
	anns := ""
	if bootstrap {
		anns = "  annotations:\n    release.openshift.io/bootstrap-required: \"true\"\n"
	}
	return fixtureManifest{
		filename: filename,
		yaml: fmt.Sprintf(`apiVersion: apiextensions.k8s.io/v1
kind: CustomResourceDefinition
metadata:
  name: %s.%s
%sspec:
  group: %s
  names:
    kind: %s
    plural: %s
  scope: Cluster
  versions:
  - name: v1
    served: true
    storage: true
`, plural, group, anns, group, kind, plural),
	}
}

func crManifest(filename, apiVersion, kind, name string) fixtureManifest {
	return fixtureManifest{
		filename: filename,
		yaml: fmt.Sprintf("apiVersion: %s\nkind: %s\nmetadata:\n  name: %s\nspec: {}\n",
			apiVersion, kind, name),
	}
}

func TestCheckCRDOrdering_Fixtures(t *testing.T) {
	tests := []struct {
		name         string
		fixtures     []fixtureManifest
		wantSeverity severity // severitySafe means "expect no violation"
	}{
		{
			name: "CRD before CR, same component, same run-level (SAFE)",
			fixtures: []fixtureManifest{
				crdManifest("0000_10_config-operator_01_widgets.crd.yaml", "example.io", "Widget", "widgets", false),
				crManifest("0000_10_config-operator_02_widget.cr.yaml", "example.io/v1", "Widget", "cluster"),
			},
			wantSeverity: severitySafe,
		},
		{
			name: "CR before CRD, same component, same run-level (NONBLOCKING)",
			fixtures: []fixtureManifest{
				crManifest("0000_10_config-operator_01_widget.cr.yaml", "example.io/v1", "Widget", "cluster"),
				crdManifest("0000_10_config-operator_02_widgets.crd.yaml", "example.io", "Widget", "widgets", false),
			},
			wantSeverity: severityNonBlocking,
		},
		{
			name: "CR lower run-level than CRD (BLOCKING, the OCPBUGS-99266 shape)",
			fixtures: []fixtureManifest{
				crManifest("0000_05_config-operator_02_widget.cr.yaml", "example.io/v1", "Widget", "cluster"),
				crdManifest("0000_10_config-operator_01_widgets.crd.yaml", "example.io", "Widget", "widgets", false),
			},
			wantSeverity: severityBlocking,
		},
		{
			name: "CRD at lower run-level than CR (SAFE)",
			fixtures: []fixtureManifest{
				crdManifest("0000_05_config-operator_01_widgets.crd.yaml", "example.io", "Widget", "widgets", false),
				crManifest("0000_10_config-operator_02_widget.cr.yaml", "example.io/v1", "Widget", "cluster"),
			},
			wantSeverity: severitySafe,
		},
		{
			name: "single-token CR filename, CRD at lower run-level (SAFE)",
			fixtures: []fixtureManifest{
				crdManifest("0000_10_config-operator_01_widgets.crd.yaml", "example.io", "Widget", "widgets", false),
				crManifest("0000_90_openshift-widget-config.yaml", "example.io/v1", "Widget", "cluster"),
			},
			wantSeverity: severitySafe,
		},
		{
			name: "bootstrap-required CRD makes any-order CR safe (SAFE)",
			fixtures: []fixtureManifest{
				crManifest("0000_05_config-operator_02_widget.cr.yaml", "example.io/v1", "Widget", "cluster"),
				crdManifest("0000_10_config-operator_01_widgets.crd.yaml", "example.io", "Widget", "widgets", true),
			},
			wantSeverity: severitySafe,
		},
		{
			name: "same run-level, different component = parallel (NONBLOCKING)",
			fixtures: []fixtureManifest{
				crdManifest("0000_10_other-operator_01_widgets.crd.yaml", "example.io", "Widget", "widgets", false),
				crManifest("0000_10_config-operator_02_widget.cr.yaml", "example.io/v1", "Widget", "cluster"),
			},
			wantSeverity: severityNonBlocking,
		},
		{
			name: "CR whose CRD is not in the payload (not checked)",
			fixtures: []fixtureManifest{
				crManifest("0000_10_config-operator_02_widget.cr.yaml", "example.io/v1", "Widget", "cluster"),
			},
			wantSeverity: severitySafe,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := checkCRDOrdering(loadFixture(t, tc.fixtures))
			if tc.wantSeverity == severitySafe {
				if len(got) != 0 {
					t.Fatalf("expected no violation, got %d: %+v", len(got), got)
				}
				return
			}
			if len(got) != 1 {
				t.Fatalf("expected exactly one violation, got %d: %+v", len(got), got)
			}
			if got[0].severity != tc.wantSeverity {
				t.Fatalf("severity = %s, want %s", got[0].severity, tc.wantSeverity)
			}
		})
	}
}

// TestPayloadCRDOrdering runs the ordering check against a real, extracted
// release payload when CVO_PAYLOAD_MANIFEST_DIR points at a directory of payload
// manifests (as produced by `oc adm release extract --to=<dir> <release>`). It is
// skipped otherwise so the package's normal unit-test run stays hermetic; wire
// this into CI (e.g. an openshift/release step that extracts the payload) to get
// full-payload, cross-component coverage.
func TestPayloadCRDOrdering(t *testing.T) {
	dir := os.Getenv("CVO_PAYLOAD_MANIFEST_DIR")
	if dir == "" {
		t.Skip("set CVO_PAYLOAD_MANIFEST_DIR to an extracted payload dir to run the full-payload ordering check")
	}

	var all []orderingManifest
	files := 0
	err := filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		if !strings.HasSuffix(path, ".yaml") && !strings.HasSuffix(path, ".yml") {
			return nil
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		files++
		all = append(all, parseOrderingManifests(t, path, raw)...)
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", dir, err)
	}
	if files == 0 {
		t.Fatalf("no manifest files found under %s", dir)
	}
	t.Logf("checked %d manifest file(s) under %s", files, dir)

	blocking, acceptedBlocking, acceptedNonBlocking, nonBlocking := 0, 0, 0, 0
	for _, v := range checkCRDOrdering(all) {
		msg := fmt.Sprintf("[%s] CR %s.%s is applied before its CRD:\n  CR : %s\n  CRD: %s",
			v.severity, v.cr.group, v.cr.kind, v.cr.filename, v.crd.filename)
		switch v.severity {
		case severityBlocking:
			if reason, ok := knownAcceptedBlocking[v.cr.filename]; ok {
				acceptedBlocking++
				t.Logf("ACCEPTED (blocking): %s\n  reason: %s", msg, reason)
				continue
			}
			// CR run-level < CRD run-level: the CR's run-level can never complete
			// on the strict update path, deadlocking the upgrade. This is a hard
			// failure (the OCPBUGS-99266 class of bug).
			blocking++
			t.Errorf("%s\nThe CR's run-level is below its CRD's, so the update deadlocks. "+
				"Move the CR to a run-level at or above its CRD (and, at the same run-level, "+
				"to a filename that sorts after the CRD within the same component).", msg)
		case severityNonBlocking:
			if reason, ok := knownAcceptedNonBlocking[v.cr.filename]; ok {
				acceptedNonBlocking++
				t.Logf("ACCEPTED (non-blocking): %s\n  reason: %s", msg, reason)
				continue
			}
			// A newly-introduced same-run-level pair (or one whose run-level is
			// unparseable). Same run-level + different component means the CVO applies
			// the CR and CRD in parallel with no ordering edge; it self-heals via
			// re-apply but is a latent ordering hazard we refuse to add to the payload.
			// The long-standing pre-existing pairs are grandfathered in
			// knownAcceptedNonBlocking; a genuinely new pair fails here.
			nonBlocking++
			t.Errorf("%s\nThe CR shares a run-level with its CRD but is not guaranteed to apply "+
				"after it (different component => parallel apply, or same component but the CR "+
				"filename sorts before the CRD). Give the CRD a lower run-level, or place the CR "+
				"after the CRD within the same component. If this pair is genuinely safe (e.g. the "+
				"CRD pre-exists from every supported prior release), add it to "+
				"knownAcceptedNonBlocking with justification.", msg)
		}
	}
	t.Logf("ordering summary: %d blocking, %d accepted-blocking, %d accepted-non-blocking, %d non-blocking",
		blocking, acceptedBlocking, acceptedNonBlocking, nonBlocking)
}

// knownAcceptedBlocking lists CR manifests whose run-level is strictly below
// their CRD's, but which do not deadlock real clusters because the CRD is
// present in every supported prior release (so it already exists on the upgrade
// path) and fresh installs use the non-strict InitializingPayload path. Entries
// must be justified; a genuinely new CRD introduced below its CR (the
// OCPBUGS-99266 shape) must NOT be added here.
var knownAcceptedBlocking = map[string]string{
	"0000_10_config-operator_03_servicemonitor.yaml": "the monitoring.coreos.com ServiceMonitor CRD ships with cluster-monitoring-operator in every supported release, so it pre-exists on the upgrade path",
}

// Reasons shared by the grandfathered same-run-level pairs below.
const (
	// reasonMonitoringParallel covers ServiceMonitor/PrometheusRule CRs. The
	// monitoring.coreos.com CRDs ship with cluster-monitoring-operator at
	// run-level 0000_50 in every supported prior release, so the CRD pre-exists
	// on the upgrade path; and being a different component at the same run-level,
	// the CVO applies them in parallel, so a first-pass failure self-heals via
	// re-apply within the run-level barrier.
	reasonMonitoringParallel = "monitoring.coreos.com CRD ships with cluster-monitoring-operator at the same run-level and pre-exists from prior releases; different component => parallel apply, self-heals via re-apply"
	// reasonOperatorConfigParallel covers operator/config CRs whose CRD is
	// generated by openshift/api at the same run-level. Different component at the
	// same run-level => the CVO applies them in parallel with no ordering edge; a
	// first-pass failure self-heals via re-apply. Long-standing; the fix would
	// span openshift/api plus the individual operator repos (see OCPBUGS-99266
	// discussion), so these are grandfathered rather than blocked.
	reasonOperatorConfigParallel = "CRD is generated by openshift/api at the same run-level but a different component => parallel apply, self-heals via re-apply; cleanup would span openshift/api and the owning operator repo"
)

// knownAcceptedNonBlocking grandfathers the same-run-level CR-before-CRD pairs
// that already ship in the payload. These do not deadlock (same run-level =>
// the failed CR re-applies once its CRD is established later in the same
// run-level), but they are latent ordering hazards, so any *new* same-run-level
// pair fails TestPayloadCRDOrdering. Entries should be removed as the owning
// repos are cleaned up. Keyed by CR filename (a single file may contribute more
// than one CR, e.g. a ServiceMonitor and a PrometheusRule; one entry covers all
// CRs in that file). Baseline captured from the 4.22.9 payload.
var knownAcceptedNonBlocking = map[string]string{
	// operator/config CRs whose CRD is generated by openshift/api.
	"0000_03_marketplace-operator_02_operatorhub.cr.yaml":                     reasonOperatorConfigParallel,
	"0000_10_openshift-controller-manager-operator_02_build_cr.yaml":          reasonOperatorConfigParallel,
	"0000_20_kube-apiserver-operator_01_operator.cr.yaml":                     reasonOperatorConfigParallel,
	"0000_25_kube-controller-manager-operator_01_operator.cr.yaml":            reasonOperatorConfigParallel,
	"0000_25_kube-scheduler-operator_02_operator.cr.yaml":                     reasonOperatorConfigParallel,
	"0000_30_cluster-api-installer_06_clusterapi.yaml":                        reasonOperatorConfigParallel,
	"0000_50_cluster-authentication-operator_02_config.cr.yaml":               reasonOperatorConfigParallel,
	"0000_50_cluster-openshift-controller-manager-operator_03_config.cr.yaml": reasonOperatorConfigParallel,
	"0000_50_cluster-storage-operator_06_operator_cr.yaml":                    reasonOperatorConfigParallel,
	"0000_50_cluster-storage-operator_06_operator_cr-hypershift.yaml":         reasonOperatorConfigParallel,
	"0000_50_console-operator_01-operator-config.yaml":                        reasonOperatorConfigParallel,

	// ServiceMonitor / PrometheusRule CRs (monitoring.coreos.com).
	"0000_50_cluster-autoscaler-operator_06_servicemonitor.yaml":                           reasonMonitoringParallel,
	"0000_50_cluster-image-registry-operator_09-prometheus-rules-imagestreams.yaml":        reasonMonitoringParallel,
	"0000_50_cluster-image-registry-operator_09-prometheus-rules-registry-operations.yaml": reasonMonitoringParallel,
	"0000_50_cluster-image-registry-operator_09-prometheus-rules.yaml":                     reasonMonitoringParallel,
	"0000_50_cluster-network-operator_06-servicemonitor.yaml":                              reasonMonitoringParallel,
	"0000_50_cluster-node-tuning-operator_30-monitoring.yaml":                              reasonMonitoringParallel,
	"0000_50_cluster-samples-operator_010-prometheus-rules.yaml":                           reasonMonitoringParallel,
	"0000_50_cluster-samples-operator_06-servicemonitor.yaml":                              reasonMonitoringParallel,
	"0000_50_cluster-storage-operator_12_prometheusrules.yaml":                             reasonMonitoringParallel,
	"0000_50_console-operator_cluster-monitoring-prometheus-rules.yaml":                    reasonMonitoringParallel,
	"0000_50_insights-operator_09-servicemonitor.yaml":                                     reasonMonitoringParallel,
	"0000_50_olm_06-psm-operator.servicemonitor.yaml":                                      reasonMonitoringParallel,
	"0000_50_operator-marketplace_11_service_monitor.yaml":                                 reasonMonitoringParallel,
	"0000_50_operator-marketplace_12_prometheus_rule.yaml":                                 reasonMonitoringParallel,
}

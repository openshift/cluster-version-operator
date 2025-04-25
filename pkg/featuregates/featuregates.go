package featuregates

import (
	configv1 "github.com/openshift/api/config/v1"
	"github.com/openshift/api/features"
)

// CvoGateChecker allows CVO code to check which feature gates are enabled
type CvoGateChecker interface {
	// UnknownVersion flag is set to true if CVO did not find a matching version in the FeatureGate
	// status resource, meaning the current set of enabled and disabled feature gates is unknown for
	// this version. This should be a temporary state (config-operator should eventually add the
	// enabled/disabled flags for this version), so CVO should try to behave in a way that reflects
	// a "good default": default-on flags are enabled, default-off flags are disabled. Where reasonable,
	// It can also attempt to tolerate the existing state: if it finds evidence that a feature was
	// enabled, it can continue to behave as if it was enabled and vice versa. This temporary state
	// should be eventually resolved when the FeatureGate status resource is updated, which forces CVO
	// to restart when the flags change.
	UnknownVersion() bool

	// ReconciliationIssuesCondition controls whether CVO maintains a Condition with
	// ReconciliationIssues type, containing a JSON that describes all "issues" that prevented
	// or delayed CVO from reconciling individual resources in the cluster. This is a pseudo-API
	// that the experimental work for "oc adm upgrade status" uses to report upgrade status, and
	// should never be relied upon by any production code. We may want to eventually turn this into
	// some kind of "real" API.
	ReconciliationIssuesCondition() bool

	// StatusReleaseArchitecture controls whether CVO populates
	// Release.Architecture in status properties like status.desired and status.history[].
	StatusReleaseArchitecture() bool

	// CVOConfiguration controls whether the CVO reconciles the ClusterVersionOperator resource that corresponds
	// to its configuration.
	CVOConfiguration() bool
}

// CvoGates contains flags that control CVO functionality gated by product feature gates. The
// names do not correspond to product feature gates, the booleans here are "smaller" (product-level
// gate will enable multiple CVO behaviors).
type CvoGates struct {
	// desiredVersion stores the currently executing version of CVO, for which these feature gates
	// are relevant
	desiredVersion string

	// individual flags mirror the CvoGateChecker interface
	unknownVersion                bool
	reconciliationIssuesCondition bool
	statusReleaseArchitecture     bool
	cvoConfiguration              bool
}

func (c CvoGates) ReconciliationIssuesCondition() bool {
	return c.reconciliationIssuesCondition
}

func (c CvoGates) StatusReleaseArchitecture() bool {
	return c.statusReleaseArchitecture
}

func (c CvoGates) UnknownVersion() bool {
	return c.unknownVersion
}

func (c CvoGates) CVOConfiguration() bool {
	return c.cvoConfiguration
}

// DefaultCvoGates apply when actual features for given version are unknown
func DefaultCvoGates(version string) CvoGates {
	return CvoGates{
		desiredVersion:                version,
		unknownVersion:                true,
		reconciliationIssuesCondition: false,
		statusReleaseArchitecture:     false,
		cvoConfiguration:              false,
	}
}

// CvoGatesFromFeatureGate finds feature gates for a given version in a FeatureGate resource and returns
// CvoGates that reflects them, or the default gates if given version was not found in the FeatureGate
func CvoGatesFromFeatureGate(gate *configv1.FeatureGate, version string) CvoGates {
	enabledGates := DefaultCvoGates(version)

	for _, g := range gate.Status.FeatureGates {

		if g.Version != version {
			continue
		}
		// We found the matching version, so we do not need to run in the unknown version mode
		enabledGates.unknownVersion = false
		for _, enabled := range g.Enabled {
			switch enabled.Name {
			case features.FeatureGateUpgradeStatus:
				enabledGates.reconciliationIssuesCondition = true
			case features.FeatureGateImageStreamImportMode:
				enabledGates.statusReleaseArchitecture = true
			case features.FeatureGateCVOConfiguration:
				enabledGates.cvoConfiguration = true
			}
		}
		for _, disabled := range g.Disabled {
			switch disabled.Name {
			case features.FeatureGateUpgradeStatus:
				enabledGates.reconciliationIssuesCondition = false
			case features.FeatureGateImageStreamImportMode:
				enabledGates.statusReleaseArchitecture = false
			case features.FeatureGateCVOConfiguration:
				enabledGates.cvoConfiguration = false
			}
		}
	}

	return enabledGates
}

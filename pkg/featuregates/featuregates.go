package featuregates

import (
	configv1 "github.com/openshift/api/config/v1"
	"github.com/openshift/api/features"
)

// StubOpenShiftVersion is the default OpenShift version placeholder for the purpose of determining
// enabled and disabled CVO feature gates. It is assumed to never conflict with a real OpenShift
// version. Both DefaultCvoGates and CvoGatesFromFeatureGate should return a safe conservative
// default set of enabled and disabled gates when StubOpenShiftVersion is passed as the version.
// This value is an analogue to the "0.0.1-snapshot" string that is used as a placeholder in
// second-level operator manifests. Here we use the same string for consistency, but the constant
// should be only used by the callers of CvoGatesFromFeatureGate and DefaultCvoGates methods when
// the caller does not know its actual OpenShift version.
const StubOpenShiftVersion = "0.0.1-snapshot"

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

	// StatusReleaseArchitecture controls whether CVO populates
	// Release.Architecture in status properties like status.desired and status.history[].
	StatusReleaseArchitecture() bool

	// CVOConfiguration controls whether the CVO reconciles the ClusterVersionOperator resource that corresponds
	// to its configuration.
	CVOConfiguration() bool

	// AcceptRisks controls whether the CVO reconciles spec.desiredUpdate.acceptRisks.
	AcceptRisks() bool
}

// CvoGates contains flags that control CVO functionality gated by product feature gates. The
// names do not correspond to product feature gates, the booleans here are "smaller" (product-level
// gate will enable multiple CVO behaviors).
type CvoGates struct {
	// desiredVersion stores the currently executing version of CVO, for which these feature gates
	// are relevant
	desiredVersion string

	// individual flags mirror the CvoGateChecker interface
	unknownVersion            bool
	statusReleaseArchitecture bool
	cvoConfiguration          bool
	acceptRisks               bool
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

func (c CvoGates) AcceptRisks() bool {
	return c.acceptRisks
}

// DefaultCvoGates apply when actual features for given version are unknown
func DefaultCvoGates(version string) CvoGates {
	return CvoGates{
		desiredVersion:            version,
		unknownVersion:            true,
		statusReleaseArchitecture: false,
		cvoConfiguration:          false,
		acceptRisks:               false,
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
			case features.FeatureGateImageStreamImportMode:
				enabledGates.statusReleaseArchitecture = true
			case features.FeatureGateCVOConfiguration:
				enabledGates.cvoConfiguration = true
			case features.FeatureGateClusterUpdateAcceptRisks:
				enabledGates.acceptRisks = true
			}
		}
		for _, disabled := range g.Disabled {
			switch disabled.Name {
			case features.FeatureGateImageStreamImportMode:
				enabledGates.statusReleaseArchitecture = false
			case features.FeatureGateCVOConfiguration:
				enabledGates.cvoConfiguration = false
			case features.FeatureGateClusterUpdateAcceptRisks:
				enabledGates.acceptRisks = false
			}
		}
	}

	return enabledGates
}

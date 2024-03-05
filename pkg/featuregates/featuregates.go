package featuregates

import (
	configv1 "github.com/openshift/api/config/v1"
	"k8s.io/klog/v2"
)

// CvoGates contains flags that control CVO functionality gated by product feature gates. The
// names do not correspond to product feature gates, the booleans here are "smaller" (product-level
// gate will enable multiple CVO behaviors).
type CvoGates struct {

	// UnknownVersion flag is set to true if CVO did not find a matching version in the FeatureGate
	// status resource, meaning the current set of enabled and disabled feature gates is unknown for
	// this version. This should be a temporary state (config-operator should eventually add the
	// enabled/disabled flags for this version), so CVO should try to behave in a way that reflects
	// a "good default": default-on flags are enabled, default-off flags are disabled. Where reasonable,
	// It can also attempt to tolerate the existing state: if it finds evidence that a feature was
	// enabled, it can continue to behave as if it was enabled and vice versa. This temporary state
	// should be eventually resolved when the FeatureGate status resource is updated, which forces CVO
	// to restart when the flags change.
	UnknownVersion bool

	// ResourceReconciliationIssuesCondition controls whether CVO maintains a Condition with
	// ResourceReconciliationIssues type, containing a JSON that describes all "issues" that prevented
	// or delayed CVO from reconciling individual resources in the cluster. This is a pseudo-API
	// that the experimental work for "oc adm upgrade status" uses to report upgrade status, and
	// should never be relied upon by any production code. We may want to eventually turn this into
	// some kind of "real" API.
	ResourceReconciliationIssuesCondition bool
}

type ClusterFeatures struct {
	StartingRequiredFeatureSet string

	// StartingCvoFeatureGates control gated CVO functionality (not gated cluster functionality)
	StartingCvoFeatureGates CvoGates
	VersionForGates         string
}

func DefaultClusterFeatures(version string) ClusterFeatures {
	return ClusterFeatures{
		// check to see if techpreview should be on or off.  If we cannot read the featuregate for any reason, it is assumed
		// to be off.  If this value changes, the binary will shutdown and expect the pod lifecycle to restart it.
		StartingRequiredFeatureSet: "",
		StartingCvoFeatureGates:    defaultGatesWhenUnknown(),
		VersionForGates:            version,
	}
}

func ClusterFeaturesFromFeatureGate(gate *configv1.FeatureGate, version string) ClusterFeatures {
	return ClusterFeatures{
		StartingRequiredFeatureSet: string(gate.Spec.FeatureSet),
		StartingCvoFeatureGates:    getCvoGatesFrom(gate, version),
		VersionForGates:            version,
	}
}

func defaultGatesWhenUnknown() CvoGates {
	return CvoGates{
		UnknownVersion: true,

		ResourceReconciliationIssuesCondition: false,
	}
}

func getCvoGatesFrom(gate *configv1.FeatureGate, version string) CvoGates {
	enabledGates := defaultGatesWhenUnknown()
	klog.Infof("Looking up feature gates for version %s", version)
	for _, g := range gate.Status.FeatureGates {

		if g.Version != version {
			continue
		}
		// We found the matching version, so we do not need to run in the unknown version mode
		enabledGates.UnknownVersion = false
		for _, enabled := range g.Enabled {
			if enabled.Name == configv1.FeatureGateUpgradeStatus {
				enabledGates.ResourceReconciliationIssuesCondition = true
			}
		}
	}

	return enabledGates
}

package capability

import (
	"fmt"
	"reflect"

	"k8s.io/apimachinery/pkg/util/sets"

	configv1 "github.com/openshift/api/config/v1"
	"github.com/openshift/api/features"
)

// featureGatedCapabilities maps each gated capability to the feature gates
// that can make it available. A capability is included when ANY of its listed
// gates is present in the enabled set. This mirrors the FeatureGateAwareEnum
// annotations on ClusterVersionCapability in the openshift/api types.
var featureGatedCapabilities = map[configv1.ClusterVersionCapability][]configv1.FeatureGateName{
	configv1.ClusterVersionCapabilityCompatibilityRequirements: {
		features.FeatureGateCRDCompatibilityRequirementOperator,
		features.FeatureGateClusterAPIMachineManagement,
	},
	configv1.ClusterVersionCapabilityClusterAPI: {
		features.FeatureGateClusterAPIMachineManagement,
	},
}

// excludedCapabilities returns the set of capabilities that should be excluded
// because none of their enabling feature gates are in enabledFeatureGates.
func excludedCapabilities(enabledFeatureGates sets.Set[string]) sets.Set[configv1.ClusterVersionCapability] {
	excluded := sets.New[configv1.ClusterVersionCapability]()
	for cap, gates := range featureGatedCapabilities {
		gateStrings := make([]string, len(gates))
		for i, g := range gates {
			gateStrings[i] = string(g)
		}
		if enabledFeatureGates.Intersection(sets.New[string](gateStrings...)).Len() == 0 {
			excluded.Insert(cap)
		}
	}
	return excluded
}

type ClusterCapabilities struct {
	Known             sets.Set[configv1.ClusterVersionCapability]
	Enabled           sets.Set[configv1.ClusterVersionCapability]
	ImplicitlyEnabled sets.Set[configv1.ClusterVersionCapability]
}

func (c *ClusterCapabilities) Equal(capabilities *ClusterCapabilities) error {
	if !reflect.DeepEqual(c.Enabled, capabilities.Enabled) {
		return fmt.Errorf("enabled %v not equal to %v", c.Enabled, capabilities.Enabled)
	}

	return nil
}

// SortedList returns the slice with contents in sorted order for a given set of configv1.ClusterVersionCapability.
// It returns nil if the give set is nil.
func SortedList(s sets.Set[configv1.ClusterVersionCapability]) []configv1.ClusterVersionCapability {
	if s == nil {
		return nil
	}
	return sets.List(s)
}

// SetCapabilities populates and returns cluster capabilities from ClusterVersion's capabilities specification and a
// collection of capabilities that are enabled including implicitly enabled.
// It ensures that each capability in the collection is still enabled in the returned cluster capabilities
// and recognizes all implicitly enabled ones. Capabilities gated behind feature gates that are not in
// enabledFeatureGates are excluded from Known, Enabled, and ImplicitlyEnabled sets.
func SetCapabilities(config *configv1.ClusterVersion,
	capabilities []configv1.ClusterVersionCapability,
	enabledFeatureGates sets.Set[string]) ClusterCapabilities {

	excluded := excludedCapabilities(enabledFeatureGates)

	var clusterCapabilities ClusterCapabilities
	clusterCapabilities.Known = sets.New[configv1.ClusterVersionCapability](configv1.KnownClusterVersionCapabilities...).Difference(excluded)

	clusterCapabilities.Enabled, clusterCapabilities.ImplicitlyEnabled =
		categorizeEnabledCapabilities(config.Spec.Capabilities, capabilities)

	clusterCapabilities.Enabled = differenceOrNil(clusterCapabilities.Enabled, excluded)
	clusterCapabilities.ImplicitlyEnabled = differenceOrNil(clusterCapabilities.ImplicitlyEnabled, excluded)

	return clusterCapabilities
}

// GetCapabilitiesAsMap returns the slice of capabilities as a map with default values.
func GetCapabilitiesAsMap(capabilities []configv1.ClusterVersionCapability) map[configv1.ClusterVersionCapability]struct{} {
	caps := make(map[configv1.ClusterVersionCapability]struct{}, len(capabilities))
	for _, c := range capabilities {
		caps[c] = struct{}{}
	}
	return caps
}

// SetFromImplicitlyEnabledCapabilities, given implicitly enabled capabilities and cluster capabilities, updates
// the latter with the given implicitly enabled capabilities and ensures each is in the enabled map. The updated
// cluster capabilities are returned.
func SetFromImplicitlyEnabledCapabilities(implicitlyEnabled []configv1.ClusterVersionCapability,
	capabilities ClusterCapabilities) ClusterCapabilities {

	if implicitlyEnabled == nil {
		capabilities.ImplicitlyEnabled = nil
	} else {
		capabilities.ImplicitlyEnabled = sets.New[configv1.ClusterVersionCapability](implicitlyEnabled...)
	}

	for _, c := range implicitlyEnabled {
		if _, ok := capabilities.Enabled[c]; !ok {
			capabilities.Enabled[c] = struct{}{}
		}
	}
	return capabilities
}

// GetCapabilitiesStatus populates and returns ClusterVersion capabilities status from given capabilities.
func GetCapabilitiesStatus(capabilities ClusterCapabilities) configv1.ClusterVersionCapabilitiesStatus {
	var status configv1.ClusterVersionCapabilitiesStatus
	status.EnabledCapabilities = SortedList(capabilities.Enabled)
	status.KnownCapabilities = SortedList(capabilities.Known)
	return status
}

func differenceOrNil(current, excluded sets.Set[configv1.ClusterVersionCapability]) sets.Set[configv1.ClusterVersionCapability] {
	if current == nil {
		return nil
	}
	ret := current.Difference(excluded)
	if ret.Len() == 0 {
		return nil
	}
	return ret
}

// categorizeEnabledCapabilities categorizes enabled capabilities by implicitness from cluster version's
// capabilities specification and a collection of capabilities that are enabled including implicitly enabled.
func categorizeEnabledCapabilities(capabilitiesSpec *configv1.ClusterVersionCapabilitiesSpec,
	capabilities []configv1.ClusterVersionCapability) (sets.Set[configv1.ClusterVersionCapability],
	sets.Set[configv1.ClusterVersionCapability]) {

	capSet := configv1.ClusterVersionCapabilitySetCurrent

	if capabilitiesSpec != nil && len(capabilitiesSpec.BaselineCapabilitySet) > 0 {
		capSet = capabilitiesSpec.BaselineCapabilitySet
	}
	enabled := sets.New[configv1.ClusterVersionCapability](configv1.ClusterVersionCapabilitySets[capSet]...)

	if capabilitiesSpec != nil {
		enabled.Insert(capabilitiesSpec.AdditionalEnabledCapabilities...)
	}
	implicitlyEnabled := sets.New[configv1.ClusterVersionCapability]()
	for _, k := range capabilities {
		if !enabled.Has(k) {
			implicitlyEnabled.Insert(k)
			enabled.Insert(k)
		}
	}
	if implicitlyEnabled.Len() == 0 {
		implicitlyEnabled = nil
	}
	return enabled, implicitlyEnabled
}

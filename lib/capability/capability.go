package capability

import (
	"reflect"
	"sort"

	configv1 "github.com/openshift/api/config/v1"
)

const (
	CapabilityAnnotation = "capability.openshift.io/name"

	DefaultCapabilitySet = configv1.ClusterVersionCapabilitySetCurrent
)

type ClusterCapabilities struct {
	KnownCapabilities             map[configv1.ClusterVersionCapability]struct{}
	EnabledCapabilities           map[configv1.ClusterVersionCapability]struct{}
	ImplicitlyEnabledCapabilities []configv1.ClusterVersionCapability
}

func (c *ClusterCapabilities) Equal(capabilities *ClusterCapabilities) bool {
	return reflect.DeepEqual(c.EnabledCapabilities, capabilities.EnabledCapabilities)
}

type capabilitiesSort []configv1.ClusterVersionCapability

func (caps capabilitiesSort) Len() int           { return len(caps) }
func (caps capabilitiesSort) Swap(i, j int)      { caps[i], caps[j] = caps[j], caps[i] }
func (caps capabilitiesSort) Less(i, j int) bool { return string(caps[i]) < string(caps[j]) }

// SetCapabilities populates and returns cluster capabilities from ClusterVersion capabilities spec. This method also
// ensures that no previousily enabled capability is now disabled and returns any such implicitly enabled capabilities.
func SetCapabilities(config *configv1.ClusterVersion,
	existingEnabled map[configv1.ClusterVersionCapability]struct{}) ClusterCapabilities {

	var capabilities ClusterCapabilities
	capabilities.KnownCapabilities = setKnownCapabilities()

	capabilities.EnabledCapabilities, capabilities.ImplicitlyEnabledCapabilities = setEnabledCapabilities(config.Spec.Capabilities,
		existingEnabled)

	return capabilities
}

// GetKnownCapabilities returns all known capabilities as defined in ClusterVersion.
func GetKnownCapabilities() []configv1.ClusterVersionCapability {
	var known []configv1.ClusterVersionCapability

	for _, v := range configv1.ClusterVersionCapabilitySets {
		known = append(known, v...)
	}
	return known
}

// GetCapabilitiesStatus populates and returns ClusterVersion capabilities status from given capabilities.
func GetCapabilitiesStatus(capabilities ClusterCapabilities) configv1.ClusterVersionCapabilitiesStatus {
	var status configv1.ClusterVersionCapabilitiesStatus
	for k := range capabilities.EnabledCapabilities {
		status.EnabledCapabilities = append(status.EnabledCapabilities, k)
	}
	sort.Sort(capabilitiesSort(status.EnabledCapabilities))
	for k := range capabilities.KnownCapabilities {
		status.KnownCapabilities = append(status.KnownCapabilities, k)
	}
	sort.Sort(capabilitiesSort(status.KnownCapabilities))
	return status
}

// setKnownCapabilities populates a map keyed by capability from all known capabilities as defined in ClusterVersion.
func setKnownCapabilities() map[configv1.ClusterVersionCapability]struct{} {
	known := make(map[configv1.ClusterVersionCapability]struct{})

	for _, v := range configv1.ClusterVersionCapabilitySets {
		for _, capability := range v {
			if _, ok := known[capability]; ok {
				continue
			}
			known[capability] = struct{}{}
		}
	}
	return known
}

// setEnabledCapabilities populates a map keyed by capability from all enabled capabilities as defined in ClusterVersion.
// DefaultCapabilitySet is used if a baseline capability set is not defined by ClusterVersion. A check is then made to
// ensure that no previousily enabled capability is now disabled and if any such capabilities are found each is enabled,
// saved, and returned.
func setEnabledCapabilities(capabilitiesSpec *configv1.ClusterVersionCapabilitiesSpec,
	priorEnabled map[configv1.ClusterVersionCapability]struct{}) (map[configv1.ClusterVersionCapability]struct{},
	[]configv1.ClusterVersionCapability) {

	enabled := make(map[configv1.ClusterVersionCapability]struct{})

	capSet := DefaultCapabilitySet

	if capabilitiesSpec != nil && len(capabilitiesSpec.BaselineCapabilitySet) > 0 {
		capSet = capabilitiesSpec.BaselineCapabilitySet
	}

	for _, v := range configv1.ClusterVersionCapabilitySets[capSet] {
		enabled[v] = struct{}{}
	}

	if capabilitiesSpec != nil {
		for _, v := range capabilitiesSpec.AdditionalEnabledCapabilities {
			if _, ok := enabled[v]; ok {
				continue
			}
			enabled[v] = struct{}{}
		}
	}
	var implicitlyEnabled []configv1.ClusterVersionCapability
	for k := range priorEnabled {
		if _, ok := enabled[k]; !ok {
			implicitlyEnabled = append(implicitlyEnabled, k)
			enabled[k] = struct{}{}
		}
	}
	return enabled, implicitlyEnabled
}

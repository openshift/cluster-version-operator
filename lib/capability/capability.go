package capability

import (
	"fmt"
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

func (c *ClusterCapabilities) Equal(capabilities *ClusterCapabilities) error {
	if !reflect.DeepEqual(c.EnabledCapabilities, capabilities.EnabledCapabilities) {
		return fmt.Errorf("enabled %v not equal to %v", c.EnabledCapabilities, capabilities.EnabledCapabilities)
	}

	return nil
}

type capabilitiesSort []configv1.ClusterVersionCapability

func (caps capabilitiesSort) Len() int           { return len(caps) }
func (caps capabilitiesSort) Swap(i, j int)      { caps[i], caps[j] = caps[j], caps[i] }
func (caps capabilitiesSort) Less(i, j int) bool { return string(caps[i]) < string(caps[j]) }

// SetCapabilities populates and returns cluster capabilities from ClusterVersion capabilities spec. This method also
// ensures that no previousily enabled capability is now disabled and returns any such implicitly enabled capabilities.
func SetCapabilities(config *configv1.ClusterVersion,
	existingEnabled, alwaysEnabled map[configv1.ClusterVersionCapability]struct{}) ClusterCapabilities {

	var capabilities ClusterCapabilities
	capabilities.KnownCapabilities = setKnownCapabilities()

	capabilities.EnabledCapabilities, capabilities.ImplicitlyEnabledCapabilities = setEnabledCapabilities(config.Spec.Capabilities,
		existingEnabled, alwaysEnabled)

	return capabilities
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

	capabilities.ImplicitlyEnabledCapabilities = implicitlyEnabled
	for _, c := range implicitlyEnabled {
		if _, ok := capabilities.EnabledCapabilities[c]; !ok {
			capabilities.EnabledCapabilities[c] = struct{}{}
		}
	}
	return capabilities
}

// GetKnownCapabilities returns all known capabilities as defined in ClusterVersion.
func GetKnownCapabilities() []configv1.ClusterVersionCapability {
	var known []configv1.ClusterVersionCapability

	for _, v := range configv1.ClusterVersionCapabilitySets {
		known = append(known, v...)
	}
	sort.Sort(capabilitiesSort(known))
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

// GetImplicitlyEnabledCapabilities, given an enabled resource's current capabilities, compares them against
// the resource's capabilities from an update release. Any of the updated resource's capabilities that do not
// exist in the current resource, are not enabled, and do not already exist in the implicitly enabled list of
// capabilities are returned. The returned list are capabilities which must be implicitly enabled.
func GetImplicitlyEnabledCapabilities(enabledManifestCaps []configv1.ClusterVersionCapability,
	updatedManifestCaps []configv1.ClusterVersionCapability,
	capabilities ClusterCapabilities) []configv1.ClusterVersionCapability {

	var caps []configv1.ClusterVersionCapability
	for _, c := range updatedManifestCaps {
		if Contains(enabledManifestCaps, c) {
			continue
		}
		if _, ok := capabilities.EnabledCapabilities[c]; !ok {
			if !Contains(capabilities.ImplicitlyEnabledCapabilities, c) {
				caps = append(caps, c)
			}
		}
	}
	sort.Sort(capabilitiesSort(caps))
	return caps
}

func Contains(caps []configv1.ClusterVersionCapability, capability configv1.ClusterVersionCapability) bool {
	found := false
	for _, c := range caps {
		if capability == c {
			found = true
			break
		}
	}
	return found
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
// The required capabilities are added to the implicitly enabled.
func setEnabledCapabilities(capabilitiesSpec *configv1.ClusterVersionCapabilitiesSpec,
	priorEnabled, alwaysEnabled map[configv1.ClusterVersionCapability]struct{}) (map[configv1.ClusterVersionCapability]struct{},
	[]configv1.ClusterVersionCapability) {

	capSet := DefaultCapabilitySet

	if capabilitiesSpec != nil && len(capabilitiesSpec.BaselineCapabilitySet) > 0 {
		capSet = capabilitiesSpec.BaselineCapabilitySet
	}
	enabled := GetCapabilitiesAsMap(configv1.ClusterVersionCapabilitySets[capSet])

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
	for k := range alwaysEnabled {
		if _, ok := enabled[k]; !ok {
			implicitlyEnabled = append(implicitlyEnabled, k)
			enabled[k] = struct{}{}
		}
	}
	sort.Sort(capabilitiesSort(implicitlyEnabled))
	return enabled, implicitlyEnabled
}

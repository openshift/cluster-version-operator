package capability

import (
	"fmt"
	"reflect"
	"sort"

	configv1 "github.com/openshift/api/config/v1"
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

// SetCapabilities populates and returns cluster capabilities from ClusterVersion's capabilities specification and a
// collection of capabilities that are enabled including implicitly enabled.
// It ensures that each capability in the collection is still enabled in the returned cluster capabilities
// and recognizes all implicitly enabled ones.
func SetCapabilities(config *configv1.ClusterVersion,
	capabilities []configv1.ClusterVersionCapability) ClusterCapabilities {

	var clusterCapabilities ClusterCapabilities
	clusterCapabilities.KnownCapabilities = GetCapabilitiesAsMap(configv1.KnownClusterVersionCapabilities)

	clusterCapabilities.EnabledCapabilities, clusterCapabilities.ImplicitlyEnabledCapabilities =
		categorizeEnabledCapabilities(config.Spec.Capabilities, capabilities)

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

	capabilities.ImplicitlyEnabledCapabilities = implicitlyEnabled
	for _, c := range implicitlyEnabled {
		if _, ok := capabilities.EnabledCapabilities[c]; !ok {
			capabilities.EnabledCapabilities[c] = struct{}{}
		}
	}
	return capabilities
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

// categorizeEnabledCapabilities categorizes enabled capabilities by implicitness from cluster version's
// capabilities specification and a collection of capabilities that are enabled including implicitly enabled.
func categorizeEnabledCapabilities(capabilitiesSpec *configv1.ClusterVersionCapabilitiesSpec,
	capabilities []configv1.ClusterVersionCapability) (map[configv1.ClusterVersionCapability]struct{},
	[]configv1.ClusterVersionCapability) {

	capSet := configv1.ClusterVersionCapabilitySetCurrent

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
	for _, k := range capabilities {
		if _, ok := enabled[k]; !ok {
			implicitlyEnabled = append(implicitlyEnabled, k)
			enabled[k] = struct{}{}
		}
	}
	sort.Sort(capabilitiesSort(implicitlyEnabled))
	return enabled, implicitlyEnabled
}

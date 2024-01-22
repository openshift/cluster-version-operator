package capability

import (
	"fmt"
	"reflect"
	"sort"

	configv1 "github.com/openshift/api/config/v1"
	"k8s.io/apimachinery/pkg/util/sets"
)

const (
	DefaultCapabilitySet = configv1.ClusterVersionCapabilitySetCurrent
)

type ClusterCapabilities struct {
	Known             sets.Set[configv1.ClusterVersionCapability]
	Enabled           sets.Set[configv1.ClusterVersionCapability]
	ImplicitlyEnabled sets.Set[configv1.ClusterVersionCapability] // TODO(muller): Get rid of this slice and use a set instead
}

func (c *ClusterCapabilities) Equal(capabilities *ClusterCapabilities) error {
	// TODO: Replace this with a set equivalences
	if !reflect.DeepEqual(c.Enabled, capabilities.Enabled) {
		return fmt.Errorf("enabled %v not equal to %v", c.Enabled, capabilities.Enabled)
	}

	return nil
}

type capabilitiesSort []configv1.ClusterVersionCapability

func (caps capabilitiesSort) Len() int           { return len(caps) }
func (caps capabilitiesSort) Swap(i, j int)      { caps[i], caps[j] = caps[j], caps[i] }
func (caps capabilitiesSort) Less(i, j int) bool { return string(caps[i]) < string(caps[j]) }

// SetCapabilities populates and returns cluster capabilities from ClusterVersion capabilities spec. This method also
// ensures that no previously enabled capability is now disabled and returns any such implicitly enabled capabilities.
func SetCapabilities(config *configv1.ClusterVersion, existingEnabled sets.Set[configv1.ClusterVersionCapability]) ClusterCapabilities { // TODO(muller): existingEnabled is a set, method is a get
	enabled, implicitlyEnabled := getEnabledCapabilities(config.Spec.Capabilities, existingEnabled)

	return ClusterCapabilities{
		Known:             allKnownCapabilities(),
		Enabled:           enabled,
		ImplicitlyEnabled: implicitlyEnabled,
	}
}

// SetFromImplicitlyEnabledCapabilities, given implicitly enabled capabilities and cluster capabilities, updates
// the latter with the given implicitly enabled capabilities and ensures each is in the enabled map. The updated
// cluster capabilities are returned.
func SetFromImplicitlyEnabledCapabilities(implicitlyEnabled []configv1.ClusterVersionCapability, capabilities ClusterCapabilities) ClusterCapabilities {
	if len(implicitlyEnabled) > 0 {
		capabilities.ImplicitlyEnabled = sets.New[configv1.ClusterVersionCapability](implicitlyEnabled...)
	} else {
		capabilities.ImplicitlyEnabled = nil
	}
	for _, c := range implicitlyEnabled {
		if _, ok := capabilities.Enabled[c]; !ok {
			capabilities.Enabled[c] = struct{}{}
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
	for k := range capabilities.Enabled {
		status.EnabledCapabilities = append(status.EnabledCapabilities, k)
	}
	sort.Sort(capabilitiesSort(status.EnabledCapabilities))
	for k := range capabilities.Known {
		status.KnownCapabilities = append(status.KnownCapabilities, k)
	}
	sort.Sort(capabilitiesSort(status.KnownCapabilities))
	return status
}

// GetImplicitlyEnabledCapabilities, given an enabled resource's current capabilities, compares them against
// the resource's capabilities from an update release. Any of the updated resource's capabilities that do not
// exist in the current resource, are not enabled, and do not already exist in the implicitly enabled list of
// capabilities are returned. The returned list are capabilities which must be implicitly enabled.
// TODO(muller): enabledManifestCaps is a set
// TODO(muller): updatedManifestCaps is a set
// TODO(muller): return values is a set
func GetImplicitlyEnabledCapabilities(enabledManifestCaps []configv1.ClusterVersionCapability,
	updatedManifestCaps []configv1.ClusterVersionCapability,
	capabilities ClusterCapabilities) []configv1.ClusterVersionCapability {

	var caps []configv1.ClusterVersionCapability
	for _, c := range updatedManifestCaps {
		if Contains(enabledManifestCaps, c) {
			continue
		}
		if !(capabilities.Enabled.Has(c) || capabilities.ImplicitlyEnabled.Has(c)) {
			caps = append(caps, c)
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

// allKnownCapabilities returns a set of all known capabilities as defined in ClusterVersion.
func allKnownCapabilities() sets.Set[configv1.ClusterVersionCapability] {
	known := sets.New[configv1.ClusterVersionCapability]()
	for _, v := range configv1.ClusterVersionCapabilitySets {
		known.Insert(v...)
	}
	return known
}

// getEnabledCapabilities returns a set of all enabled capabilities as defined in ClusterVersion.
// DefaultCapabilitySet is used if a baseline capability set is not defined by ClusterVersion. A check is then made to
// ensure that no previously enabled capability is now disabled and if any such capabilities are found each is enabled,
// saved, and returned.
func getEnabledCapabilities(capabilitiesSpec *configv1.ClusterVersionCapabilitiesSpec,
	priorEnabled sets.Set[configv1.ClusterVersionCapability]) (sets.Set[configv1.ClusterVersionCapability],
	sets.Set[configv1.ClusterVersionCapability]) {

	capSet := DefaultCapabilitySet

	if capabilitiesSpec != nil && len(capabilitiesSpec.BaselineCapabilitySet) > 0 {
		capSet = capabilitiesSpec.BaselineCapabilitySet
	}
	enabled := sets.New[configv1.ClusterVersionCapability](configv1.ClusterVersionCapabilitySets[capSet]...)

	if capabilitiesSpec != nil {
		enabled.Insert(capabilitiesSpec.AdditionalEnabledCapabilities...)
	}

	var implicitlyEnabled []configv1.ClusterVersionCapability // TODO(muller): This is a set
	for k := range priorEnabled {
		if !enabled.Has(k) {
			implicitlyEnabled = append(implicitlyEnabled, k)
			enabled.Insert(k)
		}
	}
	sort.Sort(capabilitiesSort(implicitlyEnabled))
	return enabled, sets.New[configv1.ClusterVersionCapability](implicitlyEnabled...)
}

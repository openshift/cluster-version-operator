package capability

import (
	"fmt"
	"reflect"

	"k8s.io/apimachinery/pkg/util/sets"

	configv1 "github.com/openshift/api/config/v1"
)

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
// and recognizes all implicitly enabled ones.
func SetCapabilities(config *configv1.ClusterVersion,
	capabilities []configv1.ClusterVersionCapability) ClusterCapabilities {

	var clusterCapabilities ClusterCapabilities
	clusterCapabilities.Known = sets.New[configv1.ClusterVersionCapability](configv1.KnownClusterVersionCapabilities...)

	clusterCapabilities.Enabled, clusterCapabilities.ImplicitlyEnabled =
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

// GetImplicitlyEnabledCapabilities, given an enabled resource's current capabilities, compares them against
// the resource's capabilities from an update release. Any of the updated resource's capabilities that do not
// exist in the current resource, are not enabled, and do not already exist in the implicitly enabled capabilities
// are returned. The returned capabilities must be implicitly enabled.
func GetImplicitlyEnabledCapabilities(enabledManifestCaps sets.Set[configv1.ClusterVersionCapability],
	updatedManifestCaps sets.Set[configv1.ClusterVersionCapability],
	capabilities ClusterCapabilities) sets.Set[configv1.ClusterVersionCapability] {
	caps := updatedManifestCaps.Difference(enabledManifestCaps).Difference(capabilities.Enabled).Difference(capabilities.ImplicitlyEnabled)
	if caps.Len() == 0 {
		return nil
	}
	return caps
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

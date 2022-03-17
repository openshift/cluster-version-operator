package capability

import (
	"fmt"
	"sort"
	"strings"

	configv1 "github.com/openshift/api/config/v1"
)

const (
	CapabilityAnnotation = "capability.openshift.io/name"

	DefaultCapabilitySet = configv1.ClusterVersionCapabilitySetCurrent
)

type ClusterCapabilities struct {
	KnownCapabilities   map[configv1.ClusterVersionCapability]struct{}
	EnabledCapabilities map[configv1.ClusterVersionCapability]struct{}
}

type capabilitiesSort []configv1.ClusterVersionCapability

func (caps capabilitiesSort) Len() int           { return len(caps) }
func (caps capabilitiesSort) Swap(i, j int)      { caps[i], caps[j] = caps[j], caps[i] }
func (caps capabilitiesSort) Less(i, j int) bool { return string(caps[i]) < string(caps[j]) }

// SetCapabilities populates and returns cluster capabilities from ClusterVersion capabilities spec.
func SetCapabilities(config *configv1.ClusterVersion) ClusterCapabilities {
	var capabilities ClusterCapabilities
	capabilities.KnownCapabilities = setKnownCapabilities(config)
	capabilities.EnabledCapabilities = setEnabledCapabilities(config)
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

// CheckResourceEnablement, given resource annotations and defined cluster capabilities, checks if the capability
// annotation exists. If so, each capability name is validated against the known set of capabilities. Each valid
// capability is then checked if it is disabled. If any invalid capabilities are found an error is returned listing
// all invalid capabilities. Otherwise, if any disabled capabilities are found an error is returned listing all
// disabled capabilities.
func CheckResourceEnablement(annotations map[string]string, capabilities ClusterCapabilities) error {
	val, ok := annotations[CapabilityAnnotation]
	if !ok {
		return nil
	}
	caps := strings.Split(val, "+")
	numCaps := len(caps)
	unknownCaps := make([]string, 0, numCaps)
	disabledCaps := make([]string, 0, numCaps)

	for _, c := range caps {
		if _, ok = capabilities.KnownCapabilities[configv1.ClusterVersionCapability(c)]; !ok {
			unknownCaps = append(unknownCaps, c)
		} else if _, ok = capabilities.EnabledCapabilities[configv1.ClusterVersionCapability(c)]; !ok {
			disabledCaps = append(disabledCaps, c)
		}
	}
	if len(unknownCaps) > 0 {
		return fmt.Errorf("unrecognized capability names: %s", strings.Join(unknownCaps, ", "))
	}
	if len(disabledCaps) > 0 {
		return fmt.Errorf("disabled capabilities: %s", strings.Join(disabledCaps, ", "))
	}
	return nil
}

// setKnownCapabilities populates a map keyed by capability from all known capabilities as defined in ClusterVersion.
func setKnownCapabilities(config *configv1.ClusterVersion) map[configv1.ClusterVersionCapability]struct{} {
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
// DefaultCapabilitySet is used if a baseline capability set is not defined by ClusterVersion.
func setEnabledCapabilities(config *configv1.ClusterVersion) map[configv1.ClusterVersionCapability]struct{} {
	enabled := make(map[configv1.ClusterVersionCapability]struct{})

	capSet := DefaultCapabilitySet

	if config.Spec.Capabilities != nil && len(config.Spec.Capabilities.BaselineCapabilitySet) > 0 {
		capSet = config.Spec.Capabilities.BaselineCapabilitySet
	}

	for _, v := range configv1.ClusterVersionCapabilitySets[capSet] {
		enabled[v] = struct{}{}
	}

	if config.Spec.Capabilities != nil {
		for _, v := range config.Spec.Capabilities.AdditionalEnabledCapabilities {
			if _, ok := enabled[v]; ok {
				continue
			}
			enabled[v] = struct{}{}
		}
	}
	return enabled
}

// Package manifest collects functions about manifests that are going to be lifted to library-go
// https://github.com/openshift/library-go
// Thus it should not depend on any packages from CVO: github.com/openshift/cluster-version-operator/
package manifest

import (
	configv1 "github.com/openshift/api/config/v1"
	"github.com/openshift/library-go/pkg/manifest"

	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/klog/v2"
)

// InclusionConfiguration configures manifest inclusion, so
// callers can opt in to new filtering options instead of having to
// update existing call-sites, even if they do not need a new
// filtering option.
type InclusionConfiguration struct {
	// ExcludeIdentifier, if non-nil, excludes manifests that match the exclusion identifier.
	ExcludeIdentifier *string

	// RequiredFeatureSet, if non-nil, excludes manifests unless they match the desired feature set.
	RequiredFeatureSet *string

	// Profile, if non-nil, excludes manifests unless they match the cluster profile.
	Profile *string

	// Capabilities, if non-nil, excludes manifests unless they match the enabled cluster capabilities.
	Capabilities *configv1.ClusterVersionCapabilitiesStatus

	// Overrides excludes manifests for overridden resources.
	Overrides []configv1.ComponentOverride

	// Platform, if non-nil, excludes CredentialsRequests manifests unless they match the infrastructure platform.
	Platform *string
}

// GetImplicitlyEnabledCapabilities returns a set of capabilities that are implicitly enabled after a cluster update.
// The arguments are two sets of manifests, manifest inclusion configuration, and
// a set of capabilities that are implicitly enabled on the cluster, i.e., the capabilities
// that are NOT specified in the cluster version but has to considered enabled on the cluster.
// The manifest inclusion configuration is used to determine if a manifest should be included.
// In other words, whether, or not the cluster version operator reconcile that manifest on the cluster.
// The two sets of manifests are respectively from the release that is currently running on the cluster and
// from the release that the cluster is updated to.
func GetImplicitlyEnabledCapabilities(
	updatePayloadManifests []manifest.Manifest,
	currentPayloadManifests []manifest.Manifest,
	manifestInclusionConfiguration InclusionConfiguration,
	currentImplicitlyEnabled sets.Set[configv1.ClusterVersionCapability],
	enabledFeatureGates sets.Set[string],
) sets.Set[configv1.ClusterVersionCapability] {
	ret := currentImplicitlyEnabled.Clone()
	for _, updateManifest := range updatePayloadManifests {
		updateManErr := updateManifest.IncludeAllowUnknownCapabilities(
			manifestInclusionConfiguration.ExcludeIdentifier,
			manifestInclusionConfiguration.RequiredFeatureSet,
			manifestInclusionConfiguration.Profile,
			manifestInclusionConfiguration.Capabilities,
			manifestInclusionConfiguration.Overrides,
			enabledFeatureGates,
			true,
		)
		// update manifest is enabled, no need to check
		if updateManErr == nil {
			continue
		}
		for _, currentManifest := range currentPayloadManifests {
			if !updateManifest.SameResourceID(currentManifest) {
				continue
			}
			// current manifest is disabled, no need to check
			if err := currentManifest.IncludeAllowUnknownCapabilities(
				manifestInclusionConfiguration.ExcludeIdentifier,
				manifestInclusionConfiguration.RequiredFeatureSet,
				manifestInclusionConfiguration.Profile,
				manifestInclusionConfiguration.Capabilities,
				manifestInclusionConfiguration.Overrides,
				enabledFeatureGates,
				true,
			); err != nil {
				continue
			}
			newImplicitlyEnabled := sets.New[configv1.ClusterVersionCapability](updateManifest.GetManifestCapabilities()...).
				Difference(sets.New[configv1.ClusterVersionCapability](currentManifest.GetManifestCapabilities()...)).
				Difference(currentImplicitlyEnabled).
				Difference(sets.New[configv1.ClusterVersionCapability](manifestInclusionConfiguration.Capabilities.EnabledCapabilities...))
			ret = ret.Union(newImplicitlyEnabled)
			if newImplicitlyEnabled.Len() > 0 {
				klog.V(2).Infof("%s has changed and is now part of one or more disabled capabilities. The following capabilities will be implicitly enabled: %s",
					updateManifest.String(), sets.List(newImplicitlyEnabled))
			}
		}
	}
	return ret
}

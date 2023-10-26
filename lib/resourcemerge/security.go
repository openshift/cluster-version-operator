package resourcemerge

import (
	securityv1 "github.com/openshift/api/security/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	"k8s.io/utils/pointer"
)

// EnsureSecurityContextConstraints compares the existing state with the required states and returns
// SCC to be applied to reconcile the state. To avoid breaking existing installations with a buggy
// behavior, this method does not actually enforce the required state except for the Volumes field
// (see below), but only reports, via the boolean return value, whether the existing state matches
// the required one.
//
// The Volumes member is treated differently: the target state is the union of the items in the
// existing state with items in required state.
//
// If no reconciliation is required, the method returns nil.
func EnsureSecurityContextConstraints(existing securityv1.SecurityContextConstraints, required securityv1.SecurityContextConstraints) (*securityv1.SecurityContextConstraints, bool) {
	var modified bool
	var differsFromRequired bool
	result := existing.DeepCopy()

	EnsureObjectMeta(&modified, &result.ObjectMeta, required.ObjectMeta)

	// AllowPrivilegeEscalation is defaulted to true so set it as such when manifest
	// does not specify it
	if required.AllowPrivilegeEscalation == nil {
		required.AllowPrivilegeEscalation = pointer.Bool(true)
	}

	if !(equality.Semantic.DeepEqual(result.Priority, required.Priority) &&
		equality.Semantic.DeepEqual(result.AllowPrivilegedContainer, required.AllowPrivilegedContainer) &&
		equality.Semantic.DeepEqual(result.AllowHostDirVolumePlugin, required.AllowHostDirVolumePlugin) &&
		equality.Semantic.DeepEqual(result.AllowHostNetwork, required.AllowHostNetwork) &&
		equality.Semantic.DeepEqual(result.AllowHostPorts, required.AllowHostPorts) &&
		equality.Semantic.DeepEqual(result.AllowHostPID, required.AllowHostPID) &&
		equality.Semantic.DeepEqual(result.AllowHostIPC, required.AllowHostIPC) &&
		equality.Semantic.DeepEqual(result.DefaultAllowPrivilegeEscalation, required.DefaultAllowPrivilegeEscalation) &&
		equality.Semantic.DeepEqual(result.AllowPrivilegeEscalation, required.AllowPrivilegeEscalation) &&
		equality.Semantic.DeepEqual(result.ReadOnlyRootFilesystem, required.ReadOnlyRootFilesystem) &&
		equality.Semantic.DeepEqual(result.DefaultAddCapabilities, required.DefaultAddCapabilities) &&
		equality.Semantic.DeepEqual(result.RequiredDropCapabilities, required.RequiredDropCapabilities) &&
		equality.Semantic.DeepEqual(result.AllowedCapabilities, required.AllowedCapabilities) &&
		equality.Semantic.DeepEqual(result.AllowedFlexVolumes, required.AllowedFlexVolumes) &&
		equality.Semantic.DeepEqual(result.SELinuxContext, required.SELinuxContext) &&
		equality.Semantic.DeepEqual(result.RunAsUser, required.RunAsUser) &&
		equality.Semantic.DeepEqual(result.SupplementalGroups, required.SupplementalGroups) &&
		equality.Semantic.DeepEqual(result.FSGroup, required.FSGroup) &&
		equality.Semantic.DeepEqual(result.Users, required.Users) &&
		equality.Semantic.DeepEqual(result.Groups, required.Groups) &&
		equality.Semantic.DeepEqual(result.SeccompProfiles, required.SeccompProfiles) &&
		equality.Semantic.DeepEqual(result.AllowedUnsafeSysctls, required.AllowedUnsafeSysctls) &&
		equality.Semantic.DeepEqual(result.ForbiddenSysctls, required.ForbiddenSysctls)) {
		differsFromRequired = true
	}

	for _, required := range required.Volumes {
		found := false
		for _, curr := range result.Volumes {
			if found = equality.Semantic.DeepEqual(required, curr); found {
				break
			}
		}
		if !found {
			modified = true
			result.Volumes = append(result.Volumes, required)
		}
	}

	if len(result.Volumes) > len(required.Volumes) {
		differsFromRequired = true
	}

	if modified {
		return result, differsFromRequired
	}
	return nil, differsFromRequired
}

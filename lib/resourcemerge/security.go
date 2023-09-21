package resourcemerge

import (
	securityv1 "github.com/openshift/api/security/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	"k8s.io/utils/pointer"
)

// EnsureSecurityContextConstraints compares the existing state with the required states and
// returns SCC to be applied to reconcile the state. If no reconciliation is needed, the
// method returns nil.
func EnsureSecurityContextConstraints(existing securityv1.SecurityContextConstraints, required securityv1.SecurityContextConstraints) *securityv1.SecurityContextConstraints {
	var modified bool
	var result = existing.DeepCopy()

	EnsureObjectMeta(&modified, &result.ObjectMeta, required.ObjectMeta)
	setInt32Ptr(&modified, &result.Priority, required.Priority)
	setBool(&modified, &result.AllowPrivilegedContainer, required.AllowPrivilegedContainer)
	for _, capabilityLists := range [][]*[]corev1.Capability{
		{&result.DefaultAddCapabilities, &required.DefaultAddCapabilities},
		{&result.RequiredDropCapabilities, &required.RequiredDropCapabilities},
		{&result.AllowedCapabilities, &required.AllowedCapabilities},
	} {
		existingCapabilities := capabilityLists[0]
		requiredCapabilities := capabilityLists[1]
		if !equality.Semantic.DeepEqual(existingCapabilities, requiredCapabilities) {
			modified = true
			*existingCapabilities = *requiredCapabilities
		}
	}

	setBool(&modified, &result.AllowHostDirVolumePlugin, required.AllowHostDirVolumePlugin)
	if !equality.Semantic.DeepEqual(result.Volumes, required.Volumes) {
		modified = true
		result.Volumes = required.Volumes
	}
	if !equality.Semantic.DeepEqual(result.AllowedFlexVolumes, required.AllowedFlexVolumes) {
		modified = true
		result.AllowedFlexVolumes = required.AllowedFlexVolumes
	}

	setBool(&modified, &result.AllowHostNetwork, required.AllowHostNetwork)
	setBool(&modified, &result.AllowHostPorts, required.AllowHostPorts)
	setBool(&modified, &result.AllowHostPID, required.AllowHostPID)
	setBool(&modified, &result.AllowHostIPC, required.AllowHostIPC)
	setBoolPtr(&modified, &result.DefaultAllowPrivilegeEscalation, required.DefaultAllowPrivilegeEscalation)

	// AllowPrivilegeEscalation is optional and defaults to true if not specified,
	// so we enforce default if manifest does not specify it.
	if required.AllowPrivilegeEscalation == nil {
		setBoolPtr(&modified, &result.AllowPrivilegeEscalation, pointer.Bool(true))
	} else {
		setBoolPtr(&modified, &result.AllowPrivilegeEscalation, required.AllowPrivilegeEscalation)
	}

	if !equality.Semantic.DeepEqual(result.SELinuxContext, required.SELinuxContext) {
		modified = true
		result.SELinuxContext = required.SELinuxContext
	}
	if !equality.Semantic.DeepEqual(result.RunAsUser, required.RunAsUser) {
		modified = true
		result.RunAsUser = required.RunAsUser
	}
	if !equality.Semantic.DeepEqual(result.FSGroup, required.FSGroup) {
		modified = true
		result.FSGroup = required.FSGroup
	}
	if !equality.Semantic.DeepEqual(result.SupplementalGroups, required.SupplementalGroups) {
		modified = true
		result.SupplementalGroups = required.SupplementalGroups
	}
	setBool(&modified, &result.ReadOnlyRootFilesystem, required.ReadOnlyRootFilesystem)

	for _, slices := range [][]*[]string{
		{&result.Users, &required.Users},
		{&result.Groups, &required.Groups},
		{&result.SeccompProfiles, &required.SeccompProfiles},
		{&result.AllowedUnsafeSysctls, &required.AllowedUnsafeSysctls},
		{&result.ForbiddenSysctls, &required.ForbiddenSysctls},
	} {
		existingStrings := slices[0]
		requiredStrings := slices[1]
		if !equality.Semantic.DeepEqual(existingStrings, requiredStrings) {
			modified = true
			*existingStrings = *requiredStrings
		}
	}

	if modified {
		return result
	}
	return nil
}

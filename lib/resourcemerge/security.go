package resourcemerge

import (
	securityv1 "github.com/openshift/api/security/v1"
	"k8s.io/apimachinery/pkg/api/equality"
)

// EnsureSecurityContextConstraints ensures that the result matches the required.
// modified is set to true when result had to be updated with required.
func EnsureSecurityContextConstraints(existing securityv1.SecurityContextConstraints, required securityv1.SecurityContextConstraints) *securityv1.SecurityContextConstraints {
	var modified bool
	result := existing.DeepCopy()

	EnsureObjectMeta(&modified, &result.ObjectMeta, required.ObjectMeta)
	setInt32Ptr(&modified, &result.Priority, required.Priority)
	setBool(&modified, &result.AllowPrivilegedContainer, required.AllowPrivilegedContainer)
	for _, required := range required.DefaultAddCapabilities {
		found := false
		for _, curr := range result.DefaultAddCapabilities {
			if equality.Semantic.DeepEqual(required, curr) {
				found = true
				break
			}
		}
		if !found {
			modified = true
			result.DefaultAddCapabilities = append(result.DefaultAddCapabilities, required)
		}
	}
	for _, required := range required.RequiredDropCapabilities {
		found := false
		for _, curr := range result.RequiredDropCapabilities {
			if equality.Semantic.DeepEqual(required, curr) {
				found = true
				break
			}
		}
		if !found {
			modified = true
			result.RequiredDropCapabilities = append(result.RequiredDropCapabilities, required)
		}
	}
	for _, required := range required.AllowedCapabilities {
		found := false
		for _, curr := range result.AllowedCapabilities {
			if equality.Semantic.DeepEqual(required, curr) {
				found = true
				break
			}
		}
		if !found {
			modified = true
			result.AllowedCapabilities = append(result.AllowedCapabilities, required)
		}
	}
	setBool(&modified, &result.AllowHostDirVolumePlugin, required.AllowHostDirVolumePlugin)
	for _, required := range required.Volumes {
		found := false
		for _, curr := range result.Volumes {
			if equality.Semantic.DeepEqual(required, curr) {
				found = true
				break
			}
		}
		if !found {
			modified = true
			result.Volumes = append(result.Volumes, required)
		}
	}
	for _, required := range required.AllowedFlexVolumes {
		found := false
		for _, curr := range result.AllowedFlexVolumes {
			if equality.Semantic.DeepEqual(required.Driver, curr.Driver) {
				found = true
				break
			}
		}
		if !found {
			modified = true
			result.AllowedFlexVolumes = append(result.AllowedFlexVolumes, required)
		}
	}
	setBool(&modified, &result.AllowHostNetwork, required.AllowHostNetwork)
	setBool(&modified, &result.AllowHostPorts, required.AllowHostPorts)
	setBool(&modified, &result.AllowHostPID, required.AllowHostPID)
	setBool(&modified, &result.AllowHostIPC, required.AllowHostIPC)
	setBoolPtr(&modified, &result.DefaultAllowPrivilegeEscalation, required.DefaultAllowPrivilegeEscalation)
	setBoolPtr(&modified, &result.AllowPrivilegeEscalation, required.AllowPrivilegeEscalation)
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
	mergeStringSlice(&modified, &result.Users, required.Users)
	mergeStringSlice(&modified, &result.Groups, required.Groups)
	mergeStringSlice(&modified, &result.SeccompProfiles, required.SeccompProfiles)
	mergeStringSlice(&modified, &result.AllowedUnsafeSysctls, required.AllowedUnsafeSysctls)
	mergeStringSlice(&modified, &result.ForbiddenSysctls, required.ForbiddenSysctls)

	if modified {
		return result
	}
	return nil
}

package resourcemerge

import (
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	"k8s.io/utils/pointer"

	securityv1 "github.com/openshift/api/security/v1"
)

// EnsureSecurityContextConstraints compares the existing state with the required states and returns
// SCC to be applied to reconcile the state. If there were items in the existing state that were not
// present in the required state, the items are kept and the returned SCC contains items from both
// the existing and required state. If this happens then the boolean return value is set to true.
//
// If no reconciliation is needed, the method returns nil. Note that the boolean can be still true
// indicating that existing state has additional items not present in the required state, but the
// existing state is already properly reconciled to desired state.
func EnsureSecurityContextConstraints(existing securityv1.SecurityContextConstraints, required securityv1.SecurityContextConstraints) (*securityv1.SecurityContextConstraints, bool) {
	var modified bool
	var mergedClusterState bool
	result := existing.DeepCopy()

	EnsureObjectMeta(&modified, &result.ObjectMeta, required.ObjectMeta)
	setInt32Ptr(&modified, &result.Priority, required.Priority)
	setBool(&modified, &result.AllowPrivilegedContainer, required.AllowPrivilegedContainer)
	for _, required := range required.DefaultAddCapabilities {
		found := false
		for _, curr := range result.DefaultAddCapabilities {
			if found = equality.Semantic.DeepEqual(required, curr); found {
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
			if found = equality.Semantic.DeepEqual(required, curr); found {
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
			if found = equality.Semantic.DeepEqual(required, curr); found {
				break
			}
		}
		if !found {
			modified = true
			result.AllowedCapabilities = append(result.AllowedCapabilities, required)
		}
	}

	for _, capabilityLists := range [][][]corev1.Capability{
		{result.DefaultAddCapabilities, required.DefaultAddCapabilities},
		{result.RequiredDropCapabilities, required.RequiredDropCapabilities},
		{result.AllowedCapabilities, required.AllowedCapabilities},
	} {
		if result, required := len(capabilityLists[0]), len(capabilityLists[1]); result > required {
			mergedClusterState = true
		}
	}

	setBool(&modified, &result.AllowHostDirVolumePlugin, required.AllowHostDirVolumePlugin)
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
	for _, required := range required.AllowedFlexVolumes {
		found := false
		for _, curr := range result.AllowedFlexVolumes {
			if found = equality.Semantic.DeepEqual(required.Driver, curr.Driver); found {
				break
			}
		}
		if !found {
			modified = true
			result.AllowedFlexVolumes = append(result.AllowedFlexVolumes, required)
		}
	}

	if len(result.Volumes) > len(required.Volumes) || len(result.AllowedFlexVolumes) > len(required.AllowedFlexVolumes) {
		mergedClusterState = true
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
	mergeStringSlice(&modified, &result.Users, required.Users)
	mergeStringSlice(&modified, &result.Groups, required.Groups)
	mergeStringSlice(&modified, &result.SeccompProfiles, required.SeccompProfiles)
	mergeStringSlice(&modified, &result.AllowedUnsafeSysctls, required.AllowedUnsafeSysctls)
	mergeStringSlice(&modified, &result.ForbiddenSysctls, required.ForbiddenSysctls)

	for _, itemLists := range [][][]string{
		{result.Users, required.Users},
		{result.Groups, required.Groups},
		{result.SeccompProfiles, required.SeccompProfiles},
		{result.AllowedUnsafeSysctls, required.AllowedUnsafeSysctls},
		{result.ForbiddenSysctls, required.ForbiddenSysctls},
	} {
		if result, required := len(itemLists[0]), len(itemLists[1]); result > required {
			mergedClusterState = true
		}
	}

	if modified {
		return result, mergedClusterState
	}
	return nil, mergedClusterState
}

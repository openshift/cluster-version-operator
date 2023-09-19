package resourcemerge

import (
	"testing"

	"github.com/google/go-cmp/cmp"
	securityv1 "github.com/openshift/api/security/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/pointer"
)

var restrictedv2 = securityv1.SecurityContextConstraints{
	TypeMeta: metav1.TypeMeta{Kind: "SecurityContextConstraints", APIVersion: securityv1.GroupVersion.String()},
	ObjectMeta: metav1.ObjectMeta{
		Name: "restricted-v2",
		Annotations: map[string]string{
			"include.release.openshift.io/ibm-cloud-managed":              "true",
			"include.release.openshift.io/self-managed-high-availability": "true",
			"include.release.openshift.io/single-node-developer":          "true",
			"kubernetes.io/description":                                   "restricted-v2 denies access...",
		},
	},
	RequiredDropCapabilities: []corev1.Capability{"ALL"},
	AllowedCapabilities:      []corev1.Capability{"NET_BIND_SERVICE"},
	Volumes:                  []securityv1.FSType{"configMap", "downwardAPI", "emptyDir", "persistentVolumeClaim", "projected", "secret"},
	SELinuxContext:           securityv1.SELinuxContextStrategyOptions{Type: securityv1.SELinuxStrategyMustRunAs},
	RunAsUser:                securityv1.RunAsUserStrategyOptions{Type: securityv1.RunAsUserStrategyMustRunAsRange},
	SupplementalGroups:       securityv1.SupplementalGroupsStrategyOptions{Type: securityv1.SupplementalGroupsStrategyRunAsAny},
	FSGroup:                  securityv1.FSGroupStrategyOptions{Type: securityv1.FSGroupStrategyMustRunAs},
	SeccompProfiles:          []string{"runtime/default"},
}

func TestEnsureSecurityContextConstraints(t *testing.T) {
	testCases := []struct {
		name     string
		existing securityv1.SecurityContextConstraints
		required securityv1.SecurityContextConstraints

		expected *securityv1.SecurityContextConstraints
	}{
		{
			name:     "no modified when existing is equal to required",
			existing: *restrictedv2.DeepCopy(),
			required: *restrictedv2.DeepCopy(),
			expected: nil,
		},
		{

			name: "enforce primitive fields",
			existing: securityv1.SecurityContextConstraints{
				Priority:                        pointer.Int32(123),
				AllowPrivilegedContainer:        true,
				AllowHostDirVolumePlugin:        false,
				AllowHostNetwork:                true,
				AllowHostPorts:                  false,
				AllowHostPID:                    true,
				AllowHostIPC:                    false,
				DefaultAllowPrivilegeEscalation: pointer.Bool(true),
				AllowPrivilegeEscalation:        pointer.Bool(false),
				ReadOnlyRootFilesystem:          true,
			},
			required: securityv1.SecurityContextConstraints{
				Priority:                        pointer.Int32(42),
				AllowPrivilegedContainer:        false,
				AllowHostDirVolumePlugin:        true,
				AllowHostNetwork:                false,
				AllowHostPorts:                  true,
				AllowHostPID:                    false,
				AllowHostIPC:                    true,
				DefaultAllowPrivilegeEscalation: pointer.Bool(false),
				AllowPrivilegeEscalation:        pointer.Bool(true),
				ReadOnlyRootFilesystem:          false,
			},
			expected: &securityv1.SecurityContextConstraints{
				Priority:                        pointer.Int32(42),
				AllowPrivilegedContainer:        false,
				AllowHostDirVolumePlugin:        true,
				AllowHostNetwork:                false,
				AllowHostPorts:                  true,
				AllowHostPID:                    false,
				AllowHostIPC:                    true,
				DefaultAllowPrivilegeEscalation: pointer.Bool(false),
				AllowPrivilegeEscalation:        pointer.Bool(true),
				ReadOnlyRootFilesystem:          false,
			},
		},
		{
			name: "enforce capabilities",
			existing: securityv1.SecurityContextConstraints{
				DefaultAddCapabilities:   []corev1.Capability{"SHARED_A", "EXISTING_A", "EXISTING_B"},
				RequiredDropCapabilities: []corev1.Capability{"SHARED_B", "EXISTING_C", "EXISTING_D"},
				AllowedCapabilities:      []corev1.Capability{"SHARED_C", "EXISTING_E", "EXISTING_F"},
			},
			required: securityv1.SecurityContextConstraints{
				DefaultAddCapabilities:   []corev1.Capability{"SHARED_A", "REQUIRED_A", "REQUIRED_B"},
				RequiredDropCapabilities: []corev1.Capability{"SHARED_B", "REQUIRED_C", "REQUIRED_D"},
				AllowedCapabilities:      []corev1.Capability{"SHARED_C", "REQUIRED_E", "REQUIRED_F"},
			},
			expected: &securityv1.SecurityContextConstraints{
				DefaultAddCapabilities:   []corev1.Capability{"SHARED_A", "REQUIRED_A", "REQUIRED_B"},
				RequiredDropCapabilities: []corev1.Capability{"SHARED_B", "REQUIRED_C", "REQUIRED_D"},
				AllowedCapabilities:      []corev1.Capability{"SHARED_C", "REQUIRED_E", "REQUIRED_F"},
			},
		},
		{
			name: "enforce volumes",
			existing: securityv1.SecurityContextConstraints{
				Volumes:            []securityv1.FSType{securityv1.FSTypeAzureFile, securityv1.FSTypeEphemeral, securityv1.FSTypeSecret},
				AllowedFlexVolumes: []securityv1.AllowedFlexVolume{{Driver: "shared"}, {Driver: "existing-1"}, {Driver: "existing-2"}},
			},
			required: securityv1.SecurityContextConstraints{
				Volumes:            []securityv1.FSType{securityv1.FSTypeAzureFile, securityv1.FSProjected, securityv1.FSTypePersistentVolumeClaim},
				AllowedFlexVolumes: []securityv1.AllowedFlexVolume{{Driver: "shared"}, {Driver: "required-1"}, {Driver: "required-2"}},
			},
			expected: &securityv1.SecurityContextConstraints{
				Volumes:            []securityv1.FSType{securityv1.FSTypeAzureFile, securityv1.FSProjected, securityv1.FSTypePersistentVolumeClaim},
				AllowedFlexVolumes: []securityv1.AllowedFlexVolume{{Driver: "shared"}, {Driver: "required-1"}, {Driver: "required-2"}},
			},
		},
		{
			name: "enforce strategy options",
			existing: securityv1.SecurityContextConstraints{
				SELinuxContext:     securityv1.SELinuxContextStrategyOptions{Type: securityv1.SELinuxStrategyMustRunAs, SELinuxOptions: &corev1.SELinuxOptions{User: "ooser"}},
				RunAsUser:          securityv1.RunAsUserStrategyOptions{Type: securityv1.RunAsUserStrategyRunAsAny},
				SupplementalGroups: securityv1.SupplementalGroupsStrategyOptions{Type: securityv1.SupplementalGroupsStrategyMustRunAs},
				FSGroup:            securityv1.FSGroupStrategyOptions{Type: securityv1.FSGroupStrategyRunAsAny},
			},
			required: securityv1.SecurityContextConstraints{
				SELinuxContext:     securityv1.SELinuxContextStrategyOptions{Type: securityv1.SELinuxStrategyRunAsAny},
				RunAsUser:          securityv1.RunAsUserStrategyOptions{Type: securityv1.RunAsUserStrategyMustRunAsNonRoot},
				SupplementalGroups: securityv1.SupplementalGroupsStrategyOptions{Type: securityv1.SupplementalGroupsStrategyRunAsAny},
				FSGroup:            securityv1.FSGroupStrategyOptions{Type: securityv1.FSGroupStrategyMustRunAs},
			},
			expected: &securityv1.SecurityContextConstraints{
				SELinuxContext:     securityv1.SELinuxContextStrategyOptions{Type: securityv1.SELinuxStrategyRunAsAny},
				RunAsUser:          securityv1.RunAsUserStrategyOptions{Type: securityv1.RunAsUserStrategyMustRunAsNonRoot},
				SupplementalGroups: securityv1.SupplementalGroupsStrategyOptions{Type: securityv1.SupplementalGroupsStrategyRunAsAny},
				FSGroup:            securityv1.FSGroupStrategyOptions{Type: securityv1.FSGroupStrategyMustRunAs},
			},
		},
		{
			name: "enforce users, groups, seccomp profiles, sysctls",
			existing: securityv1.SecurityContextConstraints{
				Users:                []string{"shared-u", "existing-user-1", "existing-user-2"},
				Groups:               []string{"shared-g", "existing-group-1", "existing-group-2"},
				SeccompProfiles:      []string{"shared-s", "existing-seccomp-1", "existing-seccomp-2"},
				AllowedUnsafeSysctls: []string{"shared-a", "existing-unsafe-sysctl-1", "existing-unsafe-sysctl-2"},
				ForbiddenSysctls:     []string{"shared-f", "existing-forbidden-sysctl-1", "existing-forbidden-sysctl-2"},
			},
			required: securityv1.SecurityContextConstraints{
				Users:                []string{"shared-u", "required-user-1", "required-user-2"},
				Groups:               []string{"shared-g", "required-group-1", "required-group-2"},
				SeccompProfiles:      []string{"shared-s", "required-seccomp-1", "required-seccomp-2"},
				AllowedUnsafeSysctls: []string{"shared-a", "required-unsafe-sysctl-1", "required-unsafe-sysctl-2"},
				ForbiddenSysctls:     []string{"shared-f", "required-forbidden-sysctl-1", "required-forbidden-sysctl-2"},
			},
			expected: &securityv1.SecurityContextConstraints{
				Users:                []string{"shared-u", "required-user-1", "required-user-2"},
				Groups:               []string{"shared-g", "required-group-1", "required-group-2"},
				SeccompProfiles:      []string{"shared-s", "required-seccomp-1", "required-seccomp-2"},
				AllowedUnsafeSysctls: []string{"shared-a", "required-unsafe-sysctl-1", "required-unsafe-sysctl-2"},
				ForbiddenSysctls:     []string{"shared-f", "required-forbidden-sysctl-1", "required-forbidden-sysctl-2"},
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result := EnsureSecurityContextConstraints(tc.existing, tc.required)

			origExisting, origRequired := tc.existing.DeepCopy(), tc.required.DeepCopy()

			if diff := cmp.Diff(tc.expected, result); diff != "" {
				t.Errorf("SCC differs from expected:\n%s", diff)
			}

			if diff := cmp.Diff(origExisting, &tc.existing); diff != "" {
				t.Errorf("Unexpected side effect on existing state:\n%s", diff)
			}
			if diff := cmp.Diff(origRequired, &tc.required); diff != "" {
				t.Errorf("Unexpected side effect on required state:\n%s", diff)
			}
		})
	}
}

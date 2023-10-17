package resourcemerge

import (
	"testing"

	"github.com/google/go-cmp/cmp"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/pointer"

	securityv1 "github.com/openshift/api/security/v1"
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

func defaulted(scc securityv1.SecurityContextConstraints) *securityv1.SecurityContextConstraints {
	if scc.AllowPrivilegeEscalation == nil {
		scc.AllowPrivilegeEscalation = pointer.Bool(true)
	}
	return &scc
}

func TestEnsureSecurityContextConstraints(t *testing.T) {
	testCases := []struct {
		name     string
		existing securityv1.SecurityContextConstraints
		required securityv1.SecurityContextConstraints

		expected                    *securityv1.SecurityContextConstraints
		expectedDiffersFromRequired bool
	}{
		{
			name:                        "no reconcile needed when existing is equal to required",
			existing:                    *defaulted(*restrictedv2.DeepCopy()),
			required:                    *defaulted(*restrictedv2.DeepCopy()),
			expectedDiffersFromRequired: false,
		},
		{
			name:                        "primitive field Priority is not reconciled but reported as different from required",
			existing:                    *defaulted(securityv1.SecurityContextConstraints{Priority: pointer.Int32(123)}),
			required:                    securityv1.SecurityContextConstraints{Priority: pointer.Int32(42)},
			expected:                    nil,
			expectedDiffersFromRequired: true,
		},
		{
			name:                        "primitive field AllowPrivilegedContainer is not reconciled but reported as different from required",
			existing:                    *defaulted(securityv1.SecurityContextConstraints{AllowPrivilegedContainer: true}),
			required:                    securityv1.SecurityContextConstraints{AllowPrivilegedContainer: false},
			expected:                    nil,
			expectedDiffersFromRequired: true,
		},
		{
			name:                        "primitive field AllowHostDirVolumePlugin is not reconciled but reported as different from required",
			existing:                    *defaulted(securityv1.SecurityContextConstraints{AllowHostDirVolumePlugin: false}),
			required:                    securityv1.SecurityContextConstraints{AllowHostDirVolumePlugin: true},
			expected:                    nil,
			expectedDiffersFromRequired: true,
		},
		{
			name:                        "primitive field AllowHostNetwork is not reconciled but reported as different from required",
			existing:                    *defaulted(securityv1.SecurityContextConstraints{AllowHostNetwork: true}),
			required:                    securityv1.SecurityContextConstraints{AllowHostNetwork: false},
			expected:                    nil,
			expectedDiffersFromRequired: true,
		},
		{
			name:                        "primitive field AllowHostPorts is not reconciled but reported as different from required",
			existing:                    *defaulted(securityv1.SecurityContextConstraints{AllowHostPorts: false}),
			required:                    securityv1.SecurityContextConstraints{AllowHostPorts: true},
			expected:                    nil,
			expectedDiffersFromRequired: true,
		},
		{
			name:                        "primitive field AllowHostPID is not reconciled but reported as different from required",
			existing:                    *defaulted(securityv1.SecurityContextConstraints{AllowHostPID: true}),
			required:                    securityv1.SecurityContextConstraints{AllowHostPID: false},
			expected:                    nil,
			expectedDiffersFromRequired: true,
		},
		{
			name:                        "primitive field AllowHostIPC is not reconciled but reported as different from required",
			existing:                    *defaulted(securityv1.SecurityContextConstraints{AllowHostIPC: false}),
			required:                    securityv1.SecurityContextConstraints{AllowHostIPC: true},
			expected:                    nil,
			expectedDiffersFromRequired: true,
		},
		{
			name:                        "primitive field DefaultAllowPrivilegeEscalation is not reconciled but reported as different from required",
			existing:                    *defaulted(securityv1.SecurityContextConstraints{DefaultAllowPrivilegeEscalation: pointer.Bool(true)}),
			required:                    securityv1.SecurityContextConstraints{DefaultAllowPrivilegeEscalation: pointer.Bool(false)},
			expected:                    nil,
			expectedDiffersFromRequired: true,
		},
		{
			name:                        "primitive field AllowPrivilegeEscalation is not reconciled but reported as different from required",
			existing:                    *defaulted(securityv1.SecurityContextConstraints{AllowPrivilegeEscalation: pointer.Bool(false)}),
			required:                    securityv1.SecurityContextConstraints{AllowPrivilegeEscalation: pointer.Bool(true)},
			expected:                    nil,
			expectedDiffersFromRequired: true,
		},
		{
			name:                        "primitive field ReadOnlyRootFilesystem is not reconciled but reported as different from required",
			existing:                    *defaulted(securityv1.SecurityContextConstraints{ReadOnlyRootFilesystem: true}),
			required:                    securityv1.SecurityContextConstraints{ReadOnlyRootFilesystem: false},
			expected:                    nil,
			expectedDiffersFromRequired: true,
		},
		{
			name:                        "allowPrivilegeEscalation is not reconciled to default true when not specified, but reported as different from required",
			existing:                    securityv1.SecurityContextConstraints{AllowPrivilegeEscalation: pointer.Bool(false)},
			required:                    securityv1.SecurityContextConstraints{AllowPrivilegeEscalation: nil},
			expected:                    nil,
			expectedDiffersFromRequired: true,
		},
		{
			name:                        "allowPrivilegeEscalation does not need to be reconciled when existing state is true (default)",
			existing:                    securityv1.SecurityContextConstraints{AllowPrivilegeEscalation: pointer.Bool(true)},
			required:                    securityv1.SecurityContextConstraints{AllowPrivilegeEscalation: nil},
			expectedDiffersFromRequired: false,
		},
		{
			name:                        "DefaultAddCapabilities are not reconciled but differences are tracked",
			existing:                    *defaulted(securityv1.SecurityContextConstraints{DefaultAddCapabilities: []corev1.Capability{"SHARED_A", "EXISTING_A", "EXISTING_B"}}),
			required:                    securityv1.SecurityContextConstraints{DefaultAddCapabilities: []corev1.Capability{"SHARED_A", "REQUIRED_A", "REQUIRED_B"}},
			expected:                    nil,
			expectedDiffersFromRequired: true,
		},
		{
			name:                        "RequiredDropCapabilities are not reconciled but differences are tracked",
			existing:                    *defaulted(securityv1.SecurityContextConstraints{RequiredDropCapabilities: []corev1.Capability{"SHARED_B", "EXISTING_C", "EXISTING_D"}}),
			required:                    securityv1.SecurityContextConstraints{RequiredDropCapabilities: []corev1.Capability{"SHARED_B", "REQUIRED_C", "REQUIRED_D"}},
			expected:                    nil,
			expectedDiffersFromRequired: true,
		},
		{
			name:                        "AllowedCapabilities are not reconciled but differences are tracked",
			existing:                    *defaulted(securityv1.SecurityContextConstraints{AllowedCapabilities: []corev1.Capability{"SHARED_C", "EXISTING_E", "EXISTING_F"}}),
			required:                    securityv1.SecurityContextConstraints{AllowedCapabilities: []corev1.Capability{"SHARED_C", "REQUIRED_E", "REQUIRED_F"}},
			expected:                    nil,
			expectedDiffersFromRequired: true,
		},
		{
			name: "merge Volumes, merges are tracked",
			existing: *defaulted(securityv1.SecurityContextConstraints{
				Volumes: []securityv1.FSType{securityv1.FSTypeAzureFile, securityv1.FSTypeEphemeral, securityv1.FSTypeSecret},
			}),
			required: securityv1.SecurityContextConstraints{
				Volumes: []securityv1.FSType{securityv1.FSTypeAzureFile, securityv1.FSProjected, securityv1.FSTypePersistentVolumeClaim},
			},
			expected: defaulted(securityv1.SecurityContextConstraints{
				Volumes: []securityv1.FSType{securityv1.FSTypeAzureFile, securityv1.FSTypeEphemeral, securityv1.FSTypeSecret, securityv1.FSProjected, securityv1.FSTypePersistentVolumeClaim},
			}),
			expectedDiffersFromRequired: true,
		},
		{
			name: "AllowedFlexVolumes are not reconciled but differences are tracked",
			existing: *defaulted(securityv1.SecurityContextConstraints{
				AllowedFlexVolumes: []securityv1.AllowedFlexVolume{{Driver: "shared"}, {Driver: "existing-1"}, {Driver: "existing-2"}},
			}),
			required: securityv1.SecurityContextConstraints{
				AllowedFlexVolumes: []securityv1.AllowedFlexVolume{{Driver: "shared"}, {Driver: "required-1"}, {Driver: "required-2"}},
			},
			expectedDiffersFromRequired: true,
		},
		{
			name: "SELinuxContext is not reconciled but differences are tracked",
			existing: *defaulted(securityv1.SecurityContextConstraints{
				SELinuxContext: securityv1.SELinuxContextStrategyOptions{Type: securityv1.SELinuxStrategyMustRunAs, SELinuxOptions: &corev1.SELinuxOptions{User: "ooser"}},
			}),
			required: securityv1.SecurityContextConstraints{
				SELinuxContext: securityv1.SELinuxContextStrategyOptions{Type: securityv1.SELinuxStrategyRunAsAny},
			},
			expected:                    nil,
			expectedDiffersFromRequired: true,
		},
		{
			name: "RunAsUser is not reconciled but differences are tracked",
			existing: *defaulted(securityv1.SecurityContextConstraints{
				RunAsUser: securityv1.RunAsUserStrategyOptions{Type: securityv1.RunAsUserStrategyRunAsAny},
			}),
			required: securityv1.SecurityContextConstraints{
				RunAsUser: securityv1.RunAsUserStrategyOptions{Type: securityv1.RunAsUserStrategyMustRunAsNonRoot},
			},
			expected:                    nil,
			expectedDiffersFromRequired: true,
		},
		{
			name: "SupplementalGroups is not reconciled but differences are tracked",
			existing: *defaulted(securityv1.SecurityContextConstraints{
				SupplementalGroups: securityv1.SupplementalGroupsStrategyOptions{Type: securityv1.SupplementalGroupsStrategyMustRunAs},
			}),
			required: securityv1.SecurityContextConstraints{
				SupplementalGroups: securityv1.SupplementalGroupsStrategyOptions{Type: securityv1.SupplementalGroupsStrategyRunAsAny},
			},
			expected:                    nil,
			expectedDiffersFromRequired: true,
		},
		{
			name: "FSGroup is not reconciled but differences are tracked",
			existing: *defaulted(securityv1.SecurityContextConstraints{
				FSGroup: securityv1.FSGroupStrategyOptions{Type: securityv1.FSGroupStrategyRunAsAny},
			}),
			required: securityv1.SecurityContextConstraints{
				FSGroup: securityv1.FSGroupStrategyOptions{Type: securityv1.FSGroupStrategyMustRunAs},
			},
			expected:                    nil,
			expectedDiffersFromRequired: true,
		},
		{
			name: "Users are not reconciled but differences are tracked",
			existing: *defaulted(securityv1.SecurityContextConstraints{
				Users: []string{"shared-u", "existing-user-1", "existing-user-2"},
			}),
			required: securityv1.SecurityContextConstraints{
				Users: []string{"shared-u", "required-user-1", "required-user-2"},
			},
			expected:                    nil,
			expectedDiffersFromRequired: true,
		},
		{
			name: "Groups are not reconciled but differences are tracked",
			existing: *defaulted(securityv1.SecurityContextConstraints{
				Groups: []string{"shared-g", "existing-group-1", "existing-group-2"},
			}),
			required: securityv1.SecurityContextConstraints{
				Groups: []string{"shared-g", "required-group-1", "required-group-2"},
			},
			expected:                    nil,
			expectedDiffersFromRequired: true,
		},
		{
			name: "SeccompProfiles are not reconciled but differences are tracked",
			existing: *defaulted(securityv1.SecurityContextConstraints{
				SeccompProfiles: []string{"shared-s", "existing-seccomp-1", "existing-seccomp-2"},
			}),
			required: securityv1.SecurityContextConstraints{
				SeccompProfiles: []string{"shared-s", "required-seccomp-1", "required-seccomp-2"},
			},
			expected:                    nil,
			expectedDiffersFromRequired: true,
		},
		{
			name: "AllowedUnsafeSysctls are not reconciled but differences are tracked",
			existing: *defaulted(securityv1.SecurityContextConstraints{
				AllowedUnsafeSysctls: []string{"shared-a", "existing-unsafe-sysctl-1", "existing-unsafe-sysctl-2"},
			}),
			required: securityv1.SecurityContextConstraints{
				AllowedUnsafeSysctls: []string{"shared-a", "required-unsafe-sysctl-1", "required-unsafe-sysctl-2"},
			},
			expected:                    nil,
			expectedDiffersFromRequired: true,
		},
		{
			name: "ForbiddenSysctls are not reconciled but differences are tracked",
			existing: *defaulted(securityv1.SecurityContextConstraints{
				ForbiddenSysctls: []string{"shared-f", "existing-forbidden-sysctl-1", "existing-forbidden-sysctl-2"},
			}),
			required: securityv1.SecurityContextConstraints{
				ForbiddenSysctls: []string{"shared-f", "required-forbidden-sysctl-1", "required-forbidden-sysctl-2"},
			},
			expected:                    nil,
			expectedDiffersFromRequired: true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result, differsFromRequired := EnsureSecurityContextConstraints(tc.existing, tc.required)

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

			if differsFromRequired != tc.expectedDiffersFromRequired {
				t.Errorf("expected differsFromRequired=%T, got %v", tc.expectedDiffersFromRequired, differsFromRequired)
			}
		})
	}
}

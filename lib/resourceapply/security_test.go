package resourceapply

import (
	"context"
	"testing"

	"github.com/google/go-cmp/cmp"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	kubetesting "k8s.io/client-go/testing"

	securityv1 "github.com/openshift/api/security/v1"
	securityfake "github.com/openshift/client-go/security/clientset/versioned/fake"
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

func getSCCAction(name string) kubetesting.Action {
	return kubetesting.NewGetAction(securityv1.GroupVersion.WithResource("securitycontextconstraints"), "", name)
}

func createSCCAction(scc *securityv1.SecurityContextConstraints) kubetesting.Action {
	return kubetesting.NewCreateAction(securityv1.GroupVersion.WithResource("securitycontextconstraints"), "", scc)
}

func updateSCCAction(scc *securityv1.SecurityContextConstraints) kubetesting.Action {
	return kubetesting.NewUpdateAction(securityv1.GroupVersion.WithResource("securitycontextconstraints"), "", scc)
}

func TestApplySecurityContextConstraintsv1(t *testing.T) {
	tests := []struct {
		name     string
		existing func() *securityv1.SecurityContextConstraints
		required func() *securityv1.SecurityContextConstraints

		expected         func() *securityv1.SecurityContextConstraints
		expectedAPICalls []kubetesting.Action
		expectedModified bool
		expectedErr      error
	}{
		{
			name:     "create nonexistent, expect modified",
			required: restrictedv2.DeepCopy,
			expected: restrictedv2.DeepCopy,
			expectedAPICalls: []kubetesting.Action{
				getSCCAction("restricted-v2"),
				createSCCAction(&restrictedv2),
			},
			expectedModified: true,
		},
		{
			name:     "no modified when existing is equal to required",
			existing: restrictedv2.DeepCopy,
			required: restrictedv2.DeepCopy,
			expected: restrictedv2.DeepCopy,
			expectedAPICalls: []kubetesting.Action{
				getSCCAction("restricted-v2"),
			},
			expectedModified: false,
		},
		{
			name:     "no modified when existing differs but there's release.openshift.io/create-only: true annotation",
			existing: restrictedv2.DeepCopy,
			required: func() *securityv1.SecurityContextConstraints {
				scc := restrictedv2.DeepCopy()
				scc.Annotations = map[string]string{CreateOnlyAnnotation: "true"}
				scc.Volumes = nil
				scc.AllowHostDirVolumePlugin = true
				return scc
			},
			expected: func() *securityv1.SecurityContextConstraints { return nil },
			expectedAPICalls: []kubetesting.Action{
				getSCCAction("restricted-v2"),
			},
			expectedModified: false,
		},
		{
			name:     "enforce missing volumes from required DOES NOT WORK because OCPBUGS-18386, modified is false",
			existing: restrictedv2.DeepCopy,
			required: func() *securityv1.SecurityContextConstraints {
				scc := restrictedv2.DeepCopy()
				scc.Volumes = []securityv1.FSType{"configMap", "downwardAPI", "emptyDir", "ephemeral", "persistentVolumeClaim", "projected", "secret"}
				return scc
			},
			expected: restrictedv2.DeepCopy,
			expectedAPICalls: []kubetesting.Action{
				getSCCAction("restricted-v2"),
			},
		},
		{
			name:     "do not keep additional volumes from existing DOES NOT WORK because OCPBUGS-18386, modified is false",
			existing: restrictedv2.DeepCopy,
			required: func() *securityv1.SecurityContextConstraints {
				scc := restrictedv2.DeepCopy()
				scc.Volumes = []securityv1.FSType{"configMap"}
				return scc
			},
			expected: restrictedv2.DeepCopy,
			expectedAPICalls: []kubetesting.Action{
				getSCCAction("restricted-v2"),
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			newClient := securityfake.NewSimpleClientset()
			// We cannot simply initialize the client because initialization is broken for SCC
			// https://github.com/openshift/client-go/issues/244
			if tc.existing != nil {
				if _, err := newClient.SecurityV1().SecurityContextConstraints().Create(context.Background(), tc.existing(), metav1.CreateOptions{}); err != nil {
					t.Fatalf("failed to setup fake client with existing SCCs: %v", err)
				}
				newClient.ClearActions()
			}
			actual, modified, err := ApplySecurityContextConstraintsv1(context.Background(), newClient.SecurityV1(), tc.required(), true)
			if tc.expectedErr != nil {
				if err == nil {
					t.Fatalf("expected error %v, got nil", tc.expectedErr)
				}
				if err.Error() != tc.expectedErr.Error() {
					t.Fatalf("expected error %v, got %v", tc.expectedErr, err)
				}
			}
			if tc.expectedErr == nil && err != nil {
				t.Fatalf("expected no error, got %v", err)
			}

			if diff := cmp.Diff(tc.expectedAPICalls, newClient.Actions()); diff != "" {
				t.Errorf("API calls differ from expected:\n%s", diff)
			}

			if tc.expectedModified != modified {
				t.Errorf("expected modified %v, got %v", tc.expectedModified, modified)
			}
			if diff := cmp.Diff(tc.expected(), actual); diff != "" {
				t.Errorf("SCC differs from expected:\n%s", diff)
			}
		})
	}
}

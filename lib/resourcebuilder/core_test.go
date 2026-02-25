package resourcebuilder

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"testing"

	configv1 "github.com/openshift/api/config/v1"
	fakeconfigclientv1 "github.com/openshift/client-go/config/clientset/versioned/fake"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/yaml"
)

const (
	genericOperatorConfigFullFieldsTLS12 = `apiVersion: operator.openshift.io/v1alpha1
kind: GenericOperatorConfig
servingInfo:
  bindAddress: 0.0.0.0:8443
  bindNetwork: tcp4
  certFile: /var/serving-cert/tls.crt
  keyFile: /var/serving-cert/tls.key
  cipherSuites:
  - TLS_ECDHE_ECDSA_WITH_AES_128_GCM_SHA256
  - TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256
  minTLSVersion: VersionTLS12
`
	genericOperatorConfigFullFieldsTLS13 = `apiVersion: operator.openshift.io/v1alpha1
kind: GenericOperatorConfig
servingInfo:
  bindAddress: 0.0.0.0:8443
  bindNetwork: tcp4
  certFile: /var/serving-cert/tls.crt
  keyFile: /var/serving-cert/tls.key
  cipherSuites:
  - TLS_ECDHE_ECDSA_WITH_AES_128_GCM_SHA256
  - TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256
  minTLSVersion: VersionTLS13
`
)

// makeConfigMap is a helper to create a ConfigMap with optional annotation and data
func makeConfigMap(name, namespace string, hasAnnotation bool, data map[string]string) *corev1.ConfigMap {
	cm := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		Data: data,
	}
	if hasAnnotation {
		cm.Annotations = map[string]string{
			ConfigMapInjectTLSAnnotation: "true",
		}
	}
	return cm
}

// makePodJSON is a helper to create a Pod object marshalled as JSON
func makePodJSON() string {
	pod := &corev1.Pod{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "v1",
			Kind:       "Pod",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-pod",
		},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				{
					Name:  "test-container",
					Image: "test-image:latest",
				},
			},
		},
	}
	data, _ := json.Marshal(pod)
	return string(data)
}

// makeAPIServerConfig is a helper to create an APIServer config with TLS profile
func makeAPIServerConfig() *configv1.APIServer {
	return &configv1.APIServer{
		ObjectMeta: metav1.ObjectMeta{
			Name: "cluster",
		},
		Spec: configv1.APIServerSpec{
			ClientCA: configv1.ConfigMapNameReference{
				Name: "client-ca",
			},
			TLSSecurityProfile: &configv1.TLSSecurityProfile{
				Type: configv1.TLSProfileCustomType,
				Custom: &configv1.CustomTLSProfile{
					TLSProfileSpec: configv1.TLSProfileSpec{
						Ciphers: []string{
							"ECDHE-ECDSA-AES128-GCM-SHA256",
							"ECDHE-RSA-AES128-GCM-SHA256",
						},
						MinTLSVersion: configv1.VersionTLS13,
					},
				},
			},
		},
	}
}

// validateGenericOperatorConfigUnchanged validates that a GenericOperatorConfig remains unchanged
// by performing a deep equal comparison with the original
func validateGenericOperatorConfigUnchanged(original, modified *corev1.ConfigMap) error {
	if !reflect.DeepEqual(original, modified) {
		return fmt.Errorf("ConfigMap was modified when it should not have been.\nOriginal: %+v\nModified: %+v", original, modified)
	}
	return nil
}

// validateGenericOperatorConfigTLSInjected validates that TLS settings were injected into GenericOperatorConfig
func validateGenericOperatorConfigTLSInjected(original, modified *corev1.ConfigMap, fieldName string) error {
	// Verify the field is still present
	configYAML, ok := modified.Data[fieldName]
	if !ok {
		return fmt.Errorf("%s was removed from ConfigMap", fieldName)
	}

	// Parse YAML into unstructured map
	var obj map[string]interface{}
	if err := yaml.Unmarshal([]byte(configYAML), &obj); err != nil {
		return fmt.Errorf("failed to unmarshal %s: %v", fieldName, err)
	}

	// Verify it is a GenericOperatorConfig
	kind, found, err := unstructured.NestedString(obj, "kind")
	if err != nil {
		return fmt.Errorf("failed to get kind field: %v", err)
	}
	if !found || kind != "GenericOperatorConfig" {
		return fmt.Errorf("expected kind GenericOperatorConfig, got %s", kind)
	}

	// Verify apiVersion
	apiVersion, found, err := unstructured.NestedString(obj, "apiVersion")
	if err != nil {
		return fmt.Errorf("failed to get apiVersion field: %v", err)
	}
	if !found || apiVersion != "operator.openshift.io/v1alpha1" {
		return fmt.Errorf("expected apiVersion operator.openshift.io/v1alpha1, got %s", apiVersion)
	}

	// Verify minTLSVersion was injected (should be VersionTLS13 from APIServer)
	minTLSVersion, found, err := unstructured.NestedString(obj, "servingInfo", "minTLSVersion")
	if err != nil {
		return fmt.Errorf("failed to get servingInfo.minTLSVersion: %v", err)
	}
	if !found {
		return fmt.Errorf("servingInfo.minTLSVersion not found")
	}
	if minTLSVersion != "VersionTLS13" {
		return fmt.Errorf("expected minTLSVersion VersionTLS13, got %s", minTLSVersion)
	}

	// Verify TLS cipher suites were injected from APIServer
	// The APIServer TLSSecurityProfile has these ciphers which get converted to IANA format:
	// ECDHE-ECDSA-AES128-GCM-SHA256 -> TLS_ECDHE_ECDSA_WITH_AES_128_GCM_SHA256
	// ECDHE-RSA-AES128-GCM-SHA256 -> TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256
	cipherSuites, found, err := unstructured.NestedStringSlice(obj, "servingInfo", "cipherSuites")
	if err != nil {
		return fmt.Errorf("failed to get servingInfo.cipherSuites: %v", err)
	}
	if !found {
		return fmt.Errorf("servingInfo.cipherSuites not found")
	}

	// Verify expected ciphers are present
	expectedCiphers := []string{
		"TLS_ECDHE_ECDSA_WITH_AES_128_GCM_SHA256",
		"TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256",
	}
	for _, expectedCipher := range expectedCiphers {
		found := false
		for _, cipher := range cipherSuites {
			if cipher == expectedCipher {
				found = true
				break
			}
		}
		if !found {
			return fmt.Errorf("expected cipher %s not found in cipherSuites: %v", expectedCipher, cipherSuites)
		}
	}

	return nil
}

// makeConfigMapWithGenericOperatorConfigKind creates a ConfigMap with GenericOperatorConfig
func makeConfigMapWithGenericOperatorConfigKind() *corev1.ConfigMap {
	return &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-config",
			Namespace: "test-ns",
			Annotations: map[string]string{
				"include.release.openshift.io/self-managed-high-availability": "true",
				"include.release.openshift.io/single-node-developer":          "true",
				ConfigMapInjectTLSAnnotation:                                  "true",
			},
		},
		Data: map[string]string{
			"config.yaml": `apiVersion: operator.openshift.io/v1alpha1
kind: GenericOperatorConfig
servingInfo:
  minTLSVersion: VersionTLS12
  cipherSuites:
  - TLS_ECDHE_ECDSA_WITH_AES_128_GCM_SHA256
  - TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256
  - TLS_ECDHE_ECDSA_WITH_AES_256_GCM_SHA384
  - TLS_ECDHE_RSA_WITH_AES_256_GCM_SHA384
  - TLS_ECDHE_ECDSA_WITH_CHACHA20_POLY1305_SHA256
`,
		},
	}
}

func TestModifyConfigMap(t *testing.T) {

	tests := []struct {
		name              string
		configMap         *corev1.ConfigMap
		apiServer         *configv1.APIServer
		expectError       bool
		expectModified    bool
		validateConfigMap func(t *testing.T, original, modified *corev1.ConfigMap)
	}{
		{
			name: "ConfigMap without annotation - should not modify",
			configMap: func() *corev1.ConfigMap {
				cm := makeConfigMapWithGenericOperatorConfigKind()
				// Remove the inject-tls annotation to test no-modification path
				delete(cm.Annotations, ConfigMapInjectTLSAnnotation)
				return cm
			}(),
			apiServer:      makeAPIServerConfig(),
			expectError:    false,
			expectModified: false,
			validateConfigMap: func(t *testing.T, original, modified *corev1.ConfigMap) {
				if err := validateGenericOperatorConfigUnchanged(original, modified); err != nil {
					t.Fatalf("validation failed: %v", err)
				}
			},
		},
		{
			name:           "ConfigMap with annotation but nil Data - should skip",
			configMap:      makeConfigMap("test-cm", "test-ns", true, nil),
			apiServer:      makeAPIServerConfig(),
			expectError:    false,
			expectModified: false,
			validateConfigMap: func(t *testing.T, original, modified *corev1.ConfigMap) {
				if modified.Data != nil {
					t.Errorf("Expected Data to remain nil, got %v", modified.Data)
				}
			},
		},
		{
			name:           "ConfigMap with annotation and empty Data - should skip",
			configMap:      makeConfigMap("test-cm", "test-ns", true, map[string]string{}),
			apiServer:      makeAPIServerConfig(),
			expectError:    false,
			expectModified: false,
			validateConfigMap: func(t *testing.T, original, modified *corev1.ConfigMap) {
				if len(modified.Data) != 0 {
					t.Errorf("Expected empty Data, got %v", modified.Data)
				}
			},
		},
		{
			name:           "ConfigMap with annotation and Data - APIServer not found",
			configMap:      makeConfigMapWithGenericOperatorConfigKind(),
			apiServer:      nil,
			expectError:    false,
			expectModified: false,
			validateConfigMap: func(t *testing.T, original, modified *corev1.ConfigMap) {
				if err := validateGenericOperatorConfigUnchanged(original, modified); err != nil {
					t.Fatalf("validation failed: %v", err)
				}
			},
		},
		{
			name: "ConfigMap with marshalled Pod object",
			configMap: makeConfigMap("test-cm", "test-ns", true, map[string]string{
				"pod.json": makePodJSON(),
				"other":    "data",
			}),
			apiServer:      makeAPIServerConfig(),
			expectError:    false,
			expectModified: true,
			validateConfigMap: func(t *testing.T, original, modified *corev1.ConfigMap) {
				// Verify the Pod JSON is still intact
				if _, ok := modified.Data["pod.json"]; !ok {
					t.Errorf("pod.json was removed from ConfigMap")
				}
				// Verify we can still unmarshal the Pod
				var pod corev1.Pod
				if err := json.Unmarshal([]byte(modified.Data["pod.json"]), &pod); err != nil {
					t.Errorf("Failed to unmarshal Pod from ConfigMap: %v", err)
				}
				if pod.Name != "test-pod" {
					t.Errorf("Pod name mismatch: got %s, want test-pod", pod.Name)
				}
			},
		},
		{
			name: "ConfigMap with annotation=false - should not modify",
			configMap: func() *corev1.ConfigMap {
				cm := makeConfigMapWithGenericOperatorConfigKind()
				// Set the inject-tls annotation to "false" to test no-modification path
				cm.Annotations[ConfigMapInjectTLSAnnotation] = "false"
				return cm
			}(),
			apiServer:      makeAPIServerConfig(),
			expectError:    false,
			expectModified: false,
			validateConfigMap: func(t *testing.T, original, modified *corev1.ConfigMap) {
				if err := validateGenericOperatorConfigUnchanged(original, modified); err != nil {
					t.Fatalf("validation failed: %v", err)
				}
			},
		},
		{
			name:           "ConfigMap with marshalled operator.openshift.io/v1alpha1 GenericOperatorConfig object",
			configMap:      makeConfigMapWithGenericOperatorConfigKind(),
			apiServer:      makeAPIServerConfig(),
			expectError:    false,
			expectModified: true,
			validateConfigMap: func(t *testing.T, original, modified *corev1.ConfigMap) {
				if err := validateGenericOperatorConfigTLSInjected(original, modified, "config.yaml"); err != nil {
					t.Fatalf("validation failed: %v", err)
				}
				// Verify namespace and name are preserved
				if modified.Namespace != "test-ns" {
					t.Errorf("Namespace was modified: got %s, want test-ns", modified.Namespace)
				}
				if modified.Name != "test-config" {
					t.Errorf("Name was modified: got %s, want test-config", modified.Name)
				}
			},
		},
		{
			name: "ConfigMap with mixed data - GenericOperatorConfig and plain text",
			configMap: &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "mixed-config",
					Namespace: "test-ns",
					Annotations: map[string]string{
						ConfigMapInjectTLSAnnotation: "true",
					},
				},
				Data: map[string]string{
					"config.yaml": `apiVersion: operator.openshift.io/v1alpha1
kind: GenericOperatorConfig
servingInfo:
  cipherSuites:
  - TLS_ECDHE_ECDSA_WITH_AES_128_GCM_SHA256
`,
					"plain-text":    "just some plain text",
					"another-value": "12345",
				},
			},
			apiServer:      makeAPIServerConfig(),
			expectError:    false,
			expectModified: true,
			validateConfigMap: func(t *testing.T, original, modified *corev1.ConfigMap) {
				if err := validateGenericOperatorConfigTLSInjected(original, modified, "config.yaml"); err != nil {
					t.Fatalf("validation failed: %v", err)
				}
				// Verify plain text entries remain unchanged
				if modified.Data["plain-text"] != "just some plain text" {
					t.Errorf("plain-text was modified: got %s", modified.Data["plain-text"])
				}
				if modified.Data["another-value"] != "12345" {
					t.Errorf("another-value was modified: got %s", modified.Data["another-value"])
				}
			},
		},
		{
			name: "ConfigMap with GenericOperatorConfig - verify YAML structure preservation",
			configMap: &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "structure-test",
					Namespace: "test-ns",
					Annotations: map[string]string{
						ConfigMapInjectTLSAnnotation: "true",
					},
				},
				Data: map[string]string{
					"config.yaml": genericOperatorConfigFullFieldsTLS12,
				},
			},
			apiServer:      makeAPIServerConfig(),
			expectError:    false,
			expectModified: true,
			validateConfigMap: func(t *testing.T, original, modified *corev1.ConfigMap) {
				configYAML, ok := modified.Data["config.yaml"]
				if !ok {
					t.Errorf("config.yaml was removed from ConfigMap")
					return
				}
				// Expected YAML after TLS injection (minTLSVersion updated to VersionTLS13 from APIServer)
				// kyaml preserves field order and formatting exactly
				if configYAML != genericOperatorConfigFullFieldsTLS13 {
					t.Errorf("Modified YAML does not match expected structure.\nExpected:\n%s\nGot:\n%s", genericOperatorConfigFullFieldsTLS13, configYAML)
				}
			},
		},
		{
			name: "ConfigMap with two GenericOperatorConfig fields",
			configMap: &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "multi-config",
					Namespace: "test-ns",
					Annotations: map[string]string{
						ConfigMapInjectTLSAnnotation: "true",
					},
				},
				Data: map[string]string{
					"config.yaml": `apiVersion: operator.openshift.io/v1alpha1
kind: GenericOperatorConfig
servingInfo:
  bindAddress: 0.0.0.0:8443
  cipherSuites:
  - TLS_ECDHE_ECDSA_WITH_AES_128_GCM_SHA256
  minTLSVersion: VersionTLS12
`,
					"operator-config.yaml": `apiVersion: operator.openshift.io/v1alpha1
kind: GenericOperatorConfig
servingInfo:
  bindAddress: 0.0.0.0:9443
  cipherSuites:
  - TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256
  minTLSVersion: VersionTLS11
`,
				},
			},
			apiServer:      makeAPIServerConfig(),
			expectError:    false,
			expectModified: true,
			validateConfigMap: func(t *testing.T, original, modified *corev1.ConfigMap) {
				// Validate first GenericOperatorConfig field
				if err := validateGenericOperatorConfigTLSInjected(original, modified, "config.yaml"); err != nil {
					t.Fatalf("validation failed for config.yaml: %v", err)
				}
				// Validate second GenericOperatorConfig field
				if err := validateGenericOperatorConfigTLSInjected(original, modified, "operator-config.yaml"); err != nil {
					t.Fatalf("validation failed for operator-config.yaml: %v", err)
				}
			},
		},
		{
			name: "ConfigMap with TLS already injected - same ciphers in different order should not modify",
			configMap: &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "already-injected",
					Namespace: "test-ns",
					Annotations: map[string]string{
						ConfigMapInjectTLSAnnotation: "true",
					},
				},
				Data: map[string]string{
					"config.yaml": genericOperatorConfigFullFieldsTLS13,
				},
			},
			apiServer: func() *configv1.APIServer {
				apiServer := makeAPIServerConfig()
				// Reverse the cipher order to test that same ciphers in different order don't trigger update
				apiServer.Spec.TLSSecurityProfile.Custom.Ciphers = []string{
					"ECDHE-RSA-AES128-GCM-SHA256",
					"ECDHE-ECDSA-AES128-GCM-SHA256",
				}
				return apiServer
			}(),
			expectError:    false,
			expectModified: false,
			validateConfigMap: func(t *testing.T, original, modified *corev1.ConfigMap) {
				// Validate through pure string comparison
				originalData := original.Data["config.yaml"]
				modifiedData := modified.Data["config.yaml"]
				if originalData != modifiedData {
					t.Errorf("ConfigMap data was modified when it should not have been.\nOriginal:\n%s\nModified:\n%s", originalData, modifiedData)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create fake client with or without APIServer config
			var objs []runtime.Object
			if tt.apiServer != nil {
				objs = append(objs, tt.apiServer)
			}
			fakeClient := fakeconfigclientv1.NewSimpleClientset(objs...)

			// Create builder with fake client
			b := &builder{
				configClientv1: fakeClient.ConfigV1(),
			}

			// Deep copy the ConfigMap before modification for comparison
			originalConfigMap := tt.configMap.DeepCopy()

			// Call modifyConfigMap
			ctx := context.Background()
			err := b.modifyConfigMap(ctx, tt.configMap)

			// Check error expectation
			if (err != nil) != tt.expectError {
				t.Errorf("modifyConfigMap() error = %v, expectError %v", err, tt.expectError)
				return
			}

			// Run custom validation
			if tt.validateConfigMap != nil {
				tt.validateConfigMap(t, originalConfigMap, tt.configMap)
			}
		})
	}
}

func TestModifyConfigMapAnnotationVariations(t *testing.T) {
	fakeClient := fakeconfigclientv1.NewSimpleClientset(makeAPIServerConfig())
	b := &builder{
		configClientv1: fakeClient.ConfigV1(),
	}

	tests := []struct {
		name          string
		annotations   map[string]string
		data          map[string]string
		shouldCallAPI bool
	}{
		{
			name:          "No annotations",
			annotations:   nil,
			data:          map[string]string{"key": "value"},
			shouldCallAPI: false,
		},
		{
			name: "Different annotation",
			annotations: map[string]string{
				"some.other.io/annotation": "true",
			},
			data:          map[string]string{"key": "value"},
			shouldCallAPI: false,
		},
		{
			name: "Correct annotation with value=true",
			annotations: map[string]string{
				ConfigMapInjectTLSAnnotation: "true",
			},
			data:          map[string]string{"key": "value"},
			shouldCallAPI: true,
		},
		{
			name: "Correct annotation with value=false",
			annotations: map[string]string{
				ConfigMapInjectTLSAnnotation: "false",
			},
			data:          map[string]string{"key": "value"},
			shouldCallAPI: false,
		},
		{
			name: "Correct annotation with empty value",
			annotations: map[string]string{
				ConfigMapInjectTLSAnnotation: "",
			},
			data:          map[string]string{"key": "value"},
			shouldCallAPI: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cm := &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name:        "test-cm",
					Namespace:   "test-ns",
					Annotations: tt.annotations,
				},
				Data: tt.data,
			}

			ctx := context.Background()
			err := b.modifyConfigMap(ctx, cm)
			if err != nil {
				t.Errorf("modifyConfigMap() unexpected error: %v", err)
			}

			// Verify data wasn't corrupted
			if tt.data != nil && cm.Data["key"] != "value" {
				t.Errorf("Original data was modified")
			}
		})
	}
}

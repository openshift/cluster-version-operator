package resourcebuilder

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
	"testing"
	"text/template"

	"github.com/google/go-cmp/cmp"
	"sigs.k8s.io/kustomize/kyaml/yaml"
	k8syaml "sigs.k8s.io/yaml"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"

	configv1 "github.com/openshift/api/config/v1"
	operatorv1alpha1 "github.com/openshift/api/operator/v1alpha1"
	fakeconfigclientv1 "github.com/openshift/client-go/config/clientset/versioned/fake"
	"github.com/openshift/library-go/pkg/crypto"
)

// makeGenericConfigYAML generates a generic config YAML string with the specified servingInfo fields.
func makeGenericConfigYAML(apiVersion, kind string, cipherSuites []string, minTLSVersion string) string {
	const tmpl = `apiVersion: {{ .APIVersion }}
kind: {{ .Kind }}
servingInfo:
  bindAddress: 0.0.0.0:8443
  bindNetwork: tcp4
  certFile: /var/serving-cert/tls.crt
  keyFile: /var/serving-cert/tls.key
{{- if eq (len .CipherSuites) 0 }}
  cipherSuites: []
{{- else }}
  cipherSuites:
{{- range .CipherSuites }}
  - {{ . }}
{{- end }}
{{- end }}
{{- if eq .MinTLSVersion "" }}
  minTLSVersion: ""
{{- else }}
  minTLSVersion: {{ .MinTLSVersion }}
{{- end }}
`

	data := map[string]interface{}{
		"APIVersion":    apiVersion,
		"Kind":          kind,
		"CipherSuites":  cipherSuites,
		"MinTLSVersion": minTLSVersion,
	}

	t := template.Must(template.New("config").Parse(tmpl))
	var buf bytes.Buffer
	if err := t.Execute(&buf, data); err != nil {
		panic(fmt.Sprintf("failed to execute template: %v", err))
	}

	return buf.String()
}

// makeGenericOperatorConfigYAML generates a GenericOperatorConfig YAML string
// with the specified servingInfo fields.
func makeGenericOperatorConfigYAML(cipherSuites []string, minTLSVersion string) string {
	return makeGenericConfigYAML(operatorv1alpha1.GroupVersion.String(), "GenericOperatorConfig", cipherSuites, minTLSVersion)
}

// makeGenericControllerConfigYAML generates a GenericControllerConfig YAML string
// with the specified servingInfo fields.
func makeGenericControllerConfigYAML(cipherSuites []string, minTLSVersion string) string {
	return makeGenericConfigYAML(configv1.GroupVersion.String(), "GenericControllerConfig", cipherSuites, minTLSVersion)
}

func getDefaultCipherSuitesSorted() []string {
	cipherSuites := crypto.CipherSuitesToNamesOrDie(crypto.DefaultCiphers())
	sort.Strings(cipherSuites)
	return cipherSuites
}

const (
	// TLS version constants used in test configurations
	tlsVersion12 = "VersionTLS12"
	tlsVersion13 = "VersionTLS13"

	genericOperatorConfigCMKey    = "config.yaml"
	genericOperatorConfigCMKey2   = "operator-config.yaml"
	genericControllerConfigCMKey  = "controller-config.yaml"
	genericControllerConfigCMKey2 = "controller-config2.yaml"
	someKey1                      = "plain-text"
	someKey2                      = "another-value"
	someValue1                    = "just some plain text"
	someValue2                    = "12345"
)

var (
	// testCipherSuites is a common cipher suite list used across multiple test cases
	testCipherSuites = []string{
		"TLS_ECDHE_ECDSA_WITH_AES_128_GCM_SHA256",
		"TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256",
	}

	testCipherSuites2 = []string{
		"TLS_RSA_WITH_AES_128_CBC_SHA256",
		"TLS_RSA_WITH_AES_128_GCM_SHA256",
		"TLS_RSA_WITH_AES_256_GCM_SHA384",
	}

	testOpenSSLCipherSuites = []string{
		"ECDHE-ECDSA-AES128-GCM-SHA256",
		"ECDHE-RSA-AES128-GCM-SHA256",
	}

	testOpenSSLCipherSuitesReversed = []string{
		"ECDHE-RSA-AES128-GCM-SHA256",
		"ECDHE-ECDSA-AES128-GCM-SHA256",
	}

	testOpenSSLCipherSuites2 = []string{
		"AES128-GCM-SHA256",
		"AES256-GCM-SHA384",
		"AES128-SHA256",
	}

	defaultCipherSuites = getDefaultCipherSuitesSorted()
)

// makeConfigMap is a helper to create a ConfigMap with optional annotation and data
func makeConfigMap(hasAnnotation bool, data map[string]string) *corev1.ConfigMap {
	cm := &corev1.ConfigMap{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "v1",
			Kind:       "ConfigMap",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-cm",
			Namespace: "test-ns",
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

// makeAPIServerConfig is a helper to create an APIServer config
// Optional apply function can be provided to modify the APIServer before returning
func makeAPIServerConfig(apply func(*configv1.APIServer)) *configv1.APIServer {
	apiServer := &configv1.APIServer{
		ObjectMeta: metav1.ObjectMeta{
			Name: "cluster",
		},
		Spec: configv1.APIServerSpec{
			ClientCA: configv1.ConfigMapNameReference{
				Name: "client-ca",
			},
		},
	}

	// Apply modifications if provided
	if apply != nil {
		apply(apiServer)
	}

	return apiServer
}

// withCustomTLSProfile returns an apply function that sets custom TLS profile
// with the specified ciphers and minTLSVersion
func withCustomTLSProfile(ciphers []string, minTLSVersion configv1.TLSProtocolVersion) func(*configv1.APIServer) {
	return func(apiServer *configv1.APIServer) {
		apiServer.Spec.TLSSecurityProfile = &configv1.TLSSecurityProfile{
			Type: configv1.TLSProfileCustomType,
			Custom: &configv1.CustomTLSProfile{
				TLSProfileSpec: configv1.TLSProfileSpec{
					Ciphers:       ciphers,
					MinTLSVersion: minTLSVersion,
				},
			},
		}
	}
}

// validateConfigMapsEqual validates both CMs are equal
func validateConfigMapsEqual(original, modified *corev1.ConfigMap) error {
	if !reflect.DeepEqual(original, modified) {
		return fmt.Errorf("ConfigMap was modified when it should not have been.\nOriginal: %+v\nModified: %+v", original, modified)
	}
	return nil
}

// validateGenericConfigTLSInjected validates that TLS settings were injected into a generic config
func validateGenericConfigTLSInjected(modified *corev1.ConfigMap, fieldName string, expectedKind, expectedAPIVersion string, expectedCiphers []string, expectedMinTLSVersion string) error {
	// Verify the field is still present
	configYAML, ok := modified.Data[fieldName]
	if !ok {
		return fmt.Errorf("%s was removed from ConfigMap", fieldName)
	}

	// Parse YAML into unstructured map
	var obj map[string]interface{}
	if err := k8syaml.Unmarshal([]byte(configYAML), &obj); err != nil {
		return fmt.Errorf("failed to unmarshal %s: %v", fieldName, err)
	}

	// Verify kind
	kind, found, err := unstructured.NestedString(obj, "kind")
	if err != nil {
		return fmt.Errorf("failed to get kind field: %v", err)
	}
	if !found || kind != expectedKind {
		return fmt.Errorf("expected kind %s, got %s", expectedKind, kind)
	}

	// Verify apiVersion
	apiVersion, found, err := unstructured.NestedString(obj, "apiVersion")
	if err != nil {
		return fmt.Errorf("failed to get apiVersion field: %v", err)
	}
	if !found || apiVersion != expectedAPIVersion {
		return fmt.Errorf("expected apiVersion %s, got %s", expectedAPIVersion, apiVersion)
	}

	// Verify minTLSVersion was injected
	minTLSVersion, found, err := unstructured.NestedString(obj, "servingInfo", "minTLSVersion")
	if err != nil {
		return fmt.Errorf("failed to get servingInfo.minTLSVersion: %v", err)
	}
	if !found {
		return fmt.Errorf("servingInfo.minTLSVersion not found")
	}
	if minTLSVersion != expectedMinTLSVersion {
		return fmt.Errorf("expected minTLSVersion %s, got %s", expectedMinTLSVersion, minTLSVersion)
	}

	// Verify TLS cipher suites were injected
	cipherSuites, found, err := unstructured.NestedStringSlice(obj, "servingInfo", "cipherSuites")
	if err != nil {
		return fmt.Errorf("failed to get servingInfo.cipherSuites: %v", err)
	}
	if !found {
		return fmt.Errorf("servingInfo.cipherSuites not found")
	}

	sort.Strings(expectedCiphers)
	sort.Strings(cipherSuites)
	if diff := cmp.Diff(expectedCiphers, cipherSuites); diff != "" {
		return fmt.Errorf("list of ciphers mismatch (-want +got):\n%s", diff)
	}

	return nil
}

// validateGenericOperatorConfigTLSInjected validates that TLS settings were injected into GenericOperatorConfig
func validateGenericOperatorConfigTLSInjected(modified *corev1.ConfigMap, fieldName string, expectedCiphers []string, expectedMinTLSVersion string) error {
	return validateGenericConfigTLSInjected(modified, fieldName, "GenericOperatorConfig", operatorv1alpha1.GroupVersion.String(), expectedCiphers, expectedMinTLSVersion)
}

// validateGenericControllerConfigTLSInjected validates that TLS settings were injected into GenericControllerConfig
func validateGenericControllerConfigTLSInjected(modified *corev1.ConfigMap, fieldName string, expectedCiphers []string, expectedMinTLSVersion string) error {
	return validateGenericConfigTLSInjected(modified, fieldName, "GenericControllerConfig", configv1.GroupVersion.String(), expectedCiphers, expectedMinTLSVersion)
}

func TestModifyConfigMap(t *testing.T) {

	tests := []struct {
		name              string
		configMap         *corev1.ConfigMap
		apiServer         *configv1.APIServer
		expectError       bool
		validateConfigMap func(t *testing.T, original, modified *corev1.ConfigMap)
	}{
		{
			name: "ConfigMap without annotation - no modification",
			configMap: makeConfigMap(false, map[string]string{
				genericOperatorConfigCMKey: makeGenericOperatorConfigYAML(testCipherSuites, tlsVersion12),
			}),
			// change both ciphers and the min TLS version
			apiServer:   makeAPIServerConfig(withCustomTLSProfile(append(testOpenSSLCipherSuites, "AES128-SHA256"), configv1.VersionTLS13)),
			expectError: false,
			validateConfigMap: func(t *testing.T, original, modified *corev1.ConfigMap) {
				if err := validateConfigMapsEqual(original, modified); err != nil {
					t.Fatalf("validation failed: %v", err)
				}
			},
		},
		{
			name: "ConfigMap with valid GenericOperatorConfig - annotation value not set to true - no modification",
			configMap: func() *corev1.ConfigMap {
				cm := makeConfigMap(true, map[string]string{
					genericOperatorConfigCMKey: makeGenericOperatorConfigYAML(testCipherSuites, tlsVersion12),
				})
				cm.Annotations[ConfigMapInjectTLSAnnotation] = "false"
				return cm
			}(),
			apiServer:   makeAPIServerConfig(withCustomTLSProfile(testOpenSSLCipherSuites, configv1.VersionTLS13)),
			expectError: false,
			validateConfigMap: func(t *testing.T, original, modified *corev1.ConfigMap) {
				if err := validateConfigMapsEqual(original, modified); err != nil {
					t.Fatalf("validation failed: %v", err)
				}
			},
		},
		{
			name: "ConfigMap with valid GenericOperatorConfig - annotation value empty - no modification",
			configMap: func() *corev1.ConfigMap {
				cm := makeConfigMap(true, map[string]string{
					genericOperatorConfigCMKey: makeGenericOperatorConfigYAML(testCipherSuites, tlsVersion12),
				})
				cm.Annotations[ConfigMapInjectTLSAnnotation] = ""
				return cm
			}(),
			apiServer:   makeAPIServerConfig(withCustomTLSProfile(testOpenSSLCipherSuites, configv1.VersionTLS13)),
			expectError: false,
			validateConfigMap: func(t *testing.T, original, modified *corev1.ConfigMap) {
				if err := validateConfigMapsEqual(original, modified); err != nil {
					t.Fatalf("validation failed: %v", err)
				}
			},
		},
		{
			name:      "ConfigMap with annotation but nil Data - no modification",
			configMap: makeConfigMap(true, nil),
			// change both ciphers and the min TLS version
			apiServer:   makeAPIServerConfig(withCustomTLSProfile(append(testOpenSSLCipherSuites, "AES128-SHA256"), configv1.VersionTLS13)),
			expectError: false,
			validateConfigMap: func(t *testing.T, original, modified *corev1.ConfigMap) {
				if modified.Data != nil {
					t.Errorf("Expected Data to remain nil, got %v", modified.Data)
				}
			},
		},
		{
			name:      "ConfigMap with annotation and empty Data - no modification",
			configMap: makeConfigMap(true, map[string]string{}),
			// change both ciphers and the min TLS version
			apiServer:   makeAPIServerConfig(withCustomTLSProfile(append(testOpenSSLCipherSuites, "AES128-SHA256"), configv1.VersionTLS13)),
			expectError: false,
			validateConfigMap: func(t *testing.T, original, modified *corev1.ConfigMap) {
				if len(modified.Data) != 0 {
					t.Errorf("Expected empty Data, got %v", modified.Data)
				}
			},
		},
		{
			name: "ConfigMap with annotation and Data - APIServer not found - default TLS configuration expected",
			configMap: makeConfigMap(true, map[string]string{
				genericOperatorConfigCMKey: makeGenericOperatorConfigYAML(testCipherSuites, tlsVersion12),
			}),
			apiServer:   nil,
			expectError: false,
			validateConfigMap: func(t *testing.T, original, modified *corev1.ConfigMap) {
				defaultTLSConfigMap := makeConfigMap(true, map[string]string{
					genericOperatorConfigCMKey: makeGenericOperatorConfigYAML(defaultCipherSuites, tlsVersion12),
				})
				if err := validateConfigMapsEqual(defaultTLSConfigMap, modified); err != nil {
					t.Fatalf("validation failed: %v", err)
				}
			},
		},
		{
			name: "ConfigMap with marshalled Pod object - no modification",
			configMap: makeConfigMap(true, map[string]string{
				"pod.json": makePodJSON(),
				"other":    "data",
			}),
			apiServer:   makeAPIServerConfig(withCustomTLSProfile(testOpenSSLCipherSuites, configv1.VersionTLS13)),
			expectError: false,
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
			name: "ConfigMap with valid GenericOperatorConfig - annotation=false - no modification",
			configMap: makeConfigMap(false, map[string]string{
				genericOperatorConfigCMKey: makeGenericOperatorConfigYAML(testCipherSuites, tlsVersion12),
			}),
			apiServer:   makeAPIServerConfig(withCustomTLSProfile(testOpenSSLCipherSuites, configv1.VersionTLS13)),
			expectError: false,
			validateConfigMap: func(t *testing.T, original, modified *corev1.ConfigMap) {
				if err := validateConfigMapsEqual(original, modified); err != nil {
					t.Fatalf("validation failed: %v", err)
				}
			},
		},
		{
			name: "ConfigMap with valid GenericOperatorConfig object - TLS injected",
			configMap: makeConfigMap(true, map[string]string{
				genericOperatorConfigCMKey: makeGenericOperatorConfigYAML(testCipherSuites, tlsVersion12),
			}),
			apiServer:   makeAPIServerConfig(withCustomTLSProfile(testOpenSSLCipherSuites2, configv1.VersionTLS13)),
			expectError: false,
			validateConfigMap: func(t *testing.T, original, modified *corev1.ConfigMap) {
				if err := validateGenericOperatorConfigTLSInjected(modified, genericOperatorConfigCMKey, testCipherSuites2, tlsVersion13); err != nil {
					t.Fatalf("validation failed: %v", err)
				}
				// Verify namespace and name are preserved
				if modified.Namespace != original.Namespace {
					t.Errorf("Namespace was modified: got %s, want test-ns", modified.Namespace)
				}
				if modified.Name != original.Name {
					t.Errorf("Name was modified: got %s, want test-config", modified.Name)
				}
			},
		},
		{
			name: "ConfigMap with mixed data - valid GenericOperatorConfig and plain text - TLS injected for one instance",
			configMap: makeConfigMap(true, map[string]string{
				genericOperatorConfigCMKey: makeGenericOperatorConfigYAML(testCipherSuites, tlsVersion12),
				someKey1:                   someValue1,
				someKey2:                   someValue2,
			}),
			apiServer:   makeAPIServerConfig(withCustomTLSProfile(testOpenSSLCipherSuites2, configv1.VersionTLS13)),
			expectError: false,
			validateConfigMap: func(t *testing.T, original, modified *corev1.ConfigMap) {
				if err := validateGenericOperatorConfigTLSInjected(modified, genericOperatorConfigCMKey, testCipherSuites2, tlsVersion13); err != nil {
					t.Fatalf("validation failed: %v", err)
				}
				// Verify plain text entries remain unchanged
				if modified.Data[someKey1] != someValue1 {
					t.Errorf("plain-text was modified: got %s", modified.Data[someKey1])
				}
				if modified.Data[someKey2] != someValue2 {
					t.Errorf("another-value was modified: got %s", modified.Data[someKey2])
				}
			},
		},
		{
			name: "ConfigMap with GenericOperatorConfig - verify YAML structure preservation - TLS injected",
			configMap: makeConfigMap(true, map[string]string{
				genericOperatorConfigCMKey: `apiVersion: operator.openshift.io/v1alpha1
kind: GenericOperatorConfig
servingInfo:
  bindNetwork: tcp4
  minTLSVersion: VersionTLS12
  certFile: /var/serving-cert/tls.crt
  keyFile: /var/serving-cert/tls.key
  bindAddress: 0.0.0.0:8443
  cipherSuites:
  - TLS_ECDHE_ECDSA_WITH_AES_128_GCM_SHA256
`,
			}),
			apiServer:   makeAPIServerConfig(withCustomTLSProfile(testOpenSSLCipherSuites2, configv1.VersionTLS13)),
			expectError: false,
			validateConfigMap: func(t *testing.T, original, modified *corev1.ConfigMap) {
				configYAML, ok := modified.Data[genericOperatorConfigCMKey]
				if !ok {
					t.Errorf("config.yaml was removed from ConfigMap")
					return
				}
				expectedYaml := `apiVersion: operator.openshift.io/v1alpha1
kind: GenericOperatorConfig
servingInfo:
  bindNetwork: tcp4
  minTLSVersion: VersionTLS13
  certFile: /var/serving-cert/tls.crt
  keyFile: /var/serving-cert/tls.key
  bindAddress: 0.0.0.0:8443
  cipherSuites:
  - TLS_RSA_WITH_AES_128_CBC_SHA256
  - TLS_RSA_WITH_AES_128_GCM_SHA256
  - TLS_RSA_WITH_AES_256_GCM_SHA384
`
				if configYAML != expectedYaml {
					t.Errorf("Modified YAML does not match expected structure.\nExpected:\n%s\nGot:\n%s", expectedYaml, configYAML)
				}
			},
		},
		{
			name: "ConfigMap with two GenericOperatorConfig fields - tls injected in both",
			configMap: makeConfigMap(true, map[string]string{
				genericOperatorConfigCMKey:  makeGenericOperatorConfigYAML(testCipherSuites, tlsVersion12),
				genericOperatorConfigCMKey2: makeGenericOperatorConfigYAML(testCipherSuites, tlsVersion12),
			}),
			apiServer:   makeAPIServerConfig(withCustomTLSProfile(testOpenSSLCipherSuites2, configv1.VersionTLS13)),
			expectError: false,
			validateConfigMap: func(t *testing.T, original, modified *corev1.ConfigMap) {
				// Validate the first GenericOperatorConfig field
				if err := validateGenericOperatorConfigTLSInjected(modified, genericOperatorConfigCMKey, testCipherSuites2, tlsVersion13); err != nil {
					t.Fatalf("validation failed for config.yaml: %v", err)
				}
				// Validate the second GenericOperatorConfig field
				if err := validateGenericOperatorConfigTLSInjected(modified, genericOperatorConfigCMKey2, testCipherSuites2, tlsVersion13); err != nil {
					t.Fatalf("validation failed for operator-config.yaml: %v", err)
				}
			},
		},
		{
			name: "ConfigMap with TLS already injected - same ciphers in different order with no modification",
			configMap: makeConfigMap(true, map[string]string{
				genericOperatorConfigCMKey: makeGenericOperatorConfigYAML(testCipherSuites, tlsVersion13),
			}),
			// Reverse the cipher order to test that same ciphers in different order don't trigger update
			apiServer:   makeAPIServerConfig(withCustomTLSProfile(testOpenSSLCipherSuitesReversed, configv1.VersionTLS13)),
			expectError: false,
			validateConfigMap: func(t *testing.T, original, modified *corev1.ConfigMap) {
				if diff := cmp.Diff(original.Data[genericOperatorConfigCMKey], modified.Data[genericOperatorConfigCMKey]); diff != "" {
					t.Errorf("ConfigMap YAML mismatch (-want +got):\n%s", diff)
				}
			},
		},
		{
			name: "ConfigMap with APIServer custom profile with empty ciphers and minTLSVersion - update to empty values",
			configMap: makeConfigMap(true, map[string]string{
				genericOperatorConfigCMKey: makeGenericOperatorConfigYAML(testCipherSuites, tlsVersion12),
			}),
			apiServer:   makeAPIServerConfig(withCustomTLSProfile([]string{}, "")),
			expectError: false,
			validateConfigMap: func(t *testing.T, original, modified *corev1.ConfigMap) {
				// When Custom profile has empty ciphers/minTLSVersion, it should fall back to default Intermediate profile
				defaultTLSConfigMap := makeConfigMap(true, map[string]string{
					genericOperatorConfigCMKey: makeGenericOperatorConfigYAML([]string{}, ""),
				})
				if err := validateConfigMapsEqual(defaultTLSConfigMap, modified); err != nil {
					t.Fatalf("validation failed: %v", err)
				}
			},
		},
		{
			name: "ConfigMap with APIServer custom profile with non-empty ciphers and empty minTLSVersion - update minTLSVersion to an empty value",
			configMap: makeConfigMap(true, map[string]string{
				genericOperatorConfigCMKey: makeGenericOperatorConfigYAML(testCipherSuites, tlsVersion12),
			}),
			apiServer:   makeAPIServerConfig(withCustomTLSProfile(testOpenSSLCipherSuites2, "")),
			expectError: false,
			validateConfigMap: func(t *testing.T, original, modified *corev1.ConfigMap) {
				expectedConfigMap := makeConfigMap(true, map[string]string{
					genericOperatorConfigCMKey: makeGenericOperatorConfigYAML(testCipherSuites2, ""),
				})
				if err := validateConfigMapsEqual(expectedConfigMap, modified); err != nil {
					t.Fatalf("validation failed: %v", err)
				}
			},
		},
		{
			name: "ConfigMap with APIServer custom profile with empty ciphers and non-empty minTLSVersion - update ciphers to an empty value",
			configMap: makeConfigMap(true, map[string]string{
				genericOperatorConfigCMKey: makeGenericOperatorConfigYAML(testCipherSuites, tlsVersion12),
			}),
			apiServer:   makeAPIServerConfig(withCustomTLSProfile([]string{}, tlsVersion12)),
			expectError: false,
			validateConfigMap: func(t *testing.T, original, modified *corev1.ConfigMap) {
				expectedConfigMap := makeConfigMap(true, map[string]string{
					genericOperatorConfigCMKey: makeGenericOperatorConfigYAML([]string{}, tlsVersion12),
				})
				if err := validateConfigMapsEqual(expectedConfigMap, modified); err != nil {
					t.Fatalf("validation failed: %v", err)
				}
			},
		},
		{
			name: "ConfigMap with valid GenericControllerConfig object - TLS injected",
			configMap: makeConfigMap(true, map[string]string{
				genericControllerConfigCMKey: makeGenericControllerConfigYAML(testCipherSuites, tlsVersion12),
			}),
			apiServer:   makeAPIServerConfig(withCustomTLSProfile(testOpenSSLCipherSuites2, configv1.VersionTLS13)),
			expectError: false,
			validateConfigMap: func(t *testing.T, original, modified *corev1.ConfigMap) {
				if err := validateGenericControllerConfigTLSInjected(modified, genericControllerConfigCMKey, testCipherSuites2, tlsVersion13); err != nil {
					t.Fatalf("validation failed: %v", err)
				}
			},
		},
		{
			name: "ConfigMap with both GenericOperatorConfig and GenericControllerConfig - TLS injected in both",
			configMap: makeConfigMap(true, map[string]string{
				genericOperatorConfigCMKey:   makeGenericOperatorConfigYAML(testCipherSuites, tlsVersion12),
				genericControllerConfigCMKey: makeGenericControllerConfigYAML(testCipherSuites, tlsVersion12),
			}),
			apiServer:   makeAPIServerConfig(withCustomTLSProfile(testOpenSSLCipherSuites2, configv1.VersionTLS13)),
			expectError: false,
			validateConfigMap: func(t *testing.T, original, modified *corev1.ConfigMap) {
				// Validate the GenericOperatorConfig field
				if err := validateGenericOperatorConfigTLSInjected(modified, genericOperatorConfigCMKey, testCipherSuites2, tlsVersion13); err != nil {
					t.Fatalf("validation failed for GenericOperatorConfig: %v", err)
				}
				// Validate the GenericControllerConfig field
				if err := validateGenericControllerConfigTLSInjected(modified, genericControllerConfigCMKey, testCipherSuites2, tlsVersion13); err != nil {
					t.Fatalf("validation failed for GenericControllerConfig: %v", err)
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
			fakeClient := fakeconfigclientv1.NewClientset(objs...)

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

func TestUpdateRNodeWithTLSSettings(t *testing.T) {
	tests := []struct {
		name         string
		inputYAML    string
		tlsConf      *tlsConfig
		expectedYAML string
		expectError  bool
	}{
		{
			name: "Delete both cipherSuites and minTLSVersion when not found",
			inputYAML: `apiVersion: operator.openshift.io/v1alpha1
kind: GenericOperatorConfig
servingInfo:
  bindAddress: 0.0.0.0:8443
  certFile: /var/serving-cert/tls.crt
  keyFile: /var/serving-cert/tls.key
  cipherSuites:
  - TLS_ECDHE_ECDSA_WITH_AES_128_GCM_SHA256
  minTLSVersion: VersionTLS12
`,
			tlsConf: &tlsConfig{
				minTLSVersion: optional[string]{found: false},
				cipherSuites:  optional[[]string]{found: false},
			},
			expectedYAML: `apiVersion: operator.openshift.io/v1alpha1
kind: GenericOperatorConfig
servingInfo:
  bindAddress: 0.0.0.0:8443
  certFile: /var/serving-cert/tls.crt
  keyFile: /var/serving-cert/tls.key
`,
		},
		{
			name: "Delete only cipherSuites when not found",
			inputYAML: `apiVersion: operator.openshift.io/v1alpha1
kind: GenericOperatorConfig
servingInfo:
  bindAddress: 0.0.0.0:8443
  cipherSuites:
  - TLS_ECDHE_ECDSA_WITH_AES_128_GCM_SHA256
  minTLSVersion: VersionTLS12
`,
			tlsConf: &tlsConfig{
				minTLSVersion: optional[string]{value: "VersionTLS13", found: true},
				cipherSuites:  optional[[]string]{found: false},
			},
			expectedYAML: `apiVersion: operator.openshift.io/v1alpha1
kind: GenericOperatorConfig
servingInfo:
  bindAddress: 0.0.0.0:8443
  minTLSVersion: VersionTLS13
`,
		},
		{
			name: "Delete only minTLSVersion when not found",
			inputYAML: `apiVersion: operator.openshift.io/v1alpha1
kind: GenericOperatorConfig
servingInfo:
  bindAddress: 0.0.0.0:8443
  cipherSuites:
  - TLS_ECDHE_ECDSA_WITH_AES_128_GCM_SHA256
  minTLSVersion: VersionTLS12
`,
			tlsConf: &tlsConfig{
				minTLSVersion: optional[string]{found: false},
				cipherSuites:  optional[[]string]{value: []string{"TLS_RSA_WITH_AES_128_GCM_SHA256"}, found: true},
			},
			expectedYAML: `apiVersion: operator.openshift.io/v1alpha1
kind: GenericOperatorConfig
servingInfo:
  bindAddress: 0.0.0.0:8443
  cipherSuites:
  - TLS_RSA_WITH_AES_128_GCM_SHA256
`,
		},
		{
			name: "Set both fields when found",
			inputYAML: `apiVersion: operator.openshift.io/v1alpha1
kind: GenericOperatorConfig
servingInfo:
  bindAddress: 0.0.0.0:8443
`,
			tlsConf: &tlsConfig{
				minTLSVersion: optional[string]{value: "VersionTLS13", found: true},
				cipherSuites:  optional[[]string]{value: []string{"TLS_RSA_WITH_AES_128_GCM_SHA256", "TLS_RSA_WITH_AES_256_GCM_SHA384"}, found: true},
			},
			expectedYAML: `apiVersion: operator.openshift.io/v1alpha1
kind: GenericOperatorConfig
servingInfo:
  bindAddress: 0.0.0.0:8443
  cipherSuites:
  - TLS_RSA_WITH_AES_128_GCM_SHA256
  - TLS_RSA_WITH_AES_256_GCM_SHA384
  minTLSVersion: VersionTLS13
`,
		},
		{
			name: "Set empty string for minTLSVersion when found but empty",
			inputYAML: `apiVersion: operator.openshift.io/v1alpha1
kind: GenericOperatorConfig
servingInfo:
  bindAddress: 0.0.0.0:8443
  minTLSVersion: VersionTLS12
`,
			tlsConf: &tlsConfig{
				minTLSVersion: optional[string]{value: "", found: true},
				cipherSuites:  optional[[]string]{value: []string{"TLS_RSA_WITH_AES_128_GCM_SHA256"}, found: true},
			},
			expectedYAML: `apiVersion: operator.openshift.io/v1alpha1
kind: GenericOperatorConfig
servingInfo:
  bindAddress: 0.0.0.0:8443
  minTLSVersion: ""
  cipherSuites:
  - TLS_RSA_WITH_AES_128_GCM_SHA256
`,
		},
		{
			name: "Set empty slice for cipherSuites when found but empty",
			inputYAML: `apiVersion: operator.openshift.io/v1alpha1
kind: GenericOperatorConfig
servingInfo:
  bindAddress: 0.0.0.0:8443
  cipherSuites:
  - TLS_ECDHE_ECDSA_WITH_AES_128_GCM_SHA256
`,
			tlsConf: &tlsConfig{
				minTLSVersion: optional[string]{value: "VersionTLS13", found: true},
				cipherSuites:  optional[[]string]{value: []string{}, found: true},
			},
			expectedYAML: `apiVersion: operator.openshift.io/v1alpha1
kind: GenericOperatorConfig
servingInfo:
  bindAddress: 0.0.0.0:8443
  cipherSuites: []
  minTLSVersion: VersionTLS13
`,
		},
		{
			name: "Set both fields to empty values when found but empty",
			inputYAML: `apiVersion: operator.openshift.io/v1alpha1
kind: GenericOperatorConfig
servingInfo:
  bindAddress: 0.0.0.0:8443
  cipherSuites:
  - TLS_ECDHE_ECDSA_WITH_AES_128_GCM_SHA256
  minTLSVersion: VersionTLS12
`,
			tlsConf: &tlsConfig{
				minTLSVersion: optional[string]{value: "", found: true},
				cipherSuites:  optional[[]string]{value: []string{}, found: true},
			},
			expectedYAML: `apiVersion: operator.openshift.io/v1alpha1
kind: GenericOperatorConfig
servingInfo:
  bindAddress: 0.0.0.0:8443
  cipherSuites: []
  minTLSVersion: ""
`,
		},
		{
			name: "Minimal config with only kind and apiVersion - nothing found - no fields added",
			inputYAML: `apiVersion: operator.openshift.io/v1alpha1
kind: GenericOperatorConfig
`,
			tlsConf: &tlsConfig{
				minTLSVersion: optional[string]{found: false},
				cipherSuites:  optional[[]string]{found: false},
			},
			expectedYAML: `apiVersion: operator.openshift.io/v1alpha1
kind: GenericOperatorConfig
servingInfo: {}
`,
		},
		{
			name: "Minimal config with only kind and apiVersion - everything found - fields added",
			inputYAML: `apiVersion: operator.openshift.io/v1alpha1
kind: GenericOperatorConfig
`,
			tlsConf: &tlsConfig{
				minTLSVersion: optional[string]{value: "VersionTLS13", found: true},
				cipherSuites:  optional[[]string]{value: []string{"TLS_RSA_WITH_AES_128_GCM_SHA256", "TLS_RSA_WITH_AES_256_GCM_SHA384"}, found: true},
			},
			expectedYAML: `apiVersion: operator.openshift.io/v1alpha1
kind: GenericOperatorConfig
servingInfo:
  cipherSuites:
  - TLS_RSA_WITH_AES_128_GCM_SHA256
  - TLS_RSA_WITH_AES_256_GCM_SHA384
  minTLSVersion: VersionTLS13
`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Parse input YAML
			rnode, err := yaml.Parse(tt.inputYAML)
			if err != nil {
				t.Fatalf("failed to parse input YAML: %v", err)
			}

			// Call updateRNodeWithTLSSettings
			err = updateRNodeWithTLSSettings(rnode, tt.tlsConf)

			// Check error expectation
			if (err != nil) != tt.expectError {
				t.Errorf("updateRNodeWithTLSSettings() error = %v, expectError %v", err, tt.expectError)
				return
			}

			if err == nil {
				// Convert back to YAML string
				resultYAML, err := rnode.String()
				if err != nil {
					t.Fatalf("failed to convert rnode to string: %v", err)
				}

				// Compare YAML output
				if resultYAML != tt.expectedYAML {
					t.Errorf("YAML mismatch.\nExpected:\n%s\nGot:\n%s", tt.expectedYAML, resultYAML)
				}
			}
		})
	}
}

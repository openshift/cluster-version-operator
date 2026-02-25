package resourcebuilder

import (
	"context"
	"fmt"
	"testing"

	"sigs.k8s.io/yaml"

	configv1 "github.com/openshift/api/config/v1"
	fakeconfigclientv1 "github.com/openshift/client-go/config/clientset/versioned/fake"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	fakekubernetes "k8s.io/client-go/kubernetes/fake"
)

const (
	noGenericOperatorConfigYAML = `apiVersion: v1
kind: ConfigMap
metadata:
  name: test
`
)

// TestModifyConfigMapWithBuilder tests modifyConfigMap by creating a builder with proper initialization
func TestModifyConfigMapWithBuilder(t *testing.T) {

	tests := []struct {
		name       string
		configMap  *corev1.ConfigMap
		apiServer  *configv1.APIServer
		expectYAML string // Expected YAML content after modification
	}{
		{
			name: "ConfigMap with GenericOperatorConfig - TLS injection",
			configMap: makeConfigMap(true, map[string]string{
				"config.yaml": makeGenericOperatorConfigYAML(testCipherSuites, tlsVersion12),
			}),
			apiServer:  makeAPIServerConfig(withCustomTLSProfile(testOpenSSLCipherSuites2, configv1.VersionTLS13)),
			expectYAML: makeGenericOperatorConfigYAML(testCipherSuites2, tlsVersion13),
		},
		{
			name: "ConfigMap without annotation - no modification",
			configMap: makeConfigMap(false, map[string]string{
				"config.yaml": makeGenericOperatorConfigYAML(testCipherSuites, tlsVersion12),
			}),
			apiServer:  makeAPIServerConfig(withCustomTLSProfile(testOpenSSLCipherSuites2, configv1.VersionTLS13)),
			expectYAML: makeGenericOperatorConfigYAML(testCipherSuites, tlsVersion12),
		},
		{
			name: "ConfigMap with non-GenericOperatorConfig - no modification",
			configMap: makeConfigMap(true, map[string]string{
				"config.yaml": noGenericOperatorConfigYAML,
			}),
			apiServer:  makeAPIServerConfig(withCustomTLSProfile(testOpenSSLCipherSuites, configv1.VersionTLS13)),
			expectYAML: noGenericOperatorConfigYAML,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create fake config client with or without APIServer config
			var configObjs []runtime.Object
			if tt.apiServer != nil {
				configObjs = append(configObjs, tt.apiServer)
				fmt.Printf("tt.apiServer: %v\n", tt.apiServer.Spec.TLSSecurityProfile.Custom.TLSProfileSpec)
			}
			fakeConfigClient := fakeconfigclientv1.NewSimpleClientset(configObjs...)

			// Create fake kubernetes client
			fakeKubeClient := fakekubernetes.NewSimpleClientset()

			// Marshal ConfigMap to YAML bytes
			configMapYAML, err := yaml.Marshal(tt.configMap)
			if err != nil {
				t.Fatalf("Failed to marshal ConfigMap: %v", err)
			}

			// Create builder with fake clients
			b := &builder{
				configClientv1: fakeConfigClient.ConfigV1(),
				coreClientv1:   fakeKubeClient.CoreV1(),
				raw:            configMapYAML,
			}

			// Call Do()
			ctx := context.Background()
			err = b.Do(ctx)
			if err != nil {
				t.Fatalf("Do() unexpected error: %v", err)
			}

			// Retrieve the applied ConfigMap from the fake client
			appliedConfigMap, err := fakeKubeClient.CoreV1().ConfigMaps(tt.configMap.Namespace).Get(ctx, tt.configMap.Name, metav1.GetOptions{})
			if err != nil {
				t.Fatalf("Failed to get applied ConfigMap: %v", err)
			}

			// Verify the result matches expected YAML
			modifiedYAML := appliedConfigMap.Data["config.yaml"]
			if modifiedYAML != tt.expectYAML {
				t.Errorf("ConfigMap YAML does not match expected.\nExpected:\n%s\nGot:\n%s", tt.expectYAML, modifiedYAML)
			}
		})
	}
}

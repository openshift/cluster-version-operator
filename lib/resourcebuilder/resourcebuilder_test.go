package resourcebuilder

import (
	"context"
	"fmt"
	"testing"

	"github.com/google/go-cmp/cmp"
	"sigs.k8s.io/yaml"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	fakekubernetes "k8s.io/client-go/kubernetes/fake"

	configv1 "github.com/openshift/api/config/v1"
	fakeconfigclientv1 "github.com/openshift/client-go/config/clientset/versioned/fake"
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
		name        string
		configMap   *corev1.ConfigMap
		apiServer   *configv1.APIServer
		expectYAML  string // Expected YAML content after modification
		expectError bool   // Whether an error is expected from Do()
	}{
		{
			name: "ConfigMap with GenericOperatorConfig - TLS injection",
			configMap: makeConfigMap(true, map[string]string{
				genericOperatorConfigCMKey: makeGenericOperatorConfigYAML(testCipherSuites, tlsVersion12),
			}),
			apiServer:  makeAPIServerConfig(withCustomTLSProfile(testOpenSSLCipherSuites2, configv1.VersionTLS13)),
			expectYAML: makeGenericOperatorConfigYAML(testCipherSuites2, tlsVersion13),
		},
		{
			name: "ConfigMap without annotation - no modification",
			configMap: makeConfigMap(false, map[string]string{
				genericOperatorConfigCMKey: makeGenericOperatorConfigYAML(testCipherSuites, tlsVersion12),
			}),
			apiServer:  makeAPIServerConfig(withCustomTLSProfile(testOpenSSLCipherSuites2, configv1.VersionTLS13)),
			expectYAML: makeGenericOperatorConfigYAML(testCipherSuites, tlsVersion12),
		},
		{
			name: "ConfigMap with non-GenericOperatorConfig - no modification",
			configMap: makeConfigMap(true, map[string]string{
				genericOperatorConfigCMKey: noGenericOperatorConfigYAML,
			}),
			apiServer:  makeAPIServerConfig(withCustomTLSProfile(testOpenSSLCipherSuites, configv1.VersionTLS13)),
			expectYAML: noGenericOperatorConfigYAML,
		},
		{
			name: "ConfigMap with annotation - APIServer not found - error expected",
			configMap: makeConfigMap(true, map[string]string{
				genericOperatorConfigCMKey: makeGenericOperatorConfigYAML(testCipherSuites, tlsVersion13),
			}),
			expectYAML:  makeGenericOperatorConfigYAML(testCipherSuites, tlsVersion13),
			expectError: true,
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
			fakeConfigClient := fakeconfigclientv1.NewClientset(configObjs...)

			// Create fake kubernetes client
			fakeKubeClient := fakekubernetes.NewClientset()

			// Create the ConfigMap in the fake client before running the builder
			ctx := context.Background()
			_, err := fakeKubeClient.CoreV1().ConfigMaps(tt.configMap.Namespace).Create(ctx, tt.configMap, metav1.CreateOptions{})
			if err != nil {
				t.Fatalf("Failed to create ConfigMap: %v", err)
			}

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
			err = b.Do(ctx)

			// Check error expectation
			if (err != nil) != tt.expectError {
				t.Fatalf("Do() error = %v, expectError %v", err, tt.expectError)
			}

			// ConfigMap should always be retrievable, regardless of error
			appliedConfigMap, err := fakeKubeClient.CoreV1().ConfigMaps(tt.configMap.Namespace).Get(ctx, tt.configMap.Name, metav1.GetOptions{})
			if err != nil {
				t.Fatalf("Failed to get applied ConfigMap: %v", err)
			}

			// Verify the result matches expected YAML
			// When Do() errors, the ConfigMap should remain unchanged (expectYAML has the original values)
			// When Do() succeeds, the ConfigMap should have modified values (expectYAML has the modified values)
			modifiedYAML := appliedConfigMap.Data[genericOperatorConfigCMKey]
			if diff := cmp.Diff(tt.expectYAML, modifiedYAML); diff != "" {
				t.Errorf("ConfigMap YAML mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

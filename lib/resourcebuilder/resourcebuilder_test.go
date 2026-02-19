package resourcebuilder

import (
	"context"
	"testing"

	configv1 "github.com/openshift/api/config/v1"
	fakeconfigclientv1 "github.com/openshift/client-go/config/clientset/versioned/fake"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	fakekubernetes "k8s.io/client-go/kubernetes/fake"
	"sigs.k8s.io/yaml"
)

const (
	genericOperatorConfigYAML = `apiVersion: operator.openshift.io/v1alpha1
kind: GenericOperatorConfig
servingInfo:
  bindAddress: 0.0.0.0:8443
`
	noGenericOperatorConfigYAML = `apiVersion: v1
kind: ConfigMap
metadata:
  name: test
`
)

// makeTestConfigMap creates a ConfigMap with TypeMeta populated for testing
// The apply function can customize the ConfigMap (e.g., set Data, Annotations)
func makeTestConfigMap(apply func(*corev1.ConfigMap)) *corev1.ConfigMap {
	cm := &corev1.ConfigMap{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "v1",
			Kind:       "ConfigMap",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-config",
			Namespace: "test-ns",
		},
	}
	if apply != nil {
		apply(cm)
	}
	return cm
}

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
			configMap: makeTestConfigMap(func(cm *corev1.ConfigMap) {
				cm.Data = map[string]string{
					"config.yaml": genericOperatorConfigYAML,
				}
				cm.Annotations = map[string]string{
					ConfigMapInjectTLSAnnotation: "true",
				}
			}),
			apiServer: makeAPIServerConfig(),
			expectYAML: `apiVersion: operator.openshift.io/v1alpha1
kind: GenericOperatorConfig
servingInfo:
  bindAddress: 0.0.0.0:8443
  cipherSuites:
  - TLS_ECDHE_ECDSA_WITH_AES_128_GCM_SHA256
  - TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256
  minTLSVersion: VersionTLS13
`,
		},
		{
			name: "ConfigMap without annotation - no modification",
			configMap: makeTestConfigMap(func(cm *corev1.ConfigMap) {
				cm.Data = map[string]string{
					"config.yaml": genericOperatorConfigYAML,
				}
			}),
			apiServer:  makeAPIServerConfig(),
			expectYAML: genericOperatorConfigYAML,
		},
		{
			name: "ConfigMap with non-GenericOperatorConfig - no modification",
			configMap: makeTestConfigMap(func(cm *corev1.ConfigMap) {
				cm.Data = map[string]string{
					"config.yaml": noGenericOperatorConfigYAML,
				}
				cm.Annotations = map[string]string{
					ConfigMapInjectTLSAnnotation: "true",
				}
			}),
			apiServer:  makeAPIServerConfig(),
			expectYAML: noGenericOperatorConfigYAML,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create fake config client with or without APIServer config
			var configObjs []runtime.Object
			if tt.apiServer != nil {
				configObjs = append(configObjs, tt.apiServer)
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

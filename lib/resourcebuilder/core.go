package resourcebuilder

import (
	"context"
	"slices"
	"sort"

	configv1 "github.com/openshift/api/config/v1"
	configclientv1 "github.com/openshift/client-go/config/clientset/versioned/typed/config/v1"
	configlistersv1 "github.com/openshift/client-go/config/listers/config/v1"
	"github.com/openshift/library-go/pkg/operator/configobserver/apiserver"
	"github.com/openshift/library-go/pkg/operator/events"
	"github.com/openshift/library-go/pkg/operator/resourcesynccontroller"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/client-go/tools/cache"
	"k8s.io/klog/v2"
	"k8s.io/utils/clock"
	"sigs.k8s.io/kustomize/kyaml/yaml"
)

const (
	// ConfigMapInjectTLSAnnotation is the annotation key that triggers TLS injection into ConfigMaps
	ConfigMapInjectTLSAnnotation = "config.openshift.io/inject-tls"
)

func (b *builder) modifyConfigMap(ctx context.Context, cm *corev1.ConfigMap) error {
	// Check for TLS injection annotation
	if value, ok := cm.Annotations[ConfigMapInjectTLSAnnotation]; !ok || value != "true" {
		return nil
	}

	klog.V(2).Infof("ConfigMap %s/%s has %s annotation set to true", cm.Namespace, cm.Name, ConfigMapInjectTLSAnnotation)

	// Empty data, nothing to inject into
	if cm.Data == nil {
		klog.V(2).Infof("ConfigMap %s/%s has empty data, skipping", cm.Namespace, cm.Name)
		return nil
	}

	// Observe TLS configuration from APIServer
	minTLSVersion, minTLSFound, cipherSuites, ciphersFound := b.observeTLSConfiguration(ctx, cm)

	if !minTLSFound && !ciphersFound {
		klog.V(2).Infof("ConfigMap %s/%s: no TLS configuration found, skipping", cm.Namespace, cm.Name)
		return nil
	}

	// Process each data entry that contains GenericOperatorConfig
	for key, value := range cm.Data {
		// Parse YAML into RNode to preserve formatting and field order
		rnode, err := yaml.Parse(value)
		if err != nil {
			// Not valid YAML, skip this entry
			continue
		}

		// Check if this is a GenericOperatorConfig by checking the kind field
		kind, err := rnode.GetString("kind")
		if err != nil || kind != "GenericOperatorConfig" {
			// Not a GenericOperatorConfig, skip this entry
			continue
		}

		klog.V(2).Infof("ConfigMap %s/%s processing GenericOperatorConfig in key %s", cm.Namespace, cm.Name, key)

		// Inject TLS settings into the GenericOperatorConfig while preserving structure
		if err := updateRNodeWithTLSSettings(rnode, minTLSVersion, minTLSFound, cipherSuites, ciphersFound); err != nil {
			return err
		}

		// Marshal the modified RNode back to YAML
		modifiedYAML, err := rnode.String()
		if err != nil {
			return err
		}

		// Update the ConfigMap data entry with the modified YAML
		cm.Data[key] = modifiedYAML
		klog.V(2).Infof("ConfigMap %s/%s updated GenericOperatorConfig in key %s with %d ciphers and minTLSVersion=%s",
			cm.Namespace, cm.Name, key, len(cipherSuites), minTLSVersion)
	}

	klog.V(2).Infof("APIServer config available for ConfigMap %s/%s TLS injection", cm.Namespace, cm.Name)

	return nil
}

// observeTLSConfiguration retrieves TLS configuration from the APIServer cluster CR
// using ObserveTLSSecurityProfile and extracts minTLSVersion and cipherSuites.
func (b *builder) observeTLSConfiguration(ctx context.Context, cm *corev1.ConfigMap) (minTLSVersion string, minTLSFound bool, cipherSuites []string, ciphersFound bool) {
	// First check if the APIServer cluster resource exists
	_, err := b.configClientv1.APIServers().Get(ctx, "cluster", metav1.GetOptions{})
	if err != nil {
		klog.V(2).Infof("ConfigMap %s/%s: APIServer cluster resource not found, skipping TLS injection: %v", cm.Namespace, cm.Name, err)
		return "", false, nil, false
	}

	// Create a lister adapter for ObserveTLSSecurityProfile
	lister := &apiServerListerAdapter{
		client: b.configClientv1.APIServers(),
		ctx:    ctx,
	}
	listers := &configObserverListers{
		apiServerLister: lister,
	}

	// Create an in-memory event recorder that doesn't send events to the API server
	recorder := events.NewInMemoryRecorder("configmap-tls-injection", clock.RealClock{})

	// Call ObserveTLSSecurityProfile to get TLS configuration
	observedConfig, errs := apiserver.ObserveTLSSecurityProfile(listers, recorder, map[string]interface{}{})
	if len(errs) > 0 {
		// Log errors but continue - ObserveTLSSecurityProfile is tolerant of missing config
		for _, err := range errs {
			klog.V(2).Infof("ConfigMap %s/%s: error observing TLS profile: %v", cm.Namespace, cm.Name, err)
		}
	}

	// Extract the TLS settings from the observed config
	minTLSVersion, minTLSFound, _ = unstructured.NestedString(observedConfig, "servingInfo", "minTLSVersion")
	cipherSuites, ciphersFound, _ = unstructured.NestedStringSlice(observedConfig, "servingInfo", "cipherSuites")

	// Sort cipher suites for consistent comparison
	if ciphersFound && len(cipherSuites) > 0 {
		sort.Strings(cipherSuites)
	}

	return minTLSVersion, minTLSFound, cipherSuites, ciphersFound
}

// updateRNodeWithTLSSettings injects TLS settings into a GenericOperatorConfig RNode while preserving structure
// cipherSuites is expected to be sorted
func updateRNodeWithTLSSettings(rnode *yaml.RNode, minTLSVersion string, minTLSFound bool, cipherSuites []string, ciphersFound bool) error {
	servingInfo, err := rnode.Pipe(yaml.LookupCreate(yaml.MappingNode, "servingInfo"))
	if err != nil {
		return err
	}

	if ciphersFound && len(cipherSuites) > 0 {
		currentCiphers, err := getSortedCipherSuites(servingInfo)
		if err != nil || !slices.Equal(currentCiphers, cipherSuites) {
			// Create a sequence node with the cipher suites
			seqNode := yaml.NewListRNode(cipherSuites...)
			if err := servingInfo.PipeE(yaml.SetField("cipherSuites", seqNode)); err != nil {
				return err
			}
		}
	}

	// Update minTLSVersion if found
	if minTLSFound && minTLSVersion != "" {
		if err := servingInfo.PipeE(yaml.SetField("minTLSVersion", yaml.NewStringRNode(minTLSVersion))); err != nil {
			return err
		}
	}

	return nil
}

// getSortedCipherSuites extracts and sorts the cipherSuites string slice from a servingInfo RNode
func getSortedCipherSuites(servingInfo *yaml.RNode) ([]string, error) {
	ciphersNode, err := servingInfo.Pipe(yaml.Lookup("cipherSuites"))
	if err != nil || ciphersNode == nil {
		return nil, err
	}

	elements, err := ciphersNode.Elements()
	if err != nil {
		return nil, err
	}

	var ciphers []string
	for _, elem := range elements {
		// For scalar nodes, access the value directly without YAML serialization
		// This avoids the trailing newline that String() (which uses yaml.Encode) adds
		if elem.YNode().Kind == yaml.ScalarNode {
			value := elem.YNode().Value
			// Skip empty values
			if value == "" {
				continue
			}
			ciphers = append(ciphers, value)
		}
	}

	// Sort cipher suites for consistent comparison
	sort.Strings(ciphers)

	return ciphers, nil
}

// apiServerListerAdapter adapts a client interface to the lister interface
type apiServerListerAdapter struct {
	client configclientv1.APIServerInterface
	ctx    context.Context
}

func (a *apiServerListerAdapter) List(selector labels.Selector) ([]*configv1.APIServer, error) {
	// Not implemented - ObserveTLSSecurityProfile only uses Get()
	return nil, nil
}

func (a *apiServerListerAdapter) Get(name string) (*configv1.APIServer, error) {
	return a.client.Get(a.ctx, name, metav1.GetOptions{})
}

// configObserverListers implements the configobserver.Listers interface
type configObserverListers struct {
	apiServerLister configlistersv1.APIServerLister
}

func (l *configObserverListers) APIServerLister() configlistersv1.APIServerLister {
	return l.apiServerLister
}

func (l *configObserverListers) ResourceSyncer() resourcesynccontroller.ResourceSyncer {
	// Not needed for TLS observation
	return nil
}

func (l *configObserverListers) PreRunHasSynced() []cache.InformerSynced {
	// Not needed for TLS observation
	return nil
}

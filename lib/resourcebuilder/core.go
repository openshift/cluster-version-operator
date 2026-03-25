package resourcebuilder

import (
	"context"
	"errors"
	"fmt"
	"sort"

	"sigs.k8s.io/kustomize/kyaml/yaml"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/client-go/tools/cache"
	"k8s.io/klog/v2"
	"k8s.io/utils/clock"

	configv1 "github.com/openshift/api/config/v1"
	operatorv1alpha1 "github.com/openshift/api/operator/v1alpha1"
	configlistersv1 "github.com/openshift/client-go/config/listers/config/v1"
	"github.com/openshift/library-go/pkg/operator/configobserver/apiserver"
	"github.com/openshift/library-go/pkg/operator/events"
	"github.com/openshift/library-go/pkg/operator/resourcesynccontroller"
)

const (
	// ConfigMapInjectTLSAnnotation is the annotation key that triggers TLS injection into ConfigMaps
	ConfigMapInjectTLSAnnotation = "config.openshift.io/inject-tls"
)

type optional[T any] struct {
	value T
	found bool
}

type tlsConfig struct {
	minTLSVersion optional[string]
	cipherSuites  optional[[]string]
}

func (b *builder) modifyConfigMap(ctx context.Context, cm *corev1.ConfigMap) error {
	// Check for TLS injection annotation
	if value, ok := cm.Annotations[ConfigMapInjectTLSAnnotation]; !ok || value != "true" {
		return nil
	}

	klog.V(2).Infof("ConfigMap %s/%s has %s annotation set to true", cm.Namespace, cm.Name, ConfigMapInjectTLSAnnotation)

	// Empty data, nothing to inject into
	if cm.Data == nil {
		klog.V(2).Infof("ConfigMap %s/%s has empty data, skipping TLS profile injection", cm.Namespace, cm.Name)
		return nil
	}

	// Observe TLS configuration from APIServer
	tlsConf, err := b.observeTLSConfiguration(ctx, cm)
	if err != nil {
		return fmt.Errorf("unable to observe TLS configuration: %v", err)
	}

	minTLSLog := "<not found>"
	if tlsConf.minTLSVersion.found {
		minTLSLog = tlsConf.minTLSVersion.value
	}
	cipherSuitesLog := "<not found>"
	if tlsConf.cipherSuites.found {
		cipherSuitesLog = fmt.Sprintf("%v", tlsConf.cipherSuites.value)
	}
	klog.V(4).Infof("ConfigMap %s/%s: observed minTLSVersion=%v, cipherSuites=%v",
		cm.Namespace, cm.Name, minTLSLog, cipherSuitesLog)

	// Process each data entry that contains GenericOperatorConfig
	for key, value := range cm.Data {
		klog.V(4).Infof("Processing %q key", key)
		// Parse YAML into RNode to preserve formatting and field order
		rnode, err := yaml.Parse(value)
		if err != nil {
			klog.V(4).Infof("ConfigMap's %q entry parsing failed: %v", key, err)
			// Not valid YAML, skip this entry
			continue
		}

		// Check if this is a supported config kind
		switch {
		case rnode.GetKind() == "GenericOperatorConfig" && rnode.GetApiVersion() == operatorv1alpha1.GroupVersion.String():
		case rnode.GetKind() == "GenericControllerConfig" && rnode.GetApiVersion() == configv1.GroupVersion.String():
		default:
			klog.V(4).Infof("ConfigMap's %q entry is not a supported config type. Only GenericOperatorConfig (%v) and GenericControllerConfig (%v) are. Skipping this entry", key, operatorv1alpha1.GroupVersion.String(), configv1.GroupVersion.String())
			continue
		}

		klog.V(2).Infof("ConfigMap %s/%s processing GenericOperatorConfig in key %s", cm.Namespace, cm.Name, key)

		// Inject TLS settings into the GenericOperatorConfig while preserving structure
		if err := updateRNodeWithTLSSettings(rnode, tlsConf); err != nil {
			return fmt.Errorf("failed to inject the TLS configuration: %v", err)
		}

		// Marshal the modified RNode back to YAML
		modifiedYAML, err := rnode.String()
		if err != nil {
			return fmt.Errorf("failed to marshall the modified ConfigMap back to YAML: %v", err)
		}

		// Update the ConfigMap data entry with the modified YAML
		cm.Data[key] = modifiedYAML
		klog.V(2).Infof("ConfigMap %s/%s updated GenericOperatorConfig with TLS profile in key %s", cm.Namespace, cm.Name, key)
	}
	return nil
}

// observeTLSConfiguration retrieves TLS configuration from the APIServer cluster CR
// using ObserveTLSSecurityProfile and extracts minTLSVersion and cipherSuites.
func (b *builder) observeTLSConfiguration(ctx context.Context, cm *corev1.ConfigMap) (*tlsConfig, error) {
	apiServer, err := b.configClientv1.APIServers().Get(ctx, "cluster", metav1.GetOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to retrieve APIServer CR for ConfigMap %s/%s: %w", cm.Namespace, cm.Name, err)
	}

	listers := &configObserverListers{
		apiServerLister: &apiServerListerAdapter{
			apiServer: apiServer,
		},
	}

	// Create an in-memory event recorder that doesn't send events to the API server
	recorder := events.NewInMemoryRecorder("configmap-tls-injection", clock.RealClock{})

	// Call ObserveTLSSecurityProfile to get TLS configuration
	observedConfig, errs := apiserver.ObserveTLSSecurityProfile(listers, recorder, map[string]any{})
	if len(errs) > 0 {
		return nil, fmt.Errorf("error observing TLS profile for ConfigMap %s/%s: %w", cm.Namespace, cm.Name, errors.Join(errs...))
	}

	config := &tlsConfig{}

	// Extract minTLSVersion from the observed config
	if minTLSVersion, minTLSFound, err := unstructured.NestedString(observedConfig, "servingInfo", "minTLSVersion"); err != nil {
		return nil, err
	} else if minTLSFound {
		config.minTLSVersion = optional[string]{value: minTLSVersion, found: true}
	}

	// Extract cipherSuites from the observed config
	if cipherSuites, ciphersFound, err := unstructured.NestedStringSlice(observedConfig, "servingInfo", "cipherSuites"); err != nil {
		return nil, err
	} else if ciphersFound {
		// Sort cipher suites for consistent ordering
		sort.Strings(cipherSuites)
		config.cipherSuites = optional[[]string]{value: cipherSuites, found: true}
	}

	return config, nil
}

// updateRNodeWithTLSSettings injects TLS settings into a GenericOperatorConfig RNode while preserving structure.
// If a field in tlsConf is not found, the corresponding field will be deleted from the RNode.
func updateRNodeWithTLSSettings(rnode *yaml.RNode, tlsConf *tlsConfig) error {
	servingInfo, err := rnode.Pipe(yaml.LookupCreate(yaml.MappingNode, "servingInfo"))
	if err != nil {
		return err
	}

	// Handle cipherSuites field
	if tlsConf.cipherSuites.found {
		seqNode := yaml.NewListRNode(tlsConf.cipherSuites.value...)
		if err := servingInfo.PipeE(yaml.SetField("cipherSuites", seqNode)); err != nil {
			return err
		}
	} else {
		if err := servingInfo.PipeE(yaml.Clear("cipherSuites")); err != nil {
			return err
		}
	}

	// Handle minTLSVersion field
	if tlsConf.minTLSVersion.found {
		if err := servingInfo.PipeE(yaml.SetField("minTLSVersion", yaml.NewStringRNode(tlsConf.minTLSVersion.value))); err != nil {
			return err
		}
	} else {
		if err := servingInfo.PipeE(yaml.Clear("minTLSVersion")); err != nil {
			return err
		}
	}

	return nil
}

// apiServerListerAdapter adapts an injected APIServer to the lister interface
type apiServerListerAdapter struct {
	apiServer *configv1.APIServer
}

func (a *apiServerListerAdapter) List(selector labels.Selector) ([]*configv1.APIServer, error) {
	// Not implemented - ObserveTLSSecurityProfile only uses Get()
	return nil, nil
}

func (a *apiServerListerAdapter) Get(name string) (*configv1.APIServer, error) {
	if name != a.apiServer.Name {
		return nil, fmt.Errorf("APIServer %q not found", name)
	}
	return a.apiServer, nil
}

// configObserverListers implements the configobserver.Listers interface.
// It's expected to be used solely for apiserver.ObserveTLSSecurityProfile.
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

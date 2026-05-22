package tls

import (
	"crypto/tls"
	"fmt"
	"sync"

	"k8s.io/client-go/tools/cache"
	cliflag "k8s.io/component-base/cli/flag"
	"k8s.io/klog/v2"

	configv1 "github.com/openshift/api/config/v1"
	configinformersv1 "github.com/openshift/client-go/config/informers/externalversions/config/v1"
	tlsprofile "github.com/openshift/controller-runtime-common/pkg/tls"
	"github.com/openshift/library-go/pkg/crypto"
)

// ProfileManager manages the central TLS profile configuration with event-driven updates.
type ProfileManager struct {
	// mu protects applyProfile from concurrent access during TLS handshakes
	// and APIServer event handler updates
	mu           sync.RWMutex
	applyProfile func(*tls.Config) // nil if no central profile configured
	overrides    *Settings         // nil if no overrides configured
}

type Settings struct {
	MinVersion   uint16
	CipherSuites []uint16
}

// NewProfileManager creates a new TLS profile manager and performs initial resolution.
// Falls back to safe defaults on any error to prioritize availability.
func NewProfileManager(apiServerInformer configinformersv1.APIServerInformer, overrides *Settings) (*ProfileManager, error) {
	mgr := &ProfileManager{
		overrides: overrides,
	}

	apiServer, err := apiServerInformer.Lister().Get(tlsprofile.APIServerName)
	if err != nil {
		klog.Warningf("APIServer resource not available at startup: %v, using fallback defaults", err)
		apiServer = nil
	}

	if err := mgr.updateSettings(apiServer); err != nil {
		klog.Warningf("Failed to initialize TLS profile: %v, using safe defaults", err)
	}

	if _, err := apiServerInformer.Informer().AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: func(obj interface{}) {
			if apiServer, ok := obj.(*configv1.APIServer); ok {
				if err := mgr.updateSettings(apiServer); err != nil {
					klog.Errorf("Failed to apply TLS settings on APIServer add: %v", err)
				}
			}
		},
		UpdateFunc: func(oldObj, newObj interface{}) {
			if apiServer, ok := newObj.(*configv1.APIServer); ok {
				if err := mgr.updateSettings(apiServer); err != nil {
					klog.Errorf("Failed to apply TLS settings on APIServer update: %v", err)
				}
			}
		},
		DeleteFunc: func(obj interface{}) {
			if err := mgr.updateSettings(nil); err != nil {
				klog.Errorf("Failed to apply fallback TLS settings on APIServer delete: %v", err)
			}
		},
	}); err != nil {
		return nil, fmt.Errorf("failed to register APIServer event handlers: %w", err)
	}

	return mgr, nil
}

// updateSettings resolves and caches the TLS profile apply function.
func (m *ProfileManager) updateSettings(apiServer *configv1.APIServer) error {
	applyFunc, err := resolveTLSProfile(apiServer)
	if err != nil {
		klog.Warningf("Failed to update TLS profile, keeping previous profile: %v", err)
		return err
	}

	// By storing the apply function rather than extracted values, we automatically pick up any
	// new fields that the tls profile package might set in future versions.
	m.mu.Lock()
	m.applyProfile = applyFunc
	m.mu.Unlock()

	klog.V(2).Info("Updated cached TLS profile")
	return nil
}

// resolveTLSProfile resolves the TLS profile apply function from the APIServer resource.
func resolveTLSProfile(apiServer *configv1.APIServer) (func(*tls.Config), error) {
	// No APIServer or TLS adherence disabled
	if apiServer == nil || !crypto.ShouldHonorClusterTLSProfile(apiServer.Spec.TLSAdherence) {
		return nil, nil
	}

	// Get the TLS profile spec
	profile, err := tlsprofile.GetTLSProfileSpec(apiServer.Spec.TLSSecurityProfile)
	if err != nil {
		return nil, fmt.Errorf("failed to get TLS profile spec: %w", err)
	}

	// Get the apply function from the profile
	applyFunc, unsupportedCiphers := tlsprofile.NewTLSConfigFromProfile(profile)
	if len(unsupportedCiphers) > 0 {
		klog.V(4).Infof("TLS profile contains unsupported ciphers (will be ignored): %v", unsupportedCiphers)
	}

	klog.V(4).Info("Resolved central TLS profile apply function")
	return applyFunc, nil
}

// ApplySettings applies the TLS configuration to the provided config.
// Applies: crypto defaults → central profile → overrides
func (m *ProfileManager) ApplySettings(config *tls.Config) {
	crypto.SecureTLSConfig(config)

	m.mu.RLock()
	defer m.mu.RUnlock()

	// Apply central profile if configured (modifies config in place)
	if m.applyProfile != nil {
		m.applyProfile(config)
	}

	// Apply overrides (these take final precedence)
	if m.overrides != nil {
		if m.overrides.MinVersion != 0 {
			config.MinVersion = m.overrides.MinVersion
		}
		if len(m.overrides.CipherSuites) > 0 {
			config.CipherSuites = m.overrides.CipherSuites
		}
	}
}

type Options struct {
	// MinVersionOverride is the minimum TLS version supported.
	// When set, it takes precedence over the central TLS profile.
	MinVersionOverride string

	// CipherSuitesOverride is the list of allowed cipher suites for the server.
	// When set, it takes precedence over the central TLS profile.
	CipherSuitesOverride []string

	settings *Settings
}

// GetOverrides returns TLS overrides.
// It should be called after CreateOverrides.
func (o *Options) GetOverrides() *Settings {
	return o.settings
}

// CreateOverrides creates the TLS override options and returns parsed values.
// Returns nil if no overrides are configured.
func (o *Options) CreateOverrides() error {
	// If no overrides, return nil (central profile or defaults will be used)
	if o.MinVersionOverride == "" && len(o.CipherSuitesOverride) == 0 {
		return nil
	}

	validated := &Settings{}

	if o.MinVersionOverride != "" {
		minVersion, err := cliflag.TLSVersion(o.MinVersionOverride)
		if err != nil {
			return fmt.Errorf("invalid --tls-min-version %q: %w (valid values: %v)", o.MinVersionOverride, err, cliflag.TLSPossibleVersions())
		}
		validated.MinVersion = minVersion
	}

	if len(o.CipherSuitesOverride) > 0 {
		cipherSuites, err := cliflag.TLSCipherSuites(o.CipherSuitesOverride)
		if err != nil {
			return fmt.Errorf("invalid --tls-cipher-suites: %w", err)
		}
		validated.CipherSuites = cipherSuites
	}

	o.settings = validated
	return nil
}

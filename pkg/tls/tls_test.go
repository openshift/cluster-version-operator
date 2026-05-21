package tls

import (
	"context"
	"crypto/tls"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/cache"

	configv1 "github.com/openshift/api/config/v1"
	configfake "github.com/openshift/client-go/config/clientset/versioned/fake"
	configinformers "github.com/openshift/client-go/config/informers/externalversions"
)

// Test_applyTLSProfile tests the central TLS profile application from APIServer resource
func Test_applyTLSProfile(t *testing.T) {
	tests := []struct {
		name               string
		apiServer          *configv1.APIServer
		expectError        bool
		expectMinVersion   uint16
		expectCipherSuites bool
	}{
		{
			name: "applies intermediate TLS profile",
			apiServer: &configv1.APIServer{
				ObjectMeta: metav1.ObjectMeta{
					Name:       "cluster",
					Generation: 1,
				},
				Spec: configv1.APIServerSpec{
					TLSAdherence: configv1.TLSAdherencePolicyStrictAllComponents,
					TLSSecurityProfile: &configv1.TLSSecurityProfile{
						Type:         configv1.TLSProfileIntermediateType,
						Intermediate: &configv1.IntermediateTLSProfile{},
					},
				},
			},
			expectError:        false,
			expectMinVersion:   tls.VersionTLS12,
			expectCipherSuites: true,
		},
		{
			name: "applies modern TLS profile",
			apiServer: &configv1.APIServer{
				ObjectMeta: metav1.ObjectMeta{
					Name:       "cluster",
					Generation: 2,
				},
				Spec: configv1.APIServerSpec{
					TLSAdherence: configv1.TLSAdherencePolicyStrictAllComponents,
					TLSSecurityProfile: &configv1.TLSSecurityProfile{
						Type:   configv1.TLSProfileModernType,
						Modern: &configv1.ModernTLSProfile{},
					},
				},
			},
			expectError:        false,
			expectMinVersion:   tls.VersionTLS13,
			expectCipherSuites: true,
		},
		{
			name: "applies old TLS profile",
			apiServer: &configv1.APIServer{
				ObjectMeta: metav1.ObjectMeta{
					Name:       "cluster",
					Generation: 3,
				},
				Spec: configv1.APIServerSpec{
					TLSAdherence: configv1.TLSAdherencePolicyStrictAllComponents,
					TLSSecurityProfile: &configv1.TLSSecurityProfile{
						Type: configv1.TLSProfileOldType,
						Old:  &configv1.OldTLSProfile{},
					},
				},
			},
			expectError:        false,
			expectMinVersion:   tls.VersionTLS10,
			expectCipherSuites: true,
		},
		{
			name: "applies custom TLS profile with TLS 1.2",
			apiServer: &configv1.APIServer{
				ObjectMeta: metav1.ObjectMeta{
					Name:       "cluster",
					Generation: 4,
				},
				Spec: configv1.APIServerSpec{
					TLSAdherence: configv1.TLSAdherencePolicyStrictAllComponents,
					TLSSecurityProfile: &configv1.TLSSecurityProfile{
						Type: configv1.TLSProfileCustomType,
						Custom: &configv1.CustomTLSProfile{
							TLSProfileSpec: configv1.TLSProfileSpec{
								Ciphers:       []string{"TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256"},
								MinTLSVersion: configv1.VersionTLS12,
							},
						},
					},
				},
			},
			expectError:        false,
			expectMinVersion:   tls.VersionTLS12,
			expectCipherSuites: true,
		},
		{
			name:               "fallback to TLS 1.2 when APIServer not found",
			apiServer:          nil,
			expectError:        false,
			expectMinVersion:   tls.VersionTLS12,
			expectCipherSuites: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var objects []runtime.Object
			if tt.apiServer != nil {
				objects = append(objects, tt.apiServer)
			}
			fakeClient := configfake.NewClientset(objects...)
			informerFactory := configinformers.NewSharedInformerFactory(fakeClient, 0)
			apiServerInformer := informerFactory.Config().V1().APIServers()
			apiServerInformer.Lister() // we have to call this before Start

			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()

			informerFactory.Start(ctx.Done())
			informerFactory.WaitForCacheSync(ctx.Done())

			mgr, err := NewProfileManager(apiServerInformer, nil) // No overrides

			if tt.expectError {
				if err == nil {
					t.Fatal("expected error but got none")
				}
				return
			}

			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if mgr == nil {
				t.Fatal("expected non-nil manager")
			}

			// Verify applySettings produces correct config
			config := &tls.Config{}
			mgr.ApplySettings(config)

			if config.MinVersion != tt.expectMinVersion {
				t.Errorf("expected MinVersion=%d, got %d", tt.expectMinVersion, config.MinVersion)
			}

			if tt.expectCipherSuites && len(config.CipherSuites) == 0 {
				t.Error("expected cipher suites to be set but got empty")
			}
		})
	}
}

// Test_applyTLSOptions tests the override flag behavior
func Test_applyTLSOptions(t *testing.T) {
	tests := []struct {
		name               string
		options            Options
		expectValidateErr  bool
		expectMinVersion   uint16
		expectCipherSuites int
	}{
		{
			name: "no overrides - fallback to TLS 1.2",
			options: Options{
				MinVersionOverride:   "",
				CipherSuitesOverride: nil,
			},
			expectValidateErr:  false,
			expectMinVersion:   tls.VersionTLS12, // fallback
			expectCipherSuites: 0,
		},
		{
			name: "min version override only",
			options: Options{
				MinVersionOverride:   "VersionTLS12",
				CipherSuitesOverride: nil,
			},
			expectValidateErr:  false,
			expectMinVersion:   tls.VersionTLS12,
			expectCipherSuites: 0,
		},
		{
			name: "cipher suites override only",
			options: Options{
				MinVersionOverride:   "",
				CipherSuitesOverride: []string{"TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256"},
			},
			expectValidateErr:  false,
			expectMinVersion:   tls.VersionTLS12, // fallback
			expectCipherSuites: 1,
		},
		{
			name: "both overrides",
			options: Options{
				MinVersionOverride:   "VersionTLS13",
				CipherSuitesOverride: []string{"TLS_AES_128_GCM_SHA256", "TLS_AES_256_GCM_SHA384"},
			},
			expectValidateErr:  false,
			expectMinVersion:   tls.VersionTLS13,
			expectCipherSuites: 2,
		},
		{
			name: "invalid min version",
			options: Options{
				MinVersionOverride:   "InvalidVersion",
				CipherSuitesOverride: nil,
			},
			expectValidateErr: true,
		},
		{
			name: "invalid cipher suite",
			options: Options{
				MinVersionOverride:   "",
				CipherSuitesOverride: []string{"INVALID_CIPHER"},
			},
			expectValidateErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.options.CreateOverrides()

			if tt.expectValidateErr {
				if err == nil {
					t.Fatal("expected validation error but got none")
				}
				return
			}

			if err != nil {
				t.Fatalf("unexpected validation error: %v", err)
			}

			// Test override behavior through manager (no APIServer)
			fakeClient := configfake.NewClientset()
			informerFactory := configinformers.NewSharedInformerFactory(fakeClient, 0)
			apiServerInformer := informerFactory.Config().V1().APIServers()

			mgr, err := NewProfileManager(apiServerInformer, tt.options.GetOverrides())
			if err != nil {
				t.Fatalf("unexpected manager creation error: %v", err)
			}

			// Verify behavior through applySettings
			config := &tls.Config{}
			mgr.ApplySettings(config)

			if tt.expectMinVersion > 0 && config.MinVersion != tt.expectMinVersion {
				t.Errorf("expected MinVersion %d, got %d", tt.expectMinVersion, config.MinVersion)
			}

			if tt.expectCipherSuites > 0 && len(config.CipherSuites) != tt.expectCipherSuites {
				t.Errorf("expected %d cipher suites, got %d", tt.expectCipherSuites, len(config.CipherSuites))
			}
		})
	}
}

// Test_tlsProfileOverridePrecedence tests the interaction between central profile and overrides
func Test_tlsProfileOverridePrecedence(t *testing.T) {
	// Setup APIServer with Modern profile (TLS 1.3)
	apiServer := &configv1.APIServer{
		ObjectMeta: metav1.ObjectMeta{
			Name:       "cluster",
			Generation: 1,
		},
		Spec: configv1.APIServerSpec{
			TLSAdherence: configv1.TLSAdherencePolicyStrictAllComponents,
			TLSSecurityProfile: &configv1.TLSSecurityProfile{
				Type:   configv1.TLSProfileModernType,
				Modern: &configv1.ModernTLSProfile{},
			},
		},
	}

	fakeClient := configfake.NewClientset(apiServer)
	informerFactory := configinformers.NewSharedInformerFactory(fakeClient, 0)
	apiServerInformer := informerFactory.Config().V1().APIServers()
	apiServerInformer.Lister() // we have to call this before Start

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	informerFactory.Start(ctx.Done())
	informerFactory.WaitForCacheSync(ctx.Done())

	// Test 1: Central profile only (no overrides)
	mgr1, err := NewProfileManager(apiServerInformer, nil)
	if err != nil {
		t.Fatalf("failed to create TLS profile manager: %v", err)
	}

	config1 := &tls.Config{}
	mgr1.ApplySettings(config1)
	if config1.MinVersion != tls.VersionTLS13 {
		t.Errorf("expected central profile to set TLS 1.3, got %d", config1.MinVersion)
	}

	// Test 2: Override weakens to TLS 1.2 (takes precedence)
	options := &Options{
		MinVersionOverride: "VersionTLS12",
	}
	err = options.CreateOverrides()
	if err != nil {
		t.Fatalf("validation failed: %v", err)
	}

	mgr2, err := NewProfileManager(apiServerInformer, options.GetOverrides())
	if err != nil {
		t.Fatalf("failed to create TLS profile manager: %v", err)
	}

	config2 := &tls.Config{}
	mgr2.ApplySettings(config2)
	if config2.MinVersion != tls.VersionTLS12 {
		t.Errorf("expected override to set TLS 1.2, got %d", config2.MinVersion)
	}

	// Test 3: Override strengthens to TLS 1.3 (when central is lower)
	apiServerOld := &configv1.APIServer{
		ObjectMeta: metav1.ObjectMeta{
			Name:       "cluster",
			Generation: 2,
		},
		Spec: configv1.APIServerSpec{
			TLSAdherence: configv1.TLSAdherencePolicyStrictAllComponents,
			TLSSecurityProfile: &configv1.TLSSecurityProfile{
				Type: configv1.TLSProfileOldType,
				Old:  &configv1.OldTLSProfile{},
			},
		},
	}

	fakeClientOld := configfake.NewClientset(apiServerOld)
	informerFactoryOld := configinformers.NewSharedInformerFactory(fakeClientOld, 0)
	apiServerInformerOld := informerFactoryOld.Config().V1().APIServers()
	apiServerInformerOld.Lister() // we have to call this before Start

	ctxOld, cancelOld := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancelOld()

	informerFactoryOld.Start(ctxOld.Done())
	informerFactoryOld.WaitForCacheSync(ctxOld.Done())

	options2 := Options{
		MinVersionOverride: "VersionTLS13",
	}
	err = options2.CreateOverrides()
	if err != nil {
		t.Fatalf("validation failed: %v", err)
	}

	mgr3, err := NewProfileManager(apiServerInformerOld, options2.GetOverrides())
	if err != nil {
		t.Fatalf("failed to create TLS profile manager: %v", err)
	}

	config3 := &tls.Config{}
	mgr3.ApplySettings(config3)
	if config3.MinVersion != tls.VersionTLS13 {
		t.Errorf("expected override to set TLS 1.3, got %d", config3.MinVersion)
	}
}

// Test_validateTLSOptionsOnlyOnce verifies that validation happens once and parsed values are reused
func Test_validateTLSOptionsOnlyOnce(t *testing.T) {
	options := Options{
		MinVersionOverride:   "VersionTLS12",
		CipherSuitesOverride: []string{"TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256"},
	}

	// Validate once - returns immutable struct
	err := options.CreateOverrides()
	if err != nil {
		t.Fatalf("validation failed: %v", err)
	}

	// Verify parsed values are set
	validated := options.GetOverrides()
	if validated.MinVersion == 0 {
		t.Error("expected minVersion to be set")
	}
	if len(validated.CipherSuites) == 0 {
		t.Error("expected cipherSuites to be set")
	}

	fakeClient := configfake.NewClientset()
	informerFactory := configinformers.NewSharedInformerFactory(fakeClient, 0)
	apiServerInformer := informerFactory.Config().V1().APIServers()

	mgr, err := NewProfileManager(apiServerInformer, validated)
	if err != nil {
		t.Fatalf("failed to create manager: %v", err)
	}

	// Apply settings - validated values should be used from cache
	config := &tls.Config{}
	mgr.ApplySettings(config)

	if config.MinVersion != tls.VersionTLS12 {
		t.Errorf("expected TLS 1.2, got %d", config.MinVersion)
	}
	if len(config.CipherSuites) != 1 {
		t.Errorf("expected 1 cipher suite, got %d", len(config.CipherSuites))
	}
}

// Test_tlsProfileManager_EventHandlers tests that the TLS profile manager
// correctly responds to APIServer resource events
func Test_tlsProfileManager_EventHandlers(t *testing.T) {
	// TODO fix the test and remove the skip
	t.Skip("TODO")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Create fake client and informer
	fakeClient := configfake.NewClientset()
	informerFactory := configinformers.NewSharedInformerFactory(fakeClient, 0)
	apiServerInformer := informerFactory.Config().V1().APIServers()

	// Channel to track profile updates
	profileUpdateCh := make(chan string, 10)

	// Create initial manager with no APIServer resource
	mgr, err := NewProfileManager(apiServerInformer, nil)
	if err != nil {
		t.Fatalf("failed to create TLS profile manager: %v", err)
	}

	// Helper to wait for event
	waitForEvent := func(t *testing.T, expectedEvent string) {
		t.Helper()
		select {
		case event := <-profileUpdateCh:
			if event != expectedEvent {
				t.Errorf("expected %q event, got %q", expectedEvent, event)
			}
		case <-time.After(5 * time.Second):
			t.Fatalf("timeout waiting for %q event", expectedEvent)
		}
	}

	// Helper to verify MinVersion
	verifyMinVersion := func(t *testing.T, expected uint16) {
		t.Helper()
		config := &tls.Config{}
		mgr.ApplySettings(config)
		if config.MinVersion != expected {
			t.Errorf("expected MinVersion %d, got %d", expected, config.MinVersion)
		}
	}

	// Add event handlers that mirror the RunMetrics implementation
	if _, err := apiServerInformer.Informer().AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: func(obj interface{}) {
			if apiServer, ok := obj.(*configv1.APIServer); ok {
				if err := mgr.updateSettings(apiServer); err != nil {
					t.Errorf("Failed to apply TLS settings on APIServer add: %v", err)
				}
				profileUpdateCh <- "add"
			}
		},
		UpdateFunc: func(oldObj, newObj interface{}) {
			if apiServer, ok := newObj.(*configv1.APIServer); ok {
				if err := mgr.updateSettings(apiServer); err != nil {
					t.Errorf("Failed to apply TLS settings on APIServer update: %v", err)
				}
				profileUpdateCh <- "update"
			}
		},
		DeleteFunc: func(obj interface{}) {
			if err := mgr.updateSettings(nil); err != nil {
				t.Errorf("Failed to apply fallback TLS settings on APIServer delete: %v", err)
			}
			profileUpdateCh <- "delete"
		},
	}); err != nil {
		t.Fatalf("failed to add APIServer event handler: %v", err)
	}

	// Start informer
	informerFactory.Start(ctx.Done())
	if !cache.WaitForCacheSync(ctx.Done(), apiServerInformer.Informer().HasSynced) {
		t.Fatal("failed to sync APIServer informer")
	}

	// Test Add event - create APIServer with Intermediate profile
	apiServer := &configv1.APIServer{
		ObjectMeta: metav1.ObjectMeta{Name: "cluster"},
		Spec: configv1.APIServerSpec{
			TLSAdherence: configv1.TLSAdherencePolicyStrictAllComponents,
			TLSSecurityProfile: &configv1.TLSSecurityProfile{
				Type:         configv1.TLSProfileIntermediateType,
				Intermediate: &configv1.IntermediateTLSProfile{},
			},
		},
	}

	if _, err := fakeClient.ConfigV1().APIServers().Create(ctx, apiServer, metav1.CreateOptions{}); err != nil {
		t.Fatalf("failed to create APIServer: %v", err)
	}

	waitForEvent(t, "add")
	verifyMinVersion(t, tls.VersionTLS12)

	// Test Update event - change to Modern profile
	apiServer, err = fakeClient.ConfigV1().APIServers().Get(ctx, "cluster", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("failed to get APIServer: %v", err)
	}

	apiServer.Spec.TLSSecurityProfile = &configv1.TLSSecurityProfile{
		Type:   configv1.TLSProfileModernType,
		Modern: &configv1.ModernTLSProfile{},
	}

	if _, err := fakeClient.ConfigV1().APIServers().Update(ctx, apiServer, metav1.UpdateOptions{}); err != nil {
		t.Fatalf("failed to update APIServer: %v", err)
	}

	waitForEvent(t, "update")
	verifyMinVersion(t, tls.VersionTLS13)

	// Test Delete event - verify fallback to defaults
	if err := fakeClient.ConfigV1().APIServers().Delete(ctx, "cluster", metav1.DeleteOptions{}); err != nil {
		t.Fatalf("failed to delete APIServer: %v", err)
	}

	waitForEvent(t, "delete")
	verifyMinVersion(t, tls.VersionTLS12) // Fallback to defaults
}

// Test_tlsProfileManager_InitializationErrorFallback tests that the manager
// falls back to safe defaults when initialization fails
func Test_tlsProfileManager_InitializationErrorFallback(t *testing.T) {
	// Create an invalid APIServer (Custom type with nil Custom field)
	invalidAPIServer := &configv1.APIServer{
		ObjectMeta: metav1.ObjectMeta{
			Name: "cluster",
		},
		Spec: configv1.APIServerSpec{
			TLSAdherence: configv1.TLSAdherencePolicyStrictAllComponents,
			TLSSecurityProfile: &configv1.TLSSecurityProfile{
				Type:   configv1.TLSProfileCustomType,
				Custom: nil, // Invalid: Custom type with nil Custom field
			},
		},
	}

	// Create fake client and informer
	fakeClient := configfake.NewClientset(invalidAPIServer)
	informerFactory := configinformers.NewSharedInformerFactory(fakeClient, 0)
	apiServerInformer := informerFactory.Config().V1().APIServers()
	apiServerInformer.Lister() // we have to call this before Start

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	informerFactory.Start(ctx.Done())
	informerFactory.WaitForCacheSync(ctx.Done())

	// Manager creation should succeed despite invalid profile (falls back to defaults)
	mgr, err := NewProfileManager(apiServerInformer, nil)
	if err != nil {
		t.Fatalf("expected manager creation to succeed with fallback, got error: %v", err)
	}

	// Verify manager was created
	if mgr == nil {
		t.Fatal("expected non-nil manager")
	}

	// Verify it fell back to safe defaults (applyProfile should be nil)
	mgr.mu.RLock()
	profileApplied := mgr.applyProfile != nil
	mgr.mu.RUnlock()

	if profileApplied {
		t.Error("expected applyProfile to be nil (safe defaults), but it was set")
	}

	// Verify applySettings still works with safe defaults
	config := &tls.Config{}
	mgr.ApplySettings(config)

	// Should have at least TLS 1.2 from crypto.SecureTLSConfig
	if config.MinVersion < tls.VersionTLS12 {
		t.Errorf("expected MinVersion >= TLS 1.2 from safe defaults, got %d", config.MinVersion)
	}
}

// Test_tlsProfileManager_ErrorRecovery tests that the manager retains the old
// profile when updateSettings fails
func Test_tlsProfileManager_ErrorRecovery(t *testing.T) {
	// Create manager with valid Intermediate profile
	intermediateAPIServer := &configv1.APIServer{
		ObjectMeta: metav1.ObjectMeta{
			Name: "cluster",
		},
		Spec: configv1.APIServerSpec{
			TLSAdherence: configv1.TLSAdherencePolicyStrictAllComponents,
			TLSSecurityProfile: &configv1.TLSSecurityProfile{
				Type:         configv1.TLSProfileIntermediateType,
				Intermediate: &configv1.IntermediateTLSProfile{},
			},
		},
	}

	fakeClient := configfake.NewClientset(intermediateAPIServer)
	informerFactory := configinformers.NewSharedInformerFactory(fakeClient, 0)
	apiServerInformer := informerFactory.Config().V1().APIServers()
	apiServerInformer.Lister()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	informerFactory.Start(ctx.Done())
	informerFactory.WaitForCacheSync(ctx.Done())

	mgr, err := NewProfileManager(apiServerInformer, nil)
	if err != nil {
		t.Fatalf("failed to create TLS profile manager: %v", err)
	}

	// Verify initial profile (TLS 1.2 for Intermediate)
	config := &tls.Config{}
	mgr.ApplySettings(config)
	initialMinVersion := config.MinVersion
	if initialMinVersion != tls.VersionTLS12 {
		t.Errorf("expected initial MinVersion TLS 1.2, got %d", initialMinVersion)
	}

	// Attempt to update with invalid profile (Custom type with nil Custom field)
	invalidAPIServer := &configv1.APIServer{
		ObjectMeta: metav1.ObjectMeta{
			Name: "cluster",
		},
		Spec: configv1.APIServerSpec{
			TLSAdherence: configv1.TLSAdherencePolicyStrictAllComponents,
			TLSSecurityProfile: &configv1.TLSSecurityProfile{
				Type:   configv1.TLSProfileCustomType,
				Custom: nil, // Invalid: Custom type with nil Custom field
			},
		},
	}

	// This should return an error
	err = mgr.updateSettings(invalidAPIServer)
	if err == nil {
		t.Error("expected error from updateSettings with Custom type but nil Custom field, got nil")
	}

	// Verify old profile is retained
	config = &tls.Config{}
	mgr.ApplySettings(config)
	if config.MinVersion != initialMinVersion {
		t.Errorf("expected old profile retained (TLS 1.2), got %d", config.MinVersion)
	}

	// Now update with valid Modern profile
	modernAPIServer := &configv1.APIServer{
		ObjectMeta: metav1.ObjectMeta{
			Name: "cluster",
		},
		Spec: configv1.APIServerSpec{
			TLSAdherence: configv1.TLSAdherencePolicyStrictAllComponents,
			TLSSecurityProfile: &configv1.TLSSecurityProfile{
				Type:   configv1.TLSProfileModernType,
				Modern: &configv1.ModernTLSProfile{},
			},
		},
	}

	err = mgr.updateSettings(modernAPIServer)
	if err != nil {
		t.Errorf("unexpected error updating to Modern profile: %v", err)
	}

	// Verify profile updated to TLS 1.3
	config = &tls.Config{}
	mgr.ApplySettings(config)
	if config.MinVersion != tls.VersionTLS13 {
		t.Errorf("expected MinVersion TLS 1.3 after recovery, got %d", config.MinVersion)
	}
}

// Test_tlsProfileManager_TLSAdherenceVariations tests different TLSAdherence
// policy values
func Test_tlsProfileManager_TLSAdherenceVariations(t *testing.T) {
	tests := []struct {
		name          string
		adherence     configv1.TLSAdherencePolicy
		expectApplied bool // Whether central profile should be applied
	}{
		{
			name:          "StrictAllComponents applies profile",
			adherence:     configv1.TLSAdherencePolicyStrictAllComponents,
			expectApplied: true,
		},
		{
			name:          "LegacyAdheringComponentsOnly does not apply profile",
			adherence:     configv1.TLSAdherencePolicyLegacyAdheringComponentsOnly,
			expectApplied: false,
		},
		{
			name:          "NoOpinion does not apply profile",
			adherence:     configv1.TLSAdherencePolicyNoOpinion,
			expectApplied: false,
		},
		{
			name:          "empty adherence does not apply profile",
			adherence:     "",
			expectApplied: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			apiServer := &configv1.APIServer{
				ObjectMeta: metav1.ObjectMeta{
					Name: "cluster",
				},
				Spec: configv1.APIServerSpec{
					TLSAdherence: tt.adherence,
					TLSSecurityProfile: &configv1.TLSSecurityProfile{
						Type:         configv1.TLSProfileIntermediateType,
						Intermediate: &configv1.IntermediateTLSProfile{},
					},
				},
			}

			fakeClient := configfake.NewClientset(apiServer)
			informerFactory := configinformers.NewSharedInformerFactory(fakeClient, 0)
			apiServerInformer := informerFactory.Config().V1().APIServers()
			apiServerInformer.Lister()

			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()

			informerFactory.Start(ctx.Done())
			informerFactory.WaitForCacheSync(ctx.Done())

			mgr, err := NewProfileManager(apiServerInformer, nil)
			if err != nil {
				t.Fatalf("failed to create TLS profile manager: %v", err)
			}

			// Check if profile is applied
			mgr.mu.RLock()
			profileApplied := mgr.applyProfile != nil
			mgr.mu.RUnlock()

			if profileApplied != tt.expectApplied {
				t.Errorf("expected profile applied=%v, got %v", tt.expectApplied, profileApplied)
			}

			// Verify behavior through applySettings
			config := &tls.Config{}
			mgr.ApplySettings(config)

			// All should have at least TLS 1.2 from crypto.SecureTLSConfig
			if config.MinVersion < tls.VersionTLS12 {
				t.Errorf("expected MinVersion >= TLS 1.2, got %d", config.MinVersion)
			}
		})
	}
}

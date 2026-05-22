package tls

import (
	"context"
	"crypto/tls"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"

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
	// Create an initial APIServer with Intermediate profile (TLS 1.2)
	intermediateAPIServer := &configv1.APIServer{
		ObjectMeta: metav1.ObjectMeta{
			Name:            "cluster",
			ResourceVersion: "1",
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
	apiServerInformer.Lister() // we have to call this before Start

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	informerFactory.Start(ctx.Done())
	informerFactory.WaitForCacheSync(ctx.Done())

	// Create the manager - this should pick up the initial Intermediate profile
	mgr, err := NewProfileManager(apiServerInformer, nil)
	if err != nil {
		t.Fatalf("failed to create TLS profile manager: %v", err)
	}

	// Test 1: Verify initial profile from Add event (Intermediate = TLS 1.2)
	t.Run("initial add event", func(t *testing.T) {
		config := &tls.Config{}
		mgr.ApplySettings(config)
		if config.MinVersion != tls.VersionTLS12 {
			t.Errorf("expected initial MinVersion TLS 1.2 from Intermediate profile, got %d", config.MinVersion)
		}
	})

	// Test 2: Update event - change to Modern profile (TLS 1.3)
	t.Run("update event changes profile", func(t *testing.T) {
		modernAPIServer := intermediateAPIServer.DeepCopy()
		modernAPIServer.ResourceVersion = "2"
		modernAPIServer.Spec.TLSSecurityProfile = &configv1.TLSSecurityProfile{
			Type:   configv1.TLSProfileModernType,
			Modern: &configv1.ModernTLSProfile{},
		}

		_, err := fakeClient.ConfigV1().APIServers().Update(ctx, modernAPIServer, metav1.UpdateOptions{})
		if err != nil {
			t.Fatalf("failed to update APIServer: %v", err)
		}

		// Give the informer a moment to process the update
		time.Sleep(100 * time.Millisecond)

		// Verify profile updated to Modern (TLS 1.3)
		config := &tls.Config{}
		mgr.ApplySettings(config)
		if config.MinVersion != tls.VersionTLS13 {
			t.Errorf("expected MinVersion TLS 1.3 after update to Modern profile, got %d", config.MinVersion)
		}
	})

	// Test 3: Update event with invalid profile should retain old profile
	t.Run("update event with invalid profile retains old", func(t *testing.T) {
		// First, get the current valid profile (should be Modern = TLS 1.3)
		configBefore := &tls.Config{}
		mgr.ApplySettings(configBefore)
		versionBefore := configBefore.MinVersion

		if versionBefore != tls.VersionTLS13 {
			t.Fatalf("expected TLS 1.3 before invalid update, got %d", versionBefore)
		}

		// Create invalid update (Custom type with nil Custom field)
		invalidAPIServer := intermediateAPIServer.DeepCopy()
		invalidAPIServer.ResourceVersion = "3"
		invalidAPIServer.Spec.TLSSecurityProfile = &configv1.TLSSecurityProfile{
			Type:   configv1.TLSProfileCustomType,
			Custom: nil, // Invalid
		}

		_, err := fakeClient.ConfigV1().APIServers().Update(ctx, invalidAPIServer, metav1.UpdateOptions{})
		if err != nil {
			t.Fatalf("failed to update APIServer: %v", err)
		}

		time.Sleep(100 * time.Millisecond)

		// Verify old profile is retained (still TLS 1.3)
		configAfter := &tls.Config{}
		mgr.ApplySettings(configAfter)
		if configAfter.MinVersion != versionBefore {
			t.Errorf("expected old profile retained (TLS 1.3), got %d", configAfter.MinVersion)
		}
	})

	// Test 4: Update event changing TLSAdherence to disable profile
	t.Run("update event disables profile with TLSAdherence", func(t *testing.T) {
		noOpinionAPIServer := intermediateAPIServer.DeepCopy()
		noOpinionAPIServer.ResourceVersion = "4"
		noOpinionAPIServer.Spec.TLSAdherence = configv1.TLSAdherencePolicyNoOpinion
		noOpinionAPIServer.Spec.TLSSecurityProfile = &configv1.TLSSecurityProfile{
			Type:         configv1.TLSProfileIntermediateType,
			Intermediate: &configv1.IntermediateTLSProfile{},
		}

		_, err := fakeClient.ConfigV1().APIServers().Update(ctx, noOpinionAPIServer, metav1.UpdateOptions{})
		if err != nil {
			t.Fatalf("failed to update APIServer: %v", err)
		}

		time.Sleep(100 * time.Millisecond)

		// Should still have safe defaults from crypto.SecureTLSConfig
		config := &tls.Config{}
		mgr.ApplySettings(config)
		if config.MinVersion < tls.VersionTLS12 {
			t.Errorf("expected MinVersion >= TLS 1.2 from safe defaults, got %d", config.MinVersion)
		}
	})

	// Test 5: Delete event should fall back to safe defaults
	t.Run("delete event falls back to defaults", func(t *testing.T) {
		err := fakeClient.ConfigV1().APIServers().Delete(ctx, "cluster", metav1.DeleteOptions{})
		if err != nil {
			t.Fatalf("failed to delete APIServer: %v", err)
		}

		time.Sleep(100 * time.Millisecond)

		// Should still work with safe defaults
		config := &tls.Config{}
		mgr.ApplySettings(config)
		if config.MinVersion < tls.VersionTLS12 {
			t.Errorf("expected MinVersion >= TLS 1.2 from safe defaults after delete, got %d", config.MinVersion)
		}
	})
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

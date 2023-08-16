package capability

import (
	"reflect"
	"testing"

	configv1 "github.com/openshift/api/config/v1"
)

func TestSetCapabilities(t *testing.T) {
	tests := []struct {
		name            string
		config          *configv1.ClusterVersion
		wantKnownKeys   []string
		wantEnabledKeys []string
	}{
		{name: "capabilities nil",
			config: &configv1.ClusterVersion{},
			// wantKnownKeys and wantEnabledKeys will be set to default set of capabilities by test
		},
		{name: "capabilities set not set",
			config: &configv1.ClusterVersion{
				Spec: configv1.ClusterVersionSpec{
					Capabilities: &configv1.ClusterVersionCapabilitiesSpec{},
				},
			},
			// wantKnownKeys and wantEnabledKeys will be set to default set of capabilities by test
		},
		{name: "set capabilities None",
			config: &configv1.ClusterVersion{
				Spec: configv1.ClusterVersionSpec{
					Capabilities: &configv1.ClusterVersionCapabilitiesSpec{
						BaselineCapabilitySet: configv1.ClusterVersionCapabilitySetNone,
					},
				},
			},
			wantKnownKeys: []string{
				string(configv1.ClusterVersionCapabilityBaremetal),
				string(configv1.ClusterVersionCapabilityMarketplace),
				string(configv1.ClusterVersionCapabilityOpenShiftSamples),
				string(configv1.ClusterVersionCapabilityConsole),
				string(configv1.ClusterVersionCapabilityInsights),
				string(configv1.ClusterVersionCapabilityStorage),
				string(configv1.ClusterVersionCapabilityCSISnapshot),
				string(configv1.ClusterVersionCapabilityNodeTuning),
				string(configv1.ClusterVersionCapabilityMachineAPI),
			},
			wantEnabledKeys: []string{},
		},
		{name: "set capabilities 4_11",
			config: &configv1.ClusterVersion{
				Spec: configv1.ClusterVersionSpec{
					Capabilities: &configv1.ClusterVersionCapabilitiesSpec{
						BaselineCapabilitySet:         configv1.ClusterVersionCapabilitySet4_11,
						AdditionalEnabledCapabilities: []configv1.ClusterVersionCapability{},
					},
				},
			},
			wantKnownKeys: []string{
				string(configv1.ClusterVersionCapabilityBaremetal),
				string(configv1.ClusterVersionCapabilityMarketplace),
				string(configv1.ClusterVersionCapabilityOpenShiftSamples),
				string(configv1.ClusterVersionCapabilityConsole),
				string(configv1.ClusterVersionCapabilityInsights),
				string(configv1.ClusterVersionCapabilityStorage),
				string(configv1.ClusterVersionCapabilityCSISnapshot),
				string(configv1.ClusterVersionCapabilityNodeTuning),
				string(configv1.ClusterVersionCapabilityMachineAPI),
			},
			wantEnabledKeys: []string{
				string(configv1.ClusterVersionCapabilityBaremetal),
				string(configv1.ClusterVersionCapabilityMarketplace),
				string(configv1.ClusterVersionCapabilityOpenShiftSamples),
			},
		},
		{name: "set capabilities vCurrent",
			config: &configv1.ClusterVersion{
				Spec: configv1.ClusterVersionSpec{
					Capabilities: &configv1.ClusterVersionCapabilitiesSpec{
						BaselineCapabilitySet:         configv1.ClusterVersionCapabilitySetCurrent,
						AdditionalEnabledCapabilities: []configv1.ClusterVersionCapability{},
					},
				},
			},
			wantKnownKeys: []string{
				string(configv1.ClusterVersionCapabilityBaremetal),
				string(configv1.ClusterVersionCapabilityMarketplace),
				string(configv1.ClusterVersionCapabilityOpenShiftSamples),
				string(configv1.ClusterVersionCapabilityConsole),
				string(configv1.ClusterVersionCapabilityInsights),
				string(configv1.ClusterVersionCapabilityStorage),
				string(configv1.ClusterVersionCapabilityCSISnapshot),
				string(configv1.ClusterVersionCapabilityNodeTuning),
				string(configv1.ClusterVersionCapabilityMachineAPI),
			},
			wantEnabledKeys: []string{
				string(configv1.ClusterVersionCapabilityBaremetal),
				string(configv1.ClusterVersionCapabilityMarketplace),
				string(configv1.ClusterVersionCapabilityOpenShiftSamples),
				string(configv1.ClusterVersionCapabilityConsole),
				string(configv1.ClusterVersionCapabilityInsights),
				string(configv1.ClusterVersionCapabilityStorage),
				string(configv1.ClusterVersionCapabilityCSISnapshot),
				string(configv1.ClusterVersionCapabilityNodeTuning),
				string(configv1.ClusterVersionCapabilityMachineAPI),
			},
		},
		{name: "set capabilities None with additional",
			config: &configv1.ClusterVersion{
				Spec: configv1.ClusterVersionSpec{
					Capabilities: &configv1.ClusterVersionCapabilitiesSpec{
						BaselineCapabilitySet:         configv1.ClusterVersionCapabilitySetNone,
						AdditionalEnabledCapabilities: []configv1.ClusterVersionCapability{"cap1", "cap2", "cap3"},
					},
				},
			},
			wantKnownKeys: []string{
				string(configv1.ClusterVersionCapabilityBaremetal),
				string(configv1.ClusterVersionCapabilityMarketplace),
				string(configv1.ClusterVersionCapabilityOpenShiftSamples),
				string(configv1.ClusterVersionCapabilityConsole),
				string(configv1.ClusterVersionCapabilityInsights),
				string(configv1.ClusterVersionCapabilityStorage),
				string(configv1.ClusterVersionCapabilityCSISnapshot),
				string(configv1.ClusterVersionCapabilityNodeTuning),
				string(configv1.ClusterVersionCapabilityMachineAPI),
			},
			wantEnabledKeys: []string{"cap1", "cap2", "cap3"},
		},
		{name: "set capabilities 4_11 with additional",
			config: &configv1.ClusterVersion{
				Spec: configv1.ClusterVersionSpec{
					Capabilities: &configv1.ClusterVersionCapabilitiesSpec{
						BaselineCapabilitySet:         configv1.ClusterVersionCapabilitySet4_11,
						AdditionalEnabledCapabilities: []configv1.ClusterVersionCapability{"cap1", "cap2", "cap3"},
					},
				},
			},
			wantKnownKeys: []string{
				string(configv1.ClusterVersionCapabilityBaremetal),
				string(configv1.ClusterVersionCapabilityMarketplace),
				string(configv1.ClusterVersionCapabilityOpenShiftSamples),
				string(configv1.ClusterVersionCapabilityConsole),
				string(configv1.ClusterVersionCapabilityInsights),
				string(configv1.ClusterVersionCapabilityStorage),
				string(configv1.ClusterVersionCapabilityCSISnapshot),
				string(configv1.ClusterVersionCapabilityNodeTuning),
				string(configv1.ClusterVersionCapabilityMachineAPI),
			},
			wantEnabledKeys: []string{
				string(configv1.ClusterVersionCapabilityBaremetal),
				string(configv1.ClusterVersionCapabilityMarketplace),
				string(configv1.ClusterVersionCapabilityOpenShiftSamples),
				"cap1",
				"cap2",
				"cap3",
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			caps := SetCapabilities(test.config, nil)
			if test.config.Spec.Capabilities == nil || (test.config.Spec.Capabilities != nil &&
				len(test.config.Spec.Capabilities.BaselineCapabilitySet) == 0) {

				test.wantKnownKeys = getDefaultCapabilities()
				test.wantEnabledKeys = getDefaultCapabilities()
			}
			if len(caps.KnownCapabilities) != len(test.wantKnownKeys) {
				t.Errorf("Incorrect number of KnownCapabilities keys, wanted: %q. KnownCapabilities returned: %v", test.wantKnownKeys, caps.KnownCapabilities)
			}
			for _, v := range test.wantKnownKeys {
				if _, ok := caps.KnownCapabilities[configv1.ClusterVersionCapability(v)]; !ok {
					t.Errorf("Missing KnownCapabilities key %q. KnownCapabilities returned : %v", v, caps.KnownCapabilities)
				}
			}
			if len(caps.EnabledCapabilities) != len(test.wantEnabledKeys) {
				t.Errorf("Incorrect number of EnabledCapabilities keys, wanted: %q. EnabledCapabilities returned: %v", test.wantEnabledKeys, caps.EnabledCapabilities)
			}
			for _, v := range test.wantEnabledKeys {
				if _, ok := caps.EnabledCapabilities[configv1.ClusterVersionCapability(v)]; !ok {
					t.Errorf("Missing EnabledCapabilities key %q. EnabledCapabilities returned : %v", v, caps.EnabledCapabilities)
				}
			}
		})
	}
}

func TestSetCapabilitiesWithImplicitlyEnabled(t *testing.T) {
	tests := []struct {
		name         string
		config       *configv1.ClusterVersion
		wantImplicit []string
		priorEnabled map[configv1.ClusterVersionCapability]struct{}
	}{
		{name: "set capabilities 4_11 with additional",
			config: &configv1.ClusterVersion{
				Spec: configv1.ClusterVersionSpec{
					Capabilities: &configv1.ClusterVersionCapabilitiesSpec{
						BaselineCapabilitySet: configv1.ClusterVersionCapabilitySet4_11,
					},
				},
			},
			priorEnabled: map[configv1.ClusterVersionCapability]struct{}{"cap1": {}, "cap2": {}, "cap3": {}},
			wantImplicit: []string{"cap1", "cap2", "cap3"},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			caps := SetCapabilities(test.config, test.priorEnabled)
			if len(caps.ImplicitlyEnabledCapabilities) != len(test.wantImplicit) {
				t.Errorf("Incorrect number of implicitly enabled keys, wanted: %q. ImplicitlyEnabledCapabilities returned: %v", test.wantImplicit, caps.ImplicitlyEnabledCapabilities)
			}
			for _, wanted := range test.wantImplicit {
				found := false
				for _, have := range caps.ImplicitlyEnabledCapabilities {
					if wanted == string(have) {
						found = true
						break
					}
				}
				if !found {
					t.Errorf("Missing ImplicitlyEnabledCapabilities key %q. ImplicitlyEnabledCapabilities returned : %v", wanted, caps.ImplicitlyEnabledCapabilities)
				}
			}
		})
	}
}

func TestGetCapabilitiesStatus(t *testing.T) {
	tests := []struct {
		name       string
		caps       ClusterCapabilities
		wantStatus configv1.ClusterVersionCapabilitiesStatus
	}{
		{name: "empty capabilities",
			caps: ClusterCapabilities{
				KnownCapabilities:   map[configv1.ClusterVersionCapability]struct{}{},
				EnabledCapabilities: map[configv1.ClusterVersionCapability]struct{}{},
			},
			wantStatus: configv1.ClusterVersionCapabilitiesStatus{
				EnabledCapabilities: []configv1.ClusterVersionCapability{},
				KnownCapabilities:   []configv1.ClusterVersionCapability{},
			},
		},
		{name: "capabilities",
			caps: ClusterCapabilities{
				KnownCapabilities:   map[configv1.ClusterVersionCapability]struct{}{configv1.ClusterVersionCapabilityOpenShiftSamples: {}},
				EnabledCapabilities: map[configv1.ClusterVersionCapability]struct{}{configv1.ClusterVersionCapabilityOpenShiftSamples: {}},
			},
			wantStatus: configv1.ClusterVersionCapabilitiesStatus{
				EnabledCapabilities: []configv1.ClusterVersionCapability{configv1.ClusterVersionCapabilityOpenShiftSamples},
				KnownCapabilities:   []configv1.ClusterVersionCapability{configv1.ClusterVersionCapabilityOpenShiftSamples},
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			config := GetCapabilitiesStatus(test.caps)
			if len(config.KnownCapabilities) != len(test.wantStatus.KnownCapabilities) {
				t.Errorf("Incorrect number of KnownCapabilities keys, wanted: %q. KnownCapabilities returned: %v",
					test.wantStatus.KnownCapabilities, config.KnownCapabilities)
			}
			for _, v := range test.wantStatus.KnownCapabilities {
				vFound := false
				for _, cv := range config.KnownCapabilities {
					if v == cv {
						vFound = true
						break
					}
					if !vFound {
						t.Errorf("Missing KnownCapabilities key %q. KnownCapabilities returned : %v", v, config.KnownCapabilities)
					}
				}
			}
			if len(config.EnabledCapabilities) != len(test.wantStatus.EnabledCapabilities) {
				t.Errorf("Incorrect number of EnabledCapabilities keys, wanted: %q. EnabledCapabilities returned: %v",
					test.wantStatus.EnabledCapabilities, config.EnabledCapabilities)
			}
			for _, v := range test.wantStatus.EnabledCapabilities {
				vFound := false
				for _, cv := range config.EnabledCapabilities {
					if v == cv {
						vFound = true
						break
					}
					if !vFound {
						t.Errorf("Missing EnabledCapabilities key %q. EnabledCapabilities returned : %v", v, config.EnabledCapabilities)
					}
				}
			}
		})
	}
}

func TestSetFromImplicitlyEnabledCapabilities(t *testing.T) {
	tests := []struct {
		name             string
		implicit         []configv1.ClusterVersionCapability
		capabilities     ClusterCapabilities
		wantCapabilities ClusterCapabilities
	}{
		{name: "implicitly enable capabilities",
			implicit: []configv1.ClusterVersionCapability{
				configv1.ClusterVersionCapability("cap2"),
				configv1.ClusterVersionCapability("cap3"),
				configv1.ClusterVersionCapability("cap4"),
			},
			capabilities: ClusterCapabilities{
				KnownCapabilities:   map[configv1.ClusterVersionCapability]struct{}{"cap1": {}, "cap2": {}, "cap3": {}, "cap4": {}, "cap5": {}},
				EnabledCapabilities: map[configv1.ClusterVersionCapability]struct{}{"cap1": {}},
				ImplicitlyEnabledCapabilities: []configv1.ClusterVersionCapability{
					configv1.ClusterVersionCapability("cap1"),
				},
			},
			wantCapabilities: ClusterCapabilities{
				KnownCapabilities:   map[configv1.ClusterVersionCapability]struct{}{"cap1": {}, "cap2": {}, "cap3": {}, "cap4": {}, "cap5": {}},
				EnabledCapabilities: map[configv1.ClusterVersionCapability]struct{}{"cap1": {}, "cap2": {}, "cap3": {}, "cap4": {}},
				ImplicitlyEnabledCapabilities: []configv1.ClusterVersionCapability{
					configv1.ClusterVersionCapability("cap2"),
					configv1.ClusterVersionCapability("cap3"),
					configv1.ClusterVersionCapability("cap4"),
				},
			},
		},
		{name: "already enabled capability",
			implicit: []configv1.ClusterVersionCapability{
				configv1.ClusterVersionCapability("cap2"),
			},
			capabilities: ClusterCapabilities{
				KnownCapabilities:   map[configv1.ClusterVersionCapability]struct{}{"cap1": {}, "cap2": {}},
				EnabledCapabilities: map[configv1.ClusterVersionCapability]struct{}{"cap1": {}, "cap2": {}},
			},
			wantCapabilities: ClusterCapabilities{
				KnownCapabilities:   map[configv1.ClusterVersionCapability]struct{}{"cap1": {}, "cap2": {}},
				EnabledCapabilities: map[configv1.ClusterVersionCapability]struct{}{"cap1": {}, "cap2": {}},
				ImplicitlyEnabledCapabilities: []configv1.ClusterVersionCapability{
					configv1.ClusterVersionCapability("cap2"),
				},
			},
		},
		{name: "no implicitly enabled capabilities",
			capabilities: ClusterCapabilities{
				KnownCapabilities:   map[configv1.ClusterVersionCapability]struct{}{"cap1": {}, "cap2": {}},
				EnabledCapabilities: map[configv1.ClusterVersionCapability]struct{}{"cap1": {}},
				ImplicitlyEnabledCapabilities: []configv1.ClusterVersionCapability{
					configv1.ClusterVersionCapability("cap2"),
				},
			},
			wantCapabilities: ClusterCapabilities{
				KnownCapabilities:   map[configv1.ClusterVersionCapability]struct{}{"cap1": {}, "cap2": {}},
				EnabledCapabilities: map[configv1.ClusterVersionCapability]struct{}{"cap1": {}},
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			caps := SetFromImplicitlyEnabledCapabilities(test.implicit, test.capabilities)
			if !reflect.DeepEqual(caps, test.wantCapabilities) {
				t.Fatalf("unexpected: %#v", caps)
			}
		})
	}
}

func TestGetImplicitlyEnabledCapabilities(t *testing.T) {
	tests := []struct {
		name           string
		enabledManCaps []configv1.ClusterVersionCapability
		updatedManCaps []configv1.ClusterVersionCapability
		capabilities   ClusterCapabilities
		wantImplicit   []string
	}{
		{name: "implicitly enable capability",
			enabledManCaps: []configv1.ClusterVersionCapability{"cap1", "cap3"},
			updatedManCaps: []configv1.ClusterVersionCapability{"cap2"},
			capabilities: ClusterCapabilities{
				EnabledCapabilities: map[configv1.ClusterVersionCapability]struct{}{"cap1": {}},
			},
			wantImplicit: []string{"cap2"},
		},
		{name: "no prior caps, implicitly enabled capability",
			updatedManCaps: []configv1.ClusterVersionCapability{"cap2"},
			wantImplicit:   []string{"cap2"},
		},
		{name: "multiple implicitly enable capability",
			enabledManCaps: []configv1.ClusterVersionCapability{"cap1", "cap2", "cap3"},
			updatedManCaps: []configv1.ClusterVersionCapability{"cap4", "cap5", "cap6"},
			wantImplicit:   []string{"cap4", "cap5", "cap6"},
		},
		{name: "no implicitly enable capability",
			enabledManCaps: []configv1.ClusterVersionCapability{"cap1", "cap3"},
			updatedManCaps: []configv1.ClusterVersionCapability{"cap1"},
			capabilities: ClusterCapabilities{
				EnabledCapabilities: map[configv1.ClusterVersionCapability]struct{}{"cap1": {}},
			},
		},
		{name: "prior cap, no updated caps, no implicitly enabled capability",
			enabledManCaps: []configv1.ClusterVersionCapability{"cap1"},
		},
		{name: "no implicitly enable capability, already enabled",
			enabledManCaps: []configv1.ClusterVersionCapability{"cap1", "cap2"},
			updatedManCaps: []configv1.ClusterVersionCapability{"cap2"},
			capabilities: ClusterCapabilities{
				EnabledCapabilities: map[configv1.ClusterVersionCapability]struct{}{"cap1": {}, "cap2": {}},
			},
		},
		{name: "no implicitly enable capability, new cap but already enabled",
			enabledManCaps: []configv1.ClusterVersionCapability{"cap1"},
			updatedManCaps: []configv1.ClusterVersionCapability{"cap2"},
			capabilities: ClusterCapabilities{
				EnabledCapabilities: map[configv1.ClusterVersionCapability]struct{}{"cap2": {}},
			},
		},
		{name: "no implicitly enable capability, already implcitly enabled",
			enabledManCaps: []configv1.ClusterVersionCapability{"cap1"},
			updatedManCaps: []configv1.ClusterVersionCapability{"cap2"},
			capabilities: ClusterCapabilities{
				EnabledCapabilities:           map[configv1.ClusterVersionCapability]struct{}{"cap2": {}},
				ImplicitlyEnabledCapabilities: []configv1.ClusterVersionCapability{"cap2"},
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			caps := GetImplicitlyEnabledCapabilities(test.enabledManCaps, test.updatedManCaps, test.capabilities)
			if len(caps) != len(test.wantImplicit) {
				t.Errorf("Incorrect number of implicitly enabled keys, wanted: %d. Implicitly enabled capabilities returned: %v", len(test.wantImplicit), caps)
			}
			for _, wanted := range test.wantImplicit {
				found := false
				for _, have := range caps {
					if wanted == string(have) {
						found = true
						break
					}
				}
				if !found {
					t.Errorf("Missing implicitly enabled capability %q. Implicitly enabled capabilities returned : %v", wanted, caps)
				}
			}
		})
	}
}

func getDefaultCapabilities() []string {
	var caps []string

	for _, v := range configv1.ClusterVersionCapabilitySets[DefaultCapabilitySet] {
		caps = append(caps, string(v))
	}
	return caps
}

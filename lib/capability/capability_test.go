package capability

import (
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
			wantKnownKeys:   []string{string(configv1.ClusterVersionCapabilityOpenShiftSamples)},
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
			wantKnownKeys:   []string{string(configv1.ClusterVersionCapabilityOpenShiftSamples)},
			wantEnabledKeys: []string{string(configv1.ClusterVersionCapabilityOpenShiftSamples)},
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
			wantKnownKeys:   []string{string(configv1.ClusterVersionCapabilityOpenShiftSamples)},
			wantEnabledKeys: []string{string(configv1.ClusterVersionCapabilityOpenShiftSamples)},
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
			wantKnownKeys:   []string{string(configv1.ClusterVersionCapabilityOpenShiftSamples)},
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
			wantKnownKeys:   []string{string(configv1.ClusterVersionCapabilityOpenShiftSamples)},
			wantEnabledKeys: []string{string(configv1.ClusterVersionCapabilityOpenShiftSamples), "cap1", "cap2", "cap3"},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			caps := SetCapabilities(test.config)
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

func TestCheckResourceEnablement(t *testing.T) {
	tests := []struct {
		name        string
		annotations map[string]string
		caps        ClusterCapabilities
		wantError   string
	}{
		{name: "empty annotations"},
		{name: "no capabilitity annotation",
			annotations: map[string]string{"foo": "bar"},
		},
		{name: "known capabilitity annotation",
			annotations: map[string]string{CapabilityAnnotation: string(configv1.ClusterVersionCapabilityOpenShiftSamples), "foo": "bar"},
			caps: ClusterCapabilities{
				KnownCapabilities:   map[configv1.ClusterVersionCapability]struct{}{configv1.ClusterVersionCapabilityOpenShiftSamples: {}},
				EnabledCapabilities: map[configv1.ClusterVersionCapability]struct{}{configv1.ClusterVersionCapabilityOpenShiftSamples: {}},
			},
		},
		{name: "multiple enabled capabilitities annotation",
			annotations: map[string]string{CapabilityAnnotation: "cap1+cap2"},
			caps: ClusterCapabilities{
				KnownCapabilities:   map[configv1.ClusterVersionCapability]struct{}{"cap1": {}, "cap2": {}},
				EnabledCapabilities: map[configv1.ClusterVersionCapability]struct{}{"cap1": {}, "cap2": {}},
			},
		},
		{name: "multiple capabilitities annotation with spaces",
			annotations: map[string]string{CapabilityAnnotation: " + cap1 +cap2+ "},
			caps: ClusterCapabilities{
				KnownCapabilities:   map[configv1.ClusterVersionCapability]struct{}{"cap1": {}, "cap2": {}},
				EnabledCapabilities: map[configv1.ClusterVersionCapability]struct{}{"cap1": {}, "cap2": {}},
			},
			wantError: "unrecognized capability names:  ,  cap1 ,  ",
		},
		{name: "unrecognized capabilitity annotation",
			annotations: map[string]string{CapabilityAnnotation: "cap1"},
			caps: ClusterCapabilities{
				KnownCapabilities:   map[configv1.ClusterVersionCapability]struct{}{configv1.ClusterVersionCapabilityOpenShiftSamples: {}},
				EnabledCapabilities: map[configv1.ClusterVersionCapability]struct{}{configv1.ClusterVersionCapabilityOpenShiftSamples: {}},
			},
			wantError: "unrecognized capability names: cap1",
		},
		{name: "unrecognized capabilitities, spaces",
			annotations: map[string]string{CapabilityAnnotation: "cap1 + cap2"},
			caps: ClusterCapabilities{
				KnownCapabilities:   map[configv1.ClusterVersionCapability]struct{}{"cap1": {}, "cap2": {}},
				EnabledCapabilities: map[configv1.ClusterVersionCapability]struct{}{"cap1": {}, "cap2": {}},
			},
			wantError: "unrecognized capability names: cap1 ,  cap2",
		},
		{name: "invalid capabilitity annotation divider",
			annotations: map[string]string{CapabilityAnnotation: "cap1,cap2,cap3,cap4"},
			caps: ClusterCapabilities{
				KnownCapabilities:   map[configv1.ClusterVersionCapability]struct{}{configv1.ClusterVersionCapabilityOpenShiftSamples: {}},
				EnabledCapabilities: map[configv1.ClusterVersionCapability]struct{}{configv1.ClusterVersionCapabilityOpenShiftSamples: {}},
			},
			wantError: "unrecognized capability names: cap1,cap2,cap3,cap4",
		},
		{name: "multiple unrecognized capabilitities annotation",
			annotations: map[string]string{CapabilityAnnotation: "cap1+cap2+cap3+cap4"},
			caps: ClusterCapabilities{
				KnownCapabilities:   map[configv1.ClusterVersionCapability]struct{}{configv1.ClusterVersionCapabilityOpenShiftSamples: {}},
				EnabledCapabilities: map[configv1.ClusterVersionCapability]struct{}{configv1.ClusterVersionCapabilityOpenShiftSamples: {}},
			},
			wantError: "unrecognized capability names: cap1, cap2, cap3, cap4",
		},
		{name: "disabled capabilitity annotation",
			annotations: map[string]string{CapabilityAnnotation: "cap1"},
			caps: ClusterCapabilities{
				KnownCapabilities:   map[configv1.ClusterVersionCapability]struct{}{"cap1": {}},
				EnabledCapabilities: map[configv1.ClusterVersionCapability]struct{}{},
			},
			wantError: "disabled capabilities: cap1",
		},
		{name: "multiple disabled capabilitities annotation",
			annotations: map[string]string{CapabilityAnnotation: "cap1+cap2+cap3+cap4+cap5+cap6"},
			caps: ClusterCapabilities{
				KnownCapabilities: map[configv1.ClusterVersionCapability]struct{}{"cap1": {}, "cap2": {},
					"cap3": {}, "cap4": {}, "cap5": {}, "cap6": {}},
				EnabledCapabilities: map[configv1.ClusterVersionCapability]struct{}{"cap1": {}, "cap2": {}, "cap3": {}},
			},
			wantError: "disabled capabilities: cap4, cap5, cap6",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := CheckResourceEnablement(test.annotations, test.caps)
			if len(test.wantError) == 0 {
				if err != nil {
					t.Errorf("Wanted no error, got error %q", err.Error())
				}
			} else if test.wantError != err.Error() {
				t.Errorf("Wanted error %q, got error %q", test.wantError, err.Error())
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

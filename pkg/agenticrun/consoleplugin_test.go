package agenticrun

import (
	"strings"
	"testing"

	configv1 "github.com/openshift/api/config/v1"
)

func TestResolveTLSProfileSpec(t *testing.T) {
	tests := []struct {
		name           string
		profile        *configv1.TLSSecurityProfile
		wantMinVersion configv1.TLSProtocolVersion
	}{
		{
			name:           "nil defaults to Intermediate",
			profile:        nil,
			wantMinVersion: configv1.VersionTLS12,
		},
		{
			name: "Intermediate",
			profile: &configv1.TLSSecurityProfile{
				Type:         configv1.TLSProfileIntermediateType,
				Intermediate: &configv1.IntermediateTLSProfile{},
			},
			wantMinVersion: configv1.VersionTLS12,
		},
		{
			name: "Modern",
			profile: &configv1.TLSSecurityProfile{
				Type:   configv1.TLSProfileModernType,
				Modern: &configv1.ModernTLSProfile{},
			},
			wantMinVersion: configv1.VersionTLS13,
		},
		{
			name: "Old",
			profile: &configv1.TLSSecurityProfile{
				Type: configv1.TLSProfileOldType,
				Old:  &configv1.OldTLSProfile{},
			},
			wantMinVersion: configv1.VersionTLS10,
		},
		{
			name: "Custom",
			profile: &configv1.TLSSecurityProfile{
				Type: configv1.TLSProfileCustomType,
				Custom: &configv1.CustomTLSProfile{
					TLSProfileSpec: configv1.TLSProfileSpec{
						Ciphers:       []string{"ECDHE-RSA-AES128-GCM-SHA256"},
						MinTLSVersion: configv1.VersionTLS13,
					},
				},
			},
			wantMinVersion: configv1.VersionTLS13,
		},
		{
			name: "Custom with nil Custom field falls back to Intermediate",
			profile: &configv1.TLSSecurityProfile{
				Type:   configv1.TLSProfileCustomType,
				Custom: nil,
			},
			wantMinVersion: configv1.VersionTLS12,
		},
		{
			name: "unknown type falls back to Intermediate",
			profile: &configv1.TLSSecurityProfile{
				Type: configv1.TLSProfileType("FutureTLSType"),
			},
			wantMinVersion: configv1.VersionTLS12,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			spec := resolveTLSProfileSpec(tt.profile)
			if spec == nil {
				t.Fatal("got nil spec")
			}
			if spec.MinTLSVersion != tt.wantMinVersion {
				t.Errorf("MinTLSVersion = %q, want %q", spec.MinTLSVersion, tt.wantMinVersion)
			}
		})
	}
}

func TestNginxTLSDirectives(t *testing.T) {
	tests := []struct {
		name             string
		profile          *configv1.TLSProfileSpec
		groupsEnabled    bool
		wantProtocols    string
		wantCiphers      string
		wantGroups       string
		noCipherContains string // substring that must NOT appear in ciphers
	}{
		{
			name:          "Intermediate profile",
			profile:       configv1.TLSProfiles[configv1.TLSProfileIntermediateType],
			wantProtocols: "TLSv1.2 TLSv1.3",
			wantGroups:    "auto",
		},
		{
			name:             "Modern profile uses TLS 1.3 only with placeholder cipher",
			profile:          configv1.TLSProfiles[configv1.TLSProfileModernType],
			wantProtocols:    "TLSv1.3",
			wantCiphers:      "ECDHE-ECDSA-AES128-GCM-SHA256",
			wantGroups:       "auto",
			noCipherContains: "TLS_",
		},
		{
			name:          "Old profile",
			profile:       configv1.TLSProfiles[configv1.TLSProfileOldType],
			wantProtocols: "TLSv1 TLSv1.1 TLSv1.2 TLSv1.3",
			wantGroups:    "auto",
		},
		{
			name: "unknown MinTLSVersion falls back to TLS 1.2 protocols",
			profile: &configv1.TLSProfileSpec{
				MinTLSVersion: configv1.TLSProtocolVersion("VersionTLS99"),
				Ciphers:       []string{"ECDHE-RSA-AES128-GCM-SHA256"},
			},
			wantProtocols: "TLSv1.2 TLSv1.3",
			wantCiphers:   "ECDHE-RSA-AES128-GCM-SHA256",
			wantGroups:    "auto",
		},
		{
			name: "TLS 1.3 ciphers are filtered out",
			profile: &configv1.TLSProfileSpec{
				MinTLSVersion: configv1.VersionTLS12,
				Ciphers: []string{
					"TLS_AES_128_GCM_SHA256",
					"ECDHE-RSA-AES128-GCM-SHA256",
					"TLS_CHACHA20_POLY1305_SHA256",
					"ECDHE-RSA-AES256-GCM-SHA384",
				},
			},
			wantProtocols:    "TLSv1.2 TLSv1.3",
			wantCiphers:      "ECDHE-RSA-AES128-GCM-SHA256:ECDHE-RSA-AES256-GCM-SHA384",
			wantGroups:       "auto",
			noCipherContains: "TLS_",
		},
		{
			name: "all TLS 1.3 ciphers get placeholder",
			profile: &configv1.TLSProfileSpec{
				MinTLSVersion: configv1.VersionTLS13,
				Ciphers:       []string{"TLS_AES_128_GCM_SHA256", "TLS_AES_256_GCM_SHA384"},
			},
			wantProtocols: "TLSv1.3",
			wantCiphers:   "ECDHE-ECDSA-AES128-GCM-SHA256",
			wantGroups:    "auto",
		},
		{
			name: "omitted groups use auto",
			profile: &configv1.TLSProfileSpec{
				MinTLSVersion: configv1.VersionTLS12,
				Ciphers:       []string{"ECDHE-RSA-AES128-GCM-SHA256"},
			},
			groupsEnabled: true,
			wantProtocols: "TLSv1.2 TLSv1.3",
			wantCiphers:   "ECDHE-RSA-AES128-GCM-SHA256",
			wantGroups:    "auto",
		},
		{
			name: "empty groups use auto",
			profile: &configv1.TLSProfileSpec{
				MinTLSVersion: configv1.VersionTLS12,
				Ciphers:       []string{"ECDHE-RSA-AES128-GCM-SHA256"},
				Groups:        []configv1.TLSGroup{},
			},
			groupsEnabled: true,
			wantProtocols: "TLSv1.2 TLSv1.3",
			wantCiphers:   "ECDHE-RSA-AES128-GCM-SHA256",
			wantGroups:    "auto",
		},
		{
			name: "disabled group preferences ignore explicit groups",
			profile: &configv1.TLSProfileSpec{
				MinTLSVersion: configv1.VersionTLS12,
				Ciphers:       []string{"ECDHE-RSA-AES128-GCM-SHA256"},
				Groups:        []configv1.TLSGroup{configv1.TLSGroupX25519},
			},
			wantProtocols: "TLSv1.2 TLSv1.3",
			wantCiphers:   "ECDHE-RSA-AES128-GCM-SHA256",
			wantGroups:    "auto",
		},
		{
			name: "group names and order are preserved",
			profile: &configv1.TLSProfileSpec{
				MinTLSVersion: configv1.VersionTLS12,
				Ciphers:       []string{"ECDHE-RSA-AES128-GCM-SHA256"},
				Groups:        []configv1.TLSGroup{configv1.TLSGroupSecP384r1, configv1.TLSGroupSecP256r1, configv1.TLSGroupX25519},
			},
			groupsEnabled: true,
			wantProtocols: "TLSv1.2 TLSv1.3",
			wantCiphers:   "ECDHE-RSA-AES128-GCM-SHA256",
			wantGroups:    "?secp384r1:?secp256r1:?X25519",
		},
		{
			name: "hybrid groups are left to OpenSSL",
			profile: &configv1.TLSProfileSpec{
				MinTLSVersion: configv1.VersionTLS12,
				Ciphers:       []string{"ECDHE-RSA-AES128-GCM-SHA256"},
				Groups:        []configv1.TLSGroup{configv1.TLSGroupX25519MLKEM768, configv1.TLSGroupSecP256r1MLKEM768, configv1.TLSGroupSecP384r1MLKEM1024},
			},
			groupsEnabled: true,
			wantProtocols: "TLSv1.2 TLSv1.3",
			wantCiphers:   "ECDHE-RSA-AES128-GCM-SHA256",
			wantGroups:    "?X25519MLKEM768:?SecP256r1MLKEM768:?SecP384r1MLKEM1024",
		},
		{
			name: "unsupported groups are optional without adding defaults",
			profile: &configv1.TLSProfileSpec{
				MinTLSVersion: configv1.VersionTLS12,
				Ciphers:       []string{"ECDHE-RSA-AES128-GCM-SHA256"},
				Groups:        []configv1.TLSGroup{"UnsupportedGroup"},
			},
			groupsEnabled: true,
			wantProtocols: "TLSv1.2 TLSv1.3",
			wantCiphers:   "ECDHE-RSA-AES128-GCM-SHA256",
			wantGroups:    "?UnsupportedGroup",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			protocols, ciphers, groups := nginxTLSDirectives(tt.profile, tt.groupsEnabled)
			if protocols != tt.wantProtocols {
				t.Errorf("protocols = %q, want %q", protocols, tt.wantProtocols)
			}
			if tt.wantCiphers != "" && ciphers != tt.wantCiphers {
				t.Errorf("ciphers = %q, want %q", ciphers, tt.wantCiphers)
			}
			if tt.noCipherContains != "" && strings.Contains(ciphers, tt.noCipherContains) {
				t.Errorf("ciphers %q should not contain %q", ciphers, tt.noCipherContains)
			}
			if ciphers == "" {
				t.Error("ciphers must never be empty (nginx rejects empty ssl_ciphers)")
			}
			if groups != tt.wantGroups {
				t.Errorf("groups = %q, want %q", groups, tt.wantGroups)
			}
		})
	}
}

package cvo

import (
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// testCABundle1 is a self-signed CA certificate for testing only (CN=test-ca).
const testCABundle1 = `-----BEGIN CERTIFICATE-----
MIIDBTCCAe2gAwIBAgIUALqftleDqWJSShxMMRUv/WcAppQwDQYJKoZIhvcNAQEL
BQAwEjEQMA4GA1UEAwwHdGVzdC1jYTAeFw0yNjA0MTAwNjU5MTdaFw0yNjA0MTEw
NjU5MTdaMBIxEDAOBgNVBAMMB3Rlc3QtY2EwggEiMA0GCSqGSIb3DQEBAQUAA4IB
DwAwggEKAoIBAQC1xT5ZKQc+ZiRXIDOcXhNvZ88KhHDF9iqU91s8Gvj0yiXqB9UO
HDFysRjqiXEMXqazw7Wq6OE0hCoM3znXw0NGjgA6Tnuvhw8uGd0Q34R0iC20YMUA
ln1Q93b5TRZjh97ViPhQ0hY2jZkaDM5F/gR9up1Z24okdXtzKd4fA0qjxDx6Yz/k
Zze+uf9Xy287cIwAc2ILhq9qRl/Z9nXdGLUVbZwLahidA+KqorGOSRhUTK2srTaU
Jzj0Z6XKcY6de/8quc9h8+UQ4ku5twPzwVfYUPkRAb0zm9A8jB1N4tyDXNiSIofi
JKbCnaD+6uw/Rl/cu498vKDZsx3Ug5PKNDmXAgMBAAGjUzBRMB0GA1UdDgQWBBRh
uVHEUPevz3HWe8xJplggeePnUDAfBgNVHSMEGDAWgBRhuVHEUPevz3HWe8xJplgg
eePnUDAPBgNVHRMBAf8EBTADAQH/MA0GCSqGSIb3DQEBCwUAA4IBAQCZDWO/T97v
IECb2vw+glagUup5nepZBpD/akiLLhpF8mZaS5NIfx3SsOVRJXodPHjahYvv0XzT
hniX9tlPp55vaAuCsEQnqP01Bm9YHkszB2C0saqwbchKJRqKUwcF6lQvVgAwm6O6
R9pKVa5Yqew8EhYla6YYP2Mvc3e8XhYfO/N5gqgb0BT9RjXIn5Ejb8YV/L1nKOtH
kWWacgzG2cTvoHiQUIik7SzPMFAU0ZpuOAOUuGp1YQq0pnD7ZPvZ4Xe9XhYB5Whi
CKo2N6BMFrMzrfyYf4oMm0TkTzA0wQdy5vcesbMa2cBKOv1m447AuNQryxc8zEZx
umQEpCT2KLUo
-----END CERTIFICATE-----
`

// testCABundle2 is a second self-signed CA certificate for testing only (CN=test-ca-2).
const testCABundle2 = `-----BEGIN CERTIFICATE-----
MIIDCTCCAfGgAwIBAgIUdsXWk3Ayiv/1BO/NrI0rpOr8q94wDQYJKoZIhvcNAQEL
BQAwFDESMBAGA1UEAwwJdGVzdC1jYS0yMB4XDTI2MDQxMDA2NTkyNFoXDTI2MDQx
MTA2NTkyNFowFDESMBAGA1UEAwwJdGVzdC1jYS0yMIIBIjANBgkqhkiG9w0BAQEF
AAOCAQ8AMIIBCgKCAQEAukjI/q0yQHN+uYbjW1YDxUbQdwN9oGo+p3e5eFvxShHL
EahJvZ673JrZhULRKMsoESJn9eMtkABXNMtugEGTexQag++Zly7xvEYDciyVxncE
uh8yrKyzyfkgYaBUoj+XimGnWk5DyGHET/7Ex2xY/3iXRknPkOO4TTlMyp0KZkAL
2xWQFgAnrrZ89HHAqgtpQ1gHvbmC80215vTBynBSbacgRjDK1NPnSmaA6Pkxa7mX
znUyBGeRct1+RIq9n0Fyadz/glZV8S7cGJUGAWuL2v7i6aWY1WJ+vt+SOzSkx/bK
wYOk9l6hvZKkqHgzAy1fFxGzdJ+eHNqllRKjsyWsdQIDAQABo1MwUTAdBgNVHQ4E
FgQUobxtuZWbsm6Xa1r6+v3VRmR81HUwHwYDVR0jBBgwFoAUobxtuZWbsm6Xa1r6
+v3VRmR81HUwDwYDVR0TAQH/BAUwAwEB/zANBgkqhkiG9w0BAQsFAAOCAQEAQdal
mib3V/3dMkHiajIIafT6IJp0peJh3LvP7BSpTfAE5v4cAApmmKZFko1TLq0qcItd
A9jdA8VcbGsCcWWMDTYQZhSZurGLrIbZ26tqlVX/jDcun75jx4P3Bcgj9tpEA7WF
DM9AIBF0SHmZKN2mOo7Dn+BcyUrlM1Sygyb1hAfpcu7D0bI+yJQj5aqNW8OG2zK0
zzWzHyuLepB67+vrl6Zoi3QPzR7nfXiGU52ggVZpCbluRhBIn5okNMBf3DdCtV2U
KjER4+2xPvFl3thrdb3V8ph7ujmtyPHx51F5+dDNmRRr/VOYSPxHfHgAPZ0bEvwN
bfKkg9UU+lJbK6aEOw==
-----END CERTIFICATE-----
`

// parseCertsFromPEM decodes all PEM certificates in data and returns them.
func parseCertsFromPEM(t *testing.T, data string) []*x509.Certificate {
	t.Helper()
	var certs []*x509.Certificate
	rest := []byte(data)
	for {
		var block *pem.Block
		block, rest = pem.Decode(rest)
		if block == nil {
			break
		}
		cert, err := x509.ParseCertificate(block.Bytes)
		if err != nil {
			t.Fatalf("failed to parse certificate: %v", err)
		}
		certs = append(certs, cert)
	}
	return certs
}

// poolContainsCert returns true when cert's raw subject DER is present in pool.
func poolContainsCert(pool *x509.CertPool, cert *x509.Certificate) bool {
	for _, s := range pool.Subjects() { //nolint:staticcheck // SA1019: acceptable here, we build the pool ourselves
		if string(s) == string(cert.RawSubject) {
			return true
		}
	}
	return false
}

func newCMForTest(name, caBundle string) *corev1.ConfigMap {
	return &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Data:       map[string]string{"ca-bundle.crt": caBundle},
	}
}

func TestGetTLSConfig(t *testing.T) {
	ca1Certs := parseCertsFromPEM(t, testCABundle1)
	ca2Certs := parseCertsFromPEM(t, testCABundle2)
	if len(ca1Certs) == 0 || len(ca2Certs) == 0 {
		t.Fatal("failed to parse test CA certificates")
	}
	ca1Cert := ca1Certs[0]
	ca2Cert := ca2Certs[0]

	listerErr := fmt.Errorf("transient lister error")

	tests := []struct {
		name             string
		trustedCABundle  *corev1.ConfigMap // openshift-config-managed/trusted-ca-bundle
		userCABundle     *corev1.ConfigMap // openshift-config/user-ca-bundle
		managedListerErr error             // non-NotFound error from the managed lister
		configListerErr  error             // non-NotFound error from the config lister
		hypershift       bool
		wantNilConfig    bool
		wantCA1Present   bool
		wantCA2Present   bool
		wantErr          bool
	}{
		{
			name:          "non-hypershift: no bundles → nil config",
			hypershift:    false,
			wantNilConfig: true,
		},
		{
			name:            "non-hypershift: trusted-ca-bundle only → CA1 trusted",
			hypershift:      false,
			trustedCABundle: newCMForTest("trusted-ca-bundle", testCABundle1),
			wantCA1Present:  true,
			wantCA2Present:  false,
		},
		{
			name:          "non-hypershift: user-ca-bundle present but hypershift=false → ignored",
			hypershift:    false,
			userCABundle:  newCMForTest("user-ca-bundle", testCABundle2),
			wantNilConfig: true,
		},
		{
			name:            "non-hypershift: both bundles but hypershift=false → only trusted-ca-bundle used",
			hypershift:      false,
			trustedCABundle: newCMForTest("trusted-ca-bundle", testCABundle1),
			userCABundle:    newCMForTest("user-ca-bundle", testCABundle2),
			wantCA1Present:  true,
			wantCA2Present:  false,
		},
		{
			name:          "hypershift: no bundles → nil config",
			hypershift:    true,
			wantNilConfig: true,
		},
		{
			name:            "hypershift: trusted-ca-bundle only → CA1 trusted",
			hypershift:      true,
			trustedCABundle: newCMForTest("trusted-ca-bundle", testCABundle1),
			wantCA1Present:  true,
			wantCA2Present:  false,
		},
		{
			name:           "hypershift: user-ca-bundle only → CA2 trusted",
			hypershift:     true,
			userCABundle:   newCMForTest("user-ca-bundle", testCABundle2),
			wantCA1Present: false,
			wantCA2Present: true,
		},
		{
			name:            "hypershift: both bundles → both CAs trusted",
			hypershift:      true,
			trustedCABundle: newCMForTest("trusted-ca-bundle", testCABundle1),
			userCABundle:    newCMForTest("user-ca-bundle", testCABundle2),
			wantCA1Present:  true,
			wantCA2Present:  true,
		},
		// Error path: non-NotFound errors from listers must be propagated.
		{
			name:             "managed lister non-NotFound error is propagated",
			hypershift:       false,
			managedListerErr: listerErr,
			wantErr:          true,
		},
		{
			name:            "hypershift: user-ca-bundle lister non-NotFound error is propagated",
			hypershift:      true,
			configListerErr: listerErr,
			wantErr:         true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			managedLister := &cmConfigLister{Err: tt.managedListerErr}
			if tt.trustedCABundle != nil {
				managedLister.Items = append(managedLister.Items, tt.trustedCABundle)
			}

			configLister := &cmConfigLister{Err: tt.configListerErr}
			if tt.userCABundle != nil {
				configLister.Items = append(configLister.Items, tt.userCABundle)
			}

			optr := &Operator{
				hypershift:            tt.hypershift,
				cmConfigManagedLister: managedLister,
				cmConfigLister:        configLister,
			}

			tlsCfg, err := optr.getTLSConfig()
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected an error but got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if tt.wantNilConfig {
				if tlsCfg != nil {
					t.Fatal("expected nil tls.Config, got non-nil")
				}
				return
			}

			if tlsCfg == nil {
				t.Fatal("expected non-nil tls.Config, got nil")
			}
			if tlsCfg.RootCAs == nil {
				t.Fatal("expected non-nil RootCAs, got nil")
			}

			if got := poolContainsCert(tlsCfg.RootCAs, ca1Cert); got != tt.wantCA1Present {
				if tt.wantCA1Present {
					t.Error("expected CA1 (from trusted-ca-bundle) to be in RootCAs but it is absent")
				} else {
					t.Error("expected CA1 (from trusted-ca-bundle) NOT to be in RootCAs but it is present")
				}
			}

			if got := poolContainsCert(tlsCfg.RootCAs, ca2Cert); got != tt.wantCA2Present {
				if tt.wantCA2Present {
					t.Error("expected CA2 (from user-ca-bundle) to be in RootCAs but it is absent")
				} else {
					t.Error("expected CA2 (from user-ca-bundle) NOT to be in RootCAs but it is present")
				}
			}
		})
	}
}

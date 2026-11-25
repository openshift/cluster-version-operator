package customsignaturestore

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"

	configv1 "github.com/openshift/api/config/v1"
)

type testClusterVersionLister struct {
	clusterVersion *configv1.ClusterVersion
	err            error
}

// signatureStoreRoundTripper keeps signature lookup tests isolated from the network.
type signatureStoreRoundTripper struct {
	requests atomic.Int32
}

func (t *signatureStoreRoundTripper) RoundTrip(request *http.Request) (*http.Response, error) {
	t.requests.Add(1)
	return &http.Response{
		StatusCode: http.StatusNotFound,
		Status:     "404 Not Found",
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader("")),
		Request:    request,
	}, nil
}

func (l *testClusterVersionLister) List(labels.Selector) ([]*configv1.ClusterVersion, error) {
	if l.err != nil {
		return nil, l.err
	}
	return []*configv1.ClusterVersion{l.clusterVersion}, nil
}

func (l *testClusterVersionLister) Get(name string) (*configv1.ClusterVersion, error) {
	if l.err != nil {
		return nil, l.err
	}
	if l.clusterVersion == nil || l.clusterVersion.Name != name {
		return nil, fmt.Errorf("ClusterVersion %q not found", name)
	}
	return l.clusterVersion, nil
}

func TestStoreStringUsesCurrentSignatureStores(t *testing.T) {
	const customURL = "https://example.com/signatures"
	const secondCustomURL = "https://mirror.example.com/signatures"
	tests := []struct {
		name            string
		signatureStores []configv1.SignatureStore
		want            string
	}{
		{
			name: "unset",
			want: "ClusterVersion signatureStores unset, falling back to default stores",
		},
		{
			name:            "empty",
			signatureStores: []configv1.SignatureStore{},
			want:            "0 ClusterVersion signatureStores",
		},
		{
			name:            "configured before signature lookup",
			signatureStores: []configv1.SignatureStore{{URL: customURL}, {URL: secondCustomURL}},
			want:            "ClusterVersion signatureStores configured: 2",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := &Store{
				Name: "version",
				Lister: &testClusterVersionLister{
					clusterVersion: &configv1.ClusterVersion{
						ObjectMeta: metav1.ObjectMeta{Name: "version"},
						Spec:       configv1.ClusterVersionSpec{SignatureStores: tt.signatureStores},
					},
				},
			}

			if got := store.String(); got != tt.want {
				t.Errorf("String() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestStoreStringRefreshesConfiguration(t *testing.T) {
	clusterVersion := &configv1.ClusterVersion{
		ObjectMeta: metav1.ObjectMeta{Name: "version"},
		Spec: configv1.ClusterVersionSpec{SignatureStores: []configv1.SignatureStore{
			{URL: "https://initial.example.com/signatures"},
		}},
	}
	lister := &testClusterVersionLister{clusterVersion: clusterVersion}
	roundTripper := &signatureStoreRoundTripper{}
	store := &Store{
		Name:   "version",
		Lister: lister,
		HTTPClient: func() (*http.Client, error) {
			return &http.Client{Transport: roundTripper}, nil
		},
	}

	if got, want := store.String(), "ClusterVersion signatureStores configured: 1"; got != want {
		t.Fatalf("String() = %q, want %q", got, want)
	}

	clusterVersion.Spec.SignatureStores = []configv1.SignatureStore{
		{URL: "https://one.example.com/signatures"},
		{URL: "https://two.example.com/signatures"},
	}
	if got, want := store.String(), "ClusterVersion signatureStores configured: 2"; got != want {
		t.Fatalf("String() before Signatures() = %q, want %q", got, want)
	}

	const (
		image  = "quay.io/example/release"
		digest = "sha256:8d75c2f67cc8beebcc3e7155297614f238987708fae4cf1393d116765b72e05f"
	)
	callback := func(context.Context, []byte, error) (bool, error) {
		return false, nil
	}
	err := store.Signatures(context.Background(), image, digest, callback)
	if err == nil || err.Error() != "ClusterVersion spec.signatureStores exhausted without finding a valid signature" {
		t.Fatalf("Signatures() error = %v, want exhausted-signature error", err)
	}
	if got, want := roundTripper.requests.Load(), int32(2); got != want {
		t.Fatalf("signature-store HTTP requests = %d, want %d", got, want)
	}

	clusterVersion.Spec.SignatureStores = []configv1.SignatureStore{
		{URL: "https://one.example.com/signatures"},
		{URL: "https://two.example.com/signatures"},
		{URL: "https://three.example.com/signatures"},
	}
	if got, want := store.String(), "ClusterVersion signatureStores configured: 3"; got != want {
		t.Fatalf("String() after Signatures() = %q, want %q", got, want)
	}
}

// TestStoreStringDoesNotExposeConfiguredURLs guards against leaking sensitive configuration into logs.
func TestStoreStringDoesNotExposeConfiguredURLs(t *testing.T) {
	store := &Store{
		Name: "version",
		Lister: &testClusterVersionLister{
			clusterVersion: &configv1.ClusterVersion{
				ObjectMeta: metav1.ObjectMeta{Name: "version"},
				Spec: configv1.ClusterVersionSpec{SignatureStores: []configv1.SignatureStore{{
					URL: "https://user:password@internal.example.com/private-token/signatures?access_token=query-secret#fragment-secret",
				}}},
			},
		},
	}

	if got, want := store.String(), "ClusterVersion signatureStores configured: 1"; got != want {
		t.Fatalf("String() = %q, want %q", got, want)
	}
}

// TestStoreStringDoesNotExposeConfigurationErrors prevents lookup details from leaking into logs.
func TestStoreStringDoesNotExposeConfigurationErrors(t *testing.T) {
	tests := []struct {
		name   string
		lister *testClusterVersionLister
	}{
		{
			name:   "lister error",
			lister: &testClusterVersionLister{err: errors.New("lister failed: access_token=private-token")},
		},
		{
			name: "invalid URL",
			lister: &testClusterVersionLister{clusterVersion: &configv1.ClusterVersion{
				ObjectMeta: metav1.ObjectMeta{Name: "version"},
				Spec: configv1.ClusterVersionSpec{SignatureStores: []configv1.SignatureStore{{
					URL: "https://internal.example.com/%zz?access_token=query-secret",
				}}},
			}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := &Store{Name: "version", Lister: tt.lister}
			if got, want := store.String(), "unable to load ClusterVersion signatureStores"; got != want {
				t.Errorf("String() = %q, want %q", got, want)
			}
		})
	}
}

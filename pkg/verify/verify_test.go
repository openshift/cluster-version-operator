package verify

import (
	"bytes"
	"context"
	"fmt"
	"io/ioutil"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"reflect"
	"testing"

	"golang.org/x/crypto/openpgp"
)

func Test_ReleaseVerifier_Verify(t *testing.T) {
	data, err := ioutil.ReadFile(filepath.Join("testdata", "keyrings", "redhat.txt"))
	if err != nil {
		t.Fatal(err)
	}
	redhatPublic, err := openpgp.ReadArmoredKeyRing(bytes.NewBuffer(data))
	if err != nil {
		t.Fatal(err)
	}
	data, err = ioutil.ReadFile(filepath.Join("testdata", "keyrings", "simple.txt"))
	if err != nil {
		t.Fatal(err)
	}
	simple, err := openpgp.ReadArmoredKeyRing(bytes.NewBuffer(data))
	if err != nil {
		t.Fatal(err)
	}
	data, err = ioutil.ReadFile(filepath.Join("testdata", "keyrings", "combined.txt"))
	if err != nil {
		t.Fatal(err)
	}
	combined, err := openpgp.ReadArmoredKeyRing(bytes.NewBuffer(data))
	if err != nil {
		t.Fatal(err)
	}

	serveSignatures := http.FileServer(http.Dir(filepath.Join("testdata", "signatures")))
	sigServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		serveSignatures.ServeHTTP(w, req)
	}))
	defer sigServer.Close()
	sigServerURL, _ := url.Parse(sigServer.URL)

	serveEmpty := http.FileServer(http.Dir(filepath.Join("testdata", "signatures-2")))
	emptyServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		serveEmpty.ServeHTTP(w, req)
	}))
	defer emptyServer.Close()
	emptyServerURL, _ := url.Parse(emptyServer.URL)

	tests := []struct {
		name          string
		verifiers     map[string]openpgp.EntityList
		stores        []*url.URL
		releaseDigest string
		wantErr       bool
	}{
		{releaseDigest: "", wantErr: true},
		{releaseDigest: "!", wantErr: true},

		{
			name:          "valid signature for sha over file",
			releaseDigest: "sha256:e3f12513a4b22a2d7c0e7c9207f52128113758d9d68c7d06b11a0ac7672966f7",
			stores: []*url.URL{
				{Scheme: "file", Path: "testdata/signatures"},
			},
			verifiers: map[string]openpgp.EntityList{"redhat": redhatPublic},
		},
		{
			name:          "valid signature for sha over http",
			releaseDigest: "sha256:e3f12513a4b22a2d7c0e7c9207f52128113758d9d68c7d06b11a0ac7672966f7",
			stores: []*url.URL{
				sigServerURL,
			},
			verifiers: map[string]openpgp.EntityList{"redhat": redhatPublic},
		},
		{
			name:          "valid signature for sha over http with custom gpg key",
			releaseDigest: "sha256:edd9824f0404f1a139688017e7001370e2f3fbc088b94da84506653b473fe140",
			stores: []*url.URL{
				sigServerURL,
			},
			verifiers: map[string]openpgp.EntityList{"simple": simple},
		},
		{
			name:          "valid signature for sha over http with multi-key keyring",
			releaseDigest: "sha256:edd9824f0404f1a139688017e7001370e2f3fbc088b94da84506653b473fe140",
			stores: []*url.URL{
				sigServerURL,
			},
			verifiers: map[string]openpgp.EntityList{"combined": combined},
		},

		{
			name:          "file store rejects if digest is not found",
			releaseDigest: "sha256:0000000000000000000000000000000000000000000000000000000000000000",
			stores: []*url.URL{
				{Scheme: "file", Path: "testdata/signatures"},
			},
			verifiers: map[string]openpgp.EntityList{"redhat": redhatPublic},
			wantErr:   true,
		},
		{
			name:          "http store rejects if digest is not found",
			releaseDigest: "sha256:0000000000000000000000000000000000000000000000000000000000000000",
			stores: []*url.URL{
				sigServerURL,
			},
			verifiers: map[string]openpgp.EntityList{"redhat": redhatPublic},
			wantErr:   true,
		},

		{
			name:          "sha contains invalid characters",
			releaseDigest: "!sha256:e3f12513a4b22a2d7c0e7c9207f52128113758d9d68c7d06b11a0ac7672966f7",
			stores: []*url.URL{
				{Scheme: "file", Path: "testdata/signatures"},
			},
			verifiers: map[string]openpgp.EntityList{"redhat": redhatPublic},
			wantErr:   true,
		},
		{
			name:          "sha contains too many separators",
			releaseDigest: "sha256:e3f12513a4b22a2d7c0e7c9207f52128113758d9d68c7d06b11a0ac7672966f7:",
			stores: []*url.URL{
				{Scheme: "file", Path: "testdata/signatures"},
			},
			verifiers: map[string]openpgp.EntityList{"redhat": redhatPublic},
			wantErr:   true,
		},

		{
			name:          "could not find signature in file store",
			releaseDigest: "sha256:e3f12513a4b22a2d7c0e7c9207f52128113758d9d68c7d06b11a0ac7672966f7",
			stores: []*url.URL{
				{Scheme: "file", Path: "testdata/signatures-2"},
			},
			verifiers: map[string]openpgp.EntityList{"redhat": redhatPublic},
			wantErr:   true,
		},
		{
			name:          "could not find signature in http store",
			releaseDigest: "sha256:e3f12513a4b22a2d7c0e7c9207f52128113758d9d68c7d06b11a0ac7672966f7",
			stores: []*url.URL{
				emptyServerURL,
			},
			verifiers: map[string]openpgp.EntityList{"redhat": redhatPublic},
			wantErr:   true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err != nil {
				t.Fatal(err)
			}
			v := &ReleaseVerifier{
				verifiers:     tt.verifiers,
				stores:        tt.stores,
				clientBuilder: DefaultClient,
			}
			if err := v.Verify(context.Background(), tt.releaseDigest); (err != nil) != tt.wantErr {
				t.Errorf("ReleaseVerifier.Verify() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func Test_ReleaseVerifier_String(t *testing.T) {
	data, err := ioutil.ReadFile(filepath.Join("testdata", "keyrings", "redhat.txt"))
	if err != nil {
		t.Fatal(err)
	}
	redhatPublic, err := openpgp.ReadArmoredKeyRing(bytes.NewBuffer(data))
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name      string
		verifiers map[string]openpgp.EntityList
		stores    []*url.URL
		want      string
	}{
		{
			want: `All release image digests must have GPG signatures from <ERROR: no verifiers> - will check for signatures in containers/image format at <ERROR: no stores>`,
		},
		{
			stores: []*url.URL{
				{Scheme: "http", Host: "localhost", Path: "test"},
				{Scheme: "file", Path: "/absolute/url"},
			},
			want: `All release image digests must have GPG signatures from <ERROR: no verifiers> - will check for signatures in containers/image format at http://localhost/test, file:///absolute/url`,
		},
		{
			verifiers: map[string]openpgp.EntityList{
				"redhat": redhatPublic,
			},
			stores: []*url.URL{{Scheme: "http", Host: "localhost", Path: "test"}},
			want:   `All release image digests must have GPG signatures from redhat (567E347AD0044ADE55BA8A5F199E2F91FD431D51: Red Hat, Inc. (release key 2) <security@redhat.com>) - will check for signatures in containers/image format at http://localhost/test`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			v := &ReleaseVerifier{
				verifiers: tt.verifiers,
				stores:    tt.stores,
			}
			if got := v.String(); got != tt.want {
				t.Errorf("ReleaseVerifier.String() = %v, want %v", got, tt.want)
			}
		})
	}
}

func Test_ReleaseVerifier_Signatures(t *testing.T) {
	data, err := ioutil.ReadFile(filepath.Join("testdata", "keyrings", "redhat.txt"))
	if err != nil {
		t.Fatal(err)
	}
	redhatPublic, err := openpgp.ReadArmoredKeyRing(bytes.NewBuffer(data))
	if err != nil {
		t.Fatal(err)
	}

	const signedDigest = "sha256:e3f12513a4b22a2d7c0e7c9207f52128113758d9d68c7d06b11a0ac7672966f7"

	// verify we don't cache a negative result
	verifier := NewReleaseVerifier(
		map[string]openpgp.EntityList{"redhat": redhatPublic},
		[]*url.URL{{Scheme: "file", Path: "testdata/signatures-wrong"}},
		DefaultClient,
	)
	if err := verifier.Verify(context.Background(), signedDigest); err == nil || err.Error() != "unable to locate a valid signature for one or more sources" {
		t.Fatal(err)
	}
	if sigs := verifier.Signatures(); len(sigs) != 0 {
		t.Fatalf("%#v", sigs)
	}

	// verify we cache a valid request
	verifier = NewReleaseVerifier(
		map[string]openpgp.EntityList{"redhat": redhatPublic},
		[]*url.URL{{Scheme: "file", Path: "testdata/signatures"}},
		DefaultClient,
	)
	if err := verifier.Verify(context.Background(), signedDigest); err != nil {
		t.Fatal(err)
	}
	if sigs := verifier.Signatures(); len(sigs) != 1 {
		t.Fatalf("%#v", sigs)
	}

	// verify we hit the cache instead of verifying, even after changing the stores directory
	verifier.stores = []*url.URL{{Scheme: "file", Path: "testdata/signatures-wrong"}}
	if err := verifier.Verify(context.Background(), signedDigest); err != nil {
		t.Fatal(err)
	}
	if sigs := verifier.Signatures(); len(sigs) != 1 {
		t.Fatalf("%#v", sigs)
	}

	// verify we maintain a maximum number of cache entries a valid request
	expectedSignature, err := ioutil.ReadFile(filepath.Join("testdata", "signatures", "sha256=e3f12513a4b22a2d7c0e7c9207f52128113758d9d68c7d06b11a0ac7672966f7", "signature-1"))
	if err != nil {
		t.Fatal(err)
	}

	verifier = NewReleaseVerifier(
		map[string]openpgp.EntityList{"redhat": redhatPublic},
		[]*url.URL{{Scheme: "file", Path: "testdata/signatures"}},
		DefaultClient,
	)
	for i := 0; i < maxSignatureCacheSize*2; i++ {
		verifier.signatureCache[fmt.Sprintf("test-%d", i)] = [][]byte{[]byte("blah")}
	}

	if err := verifier.Verify(context.Background(), signedDigest); err != nil {
		t.Fatal(err)
	}
	if sigs := verifier.Signatures(); len(sigs) != 64 || !reflect.DeepEqual(sigs[signedDigest], [][]byte{expectedSignature}) {
		t.Fatalf("%d %#v", len(sigs), sigs)
	}
}

package verify

import (
	"bytes"
	"context"
	"io/ioutil"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"testing"

	"k8s.io/client-go/transport"

	"github.com/openshift/cluster-version-operator/lib"
	"github.com/openshift/cluster-version-operator/pkg/payload"
	"golang.org/x/crypto/openpgp"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

func Test_releaseVerifier_Verify(t *testing.T) {
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
			transport, err := transport.New(&transport.Config{})
			if err != nil {
				t.Fatal(err)
			}
			v := &releaseVerifier{
				verifiers: tt.verifiers,
				stores:    tt.stores,
				client: &http.Client{
					Transport: transport,
				},
			}
			if err := v.Verify(context.Background(), tt.releaseDigest); (err != nil) != tt.wantErr {
				t.Errorf("releaseVerifier.Verify() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func Test_loadReleaseVerifierFromPayload(t *testing.T) {
	redhatData, err := ioutil.ReadFile(filepath.Join("testdata", "keyrings", "redhat.txt"))
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name          string
		update        *payload.Update
		want          bool
		wantErr       bool
		wantVerifiers int
	}{
		{
			name:   "is a no-op when no objects are found",
			update: &payload.Update{},
		},
		{
			name: "requires data",
			update: &payload.Update{
				Manifests: []lib.Manifest{
					{
						GVK: schema.GroupVersionKind{Version: "v1", Kind: "ConfigMap"},
						Obj: &unstructured.Unstructured{
							Object: map[string]interface{}{
								"metadata": map[string]interface{}{
									"name":      "release-verification",
									"namespace": "openshift-config-managed",
									"annotations": map[string]interface{}{
										"release.openshift.io/verification-config-map": "",
									},
								},
							},
						},
					},
				},
			},
			wantErr: true,
		},
		{
			name: "requires stores",
			update: &payload.Update{
				Manifests: []lib.Manifest{
					{
						GVK: schema.GroupVersionKind{Version: "v1", Kind: "ConfigMap"},
						Obj: &unstructured.Unstructured{
							Object: map[string]interface{}{
								"metadata": map[string]interface{}{
									"name":      "verification",
									"namespace": "openshift-config",
									"annotations": map[string]interface{}{
										"release.openshift.io/verification-config-map": "",
									},
								},
								"data": map[string]interface{}{
									"verifier-public-key-redhat": string(redhatData),
								},
							},
						},
					},
				},
			},
			wantErr: true,
		},
		{
			name: "requires verifiers",
			update: &payload.Update{
				Manifests: []lib.Manifest{
					{
						GVK: schema.GroupVersionKind{Version: "v1", Kind: "ConfigMap"},
						Obj: &unstructured.Unstructured{
							Object: map[string]interface{}{
								"metadata": map[string]interface{}{
									"name":      "release-verification",
									"namespace": "openshift-config-managed",
									"annotations": map[string]interface{}{
										"release.openshift.io/verification-config-map": "",
									},
								},
								"data": map[string]interface{}{
									"store-local": "file://testdata/signatures",
								},
							},
						},
					},
				},
			},
			wantErr: true,
		},
		{
			name: "loads valid configuration",
			update: &payload.Update{
				Manifests: []lib.Manifest{
					{
						GVK: schema.GroupVersionKind{Version: "v1", Kind: "ConfigMap"},
						Obj: &unstructured.Unstructured{
							Object: map[string]interface{}{
								"metadata": map[string]interface{}{
									"name":      "release-verification",
									"namespace": "openshift-config-managed",
									"annotations": map[string]interface{}{
										"release.openshift.io/verification-config-map": "",
									},
								},
								"data": map[string]interface{}{
									"verifier-public-key-redhat": string(redhatData),
									"store-local":                "file://testdata/signatures",
								},
							},
						},
					},
				},
			},
			want:          true,
			wantVerifiers: 1,
		},
		{
			name: "only the first valid configuration is used",
			update: &payload.Update{
				Manifests: []lib.Manifest{
					{
						GVK: schema.GroupVersionKind{Version: "v1", Kind: "ConfigMap"},
						Obj: &unstructured.Unstructured{
							Object: map[string]interface{}{
								"metadata": map[string]interface{}{
									"name":      "release-verification",
									"namespace": "openshift-config-managed",
									"annotations": map[string]interface{}{
										"release.openshift.io/verification-config-map": "",
									},
								},
								"data": map[string]interface{}{
									"verifier-public-key-redhat": string(redhatData),
									"store-local":                "\nfile://testdata/signatures\n",
								},
							},
						},
					},
					{
						GVK: schema.GroupVersionKind{Version: "v1", Kind: "ConfigMap"},
						Obj: &unstructured.Unstructured{
							Object: map[string]interface{}{
								"metadata": map[string]interface{}{
									"name":      "release-verificatio-2n",
									"namespace": "openshift-config-managed",
									"annotations": map[string]interface{}{
										"release.openshift.io/verification-config-map": "",
									},
								},
								"data": map[string]interface{}{
									"verifier-public-key-redhat":   string(redhatData),
									"verifier-public-key-redhat-2": string(redhatData),
									"store-local":                  "file://testdata/signatures",
								},
							},
						},
					},
				},
			},
			want:          true,
			wantVerifiers: 1,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := LoadFromPayload(tt.update)
			if (err != nil) != tt.wantErr {
				t.Fatalf("loadReleaseVerifierFromPayload() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if got != nil != tt.want {
				t.Fatal(got)
			}
			if err != nil {
				return
			}
			if got == nil {
				return
			}
			rv := got.(*releaseVerifier)
			if len(rv.verifiers) != tt.wantVerifiers {
				t.Fatalf("unexpected release verifier: %#v", rv)
			}
		})
	}
}

func Test_releaseVerifier_String(t *testing.T) {
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
			v := &releaseVerifier{
				verifiers: tt.verifiers,
				stores:    tt.stores,
			}
			if got := v.String(); got != tt.want {
				t.Errorf("releaseVerifier.String() = %v, want %v", got, tt.want)
			}
		})
	}
}

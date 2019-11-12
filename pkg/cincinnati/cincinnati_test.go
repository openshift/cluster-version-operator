package cincinnati

import (
	"crypto/tls"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"testing"

	"github.com/blang/semver"
	"github.com/google/uuid"
	_ "k8s.io/klog" // integration tests set glog flags.
)

func TestGetUpdates(t *testing.T) {
	clientID := uuid.Must(uuid.Parse("01234567-0123-0123-0123-0123456789ab"))
	arch := "test-arch"
	channelName := "test-channel"
	tests := []struct {
		name    string
		version string

		expectedQuery string
		available     []Update
		err           string
	}{{
		name:          "one update available",
		version:       "4.0.0-4",
		expectedQuery: "arch=test-arch&channel=test-channel&id=01234567-0123-0123-0123-0123456789ab&version=4.0.0-4",
		available: []Update{
			{semver.MustParse("4.0.0-5"), "quay.io/openshift-release-dev/ocp-release:4.0.0-5"},
		},
	}, {
		name:          "two updates available",
		version:       "4.0.0-5",
		expectedQuery: "arch=test-arch&channel=test-channel&id=01234567-0123-0123-0123-0123456789ab&version=4.0.0-5",
		available: []Update{
			{semver.MustParse("4.0.0-6"), "quay.io/openshift-release-dev/ocp-release:4.0.0-6"},
			{semver.MustParse("4.0.0-6+2"), "quay.io/openshift-release-dev/ocp-release:4.0.0-6+2"},
		},
	}, {
		name:          "no updates available",
		version:       "4.0.0-0.okd-0",
		expectedQuery: "arch=test-arch&channel=test-channel&id=01234567-0123-0123-0123-0123456789ab&version=4.0.0-0.okd-0",
	}, {
		name:          "unknown version",
		version:       "4.0.0-3",
		expectedQuery: "arch=test-arch&channel=test-channel&id=01234567-0123-0123-0123-0123456789ab&version=4.0.0-3",
		err:           "VersionNotFound: currently installed version 4.0.0-3 not found in the \"test-channel\" channel",
	}}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			requestQuery := make(chan string, 1)
			defer close(requestQuery)

			handler := func(w http.ResponseWriter, r *http.Request) {
				select {
				case requestQuery <- r.URL.RawQuery:
				default:
					t.Fatalf("received multiple requests at upstream URL")
				}

				if r.Method != http.MethodGet && r.Method != http.MethodHead {
					w.WriteHeader(http.StatusMethodNotAllowed)
					return
				}

				mtype := r.Header.Get("Accept")
				if mtype != GraphMediaType {
					w.WriteHeader(http.StatusUnsupportedMediaType)
					return
				}

				_, err := w.Write([]byte(`{
					"nodes": [
					  {
						"version": "4.0.0-4",
						"payload": "quay.io/openshift-release-dev/ocp-release:4.0.0-4",
						"metadata": {}
					  },
					  {
						"version": "4.0.0-5",
						"payload": "quay.io/openshift-release-dev/ocp-release:4.0.0-5",
						"metadata": {}
					  },
					  {
						"version": "4.0.0-6",
						"payload": "quay.io/openshift-release-dev/ocp-release:4.0.0-6",
						"metadata": {}
					  },
					  {
						"version": "4.0.0-6+2",
						"payload": "quay.io/openshift-release-dev/ocp-release:4.0.0-6+2",
						"metadata": {}
					  },
					  {
						"version": "4.0.0-0.okd-0",
						"payload": "quay.io/openshift-release-dev/ocp-release:4.0.0-0.okd-0",
						"metadata": {}
					  },
					  {
						"version": "4.0.0-0.2",
						"payload": "quay.io/openshift-release-dev/ocp-release:4.0.0-0.2",
						"metadata": {}
					  },
					  {
						"version": "4.0.0-0.3",
						"payload": "quay.io/openshift-release-dev/ocp-release:4.0.0-0.3",
						"metadata": {}
					  }
					],
					"edges": [[0,1],[1,2],[1,3],[5,6]]
				  }`))
				if err != nil {
					w.WriteHeader(http.StatusInternalServerError)
					return
				}
			}

			ts := httptest.NewServer(http.HandlerFunc(handler))
			defer ts.Close()
			var proxyURL *url.URL
			var tlsConfig *tls.Config

			c := NewClient(clientID, proxyURL, tlsConfig)

			uri, err := url.Parse(ts.URL)
			if err != nil {
				t.Fatal(err)
			}

			updates, err := c.GetUpdates(uri, arch, channelName, semver.MustParse(test.version))
			if test.err == "" {
				if err != nil {
					t.Fatalf("expected nil error, got: %v", err)
				}
				if !reflect.DeepEqual(updates, test.available) {
					t.Fatalf("expected %v, got: %v", test.available, updates)
				}
			} else {
				if err == nil || err.Error() != test.err {
					t.Fatalf("expected err to be %s, got: %v", test.err, err)
				}
			}

			actualQuery := ""
			select {
			case actualQuery = <-requestQuery:
			default:
				t.Fatal("no request received at upstream URL")
			}
			expectedQueryValues, err := url.ParseQuery(test.expectedQuery)
			if err != nil {
				t.Fatalf("could not parse expected query: %v", err)
			}
			actualQueryValues, err := url.ParseQuery(actualQuery)
			if err != nil {
				t.Fatalf("could not parse acutal query: %v", err)
			}
			if e, a := expectedQueryValues, actualQueryValues; !reflect.DeepEqual(e, a) {
				t.Errorf("expected query to be %q, got: %q", e, a)
			}
		})
	}
}

func Test_nodeUnmarshalJSON(t *testing.T) {
	tests := []struct {
		raw []byte

		exp node
		err string
	}{{
		raw: []byte(`{
			"version": "4.0.0-5",
			"payload": "quay.io/openshift-release-dev/ocp-release:4.0.0-5",
			"metadata": {}
		  }`),

		exp: node{semver.MustParse("4.0.0-5"), "quay.io/openshift-release-dev/ocp-release:4.0.0-5"},
	}, {
		raw: []byte(`{
			"version": "4.0.0-0.1",
			"payload": "quay.io/openshift-release-dev/ocp-release:4.0.0-0.1",
			"metadata": {
			  "description": "This is the beta1 image based on the 4.0.0-0.nightly-2019-01-15-010905 build"
			}
		  }`),
		exp: node{semver.MustParse("4.0.0-0.1"), "quay.io/openshift-release-dev/ocp-release:4.0.0-0.1"},
	}, {
		raw: []byte(`{
			"version": "v4.0.0-0.1",
			"payload": "quay.io/openshift-release-dev/ocp-release:4.0.0-0.1",
			"metadata": {
			  "description": "This is the beta1 image based on the 4.0.0-0.nightly-2019-01-15-010905 build"
			}
		  }`),
		err: `Invalid character(s) found in major number "v4"`,
	}, {
		raw: []byte(`{
			"version": "4-0-0+0.1",
			"payload": "quay.io/openshift-release-dev/ocp-release:4.0.0-0.1",
			"metadata": {
			  "description": "This is the beta1 image based on the 4.0.0-0.nightly-2019-01-15-010905 build"
			}
		  }
	  `),

		err: "No Major.Minor.Patch elements found",
	}}

	for idx, test := range tests {
		t.Run(fmt.Sprintf("#%d", idx), func(t *testing.T) {
			var n node
			err := json.Unmarshal(test.raw, &n)
			if test.err == "" {
				if err != nil {
					t.Fatalf("expecting nil error, got: %v", err)
				}
				if !reflect.DeepEqual(n, test.exp) {
					t.Fatalf("expecting %v got %v", test.exp, n)
				}
			} else {
				if err.Error() != test.err {
					t.Fatalf("expecting %s error, got: %v", test.err, err)
				}
			}
		})
	}
}

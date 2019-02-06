package cincinnati

import (
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"github.com/blang/semver"

	"github.com/google/uuid"
)

func TestGetUpdates(t *testing.T) {
	handler := func(w http.ResponseWriter, r *http.Request) {
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
				"image": "quay.io/openshift-release-dev/ocp-release:4.0.0-4",
				"metadata": {}
			  },
			  {
				"version": "4.0.0-5",
				"image": "quay.io/openshift-release-dev/ocp-release:4.0.0-5",
				"metadata": {}
			  },
			  {
				"version": "4.0.0-6",
				"image": "quay.io/openshift-release-dev/ocp-release:4.0.0-6",
				"metadata": {}
			  },
			  {
				"version": "4.0.0-6+2",
				"image": "quay.io/openshift-release-dev/ocp-release:4.0.0-6+2",
				"metadata": {}
			  },
			  {
				"version": "4.0.0-0.okd-0",
				"image": "quay.io/openshift-release-dev/ocp-release:4.0.0-0.okd-0",
				"metadata": {}
			  },
			  {
				"version": "4.0.0-0.2",
				"image": "quay.io/openshift-release-dev/ocp-release:4.0.0-0.2",
				"metadata": {}
			  },
			  {
				"version": "4.0.0-0.3",
				"image": "quay.io/openshift-release-dev/ocp-release:4.0.0-0.3",
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

	tests := []struct {
		name    string
		version string

		available []Update
		err       string
	}{{
		name:    "one update available",
		version: "4.0.0-4",
		available: []Update{
			{semver.MustParse("4.0.0-5"), "quay.io/openshift-release-dev/ocp-release:4.0.0-5"},
		},
	}, {
		name:    "two updates available",
		version: "4.0.0-5",
		available: []Update{
			{semver.MustParse("4.0.0-6"), "quay.io/openshift-release-dev/ocp-release:4.0.0-6"},
			{semver.MustParse("4.0.0-6+2"), "quay.io/openshift-release-dev/ocp-release:4.0.0-6+2"},
		},
	}, {
		name:    "no updates available",
		version: "4.0.0-0.okd-0",
	}, {
		name:    "unknown version",
		version: "4.0.0-3",
		err:     "unknown version 4.0.0-3",
	}}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ts := httptest.NewServer(http.HandlerFunc(handler))
			c := NewClient(uuid.New())

			updates, err := c.GetUpdates(ts.URL, "", semver.MustParse(test.version))
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
		})
	}
}

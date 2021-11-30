package cincinnati

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"testing"

	"github.com/blang/semver/v4"
	"github.com/google/uuid"
	configv1 "github.com/openshift/api/config/v1"
	_ "github.com/openshift/cluster-version-operator/pkg/clusterconditions/always"
	_ "github.com/openshift/cluster-version-operator/pkg/clusterconditions/promql"
	_ "k8s.io/klog/v2" // integration tests set glog flags.
)

func TestGetUpdates(t *testing.T) {
	clientID := uuid.Must(uuid.Parse("01234567-0123-0123-0123-0123456789ab"))
	arch := "test-arch"
	channelName := "test-channel"
	tests := []struct {
		name    string
		version string

		expectedQuery      string
		graph              string
		current            configv1.Release
		available          []configv1.Release
		conditionalUpdates []configv1.ConditionalUpdate
		err                string
	}{{
		name:    "one update available",
		version: "4.1.0",
		graph: `{
  "nodes": [
    {
      "version": "4.1.0",
      "payload": "quay.io/openshift-release-dev/ocp-release:4.1.0",
      "metadata": {
        "url": "https://example.com/errata/4.1.0",
	"io.openshift.upgrades.graph.release.channels": "test-channel,channel-a"
      }
    },
    {
      "version": "4.1.1",
      "payload": "quay.io/openshift-release-dev/ocp-release:4.1.1",
      "metadata": {
        "url": "https://example.com/errata/4.1.1",
	"io.openshift.upgrades.graph.release.channels": "test-channel"
      }
    }
  ],
  "edges": [[0,1]]
}`,
		expectedQuery: "arch=test-arch&channel=test-channel&id=01234567-0123-0123-0123-0123456789ab&version=4.1.0",
		current: configv1.Release{
			Version:  "4.1.0",
			Image:    "quay.io/openshift-release-dev/ocp-release:4.1.0",
			URL:      "https://example.com/errata/4.1.0",
			Channels: []string{"channel-a", "test-channel"},
		},
		available: []configv1.Release{
			{
				Version:  "4.1.1",
				Image:    "quay.io/openshift-release-dev/ocp-release:4.1.1",
				URL:      "https://example.com/errata/4.1.1",
				Channels: []string{"test-channel"},
			},
		},
	}, {
		name:    "two updates available",
		version: "4.1.0",
		graph: `{
  "nodes": [
    {
      "version": "4.1.0",
      "payload": "quay.io/openshift-release-dev/ocp-release:4.1.0",
      "metadata": {
        "url": "https://example.com/errata/4.1.0",
	"io.openshift.upgrades.graph.release.channels": "test-channel,channel-a"
      }
    },
    {
      "version": "4.1.1",
      "payload": "quay.io/openshift-release-dev/ocp-release:4.1.1",
      "metadata": {
        "url": "https://example.com/errata/4.1.1",
	"io.openshift.upgrades.graph.release.channels": "test-channel,channel-a"
      }
    },
    {
      "version": "4.1.2",
      "payload": "quay.io/openshift-release-dev/ocp-release:4.1.2",
      "metadata": {
        "url": "https://example.com/errata/4.1.2",
	"io.openshift.upgrades.graph.release.channels": "test-channel"
      }
    },
    {
      "version": "4.1.3",
      "payload": "quay.io/openshift-release-dev/ocp-release:4.1.3",
      "metadata": {
        "url": "https://example.com/errata/4.1.3",
	"io.openshift.upgrades.graph.release.channels": "test-channel"
      }
    }
  ],
  "edges": [[0,1], [0,2], [1,2], [2,3]]
}`,
		expectedQuery: "arch=test-arch&channel=test-channel&id=01234567-0123-0123-0123-0123456789ab&version=4.1.0",
		current: configv1.Release{
			Version:  "4.1.0",
			Image:    "quay.io/openshift-release-dev/ocp-release:4.1.0",
			URL:      "https://example.com/errata/4.1.0",
			Channels: []string{"channel-a", "test-channel"},
		},
		available: []configv1.Release{
			{
				Version:  "4.1.1",
				Image:    "quay.io/openshift-release-dev/ocp-release:4.1.1",
				URL:      "https://example.com/errata/4.1.1",
				Channels: []string{"channel-a", "test-channel"},
			},
			{
				Version:  "4.1.2",
				Image:    "quay.io/openshift-release-dev/ocp-release:4.1.2",
				URL:      "https://example.com/errata/4.1.2",
				Channels: []string{"test-channel"},
			},
		},
	}, {
		name:    "no updates available",
		version: "4.1.0-0.okd-0",
		graph: `{
  "nodes": [
    {
      "version": "4.1.0-0.okd-0",
      "payload": "quay.io/openshift-release-dev/ocp-release:4.1.0-0.okd-0",
      "metadata": {
        "url": "https://example.com/errata/4.1.0-0.okd-0",
	"io.openshift.upgrades.graph.release.channels": "test-channel,channel-a"
      }
    },
    {
      "version": "4.1.1",
      "payload": "quay.io/openshift-release-dev/ocp-release:4.1.1",
      "metadata": {
        "url": "https://example.com/errata/4.1.1",
	"io.openshift.upgrades.graph.release.channels": "test-channel"
      }
    },
    {
      "version": "4.1.2",
      "payload": "quay.io/openshift-release-dev/ocp-release:4.1.2",
      "metadata": {
        "url": "https://example.com/errata/4.1.2",
	"io.openshift.upgrades.graph.release.channels": "test-channel"
      }
    }
  ],
  "edges": [[1,2]]
}`,
		expectedQuery: "arch=test-arch&channel=test-channel&id=01234567-0123-0123-0123-0123456789ab&version=4.1.0-0.okd-0",
		current: configv1.Release{
			Version:  "4.1.0-0.okd-0",
			Image:    "quay.io/openshift-release-dev/ocp-release:4.1.0-0.okd-0",
			URL:      "https://example.com/errata/4.1.0-0.okd-0",
			Channels: []string{"channel-a", "test-channel"},
		},
	}, {
		name:    "conditional updates available",
		version: "4.1.0",
		graph: `{
  "nodes": [
    {
      "version": "4.1.0",
      "payload": "quay.io/openshift-release-dev/ocp-release:4.1.0",
      "metadata": {
        "url": "https://example.com/errata/4.1.0",
	"io.openshift.upgrades.graph.release.channels": "test-channel,channel-a"
      }
    },
    {
      "version": "4.1.1",
      "payload": "quay.io/openshift-release-dev/ocp-release:4.1.1",
      "metadata": {
        "url": "https://example.com/errata/4.1.1",
	"io.openshift.upgrades.graph.release.channels": "test-channel,channel-a"
      }
    },
    {
      "version": "4.1.2",
      "payload": "quay.io/openshift-release-dev/ocp-release:4.1.2",
      "metadata": {
        "url": "https://example.com/errata/4.1.2",
	"io.openshift.upgrades.graph.release.channels": "test-channel"
      }
    },
    {
      "version": "4.1.3",
      "payload": "quay.io/openshift-release-dev/ocp-release:4.1.3",
      "metadata": {
        "url": "https://example.com/errata/4.1.3",
	"io.openshift.upgrades.graph.release.channels": "test-channel"
      }
    }
  ],
  "edges": [[0,1], [0,2], [1,2], [2,3]],
  "conditionalEdges": [
    {
      "edges": [{"from": "4.1.0", "to": "4.1.3"}],
      "risks": [
        {
          "url": "https://example.com/bug/123",
          "name": "BugA",
          "message": "On clusters with a Proxy configured, everything breaks.",
          "matchingRules": [
            {
              "type": "PromQL",
              "promql": {
                "promql": "max(cluster_proxy_enabled{type=~\"https?\"})"
              }
            }
          ]
        },
        {
          "url": "https://example.com/bug/456",
          "name": "BugB",
          "message": "All 4.1.0 clusters are incompatible with 4.1.3, and must pass through 4.1.2 on their way to 4.1.3 to avoid breaking.",
          "matchingRules": [
            {
              "type": "Always"
            }
          ]
        }
      ]
    }
  ]
}`,
		expectedQuery: "arch=test-arch&channel=test-channel&id=01234567-0123-0123-0123-0123456789ab&version=4.1.0",
		current: configv1.Release{
			Version:  "4.1.0",
			Image:    "quay.io/openshift-release-dev/ocp-release:4.1.0",
			URL:      "https://example.com/errata/4.1.0",
			Channels: []string{"channel-a", "test-channel"},
		},
		available: []configv1.Release{
			{
				Version:  "4.1.1",
				Image:    "quay.io/openshift-release-dev/ocp-release:4.1.1",
				URL:      "https://example.com/errata/4.1.1",
				Channels: []string{"channel-a", "test-channel"},
			},
			{
				Version:  "4.1.2",
				Image:    "quay.io/openshift-release-dev/ocp-release:4.1.2",
				URL:      "https://example.com/errata/4.1.2",
				Channels: []string{"test-channel"},
			},
		},
		conditionalUpdates: []configv1.ConditionalUpdate{
			{
				Release: configv1.Release{
					Version:  "4.1.3",
					Image:    "quay.io/openshift-release-dev/ocp-release:4.1.3",
					URL:      "https://example.com/errata/4.1.3",
					Channels: []string{"test-channel"},
				},
				Risks: []configv1.ConditionalUpdateRisk{
					{
						URL:     "https://example.com/bug/123",
						Name:    "BugA",
						Message: "On clusters with a Proxy configured, everything breaks.",
						MatchingRules: []configv1.ClusterCondition{
							{
								Type: "PromQL",
								PromQL: &configv1.PromQLClusterCondition{
									PromQL: "max(cluster_proxy_enabled{type=~\"https?\"})",
								},
							},
						},
					}, {
						URL:     "https://example.com/bug/456",
						Name:    "BugB",
						Message: "All 4.1.0 clusters are incompatible with 4.1.3, and must pass through 4.1.2 on their way to 4.1.3 to avoid breaking.",
						MatchingRules: []configv1.ClusterCondition{
							{
								Type: "Always",
							},
						},
					},
				},
			},
		},
	}, {
		name:    "conditional updates available, but have no recognized rules",
		version: "4.1.0",
		graph: `{
  "nodes": [
    {
      "version": "4.1.0",
      "payload": "quay.io/openshift-release-dev/ocp-release:4.1.0",
      "metadata": {
        "url": "https://example.com/errata/4.1.0",
	"io.openshift.upgrades.graph.release.channels": "test-channel,channel-a"
      }
    },
    {
      "version": "4.1.1",
      "payload": "quay.io/openshift-release-dev/ocp-release:4.1.1",
      "metadata": {
        "url": "https://example.com/errata/4.1.1",
	"io.openshift.upgrades.graph.release.channels": "test-channel,channel-a"
      }
    }
  ],
  "conditionalEdges": [
    {
      "edges": [{"from": "4.1.0", "to": "4.1.1"}],
      "risks": [
        {
          "url": "https://example.com/bug/123",
          "name": "BugA",
          "message": "On clusters with a Proxy configured, everything breaks.",
          "matchingRules": [
            {
              "type": "PromQL",
              "promql": {
                "promql": "max(cluster_proxy_enabled{type=~\"https?\"})"
              }
            }
          ]
        },
        {
          "url": "https://example.com/bug/456",
          "name": "BugB",
          "message": "This risk has no recognized rules, and so the conditional update to 4.1.1 will be dropped to avoid rejections when pushing to the Kubernetes API server.",
          "matchingRules": [
            {
              "type": "does-not-exist"
            }
          ]
        }
      ]
    }
  ]
}`,
		expectedQuery: "arch=test-arch&channel=test-channel&id=01234567-0123-0123-0123-0123456789ab&version=4.1.0",
		current: configv1.Release{
			Version:  "4.1.0",
			Image:    "quay.io/openshift-release-dev/ocp-release:4.1.0",
			URL:      "https://example.com/errata/4.1.0",
			Channels: []string{"channel-a", "test-channel"},
		},
	}, {
		name:    "conditional updates available, and overlap with unconditional edge",
		version: "4.1.0",
		graph: `{
  "nodes": [
    {
      "version": "4.1.0",
      "payload": "quay.io/openshift-release-dev/ocp-release:4.1.0",
      "metadata": {
        "url": "https://example.com/errata/4.1.0",
	"io.openshift.upgrades.graph.release.channels": "test-channel,channel-a"
      }
    },
    {
      "version": "4.1.1",
      "payload": "quay.io/openshift-release-dev/ocp-release:4.1.1",
      "metadata": {
        "url": "https://example.com/errata/4.1.1",
	"io.openshift.upgrades.graph.release.channels": "test-channel"
      }
    }
  ],
  "edges": [[0,1]],
  "conditionalEdges": [
    {
      "edges": [{"from": "4.1.0", "to": "4.1.1"}],
      "risks": [
        {
          "url": "https://example.com/bug/123",
          "name": "BugA",
          "message": "On clusters with a Proxy configured, everything breaks.",
          "matchingRules": [
            {
              "type": "PromQL",
              "promql": {
                "promql": "max(cluster_proxy_enabled{type=~\"https?\"})"
              }
            }
          ]
        }
      ]
    }
  ]
}`,
		expectedQuery: "arch=test-arch&channel=test-channel&id=01234567-0123-0123-0123-0123456789ab&version=4.1.0",
		current: configv1.Release{
			Version:  "4.1.0",
			Image:    "quay.io/openshift-release-dev/ocp-release:4.1.0",
			URL:      "https://example.com/errata/4.1.0",
			Channels: []string{"channel-a", "test-channel"},
		},
		conditionalUpdates: []configv1.ConditionalUpdate{
			{
				Release: configv1.Release{
					Version:  "4.1.1",
					Image:    "quay.io/openshift-release-dev/ocp-release:4.1.1",
					URL:      "https://example.com/errata/4.1.1",
					Channels: []string{"test-channel"},
				},
				Risks: []configv1.ConditionalUpdateRisk{
					{
						URL:     "https://example.com/bug/123",
						Name:    "BugA",
						Message: "On clusters with a Proxy configured, everything breaks.",
						MatchingRules: []configv1.ClusterCondition{
							{
								Type: "PromQL",
								PromQL: &configv1.PromQLClusterCondition{
									PromQL: "max(cluster_proxy_enabled{type=~\"https?\"})",
								},
							},
						},
					},
				},
			},
		},
	}, {
		name:    "multiple conditional updates with a single target",
		version: "4.1.0",
		graph: `{
  "nodes": [
    {
      "version": "4.1.0",
      "payload": "quay.io/openshift-release-dev/ocp-release:4.1.0",
      "metadata": {
        "url": "https://example.com/errata/4.1.0",
	"io.openshift.upgrades.graph.release.channels": "test-channel,channel-a"
      }
    },
    {
      "version": "4.1.1",
      "payload": "quay.io/openshift-release-dev/ocp-release:4.1.1",
      "metadata": {
        "url": "https://example.com/errata/4.1.1",
	"io.openshift.upgrades.graph.release.channels": "test-channel"
      }
    }
  ],
  "conditionalEdges": [
    {
      "edges": [{"from": "4.1.0", "to": "4.1.1"}],
      "risks": [
        {
          "url": "https://example.com/bug/123",
          "name": "BugA",
          "message": "On clusters with a Proxy configured, everything breaks.",
          "matchingRules": [
            {
              "type": "PromQL",
              "promql": {
                "promql": "max(cluster_proxy_enabled{type=~\"https?\"})"
              }
            }
          ]
        }
      ]
    }, {
      "edges": [{"from": "4.1.0", "to": "4.1.1"}],
      "risks": [
        {
          "url": "https://example.com/bug/456",
          "name": "BugB",
          "message": "All 4.1.0 clusters are incompatible with 4.1.3, and must pass through 4.1.2 on their way to 4.1.3 to avoid breaking.",
          "matchingRules": [
            {
              "type": "Always"
            }
          ]
        }
      ]
    }
  ]
}`,
		expectedQuery: "arch=test-arch&channel=test-channel&id=01234567-0123-0123-0123-0123456789ab&version=4.1.0",
		current: configv1.Release{
			Version:  "4.1.0",
			Image:    "quay.io/openshift-release-dev/ocp-release:4.1.0",
			URL:      "https://example.com/errata/4.1.0",
			Channels: []string{"channel-a", "test-channel"},
		},
	}, {
		name:    "condition with risks after invalid risk",
		version: "4.1.0",
		graph: `{
  "nodes": [
    {
      "version": "4.1.0",
      "payload": "quay.io/openshift-release-dev/ocp-release:4.1.0",
      "metadata": {
        "url": "https://example.com/errata/4.1.0",
	"io.openshift.upgrades.graph.release.channels": "test-channel,channel-a"
      }
    },
    {
      "version": "4.1.1",
      "payload": "quay.io/openshift-release-dev/ocp-release:4.1.1",
      "metadata": {
        "url": "https://example.com/errata/4.1.1",
	"io.openshift.upgrades.graph.release.channels": "test-channel"
      }
    }
  ],
  "conditionalEdges": [
    {
      "edges": [{"from": "4.1.0", "to": "4.1.1"}],
      "risks": [
        {
          "url": "https://example.com/bug/123",
          "name": "BugA",
          "message": "This risk has no recognized rules, and so the conditional update to 4.1.1 will be dropped to avoid rejections when pushing to the Kubernetes API server.",
          "matchingRules": [
            {
              "type": "does-not-exist"
            }
          ]
        },
        {
          "url": "https://example.com/bug/456",
          "name": "BugB",
          "message": "All 4.1.0 clusters are incompatible with 4.1.1.",
          "matchingRules": [
            {
              "type": "Always"
            }
          ]
        }
      ]
    }
  ]
}`,
		expectedQuery: "arch=test-arch&channel=test-channel&id=01234567-0123-0123-0123-0123456789ab&version=4.1.0",
		current: configv1.Release{
			Version:  "4.1.0",
			Image:    "quay.io/openshift-release-dev/ocp-release:4.1.0",
			URL:      "https://example.com/errata/4.1.0",
			Channels: []string{"channel-a", "test-channel"},
		},
	}, {
		name:    "unknown version",
		version: "4.1.0",
		graph: `{
  "nodes": [
    {
      "version": "4.1.1",
      "payload": "quay.io/openshift-release-dev/ocp-release:4.1.1",
      "metadata": {
        "url": "https://example.com/errata/4.1.1",
	"io.openshift.upgrades.graph.release.channels": "test-channel,channel-a"
      }
    }
  ]
}`,
		expectedQuery: "arch=test-arch&channel=test-channel&id=01234567-0123-0123-0123-0123456789ab&version=4.1.0",
		err:           "VersionNotFound: currently reconciling cluster version 4.1.0 not found in the \"test-channel\" channel",
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

				_, err := w.Write([]byte(test.graph))
				if err != nil {
					w.WriteHeader(http.StatusInternalServerError)
					return
				}
			}

			ts := httptest.NewServer(http.HandlerFunc(handler))
			defer ts.Close()

			c := NewClient(clientID, nil)

			uri, err := url.Parse(ts.URL)
			if err != nil {
				t.Fatal(err)
			}

			current, updates, conditionalUpdates, err := c.GetUpdates(context.Background(), uri, arch, channelName, semver.MustParse(test.version))
			if test.err == "" {
				if err != nil {
					t.Fatalf("expected nil error, got: %v", err)
				}
				if !reflect.DeepEqual(current, test.current) {
					t.Fatalf("expected current %v, got: %v", test.current, current)
				}
				if !reflect.DeepEqual(updates, test.available) {
					t.Fatalf("expected updates %v, got: %v", test.available, updates)
				}
				if !reflect.DeepEqual(conditionalUpdates, test.conditionalUpdates) {
					t.Fatalf("expected conditional updates %v, got: %v", test.conditionalUpdates, conditionalUpdates)
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

		exp: node{
			Version:  semver.MustParse("4.0.0-5"),
			Image:    "quay.io/openshift-release-dev/ocp-release:4.0.0-5",
			Metadata: map[string]string{},
		},
	}, {
		raw: []byte(`{
			"version": "4.0.0-0.1",
			"payload": "quay.io/openshift-release-dev/ocp-release:4.0.0-0.1",
			"metadata": {
			  "description": "This is the beta1 image based on the 4.0.0-0.nightly-2019-01-15-010905 build"
			}
		  }`),
		exp: node{
			Version: semver.MustParse("4.0.0-0.1"),
			Image:   "quay.io/openshift-release-dev/ocp-release:4.0.0-0.1",
			Metadata: map[string]string{
				"description": "This is the beta1 image based on the 4.0.0-0.nightly-2019-01-15-010905 build",
			},
		},
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

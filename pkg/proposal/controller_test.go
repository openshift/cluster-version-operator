package proposal

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"k8s.io/client-go/kubernetes/scheme"

	configv1 "github.com/openshift/api/config/v1"

	proposalv1alpha1 "github.com/openshift/cluster-version-operator/pkg/proposal/api/v1alpha1"
)

func init() {
	err := proposalv1alpha1.AddToScheme(scheme.Scheme)
	if err != nil {
		panic(err)
	}
}

func TestController_Sync(t *testing.T) {
	tests := []struct {
		name              string
		updatesGetterFunc UpdatesGetterFunc
		client            ctrlruntimeclient.Client
		cvGetterFunc      cvGetterFunc
		expected          error
		verifyFunc        func(client ctrlruntimeclient.Client) error
	}{
		{
			name: "basic case",
			updatesGetterFunc: func() ([]configv1.Release, []configv1.ConditionalUpdate, error) {
				return nil, nil, nil
			},
			cvGetterFunc: func(_ string) (*configv1.ClusterVersion, error) {
				return &configv1.ClusterVersion{}, nil
			},
			client: fake.NewClientBuilder().Build(),
			verifyFunc: func(client ctrlruntimeclient.Client) error {
				proposals := &proposalv1alpha1.ProposalList{}
				if err := client.List(context.Background(), proposals); err != nil {
					return err
				}
				if len(proposals.Items) == 0 {
					return fmt.Errorf("expected proposals, none")
				}
				return nil
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := NewController(tt.updatesGetterFunc, tt.client, tt.cvGetterFunc)
			actual := c.Sync(context.Background(), tt.name)
			if diff := cmp.Diff(tt.expected, actual, cmp.Transformer("Error", func(e error) string {
				if e == nil {
					return ""
				}
				return e.Error()
			})); diff != "" {
				t.Errorf("unexpected error (-want +got):\n%s", diff)
			}
			if tt.verifyFunc != nil {
				if err := tt.verifyFunc(tt.client); err != nil {
					t.Errorf("unexpected error: %v", err)
				}
			}
		})
	}
}

func TestClassifyUpdate(t *testing.T) {
	tests := []struct {
		name     string
		current  string
		target   string
		expected string
	}{
		{name: "z-stream", current: "4.15.1", target: "4.15.3", expected: "z-stream"},
		{name: "minor", current: "4.15.1", target: "4.16.0", expected: "minor"},
		{name: "major", current: "4.15.1", target: "5.0.0", expected: "minor"},
		{name: "invalid current", current: "bad", target: "4.15.0", expected: "unknown"},
		{name: "invalid target", current: "4.15.0", target: "bad", expected: "unknown"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := classifyUpdate(tt.current, tt.target)
			if got != tt.expected {
				t.Errorf("classifyUpdate(%q, %q) = %q, want %q", tt.current, tt.target, got, tt.expected)
			}
		})
	}
}

func TestProposalName(t *testing.T) {
	tests := []struct {
		current  string
		target   string
		expected string
	}{
		{"4.15.1", "4.15.3", "ota-4-15-1-to-4-15-3"},
		{"4.15.1", "4.16.0", "ota-4-15-1-to-4-16-0"},
	}

	for _, tt := range tests {
		t.Run(tt.current+"->"+tt.target, func(t *testing.T) {
			got := proposalName(tt.current, tt.target)
			if got != tt.expected {
				t.Errorf("proposalName(%q, %q) = %q, want %q", tt.current, tt.target, got, tt.expected)
			}
		})
	}
}

func TestSanitize(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"4.15.1", "4-15-1"},
		{"Hello World", "hello-world"},
		{"a-very-long-version-string-that-is-too-long", "a-very-long-version"},
		{"trailing-dot.", "trailing-dot"},
		{"trailing-dash-", "trailing-dash"},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := sanitize(tt.input)
			if got != tt.expected {
				t.Errorf("sanitize(%q) = %q, want %q", tt.input, got, tt.expected)
			}
		})
	}
}

func TestBuildRequest(t *testing.T) {
	updates := []configv1.Release{
		{Version: "4.16.0", URL: "https://example.com/errata/1"},
		{Version: "4.16.1", URL: "https://example.com/errata/2"},
	}

	t.Run("recommended target", func(t *testing.T) {
		request := buildRequest("", "4.15.3", "4.16.0", "stable-4.16", "minor", "recommended", updates, "")
		if !strings.Contains(request, "Current version: OCP 4.15.3") {
			t.Error("request should contain current version")
		}
		if !strings.Contains(request, "Target version: OCP 4.16.0") {
			t.Error("request should contain target version")
		}
		if !strings.Contains(request, "Update type: minor") {
			t.Error("request should contain update type")
		}
		if !strings.Contains(request, "Update path: recommended") {
			t.Error("request should contain update path")
		}
		if strings.Contains(request, "WARNING") {
			t.Error("recommended target should not have warning")
		}
		if !strings.Contains(request, "Other recommended versions available:") {
			t.Error("should list other versions when more than one update")
		}
		if !strings.Contains(request, "4.16.1") {
			t.Error("should list alternative version")
		}
	})

	t.Run("conditional target", func(t *testing.T) {
		request := buildRequest("", "4.15.3", "4.16.0", "stable-4.16", "minor", "conditional", updates, "")
		if !strings.Contains(request, "WARNING") {
			t.Error("conditional target should have warning")
		}
		if !strings.Contains(request, "CONDITIONAL update") {
			t.Error("conditional target should mention CONDITIONAL")
		}
	})

	t.Run("readiness JSON embedded", func(t *testing.T) {
		request := buildRequest("", "4.15.3", "4.16.0", "stable-4.16", "minor", "recommended", updates, `{"checks":{},"meta":{}}`)
		if !strings.Contains(request, "## Cluster Readiness Data") {
			t.Error("request should contain readiness data header")
		}
		if !strings.Contains(request, `{"checks":{},"meta":{}}`) {
			t.Error("request should contain readiness JSON")
		}
	})
}

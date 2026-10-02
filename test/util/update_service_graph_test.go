package util

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/blang/semver/v4"
	"github.com/google/go-cmp/cmp"

	configv1 "github.com/openshift/api/config/v1"

	"github.com/openshift/cluster-version-operator/pkg/cincinnati"
)

func TestGenerateGraph_RisksAlways(t *testing.T) {
	channel := "risks-always"
	current := configv1.Release{
		Version: "4.17.5",
		Image:   "quay.io/openshift-release-dev/ocp-release@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		URL:     "https://example.com/errata/current",
	}

	raw, err := GenerateGraph(current, channel)
	if err != nil {
		t.Fatalf("GenerateGraph() error = %v", err)
	}

	var got cincinnati.Graph
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}

	want := cincinnati.Graph{
		Nodes: []cincinnati.Node{
			{
				Version: semver.MustParse("4.17.5"),
				Image:   current.Image,
				Metadata: map[string]interface{}{
					"io.openshift.upgrades.graph.release.channels": channel,
					"url": "https://example.com/errata/current",
				},
			},
			{
				Version: semver.MustParse("4.17.6"),
				Image:   "example.com/test@sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
				Metadata: map[string]interface{}{
					"io.openshift.upgrades.graph.release.channels": channel,
					"url": "https://access.redhat.com/errata/RHSA-2024:05706",
				},
			},
			{
				Version: semver.MustParse("4.18.0"),
				Image:   "example.com/test@sha256:cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc",
				Metadata: map[string]interface{}{
					"io.openshift.upgrades.graph.release.channels": channel,
					"url": "https://access.redhat.com/errata/RHSA-2024:05800",
				},
			},
		},
		Edges: []cincinnati.Edge{},
		ConditionalEdges: []cincinnati.ConditionalEdges{
			{
				Edges: []cincinnati.ConditionalEdge{{From: "4.17.5", To: "4.17.6"}},
				Risks: []configv1.ConditionalUpdateRisk{
					{
						URL:     "https://docs.openshift.com/synthetic-risk-a",
						Name:    "SyntheticRiskA",
						Message: "This is a synthetic risk A that always applies for testing purposes",
						MatchingRules: []configv1.ClusterCondition{
							{Type: "Always"},
						},
					},
					{
						URL:     "https://docs.openshift.com/synthetic-risk-b",
						Name:    "SyntheticRiskB",
						Message: "This is a synthetic risk B that always applies for testing purposes",
						MatchingRules: []configv1.ClusterCondition{
							{Type: "Always"},
						},
					},
				},
			},
			{
				Edges: []cincinnati.ConditionalEdge{{From: "4.17.5", To: "4.18.0"}},
				Risks: []configv1.ConditionalUpdateRisk{
					{
						URL:     "https://docs.openshift.com/synthetic-risk-a",
						Name:    "SyntheticRiskA",
						Message: "This is a synthetic risk A that always applies for testing purposes",
						MatchingRules: []configv1.ClusterCondition{
							{Type: "Always"},
						},
					},
					{
						URL:     "https://docs.openshift.com/synthetic-risk-c",
						Name:    "SyntheticRiskC",
						Message: "This is a synthetic risk C that always applies for testing purposes",
						MatchingRules: []configv1.ClusterCondition{
							{Type: "Always"},
						},
					},
				},
			},
		},
	}

	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("GenerateGraph() mismatch (-want +got):\n%s", diff)
	}
}

func TestGenerateGraph_MultiArch(t *testing.T) {
	current := configv1.Release{
		Version:      "4.17.5",
		Image:        "example.com/current@sha256:deadbeef",
		Architecture: configv1.ClusterVersionArchitectureMulti,
	}

	raw, err := GenerateGraph(current, "risks-always")
	if err != nil {
		t.Fatalf("GenerateGraph() error = %v", err)
	}

	var got cincinnati.Graph
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}

	for i, node := range got.Nodes {
		arch, ok := node.Metadata["release.openshift.io/architecture"]
		if !ok {
			t.Errorf("node[%d] missing release.openshift.io/architecture", i)
			continue
		}
		if arch != "multi" {
			t.Errorf("node[%d] architecture = %v, want multi", i, arch)
		}
		if _, ok := node.Metadata["url"]; !ok {
			t.Errorf("node[%d] missing url", i)
		}
	}
}

func TestGenerateGraph_Prerelease(t *testing.T) {
	current := configv1.Release{
		Version: "4.20.0-0.nightly-2026-01-01-000000",
		Image:   "example.com/current@sha256:deadbeef",
	}
	raw, err := GenerateGraph(current, "risks-always")
	if err != nil {
		t.Fatalf("GenerateGraph() error = %v", err)
	}

	var got cincinnati.Graph
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}

	if got.Nodes[0].Version.String() != "4.20.0-0.nightly-2026-01-01-000000" {
		t.Errorf("current node version = %q, want current prerelease", got.Nodes[0].Version)
	}
	if got.Nodes[1].Version.String() != "4.20.1" {
		t.Errorf("patch bump node version = %q, want 4.20.1", got.Nodes[1].Version)
	}
	if got.Nodes[2].Version.String() != "4.21.0" {
		t.Errorf("minor bump node version = %q, want 4.21.0", got.Nodes[2].Version)
	}
}

func TestGenerateGraph_UnsupportedChannel(t *testing.T) {
	current := configv1.Release{Version: "4.17.5", Image: "example.com/current@sha256:deadbeef"}
	_, err := GenerateGraph(current, "unknown-channel")
	if err == nil {
		t.Fatal("GenerateGraph() error = nil, want unsupported channel error")
	}
	if !strings.Contains(err.Error(), "unsupported channel") {
		t.Errorf("GenerateGraph() error = %v, want unsupported channel", err)
	}
}

package risk_test

import (
	"context"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	configv1 "github.com/openshift/api/config/v1"

	"github.com/openshift/cluster-version-operator/pkg/risk"
	"github.com/openshift/cluster-version-operator/pkg/risk/mock"
)

func Test_Merge(t *testing.T) {
	tests := []struct {
		name                       string
		updates                    []configv1.Release
		conditionalUpdates         []configv1.ConditionalUpdate
		risks                      risk.Source
		expectedUpdates            []configv1.Release
		expectedConditionalUpdates []configv1.ConditionalUpdate
		expectedError              string
	}{
		{
			name: "basic case",
			updates: []configv1.Release{
				{
					Version: "4.21.1",
				},
				{
					Version: "4.21.2",
				},
			},
			conditionalUpdates: []configv1.ConditionalUpdate{
				{
					Release: configv1.Release{
						Version: "4.21.3",
					},
					Risks: []configv1.ConditionalUpdateRisk{
						{
							Name: "ExistingRisk",
						},
					},
				},
			},
			risks: &mock.Mock{
				InternalName: "Mock",
				InternalRisks: []configv1.ConditionalUpdateRisk{{
					Name:          "TestAlert",
					Message:       "Test summary.",
					URL:           "https://example.com/testAlert",
					MatchingRules: []configv1.ClusterCondition{{Type: "Always"}},
				}},
			},
			expectedConditionalUpdates: []configv1.ConditionalUpdate{
				{
					Release: configv1.Release{
						Version: "4.21.1",
					},
					Risks: []configv1.ConditionalUpdateRisk{
						{
							Name:          "TestAlert",
							Message:       "Test summary.",
							URL:           "https://example.com/testAlert",
							MatchingRules: []configv1.ClusterCondition{{Type: "Always"}},
						},
					},
				},
				{
					Release: configv1.Release{
						Version: "4.21.2",
					},
					Risks: []configv1.ConditionalUpdateRisk{
						{
							Name:          "TestAlert",
							Message:       "Test summary.",
							URL:           "https://example.com/testAlert",
							MatchingRules: []configv1.ClusterCondition{{Type: "Always"}},
						},
					},
				},
				{
					Release: configv1.Release{
						Version: "4.21.3",
					},
					Risks: []configv1.ConditionalUpdateRisk{
						{
							Name: "ExistingRisk",
						},
						{
							Name:          "TestAlert",
							Message:       "Test summary.",
							URL:           "https://example.com/testAlert",
							MatchingRules: []configv1.ClusterCondition{{Type: "Always"}},
						},
					},
				},
			},
		},
		{
			name:    "invalid risk name",
			updates: []configv1.Release{{Version: "1.2.3"}},
			risks: &mock.Mock{
				InternalName: "Mock",
				InternalRisks: []configv1.ConditionalUpdateRisk{{
					Name:          "",
					Message:       "Test summary.",
					URL:           "https://example.com/testAlert",
					MatchingRules: []configv1.ClusterCondition{{Type: "Always"}},
				}},
			},
			expectedUpdates: []configv1.Release{{Version: "1.2.3"}},
			expectedError:   `invalid risk name "" does not match "^[A-Z][A-Za-z0-9]*$"`,
		},
		{
			name:    "single-source collision",
			updates: []configv1.Release{{Version: "1.2.3"}},
			risks: &mock.Mock{
				InternalName: "Mock",
				InternalRisks: []configv1.ConditionalUpdateRisk{
					{
						Name:          "RiskA",
						Message:       "Test summary 1.",
						URL:           "https://example.com/testAlert",
						MatchingRules: []configv1.ClusterCondition{{Type: "Always"}},
					},
					{
						Name:          "RiskA",
						Message:       "Test summary 2.",
						URL:           "https://example.com/testAlert",
						MatchingRules: []configv1.ClusterCondition{{Type: "Always"}},
					},
				},
			},
			expectedConditionalUpdates: []configv1.ConditionalUpdate{{
				Release: configv1.Release{
					Version: "1.2.3",
				},
				Risks: []configv1.ConditionalUpdateRisk{
					{
						Name:          "RiskA",
						Message:       "Test summary 1.",
						URL:           "https://example.com/testAlert",
						MatchingRules: []configv1.ClusterCondition{{Type: "Always"}},
					},
				},
			}},
			expectedError: `divergent definitions of risk "RiskA" from the merging source:
  v1.ConditionalUpdateRisk{
  	... // 1 ignored field
  	URL:           "https://example.com/testAlert",
  	Name:          "RiskA",
- 	Message:       "Test summary 1.",
+ 	Message:       "Test summary 2.",
  	MatchingRules: {{Type: "Always"}},
  }
`,
		},
		{
			name: "multi-source collision",
			conditionalUpdates: []configv1.ConditionalUpdate{{
				Release: configv1.Release{
					Version: "1.2.3",
				},
				Risks: []configv1.ConditionalUpdateRisk{
					{
						Name:          "RiskA",
						Message:       "Test summary 1.",
						URL:           "https://example.com/testAlert",
						MatchingRules: []configv1.ClusterCondition{{Type: "Always"}},
					},
				},
			}},
			risks: &mock.Mock{
				InternalName: "Mock",
				InternalRisks: []configv1.ConditionalUpdateRisk{
					{
						Name:          "RiskA",
						Message:       "Test summary 2.",
						URL:           "https://example.com/testAlert",
						MatchingRules: []configv1.ClusterCondition{{Type: "Always"}},
					},
				},
			},
			expectedConditionalUpdates: []configv1.ConditionalUpdate{{
				Release: configv1.Release{
					Version: "1.2.3",
				},
				Risks: []configv1.ConditionalUpdateRisk{
					{
						Name:          "RiskA",
						Message:       "Test summary 1.",
						URL:           "https://example.com/testAlert",
						MatchingRules: []configv1.ClusterCondition{{Type: "Always"}},
					},
				},
			}},
			expectedError: `divergent definitions of risk "RiskA":
  v1.ConditionalUpdateRisk{
  	... // 1 ignored field
  	URL:           "https://example.com/testAlert",
  	Name:          "RiskA",
- 	Message:       "Test summary 1.",
+ 	Message:       "Test summary 2.",
  	MatchingRules: {{Type: "Always"}},
  }
`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			allVersions := make([]string, 0, len(tt.updates)+len(tt.conditionalUpdates))
			for _, u := range tt.updates {
				allVersions = append(allVersions, u.Version)
			}
			for _, u := range tt.conditionalUpdates {
				allVersions = append(allVersions, u.Release.Version)
			}
			risks, versions, err := tt.risks.Risks(context.Background(), allVersions)
			if err != nil {
				t.Fatalf("failed to get risks from %s: %v", tt.risks.Name(), err)
			}
			updates, conditionalUpdates, err := risk.Merge(tt.updates, tt.conditionalUpdates, risks, versions)
			initialPassSuccess := true
			if diff := cmp.Diff(tt.expectedUpdates, updates); diff != "" {
				initialPassSuccess = false
				t.Errorf("available updates mismatch (-want +got):\n%s", diff)
			}
			if diff := cmp.Diff(tt.expectedConditionalUpdates, conditionalUpdates); diff != "" {
				initialPassSuccess = false
				t.Errorf("conditional updates mismatch (-want +got):\n%s", diff)
			}
			var actualError string
			if err != nil {
				actualError = strings.ReplaceAll(err.Error(), "\u00a0", " ")
			}
			if diff := cmp.Diff(tt.expectedError, actualError); diff != "" {
				initialPassSuccess = false
				t.Errorf("merge error mismatch (-want +got):\n%s", diff)
			}
			if !initialPassSuccess {
				return // we already have enough errors to work through; skip the idempotency test
			}
			updates, conditionalUpdates, err = risk.Merge(updates, conditionalUpdates, risks, versions)
			if diff := cmp.Diff(tt.expectedUpdates, updates); diff != "" {
				t.Errorf("idempotent available updates mismatch (-want +got):\n%s", diff)
			}
			if diff := cmp.Diff(tt.expectedConditionalUpdates, conditionalUpdates); diff != "" {
				t.Errorf("idempotent conditional updates mismatch (-want +got):\n%s", diff)
			}
			actualError = ""
			if err != nil {
				actualError = strings.ReplaceAll(err.Error(), "\u00a0", " ")
			}
			if diff := cmp.Diff(tt.expectedError, actualError); diff != "" {
				t.Errorf("idempotent merge error mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

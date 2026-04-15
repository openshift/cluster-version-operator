package aggregate_test

import (
	"context"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	configv1 "github.com/openshift/api/config/v1"

	"github.com/openshift/cluster-version-operator/pkg/risk"
	"github.com/openshift/cluster-version-operator/pkg/risk/aggregate"
	"github.com/openshift/cluster-version-operator/pkg/risk/mock"
)

func Test_aggregate(t *testing.T) {
	tests := []struct {
		name             string
		sources          []risk.Source
		versions         []string
		expectedName     string
		expectedRisks    []configv1.ConditionalUpdateRisk
		expectedVersions map[string][]string
		expectedError    string
	}{
		{
			name: "basic case",
			sources: []risk.Source{
				&mock.Mock{
					InternalName: "MockA",
					InternalRisks: []configv1.ConditionalUpdateRisk{{
						Name:          "TestAlert",
						Message:       "Test summary.",
						URL:           "https://example.com/testAlert",
						MatchingRules: []configv1.ClusterCondition{{Type: "Always"}},
					}},
				},
				&mock.Mock{
					InternalName: "MockB",
					InternalRisks: []configv1.ConditionalUpdateRisk{{
						Name:          "UpgradeableFalse",
						Message:       "Upgradeable summary.",
						URL:           "https://example.com/upgradeable",
						MatchingRules: []configv1.ClusterCondition{{Type: "Always"}},
					}},
					InternalVersions: map[string][]string{
						"UpgradeableFalse": {"2.3.4"},
					},
				},
			},
			versions:     []string{"1.2.3", "2.3.4"},
			expectedName: "Aggregate(MockA, MockB)",
			expectedRisks: []configv1.ConditionalUpdateRisk{
				{
					Name:          "TestAlert",
					Message:       "Test summary.",
					URL:           "https://example.com/testAlert",
					MatchingRules: []configv1.ClusterCondition{{Type: "Always"}},
				},
				{
					Name:          "UpgradeableFalse",
					Message:       "Upgradeable summary.",
					URL:           "https://example.com/upgradeable",
					MatchingRules: []configv1.ClusterCondition{{Type: "Always"}},
				},
			},
			expectedVersions: map[string][]string{
				"TestAlert":        {"1.2.3", "2.3.4"},
				"UpgradeableFalse": {"2.3.4"},
			},
		},
		{
			name: "matching duplicates",
			sources: []risk.Source{
				&mock.Mock{
					InternalName: "MockA",
					InternalRisks: []configv1.ConditionalUpdateRisk{{
						Name:          "TestAlert",
						Message:       "Test summary.",
						URL:           "https://example.com/testAlert",
						MatchingRules: []configv1.ClusterCondition{{Type: "Always"}},
					}},
				},
				&mock.Mock{
					InternalName: "MockB",
					InternalRisks: []configv1.ConditionalUpdateRisk{{
						Name:          "TestAlert",
						Message:       "Test summary.",
						URL:           "https://example.com/testAlert",
						MatchingRules: []configv1.ClusterCondition{{Type: "Always"}},
					}},
				},
			},
			versions:     []string{"1.2.3"},
			expectedName: "Aggregate(MockA, MockB)",
			expectedRisks: []configv1.ConditionalUpdateRisk{
				{
					Name:          "TestAlert",
					Message:       "Test summary.",
					URL:           "https://example.com/testAlert",
					MatchingRules: []configv1.ClusterCondition{{Type: "Always"}},
				},
			},
			expectedVersions: map[string][]string{
				"TestAlert": {"1.2.3"},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			source := aggregate.New(tt.sources...)
			name := source.Name()
			if name != tt.expectedName {
				t.Errorf("unexpected name %q diverges from expected %q", name, tt.expectedName)
			}
			risks, versions, err := source.Risks(context.Background(), tt.versions)
			if diff := cmp.Diff(tt.expectedRisks, risks); diff != "" {
				t.Errorf("risk mismatch (-want +got):\n%s", diff)
			}
			if diff := cmp.Diff(tt.expectedVersions, versions); diff != "" {
				t.Errorf("versions mismatch (-want +got):\n%s", diff)
			}
			var actualError string
			if err != nil {
				actualError = strings.ReplaceAll(err.Error(), "\u00a0", " ")
			}
			if diff := cmp.Diff(tt.expectedError, actualError); diff != "" {
				t.Errorf("error mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

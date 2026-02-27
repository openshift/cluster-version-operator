package clusterconditions_test

import (
	"context"
	"fmt"
	"reflect"
	"regexp"
	"testing"

	configv1 "github.com/openshift/api/config/v1"

	"github.com/openshift/cluster-version-operator/pkg/clusterconditions"
	"github.com/openshift/cluster-version-operator/pkg/clusterconditions/standard"
)

// Error implements a cluster condition that always errors.
type Error struct {
	count int
}

// Valid always returns 'nil', because we are not using this type to
// exercise validation.
func (e *Error) Valid(ctx context.Context, condition *configv1.ClusterCondition) error {
	return nil
}

// Match always returns an error.
func (e *Error) Match(ctx context.Context, condition *configv1.ClusterCondition) (bool, error) {
	e.count += 1
	return false, fmt.Errorf("test error %d", e.count)
}

func TestPruneInvalid(t *testing.T) {
	ctx := context.Background()
	registry := standard.NewConditionRegistry(clusterconditions.DefaultPromQLTarget())

	for _, testCase := range []struct {
		name          string
		conditions    []configv1.ClusterCondition
		expectedValid []configv1.ClusterCondition
		expectedError *regexp.Regexp
	}{
		{
			name: "no conditions",
		},
		{
			name: "valid conditions",
			conditions: []configv1.ClusterCondition{
				{
					Type: "Always",
				},
				{
					Type: "PromQL",
					PromQL: &configv1.PromQLClusterCondition{
						PromQL: "max(cluster_proxy_enabled{type=~\"https?\"})",
					},
				},
			},
			expectedValid: []configv1.ClusterCondition{
				{
					Type: "Always",
				},
				{
					Type: "PromQL",
					PromQL: &configv1.PromQLClusterCondition{
						PromQL: "max(cluster_proxy_enabled{type=~\"https?\"})",
					},
				},
			},
		},
		{
			name: "some invalid conditions",
			conditions: []configv1.ClusterCondition{
				{
					Type: "Always",
				},
				{
					Type: "PromQL",
				},
			},
			expectedValid: []configv1.ClusterCondition{
				{
					Type: "Always",
				},
			},
			expectedError: regexp.MustCompile("^the 'promql' property is required for 'type: PromQL' conditions$"),
		},
		{
			name: "all invalid",
			conditions: []configv1.ClusterCondition{
				{
					Type:   "Always",
					PromQL: &configv1.PromQLClusterCondition{},
				},
				{
					Type: "PromQL",
				},
			},
			expectedError: regexp.MustCompile("^[[]the 'promql' property is not valid for 'type: Always' conditions, the 'promql' property is required for 'type: PromQL' conditions]$"),
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			valid, err := registry.PruneInvalid(ctx, testCase.conditions)
			if !reflect.DeepEqual(valid, testCase.expectedValid) {
				t.Errorf("got valid %v but expected %v", valid, testCase.expectedValid)
			}
			if err != nil && testCase.expectedError == nil {
				t.Errorf("unexpected error: %v", err)
			} else if testCase.expectedError != nil && err == nil {
				t.Errorf("unexpected success, expected: %s", testCase.expectedError)
			} else if testCase.expectedError != nil && !testCase.expectedError.MatchString(err.Error()) {
				t.Errorf("expected error %s, not: %v", testCase.expectedError, err)
			}
		})
	}
}

func TestMatch(t *testing.T) {
	ctx := context.Background()
	registry := standard.NewConditionRegistry(clusterconditions.DefaultPromQLTarget())
	registry.Register("Error", &Error{})

	for _, testCase := range []struct {
		name          string
		conditions    []configv1.ClusterCondition
		expectedMatch bool
		expectedError *regexp.Regexp
	}{
		{
			name:          "no conditions",
			expectedMatch: false,
		},
		{
			name: "valid condition before unrecognized condition",
			conditions: []configv1.ClusterCondition{
				{
					Type: "Always",
				},
				{
					Type: "does-not-exist",
				},
			},
			expectedMatch: true,
		},
		{
			name: "valid condition after unrecognized condition",
			conditions: []configv1.ClusterCondition{
				{
					Type: "does-not-exist",
				},
				{
					Type: "Always",
				},
			},
			expectedMatch: true,
		},
		{
			name: "all unrecognized",
			conditions: []configv1.ClusterCondition{
				{
					Type: "does-not-exist-1",
				},
				{
					Type: "does-not-exist-2",
				},
			},
		},
		{
			name: "unrecognized and two errors",
			conditions: []configv1.ClusterCondition{
				{
					Type: "does-not-exist",
				},
				{
					Type: "Error",
				},
				{
					Type: "Error",
				},
			},
			expectedError: regexp.MustCompile("^[[]test error 1, test error 2]"),
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			match, err := registry.Match(ctx, testCase.conditions)
			if match != testCase.expectedMatch {
				t.Errorf("got match %t but expected %t", match, testCase.expectedMatch)
			}
			if err != nil && testCase.expectedError == nil {
				t.Errorf("unexpected error: %v", err)
			} else if testCase.expectedError != nil && err == nil {
				t.Errorf("unexpected success, expected: %s", testCase.expectedError)
			} else if testCase.expectedError != nil && !testCase.expectedError.MatchString(err.Error()) {
				t.Errorf("expected error %s, not: %v", testCase.expectedError, err)
			}
		})
	}
}

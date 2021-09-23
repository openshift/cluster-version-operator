// Package promql
//
// https://github.com/openshift/enhancements/blob/master/enhancements/update/targeted-update-edge-blocking.md#promql
package promql

import (
	"context"
	"errors"

	configv1 "github.com/openshift/api/config/v1"
	"github.com/openshift/cluster-version-operator/pkg/clusterconditions"
)

// PromQL implements a cluster condition that matches based on PromQL.
type PromQL struct{}

var promql = &PromQL{}

// Valid returns an error if the condition contains any properties
// besides 'type' and a valid `promql`.
func (p *PromQL) Valid(ctx context.Context, condition *configv1.ClusterCondition) error {
	if condition.PromQL == nil {
		return errors.New("the 'promql' property is required for 'type: PromQL' conditions")
	}

	if condition.PromQL.PromQL == "" {
		return errors.New("the 'promql.promql' query string must be non-empty for 'type: PromQL' conditions")
	}

	return nil
}

// Match returns true when the condition's PromQL evaluates to 1,
// false when the PromQL evaluates to 0, and an error if the PromQL
// returns no time series or returns a value besides 0 or 1.
func (p *PromQL) Match(ctx context.Context, condition *configv1.ClusterCondition) (bool, error) {
	return false, errors.New("not yet implemented: PromQL matching")
}

func init() {
	clusterconditions.Register("PromQL", promql)
}

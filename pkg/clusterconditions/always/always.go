// Package always implements a cluster condition that always matches.
//
// https://github.com/openshift/enhancements/blob/master/enhancements/update/targeted-update-edge-blocking.md#always
package always

import (
	"context"
	"errors"

	configv1 "github.com/openshift/api/config/v1"
)

// Always implements a cluster condition that always matches.
type Always struct{}

// Valid returns an error if the condition contains any properties
// besides 'type'.
func (a *Always) Valid(ctx context.Context, condition *configv1.ClusterCondition) error {
	if condition.PromQL != nil {
		return errors.New("the 'promql' property is not valid for 'type: Always' conditions")
	}

	return nil
}

// Match always returns true.
func (a *Always) Match(ctx context.Context, condition *configv1.ClusterCondition) (bool, error) {
	return true, nil
}

// Package promql implements a cluster condition based on PromQL queries.
//
// https://github.com/openshift/enhancements/blob/master/enhancements/update/targeted-update-edge-blocking.md#promql
package promql

import (
	"context"
	"errors"
	"fmt"
	"time"

	configv1 "github.com/openshift/api/config/v1"
	"github.com/prometheus/client_golang/api"
	prometheusv1 "github.com/prometheus/client_golang/api/prometheus/v1"
	"github.com/prometheus/common/config"
	"github.com/prometheus/common/model"
	"k8s.io/klog/v2"

	"github.com/openshift/cluster-version-operator/pkg/clusterconditions"
)

// PromQL implements a cluster condition that matches based on PromQL.
type PromQL struct {
	// Address holds the Prometheus query URI.
	Address string

	// HTTPClientConfig holds the client configuration for connecting to the Prometheus service.
	HTTPClientConfig config.HTTPClientConfig
}

var promql = &PromQL{
	Address: "https://thanos-querier.openshift-monitoring.svc.cluster.local:9091",
	HTTPClientConfig: config.HTTPClientConfig{
		Authorization: &config.Authorization{
			Type:            "Bearer",
			CredentialsFile: "/var/run/secrets/kubernetes.io/serviceaccount/token",
		},
		TLSConfig: config.TLSConfig{
			CAFile: "/etc/tls/service-ca/service-ca.crt",
		},
	},
}

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
	clientConfig := api.Config{Address: p.Address}

	if roundTripper, err := config.NewRoundTripperFromConfig(p.HTTPClientConfig, "cluster-conditions"); err == nil {
		clientConfig.RoundTripper = roundTripper
	} else {
		return false, fmt.Errorf("creating PromQL round-tripper: %w", err)
	}

	client, err := api.NewClient(clientConfig)
	if err != nil {
		return false, fmt.Errorf("creating PromQL client: %w", err)
	}

	v1api := prometheusv1.NewAPI(client)
	klog.V(4).Infof("evaluate %s cluster condition: %q", condition.Type, condition.PromQL.PromQL)
	result, warnings, err := v1api.Query(ctx, condition.PromQL.PromQL, time.Now())
	if err != nil {
		return false, fmt.Errorf("executing PromQL query: %w", err)
	}

	for _, warning := range warnings {
		klog.Warning(warning)
	}

	if result.Type() != model.ValVector {
		return false, fmt.Errorf("invalid PromQL result type is %s, not vector", result.Type())
	}

	vector, ok := result.(model.Vector)
	if !ok {
		return false, fmt.Errorf("invalid PromQL result type is nominally %s, but fails Vector cast", result.Type())
	}

	if vector.Len() != 1 {
		return false, fmt.Errorf("invalid PromQL result length must be one, but is %d", vector.Len())
	}

	sample := vector[0]
	if sample.Value == 0 {
		return false, nil
	} else if sample.Value == 1 {
		return true, nil
	}
	return false, fmt.Errorf("invalid PromQL result (must be 0 or 1): %v", sample.Value)
}

func init() {
	clusterconditions.Register("PromQL", promql)
}

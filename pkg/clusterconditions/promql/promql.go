// Package promql implements a cluster condition based on PromQL queries.
//
// https://github.com/openshift/enhancements/blob/master/enhancements/update/targeted-update-edge-blocking.md#promql
package promql

import (
	"context"
	"errors"
	"fmt"
	"net"
	"time"

	configv1 "github.com/openshift/api/config/v1"
	"github.com/prometheus/client_golang/api"
	prometheusv1 "github.com/prometheus/client_golang/api/prometheus/v1"
	"github.com/prometheus/common/config"
	"github.com/prometheus/common/model"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/klog/v2"

	"github.com/openshift/cluster-version-operator/pkg/clusterconditions/cache"
)

// PromQL implements a cluster condition that matches based on PromQL.
type PromQL struct {
	kubeClient kubernetes.Interface

	// HTTPClientConfig holds the client configuration for connecting to the Prometheus service.
	HTTPClientConfig config.HTTPClientConfig

	// QueryTimeout limits the amount of time we wait before giving up on the Prometheus query.
	QueryTimeout time.Duration
}

func NewPromQL(kubeClient kubernetes.Interface) *cache.Cache {
	return &cache.Cache{
		Condition: &PromQL{
			kubeClient: kubeClient,
			HTTPClientConfig: config.HTTPClientConfig{
				Authorization: &config.Authorization{
					Type:            "Bearer",
					CredentialsFile: "/var/run/secrets/kubernetes.io/serviceaccount/token",
				},
				TLSConfig: config.TLSConfig{
					CAFile: "/etc/tls/service-ca/service-ca.crt",
					// ServerName is used to verify the name of the service we will connect to using IP.
					ServerName: "thanos-querier.openshift-monitoring.svc.cluster.local",
				},
			},
			QueryTimeout: 5 * time.Minute,
		},
		MinBetweenMatches: 10 * time.Minute,
		MinForCondition:   time.Hour,
		Expiration:        24 * time.Hour,
	}
}

// Address determines the address of the thanos-querier to avoid requiring service DNS resolution.
// We do this so that our host-network pod can use the node's resolv.conf to resolve the internal load balancer name
// on the pod before DNS pods are available and before the service network is available.  The side effect is that
// the CVO cannot resolve service DNS names.
func (p *PromQL) Address(ctx context.Context) (string, error) {
	svc, err := p.kubeClient.CoreV1().Services("openshift-monitoring").Get(ctx, "thanos-querier", metav1.GetOptions{})
	if err != nil {
		return "", err
	}

	return fmt.Sprintf("https://%s", net.JoinHostPort(svc.Spec.ClusterIP, "9091")), nil
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
	// Lookup the address every attempt in case the service IP changes.  This can happen when the thanos service is
	// deleted and recreated.
	address, err := p.Address(ctx)
	if err != nil {
		return false, fmt.Errorf("failure determine thanos IP: %w", err)
	}
	clientConfig := api.Config{Address: address}

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

	queryContext := ctx
	if p.QueryTimeout > 0 {
		var cancel context.CancelFunc
		queryContext, cancel = context.WithTimeout(ctx, p.QueryTimeout)
		defer cancel()
	}

	klog.V(2).Infof("evaluate %s cluster condition: %q", condition.Type, condition.PromQL.PromQL)
	result, warnings, err := v1api.Query(queryContext, condition.PromQL.PromQL, time.Now())
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

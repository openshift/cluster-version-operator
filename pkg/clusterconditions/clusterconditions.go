// Package clusterconditions implements cluster conditions for
// identifying matching clusters.
//
// https://github.com/openshift/enhancements/blob/master/enhancements/update/targeted-update-edge-blocking.md#cluster-condition-type-registry
package clusterconditions

import (
	"context"
	"fmt"
	"net/url"

	configv1 "github.com/openshift/api/config/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/client-go/kubernetes"
	"k8s.io/klog/v2"
)

type Condition interface {
	// Valid returns an error if the condition is expected to be
	// rejected by the Kubernetes API server.  For example, for
	// missing or invalid data.
	Valid(ctx context.Context, condition *configv1.ClusterCondition) error

	// Match returns whether the condition matches the current
	// cluster (true), does not match the current cluster (false),
	// or fails to evaluate (error).
	Match(ctx context.Context, condition *configv1.ClusterCondition) (bool, error)
}

type ConditionRegistry interface {
	// Register registers a condition type, and panics on any name collisions.
	Register(conditionType string, condition Condition)

	// PruneInvalid returns a new slice with recognized, valid conditions.
	// The error complains about any unrecognized or invalid conditions.
	PruneInvalid(ctx context.Context, matchingRules []configv1.ClusterCondition) ([]configv1.ClusterCondition, error)

	// Match returns whether the cluster matches the given rules (true),
	// does not match (false), or the rules fail to evaluate (error).
	Match(ctx context.Context, matchingRules []configv1.ClusterCondition) (bool, error)
}

type conditionRegistry struct {
	// registry is a registry of implemented condition types.
	registry map[string]Condition
}

func NewConditionRegistry() ConditionRegistry {
	ret := &conditionRegistry{
		registry: map[string]Condition{},
	}

	return ret
}

// Register registers a condition type, and panics on any name collisions.
func (r *conditionRegistry) Register(conditionType string, condition Condition) {
	if r.registry == nil {
		r.registry = make(map[string]Condition, 1)
	}
	if existing, ok := r.registry[conditionType]; ok && condition != existing {
		panic(fmt.Sprintf("cluster condition %q already registered", conditionType))
	}
	r.registry[conditionType] = condition
}

// PruneInvalid returns a new slice with recognized, valid conditions.
// The error complains about any unrecognized or invalid conditions.
func (r *conditionRegistry) PruneInvalid(ctx context.Context, matchingRules []configv1.ClusterCondition) ([]configv1.ClusterCondition, error) {
	var valid []configv1.ClusterCondition
	var errs []error

	for _, config := range matchingRules {
		condition, ok := r.registry[config.Type]
		if !ok {
			errs = append(errs, fmt.Errorf("Skipping unrecognized cluster condition type %q", config.Type))
			continue
		}
		if err := condition.Valid(ctx, &config); err != nil {
			errs = append(errs, err)
			continue
		}
		valid = append(valid, config)
	}

	return valid, errors.NewAggregate(errs)
}

// Match returns whether the cluster matches the given rules (true),
// does not match (false), or the rules fail to evaluate (error).
func (r *conditionRegistry) Match(ctx context.Context, matchingRules []configv1.ClusterCondition) (bool, error) {
	var errs []error

	for _, config := range matchingRules {
		condition, ok := r.registry[config.Type]
		if !ok {
			klog.V(2).Infof("Skipping unrecognized cluster condition type %q", config.Type)
			continue
		}
		match, err := condition.Match(ctx, &config)
		if err == nil {
			return match, nil
		}
		errs = append(errs, err)
	}

	return false, errors.NewAggregate(errs)
}

type PromQLTarget struct {
	KubeClient      kubernetes.Interface
	UseDNS          bool
	URL             *url.URL
	CABundleFile    string
	BearerTokenFile string

	// The Kubernetes service to be used to resolve the IP address when DNS is not used.
	KubeSvc types.NamespacedName
}

func DefaultPromQLTarget() PromQLTarget {
	promqlURL, _ := url.Parse("https://thanos-querier.openshift-monitoring.svc.cluster.local:9091")
	return PromQLTarget{
		KubeClient: nil,
		UseDNS:     false,
		URL:        promqlURL,
		KubeSvc: types.NamespacedName{
			Name:      "thanos-querier",
			Namespace: "openshift-monitoring",
		},
		CABundleFile:    "/etc/tls/service-ca/service-ca.crt",
		BearerTokenFile: "/var/run/secrets/kubernetes.io/serviceaccount/token",
	}
}

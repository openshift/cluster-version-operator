package promql

import (
	"context"
	"fmt"
	"sync"

	"github.com/prometheus/client_golang/api"
	prometheusv1 "github.com/prometheus/client_golang/api/prometheus/v1"
	"github.com/prometheus/common/config"

	"k8s.io/klog/v2"

	"github.com/openshift/cluster-version-operator/pkg/clusterconditions"
)

type Getter interface {
	Get(ctx context.Context) prometheusv1.AlertsResult
}

func NewAlertGetter(promQLTarget clusterconditions.PromQLTarget) Getter {
	p := NewPromQL(promQLTarget)
	condition := p.Condition
	v, ok := condition.(*PromQL)
	if !ok {
		panic("invalid condition type")
	}
	return &ocAlertGetter{promQL: v}
}

type ocAlertGetter struct {
	promQL *PromQL

	mutex  sync.Mutex
	cached prometheusv1.AlertsResult
}

func (o *ocAlertGetter) Get(ctx context.Context) prometheusv1.AlertsResult {
	if err := o.refresh(ctx); err != nil {
		klog.Errorf("Failed to refresh alerts, using stale cache instead: %v", err)
	}
	return o.cached
}

func (o *ocAlertGetter) refresh(ctx context.Context) error {
	o.mutex.Lock()
	defer o.mutex.Unlock()

	klog.Info("refresh alerts ...")
	p := o.promQL
	host, err := p.Host(ctx)
	if err != nil {
		return fmt.Errorf("failure determine thanos IP: %w", err)
	}
	p.url.Host = host
	clientConfig := api.Config{Address: p.url.String()}

	if roundTripper, err := config.NewRoundTripperFromConfig(p.HTTPClientConfig, "cluster-conditions"); err == nil {
		clientConfig.RoundTripper = roundTripper
	} else {
		return fmt.Errorf("creating PromQL round-tripper: %w", err)
	}

	promqlClient, err := api.NewClient(clientConfig)
	if err != nil {
		return fmt.Errorf("creating PromQL client: %w", err)
	}

	client := &statusCodeNotImplementedForPostClient{
		client: promqlClient,
	}

	v1api := prometheusv1.NewAPI(client)

	queryContext := ctx
	if p.QueryTimeout > 0 {
		var cancel context.CancelFunc
		queryContext, cancel = context.WithTimeout(ctx, p.QueryTimeout)
		defer cancel()
	}

	r, err := v1api.Alerts(queryContext)
	if err != nil {
		return fmt.Errorf("failed to get alerts: %w", err)
	}
	o.cached = r
	klog.Infof("refreshed: %d alerts", len(o.cached.Alerts))
	return nil
}

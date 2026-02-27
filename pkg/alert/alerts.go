package alert

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/rest"

	routev1 "github.com/openshift/api/route/v1"
	routev1client "github.com/openshift/client-go/route/clientset/versioned/typed/route/v1"
)

type DataAndStatus struct {
	Status string `json:"status"`
	Data   Data   `json:"data"`
}

type Data struct {
	Alerts []Alert `json:"alerts"`
}

type Alert struct {
	Labels                  AlertLabels      `json:"labels,omitempty"`
	Annotations             AlertAnnotations `json:"annotations,omitempty"`
	State                   string           `json:"state,omitempty"`
	Value                   string           `json:"value,omitempty"`
	ActiveAt                time.Time        `json:"activeAt,omitempty"`
	PartialResponseStrategy string           `json:"partialResponseStrategy,omitempty"`
}

type AlertLabels struct {
	AlertName           string `json:"alertname,omitempty"`
	Name                string `json:"name,omitempty"`
	Namespace           string `json:"namespace,omitempty"`
	PodDisruptionBudget string `json:"poddisruptionbudget,omitempty"`
	Reason              string `json:"reason,omitempty"`
	Severity            string `json:"severity,omitempty"`
}

type AlertAnnotations struct {
	Description string `json:"description,omitempty"`
	Summary     string `json:"summary,omitempty"`
	Runbook     string `json:"runbook_url,omitempty"`
	Message     string `json:"message,omitempty"`
}

type Getter interface {
	Get(ctx context.Context) (*DataAndStatus, error)
}

func NewAlertGetterOrDie(c *rest.Config) Getter {
	client := routev1client.NewForConfigOrDie(c)
	return &ocAlertGetter{config: c, routeClient: client}
}

type ocAlertGetter struct {
	config      *rest.Config
	routeClient *routev1client.RouteV1Client
}

func (o *ocAlertGetter) Get(ctx context.Context) (*DataAndStatus, error) {
	roundTripper, err := rest.TransportFor(o.config)
	if err != nil {
		return nil, fmt.Errorf("failed to create roundtripper: %w", err)
	}

	routeGetter := func(ctx context.Context, namespace string, name string, opts metav1.GetOptions) (*routev1.Route, error) {
		return o.routeClient.Routes(namespace).Get(ctx, name, opts)
	}
	alertsBytes, err := GetAlerts(ctx, roundTripper, routeGetter)
	if err != nil {
		return nil, fmt.Errorf("failed to get alerts: %w", err)
	}
	var ret DataAndStatus
	if err := json.Unmarshal(alertsBytes, &ret); err != nil {
		return nil, fmt.Errorf("parsing alerts: %w", err)
	}
	return &ret, nil
}

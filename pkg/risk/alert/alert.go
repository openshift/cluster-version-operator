package alert

import (
	"context"
	"fmt"
	"sort"
	"strings"

	prometheusv1 "github.com/prometheus/client_golang/api/prometheus/v1"

	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/klog/v2"

	configv1 "github.com/openshift/api/config/v1"

	"github.com/openshift/cluster-version-operator/pkg/clusterconditions"
	"github.com/openshift/cluster-version-operator/pkg/clusterconditions/promql"
	"github.com/openshift/cluster-version-operator/pkg/internal"
	"github.com/openshift/cluster-version-operator/pkg/risk"
)

func New(name string, promQLTarget clusterconditions.PromQLTarget) risk.Source {
	return &alertRisk{
		name:   name,
		getter: promql.NewAlertGetter(promQLTarget),
	}
}

type alertRisk struct {
	name   string
	getter promql.Getter
}

func (a *alertRisk) Name() string {
	return a.name
}

func (a *alertRisk) Risks(ctx context.Context, versions []string) ([]configv1.ConditionalUpdateRisk, map[string][]string, error) {
	alerts := a.getter.Get(ctx)
	risks := alertsToRisks(alerts.Alerts)
	versionMap := make(map[string][]string, len(risks))
	for _, risk := range risks {
		versionMap[risk.Name] = versions
	}
	return risks, versionMap, nil
}

func alertsToRisks(alerts []prometheusv1.Alert) []configv1.ConditionalUpdateRisk {
	klog.V(2).Infof("Found %d alerts", len(alerts))
	risks := map[string]configv1.ConditionalUpdateRisk{}
	for _, alert := range alerts {
		var alertName string
		if alertName = string(alert.Labels["alertname"]); alertName == "" {
			continue
		}
		if alert.State == "pending" {
			continue
		}

		var summary string
		if summary = string(alert.Annotations["summary"]); summary == "" {
			summary = alertName
		}
		if !strings.HasSuffix(summary, ".") {
			summary += "."
		}

		var description string
		alertMessage := string(alert.Annotations["message"])
		alertDescription := string(alert.Annotations["description"])
		switch {
		case alertMessage != "" && alertDescription != "":
			description += " The alert description is: " + alertDescription + " | " + alertMessage
		case alertDescription != "":
			description += " The alert description is: " + alertDescription
		case alertMessage != "":
			description += " The alert description is: " + alertMessage
		default:
			description += " The alert has no description."
		}

		var runbook string
		alertURL := "https://github.com/openshift/runbooks/tree/master/alerts?runbook=notfound"
		if runbook = string(alert.Annotations["runbook_url"]); runbook == "" {
			runbook = "<alert does not have a runbook_url annotation>"
		} else {
			alertURL = runbook
		}

		details := fmt.Sprintf("%s%s %s", summary, description, runbook)

		severity := string(alert.Labels["severity"])
		if severity == "critical" {
			if alertName == internal.AlertNamePodDisruptionBudgetLimit {
				details = fmt.Sprintf("Namespace=%s, PodDisruptionBudget=%s. %s", alert.Labels["namespace"], alert.Labels["poddisruptionbudget"], details)
			}
			risks[alertName] = getRisk(risks, alertName, summary, alertURL, metav1.Condition{
				Type:               internal.ConditionalUpdateRiskConditionTypeApplies,
				Status:             metav1.ConditionTrue,
				Reason:             fmt.Sprintf("Alert:%s", alert.State),
				Message:            internal.AlertConditionMessage(alertName, severity, string(alert.State), "suggesting significant cluster issues worth investigating", details),
				LastTransitionTime: metav1.NewTime(alert.ActiveAt),
			})
			continue
		}

		if alertName == internal.AlertNamePodDisruptionBudgetAtLimit {
			details = fmt.Sprintf("Namespace=%s, PodDisruptionBudget=%s. %s", alert.Labels["namespace"], alert.Labels["poddisruptionbudget"], details)
			risks[alertName] = getRisk(risks, alertName, summary, alertURL, metav1.Condition{
				Type:               internal.ConditionalUpdateRiskConditionTypeApplies,
				Status:             metav1.ConditionTrue,
				Reason:             internal.AlertConditionReason(string(alert.State)),
				Message:            internal.AlertConditionMessage(alertName, severity, string(alert.State), "which might slow node drains", details),
				LastTransitionTime: metav1.NewTime(alert.ActiveAt),
			})
			continue
		}

		if internal.HavePullWaiting.Has(alertName) ||
			internal.HaveNodes.Has(alertName) ||
			alertName == internal.AlertNameVirtHandlerDaemonSetRolloutFailing ||
			alertName == internal.AlertNameVMCannotBeEvicted {
			risks[alertName] = getRisk(risks, alertName, summary, alertURL, metav1.Condition{
				Type:               internal.ConditionalUpdateRiskConditionTypeApplies,
				Status:             metav1.ConditionTrue,
				Reason:             internal.AlertConditionReason(string(alert.State)),
				Message:            internal.AlertConditionMessage(alertName, severity, string(alert.State), "which may slow workload redistribution during rolling node updates", details),
				LastTransitionTime: metav1.NewTime(alert.ActiveAt),
			})
			continue
		}

		updatePrecheck := string(alert.Labels["openShiftUpdatePrecheck"])
		if updatePrecheck == "true" {
			risks[alertName] = getRisk(risks, alertName, summary, alertURL, metav1.Condition{
				Type:               internal.ConditionalUpdateRiskConditionTypeApplies,
				Status:             metav1.ConditionTrue,
				Reason:             fmt.Sprintf("Alert:%s", alert.State),
				Message:            internal.AlertConditionMessage(alertName, severity, string(alert.State), "suggesting issues worth investigating before updating the cluster", details),
				LastTransitionTime: metav1.NewTime(alert.ActiveAt),
			})
			continue
		}
	}

	klog.V(2).Infof("Got %d risks", len(risks))
	if len(risks) == 0 {
		return nil
	}

	var ret []configv1.ConditionalUpdateRisk
	var keys []string
	for k := range risks {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		ret = append(ret, risks[k])
	}
	return ret
}

func getRisk(risks map[string]configv1.ConditionalUpdateRisk, riskName, message, url string, condition metav1.Condition) configv1.ConditionalUpdateRisk {
	risk, ok := risks[riskName]
	if !ok {
		return configv1.ConditionalUpdateRisk{
			Name:    riskName,
			Message: message,
			URL:     url,
			// Always as the alert is firing
			MatchingRules: []configv1.ClusterCondition{{Type: "Always"}},
			Conditions:    []metav1.Condition{condition},
		}
	}

	if c := meta.FindStatusCondition(risk.Conditions, condition.Type); c != nil {
		c.Message = fmt.Sprintf("%s; %s", c.Message, condition.Message)
		if c.LastTransitionTime.After(condition.LastTransitionTime.Time) {
			c.LastTransitionTime = condition.LastTransitionTime
		}
		meta.SetStatusCondition(&risk.Conditions, *c)
	}

	return risk
}

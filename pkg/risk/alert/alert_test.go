package alert

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	prometheusv1 "github.com/prometheus/client_golang/api/prometheus/v1"
	"github.com/prometheus/common/model"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	configv1 "github.com/openshift/api/config/v1"

	"github.com/openshift/cluster-version-operator/pkg/clusterconditions/promql"
)

type mockAlertGetter struct {
	ret      prometheusv1.AlertsResult
	jsonFile string
	t        *testing.T
}

func (m *mockAlertGetter) Get(_ context.Context) prometheusv1.AlertsResult {
	var ret prometheusv1.AlertsResult
	if m.jsonFile != "" {
		data, err := os.ReadFile(m.jsonFile)
		if err != nil {
			m.t.Fatal(err)
		}
		err = json.Unmarshal(data, &ret)
		if err != nil {
			m.t.Fatal(err)
		}
		return ret
	}
	return m.ret
}

func Test_alertRisk(t *testing.T) {
	t1 := time.Now()
	t2 := time.Now().Add(-3 * time.Minute)
	t3, err := time.Parse(time.RFC3339, "2026-03-04T00:38:19.02109776Z")
	if err != nil {
		t.Fatalf("failed to parse time: %v", err)
	}
	tests := []struct {
		name               string
		getter             promql.Getter
		expectedErr        error
		expectedAlertRisks []configv1.ConditionalUpdateRisk
	}{
		{
			name: "basic case",
			getter: &mockAlertGetter{
				t: t,
				ret: prometheusv1.AlertsResult{
					Alerts: []prometheusv1.Alert{
						{
							Labels: map[model.LabelName]model.LabelValue{
								model.LabelName("alertname"):           model.LabelValue("PodDisruptionBudgetLimit"),
								model.LabelName("severity"):            model.LabelValue("critical"),
								model.LabelName("namespace"):           model.LabelValue("namespace"),
								model.LabelName("poddisruptionbudget"): model.LabelValue("some-pdb"),
							},
							State: prometheusv1.AlertStateFiring,
							Annotations: map[model.LabelName]model.LabelValue{
								model.LabelName("summary"):     model.LabelValue("summary"),
								model.LabelName("description"): model.LabelValue("description"),
								model.LabelName("message"):     model.LabelValue("message"),
								model.LabelName("runbook_url"): model.LabelValue("http://runbook.example.com/runbooks/abc.md"),
							},
							ActiveAt: t1,
						},
						{
							Labels: map[model.LabelName]model.LabelValue{
								model.LabelName("alertname"): model.LabelValue("not-important"),
							},
							State: prometheusv1.AlertStatePending,
						},
						{
							Labels: map[model.LabelName]model.LabelValue{
								model.LabelName("alertname"):           model.LabelValue("PodDisruptionBudgetAtLimit"),
								model.LabelName("severity"):            model.LabelValue("severity"),
								model.LabelName("namespace"):           model.LabelValue("namespace"),
								model.LabelName("poddisruptionbudget"): model.LabelValue("some-pdb"),
							},
							State: prometheusv1.AlertStateFiring,
							Annotations: map[model.LabelName]model.LabelValue{
								model.LabelName("summary"):     model.LabelValue("summary"),
								model.LabelName("description"): model.LabelValue("description"),
								model.LabelName("message"):     model.LabelValue("message"),
								model.LabelName("runbook_url"): model.LabelValue("http://runbook.example.com/runbooks/bbb.md"),
							},
							ActiveAt: t1,
						},
						{
							Labels: map[model.LabelName]model.LabelValue{
								model.LabelName("alertname"):           model.LabelValue("PodDisruptionBudgetAtLimit"),
								model.LabelName("severity"):            model.LabelValue("severity"),
								model.LabelName("namespace"):           model.LabelValue("namespace"),
								model.LabelName("poddisruptionbudget"): model.LabelValue("another-pdb"),
							},
							State: prometheusv1.AlertStateFiring,
							Annotations: map[model.LabelName]model.LabelValue{
								model.LabelName("summary"):     model.LabelValue("summary"),
								model.LabelName("description"): model.LabelValue("description"),
								model.LabelName("message"):     model.LabelValue("message"),
								model.LabelName("runbook_url"): model.LabelValue("http://runbook.example.com/runbooks/bbb.md"),
							},
							ActiveAt: t2,
						},
					},
				},
			},
			expectedAlertRisks: []configv1.ConditionalUpdateRisk{
				{
					Name:          "PodDisruptionBudgetAtLimit",
					Message:       "summary.",
					URL:           "http://runbook.example.com/runbooks/bbb.md",
					MatchingRules: []configv1.ClusterCondition{{Type: "Always"}},
					Conditions: []metav1.Condition{{
						Type:               "Applies",
						Status:             "True",
						Reason:             "Alert:firing",
						Message:            "severity alert PodDisruptionBudgetAtLimit firing, which might slow node drains. Namespace=namespace, PodDisruptionBudget=some-pdb. summary. The alert description is: description | message http://runbook.example.com/runbooks/bbb.md; severity alert PodDisruptionBudgetAtLimit firing, which might slow node drains. Namespace=namespace, PodDisruptionBudget=another-pdb. summary. The alert description is: description | message http://runbook.example.com/runbooks/bbb.md",
						LastTransitionTime: metav1.NewTime(t2),
					}},
				},
				{
					Name:          "PodDisruptionBudgetLimit",
					Message:       "summary.",
					URL:           "http://runbook.example.com/runbooks/abc.md",
					MatchingRules: []configv1.ClusterCondition{{Type: "Always"}},
					Conditions: []metav1.Condition{{
						Type:               "Applies",
						Status:             "True",
						Reason:             "Alert:firing",
						Message:            "critical alert PodDisruptionBudgetLimit firing, suggesting significant cluster issues worth investigating. Namespace=namespace, PodDisruptionBudget=some-pdb. summary. The alert description is: description | message http://runbook.example.com/runbooks/abc.md",
						LastTransitionTime: metav1.NewTime(t1),
					}},
				},
			},
		},
		{
			name: "from file",
			getter: &mockAlertGetter{
				t:        t,
				jsonFile: filepath.Join("testdata", "alerts.json"),
			},
			expectedAlertRisks: []configv1.ConditionalUpdateRisk{
				{
					Name:          "TestAlert",
					Message:       "Test summary.",
					URL:           "https://github.com/openshift/runbooks/tree/master/alerts?runbook=notfound",
					MatchingRules: []configv1.ClusterCondition{{Type: "Always"}},
					Conditions: []metav1.Condition{{
						Type:               "Applies",
						Status:             "True",
						Reason:             "Alert:firing",
						Message:            "critical alert TestAlert firing, suggesting significant cluster issues worth investigating. Test summary. The alert description is: Test description. <alert does not have a runbook_url annotation>",
						LastTransitionTime: metav1.NewTime(t3),
					}},
				},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			source := &alertRisk{
				name:   "Alert",
				getter: tt.getter,
			}
			allVersions := []string{"1.2.3"}
			risks, versions, err := source.Risks(context.Background(), allVersions)

			if diff := cmp.Diff(tt.expectedAlertRisks, risks); diff != "" {
				t.Errorf("Alert Risks mismatch (-want +got):\n%s", diff)
			}

			expectedVersions := make(map[string][]string, len(risks))
			for _, risk := range risks {
				expectedVersions[risk.Name] = allVersions
			}
			if diff := cmp.Diff(expectedVersions, versions); diff != "" {
				t.Errorf("Alert Risks version mismatch (-want +got):\n%s", diff)
			}

			if err != nil {
				t.Errorf("Alert Risks evaluation error: %v", err)
			}
		})
	}
}

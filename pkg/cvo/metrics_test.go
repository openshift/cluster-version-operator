package cvo

import (
	"errors"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/davecgh/go-spew/spew"
	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/tools/record"

	configv1 "github.com/openshift/api/config/v1"
)

func Test_operatorMetrics_Collect(t *testing.T) {
	tests := []struct {
		name  string
		optr  *Operator
		wants func(*testing.T, []prometheus.Metric)
	}{
		{
			name: "collect conditional update recommendations",
			optr: &Operator{
				name:           "test",
				releaseCreated: time.Unix(2, 0),
				cvLister: &cvLister{
					Items: []*configv1.ClusterVersion{
						{
							ObjectMeta: metav1.ObjectMeta{Name: "test"},
							Status: configv1.ClusterVersionStatus{
								ConditionalUpdates: []configv1.ConditionalUpdate{
									{
										Release: configv1.Release{Version: "4.5.6", Image: "pullspec/4.5.6"},
										Conditions: []metav1.Condition{
											{
												Type:               ConditionalUpdateConditionTypeRecommended,
												Status:             metav1.ConditionTrue,
												LastTransitionTime: metav1.NewTime(time.Unix(3, 0)),
												Reason:             "RiskDoesNotApply",
												Message:            "Risk is not risky so you do not risk",
											},
										},
									},
								},
							},
						},
					},
				},
			},
			wants: func(t *testing.T, metrics []prometheus.Metric) {
				if len(metrics) != 5 {
					t.Fatalf("Unexpected metrics %s", spew.Sdump(metrics))
				}
				// optr.releaseCreated is epoch+2s, LastTransitionTime is epoch+3s
				// we mock the evaluation time to be one minute after optr.releaseCreated => expect 59s
				expectMetric(t, metrics[2], 59, map[string]string{"reason": "RiskDoesNotApply", "recommended": "true", "version": "4.5.6"})
			},
		},
		{
			name: "collects current version",
			optr: &Operator{
				release: configv1.Release{
					Version: "0.0.2",
					Image:   "test/image:1",
				},
				releaseCreated: time.Unix(3, 0),
			},
			wants: func(t *testing.T, metrics []prometheus.Metric) {
				if len(metrics) != 2 {
					t.Fatalf("Unexpected metrics %s", spew.Sdump(metrics))
				}
				expectMetric(t, metrics[0], 3, map[string]string{"type": "current", "version": "0.0.2", "image": "test/image:1"})
				expectMetric(t, metrics[1], 1, map[string]string{"type": ""})
			},
		},
		{
			name: "collects current version with no age",
			optr: &Operator{
				release: configv1.Release{
					Version: "0.0.2",
					Image:   "test/image:1",
				},
			},
			wants: func(t *testing.T, metrics []prometheus.Metric) {
				if len(metrics) != 2 {
					t.Fatalf("Unexpected metrics %s", spew.Sdump(metrics))
				}
				expectMetric(t, metrics[0], 0, map[string]string{"type": "current", "version": "0.0.2", "image": "test/image:1"})
				expectMetric(t, metrics[1], 1, map[string]string{"type": ""})
			},
		},
		{
			name: "collects completed history",
			optr: &Operator{
				name: "test",
				release: configv1.Release{
					Version: "0.0.2",
					Image:   "test/image:1",
				},
				releaseCreated: time.Unix(3, 0),
				cvLister: &cvLister{
					Items: []*configv1.ClusterVersion{
						{
							ObjectMeta: metav1.ObjectMeta{
								Name:              "test",
								CreationTimestamp: metav1.Time{Time: time.Unix(2, 0)},
							},
							Status: configv1.ClusterVersionStatus{
								History: []configv1.UpdateHistory{
									{State: configv1.PartialUpdate, Version: "0.0.2", Image: "test/image:1", StartedTime: metav1.Time{Time: time.Unix(2, 0)}},
									{State: configv1.CompletedUpdate, Version: "0.0.1", Image: "test/image:0", CompletionTime: &([]metav1.Time{{Time: time.Unix(4, 0)}}[0])},
								},
							},
						},
					},
				},
			},
			wants: func(t *testing.T, metrics []prometheus.Metric) {
				if len(metrics) != 6 {
					t.Fatalf("Unexpected metrics %s", spew.Sdump(metrics))
				}
				expectMetric(t, metrics[0], 4, map[string]string{"type": "completed", "version": "0.0.1", "image": "test/image:0", "from_version": ""})
				expectMetric(t, metrics[1], 2, map[string]string{"type": "initial", "version": "0.0.1", "image": "test/image:0", "from_version": ""})
				expectMetric(t, metrics[2], 2, map[string]string{"type": "cluster", "version": "0.0.2", "image": "test/image:1", "from_version": "0.0.1"})
				expectMetric(t, metrics[3], 2, map[string]string{"type": "updating", "version": "0.0.2", "image": "test/image:1", "from_version": "0.0.1"})
				expectMetric(t, metrics[4], 3, map[string]string{"type": "current", "version": "0.0.2", "image": "test/image:1", "from_version": "0.0.1"})
				expectMetric(t, metrics[5], 1, map[string]string{"type": ""})
			},
		},
		{
			name: "collects completed history with prior completion",
			optr: &Operator{
				name: "test",
				release: configv1.Release{
					Version: "0.0.3",
					Image:   "test/image:2",
				},
				releaseCreated: time.Unix(3, 0),
				cvLister: &cvLister{
					Items: []*configv1.ClusterVersion{
						{
							ObjectMeta: metav1.ObjectMeta{
								Name:              "test",
								CreationTimestamp: metav1.Time{Time: time.Unix(2, 0)},
							},
							Status: configv1.ClusterVersionStatus{
								History: []configv1.UpdateHistory{
									{State: configv1.PartialUpdate, Version: "0.0.3", Image: "test/image:2", StartedTime: metav1.Time{Time: time.Unix(2, 0)}},
									{State: configv1.CompletedUpdate, Version: "0.0.2", Image: "test/image:1", CompletionTime: &([]metav1.Time{{Time: time.Unix(4, 0)}}[0])},
									{State: configv1.CompletedUpdate, Version: "0.0.1", Image: "test/image:0", CompletionTime: &([]metav1.Time{{Time: time.Unix(4, 0)}}[0])},
								},
							},
						},
					},
				},
			},
			wants: func(t *testing.T, metrics []prometheus.Metric) {
				if len(metrics) != 6 {
					t.Fatalf("Unexpected metrics %s", spew.Sdump(metrics))
				}
				expectMetric(t, metrics[0], 4, map[string]string{"type": "completed", "version": "0.0.2", "image": "test/image:1", "from_version": "0.0.1"})
				expectMetric(t, metrics[1], 2, map[string]string{"type": "initial", "version": "0.0.1", "image": "test/image:0", "from_version": ""})
				expectMetric(t, metrics[2], 2, map[string]string{"type": "cluster", "version": "0.0.3", "image": "test/image:2", "from_version": "0.0.1"})
				expectMetric(t, metrics[3], 2, map[string]string{"type": "updating", "version": "0.0.3", "image": "test/image:2", "from_version": "0.0.2"})
				expectMetric(t, metrics[4], 3, map[string]string{"type": "current", "version": "0.0.3", "image": "test/image:2", "from_version": "0.0.2"})
				expectMetric(t, metrics[5], 1, map[string]string{"type": ""})
			},
		},
		{
			name: "ignores partial history",
			optr: &Operator{
				name: "test",
				release: configv1.Release{
					Version: "0.0.2",
					Image:   "test/image:1",
				},
				releaseCreated: time.Unix(3, 0),
				cvLister: &cvLister{
					Items: []*configv1.ClusterVersion{
						{
							ObjectMeta: metav1.ObjectMeta{
								Name:              "test",
								CreationTimestamp: metav1.Time{Time: time.Unix(2, 0)},
							},
							Status: configv1.ClusterVersionStatus{
								History: []configv1.UpdateHistory{
									{State: configv1.PartialUpdate, CompletionTime: &([]metav1.Time{{Time: time.Unix(2, 0)}}[0])},
								},
							},
						},
					},
				},
			},
			wants: func(t *testing.T, metrics []prometheus.Metric) {
				if len(metrics) != 5 {
					t.Fatalf("Unexpected metrics %s", spew.Sdump(metrics))
				}
				expectMetric(t, metrics[0], 2, map[string]string{"type": "initial", "version": "", "image": "", "from_version": ""})
				expectMetric(t, metrics[1], 2, map[string]string{"type": "cluster", "version": "0.0.2", "image": "test/image:1", "from_version": ""})
				expectMetric(t, metrics[2], 0, map[string]string{"type": "updating", "version": "", "image": "", "from_version": ""})
				expectMetric(t, metrics[3], 3, map[string]string{"type": "current", "version": "0.0.2", "image": "test/image:1", "from_version": ""})
				expectMetric(t, metrics[4], 1, map[string]string{"type": ""})
			},
		},
		{
			name: "collects cluster operator without conditions",
			optr: &Operator{
				coLister: &coLister{
					Items: []*configv1.ClusterOperator{
						{
							ObjectMeta: metav1.ObjectMeta{
								Name: "test",
							},
							Status: configv1.ClusterOperatorStatus{
								Versions: []configv1.OperandVersion{
									{Name: "operator", Version: "10.1.5-1"},
									{Name: "operand", Version: "10.1.5-2"},
								},
							},
						},
					},
				},
			},
			wants: func(t *testing.T, metrics []prometheus.Metric) {
				if len(metrics) != 3 {
					t.Fatalf("Unexpected metrics %s", spew.Sdump(metrics))
				}
				expectMetric(t, metrics[0], 0, map[string]string{"type": "current", "version": "", "image": "", "from_version": ""})
				expectMetric(t, metrics[1], 0, map[string]string{"name": "test", "version": "10.1.5-1", "reason": "NoAvailableCondition"})
				expectMetric(t, metrics[2], 1, map[string]string{"type": ""})
			},
		},
		{
			name: "collects cluster operator unavailable",
			optr: &Operator{
				coLister: &coLister{
					Items: []*configv1.ClusterOperator{
						{
							ObjectMeta: metav1.ObjectMeta{
								Name: "test",
							},
							Status: configv1.ClusterOperatorStatus{
								Versions: []configv1.OperandVersion{
									{Name: "operator", Version: "10.1.5-1"},
									{Name: "operand", Version: "10.1.5-2"},
								},
								Conditions: []configv1.ClusterOperatorStatusCondition{
									{Type: configv1.OperatorAvailable, Status: configv1.ConditionFalse},
								},
							},
						},
					},
				},
			},
			wants: func(t *testing.T, metrics []prometheus.Metric) {
				if len(metrics) != 4 {
					t.Fatalf("Unexpected metrics %s", spew.Sdump(metrics))
				}
				expectMetric(t, metrics[0], 0, map[string]string{"type": "current", "version": "", "image": "", "from_version": ""})
				expectMetric(t, metrics[1], 0, map[string]string{"name": "test", "version": "10.1.5-1"})
				expectMetric(t, metrics[2], 0, map[string]string{"name": "test", "condition": "Available"})
				expectMetric(t, metrics[3], 1, map[string]string{"type": ""})
			},
		},
		{
			name: "collects cluster operator degraded",
			optr: &Operator{
				coLister: &coLister{
					Items: []*configv1.ClusterOperator{
						{
							ObjectMeta: metav1.ObjectMeta{
								Name: "test",
							},
							Status: configv1.ClusterOperatorStatus{
								Versions: []configv1.OperandVersion{
									{Name: "operator", Version: "10.1.5-1"},
									{Name: "operand", Version: "10.1.5-2"},
								},
								Conditions: []configv1.ClusterOperatorStatusCondition{
									{Type: configv1.OperatorAvailable, Status: configv1.ConditionTrue},
									{Type: configv1.OperatorDegraded, Status: configv1.ConditionTrue, LastTransitionTime: metav1.Time{Time: time.Unix(5, 0)}},
								},
							},
						},
					},
				},
			},
			wants: func(t *testing.T, metrics []prometheus.Metric) {
				if len(metrics) != 5 {
					t.Fatalf("Unexpected metrics %s", spew.Sdump(metrics))
				}
				expectMetric(t, metrics[0], 0, map[string]string{"type": "current", "version": "", "image": "", "from_version": ""})
				expectMetric(t, metrics[1], 1, map[string]string{"name": "test", "version": "10.1.5-1"})
				expectMetric(t, metrics[2], 1, map[string]string{"name": "test", "condition": "Available"})
				expectMetric(t, metrics[3], 1, map[string]string{"name": "test", "condition": "Degraded"})
				expectMetric(t, metrics[4], 1, map[string]string{"type": ""})
			},
		},
		{
			name: "collects cluster operator status custom",
			optr: &Operator{
				coLister: &coLister{
					Items: []*configv1.ClusterOperator{
						{
							ObjectMeta: metav1.ObjectMeta{
								Namespace: "default",
								Name:      "test",
							},
							Status: configv1.ClusterOperatorStatus{
								Conditions: []configv1.ClusterOperatorStatusCondition{
									{Type: configv1.OperatorAvailable, Status: configv1.ConditionTrue},
									{Type: configv1.ClusterStatusConditionType("Custom"), Status: configv1.ConditionFalse, Reason: "CustomReason"},
									{Type: configv1.ClusterStatusConditionType("Unknown"), Status: configv1.ConditionUnknown},
								},
							},
						},
					},
				},
			},
			wants: func(t *testing.T, metrics []prometheus.Metric) {
				if len(metrics) != 5 {
					t.Fatalf("Unexpected metrics %s", spew.Sdump(metrics))
				}
				expectMetric(t, metrics[0], 0, map[string]string{"type": "current", "version": "", "image": "", "from_version": ""})
				expectMetric(t, metrics[1], 1, map[string]string{"name": "test", "version": ""})
				expectMetric(t, metrics[2], 1, map[string]string{"name": "test", "condition": "Available", "reason": ""})
				expectMetric(t, metrics[3], 0, map[string]string{"name": "test", "condition": "Custom", "reason": "CustomReason"})
				expectMetric(t, metrics[4], 1, map[string]string{"type": ""})
			},
		},
		{
			name: "collects available updates",
			optr: &Operator{
				name: "test",
				cvLister: &cvLister{
					Items: []*configv1.ClusterVersion{
						{
							ObjectMeta: metav1.ObjectMeta{
								Name:              "test",
								CreationTimestamp: metav1.Time{Time: time.Unix(2, 0)},
							},
							Status: configv1.ClusterVersionStatus{
								AvailableUpdates: []configv1.Release{
									{Version: "1.0.1"},
									{Version: "1.0.2"},
								},
							},
						},
					},
				},
			},
			wants: func(t *testing.T, metrics []prometheus.Metric) {
				if len(metrics) != 5 {
					t.Fatalf("Unexpected metrics %s", spew.Sdump(metrics))
				}
				expectMetric(t, metrics[0], 2, map[string]string{"type": "initial", "version": "", "image": "", "from_version": ""})
				expectMetric(t, metrics[1], 2, map[string]string{"type": "cluster", "version": "", "image": "", "from_version": ""})
				expectMetric(t, metrics[2], 2, map[string]string{"upstream": "<default>", "channel": ""})
				expectMetric(t, metrics[3], 0, map[string]string{"type": "current", "version": "", "image": "", "from_version": ""})
				expectMetric(t, metrics[4], 1, map[string]string{"type": ""})
			},
		},
		{
			name: "collects available updates and reports 0 when updates fetched",
			optr: &Operator{
				name: "test",
				cvLister: &cvLister{
					Items: []*configv1.ClusterVersion{
						{
							ObjectMeta: metav1.ObjectMeta{
								Name:              "test",
								CreationTimestamp: metav1.Time{Time: time.Unix(2, 0)},
							},
							Status: configv1.ClusterVersionStatus{
								Conditions: []configv1.ClusterOperatorStatusCondition{
									{Type: configv1.RetrievedUpdates, Status: configv1.ConditionTrue, Reason: "Because stuff"},
								},
							},
						},
					},
				},
			},
			wants: func(t *testing.T, metrics []prometheus.Metric) {
				if len(metrics) != 6 {
					t.Fatalf("Unexpected metrics %s", spew.Sdump(metrics))
				}
				expectMetric(t, metrics[0], 2, map[string]string{"type": "initial", "version": "", "image": "", "from_version": ""})
				expectMetric(t, metrics[1], 2, map[string]string{"type": "cluster", "version": "", "image": "", "from_version": ""})

				expectMetric(t, metrics[2], 0, map[string]string{"upstream": "<default>", "channel": ""})
				expectMetric(t, metrics[3], 1, map[string]string{"name": "version", "condition": "RetrievedUpdates", "reason": "Because stuff"})
				expectMetric(t, metrics[4], 0, map[string]string{"type": "current", "version": "", "image": "", "from_version": ""})
				expectMetric(t, metrics[5], 1, map[string]string{"type": ""})
			},
		},
		{
			name: "collects update",
			optr: &Operator{
				release: configv1.Release{
					Version: "0.0.2",
					Image:   "test/image:1",
				},
				name: "test",
				cvLister: &cvLister{
					Items: []*configv1.ClusterVersion{
						{
							ObjectMeta: metav1.ObjectMeta{
								Name:              "test",
								CreationTimestamp: metav1.Time{Time: time.Unix(2, 0)},
							},
							Spec: configv1.ClusterVersionSpec{
								DesiredUpdate: &configv1.Update{Version: "1.0.0", Image: "test/image:2"},
							},
							Status: configv1.ClusterVersionStatus{
								Conditions: []configv1.ClusterOperatorStatusCondition{
									{Type: configv1.OperatorAvailable, Status: configv1.ConditionTrue, LastTransitionTime: metav1.Time{Time: time.Unix(5, 0)}, Reason: "Because stuff"},
								},
							},
						},
					},
				},
			},
			wants: func(t *testing.T, metrics []prometheus.Metric) {
				if len(metrics) != 6 {
					t.Fatalf("Unexpected metrics %s", spew.Sdump(metrics))
				}
				expectMetric(t, metrics[0], 2, map[string]string{"type": "initial", "version": "", "image": "", "from_version": ""})
				expectMetric(t, metrics[1], 2, map[string]string{"type": "cluster", "version": "0.0.2", "image": "test/image:1", "from_version": ""})
				expectMetric(t, metrics[2], 5, map[string]string{"type": "desired", "version": "1.0.0", "image": "test/image:2", "from_version": ""})
				expectMetric(t, metrics[3], 1, map[string]string{"name": "version", "condition": "Available", "reason": "Because stuff"})
				expectMetric(t, metrics[4], 0, map[string]string{"type": "current", "version": "0.0.2", "image": "test/image:1", "from_version": ""})
				expectMetric(t, metrics[5], 1, map[string]string{"type": ""})
			},
		},
		{
			name: "collects failing update",
			optr: &Operator{
				release: configv1.Release{
					Version: "0.0.2",
					Image:   "test/image:1",
				},
				name:           "test",
				releaseCreated: time.Unix(6, 0),
				cvLister: &cvLister{
					Items: []*configv1.ClusterVersion{
						{
							ObjectMeta: metav1.ObjectMeta{
								Name:              "test",
								CreationTimestamp: metav1.Time{Time: time.Unix(5, 0)},
							},
							Spec: configv1.ClusterVersionSpec{
								DesiredUpdate: &configv1.Update{Version: "1.0.0", Image: "test/image:2"},
							},
							Status: configv1.ClusterVersionStatus{
								History: []configv1.UpdateHistory{
									{State: configv1.CompletedUpdate, Version: "0.0.2", Image: "test/image:1", CompletionTime: &([]metav1.Time{{Time: time.Unix(2, 0)}}[0])},
								},
								Conditions: []configv1.ClusterOperatorStatusCondition{
									{Type: ClusterStatusFailing, Status: configv1.ConditionTrue, LastTransitionTime: metav1.Time{Time: time.Unix(4, 0)}, Reason: "Because stuff"},
								},
							},
						},
					},
				},
			},
			wants: func(t *testing.T, metrics []prometheus.Metric) {
				if len(metrics) != 8 {
					t.Fatalf("Unexpected metrics %s", spew.Sdump(metrics))
				}
				expectMetric(t, metrics[0], 2, map[string]string{"type": "completed", "version": "0.0.2", "image": "test/image:1", "from_version": ""})
				expectMetric(t, metrics[1], 5, map[string]string{"type": "initial", "version": "0.0.2", "image": "test/image:1", "from_version": ""})
				expectMetric(t, metrics[2], 5, map[string]string{"type": "cluster", "version": "0.0.2", "image": "test/image:1", "from_version": "0.0.2"})
				expectMetric(t, metrics[3], 5, map[string]string{"type": "desired", "version": "1.0.0", "image": "test/image:2", "from_version": "0.0.2"})
				expectMetric(t, metrics[4], 4, map[string]string{"type": "failure", "version": "1.0.0", "image": "test/image:2", "from_version": "0.0.2"})
				expectMetric(t, metrics[5], 1, map[string]string{"name": "version", "condition": "Failing", "reason": "Because stuff"})
				expectMetric(t, metrics[6], 6, map[string]string{"type": "current", "version": "0.0.2", "image": "test/image:1", "from_version": "0.0.2"})
				expectMetric(t, metrics[7], 1, map[string]string{"type": ""})
			},
		},
		{
			name: "collects failing image",
			optr: &Operator{
				release: configv1.Release{
					Version: "0.0.2",
					Image:   "test/image:1",
				},
				name: "test",
				cvLister: &cvLister{
					Items: []*configv1.ClusterVersion{
						{
							ObjectMeta: metav1.ObjectMeta{
								Name:              "test",
								CreationTimestamp: metav1.Time{Time: time.Unix(2, 0)},
							},
							Status: configv1.ClusterVersionStatus{
								Conditions: []configv1.ClusterOperatorStatusCondition{
									{Type: ClusterStatusFailing, Status: configv1.ConditionTrue, Reason: "Because stuff"},
								},
							},
						},
					},
				},
			},
			wants: func(t *testing.T, metrics []prometheus.Metric) {
				if len(metrics) != 6 {
					t.Fatalf("Unexpected metrics %s", spew.Sdump(metrics))
				}
				expectMetric(t, metrics[0], 2, map[string]string{"type": "initial", "version": "", "image": "", "from_version": ""})
				expectMetric(t, metrics[1], 2, map[string]string{"type": "cluster", "version": "0.0.2", "image": "test/image:1", "from_version": ""})
				expectMetric(t, metrics[2], 0, map[string]string{"type": "failure", "version": "0.0.2", "image": "test/image:1", "from_version": ""})
				expectMetric(t, metrics[3], 1, map[string]string{"name": "version", "condition": "Failing", "reason": "Because stuff"})
				expectMetric(t, metrics[4], 0, map[string]string{"type": "current", "version": "0.0.2", "image": "test/image:1", "from_version": ""})
				expectMetric(t, metrics[5], 1, map[string]string{"type": ""})
			},
		},
		{
			name: "collects legacy openshift-install info",
			optr: &Operator{
				release: configv1.Release{
					Version: "0.0.2",
					Image:   "test/image:1",
				},
				releaseCreated: time.Unix(3, 0),
				cmConfigLister: &cmConfigLister{
					Items: []*corev1.ConfigMap{{
						ObjectMeta: metav1.ObjectMeta{Name: "openshift-install"},
						Data:       map[string]string{"version": "v0.0.2", "invoker": "jane"},
					}},
				},
			},
			wants: func(t *testing.T, metrics []prometheus.Metric) {
				if len(metrics) != 2 {
					t.Fatalf("Unexpected metrics %s", spew.Sdump(metrics))
				}
				expectMetric(t, metrics[0], 3, map[string]string{"type": "current", "version": "0.0.2", "image": "test/image:1"})
				expectMetric(t, metrics[1], 1, map[string]string{"type": "openshift-install", "version": "v0.0.2", "invoker": "jane"})
			},
		},
		{
			name: "collects openshift-install info",
			optr: &Operator{
				release: configv1.Release{
					Version: "0.0.2",
					Image:   "test/image:1",
				},
				releaseCreated: time.Unix(3, 0),
				cmConfigLister: &cmConfigLister{
					Items: []*corev1.ConfigMap{
						{
							ObjectMeta: metav1.ObjectMeta{Name: "openshift-install"},
							Data:       map[string]string{"version": "v0.0.2", "invoker": "jane"},
						},
						{
							ObjectMeta: metav1.ObjectMeta{Name: "openshift-install-manifests"},
							Data:       map[string]string{"version": "v0.0.1", "invoker": "bill"},
						},
					},
				},
			},
			wants: func(t *testing.T, metrics []prometheus.Metric) {
				if len(metrics) != 2 {
					t.Fatalf("Unexpected metrics %s", spew.Sdump(metrics))
				}
				expectMetric(t, metrics[0], 3, map[string]string{"type": "current", "version": "0.0.2", "image": "test/image:1"})
				expectMetric(t, metrics[1], 1, map[string]string{"type": "openshift-install", "version": "v0.0.2", "invoker": "jane"})
			},
		},
		{
			name: "collects openshift-install-manifests info",
			optr: &Operator{
				release: configv1.Release{
					Version: "0.0.2",
					Image:   "test/image:1",
				},
				releaseCreated: time.Unix(3, 0),
				cmConfigLister: &cmConfigLister{
					Items: []*corev1.ConfigMap{{
						ObjectMeta: metav1.ObjectMeta{Name: "openshift-install-manifests"},
						Data:       map[string]string{"version": "v0.0.1", "invoker": "bill"},
					}},
				},
			},
			wants: func(t *testing.T, metrics []prometheus.Metric) {
				if len(metrics) != 2 {
					t.Fatalf("Unexpected metrics %s", spew.Sdump(metrics))
				}
				expectMetric(t, metrics[0], 3, map[string]string{"type": "current", "version": "0.0.2", "image": "test/image:1"})
				expectMetric(t, metrics[1], 1, map[string]string{"type": "other", "version": "v0.0.1", "invoker": "bill"})
			},
		},
		{
			name: "collects empty openshift-install info",
			optr: &Operator{
				release: configv1.Release{
					Version: "0.0.2",
					Image:   "test/image:1",
				},
				releaseCreated: time.Unix(3, 0),
				cmConfigLister: &cmConfigLister{
					Items: []*corev1.ConfigMap{{
						ObjectMeta: metav1.ObjectMeta{Name: "openshift-install"},
					}},
				},
			},
			wants: func(t *testing.T, metrics []prometheus.Metric) {
				if len(metrics) != 2 {
					t.Fatalf("Unexpected metrics %s", spew.Sdump(metrics))
				}
				expectMetric(t, metrics[0], 3, map[string]string{"type": "current", "version": "0.0.2", "image": "test/image:1"})
				expectMetric(t, metrics[1], 1, map[string]string{"type": "openshift-install", "version": "<missing>", "invoker": "<missing>"})
			},
		},
		{
			name: "skips openshift-install info on error",
			optr: &Operator{
				release: configv1.Release{
					Version: "0.0.2",
					Image:   "test/image:1",
				},
				releaseCreated: time.Unix(3, 0),
				cmConfigLister: &cmConfigLister{
					Err: errors.New("dial timeout"),
				},
			},
			wants: func(t *testing.T, metrics []prometheus.Metric) {
				if len(metrics) != 1 {
					t.Fatalf("Unexpected metrics %s", spew.Sdump(metrics))
				}
				expectMetric(t, metrics[0], 3, map[string]string{"type": "current", "version": "0.0.2", "image": "test/image:1"})
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.optr.eventRecorder = record.NewFakeRecorder(100)
			if tt.optr.cvLister == nil {
				tt.optr.cvLister = &cvLister{}
			}
			if tt.optr.coLister == nil {
				tt.optr.coLister = &coLister{}
			}
			if tt.optr.cmConfigLister == nil {
				tt.optr.cmConfigLister = &cmConfigLister{}
			}
			m := newOperatorMetrics(tt.optr)
			m.nowFunc = func() time.Time { return tt.optr.releaseCreated.Add(time.Minute) }
			descCh := make(chan *prometheus.Desc)
			go func() {
				for range descCh {
				}
			}()
			m.Describe(descCh)
			close(descCh)
			ch := make(chan prometheus.Metric)
			go func() {
				m.Collect(ch)
				close(ch)
			}()
			var collected []prometheus.Metric
			for sample := range ch {
				collected = append(collected, sample)
			}
			tt.wants(t, collected)
		})
	}
}

func Test_operatorMetrics_CollectTransitions(t *testing.T) {
	tests := []struct {
		name    string
		optr    *Operator
		changes []interface{}
		wants   func(*testing.T, []prometheus.Metric)
	}{
		{
			changes: []interface{}{
				&configv1.ClusterOperator{
					ObjectMeta: metav1.ObjectMeta{Name: "test"},
					Status: configv1.ClusterOperatorStatus{
						Conditions: []configv1.ClusterOperatorStatusCondition{},
					},
				},
				&configv1.ClusterOperator{
					ObjectMeta: metav1.ObjectMeta{Name: "test"},
					Status: configv1.ClusterOperatorStatus{
						Conditions: []configv1.ClusterOperatorStatusCondition{
							{Type: configv1.OperatorAvailable, Status: configv1.ConditionTrue},
							{Type: configv1.ClusterStatusConditionType("Custom"), Status: configv1.ConditionFalse},
						},
					},
				},
				&configv1.ClusterOperator{
					ObjectMeta: metav1.ObjectMeta{Name: "test"},
					Status: configv1.ClusterOperatorStatus{
						Conditions: []configv1.ClusterOperatorStatusCondition{
							{Type: configv1.OperatorAvailable, Status: configv1.ConditionFalse},
							{Type: configv1.ClusterStatusConditionType("Custom"), Status: configv1.ConditionFalse},
							{Type: configv1.ClusterStatusConditionType("Unknown"), Status: configv1.ConditionUnknown},
						},
					},
				},
				&configv1.ClusterOperator{
					ObjectMeta: metav1.ObjectMeta{Name: "test"},
					Status: configv1.ClusterOperatorStatus{
						Conditions: []configv1.ClusterOperatorStatusCondition{
							{Type: configv1.ClusterStatusConditionType("Custom"), Status: configv1.ConditionTrue},
							{Type: configv1.ClusterStatusConditionType("Unknown"), Status: configv1.ConditionTrue},
						},
					},
				},
			},
			optr: &Operator{
				coLister:      &coLister{},
				eventRecorder: record.NewFakeRecorder(100),
			},
			wants: func(t *testing.T, metrics []prometheus.Metric) {
				if len(metrics) != 5 {
					t.Fatalf("Unexpected metrics %s", spew.Sdump(metrics))
				}
				sort.Slice(metrics, func(i, j int) bool {
					a, b := metricParts(t, metrics[i], "name", "condition"), metricParts(t, metrics[j], "name", "condition")
					return a < b
				})
				expectMetric(t, metrics[0], 1, map[string]string{"type": ""})
				expectMetric(t, metrics[1], 3, map[string]string{"name": "test", "condition": "Available"})
				expectMetric(t, metrics[2], 2, map[string]string{"name": "test", "condition": "Custom"})
				expectMetric(t, metrics[3], 2, map[string]string{"name": "test", "condition": "Unknown"})
				expectMetric(t, metrics[4], 0, map[string]string{"type": "current", "version": "", "image": ""})
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.optr.cvLister == nil {
				tt.optr.cvLister = &cvLister{}
			}
			if tt.optr.coLister == nil {
				tt.optr.coLister = &coLister{}
			}
			if tt.optr.cmConfigLister == nil {
				tt.optr.cmConfigLister = &cmConfigLister{}
			}
			m := newOperatorMetrics(tt.optr)
			for i := range tt.changes {
				if i == 0 {
					continue
				}
				m.clusterOperatorChanged(tt.changes[i-1], tt.changes[i])
			}
			ch := make(chan prometheus.Metric)
			go func() {
				m.Collect(ch)
				close(ch)
			}()
			var collected []prometheus.Metric
			for sample := range ch {
				collected = append(collected, sample)
			}
			tt.wants(t, collected)
		})
	}
}

func TestCollectUnknownConditionalUpdates(t *testing.T) {
	anchorTime := time.Now()

	type valueWithLabels struct {
		value  float64
		labels map[string]string
	}
	testCases := []struct {
		name     string
		updates  []configv1.ConditionalUpdate
		evaluate time.Time
		expected []valueWithLabels
	}{
		{
			name:     "no conditional updates",
			updates:  []configv1.ConditionalUpdate{},
			evaluate: anchorTime.Add(time.Minute),
			expected: []valueWithLabels{},
		},
		{
			name: "no recommended conditions on updates",
			updates: []configv1.ConditionalUpdate{
				{
					Release: configv1.Release{Version: "4.13.1", Image: "pullspec"},
					Risks:   []configv1.ConditionalUpdateRisk{{Name: "Risk"}},
					Conditions: []metav1.Condition{{
						Type:               "SomethingElseThanRecommended",
						Status:             "Unknown",
						LastTransitionTime: metav1.NewTime(anchorTime),
						Reason:             "NoIdea",
						Message:            "No Idea",
					}},
				},
			},
			expected: []valueWithLabels{},
		},
		{
			name: "recommended false",
			updates: []configv1.ConditionalUpdate{
				{
					Release: configv1.Release{Version: "4.13.1", Image: "pullspec"},
					Risks:   []configv1.ConditionalUpdateRisk{{Name: "Risk"}},
					Conditions: []metav1.Condition{{
						Type:               ConditionalUpdateConditionTypeRecommended,
						Status:             metav1.ConditionFalse,
						LastTransitionTime: metav1.NewTime(anchorTime),
						Reason:             "RiskApplies",
						Message:            "Risk is risky so do not risk it",
					}},
				},
			},
			evaluate: anchorTime.Add(time.Minute),
			expected: []valueWithLabels{{
				value:  60,
				labels: map[string]string{"version": "4.13.1", "recommended": "false", "reason": "RiskApplies"},
			}},
		},
		{
			name: "recommended true",
			updates: []configv1.ConditionalUpdate{
				{
					Release: configv1.Release{Version: "4.13.1", Image: "pullspec"},
					Risks:   []configv1.ConditionalUpdateRisk{{Name: "Risk"}},
					Conditions: []metav1.Condition{{
						Type:               ConditionalUpdateConditionTypeRecommended,
						Status:             metav1.ConditionTrue,
						LastTransitionTime: metav1.NewTime(anchorTime),
						Reason:             "RiskDoesNotApply",
						Message:            "Risk is not risky so you do not risk",
					}},
				},
			},
			evaluate: anchorTime.Add(time.Minute),
			expected: []valueWithLabels{{
				value:  60,
				labels: map[string]string{"version": "4.13.1", "recommended": "true", "reason": "RiskDoesNotApply"},
			}},
		},
		{
			name: "recommended unknown",
			updates: []configv1.ConditionalUpdate{
				{
					Release: configv1.Release{Version: "4.13.1", Image: "pullspec"},
					Risks:   []configv1.ConditionalUpdateRisk{{Name: "Risk"}},
					Conditions: []metav1.Condition{{
						Type:               ConditionalUpdateConditionTypeRecommended,
						Status:             metav1.ConditionUnknown,
						LastTransitionTime: metav1.NewTime(anchorTime),
						Reason:             "EvaluationFailed",
						Message:            "No idea sorry",
					}},
				},
			},
			evaluate: anchorTime.Add(time.Minute),
			expected: []valueWithLabels{{
				value:  60,
				labels: map[string]string{"version": "4.13.1", "recommended": "unknown", "reason": "EvaluationFailed"},
			}},
		},
		{
			name: "multiple versions with different transition times",
			updates: []configv1.ConditionalUpdate{
				{
					Release: configv1.Release{Version: "4.13.1", Image: "pullspec"},
					Risks:   []configv1.ConditionalUpdateRisk{{Name: "Risk"}},
					Conditions: []metav1.Condition{{
						Type:               ConditionalUpdateConditionTypeRecommended,
						Status:             metav1.ConditionFalse,
						LastTransitionTime: metav1.NewTime(anchorTime),
						Reason:             "RiskApplies",
						Message:            "Risk is risky so do not risk it",
					}},
				},
				{
					Release: configv1.Release{Version: "4.13.2", Image: "pullspec"},
					Risks:   []configv1.ConditionalUpdateRisk{{Name: "Risk"}},
					Conditions: []metav1.Condition{{
						Type:               ConditionalUpdateConditionTypeRecommended,
						Status:             metav1.ConditionTrue,
						LastTransitionTime: metav1.NewTime(anchorTime.Add(time.Minute)),
						Reason:             "RiskDoesNotApply",
						Message:            "Risk is not risky",
					}},
				},
				{
					Release: configv1.Release{Version: "4.13.3", Image: "pullspec"},
					Risks:   []configv1.ConditionalUpdateRisk{{Name: "Risk"}},
					Conditions: []metav1.Condition{{
						Type:               ConditionalUpdateConditionTypeRecommended,
						Status:             metav1.ConditionUnknown,
						LastTransitionTime: metav1.NewTime(anchorTime.Add(time.Minute * 2)),
						Reason:             "EvaluationFailed",
						Message:            "Metrics stack was abducted by aliens",
					}},
				},
			},
			evaluate: anchorTime.Add(5 * time.Minute),
			expected: []valueWithLabels{
				{
					value:  5 * 60,
					labels: map[string]string{"version": "4.13.1", "recommended": "false", "reason": "RiskApplies"},
				},
				{
					value:  4 * 60,
					labels: map[string]string{"version": "4.13.2", "recommended": "true", "reason": "RiskDoesNotApply"},
				},
				{
					value:  3 * 60,
					labels: map[string]string{"version": "4.13.3", "recommended": "unknown", "reason": "EvaluationFailed"},
				},
			},
		},
	}

	for _, tc := range testCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			optr := &Operator{}
			m := newOperatorMetrics(optr)
			m.nowFunc = func() time.Time { return tc.evaluate }
			ch := make(chan prometheus.Metric)

			go func() {
				m.collectConditionalUpdates(ch, tc.updates)
				close(ch)
			}()

			var collected []prometheus.Metric
			for item := range ch {
				collected = append(collected, item)
			}

			if lenC, lenE := len(collected), len(tc.expected); lenC != lenE {

				t.Fatalf("Expected %d metrics, got %d metrics\nGot metrics: %s", lenE, lenC, spew.Sdump(collected))
			}
			for i := range tc.expected {
				expectMetric(t, collected[i], tc.expected[i].value, tc.expected[i].labels)
			}
		})
	}
}

func expectMetric(t *testing.T, metric prometheus.Metric, value float64, labels map[string]string) {
	t.Helper()
	var d dto.Metric
	if err := metric.Write(&d); err != nil {
		t.Fatalf("unable to write metrics: %v", err)
	}
	if d.Gauge != nil {
		if value != *d.Gauge.Value {
			t.Fatalf("incorrect value for %s: %s", metric.Desc().String(), d.String())
		}
	}
	for _, label := range d.Label {
		if labels[*label.Name] != *label.Value {
			t.Fatalf("unexpected labels for %s: %s=%v", metric.Desc().String(), *label.Name, *label.Value)
		}
		delete(labels, *label.Name)
	}
	if len(labels) > 0 {
		t.Fatalf("missing labels for %s: %v", metric.Desc().String(), labels)
	}
}

func metricParts(t *testing.T, metric prometheus.Metric, labels ...string) string {
	t.Helper()
	var d dto.Metric
	if err := metric.Write(&d); err != nil {
		t.Fatalf("unable to write metrics: %v", err)
	}
	var parts []string
	parts = append(parts, metric.Desc().String())
	for _, name := range labels {
		var found bool
		for _, label := range d.Label {
			if *label.Name == name {
				found = true
				parts = append(parts, *label.Value)
				break
			}
		}
		if !found {
			parts = append(parts, "")
		}
	}
	return strings.Join(parts, " ")
}

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

	configv1 "github.com/openshift/api/config/v1"
)

func Test_operatorMetrics_Collect(t *testing.T) {
	tests := []struct {
		name  string
		optr  *Operator
		wants func(*testing.T, []prometheus.Metric)
	}{
		{
			name: "collects current version",
			optr: &Operator{
				releaseVersion: "0.0.2",
				releaseImage:   "test/image:1",
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
				releaseVersion: "0.0.2",
				releaseImage:   "test/image:1",
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
				name:           "test",
				releaseVersion: "0.0.2",
				releaseImage:   "test/image:1",
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
				name:           "test",
				releaseVersion: "0.0.3",
				releaseImage:   "test/image:2",
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
				name:           "test",
				releaseVersion: "0.0.2",
				releaseImage:   "test/image:1",
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
			name: "collects cluster operator status failure",
			optr: &Operator{
				coLister: &coLister{
					Items: []*configv1.ClusterOperator{
						{
							ObjectMeta: metav1.ObjectMeta{
								Name: "test",
							},
							Status: configv1.ClusterOperatorStatus{
								Versions: []configv1.OperandVersion{
									{Version: "10.1.5-1"},
									{Version: "10.1.5-2"},
								},
								Conditions: []configv1.ClusterOperatorStatusCondition{
									{Type: configv1.OperatorAvailable, Status: configv1.ConditionTrue},
									{Type: ClusterStatusFailing, Status: configv1.ConditionTrue, LastTransitionTime: metav1.Time{Time: time.Unix(5, 0)}},
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
				expectMetric(t, metrics[1], 0, map[string]string{"name": "test", "version": "10.1.5-1"})
				expectMetric(t, metrics[2], 1, map[string]string{"name": "test", "condition": "Available"})
				expectMetric(t, metrics[3], 1, map[string]string{"name": "test", "condition": "Failing"})
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
								AvailableUpdates: []configv1.Update{
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
									{Type: configv1.RetrievedUpdates, Status: configv1.ConditionTrue},
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
				expectMetric(t, metrics[2], 0, map[string]string{"upstream": "<default>", "channel": ""})
				expectMetric(t, metrics[3], 0, map[string]string{"type": "current", "version": "", "image": "", "from_version": ""})
				expectMetric(t, metrics[4], 1, map[string]string{"type": ""})
			},
		},
		{
			name: "collects update",
			optr: &Operator{
				releaseVersion: "0.0.2",
				releaseImage:   "test/image:1",
				name:           "test",
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
									{Type: configv1.OperatorAvailable, Status: configv1.ConditionTrue, LastTransitionTime: metav1.Time{Time: time.Unix(5, 0)}},
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
				expectMetric(t, metrics[2], 5, map[string]string{"type": "desired", "version": "1.0.0", "image": "test/image:2", "from_version": ""})
				expectMetric(t, metrics[3], 0, map[string]string{"type": "current", "version": "0.0.2", "image": "test/image:1", "from_version": ""})
				expectMetric(t, metrics[4], 1, map[string]string{"type": ""})
			},
		},
		{
			name: "collects failing update",
			optr: &Operator{
				releaseVersion: "0.0.2",
				releaseImage:   "test/image:1",
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
								Conditions: []configv1.ClusterOperatorStatusCondition{
									{Type: ClusterStatusFailing, Status: configv1.ConditionTrue, LastTransitionTime: metav1.Time{Time: time.Unix(4, 0)}},
								},
							},
						},
					},
				},
			},
			wants: func(t *testing.T, metrics []prometheus.Metric) {
				if len(metrics) != 7 {
					t.Fatalf("Unexpected metrics %s", spew.Sdump(metrics))
				}
				expectMetric(t, metrics[0], 5, map[string]string{"type": "initial", "version": "", "image": "", "from_version": ""})
				expectMetric(t, metrics[1], 5, map[string]string{"type": "cluster", "version": "0.0.2", "image": "test/image:1", "from_version": ""})
				expectMetric(t, metrics[2], 5, map[string]string{"type": "desired", "version": "1.0.0", "image": "test/image:2", "from_version": ""})
				expectMetric(t, metrics[3], 4, map[string]string{"type": "failure", "version": "1.0.0", "image": "test/image:2", "from_version": ""})
				expectMetric(t, metrics[4], 4, map[string]string{"type": "failure", "version": "0.0.2", "image": "test/image:1", "from_version": ""})
				expectMetric(t, metrics[5], 6, map[string]string{"type": "current", "version": "0.0.2", "image": "test/image:1", "from_version": ""})
				expectMetric(t, metrics[6], 1, map[string]string{"type": ""})
			},
		},
		{
			name: "collects failing image",
			optr: &Operator{
				releaseVersion: "0.0.2",
				releaseImage:   "test/image:1",
				name:           "test",
				cvLister: &cvLister{
					Items: []*configv1.ClusterVersion{
						{
							ObjectMeta: metav1.ObjectMeta{
								Name:              "test",
								CreationTimestamp: metav1.Time{Time: time.Unix(2, 0)},
							},
							Status: configv1.ClusterVersionStatus{
								Conditions: []configv1.ClusterOperatorStatusCondition{
									{Type: ClusterStatusFailing, Status: configv1.ConditionTrue},
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
				expectMetric(t, metrics[2], 0, map[string]string{"type": "failure", "version": "0.0.2", "image": "test/image:1", "from_version": ""})
				expectMetric(t, metrics[3], 0, map[string]string{"type": "current", "version": "0.0.2", "image": "test/image:1", "from_version": ""})
				expectMetric(t, metrics[4], 1, map[string]string{"type": ""})
			},
		},
		{
			name: "collects openshift-install info",
			optr: &Operator{
				releaseVersion: "0.0.2",
				releaseImage:   "test/image:1",
				releaseCreated: time.Unix(3, 0),
				cmLister: &cmLister{
					Items: []*corev1.ConfigMap{{
						ObjectMeta: metav1.ObjectMeta{Name: "openshift-install"},
						Data: map[string]string {"version": "v0.0.2", "invoker": "jane"},
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
			name: "collects empty openshift-install info",
			optr: &Operator{
				releaseVersion: "0.0.2",
				releaseImage:   "test/image:1",
				releaseCreated: time.Unix(3, 0),
				cmLister: &cmLister{
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
				releaseVersion: "0.0.2",
				releaseImage:   "test/image:1",
				releaseCreated: time.Unix(3, 0),
				cmLister: &cmLister{
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
			if tt.optr.cvLister == nil {
				tt.optr.cvLister = &cvLister{}
			}
			if tt.optr.coLister == nil {
				tt.optr.coLister = &coLister{}
			}
			if tt.optr.cmLister == nil {
				tt.optr.cmLister = &cmLister{}
			}
			m := newOperatorMetrics(tt.optr)
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
				coLister: &coLister{},
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
			if tt.optr.cmLister == nil {
				tt.optr.cmLister = &cmLister{}
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

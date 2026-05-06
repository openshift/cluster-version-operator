package cvo

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/davecgh/go-spew/spew"
	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apiserver/pkg/authentication/user"
	"k8s.io/apiserver/pkg/server/dynamiccertificates"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/tools/record"

	configv1 "github.com/openshift/api/config/v1"
	configfake "github.com/openshift/client-go/config/clientset/versioned/fake"
	configinformers "github.com/openshift/client-go/config/informers/externalversions"
	"github.com/openshift/library-go/pkg/crypto"

	"github.com/openshift/cluster-version-operator/pkg/featuregates"
	"github.com/openshift/cluster-version-operator/pkg/internal"
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
												Type:               internal.ConditionalUpdateConditionTypeRecommended,
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
				expectMetric(t, metrics[2], 3, map[string]string{"condition": "Recommended", "reason": "RiskDoesNotApply", "status": "True", "version": "4.5.6"})
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
				if len(metrics) != 4 {
					t.Fatalf("Unexpected metrics %s", spew.Sdump(metrics))
				}
				expectMetric(t, metrics[0], 0, map[string]string{"name": "version", "version": "", "reason": "ClusterVersionNotFound"})
				expectMetric(t, metrics[1], 0, map[string]string{"name": "version", "condition": "Available", "reason": "ClusterVersionNotFound"})
				expectMetric(t, metrics[2], 3, map[string]string{"type": "current", "version": "0.0.2", "image": "test/image:1"})
				expectMetric(t, metrics[3], 1, map[string]string{"type": "", "version": "", "invoker": ""})
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
				if len(metrics) != 4 {
					t.Fatalf("Unexpected metrics %s", spew.Sdump(metrics))
				}
				expectMetric(t, metrics[0], 0, map[string]string{"name": "version", "version": "", "reason": "ClusterVersionNotFound"})
				expectMetric(t, metrics[1], 0, map[string]string{"name": "version", "condition": "Available", "reason": "ClusterVersionNotFound"})
				expectMetric(t, metrics[2], 0, map[string]string{"type": "current", "version": "0.0.2", "image": "test/image:1"})
				expectMetric(t, metrics[3], 1, map[string]string{"type": "", "version": "", "invoker": ""})
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
				if len(metrics) != 5 {
					t.Fatalf("Unexpected metrics %s", spew.Sdump(metrics))
				}
				expectMetric(t, metrics[0], 0, map[string]string{"name": "version", "version": "", "reason": "ClusterVersionNotFound"})
				expectMetric(t, metrics[1], 0, map[string]string{"name": "version", "condition": "Available", "reason": "ClusterVersionNotFound"})
				expectMetric(t, metrics[2], 0, map[string]string{"type": "current", "version": "", "image": "", "from_version": ""})
				expectMetric(t, metrics[3], 0, map[string]string{"name": "test", "version": "10.1.5-1", "reason": "NoAvailableCondition"})
				expectMetric(t, metrics[4], 1, map[string]string{"type": ""})
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
				if len(metrics) != 6 {
					t.Fatalf("Unexpected metrics %s", spew.Sdump(metrics))
				}
				expectMetric(t, metrics[0], 0, map[string]string{"name": "version", "version": "", "reason": "ClusterVersionNotFound"})
				expectMetric(t, metrics[1], 0, map[string]string{"name": "version", "condition": "Available", "reason": "ClusterVersionNotFound"})
				expectMetric(t, metrics[2], 0, map[string]string{"type": "current", "version": "", "image": "", "from_version": ""})
				expectMetric(t, metrics[3], 0, map[string]string{"name": "test", "version": "10.1.5-1"})
				expectMetric(t, metrics[4], 0, map[string]string{"name": "test", "condition": "Available"})
				expectMetric(t, metrics[5], 1, map[string]string{"type": ""})
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
				if len(metrics) != 7 {
					t.Fatalf("Unexpected metrics %s", spew.Sdump(metrics))
				}
				expectMetric(t, metrics[0], 0, map[string]string{"name": "version", "version": "", "reason": "ClusterVersionNotFound"})
				expectMetric(t, metrics[1], 0, map[string]string{"name": "version", "condition": "Available", "reason": "ClusterVersionNotFound"})
				expectMetric(t, metrics[2], 0, map[string]string{"type": "current", "version": "", "image": "", "from_version": ""})
				expectMetric(t, metrics[3], 1, map[string]string{"name": "test", "version": "10.1.5-1"})
				expectMetric(t, metrics[4], 1, map[string]string{"name": "test", "condition": "Available"})
				expectMetric(t, metrics[5], 1, map[string]string{"name": "test", "condition": "Degraded"})
				expectMetric(t, metrics[6], 1, map[string]string{"type": ""})
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
									{Type: "Custom", Status: configv1.ConditionFalse, Reason: "CustomReason"},
									{Type: "Unknown", Status: configv1.ConditionUnknown},
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
				expectMetric(t, metrics[0], 0, map[string]string{"name": "version", "version": "", "reason": "ClusterVersionNotFound"})
				expectMetric(t, metrics[1], 0, map[string]string{"name": "version", "condition": "Available", "reason": "ClusterVersionNotFound"})
				expectMetric(t, metrics[2], 0, map[string]string{"type": "current", "version": "", "image": "", "from_version": ""})
				expectMetric(t, metrics[3], 1, map[string]string{"name": "test", "version": ""})
				expectMetric(t, metrics[4], 1, map[string]string{"name": "test", "condition": "Available", "reason": ""})
				expectMetric(t, metrics[5], 0, map[string]string{"name": "test", "condition": "Custom", "reason": "CustomReason"})
				expectMetric(t, metrics[6], 1, map[string]string{"type": ""})
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
				if len(metrics) != 7 {
					t.Fatalf("Unexpected metrics %s", spew.Sdump(metrics))
				}
				expectMetric(t, metrics[0], 2, map[string]string{"type": "initial", "version": "", "image": "", "from_version": ""})
				expectMetric(t, metrics[1], 2, map[string]string{"type": "cluster", "version": "0.0.2", "image": "test/image:1", "from_version": ""})
				expectMetric(t, metrics[2], 5, map[string]string{"type": "desired", "version": "1.0.0", "image": "test/image:2", "from_version": ""})
				expectMetric(t, metrics[3], 1, map[string]string{"name": "version", "version": "", "reason": "Because stuff"})
				expectMetric(t, metrics[4], 1, map[string]string{"name": "version", "condition": "Available", "reason": "Because stuff"})
				expectMetric(t, metrics[5], 0, map[string]string{"type": "current", "version": "0.0.2", "image": "test/image:1", "from_version": ""})
				expectMetric(t, metrics[6], 1, map[string]string{"type": ""})
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
									{Type: internal.ClusterStatusFailing, Status: configv1.ConditionTrue, LastTransitionTime: metav1.Time{Time: time.Unix(4, 0)}, Reason: "Because stuff"},
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
									{Type: internal.ClusterStatusFailing, Status: configv1.ConditionTrue, Reason: "Because stuff"},
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
				if len(metrics) != 4 {
					t.Fatalf("Unexpected metrics %s", spew.Sdump(metrics))
				}
				expectMetric(t, metrics[0], 0, map[string]string{"name": "version", "version": "", "reason": "ClusterVersionNotFound"})
				expectMetric(t, metrics[1], 0, map[string]string{"name": "version", "condition": "Available", "reason": "ClusterVersionNotFound"})
				expectMetric(t, metrics[2], 3, map[string]string{"type": "current", "version": "0.0.2", "image": "test/image:1"})
				expectMetric(t, metrics[3], 1, map[string]string{"type": "openshift-install", "version": "v0.0.2", "invoker": "jane"})
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
				if len(metrics) != 4 {
					t.Fatalf("Unexpected metrics %s", spew.Sdump(metrics))
				}
				expectMetric(t, metrics[0], 0, map[string]string{"name": "version", "version": "", "reason": "ClusterVersionNotFound"})
				expectMetric(t, metrics[1], 0, map[string]string{"name": "version", "condition": "Available", "reason": "ClusterVersionNotFound"})
				expectMetric(t, metrics[2], 3, map[string]string{"type": "current", "version": "0.0.2", "image": "test/image:1"})
				expectMetric(t, metrics[3], 1, map[string]string{"type": "openshift-install", "version": "v0.0.2", "invoker": "jane"})
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
				if len(metrics) != 4 {
					t.Fatalf("Unexpected metrics %s", spew.Sdump(metrics))
				}
				expectMetric(t, metrics[0], 0, map[string]string{"name": "version", "version": "", "reason": "ClusterVersionNotFound"})
				expectMetric(t, metrics[1], 0, map[string]string{"name": "version", "condition": "Available", "reason": "ClusterVersionNotFound"})
				expectMetric(t, metrics[2], 3, map[string]string{"type": "current", "version": "0.0.2", "image": "test/image:1"})
				expectMetric(t, metrics[3], 1, map[string]string{"type": "other", "version": "v0.0.1", "invoker": "bill"})
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
				if len(metrics) != 4 {
					t.Fatalf("Unexpected metrics %s", spew.Sdump(metrics))
				}
				expectMetric(t, metrics[0], 0, map[string]string{"name": "version", "version": "", "reason": "ClusterVersionNotFound"})
				expectMetric(t, metrics[1], 0, map[string]string{"name": "version", "condition": "Available", "reason": "ClusterVersionNotFound"})
				expectMetric(t, metrics[2], 3, map[string]string{"type": "current", "version": "0.0.2", "image": "test/image:1"})
				expectMetric(t, metrics[3], 1, map[string]string{"type": "openshift-install", "version": "<missing>", "invoker": "<missing>"})
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
				if len(metrics) != 3 {
					t.Fatalf("Unexpected metrics %s", spew.Sdump(metrics))
				}
				expectMetric(t, metrics[0], 0, map[string]string{"name": "version", "version": "", "reason": "ClusterVersionNotFound"})
				expectMetric(t, metrics[1], 0, map[string]string{"name": "version", "condition": "Available", "reason": "ClusterVersionNotFound"})
				expectMetric(t, metrics[2], 3, map[string]string{"type": "current", "version": "0.0.2", "image": "test/image:1"})
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.optr.enabledCVOFeatureGates = featuregates.DefaultCvoGates("version")
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
							{Type: "Custom", Status: configv1.ConditionFalse},
						},
					},
				},
				&configv1.ClusterOperator{
					ObjectMeta: metav1.ObjectMeta{Name: "test"},
					Status: configv1.ClusterOperatorStatus{
						Conditions: []configv1.ClusterOperatorStatusCondition{
							{Type: configv1.OperatorAvailable, Status: configv1.ConditionFalse},
							{Type: "Custom", Status: configv1.ConditionFalse},
							{Type: "Unknown", Status: configv1.ConditionUnknown},
						},
					},
				},
				&configv1.ClusterOperator{
					ObjectMeta: metav1.ObjectMeta{Name: "test"},
					Status: configv1.ClusterOperatorStatus{
						Conditions: []configv1.ClusterOperatorStatusCondition{
							{Type: "Custom", Status: configv1.ConditionTrue},
							{Type: "Unknown", Status: configv1.ConditionTrue},
						},
					},
				},
			},
			optr: &Operator{
				coLister:      &coLister{},
				eventRecorder: record.NewFakeRecorder(100),
			},
			wants: func(t *testing.T, metrics []prometheus.Metric) {
				sort.Slice(metrics, func(i, j int) bool {
					a, b := metricParts(t, metrics[i], "name", "condition"), metricParts(t, metrics[j], "name", "condition")
					return a < b
				})
				if len(metrics) != 7 {
					t.Fatalf("Unexpected metrics %s", spew.Sdump(metrics))
				}
				expectMetric(t, metrics[0], 1, map[string]string{"type": ""})
				expectMetric(t, metrics[1], 3, map[string]string{"name": "test", "condition": "Available"})
				expectMetric(t, metrics[2], 2, map[string]string{"name": "test", "condition": "Custom"})
				expectMetric(t, metrics[3], 2, map[string]string{"name": "test", "condition": "Unknown"})
				expectMetric(t, metrics[4], 0, map[string]string{"name": "version", "condition": "Available", "reason": "ClusterVersionNotFound"})
				expectMetric(t, metrics[5], 0, map[string]string{"name": "version", "version": "", "reason": "ClusterVersionNotFound"})
				expectMetric(t, metrics[6], 0, map[string]string{"type": "current", "version": "", "image": ""})
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
		expected []valueWithLabels
	}{
		{
			name:     "no conditional updates",
			updates:  []configv1.ConditionalUpdate{},
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
						Type:               internal.ConditionalUpdateConditionTypeRecommended,
						Status:             metav1.ConditionFalse,
						LastTransitionTime: metav1.NewTime(anchorTime),
						Reason:             "RiskApplies",
						Message:            "Risk is risky so do not risk it",
					}},
				},
			},
			expected: []valueWithLabels{{
				value:  float64(anchorTime.Unix()),
				labels: map[string]string{"version": "4.13.1", "condition": "Recommended", "status": "False", "reason": "RiskApplies"},
			}},
		},
		{
			name: "recommended true",
			updates: []configv1.ConditionalUpdate{
				{
					Release: configv1.Release{Version: "4.13.1", Image: "pullspec"},
					Risks:   []configv1.ConditionalUpdateRisk{{Name: "Risk"}},
					Conditions: []metav1.Condition{{
						Type:               internal.ConditionalUpdateConditionTypeRecommended,
						Status:             metav1.ConditionTrue,
						LastTransitionTime: metav1.NewTime(anchorTime),
						Reason:             "RiskDoesNotApply",
						Message:            "Risk is not risky so you do not risk",
					}},
				},
			},
			expected: []valueWithLabels{{
				value:  float64(anchorTime.Unix()),
				labels: map[string]string{"version": "4.13.1", "condition": "Recommended", "status": "True", "reason": "RiskDoesNotApply"},
			}},
		},
		{
			name: "recommended unknown",
			updates: []configv1.ConditionalUpdate{
				{
					Release: configv1.Release{Version: "4.13.1", Image: "pullspec"},
					Risks:   []configv1.ConditionalUpdateRisk{{Name: "Risk"}},
					Conditions: []metav1.Condition{{
						Type:               internal.ConditionalUpdateConditionTypeRecommended,
						Status:             metav1.ConditionUnknown,
						LastTransitionTime: metav1.NewTime(anchorTime),
						Reason:             "EvaluationFailed",
						Message:            "No idea sorry",
					}},
				},
			},
			expected: []valueWithLabels{{
				value:  float64(anchorTime.Unix()),
				labels: map[string]string{"version": "4.13.1", "condition": "Recommended", "status": "Unknown", "reason": "EvaluationFailed"},
			}},
		},
		{
			name: "multiple versions with different transition times",
			updates: []configv1.ConditionalUpdate{
				{
					Release: configv1.Release{Version: "4.13.1", Image: "pullspec"},
					Risks:   []configv1.ConditionalUpdateRisk{{Name: "Risk"}},
					Conditions: []metav1.Condition{{
						Type:               internal.ConditionalUpdateConditionTypeRecommended,
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
						Type:               internal.ConditionalUpdateConditionTypeRecommended,
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
						Type:               internal.ConditionalUpdateConditionTypeRecommended,
						Status:             metav1.ConditionUnknown,
						LastTransitionTime: metav1.NewTime(anchorTime.Add(time.Minute * 2)),
						Reason:             "EvaluationFailed",
						Message:            "Metrics stack was abducted by aliens",
					}},
				},
			},
			expected: []valueWithLabels{
				{
					value:  float64(anchorTime.Unix()),
					labels: map[string]string{"version": "4.13.1", "condition": "Recommended", "status": "False", "reason": "RiskApplies"},
				},
				{
					value:  float64(anchorTime.Unix() + 60),
					labels: map[string]string{"version": "4.13.2", "condition": "Recommended", "status": "True", "reason": "RiskDoesNotApply"},
				},
				{
					value:  float64(anchorTime.Unix() + 120),
					labels: map[string]string{"version": "4.13.3", "condition": "Recommended", "status": "Unknown", "reason": "EvaluationFailed"},
				},
			},
		},
	}

	for _, tc := range testCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			optr := &Operator{}
			m := newOperatorMetrics(optr)
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

func Test_collectConditionalUpdateRisks(t *testing.T) {
	type valueWithLabels struct {
		value  float64
		labels map[string]string
	}
	testCases := []struct {
		name     string
		risks    []configv1.ConditionalUpdateRisk
		expected []valueWithLabels
	}{
		{
			name:     "no conditional updates",
			expected: []valueWithLabels{},
		},
		{
			name: "unknown type",
			risks: []configv1.ConditionalUpdateRisk{
				{
					Name: "RiskX",
					Conditions: []metav1.Condition{{
						Type:    internal.ConditionalUpdateConditionTypeRecommended,
						Status:  metav1.ConditionFalse,
						Reason:  "ReasonA",
						Message: "Risk does not apply",
					}},
				},
			},
		},
		{
			name: "apply false",
			risks: []configv1.ConditionalUpdateRisk{
				{
					Name: "RiskX",
					Conditions: []metav1.Condition{{
						Type:    internal.ConditionalUpdateRiskConditionTypeApplies,
						Status:  metav1.ConditionFalse,
						Reason:  "ReasonA",
						Message: "Risk does not apply",
					}},
				},
			},
			expected: []valueWithLabels{{
				labels: map[string]string{"condition": "Applies", "risk": "RiskX", "reason": "ReasonA"},
			}},
		},
		{
			name: "apply true",
			risks: []configv1.ConditionalUpdateRisk{
				{
					Name: "RiskX",
					Conditions: []metav1.Condition{{
						Type:    internal.ConditionalUpdateRiskConditionTypeApplies,
						Status:  metav1.ConditionTrue,
						Reason:  "ReasonA",
						Message: "Risk does not apply",
					}},
				},
			},
			expected: []valueWithLabels{{
				value:  1,
				labels: map[string]string{"condition": "Applies", "risk": "RiskX", "reason": "ReasonA"},
			}},
		},
		{
			name: "apply unknown",
			risks: []configv1.ConditionalUpdateRisk{
				{
					Name: "RiskX",
					Conditions: []metav1.Condition{{
						Type:    internal.ConditionalUpdateRiskConditionTypeApplies,
						Status:  metav1.ConditionUnknown,
						Reason:  "ReasonA",
						Message: "Risk does not apply",
					}},
				},
			},
			expected: []valueWithLabels{{
				value:  -1,
				labels: map[string]string{"condition": "Applies", "risk": "RiskX", "reason": "ReasonA"},
			}},
		},
	}

	for _, tc := range testCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			optr := &Operator{}
			m := newOperatorMetrics(optr)
			ch := make(chan prometheus.Metric)

			go func() {
				m.collectConditionalUpdateRisks(ch, tc.risks)
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

func Test_authHandler(t *testing.T) {
	// Setup test certificates
	caPEM, prometheusCert, unauthorizedCert, untrustedCert := setupTestCerts(t)
	tests := []struct {
		name           string
		enableAuthn    bool
		enableAuthz    bool
		clientCN       string
		provideCert    bool
		provideCA      bool
		caPEM          []byte
		wantStatusCode int
		wantBodyMatch  string
	}{
		// No security - authn and authz disabled
		{
			name:           "disabled: both authn and authz disabled - client cert not provided",
			enableAuthn:    false,
			enableAuthz:    false,
			provideCert:    false,
			wantStatusCode: http.StatusOK,
			wantBodyMatch:  "ok",
		},
		{
			name:           "disabled: both authn and authz disabled - client cert provided",
			enableAuthn:    false,
			enableAuthz:    false,
			provideCert:    true,
			wantStatusCode: http.StatusOK,
			wantBodyMatch:  "ok",
		},

		// Authentication only (authz disabled)
		{
			name:           "authn: trusted cert",
			enableAuthn:    true,
			enableAuthz:    false,
			clientCN:       "system:serviceaccount:default:unauthorized",
			provideCert:    true,
			provideCA:      true,
			caPEM:          caPEM,
			wantStatusCode: http.StatusOK,
			wantBodyMatch:  "ok",
		},
		{
			name:           "authn: untrusted cert",
			enableAuthn:    true,
			enableAuthz:    false,
			provideCert:    true,
			provideCA:      true,
			caPEM:          caPEM,
			wantStatusCode: http.StatusUnauthorized,
			wantBodyMatch:  "client certificate not trusted\n",
		},
		{
			name:           "authn: no cert provided",
			enableAuthn:    true,
			enableAuthz:    false,
			provideCA:      true,
			caPEM:          caPEM,
			wantStatusCode: http.StatusUnauthorized,
			wantBodyMatch:  "client certificate required\n",
		},
		{
			name:           "authn: CA bundle returns nil",
			enableAuthn:    true,
			enableAuthz:    false,
			provideCert:    true,
			provideCA:      true,
			caPEM:          nil,
			wantStatusCode: http.StatusInternalServerError,
			wantBodyMatch:  "internal server error\n",
		},
		{
			name:           "authn: invalid CA (empty)",
			enableAuthn:    true,
			enableAuthz:    false,
			provideCert:    true,
			provideCA:      true,
			caPEM:          []byte{},
			wantStatusCode: http.StatusInternalServerError,
			wantBodyMatch:  "internal server error\n",
		},

		// Authorization only (authn disabled)
		{
			name:           "authz: allowed CN",
			enableAuthn:    false,
			enableAuthz:    true,
			clientCN:       "system:serviceaccount:openshift-monitoring:prometheus-k8s",
			provideCert:    true,
			wantStatusCode: http.StatusOK,
			wantBodyMatch:  "ok",
		},
		{
			name:           "authz: unauthorized CN",
			enableAuthn:    false,
			enableAuthz:    true,
			clientCN:       "system:serviceaccount:default:unauthorized",
			provideCert:    true,
			wantStatusCode: http.StatusForbidden,
			wantBodyMatch:  "unauthorized common name: system:serviceaccount:default:unauthorized\n",
		},
		{
			name:           "authz: no cert provided",
			enableAuthn:    false,
			enableAuthz:    true,
			provideCert:    false,
			wantStatusCode: http.StatusUnauthorized,
			wantBodyMatch:  "client certificate required\n",
		},

		// Both authentication and authorization enabled
		{
			name:           "both: trusted cert and authorized CN",
			enableAuthn:    true,
			enableAuthz:    true,
			clientCN:       "system:serviceaccount:openshift-monitoring:prometheus-k8s",
			provideCert:    true,
			provideCA:      true,
			caPEM:          caPEM,
			wantStatusCode: http.StatusOK,
			wantBodyMatch:  "ok",
		},
		{
			name:           "both: trusted cert but unauthorized CN",
			enableAuthn:    true,
			enableAuthz:    true,
			clientCN:       "system:serviceaccount:default:unauthorized",
			provideCert:    true,
			provideCA:      true,
			caPEM:          caPEM,
			wantStatusCode: http.StatusForbidden,
			wantBodyMatch:  "unauthorized common name: system:serviceaccount:default:unauthorized\n",
		},
		{
			name:           "both: cert not trusted by CA (authentication fails)",
			enableAuthn:    true,
			enableAuthz:    true,
			clientCN:       "untrusted",
			provideCert:    true,
			provideCA:      true,
			caPEM:          caPEM,
			wantStatusCode: http.StatusUnauthorized,
			wantBodyMatch:  "client certificate not trusted\n",
		},
		{
			name:           "both: no cert provided",
			enableAuthn:    true,
			enableAuthz:    true,
			provideCert:    false,
			provideCA:      true,
			caPEM:          caPEM,
			wantStatusCode: http.StatusUnauthorized,
			wantBodyMatch:  "client certificate required\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var caProvider dynamiccertificates.CAContentProvider
			if tt.provideCA {
				caProvider = &mockCAProvider{caPEM: tt.caPEM}
			}

			handler := &authHandler{
				downstream: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					_, _ = fmt.Fprintf(w, "ok")
				}),
				clientCA:             caProvider,
				enableAuthentication: tt.enableAuthn,
				enableAuthorization:  tt.enableAuthz,
			}

			req := httptest.NewRequest("GET", "/metrics", nil)
			if tt.provideCert {
				var cert *x509.Certificate
				switch tt.clientCN {
				case "system:serviceaccount:openshift-monitoring:prometheus-k8s":
					cert = prometheusCert
				case "system:serviceaccount:default:unauthorized":
					cert = unauthorizedCert
				default:
					cert = untrustedCert
				}
				req.TLS = &tls.ConnectionState{PeerCertificates: []*x509.Certificate{cert}}
			}

			rr := httptest.NewRecorder()
			handler.ServeHTTP(rr, req)

			if rr.Code != tt.wantStatusCode {
				t.Errorf("status code: got %d, want %d", rr.Code, tt.wantStatusCode)
			}
			if rr.Body.String() != tt.wantBodyMatch {
				t.Errorf("body: got %q, want to contain %q", rr.Body.String(), tt.wantBodyMatch)
			}
		})
	}
}

// setupTestCerts creates a test CA and multiple client certificates for testing
func setupTestCerts(t *testing.T) (caPEM []byte, prometheusCert, unauthorizedCert, untrustedCert *x509.Certificate) {
	t.Helper()

	caConfig, err := crypto.MakeSelfSignedCAConfig("test-ca", time.Hour)
	if err != nil {
		t.Fatalf("failed to create CA: %v", err)
	}

	ca := &crypto.CA{
		SerialGenerator: &crypto.RandomSerialGenerator{},
		Config:          caConfig,
	}

	caPEM, _, err = caConfig.GetPEMBytes()
	if err != nil {
		t.Fatalf("failed to get CA PEM: %v", err)
	}

	// Generate cert with prometheus CN for authorization tests
	prometheusConfig, err := ca.MakeClientCertificateForDuration(
		&user.DefaultInfo{Name: "system:serviceaccount:openshift-monitoring:prometheus-k8s"},
		time.Hour,
	)
	if err != nil {
		t.Fatalf("failed to create prometheus cert: %v", err)
	}
	prometheusCert = prometheusConfig.Certs[0]

	// Generate cert with unauthorized CN
	unauthorizedConfig, err := ca.MakeClientCertificateForDuration(
		&user.DefaultInfo{Name: "system:serviceaccount:default:unauthorized"},
		time.Hour,
	)
	if err != nil {
		t.Fatalf("failed to create unauthorized cert: %v", err)
	}
	unauthorizedCert = unauthorizedConfig.Certs[0]

	// Generate cert from a different CA (untrusted) for authentication failure tests
	untrustedCAConfig, err := crypto.MakeSelfSignedCAConfig("untrusted-ca", time.Hour)
	if err != nil {
		t.Fatalf("failed to create untrusted CA: %v", err)
	}

	untrustedCA := &crypto.CA{
		SerialGenerator: &crypto.RandomSerialGenerator{},
		Config:          untrustedCAConfig,
	}

	untrustedConfig, err := untrustedCA.MakeClientCertificateForDuration(
		&user.DefaultInfo{Name: "system:serviceaccount:openshift-monitoring:prometheus-k8s"},
		time.Hour,
	)
	if err != nil {
		t.Fatalf("failed to create untrusted cert: %v", err)
	}
	untrustedCert = untrustedConfig.Certs[0]

	return caPEM, prometheusCert, unauthorizedCert, untrustedCert
}

// mockCAProvider implements CAContentProvider for testing
type mockCAProvider struct {
	caPEM []byte
}

func (m *mockCAProvider) Name() string {
	return "mock-ca-provider"
}

func (m *mockCAProvider) CurrentCABundleContent() []byte {
	return m.caPEM
}

func (m *mockCAProvider) VerifyOptions() (x509.VerifyOptions, bool) {
	if len(m.caPEM) == 0 {
		return x509.VerifyOptions{}, false
	}

	certPool := x509.NewCertPool()
	if !certPool.AppendCertsFromPEM(m.caPEM) {
		return x509.VerifyOptions{}, false
	}

	return x509.VerifyOptions{
		Roots:     certPool,
		KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
	}, true
}

func (m *mockCAProvider) AddListener(_ dynamiccertificates.Listener) {
}

// Test_applyTLSProfile tests the central TLS profile application from APIServer resource
func Test_applyTLSProfile(t *testing.T) {
	tests := []struct {
		name               string
		apiServer          *configv1.APIServer
		expectError        bool
		expectMinVersion   uint16
		expectCipherSuites bool
	}{
		{
			name: "applies intermediate TLS profile",
			apiServer: &configv1.APIServer{
				ObjectMeta: metav1.ObjectMeta{
					Name:       "cluster",
					Generation: 1,
				},
				Spec: configv1.APIServerSpec{
					TLSAdherence: configv1.TLSAdherencePolicyStrictAllComponents,
					TLSSecurityProfile: &configv1.TLSSecurityProfile{
						Type:         configv1.TLSProfileIntermediateType,
						Intermediate: &configv1.IntermediateTLSProfile{},
					},
				},
			},
			expectError:        false,
			expectMinVersion:   tls.VersionTLS12,
			expectCipherSuites: true,
		},
		{
			name: "applies modern TLS profile",
			apiServer: &configv1.APIServer{
				ObjectMeta: metav1.ObjectMeta{
					Name:       "cluster",
					Generation: 2,
				},
				Spec: configv1.APIServerSpec{
					TLSAdherence: configv1.TLSAdherencePolicyStrictAllComponents,
					TLSSecurityProfile: &configv1.TLSSecurityProfile{
						Type:   configv1.TLSProfileModernType,
						Modern: &configv1.ModernTLSProfile{},
					},
				},
			},
			expectError:        false,
			expectMinVersion:   tls.VersionTLS13,
			expectCipherSuites: true,
		},
		{
			name: "applies old TLS profile",
			apiServer: &configv1.APIServer{
				ObjectMeta: metav1.ObjectMeta{
					Name:       "cluster",
					Generation: 3,
				},
				Spec: configv1.APIServerSpec{
					TLSAdherence: configv1.TLSAdherencePolicyStrictAllComponents,
					TLSSecurityProfile: &configv1.TLSSecurityProfile{
						Type: configv1.TLSProfileOldType,
						Old:  &configv1.OldTLSProfile{},
					},
				},
			},
			expectError:        false,
			expectMinVersion:   tls.VersionTLS10,
			expectCipherSuites: true,
		},
		{
			name: "applies custom TLS profile with TLS 1.2",
			apiServer: &configv1.APIServer{
				ObjectMeta: metav1.ObjectMeta{
					Name:       "cluster",
					Generation: 4,
				},
				Spec: configv1.APIServerSpec{
					TLSAdherence: configv1.TLSAdherencePolicyStrictAllComponents,
					TLSSecurityProfile: &configv1.TLSSecurityProfile{
						Type: configv1.TLSProfileCustomType,
						Custom: &configv1.CustomTLSProfile{
							TLSProfileSpec: configv1.TLSProfileSpec{
								Ciphers:       []string{"TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256"},
								MinTLSVersion: configv1.VersionTLS12,
							},
						},
					},
				},
			},
			expectError:        false,
			expectMinVersion:   tls.VersionTLS12,
			expectCipherSuites: true,
		},
		{
			name:               "fallback to TLS 1.2 when APIServer not found",
			apiServer:          nil,
			expectError:        false,
			expectMinVersion:   tls.VersionTLS12,
			expectCipherSuites: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			lister := &mockAPIServerLister{apiServer: tt.apiServer}
			mgr, err := newTLSProfileManager(lister, nil) // No overrides

			if tt.expectError {
				if err == nil {
					t.Fatal("expected error but got none")
				}
				return
			}

			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if mgr == nil {
				t.Fatal("expected non-nil manager")
			}

			// Verify applySettings produces correct config
			config := &tls.Config{}
			mgr.applySettings(config)

			if config.MinVersion != tt.expectMinVersion {
				t.Errorf("expected MinVersion=%d, got %d", tt.expectMinVersion, config.MinVersion)
			}

			if tt.expectCipherSuites && len(config.CipherSuites) == 0 {
				t.Error("expected cipher suites to be set but got empty")
			}
		})
	}
}

// Test_applyTLSOptions tests the override flag behavior
func Test_applyTLSOptions(t *testing.T) {
	tests := []struct {
		name               string
		options            MetricsOptions
		expectValidateErr  bool
		expectMinVersion   uint16
		expectCipherSuites int
	}{
		{
			name: "no overrides - fallback to TLS 1.2",
			options: MetricsOptions{
				TLSMinVersionOverride:   "",
				TLSCipherSuitesOverride: nil,
			},
			expectValidateErr:  false,
			expectMinVersion:   tls.VersionTLS12, // fallback
			expectCipherSuites: 0,
		},
		{
			name: "min version override only",
			options: MetricsOptions{
				TLSMinVersionOverride:   "VersionTLS12",
				TLSCipherSuitesOverride: nil,
			},
			expectValidateErr:  false,
			expectMinVersion:   tls.VersionTLS12,
			expectCipherSuites: 0,
		},
		{
			name: "cipher suites override only",
			options: MetricsOptions{
				TLSMinVersionOverride:   "",
				TLSCipherSuitesOverride: []string{"TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256"},
			},
			expectValidateErr:  false,
			expectMinVersion:   tls.VersionTLS12, // fallback
			expectCipherSuites: 1,
		},
		{
			name: "both overrides",
			options: MetricsOptions{
				TLSMinVersionOverride:   "VersionTLS13",
				TLSCipherSuitesOverride: []string{"TLS_AES_128_GCM_SHA256", "TLS_AES_256_GCM_SHA384"},
			},
			expectValidateErr:  false,
			expectMinVersion:   tls.VersionTLS13,
			expectCipherSuites: 2,
		},
		{
			name: "invalid min version",
			options: MetricsOptions{
				TLSMinVersionOverride:   "InvalidVersion",
				TLSCipherSuitesOverride: nil,
			},
			expectValidateErr: true,
		},
		{
			name: "invalid cipher suite",
			options: MetricsOptions{
				TLSMinVersionOverride:   "",
				TLSCipherSuitesOverride: []string{"INVALID_CIPHER"},
			},
			expectValidateErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			validated, err := validateTLSOverrides(tt.options)

			if tt.expectValidateErr {
				if err == nil {
					t.Fatal("expected validation error but got none")
				}
				return
			}

			if err != nil {
				t.Fatalf("unexpected validation error: %v", err)
			}

			// Test override behavior through manager (no APIServer)
			lister := &mockAPIServerLister{apiServer: nil}
			mgr, err := newTLSProfileManager(lister, validated)
			if err != nil {
				t.Fatalf("unexpected manager creation error: %v", err)
			}

			// Verify behavior through applySettings
			config := &tls.Config{}
			mgr.applySettings(config)

			if tt.expectMinVersion > 0 && config.MinVersion != tt.expectMinVersion {
				t.Errorf("expected MinVersion %d, got %d", tt.expectMinVersion, config.MinVersion)
			}

			if tt.expectCipherSuites > 0 && len(config.CipherSuites) != tt.expectCipherSuites {
				t.Errorf("expected %d cipher suites, got %d", tt.expectCipherSuites, len(config.CipherSuites))
			}
		})
	}
}

// Test_tlsProfileOverridePrecedence tests the interaction between central profile and overrides
func Test_tlsProfileOverridePrecedence(t *testing.T) {
	// Setup APIServer with Modern profile (TLS 1.3)
	apiServer := &configv1.APIServer{
		ObjectMeta: metav1.ObjectMeta{
			Name:       "cluster",
			Generation: 1,
		},
		Spec: configv1.APIServerSpec{
			TLSAdherence: configv1.TLSAdherencePolicyStrictAllComponents,
			TLSSecurityProfile: &configv1.TLSSecurityProfile{
				Type:   configv1.TLSProfileModernType,
				Modern: &configv1.ModernTLSProfile{},
			},
		},
	}

	lister := &mockAPIServerLister{apiServer: apiServer}

	// Test 1: Central profile only (no overrides)
	mgr1, err := newTLSProfileManager(lister, nil)
	if err != nil {
		t.Fatalf("failed to create TLS profile manager: %v", err)
	}

	config1 := &tls.Config{}
	mgr1.applySettings(config1)
	if config1.MinVersion != tls.VersionTLS13 {
		t.Errorf("expected central profile to set TLS 1.3, got %d", config1.MinVersion)
	}

	// Test 2: Override weakens to TLS 1.2 (takes precedence)
	options := MetricsOptions{
		TLSMinVersionOverride: "VersionTLS12",
	}
	validated2, err := validateTLSOverrides(options)
	if err != nil {
		t.Fatalf("validation failed: %v", err)
	}

	mgr2, err := newTLSProfileManager(lister, validated2)
	if err != nil {
		t.Fatalf("failed to create TLS profile manager: %v", err)
	}

	config2 := &tls.Config{}
	mgr2.applySettings(config2)
	if config2.MinVersion != tls.VersionTLS12 {
		t.Errorf("expected override to set TLS 1.2, got %d", config2.MinVersion)
	}

	// Test 3: Override strengthens to TLS 1.3 (when central is lower)
	apiServerOld := &configv1.APIServer{
		ObjectMeta: metav1.ObjectMeta{
			Name:       "cluster",
			Generation: 2,
		},
		Spec: configv1.APIServerSpec{
			TLSAdherence: configv1.TLSAdherencePolicyStrictAllComponents,
			TLSSecurityProfile: &configv1.TLSSecurityProfile{
				Type: configv1.TLSProfileOldType,
				Old:  &configv1.OldTLSProfile{},
			},
		},
	}

	listerOld := &mockAPIServerLister{apiServer: apiServerOld}

	options2 := MetricsOptions{
		TLSMinVersionOverride: "VersionTLS13",
	}
	validated3, err := validateTLSOverrides(options2)
	if err != nil {
		t.Fatalf("validation failed: %v", err)
	}

	mgr3, err := newTLSProfileManager(listerOld, validated3)
	if err != nil {
		t.Fatalf("failed to create TLS profile manager: %v", err)
	}

	config3 := &tls.Config{}
	mgr3.applySettings(config3)
	if config3.MinVersion != tls.VersionTLS13 {
		t.Errorf("expected override to set TLS 1.3, got %d", config3.MinVersion)
	}
}

// Test_validateTLSOptionsOnlyOnce verifies that validation happens once and parsed values are reused
func Test_validateTLSOptionsOnlyOnce(t *testing.T) {
	options := MetricsOptions{
		TLSMinVersionOverride:   "VersionTLS12",
		TLSCipherSuitesOverride: []string{"TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256"},
	}

	// Validate once - returns immutable struct
	validated, err := validateTLSOverrides(options)
	if err != nil {
		t.Fatalf("validation failed: %v", err)
	}

	// Verify parsed values are set
	if validated.minVersion == 0 {
		t.Error("expected minVersion to be set")
	}
	if len(validated.cipherSuites) == 0 {
		t.Error("expected cipherSuites to be set")
	}

	// Create manager with validated overrides (no APIServer)
	lister := &mockAPIServerLister{apiServer: nil}
	mgr, err := newTLSProfileManager(lister, validated)
	if err != nil {
		t.Fatalf("failed to create manager: %v", err)
	}

	// Apply settings - validated values should be used from cache
	config := &tls.Config{}
	mgr.applySettings(config)

	if config.MinVersion != tls.VersionTLS12 {
		t.Errorf("expected TLS 1.2, got %d", config.MinVersion)
	}
	if len(config.CipherSuites) != 1 {
		t.Errorf("expected 1 cipher suite, got %d", len(config.CipherSuites))
	}
}

// mockAPIServerLister is a simple mock for testing
type mockAPIServerLister struct {
	apiServer *configv1.APIServer
	err       error
}

func (m *mockAPIServerLister) List(_ labels.Selector) ([]*configv1.APIServer, error) {
	if m.err != nil {
		return nil, m.err
	}
	if m.apiServer == nil {
		return []*configv1.APIServer{}, nil
	}
	return []*configv1.APIServer{m.apiServer}, nil
}

func (m *mockAPIServerLister) Get(name string) (*configv1.APIServer, error) {
	if m.err != nil {
		return nil, m.err
	}
	if m.apiServer == nil || m.apiServer.Name != name {
		return nil, apierrors.NewNotFound(schema.GroupResource{Group: configv1.GroupName, Resource: "apiservers"}, name)
	}
	return m.apiServer, nil
}

// Test_tlsProfileManager_EventHandlers tests that the TLS profile manager
// correctly responds to APIServer resource events
func Test_tlsProfileManager_EventHandlers(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Create fake client and informer
	fakeClient := configfake.NewClientset()
	informerFactory := configinformers.NewSharedInformerFactory(fakeClient, 0)
	apiServerInformer := informerFactory.Config().V1().APIServers()

	// Channel to track profile updates
	profileUpdateCh := make(chan string, 10)

	// Create initial manager with no APIServer resource
	mgr, err := newTLSProfileManager(apiServerInformer.Lister(), nil)
	if err != nil {
		t.Fatalf("failed to create TLS profile manager: %v", err)
	}

	// Helper to wait for event
	waitForEvent := func(t *testing.T, expectedEvent string) {
		t.Helper()
		select {
		case event := <-profileUpdateCh:
			if event != expectedEvent {
				t.Errorf("expected %q event, got %q", expectedEvent, event)
			}
		case <-time.After(5 * time.Second):
			t.Fatalf("timeout waiting for %q event", expectedEvent)
		}
	}

	// Helper to verify MinVersion
	verifyMinVersion := func(t *testing.T, expected uint16) {
		t.Helper()
		config := &tls.Config{}
		mgr.applySettings(config)
		if config.MinVersion != expected {
			t.Errorf("expected MinVersion %d, got %d", expected, config.MinVersion)
		}
	}

	// Add event handlers that mirror the RunMetrics implementation
	if _, err := apiServerInformer.Informer().AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: func(obj interface{}) {
			if apiServer, ok := obj.(*configv1.APIServer); ok {
				if err := mgr.updateSettings(apiServer); err != nil {
					t.Errorf("Failed to apply TLS settings on APIServer add: %v", err)
				}
				profileUpdateCh <- "add"
			}
		},
		UpdateFunc: func(oldObj, newObj interface{}) {
			if apiServer, ok := newObj.(*configv1.APIServer); ok {
				if err := mgr.updateSettings(apiServer); err != nil {
					t.Errorf("Failed to apply TLS settings on APIServer update: %v", err)
				}
				profileUpdateCh <- "update"
			}
		},
		DeleteFunc: func(obj interface{}) {
			if err := mgr.updateSettings(nil); err != nil {
				t.Errorf("Failed to apply fallback TLS settings on APIServer delete: %v", err)
			}
			profileUpdateCh <- "delete"
		},
	}); err != nil {
		t.Fatalf("failed to add APIServer event handler: %v", err)
	}

	// Start informer
	informerFactory.Start(ctx.Done())
	if !cache.WaitForCacheSync(ctx.Done(), apiServerInformer.Informer().HasSynced) {
		t.Fatal("failed to sync APIServer informer")
	}

	// Test Add event - create APIServer with Intermediate profile
	apiServer := &configv1.APIServer{
		ObjectMeta: metav1.ObjectMeta{Name: "cluster"},
		Spec: configv1.APIServerSpec{
			TLSAdherence: configv1.TLSAdherencePolicyStrictAllComponents,
			TLSSecurityProfile: &configv1.TLSSecurityProfile{
				Type:         configv1.TLSProfileIntermediateType,
				Intermediate: &configv1.IntermediateTLSProfile{},
			},
		},
	}

	if _, err := fakeClient.ConfigV1().APIServers().Create(ctx, apiServer, metav1.CreateOptions{}); err != nil {
		t.Fatalf("failed to create APIServer: %v", err)
	}

	waitForEvent(t, "add")
	verifyMinVersion(t, tls.VersionTLS12)

	// Test Update event - change to Modern profile
	apiServer, err = fakeClient.ConfigV1().APIServers().Get(ctx, "cluster", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("failed to get APIServer: %v", err)
	}

	apiServer.Spec.TLSSecurityProfile = &configv1.TLSSecurityProfile{
		Type:   configv1.TLSProfileModernType,
		Modern: &configv1.ModernTLSProfile{},
	}

	if _, err := fakeClient.ConfigV1().APIServers().Update(ctx, apiServer, metav1.UpdateOptions{}); err != nil {
		t.Fatalf("failed to update APIServer: %v", err)
	}

	waitForEvent(t, "update")
	verifyMinVersion(t, tls.VersionTLS13)

	// Test Delete event - verify fallback to defaults
	if err := fakeClient.ConfigV1().APIServers().Delete(ctx, "cluster", metav1.DeleteOptions{}); err != nil {
		t.Fatalf("failed to delete APIServer: %v", err)
	}

	waitForEvent(t, "delete")
	verifyMinVersion(t, tls.VersionTLS12) // Fallback to defaults
}

// Test_tlsProfileManager_InitializationErrorFallback tests that the manager
// falls back to safe defaults when initialization fails
func Test_tlsProfileManager_InitializationErrorFallback(t *testing.T) {
	// Create an invalid APIServer (Custom type with nil Custom field)
	invalidAPIServer := &configv1.APIServer{
		ObjectMeta: metav1.ObjectMeta{
			Name: "cluster",
		},
		Spec: configv1.APIServerSpec{
			TLSAdherence: configv1.TLSAdherencePolicyStrictAllComponents,
			TLSSecurityProfile: &configv1.TLSSecurityProfile{
				Type:   configv1.TLSProfileCustomType,
				Custom: nil, // Invalid: Custom type with nil Custom field
			},
		},
	}

	lister := &mockAPIServerLister{apiServer: invalidAPIServer}

	// Manager creation should succeed despite invalid profile (falls back to defaults)
	mgr, err := newTLSProfileManager(lister, nil)
	if err != nil {
		t.Fatalf("expected manager creation to succeed with fallback, got error: %v", err)
	}

	// Verify manager was created
	if mgr == nil {
		t.Fatal("expected non-nil manager")
	}

	// Verify it fell back to safe defaults (applyProfile should be nil)
	mgr.mu.RLock()
	profileApplied := mgr.applyProfile != nil
	mgr.mu.RUnlock()

	if profileApplied {
		t.Error("expected applyProfile to be nil (safe defaults), but it was set")
	}

	// Verify applySettings still works with safe defaults
	config := &tls.Config{}
	mgr.applySettings(config)

	// Should have at least TLS 1.2 from crypto.SecureTLSConfig
	if config.MinVersion < tls.VersionTLS12 {
		t.Errorf("expected MinVersion >= TLS 1.2 from safe defaults, got %d", config.MinVersion)
	}
}

// Test_tlsProfileManager_ErrorRecovery tests that the manager retains the old
// profile when updateSettings fails
func Test_tlsProfileManager_ErrorRecovery(t *testing.T) {
	// Create manager with valid Intermediate profile
	intermediateAPIServer := &configv1.APIServer{
		ObjectMeta: metav1.ObjectMeta{
			Name: "cluster",
		},
		Spec: configv1.APIServerSpec{
			TLSAdherence: configv1.TLSAdherencePolicyStrictAllComponents,
			TLSSecurityProfile: &configv1.TLSSecurityProfile{
				Type:         configv1.TLSProfileIntermediateType,
				Intermediate: &configv1.IntermediateTLSProfile{},
			},
		},
	}

	lister := &mockAPIServerLister{apiServer: intermediateAPIServer}
	mgr, err := newTLSProfileManager(lister, nil)
	if err != nil {
		t.Fatalf("failed to create TLS profile manager: %v", err)
	}

	// Verify initial profile (TLS 1.2 for Intermediate)
	config := &tls.Config{}
	mgr.applySettings(config)
	initialMinVersion := config.MinVersion
	if initialMinVersion != tls.VersionTLS12 {
		t.Errorf("expected initial MinVersion TLS 1.2, got %d", initialMinVersion)
	}

	// Attempt to update with invalid profile (Custom type with nil Custom field)
	invalidAPIServer := &configv1.APIServer{
		ObjectMeta: metav1.ObjectMeta{
			Name: "cluster",
		},
		Spec: configv1.APIServerSpec{
			TLSAdherence: configv1.TLSAdherencePolicyStrictAllComponents,
			TLSSecurityProfile: &configv1.TLSSecurityProfile{
				Type:   configv1.TLSProfileCustomType,
				Custom: nil, // Invalid: Custom type with nil Custom field
			},
		},
	}

	// This should return an error
	err = mgr.updateSettings(invalidAPIServer)
	if err == nil {
		t.Error("expected error from updateSettings with Custom type but nil Custom field, got nil")
	}

	// Verify old profile is retained
	config = &tls.Config{}
	mgr.applySettings(config)
	if config.MinVersion != initialMinVersion {
		t.Errorf("expected old profile retained (TLS 1.2), got %d", config.MinVersion)
	}

	// Now update with valid Modern profile
	modernAPIServer := &configv1.APIServer{
		ObjectMeta: metav1.ObjectMeta{
			Name: "cluster",
		},
		Spec: configv1.APIServerSpec{
			TLSAdherence: configv1.TLSAdherencePolicyStrictAllComponents,
			TLSSecurityProfile: &configv1.TLSSecurityProfile{
				Type:   configv1.TLSProfileModernType,
				Modern: &configv1.ModernTLSProfile{},
			},
		},
	}

	err = mgr.updateSettings(modernAPIServer)
	if err != nil {
		t.Errorf("unexpected error updating to Modern profile: %v", err)
	}

	// Verify profile updated to TLS 1.3
	config = &tls.Config{}
	mgr.applySettings(config)
	if config.MinVersion != tls.VersionTLS13 {
		t.Errorf("expected MinVersion TLS 1.3 after recovery, got %d", config.MinVersion)
	}
}

// Test_tlsProfileManager_TLSAdherenceVariations tests different TLSAdherence
// policy values
func Test_tlsProfileManager_TLSAdherenceVariations(t *testing.T) {
	tests := []struct {
		name          string
		adherence     configv1.TLSAdherencePolicy
		expectApplied bool // Whether central profile should be applied
	}{
		{
			name:          "StrictAllComponents applies profile",
			adherence:     configv1.TLSAdherencePolicyStrictAllComponents,
			expectApplied: true,
		},
		{
			name:          "LegacyAdheringComponentsOnly does not apply profile",
			adherence:     configv1.TLSAdherencePolicyLegacyAdheringComponentsOnly,
			expectApplied: false,
		},
		{
			name:          "NoOpinion does not apply profile",
			adherence:     configv1.TLSAdherencePolicyNoOpinion,
			expectApplied: false,
		},
		{
			name:          "empty adherence does not apply profile",
			adherence:     "",
			expectApplied: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			apiServer := &configv1.APIServer{
				ObjectMeta: metav1.ObjectMeta{
					Name: "cluster",
				},
				Spec: configv1.APIServerSpec{
					TLSAdherence: tt.adherence,
					TLSSecurityProfile: &configv1.TLSSecurityProfile{
						Type:         configv1.TLSProfileIntermediateType,
						Intermediate: &configv1.IntermediateTLSProfile{},
					},
				},
			}

			lister := &mockAPIServerLister{apiServer: apiServer}
			mgr, err := newTLSProfileManager(lister, nil)
			if err != nil {
				t.Fatalf("failed to create TLS profile manager: %v", err)
			}

			// Check if profile is applied
			mgr.mu.RLock()
			profileApplied := mgr.applyProfile != nil
			mgr.mu.RUnlock()

			if profileApplied != tt.expectApplied {
				t.Errorf("expected profile applied=%v, got %v", tt.expectApplied, profileApplied)
			}

			// Verify behavior through applySettings
			config := &tls.Config{}
			mgr.applySettings(config)

			// All should have at least TLS 1.2 from crypto.SecureTLSConfig
			if config.MinVersion < tls.VersionTLS12 {
				t.Errorf("expected MinVersion >= TLS 1.2, got %d", config.MinVersion)
			}
		})
	}
}

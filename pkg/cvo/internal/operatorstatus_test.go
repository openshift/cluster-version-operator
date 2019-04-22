package internal

import (
	"context"
	"fmt"
	"reflect"
	"testing"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/diff"
	clientgotesting "k8s.io/client-go/testing"
	"k8s.io/client-go/tools/record"

	configv1 "github.com/openshift/api/config/v1"
	"github.com/openshift/client-go/config/clientset/versioned/fake"
	"github.com/openshift/cluster-version-operator/lib/resourcebuilder"
	"github.com/openshift/cluster-version-operator/pkg/payload"
)

func Test_waitForOperatorStatusToBeDone(t *testing.T) {
	tests := []struct {
		name   string
		actual *configv1.ClusterOperator
		mode   resourcebuilder.Mode
		exp    *configv1.ClusterOperator
		expErr error
	}{{
		name: "cluster operator not found",
		actual: &configv1.ClusterOperator{
			ObjectMeta: metav1.ObjectMeta{Name: "dummy"},
		},
		exp: &configv1.ClusterOperator{
			ObjectMeta: metav1.ObjectMeta{Name: "test-co"},
			Status: configv1.ClusterOperatorStatus{
				Versions: []configv1.OperandVersion{{
					Name: "operator", Version: "v1",
				}, {
					Name: "operand-1", Version: "v1",
				}},
			},
		},
		expErr: &payload.UpdateError{
			Nested:  apierrors.NewNotFound(schema.GroupResource{"", "clusteroperator"}, "test-co"),
			Reason:  "ClusterOperatorNotAvailable",
			Message: "Cluster operator test-co has not yet reported success",
			Name:    "test-co",
		},
	}, {
		name: "cluster operator reporting no versions with no operands",
		actual: &configv1.ClusterOperator{
			ObjectMeta: metav1.ObjectMeta{Name: "test-co"},
		},
		exp: &configv1.ClusterOperator{
			ObjectMeta: metav1.ObjectMeta{Name: "test-co"},
			Status: configv1.ClusterOperatorStatus{
				Versions: []configv1.OperandVersion{{
					Name: "operator", Version: "v1",
				}},
			},
		},
		expErr: &payload.UpdateError{
			Nested:  fmt.Errorf("cluster operator test-co is still updating"),
			Reason:  "ClusterOperatorNotAvailable",
			Message: "Cluster operator test-co is still updating",
			Name:    "test-co",
		},
	}, {
		name: "cluster operator reporting no versions",
		actual: &configv1.ClusterOperator{
			ObjectMeta: metav1.ObjectMeta{Name: "test-co"},
		},
		exp: &configv1.ClusterOperator{
			ObjectMeta: metav1.ObjectMeta{Name: "test-co"},
			Status: configv1.ClusterOperatorStatus{
				Versions: []configv1.OperandVersion{{
					Name: "operator", Version: "v1",
				}, {
					Name: "operand-1", Version: "v1",
				}},
			},
		},
		expErr: &payload.UpdateError{
			Nested:  fmt.Errorf("cluster operator test-co is still updating: missing version information for operand-1"),
			Reason:  "ClusterOperatorNotAvailable",
			Message: "Cluster operator test-co is still updating: missing version information for operand-1",
			Name:    "test-co",
		},
	}, {
		name: "cluster operator reporting no versions for operand",
		actual: &configv1.ClusterOperator{
			ObjectMeta: metav1.ObjectMeta{Name: "test-co"},
			Status: configv1.ClusterOperatorStatus{
				Versions: []configv1.OperandVersion{{
					Name: "operator", Version: "v0",
				}},
			},
		},
		exp: &configv1.ClusterOperator{
			ObjectMeta: metav1.ObjectMeta{Name: "test-co"},
			Status: configv1.ClusterOperatorStatus{
				Versions: []configv1.OperandVersion{{
					Name: "operator", Version: "v1",
				}, {
					Name: "operand-1", Version: "v1",
				}},
			},
		},
		expErr: &payload.UpdateError{
			Nested:  fmt.Errorf("cluster operator test-co is still updating: missing version information for operand-1"),
			Reason:  "ClusterOperatorNotAvailable",
			Message: "Cluster operator test-co is still updating: missing version information for operand-1",
			Name:    "test-co",
		},
	}, {
		name: "cluster operator reporting old versions",
		actual: &configv1.ClusterOperator{
			ObjectMeta: metav1.ObjectMeta{Name: "test-co"},
			Status: configv1.ClusterOperatorStatus{
				Versions: []configv1.OperandVersion{{
					Name: "operator", Version: "v0",
				}, {
					Name: "operand-1", Version: "v0",
				}},
			},
		},
		exp: &configv1.ClusterOperator{
			ObjectMeta: metav1.ObjectMeta{Name: "test-co"},
			Status: configv1.ClusterOperatorStatus{
				Versions: []configv1.OperandVersion{{
					Name: "operator", Version: "v1",
				}, {
					Name: "operand-1", Version: "v1",
				}},
			},
		},
		expErr: &payload.UpdateError{
			Nested:  fmt.Errorf("cluster operator test-co is still updating: upgrading operand-1 from v0 to v1"),
			Reason:  "ClusterOperatorNotAvailable",
			Message: "Cluster operator test-co is still updating: upgrading operand-1 from v0 to v1",
			Name:    "test-co",
		},
	}, {
		name: "cluster operator reporting mix of desired and old versions",
		actual: &configv1.ClusterOperator{
			ObjectMeta: metav1.ObjectMeta{Name: "test-co"},
			Status: configv1.ClusterOperatorStatus{
				Versions: []configv1.OperandVersion{{
					Name: "operator", Version: "v1",
				}, {
					Name: "operand-1", Version: "v0",
				}},
			},
		},
		exp: &configv1.ClusterOperator{
			ObjectMeta: metav1.ObjectMeta{Name: "test-co"},
			Status: configv1.ClusterOperatorStatus{
				Versions: []configv1.OperandVersion{{
					Name: "operator", Version: "v1",
				}, {
					Name: "operand-1", Version: "v1",
				}},
			},
		},
		expErr: &payload.UpdateError{
			Nested:  fmt.Errorf("cluster operator test-co is still updating: upgrading operand-1 from v0 to v1"),
			Reason:  "ClusterOperatorNotAvailable",
			Message: "Cluster operator test-co is still updating: upgrading operand-1 from v0 to v1",
			Name:    "test-co",
		},
	}, {
		name: "cluster operator reporting desired operator and old versions for 2 operands",
		actual: &configv1.ClusterOperator{
			ObjectMeta: metav1.ObjectMeta{Name: "test-co"},
			Status: configv1.ClusterOperatorStatus{
				Versions: []configv1.OperandVersion{{
					Name: "operator", Version: "v1",
				}, {
					Name: "operand-1", Version: "v0",
				}, {
					Name: "operand-2", Version: "v0",
				}},
			},
		},
		exp: &configv1.ClusterOperator{
			ObjectMeta: metav1.ObjectMeta{Name: "test-co"},
			Status: configv1.ClusterOperatorStatus{
				Versions: []configv1.OperandVersion{{
					Name: "operator", Version: "v1",
				}, {
					Name: "operand-1", Version: "v1",
				}, {
					Name: "operand-2", Version: "v1",
				}},
			},
		},
		expErr: &payload.UpdateError{
			Nested:  fmt.Errorf("cluster operator test-co is still updating: upgrading operand-1 from v0 to v1, upgrading operand-2 from v0 to v1"),
			Reason:  "ClusterOperatorNotAvailable",
			Message: "Cluster operator test-co is still updating: upgrading operand-1 from v0 to v1, upgrading operand-2 from v0 to v1",
			Name:    "test-co",
		},
	}, {
		name: "cluster operator reporting desired operator and mix of old and desired versions for 2 operands",
		actual: &configv1.ClusterOperator{
			ObjectMeta: metav1.ObjectMeta{Name: "test-co"},
			Status: configv1.ClusterOperatorStatus{
				Versions: []configv1.OperandVersion{{
					Name: "operator", Version: "v1",
				}, {
					Name: "operand-1", Version: "v1",
				}, {
					Name: "operand-2", Version: "v0",
				}},
			},
		},
		exp: &configv1.ClusterOperator{
			ObjectMeta: metav1.ObjectMeta{Name: "test-co"},
			Status: configv1.ClusterOperatorStatus{
				Versions: []configv1.OperandVersion{{
					Name: "operator", Version: "v1",
				}, {
					Name: "operand-1", Version: "v1",
				}, {
					Name: "operand-2", Version: "v1",
				}},
			},
		},
		expErr: &payload.UpdateError{
			Nested:  fmt.Errorf("cluster operator test-co is still updating: upgrading operand-2 from v0 to v1"),
			Reason:  "ClusterOperatorNotAvailable",
			Message: "Cluster operator test-co is still updating: upgrading operand-2 from v0 to v1",
			Name:    "test-co",
		},
	}, {
		name: "cluster operator reporting desired versions and no conditions",
		actual: &configv1.ClusterOperator{
			ObjectMeta: metav1.ObjectMeta{Name: "test-co"},
			Status: configv1.ClusterOperatorStatus{
				Versions: []configv1.OperandVersion{{
					Name: "operator", Version: "v1",
				}, {
					Name: "operand-1", Version: "v1",
				}},
			},
		},
		exp: &configv1.ClusterOperator{
			ObjectMeta: metav1.ObjectMeta{Name: "test-co"},
			Status: configv1.ClusterOperatorStatus{
				Versions: []configv1.OperandVersion{{
					Name: "operator", Version: "v1",
				}, {
					Name: "operand-1", Version: "v1",
				}},
			},
		},
		expErr: &payload.UpdateError{
			Nested:  fmt.Errorf("cluster operator test-co is not done; it is available=false, progressing=true, degraded=true"),
			Reason:  "ClusterOperatorNotAvailable",
			Message: "Cluster operator test-co has not yet reported success",
			Name:    "test-co",
		},
	}, {
		name: "cluster operator reporting progressing=true",
		actual: &configv1.ClusterOperator{
			ObjectMeta: metav1.ObjectMeta{Name: "test-co"},
			Status: configv1.ClusterOperatorStatus{
				Versions: []configv1.OperandVersion{{
					Name: "operator", Version: "v1",
				}, {
					Name: "operand-1", Version: "v1",
				}},
				Conditions: []configv1.ClusterOperatorStatusCondition{{Type: configv1.OperatorProgressing, Status: configv1.ConditionTrue}},
			},
		},
		exp: &configv1.ClusterOperator{
			ObjectMeta: metav1.ObjectMeta{Name: "test-co"},
			Status: configv1.ClusterOperatorStatus{
				Versions: []configv1.OperandVersion{{
					Name: "operator", Version: "v1",
				}, {
					Name: "operand-1", Version: "v1",
				}},
			},
		},
		expErr: &payload.UpdateError{
			Nested:  fmt.Errorf("cluster operator test-co is not done; it is available=false, progressing=true, degraded=true"),
			Reason:  "ClusterOperatorNotAvailable",
			Message: "Cluster operator test-co has not yet reported success",
			Name:    "test-co",
		},
	}, {
		name: "cluster operator reporting degraded=true",
		actual: &configv1.ClusterOperator{
			ObjectMeta: metav1.ObjectMeta{Name: "test-co"},
			Status: configv1.ClusterOperatorStatus{
				Versions: []configv1.OperandVersion{{
					Name: "operator", Version: "v1",
				}, {
					Name: "operand-1", Version: "v1",
				}},
				Conditions: []configv1.ClusterOperatorStatusCondition{{Type: configv1.OperatorDegraded, Status: configv1.ConditionTrue, Message: "random error"}},
			},
		},
		exp: &configv1.ClusterOperator{
			ObjectMeta: metav1.ObjectMeta{Name: "test-co"},
			Status: configv1.ClusterOperatorStatus{
				Versions: []configv1.OperandVersion{{
					Name: "operator", Version: "v1",
				}, {
					Name: "operand-1", Version: "v1",
				}},
			},
		},
		expErr: &payload.UpdateError{
			Nested:  fmt.Errorf("cluster operator test-co is reporting a failure: random error"),
			Reason:  "ClusterOperatorDegraded",
			Message: "Cluster operator test-co is reporting a failure: random error",
			Name:    "test-co",
		},
	}, {
		name: "cluster operator reporting available=true progressing=true",
		actual: &configv1.ClusterOperator{
			ObjectMeta: metav1.ObjectMeta{Name: "test-co"},
			Status: configv1.ClusterOperatorStatus{
				Versions: []configv1.OperandVersion{{
					Name: "operator", Version: "v1",
				}, {
					Name: "operand-1", Version: "v1",
				}},
				Conditions: []configv1.ClusterOperatorStatusCondition{{Type: configv1.OperatorAvailable, Status: configv1.ConditionTrue}, {Type: configv1.OperatorProgressing, Status: configv1.ConditionTrue}},
			},
		},
		exp: &configv1.ClusterOperator{
			ObjectMeta: metav1.ObjectMeta{Name: "test-co"},
			Status: configv1.ClusterOperatorStatus{
				Versions: []configv1.OperandVersion{{
					Name: "operator", Version: "v1",
				}, {
					Name: "operand-1", Version: "v1",
				}},
			},
		},
		expErr: &payload.UpdateError{
			Nested:  fmt.Errorf("cluster operator test-co is not done; it is available=true, progressing=true, degraded=true"),
			Reason:  "ClusterOperatorNotAvailable",
			Message: "Cluster operator test-co has not yet reported success",
			Name:    "test-co",
		},
	}, {
		name: "cluster operator reporting available=true degraded=true",
		actual: &configv1.ClusterOperator{
			ObjectMeta: metav1.ObjectMeta{Name: "test-co"},
			Status: configv1.ClusterOperatorStatus{
				Versions: []configv1.OperandVersion{{
					Name: "operator", Version: "v1",
				}, {
					Name: "operand-1", Version: "v1",
				}},
				Conditions: []configv1.ClusterOperatorStatusCondition{{Type: configv1.OperatorAvailable, Status: configv1.ConditionTrue}, {Type: configv1.OperatorDegraded, Status: configv1.ConditionTrue, Message: "random error"}},
			},
		},
		exp: &configv1.ClusterOperator{
			ObjectMeta: metav1.ObjectMeta{Name: "test-co"},
			Status: configv1.ClusterOperatorStatus{
				Versions: []configv1.OperandVersion{{
					Name: "operator", Version: "v1",
				}, {
					Name: "operand-1", Version: "v1",
				}},
			},
		},
		expErr: &payload.UpdateError{
			Nested:  fmt.Errorf("cluster operator test-co is reporting a failure: random error"),
			Reason:  "ClusterOperatorDegraded",
			Message: "Cluster operator test-co is reporting a failure: random error",
			Name:    "test-co",
		},
	}, {
		name: "cluster operator reporting available=true progressing=true degraded=true",
		actual: &configv1.ClusterOperator{
			ObjectMeta: metav1.ObjectMeta{Name: "test-co"},
			Status: configv1.ClusterOperatorStatus{
				Versions: []configv1.OperandVersion{{
					Name: "operator", Version: "v1",
				}, {
					Name: "operand-1", Version: "v1",
				}},
				Conditions: []configv1.ClusterOperatorStatusCondition{{Type: configv1.OperatorAvailable, Status: configv1.ConditionTrue}, {Type: configv1.OperatorProgressing, Status: configv1.ConditionTrue}, {Type: configv1.OperatorDegraded, Status: configv1.ConditionTrue, Message: "random error"}},
			},
		},
		exp: &configv1.ClusterOperator{
			ObjectMeta: metav1.ObjectMeta{Name: "test-co"},
			Status: configv1.ClusterOperatorStatus{
				Versions: []configv1.OperandVersion{{
					Name: "operator", Version: "v1",
				}, {
					Name: "operand-1", Version: "v1",
				}},
			},
		},
		expErr: &payload.UpdateError{
			Nested:  fmt.Errorf("cluster operator test-co is reporting a failure: random error"),
			Reason:  "ClusterOperatorDegraded",
			Message: "Cluster operator test-co is reporting a failure: random error",
			Name:    "test-co",
		},
	}, {
		name: "cluster operator reporting available=true no progressing or degraded",
		actual: &configv1.ClusterOperator{
			ObjectMeta: metav1.ObjectMeta{Name: "test-co"},
			Status: configv1.ClusterOperatorStatus{
				Versions: []configv1.OperandVersion{{
					Name: "operator", Version: "v1",
				}, {
					Name: "operand-1", Version: "v1",
				}},
				Conditions: []configv1.ClusterOperatorStatusCondition{{Type: configv1.OperatorAvailable, Status: configv1.ConditionTrue}},
			},
		},
		exp: &configv1.ClusterOperator{
			ObjectMeta: metav1.ObjectMeta{Name: "test-co"},
			Status: configv1.ClusterOperatorStatus{
				Versions: []configv1.OperandVersion{{
					Name: "operator", Version: "v1",
				}, {
					Name: "operand-1", Version: "v1",
				}},
			},
		},
		expErr: &payload.UpdateError{
			Nested:  fmt.Errorf("cluster operator test-co is not done; it is available=true, progressing=true, degraded=true"),
			Reason:  "ClusterOperatorNotAvailable",
			Message: "Cluster operator test-co has not yet reported success",
			Name:    "test-co",
		},
	}, {
		name: "cluster operator reporting available=true progressing=false degraded=false",
		actual: &configv1.ClusterOperator{
			ObjectMeta: metav1.ObjectMeta{Name: "test-co"},
			Status: configv1.ClusterOperatorStatus{
				Versions: []configv1.OperandVersion{{
					Name: "operator", Version: "v1",
				}, {
					Name: "operand-1", Version: "v1",
				}},
				Conditions: []configv1.ClusterOperatorStatusCondition{{Type: configv1.OperatorAvailable, Status: configv1.ConditionTrue}, {Type: configv1.OperatorProgressing, Status: configv1.ConditionFalse}, {Type: configv1.OperatorDegraded, Status: configv1.ConditionFalse}},
			},
		},
		exp: &configv1.ClusterOperator{
			ObjectMeta: metav1.ObjectMeta{Name: "test-co"},
			Status: configv1.ClusterOperatorStatus{
				Versions: []configv1.OperandVersion{{
					Name: "operator", Version: "v1",
				}, {
					Name: "operand-1", Version: "v1",
				}},
			},
		},
	}}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			client := &fake.Clientset{}
			client.AddReactor("*", "*", func(action clientgotesting.Action) (handled bool, ret runtime.Object, err error) {
				return false, nil, fmt.Errorf("unexpected client action: %#v", action)
			})
			client.AddReactor("*", "clusteroperators", func(action clientgotesting.Action) (handled bool, ret runtime.Object, err error) {
				switch a := action.(type) {
				case clientgotesting.GetAction:
					if test.actual != nil && a.GetName() == test.actual.GetName() {
						return true, test.actual.DeepCopyObject(), nil
					}
					return true, nil, apierrors.NewNotFound(schema.GroupResource{Resource: "clusteroperator"}, a.GetName())
				}
				return false, nil, fmt.Errorf("unexpected client action: %#v", action)
			})

			ctxWithTimeout, cancel := context.WithTimeout(context.TODO(), 1*time.Millisecond)
			defer cancel()
			err := waitForOperatorStatusToBeDone(ctxWithTimeout, 1*time.Millisecond, clientClusterOperatorsGetter{getter: client.ConfigV1().ClusterOperators()}, test.exp, test.mode, record.NewFakeRecorder(100))
			if (test.expErr == nil) != (err == nil) {
				t.Fatalf("unexpected error: %v", err)
			}
			if !reflect.DeepEqual(test.expErr, err) {
				t.Fatalf("unexpected: %s", diff.ObjectReflectDiff(test.expErr, err))
			}
		})
	}

}

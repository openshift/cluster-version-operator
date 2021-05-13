package internal

import (
	"context"
	"fmt"
	"reflect"
	"testing"

	"github.com/davecgh/go-spew/spew"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	clientgotesting "k8s.io/client-go/testing"

	configv1 "github.com/openshift/api/config/v1"
	"github.com/openshift/client-go/config/clientset/versioned/fake"
	"github.com/openshift/cluster-version-operator/lib/resourcebuilder"
	"github.com/openshift/cluster-version-operator/pkg/payload"
)

func Test_checkOperatorHealth(t *testing.T) {
	ctx := context.Background()
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
			Nested:       apierrors.NewNotFound(schema.GroupResource{"", "clusteroperator"}, "test-co"),
			UpdateEffect: payload.UpdateEffectNone,
			Reason:       "ClusterOperatorNotAvailable",
			Message:      "Cluster operator test-co has not yet reported success",
			Name:         "test-co",
		},
	}, {
		name: "cluster operator reporting available=true and degraded=false, but no versions",
		actual: &configv1.ClusterOperator{
			ObjectMeta: metav1.ObjectMeta{Name: "test-co"},
			Status: configv1.ClusterOperatorStatus{
				Conditions: []configv1.ClusterOperatorStatusCondition{
					{Type: configv1.OperatorAvailable, Status: configv1.ConditionTrue},
					{Type: configv1.OperatorDegraded, Status: configv1.ConditionFalse},
				},
			},
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
			Nested:       fmt.Errorf("cluster operator test-co is available and not degraded but has not finished updating to target version"),
			UpdateEffect: payload.UpdateEffectNone,
			Reason:       "ClusterOperatorUpdating",
			Message:      "Cluster operator test-co is updating versions",
			Name:         "test-co",
		},
	}, {
		name: "cluster operator reporting available=true, degraded=false, but no versions, while expecting an operator version",
		actual: &configv1.ClusterOperator{
			ObjectMeta: metav1.ObjectMeta{Name: "test-co"},
			Status: configv1.ClusterOperatorStatus{
				Conditions: []configv1.ClusterOperatorStatusCondition{
					{Type: configv1.OperatorAvailable, Status: configv1.ConditionTrue},
					{Type: configv1.OperatorDegraded, Status: configv1.ConditionFalse},
				},
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
			Nested:       fmt.Errorf("cluster operator test-co is available and not degraded but has not finished updating to target version"),
			UpdateEffect: payload.UpdateEffectNone,
			Reason:       "ClusterOperatorUpdating",
			Message:      "Cluster operator test-co is updating versions",
			Name:         "test-co",
		},
	}, {
		name: "cluster operator reporting available=true and degraded=false, but no versions for operand",
		actual: &configv1.ClusterOperator{
			ObjectMeta: metav1.ObjectMeta{Name: "test-co"},
			Status: configv1.ClusterOperatorStatus{
				Conditions: []configv1.ClusterOperatorStatusCondition{
					{Type: configv1.OperatorAvailable, Status: configv1.ConditionTrue},
					{Type: configv1.OperatorDegraded, Status: configv1.ConditionFalse},
				},
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
			Nested:       fmt.Errorf("cluster operator test-co is available and not degraded but has not finished updating to target version"),
			UpdateEffect: payload.UpdateEffectNone,
			Reason:       "ClusterOperatorUpdating",
			Message:      "Cluster operator test-co is updating versions",
			Name:         "test-co",
		},
	}, {
		name: "cluster operator reporting available=true and degraded=false, but old versions",
		actual: &configv1.ClusterOperator{
			ObjectMeta: metav1.ObjectMeta{Name: "test-co"},
			Status: configv1.ClusterOperatorStatus{
				Conditions: []configv1.ClusterOperatorStatusCondition{
					{Type: configv1.OperatorAvailable, Status: configv1.ConditionTrue},
					{Type: configv1.OperatorDegraded, Status: configv1.ConditionFalse},
				},
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
			Nested:       fmt.Errorf("cluster operator test-co is available and not degraded but has not finished updating to target version"),
			UpdateEffect: payload.UpdateEffectNone,
			Reason:       "ClusterOperatorUpdating",
			Message:      "Cluster operator test-co is updating versions",
			Name:         "test-co",
		},
	}, {
		name: "cluster operator reporting available=true and degraded=false, but mix of desired and old versions",
		actual: &configv1.ClusterOperator{
			ObjectMeta: metav1.ObjectMeta{Name: "test-co"},
			Status: configv1.ClusterOperatorStatus{
				Conditions: []configv1.ClusterOperatorStatusCondition{
					{Type: configv1.OperatorAvailable, Status: configv1.ConditionTrue},
					{Type: configv1.OperatorDegraded, Status: configv1.ConditionFalse},
				},
				Versions: []configv1.OperandVersion{{
					Name: "operator", Version: "v0",
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
			Nested:       fmt.Errorf("cluster operator test-co is available and not degraded but has not finished updating to target version"),
			UpdateEffect: payload.UpdateEffectNone,
			Reason:       "ClusterOperatorUpdating",
			Message:      "Cluster operator test-co is updating versions",
			Name:         "test-co",
		},
	}, {
		name: "cluster operator reporting available=true, degraded=false, and desired operator, but old versions for 2 operands",
		actual: &configv1.ClusterOperator{
			ObjectMeta: metav1.ObjectMeta{Name: "test-co"},
			Status: configv1.ClusterOperatorStatus{
				Conditions: []configv1.ClusterOperatorStatusCondition{
					{Type: configv1.OperatorAvailable, Status: configv1.ConditionTrue},
					{Type: configv1.OperatorDegraded, Status: configv1.ConditionFalse},
				},
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
			Nested:       fmt.Errorf("cluster operator test-co is available and not degraded but has not finished updating to target version"),
			UpdateEffect: payload.UpdateEffectNone,
			Reason:       "ClusterOperatorUpdating",
			Message:      "Cluster operator test-co is updating versions",
			Name:         "test-co",
		},
	}, {
		name: "cluster operator reporting available=true, degraded=false, and desired operator, but mix of old and desired versions for 2 operands",
		actual: &configv1.ClusterOperator{
			ObjectMeta: metav1.ObjectMeta{Name: "test-co"},
			Status: configv1.ClusterOperatorStatus{
				Conditions: []configv1.ClusterOperatorStatusCondition{
					{Type: configv1.OperatorAvailable, Status: configv1.ConditionTrue},
					{Type: configv1.OperatorDegraded, Status: configv1.ConditionFalse},
				},
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
			Nested:       fmt.Errorf("cluster operator test-co is available and not degraded but has not finished updating to target version"),
			UpdateEffect: payload.UpdateEffectNone,
			Reason:       "ClusterOperatorUpdating",
			Message:      "Cluster operator test-co is updating versions",
			Name:         "test-co",
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
			Nested:       fmt.Errorf("cluster operator test-co: available=false, progressing=true, degraded=true, undone="),
			UpdateEffect: payload.UpdateEffectFail,
			Reason:       "ClusterOperatorNotAvailable",
			Message:      "Cluster operator test-co is not available",
			Name:         "test-co",
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
			Nested:       fmt.Errorf("cluster operator test-co: available=false, progressing=true, degraded=true, undone="),
			UpdateEffect: payload.UpdateEffectFail,
			Reason:       "ClusterOperatorNotAvailable",
			Message:      "Cluster operator test-co is not available",
			Name:         "test-co",
		},
	}, {
		name: "cluster operator reporting available=false degraded=true",
		actual: &configv1.ClusterOperator{
			ObjectMeta: metav1.ObjectMeta{Name: "test-co"},
			Status: configv1.ClusterOperatorStatus{
				Versions: []configv1.OperandVersion{{
					Name: "operator", Version: "v1",
				}, {
					Name: "operand-1", Version: "v1",
				}},
				Conditions: []configv1.ClusterOperatorStatusCondition{{Type: configv1.OperatorAvailable, Status: configv1.ConditionFalse}, {Type: configv1.OperatorDegraded, Status: configv1.ConditionTrue}},
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
			Nested:       fmt.Errorf("cluster operator test-co: available=false, progressing=true, degraded=true, undone="),
			UpdateEffect: payload.UpdateEffectFail,
			Reason:       "ClusterOperatorNotAvailable",
			Message:      "Cluster operator test-co is not available",
			Name:         "test-co",
		},
	}, {
		name: "cluster operator reporting available=true, degraded=false, and progressing=true",
		actual: &configv1.ClusterOperator{
			ObjectMeta: metav1.ObjectMeta{Name: "test-co"},
			Status: configv1.ClusterOperatorStatus{
				Versions: []configv1.OperandVersion{{
					Name: "operator", Version: "v1",
				}, {
					Name: "operand-1", Version: "v1",
				}},
				Conditions: []configv1.ClusterOperatorStatusCondition{
					{Type: configv1.OperatorAvailable, Status: configv1.ConditionTrue},
					{Type: configv1.OperatorDegraded, Status: configv1.ConditionFalse},
					{Type: configv1.OperatorProgressing, Status: configv1.ConditionTrue},
				},
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
				Conditions: []configv1.ClusterOperatorStatusCondition{
					{Type: configv1.OperatorAvailable, Status: configv1.ConditionTrue},
					{Type: configv1.OperatorDegraded, Status: configv1.ConditionTrue, Reason: "RandomReason", Message: "random error"},
				},
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
			Nested:       fmt.Errorf("cluster operator test-co is Degraded=True: RandomReason, random error"),
			UpdateEffect: payload.UpdateEffectFailAfterInterval,
			Reason:       "ClusterOperatorDegraded",
			Message:      "Cluster operator test-co is degraded",
			Name:         "test-co",
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
				Conditions: []configv1.ClusterOperatorStatusCondition{
					{Type: configv1.OperatorAvailable, Status: configv1.ConditionTrue},
					{Type: configv1.OperatorProgressing, Status: configv1.ConditionTrue},
					{Type: configv1.OperatorDegraded, Status: configv1.ConditionTrue, Reason: "RandomReason", Message: "random error"},
				},
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
			Nested:       fmt.Errorf("cluster operator test-co is Degraded=True: RandomReason, random error"),
			UpdateEffect: payload.UpdateEffectFailAfterInterval,
			Reason:       "ClusterOperatorDegraded",
			Message:      "Cluster operator test-co is degraded",
			Name:         "test-co",
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
			Nested:       fmt.Errorf("cluster operator test-co: available=true, progressing=true, degraded=true, undone="),
			UpdateEffect: payload.UpdateEffectFailAfterInterval,
			Reason:       "ClusterOperatorDegraded",
			Message:      "Cluster operator test-co is degraded",
			Name:         "test-co",
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

			err := checkOperatorHealth(ctx, clientClusterOperatorsGetter{getter: client.ConfigV1().ClusterOperators()}, test.exp, test.mode)
			if (test.expErr == nil) != (err == nil) {
				t.Fatalf("unexpected error: %v", err)
			}
			if !reflect.DeepEqual(test.expErr, err) {
				spew.Config.DisableMethods = true
				t.Fatalf("Incorrect value returned -\nexpected: %s\nreturned: %s", spew.Sdump(test.expErr), spew.Sdump(err))
			}
		})
	}

}

package cvo

import (
	"context"
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/openshift/cluster-version-operator/pkg/internal"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"github.com/openshift/cluster-version-operator/pkg/payload"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	apierrors "k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/apimachinery/pkg/util/validation/field"
	"k8s.io/client-go/tools/record"

	configv1 "github.com/openshift/api/config/v1"
	"github.com/openshift/client-go/config/clientset/versioned/fake"

	"github.com/openshift/cluster-version-operator/lib/resourcemerge"
)

func Test_mergeEqualVersions(t *testing.T) {
	tests := []struct {
		name    string
		current *configv1.UpdateHistory
		desired configv1.Release
		want    bool
	}{
		{
			current: &configv1.UpdateHistory{Image: "test:1", Version: "0.0.1"},
			desired: configv1.Release{Image: "test:1", Version: "0.0.1"},
			want:    true,
		},
		{
			current: &configv1.UpdateHistory{Image: "test:1"},
			desired: configv1.Release{Image: "test:1", Version: "0.0.1"},
			want:    true,
		},
		{
			current: &configv1.UpdateHistory{Image: "test:1", Version: "0.0.1"},
			desired: configv1.Release{Image: "test:1"},
			want:    true,
		},
		{
			current: &configv1.UpdateHistory{Image: "test:1", Version: "0.0.1"},
			desired: configv1.Release{Version: "0.0.1"},
			want:    false,
		},
		{
			current: &configv1.UpdateHistory{Image: "test:1", Version: "0.0.1"},
			desired: configv1.Release{Image: "test:2", Version: "0.0.1"},
			want:    false,
		},
		{
			current: &configv1.UpdateHistory{Image: "test:1", Version: "0.0.1"},
			desired: configv1.Release{Image: "test:1", Version: "0.0.2"},
			want:    false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := mergeEqualVersions(tt.current, tt.desired); got != tt.want {
				t.Errorf("%v != %v", tt.want, got)
			}
		})
	}
}

func TestOperator_syncFailingStatus(t *testing.T) {
	ctx := context.Background()
	tests := []struct {
		name        string
		optr        *Operator
		init        func(optr *Operator)
		wantErr     func(*testing.T, error)
		wantActions func(*testing.T, *Operator)
		wantSync    []configv1.Release

		original *configv1.ClusterVersion
		ierr     error
	}{
		{
			ierr: fmt.Errorf("bad"),
			optr: &Operator{
				release: configv1.Release{
					Version: "4.0.1",
					Image:   "image/image:v4.0.1",
					URL:     configv1.URL("https://example.com/v4.0.1"),
				},
				namespace: "test",
				name:      "default",
				client: fakeClientsetWithUpdates(
					&configv1.ClusterVersion{
						ObjectMeta: metav1.ObjectMeta{
							Name: "default",
						},
						Spec: configv1.ClusterVersionSpec{
							Channel: "fast",
						},
						Status: configv1.ClusterVersionStatus{
							History: []configv1.UpdateHistory{
								{Version: "4.0.1", Image: "image/image:v4.0.1", StartedTime: defaultStartedTime},
							},
							Desired: configv1.Release{
								Version: "4.0.1",
								Image:   "image/image:v4.0.1",
								URL:     configv1.URL("https://example.com/v4.0.1"),
							},
							VersionHash: "",
							Conditions: []configv1.ClusterOperatorStatusCondition{
								{Type: configv1.OperatorAvailable, Status: configv1.ConditionFalse},
								{Type: internal.ClusterStatusFailing, Status: configv1.ConditionTrue, Reason: "UpdatePayloadIntegrity", Message: "unable to apply object"},
								{Type: configv1.OperatorProgressing, Status: configv1.ConditionTrue, Message: "Working towards 4.0.1"},
								{Type: configv1.RetrievedUpdates, Status: configv1.ConditionFalse},
								{Type: "ImplicitlyEnabledCapabilities", Status: "False", Reason: "AsExpected", Message: "Capabilities match configured spec"},
							},
						},
					},
				),
				eventRecorder: record.NewFakeRecorder(100),
			},
			wantErr: func(t *testing.T, err error) {
				if err == nil || err.Error() != "bad" {
					t.Fatal(err)
				}
			},
			wantActions: func(t *testing.T, optr *Operator) {
				f := optr.client.(*fake.Clientset)
				act := f.Actions()
				if len(act) != 2 {
					t.Fatalf("unknown actions: %d %#v", len(act), act)
				}
				expectGet(t, act[0], "clusterversions", "", "default")
				expectUpdateStatus(t, act[1], "clusterversions", "", &configv1.ClusterVersion{
					ObjectMeta: metav1.ObjectMeta{
						Name: "default",
					},
					Spec: configv1.ClusterVersionSpec{
						Channel: "fast",
					},
					Status: configv1.ClusterVersionStatus{
						History: []configv1.UpdateHistory{
							{State: configv1.PartialUpdate, Version: "4.0.1", Image: "image/image:v4.0.1", StartedTime: defaultStartedTime},
						},
						Desired: configv1.Release{
							Version: "4.0.1",
							Image:   "image/image:v4.0.1",
							URL:     configv1.URL("https://example.com/v4.0.1"),
						},
						VersionHash: "",
						Conditions: []configv1.ClusterOperatorStatusCondition{
							{Type: configv1.OperatorAvailable, Status: configv1.ConditionFalse},
							{Type: internal.ClusterStatusFailing, Status: configv1.ConditionTrue, Reason: "", Message: "bad"},
							{Type: configv1.OperatorProgressing, Status: configv1.ConditionTrue, Reason: "", Message: "Error ensuring the cluster version is up to date: bad"},
							{Type: configv1.RetrievedUpdates, Status: configv1.ConditionFalse},
							{Type: "ImplicitlyEnabledCapabilities", Status: "False", Reason: "AsExpected", Message: "Capabilities match configured spec"},
						},
					},
				})
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			optr := tt.optr
			if tt.init != nil {
				tt.init(optr)
			}
			optr.cvLister = &clientCVLister{client: optr.client}
			optr.coLister = &clientCOLister{client: optr.client}

			var originalCopy *configv1.ClusterVersion
			if tt.original != nil {
				originalCopy = tt.original.DeepCopy()
			}

			err := optr.syncFailingStatus(ctx, tt.original, tt.ierr)

			if !reflect.DeepEqual(originalCopy, tt.original) {
				t.Fatalf("syncFailingStatus mutated input: %s", cmp.Diff(originalCopy, tt.original))
			}

			if err != nil && tt.wantErr == nil {
				t.Fatalf("Operator.sync() unexpected error: %v", err)
			}
			if tt.wantErr != nil {
				tt.wantErr(t, err)
			}
			if tt.wantActions != nil {
				tt.wantActions(t, optr)
			}
			if err != nil {
				return
			}
		})
	}
}

type fakeRiFlags struct {
	desiredVersion            string
	unknownVersion            bool
	statusReleaseArchitecture bool
	cvoConfiguration          bool
	acceptRisks               bool
}

func (f fakeRiFlags) DesiredVersion() string {
	return f.desiredVersion
}

func (f fakeRiFlags) UnknownVersion() bool {
	return f.unknownVersion
}

func (f fakeRiFlags) StatusReleaseArchitecture() bool {
	return f.statusReleaseArchitecture
}

func (f fakeRiFlags) CVOConfiguration() bool {
	return f.cvoConfiguration
}

func (f fakeRiFlags) AcceptRisks() bool {
	return f.acceptRisks
}

func TestUpdateClusterVersionStatus_FilteringMultipleErrorsForFailingCondition(t *testing.T) {
	ignoreLastTransitionTime := cmpopts.IgnoreFields(configv1.ClusterOperatorStatusCondition{}, "LastTransitionTime")
	type args struct {
		syncWorkerStatus *SyncWorkerStatus
	}
	payload.COUpdateStartTimesEnsure("co-not-timeout")
	payload.COUpdateStartTimesEnsure("co-bar-not-timeout")
	payload.COUpdateStartTimesEnsure("co-foo-not-timeout")
	payload.COUpdateStartTimesAt("co-timeout", time.Now().Add(-60*time.Minute))
	payload.COUpdateStartTimesAt("co-bar-timeout", time.Now().Add(-60*time.Minute))
	defer func() {
		payload.COUpdateStartTimesRemove("co-not-timeout")
		payload.COUpdateStartTimesRemove("co-bar-not-timeout")
		payload.COUpdateStartTimesRemove("co-foo-not-timeout")
		payload.COUpdateStartTimesRemove("co-timeout")
		payload.COUpdateStartTimesRemove("co-bar-timeout")
	}()

	tests := []struct {
		name                                             string
		args                                             args
		shouldModifyWhenNotReconcilingAndHistoryNotEmpty bool
		expectedConditionNotModified                     *configv1.ClusterOperatorStatusCondition
		expectedConditionModified                        *configv1.ClusterOperatorStatusCondition
		machineConfigTimeout                             bool
		expectedProgressingCondition                     *configv1.ClusterOperatorStatusCondition
	}{
		{
			name: "no errors are present",
			args: args{
				syncWorkerStatus: &SyncWorkerStatus{
					Failure: nil,
				},
			},
			expectedConditionNotModified: &configv1.ClusterOperatorStatusCondition{
				Type:   internal.ClusterStatusFailing,
				Status: configv1.ConditionFalse,
			},
		},
		{
			name: "single generic error",
			args: args{
				syncWorkerStatus: &SyncWorkerStatus{
					Failure: fmt.Errorf("error has happened"),
				},
			},
			expectedConditionNotModified: &configv1.ClusterOperatorStatusCondition{
				Type:    internal.ClusterStatusFailing,
				Status:  configv1.ConditionTrue,
				Message: "error has happened",
			},
		},
		{
			name: "single UpdateEffectNone error",
			args: args{
				syncWorkerStatus: &SyncWorkerStatus{
					Actual: configv1.Release{Version: "1.2.3"},
					Failure: &payload.UpdateError{
						UpdateEffect: payload.UpdateEffectNone,
						Reason:       "ClusterOperatorUpdating",
						Message:      "Cluster operator A is updating",
						Name:         "co-not-timeout",
					},
				},
			},
			expectedConditionNotModified: &configv1.ClusterOperatorStatusCondition{
				Type:    internal.ClusterStatusFailing,
				Status:  configv1.ConditionTrue,
				Reason:  "ClusterOperatorUpdating",
				Message: "Cluster operator A is updating",
			},
			shouldModifyWhenNotReconcilingAndHistoryNotEmpty: true,
			expectedConditionModified: &configv1.ClusterOperatorStatusCondition{
				Type:   internal.ClusterStatusFailing,
				Status: configv1.ConditionFalse,
			},
			expectedProgressingCondition: &configv1.ClusterOperatorStatusCondition{
				Type:    configv1.OperatorProgressing,
				Status:  configv1.ConditionTrue,
				Reason:  "ClusterOperatorUpdating",
				Message: "Working towards 1.2.3: waiting on co-not-timeout",
			},
		},
		{
			name: "single UpdateEffectNone error and timeout",
			args: args{
				syncWorkerStatus: &SyncWorkerStatus{
					Actual: configv1.Release{Version: "1.2.3"},
					Failure: &payload.UpdateError{
						UpdateEffect: payload.UpdateEffectNone,
						Reason:       "ClusterOperatorUpdating",
						Message:      "Cluster operator A is updating",
						Name:         "co-timeout",
					},
				},
			},
			expectedConditionNotModified: &configv1.ClusterOperatorStatusCondition{
				Type:    internal.ClusterStatusFailing,
				Status:  configv1.ConditionTrue,
				Reason:  "ClusterOperatorUpdating",
				Message: "Cluster operator A is updating",
			},
			shouldModifyWhenNotReconcilingAndHistoryNotEmpty: true,
			expectedConditionModified: &configv1.ClusterOperatorStatusCondition{
				Type:    internal.ClusterStatusFailing,
				Status:  configv1.ConditionUnknown,
				Reason:  "SlowClusterOperator",
				Message: "waiting on co-timeout over 30 minutes which is longer than expected",
			},
			expectedProgressingCondition: &configv1.ClusterOperatorStatusCondition{
				Type:    configv1.OperatorProgressing,
				Status:  configv1.ConditionTrue,
				Reason:  "ClusterOperatorUpdating",
				Message: "Working towards 1.2.3: waiting on co-timeout over 30 minutes which is longer than expected",
			},
		},
		{
			name: "single UpdateEffectNone error and machine-config",
			args: args{
				syncWorkerStatus: &SyncWorkerStatus{
					Actual: configv1.Release{Version: "1.2.3"},
					Failure: &payload.UpdateError{
						UpdateEffect: payload.UpdateEffectNone,
						Reason:       "ClusterOperatorUpdating",
						Message:      "Cluster operator A is updating",
						Name:         "machine-config",
					},
				},
			},
			expectedConditionNotModified: &configv1.ClusterOperatorStatusCondition{
				Type:    internal.ClusterStatusFailing,
				Status:  configv1.ConditionTrue,
				Reason:  "ClusterOperatorUpdating",
				Message: "Cluster operator A is updating",
			},
			shouldModifyWhenNotReconcilingAndHistoryNotEmpty: true,
			expectedConditionModified: &configv1.ClusterOperatorStatusCondition{
				Type:   internal.ClusterStatusFailing,
				Status: configv1.ConditionFalse,
			},
			expectedProgressingCondition: &configv1.ClusterOperatorStatusCondition{
				Type:    configv1.OperatorProgressing,
				Status:  configv1.ConditionTrue,
				Reason:  "ClusterOperatorUpdating",
				Message: "Working towards 1.2.3: waiting on machine-config",
			},
		},
		{
			name: "single UpdateEffectNone error and machine-config timeout",
			args: args{
				syncWorkerStatus: &SyncWorkerStatus{
					Actual: configv1.Release{Version: "1.2.3"},
					Failure: &payload.UpdateError{
						UpdateEffect: payload.UpdateEffectNone,
						Reason:       "ClusterOperatorUpdating",
						Message:      "Cluster operator A is updating",
						Name:         "machine-config",
					},
				},
			},
			machineConfigTimeout: true,
			expectedConditionNotModified: &configv1.ClusterOperatorStatusCondition{
				Type:    internal.ClusterStatusFailing,
				Status:  configv1.ConditionTrue,
				Reason:  "ClusterOperatorUpdating",
				Message: "Cluster operator A is updating",
			},
			shouldModifyWhenNotReconcilingAndHistoryNotEmpty: true,
			expectedConditionModified: &configv1.ClusterOperatorStatusCondition{
				Type:    internal.ClusterStatusFailing,
				Status:  configv1.ConditionUnknown,
				Reason:  "SlowClusterOperator",
				Message: "waiting on machine-config over 90 minutes which is longer than expected",
			},
			expectedProgressingCondition: &configv1.ClusterOperatorStatusCondition{
				Type:    configv1.OperatorProgressing,
				Status:  configv1.ConditionTrue,
				Reason:  "ClusterOperatorUpdating",
				Message: "Working towards 1.2.3: waiting on machine-config over 90 minutes which is longer than expected",
			},
		},
		{
			name: "single condensed UpdateEffectFail UpdateError",
			args: args{
				syncWorkerStatus: &SyncWorkerStatus{
					Failure: &payload.UpdateError{
						Nested: apierrors.NewAggregate([]error{
							&payload.UpdateError{
								UpdateEffect: payload.UpdateEffectFail,
								Reason:       "ClusterOperatorNotAvailable",
								Message:      "Cluster operator A is not available",
							},
							&payload.UpdateError{
								UpdateEffect: payload.UpdateEffectFail,
								Reason:       "ClusterOperatorNotAvailable",
								Message:      "Cluster operator B is not available",
							},
						}),
						UpdateEffect: payload.UpdateEffectFail,
						Reason:       "ClusterOperatorNotAvailable",
						Message:      "Cluster operators A, B are not available",
					},
				},
			},
			expectedConditionNotModified: &configv1.ClusterOperatorStatusCondition{
				Type:    internal.ClusterStatusFailing,
				Status:  configv1.ConditionTrue,
				Reason:  "ClusterOperatorNotAvailable",
				Message: "Cluster operators A, B are not available",
			},
		},
		{
			name: "MultipleErrors of UpdateEffectFail and UpdateEffectFailAfterInterval",
			args: args{
				syncWorkerStatus: &SyncWorkerStatus{
					Failure: &payload.UpdateError{
						Nested: apierrors.NewAggregate([]error{
							&payload.UpdateError{
								UpdateEffect: payload.UpdateEffectFail,
								Reason:       "ClusterOperatorNotAvailable",
								Message:      "Cluster operator A is not available",
							},
							&payload.UpdateError{
								UpdateEffect: payload.UpdateEffectFailAfterInterval,
								Reason:       "ClusterOperatorDegraded",
								Message:      "Cluster operator B is degraded",
							},
						}),
						UpdateEffect: payload.UpdateEffectFail,
						Reason:       "MultipleErrors",
						Message:      "Multiple errors are preventing progress:\n* Cluster operator A is not available\n* Cluster operator B is degraded",
					},
				},
			},
			expectedConditionNotModified: &configv1.ClusterOperatorStatusCondition{
				Type:    internal.ClusterStatusFailing,
				Status:  configv1.ConditionTrue,
				Reason:  "MultipleErrors",
				Message: "Multiple errors are preventing progress:\n* Cluster operator A is not available\n* Cluster operator B is degraded",
			},
		},
		{
			name: "MultipleErrors of UpdateEffectFail and UpdateEffectNone",
			args: args{
				syncWorkerStatus: &SyncWorkerStatus{
					Failure: &payload.UpdateError{
						Nested: apierrors.NewAggregate([]error{
							&payload.UpdateError{
								UpdateEffect: payload.UpdateEffectFail,
								Reason:       "ClusterOperatorNotAvailable",
								Message:      "Cluster operator A is not available",
							},
							&payload.UpdateError{
								UpdateEffect: payload.UpdateEffectNone,
								Reason:       "ClusterOperatorUpdating",
								Message:      "Cluster operator B is updating versions",
							},
						}),
						UpdateEffect: payload.UpdateEffectFail,
						Reason:       "MultipleErrors",
						Message:      "Multiple errors are preventing progress:\n* Cluster operator A is not available\n* Cluster operator B is updating versions",
					},
				},
			},
			expectedConditionNotModified: &configv1.ClusterOperatorStatusCondition{
				Type:    internal.ClusterStatusFailing,
				Status:  configv1.ConditionTrue,
				Reason:  "MultipleErrors",
				Message: "Multiple errors are preventing progress:\n* Cluster operator A is not available\n* Cluster operator B is updating versions",
			},
			shouldModifyWhenNotReconcilingAndHistoryNotEmpty: true,
			expectedConditionModified: &configv1.ClusterOperatorStatusCondition{
				Type:    internal.ClusterStatusFailing,
				Status:  configv1.ConditionTrue,
				Reason:  "ClusterOperatorNotAvailable",
				Message: "Cluster operator A is not available",
			},
		},
		{
			name: "MultipleErrors of UpdateEffectNone and UpdateEffectNone",
			args: args{
				syncWorkerStatus: &SyncWorkerStatus{
					Failure: &payload.UpdateError{
						Nested: apierrors.NewAggregate([]error{
							&payload.UpdateError{
								UpdateEffect: payload.UpdateEffectNone,
								Reason:       "ClusterOperatorUpdating",
								Message:      "Cluster operator A is updating versions",
							},
							&payload.UpdateError{
								UpdateEffect: payload.UpdateEffectNone,
								Reason:       "NewUpdateEffectNoneReason",
								Message:      "Cluster operator B is getting conscious",
							},
						}),
						UpdateEffect: payload.UpdateEffectFail,
						Reason:       "MultipleErrors",
						Message:      "Multiple errors are preventing progress:\n* Cluster operator A is updating versions\n* Cluster operator B is getting conscious",
					},
				},
			},
			expectedConditionNotModified: &configv1.ClusterOperatorStatusCondition{
				Type:    internal.ClusterStatusFailing,
				Status:  configv1.ConditionTrue,
				Reason:  "MultipleErrors",
				Message: "Multiple errors are preventing progress:\n* Cluster operator A is updating versions\n* Cluster operator B is getting conscious",
			},
			shouldModifyWhenNotReconcilingAndHistoryNotEmpty: true,
			expectedConditionModified: &configv1.ClusterOperatorStatusCondition{
				Type:   internal.ClusterStatusFailing,
				Status: configv1.ConditionFalse,
			},
		},
		{
			name: "MultipleErrors of UpdateEffectFail, UpdateEffectFailAfterInterval, and UpdateEffectNone",
			args: args{
				syncWorkerStatus: &SyncWorkerStatus{
					Failure: &payload.UpdateError{
						Nested: apierrors.NewAggregate([]error{
							&payload.UpdateError{
								UpdateEffect: payload.UpdateEffectFail,
								Reason:       "ClusterOperatorNotAvailable",
								Message:      "Cluster operator A is not available",
							},
							&payload.UpdateError{
								UpdateEffect: payload.UpdateEffectNone,
								Reason:       "ClusterOperatorUpdating",
								Message:      "Cluster operator B is updating versions",
							},
							&payload.UpdateError{
								UpdateEffect: payload.UpdateEffectFailAfterInterval,
								Reason:       "ClusterOperatorDegraded",
								Message:      "Cluster operator C is degraded",
							},
						}),
						UpdateEffect: payload.UpdateEffectFail,
						Reason:       "MultipleErrors",
						Message:      "Multiple errors are preventing progress:\n* Cluster operator A is not available\n* Cluster operator B is updating versions\n* Cluster operator C is degraded",
					},
				},
			},
			expectedConditionNotModified: &configv1.ClusterOperatorStatusCondition{
				Type:    internal.ClusterStatusFailing,
				Status:  configv1.ConditionTrue,
				Reason:  "MultipleErrors",
				Message: "Multiple errors are preventing progress:\n* Cluster operator A is not available\n* Cluster operator B is updating versions\n* Cluster operator C is degraded",
			},
			shouldModifyWhenNotReconcilingAndHistoryNotEmpty: true,
			expectedConditionModified: &configv1.ClusterOperatorStatusCondition{
				Type:    internal.ClusterStatusFailing,
				Status:  configv1.ConditionTrue,
				Reason:  "MultipleErrors",
				Message: "Multiple errors are preventing progress:\n* Cluster operator A is not available\n* Cluster operator C is degraded",
			},
		},
		{
			name: "MultipleErrors: all updating and none slow",
			args: args{
				syncWorkerStatus: &SyncWorkerStatus{
					Actual: configv1.Release{Version: "1.2.3"},
					Failure: &payload.UpdateError{
						UpdateEffect: payload.UpdateEffectNone,
						Reason:       "ClusterOperatorsUpdating",
						Message:      "some-message",
						Names:        []string{"co-not-timeout", "co-bar-not-timeout", "co-foo-not-timeout"},
					},
				},
			},
			expectedConditionNotModified: &configv1.ClusterOperatorStatusCondition{
				Type:    internal.ClusterStatusFailing,
				Status:  configv1.ConditionTrue,
				Reason:  "ClusterOperatorsUpdating",
				Message: "some-message",
			},
			shouldModifyWhenNotReconcilingAndHistoryNotEmpty: true,
			expectedConditionModified: &configv1.ClusterOperatorStatusCondition{
				Type:   internal.ClusterStatusFailing,
				Status: configv1.ConditionFalse,
			},
			expectedProgressingCondition: &configv1.ClusterOperatorStatusCondition{
				Type:    configv1.OperatorProgressing,
				Status:  configv1.ConditionTrue,
				Reason:  "ClusterOperatorsUpdating",
				Message: "Working towards 1.2.3: waiting on co-not-timeout, co-bar-not-timeout, co-foo-not-timeout",
			},
		},
		{
			name: "MultipleErrors: all updating and one slow",
			args: args{
				syncWorkerStatus: &SyncWorkerStatus{
					Actual: configv1.Release{Version: "1.2.3"},
					Failure: &payload.UpdateError{
						UpdateEffect: payload.UpdateEffectNone,
						Reason:       "ClusterOperatorsUpdating",
						Message:      "some-message",
						Names:        []string{"co-timeout", "co-bar-not-timeout", "co-foo-not-timeout"},
					},
				},
			},
			expectedConditionNotModified: &configv1.ClusterOperatorStatusCondition{
				Type:    internal.ClusterStatusFailing,
				Status:  configv1.ConditionTrue,
				Reason:  "ClusterOperatorsUpdating",
				Message: "some-message",
			},
			shouldModifyWhenNotReconcilingAndHistoryNotEmpty: true,
			expectedConditionModified: &configv1.ClusterOperatorStatusCondition{
				Type:    internal.ClusterStatusFailing,
				Status:  configv1.ConditionUnknown,
				Reason:  "SlowClusterOperator",
				Message: "waiting on co-timeout over 30 minutes which is longer than expected",
			},
			expectedProgressingCondition: &configv1.ClusterOperatorStatusCondition{
				Type:    configv1.OperatorProgressing,
				Status:  configv1.ConditionTrue,
				Reason:  "ClusterOperatorsUpdating",
				Message: "Working towards 1.2.3: waiting on co-timeout over 30 minutes which is longer than expected",
			},
		},
		{
			name: "MultipleErrors: all updating and some slow",
			args: args{
				syncWorkerStatus: &SyncWorkerStatus{
					Actual: configv1.Release{Version: "1.2.3"},
					Failure: &payload.UpdateError{
						UpdateEffect: payload.UpdateEffectNone,
						Reason:       "ClusterOperatorsUpdating",
						Message:      "some-message",
						Names:        []string{"co-timeout", "co-bar-timeout", "co-foo-not-timeout"},
					},
				},
			},
			expectedConditionNotModified: &configv1.ClusterOperatorStatusCondition{
				Type:    internal.ClusterStatusFailing,
				Status:  configv1.ConditionTrue,
				Reason:  "ClusterOperatorsUpdating",
				Message: "some-message",
			},
			shouldModifyWhenNotReconcilingAndHistoryNotEmpty: true,
			expectedConditionModified: &configv1.ClusterOperatorStatusCondition{
				Type:    internal.ClusterStatusFailing,
				Status:  configv1.ConditionUnknown,
				Reason:  "SlowClusterOperator",
				Message: "waiting on co-timeout, co-bar-timeout over 30 minutes which is longer than expected",
			},
			expectedProgressingCondition: &configv1.ClusterOperatorStatusCondition{
				Type:    configv1.OperatorProgressing,
				Status:  configv1.ConditionTrue,
				Reason:  "ClusterOperatorsUpdating",
				Message: "Working towards 1.2.3: waiting on co-timeout, co-bar-timeout over 30 minutes which is longer than expected",
			},
		},
		{
			name: "MultipleErrors: all updating and all slow",
			args: args{
				syncWorkerStatus: &SyncWorkerStatus{
					Actual: configv1.Release{Version: "1.2.3"},
					Failure: &payload.UpdateError{
						UpdateEffect: payload.UpdateEffectNone,
						Reason:       "ClusterOperatorsUpdating",
						Message:      "some-message",
						Names:        []string{"co-timeout", "co-bar-timeout", "machine-config"},
					},
				},
			},
			machineConfigTimeout: true,
			expectedConditionNotModified: &configv1.ClusterOperatorStatusCondition{
				Type:    internal.ClusterStatusFailing,
				Status:  configv1.ConditionTrue,
				Reason:  "ClusterOperatorsUpdating",
				Message: "some-message",
			},
			shouldModifyWhenNotReconcilingAndHistoryNotEmpty: true,
			expectedConditionModified: &configv1.ClusterOperatorStatusCondition{
				Type:    internal.ClusterStatusFailing,
				Status:  configv1.ConditionUnknown,
				Reason:  "SlowClusterOperator",
				Message: "waiting on co-timeout, co-bar-timeout over 30 minutes and machine-config over 90 minutes which is longer than expected",
			},
			expectedProgressingCondition: &configv1.ClusterOperatorStatusCondition{
				Type:    configv1.OperatorProgressing,
				Status:  configv1.ConditionTrue,
				Reason:  "ClusterOperatorsUpdating",
				Message: "Working towards 1.2.3: waiting on co-timeout, co-bar-timeout over 30 minutes and machine-config over 90 minutes which is longer than expected",
			},
		},
	}
	for _, tc := range tests {
		tc := tc
		gates := fakeRiFlags{}
		release := configv1.Release{}
		getAvailableUpdates := func() *availableUpdates { return nil }
		var noErrors field.ErrorList

		t.Run(tc.name, func(t *testing.T) {
			type helper struct {
				isReconciling  bool
				isHistoryEmpty bool
			}
			combinations := []helper{
				{false, false},
				{false, true},
				{true, false},
				{true, true},
			}
			if tc.machineConfigTimeout {
				payload.COUpdateStartTimesAt("machine-config", time.Now().Add(-120*time.Minute))
			} else {
				payload.COUpdateStartTimesAt("machine-config", time.Now().Add(-60*time.Minute))
			}
			for _, c := range combinations {
				tc.args.syncWorkerStatus.Reconciling = c.isReconciling
				cvStatus := &configv1.ClusterVersionStatus{
					Conditions: []configv1.ClusterOperatorStatusCondition{
						{Type: "ImplicitlyEnabled", Message: "to be removed"},
					},
				}
				if !c.isHistoryEmpty {
					cvStatus.History = []configv1.UpdateHistory{{State: configv1.PartialUpdate}}
				}
				expectedCondition := tc.expectedConditionNotModified
				if tc.shouldModifyWhenNotReconcilingAndHistoryNotEmpty && !c.isReconciling && !c.isHistoryEmpty {
					expectedCondition = tc.expectedConditionModified
				}
				updateClusterVersionStatus(cvStatus, tc.args.syncWorkerStatus, release, getAvailableUpdates, gates, noErrors, func() bool {
					return false
				})
				condition := resourcemerge.FindOperatorStatusCondition(cvStatus.Conditions, internal.ClusterStatusFailing)
				if diff := cmp.Diff(expectedCondition, condition, ignoreLastTransitionTime); diff != "" {
					t.Errorf("unexpected condition when Reconciling == %t && isHistoryEmpty == %t\n:%s", c.isReconciling, c.isHistoryEmpty, diff)
				}

				if tc.expectedProgressingCondition != nil && !c.isReconciling && !c.isHistoryEmpty {
					progressingCondition := resourcemerge.FindOperatorStatusCondition(cvStatus.Conditions, configv1.OperatorProgressing)
					if diff := cmp.Diff(tc.expectedProgressingCondition, progressingCondition, ignoreLastTransitionTime); diff != "" {
						t.Errorf("unexpected progressingCondition when Reconciling == %t && isHistoryEmpty == %t\n:%s", c.isReconciling, c.isHistoryEmpty, diff)
					}
				}
				conditionRemoved := resourcemerge.FindOperatorStatusCondition(cvStatus.Conditions, "ImplicitlyEnabled")
				if conditionRemoved != nil {
					t.Errorf("ImplicitlyEnabled condition should be removed but is still there when Reconciling == %t && isHistoryEmpty == %t", c.isReconciling, c.isHistoryEmpty)
				}
			}
			payload.COUpdateStartTimesRemove("machine-config")
		})
	}
}

func Test_filterOutUpdateErrors(t *testing.T) {
	type args struct {
		errs             []error
		updateEffectType payload.UpdateEffectType
	}
	tests := []struct {
		name string
		args args
		want []error
	}{
		{
			name: "empty errors",
			args: args{
				errs:             []error{},
				updateEffectType: payload.UpdateEffectNone,
			},
			want: []error{},
		},
		{
			name: "single update error of the specified value",
			args: args{
				errs: []error{
					&payload.UpdateError{
						Name:         "None",
						UpdateEffect: payload.UpdateEffectNone,
					},
				},
				updateEffectType: payload.UpdateEffectNone,
			},
			want: []error{},
		},
		{
			name: "errors do not contain update errors of the specified value",
			args: args{
				errs: []error{
					&payload.UpdateError{
						Name:         "Fail",
						UpdateEffect: payload.UpdateEffectFail,
					},
					&payload.UpdateError{
						Name:         "Report",
						UpdateEffect: payload.UpdateEffectReport,
					},
					&payload.UpdateError{
						Name:         "Fail After Interval",
						UpdateEffect: payload.UpdateEffectFailAfterInterval,
					},
				},
				updateEffectType: payload.UpdateEffectNone,
			},
			want: []error{
				&payload.UpdateError{
					Name:         "Fail",
					UpdateEffect: payload.UpdateEffectFail,
				},
				&payload.UpdateError{
					Name:         "Report",
					UpdateEffect: payload.UpdateEffectReport,
				},
				&payload.UpdateError{
					Name:         "Fail After Interval",
					UpdateEffect: payload.UpdateEffectFailAfterInterval,
				},
			},
		},
		{
			name: "errors contain update errors of the specified value UpdateEffectNone",
			args: args{
				errs: []error{
					&payload.UpdateError{
						Name:         "Fail After Interval",
						UpdateEffect: payload.UpdateEffectFailAfterInterval,
					},
					&payload.UpdateError{
						Name:         "None #1",
						UpdateEffect: payload.UpdateEffectNone,
					},
					&payload.UpdateError{
						Name:         "Report",
						UpdateEffect: payload.UpdateEffectReport,
					},
					&payload.UpdateError{
						Name:         "None #2",
						UpdateEffect: payload.UpdateEffectNone,
					},
				},
				updateEffectType: payload.UpdateEffectNone,
			},
			want: []error{
				&payload.UpdateError{
					Name:         "Fail After Interval",
					UpdateEffect: payload.UpdateEffectFailAfterInterval,
				},
				&payload.UpdateError{
					Name:         "Report",
					UpdateEffect: payload.UpdateEffectReport,
				},
			},
		},
		{
			name: "errors contain update errors of the specified value UpdateEffectReport",
			args: args{
				errs: []error{
					&payload.UpdateError{
						Name:         "Fail After Interval",
						UpdateEffect: payload.UpdateEffectFailAfterInterval,
					},
					&payload.UpdateError{
						Name:         "None #1",
						UpdateEffect: payload.UpdateEffectNone,
					},
					&payload.UpdateError{
						Name:         "Report",
						UpdateEffect: payload.UpdateEffectReport,
					},
					&payload.UpdateError{
						Name:         "None #2",
						UpdateEffect: payload.UpdateEffectNone,
					},
				},
				updateEffectType: payload.UpdateEffectReport,
			},
			want: []error{
				&payload.UpdateError{
					Name:         "Fail After Interval",
					UpdateEffect: payload.UpdateEffectFailAfterInterval,
				},
				&payload.UpdateError{
					Name:         "None #1",
					UpdateEffect: payload.UpdateEffectNone,
				},
				&payload.UpdateError{
					Name:         "None #2",
					UpdateEffect: payload.UpdateEffectNone,
				},
			},
		},
		{
			name: "errors contain only update errors of the specified value UpdateEffectNone",
			args: args{
				errs: []error{
					&payload.UpdateError{
						Name:         "None #1",
						UpdateEffect: payload.UpdateEffectNone,
					},
					&payload.UpdateError{
						Name:         "None #2",
						UpdateEffect: payload.UpdateEffectNone,
					},
					&payload.UpdateError{
						Name:         "None #3",
						UpdateEffect: payload.UpdateEffectNone,
					},
					&payload.UpdateError{
						Name:         "None #4",
						UpdateEffect: payload.UpdateEffectNone,
					},
				},
				updateEffectType: payload.UpdateEffectNone,
			},
			want: []error{},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			filtered := filterOutUpdateErrors(tt.args.errs, tt.args.updateEffectType)
			if difference := cmp.Diff(filtered, tt.want); difference != "" {
				t.Errorf("got errors differ from expected:\n%s", difference)
			}
		})
	}
}

var (
	cu1 = configv1.ConditionalUpdate{
		Release:   configv1.Release{Version: "4.5.6", Image: "pullspec/4.5.6"},
		RiskNames: []string{"wrongName"},
		Risks: []configv1.ConditionalUpdateRisk{
			{
				Name: "Risk1",
				Conditions: []metav1.Condition{
					{Type: "wrongType", Status: metav1.ConditionUnknown},
				},
			},
		},
	}

	cu2 = configv1.ConditionalUpdate{
		Release: configv1.Release{Version: "4.5.7", Image: "pullspec/4.5.7"},
		Risks: []configv1.ConditionalUpdateRisk{
			{
				Name: "Risk1",
			},
			{
				Name: "Risk2",
			},
		},
	}
)

func Test_conditionalUpdateWithRiskNamesAndRiskConditions(t *testing.T) {
	tests := []struct {
		name               string
		conditionalUpdates []configv1.ConditionalUpdate
		desiredImage       string
		availableUpdates   *availableUpdates
		expected           []configv1.ConditionalUpdate
		expectedNames      []string
	}{
		{
			name:               "nil available updates",
			conditionalUpdates: []configv1.ConditionalUpdate{*cu1.DeepCopy()},
			expected: []configv1.ConditionalUpdate{{
				RiskNames: []string{"Risk1"},
				Release:   configv1.Release{Version: "4.5.6", Image: "pullspec/4.5.6"},
				Risks: []configv1.ConditionalUpdateRisk{
					{
						Name: "Risk1", Conditions: []metav1.Condition{{
							Type:    "Applies",
							Status:  metav1.ConditionUnknown,
							Reason:  "InternalErrorFoundNoRiskCondition",
							Message: "failed to find risk condition for risk Risk1",
						},
						}},
				},
			}},
		},
		{
			name:               "no risk conditions",
			availableUpdates:   &availableUpdates{},
			conditionalUpdates: []configv1.ConditionalUpdate{*cu1.DeepCopy()},
			expected: []configv1.ConditionalUpdate{{
				Release:   configv1.Release{Version: "4.5.6", Image: "pullspec/4.5.6"},
				RiskNames: []string{"Risk1"},
				Risks: []configv1.ConditionalUpdateRisk{{Name: "Risk1",
					Conditions: []metav1.Condition{{
						Type:    "Applies",
						Status:  metav1.ConditionUnknown,
						Reason:  "InternalErrorFoundNoRiskCondition",
						Message: "failed to find risk condition for risk Risk1",
					},
					}}},
			}},
		},
		{
			name:               "no risk conditions but desired image matches",
			desiredImage:       "pullspec/4.5.6",
			availableUpdates:   &availableUpdates{},
			conditionalUpdates: []configv1.ConditionalUpdate{*cu1.DeepCopy()},
			expected: []configv1.ConditionalUpdate{{
				Release:   configv1.Release{Version: "4.5.6", Image: "pullspec/4.5.6"},
				RiskNames: []string{"Risk1"},
				Risks: []configv1.ConditionalUpdateRisk{{Name: "Risk1",
					Conditions: []metav1.Condition{{
						Type:    "Applies",
						Status:  metav1.ConditionUnknown,
						Reason:  "InternalErrorFoundNoRiskCondition",
						Message: "failed to find risk condition for risk Risk1",
					},
					}}},
			}},
			expectedNames: []string{"Risk1"},
		},
		{
			name:         "basic case",
			desiredImage: "pullspec/4.5.7",
			availableUpdates: &availableUpdates{
				RiskConditions: map[string][]metav1.Condition{
					"Risk1": {{Type: "Applies", Status: metav1.ConditionTrue}},
					"Risk2": {{Type: "Applies", Status: metav1.ConditionFalse}},
				},
			},
			conditionalUpdates: []configv1.ConditionalUpdate{*cu1.DeepCopy(), *cu2.DeepCopy()},
			expected: []configv1.ConditionalUpdate{{
				Release:   configv1.Release{Version: "4.5.6", Image: "pullspec/4.5.6"},
				RiskNames: []string{"Risk1"},
				Risks: []configv1.ConditionalUpdateRisk{{Name: "Risk1",
					Conditions: []metav1.Condition{{
						Type:   "Applies",
						Status: metav1.ConditionTrue,
					},
					}}},
			}, {
				Release:   configv1.Release{Version: "4.5.7", Image: "pullspec/4.5.7"},
				RiskNames: []string{"Risk1", "Risk2"},
				Risks: []configv1.ConditionalUpdateRisk{{Name: "Risk1",
					Conditions: []metav1.Condition{{
						Type:   "Applies",
						Status: metav1.ConditionTrue,
					},
					}}, {Name: "Risk2",
					Conditions: []metav1.Condition{{
						Type:   "Applies",
						Status: metav1.ConditionFalse,
					},
					}}},
			}},
			expectedNames: []string{"Risk1", "Risk2"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			getAvailableUpdates := func() *availableUpdates {
				return tt.availableUpdates
			}
			actual, actualNames := conditionalUpdateWithRiskNamesAndRiskConditions(tt.conditionalUpdates, getAvailableUpdates, tt.desiredImage)
			if difference := cmp.Diff(tt.expected, actual, cmpopts.IgnoreFields(metav1.Condition{}, "LastTransitionTime")); difference != "" {
				t.Errorf("conditional updates differ from expected:\n%s", difference)
			}
			if difference := cmp.Diff(tt.expectedNames, actualNames); difference != "" {
				t.Errorf("risk names differ from expected:\n%s", difference)
			}
		})
	}
}

func Test_conditionalUpdateRisks(t *testing.T) {
	tests := []struct {
		name               string
		conditionalUpdates []configv1.ConditionalUpdate
		expected           []configv1.ConditionalUpdateRisk
	}{
		{
			name: "basic case",
			conditionalUpdates: []configv1.ConditionalUpdate{{
				Release:   configv1.Release{Version: "4.5.6", Image: "pullspec/4.5.6"},
				RiskNames: []string{"Risk1", "Risk3"},
				Risks: []configv1.ConditionalUpdateRisk{{Name: "Risk1",
					Conditions: []metav1.Condition{{
						Type:   "Applies",
						Status: metav1.ConditionTrue,
					},
					}}, {Name: "Risk3",
					Conditions: []metav1.Condition{{
						Type:   "Applies",
						Status: metav1.ConditionTrue,
					},
					}}},
			}, {
				Release:   configv1.Release{Version: "4.5.7", Image: "pullspec/4.5.7"},
				RiskNames: []string{"Risk1", "Risk2"},
				Risks: []configv1.ConditionalUpdateRisk{{Name: "Risk1",
					Conditions: []metav1.Condition{{
						Type:   "Applies",
						Status: metav1.ConditionTrue,
					},
					}}, {Name: "Risk2",
					Conditions: []metav1.Condition{{
						Type:   "Applies",
						Status: metav1.ConditionFalse,
					},
					}}},
			}},
			expected: []configv1.ConditionalUpdateRisk{{Name: "Risk1",
				Conditions: []metav1.Condition{{
					Type:   "Applies",
					Status: metav1.ConditionTrue,
				},
				}}, {Name: "Risk2",
				Conditions: []metav1.Condition{{
					Type:   "Applies",
					Status: metav1.ConditionFalse,
				},
				}}, {Name: "Risk3",
				Conditions: []metav1.Condition{{
					Type:   "Applies",
					Status: metav1.ConditionTrue,
				},
				}}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			actual := conditionalUpdateRisks(tt.conditionalUpdates)
			if difference := cmp.Diff(tt.expected, actual, cmpopts.IgnoreFields(metav1.Condition{}, "LastTransitionTime")); difference != "" {
				t.Errorf("actual differ from expected:\n%s", difference)
			}
		})
	}
}

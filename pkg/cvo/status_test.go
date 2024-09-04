package cvo

import (
	"context"
	"fmt"
	"reflect"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"github.com/openshift/cluster-version-operator/pkg/payload"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/diff"
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
								{Type: ClusterStatusFailing, Status: configv1.ConditionTrue, Reason: "UpdatePayloadIntegrity", Message: "unable to apply object"},
								{Type: configv1.OperatorProgressing, Status: configv1.ConditionTrue, Message: "Working towards 4.0.1"},
								{Type: configv1.RetrievedUpdates, Status: configv1.ConditionFalse},
								{Type: ImplicitlyEnabledCapabilities, Status: "False", Reason: "AsExpected", Message: "Capabilities match configured spec"},
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
							{Type: ClusterStatusFailing, Status: configv1.ConditionTrue, Reason: "", Message: "bad"},
							{Type: configv1.OperatorProgressing, Status: configv1.ConditionTrue, Reason: "", Message: "Error ensuring the cluster version is up to date: bad"},
							{Type: configv1.RetrievedUpdates, Status: configv1.ConditionFalse},
							{Type: ImplicitlyEnabledCapabilities, Status: "False", Reason: "AsExpected", Message: "Capabilities match configured spec"},
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
				t.Fatalf("syncFailingStatus mutated input: %s", diff.ObjectReflectDiff(originalCopy, tt.original))
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
	unknownVersion                bool
	reconciliationIssuesCondition bool
}

func (f fakeRiFlags) UnknownVersion() bool {
	return f.unknownVersion
}

func (f fakeRiFlags) ReconciliationIssuesCondition() bool {
	return f.reconciliationIssuesCondition
}

func TestUpdateClusterVersionStatus_UnknownVersionAndReconciliationIssues(t *testing.T) {
	ignoreLastTransitionTime := cmpopts.IgnoreFields(configv1.ClusterOperatorStatusCondition{}, "LastTransitionTime")

	testCases := []struct {
		name string

		unknownVersion bool
		oldCondition   *configv1.ClusterOperatorStatusCondition
		failure        error

		expectedRiCondition *configv1.ClusterOperatorStatusCondition
	}{
		{
			name:                "ReconciliationIssues disabled, version known, no failure => condition not present",
			unknownVersion:      false,
			expectedRiCondition: nil,
		},
		{
			name:                "ReconciliationIssues disabled, version known, failure => condition not present",
			unknownVersion:      false,
			failure:             fmt.Errorf("Something happened"),
			expectedRiCondition: nil,
		},
		{
			name: "ReconciliationIssues disabled, version unknown, failure, existing condition => condition present",
			oldCondition: &configv1.ClusterOperatorStatusCondition{
				Type:    reconciliationIssuesConditionType,
				Status:  configv1.ConditionFalse,
				Reason:  noReconciliationIssuesReason,
				Message: "Happy condition is happy",
			},
			unknownVersion: true,
			failure:        fmt.Errorf("Something happened"),
			expectedRiCondition: &configv1.ClusterOperatorStatusCondition{
				Type:    reconciliationIssuesConditionType,
				Status:  configv1.ConditionTrue,
				Reason:  reconciliationIssuesFoundReason,
				Message: `{"message":"Something happened"}`,
			},
		},
		{
			name:                "ReconciliationIssues disabled, version unknown, failure, no existing condition => condition not present",
			unknownVersion:      true,
			failure:             fmt.Errorf("Something happened"),
			expectedRiCondition: nil,
		},
	}
	for _, tc := range testCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			gates := fakeRiFlags{
				unknownVersion:                tc.unknownVersion,
				reconciliationIssuesCondition: false,
			}
			release := configv1.Release{}
			getAvailableUpdates := func() *availableUpdates { return nil }
			var noErrors field.ErrorList
			cvStatus := configv1.ClusterVersionStatus{}
			if tc.oldCondition != nil {
				cvStatus.Conditions = append(cvStatus.Conditions, *tc.oldCondition)
			}
			updateClusterVersionStatus(&cvStatus, &SyncWorkerStatus{Failure: tc.failure}, release, getAvailableUpdates, gates, noErrors)
			condition := resourcemerge.FindOperatorStatusCondition(cvStatus.Conditions, reconciliationIssuesConditionType)
			if diff := cmp.Diff(tc.expectedRiCondition, condition, ignoreLastTransitionTime); diff != "" {
				t.Errorf("unexpected condition\n:%s", diff)
			}
		})

	}

}

func TestUpdateClusterVersionStatus_ReconciliationIssues(t *testing.T) {
	ignoreLastTransitionTime := cmpopts.IgnoreFields(configv1.ClusterOperatorStatusCondition{}, "LastTransitionTime")

	testCases := []struct {
		name             string
		syncWorkerStatus SyncWorkerStatus

		enabled bool

		expectedCondition *configv1.ClusterOperatorStatusCondition
	}{
		{
			name:             "ReconciliationIssues present and happy when gate is enabled and no failures happened",
			syncWorkerStatus: SyncWorkerStatus{},
			enabled:          true,
			expectedCondition: &configv1.ClusterOperatorStatusCondition{
				Type:    reconciliationIssuesConditionType,
				Status:  configv1.ConditionFalse,
				Reason:  noReconciliationIssuesReason,
				Message: noReconciliationIssuesMessage,
			},
		},
		{
			name: "ReconciliationIssues present and unhappy when gate is enabled and failures happened",
			syncWorkerStatus: SyncWorkerStatus{
				Failure: fmt.Errorf("Something happened"),
			},
			enabled: true,
			expectedCondition: &configv1.ClusterOperatorStatusCondition{
				Type:    reconciliationIssuesConditionType,
				Status:  configv1.ConditionTrue,
				Reason:  reconciliationIssuesFoundReason,
				Message: `{"message":"Something happened"}`,
			},
		},
		{
			name: "ReconciliationIssues not present when gate is enabled and failures happened",
			syncWorkerStatus: SyncWorkerStatus{
				Failure: fmt.Errorf("Something happened"),
			},
			enabled:           false,
			expectedCondition: nil,
		},
	}

	for _, tc := range testCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			gates := fakeRiFlags{
				unknownVersion:                false,
				reconciliationIssuesCondition: tc.enabled,
			}
			release := configv1.Release{}
			getAvailableUpdates := func() *availableUpdates { return nil }
			var noErrors field.ErrorList
			cvStatus := configv1.ClusterVersionStatus{}
			updateClusterVersionStatus(&cvStatus, &tc.syncWorkerStatus, release, getAvailableUpdates, gates, noErrors)
			condition := resourcemerge.FindOperatorStatusCondition(cvStatus.Conditions, reconciliationIssuesConditionType)
			if diff := cmp.Diff(tc.expectedCondition, condition, ignoreLastTransitionTime); diff != "" {
				t.Errorf("unexpected condition\n:%s", diff)
			}
		})
	}
}

func TestUpdateClusterVersionStatus_FilteringMultipleErrorsForFailingCondition(t *testing.T) {
	ignoreLastTransitionTime := cmpopts.IgnoreFields(configv1.ClusterOperatorStatusCondition{}, "LastTransitionTime")
	type args struct {
		syncWorkerStatus *SyncWorkerStatus
	}
	tests := []struct {
		name                                             string
		args                                             args
		shouldModifyWhenNotReconcilingAndHistoryNotEmpty bool
		expectedConditionNotModified                     *configv1.ClusterOperatorStatusCondition
		expectedConditionModified                        *configv1.ClusterOperatorStatusCondition
	}{
		{
			name: "no errors are present",
			args: args{
				syncWorkerStatus: &SyncWorkerStatus{
					Failure: nil,
				},
			},
			expectedConditionNotModified: &configv1.ClusterOperatorStatusCondition{
				Type:   ClusterStatusFailing,
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
				Type:    ClusterStatusFailing,
				Status:  configv1.ConditionTrue,
				Message: "error has happened",
			},
		},
		{
			name: "single UpdateEffectNone error",
			args: args{
				syncWorkerStatus: &SyncWorkerStatus{
					Failure: &payload.UpdateError{
						UpdateEffect: payload.UpdateEffectNone,
						Reason:       "ClusterOperatorUpdating",
						Message:      "Cluster operator A is updating",
					},
				},
			},
			expectedConditionNotModified: &configv1.ClusterOperatorStatusCondition{
				Type:    ClusterStatusFailing,
				Status:  configv1.ConditionTrue,
				Reason:  "ClusterOperatorUpdating",
				Message: "Cluster operator A is updating",
			},
			shouldModifyWhenNotReconcilingAndHistoryNotEmpty: true,
			expectedConditionModified: &configv1.ClusterOperatorStatusCondition{
				Type:   ClusterStatusFailing,
				Status: configv1.ConditionFalse,
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
				Type:    ClusterStatusFailing,
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
				Type:    ClusterStatusFailing,
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
				Type:    ClusterStatusFailing,
				Status:  configv1.ConditionTrue,
				Reason:  "MultipleErrors",
				Message: "Multiple errors are preventing progress:\n* Cluster operator A is not available\n* Cluster operator B is updating versions",
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
				Type:    ClusterStatusFailing,
				Status:  configv1.ConditionTrue,
				Reason:  "MultipleErrors",
				Message: "Multiple errors are preventing progress:\n* Cluster operator A is updating versions\n* Cluster operator B is getting conscious",
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
				Type:    ClusterStatusFailing,
				Status:  configv1.ConditionTrue,
				Reason:  "MultipleErrors",
				Message: "Multiple errors are preventing progress:\n* Cluster operator A is not available\n* Cluster operator B is updating versions\n* Cluster operator C is degraded",
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
			for _, c := range combinations {
				tc.args.syncWorkerStatus.Reconciling = c.isReconciling
				cvStatus := &configv1.ClusterVersionStatus{}
				if !c.isHistoryEmpty {
					cvStatus.History = []configv1.UpdateHistory{{State: configv1.PartialUpdate}}
				}
				expectedCondition := tc.expectedConditionNotModified
				if tc.shouldModifyWhenNotReconcilingAndHistoryNotEmpty && !c.isReconciling && !c.isHistoryEmpty {
					expectedCondition = tc.expectedConditionModified
				}
				updateClusterVersionStatus(cvStatus, tc.args.syncWorkerStatus, release, getAvailableUpdates, gates, noErrors)
				condition := resourcemerge.FindOperatorStatusCondition(cvStatus.Conditions, ClusterStatusFailing)
				if diff := cmp.Diff(expectedCondition, condition, ignoreLastTransitionTime); diff != "" {
					t.Errorf("unexpected condition when Reconciling == %t && isHistoryEmpty == %t\n:%s", c.isReconciling, c.isHistoryEmpty, diff)
				}
			}
		})
	}
}

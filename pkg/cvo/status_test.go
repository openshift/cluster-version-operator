package cvo

import (
	"fmt"
	"reflect"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/diff"

	configv1 "github.com/openshift/api/config/v1"
	"github.com/openshift/client-go/config/clientset/versioned/fake"
)

func Test_mergeEqualVersions(t *testing.T) {
	type args struct {
	}
	tests := []struct {
		name    string
		current *configv1.UpdateHistory
		desired configv1.Update
		want    bool
	}{
		{
			current: &configv1.UpdateHistory{Image: "test:1", Version: "0.0.1"},
			desired: configv1.Update{Image: "test:1", Version: "0.0.1"},
			want:    true,
		},
		{
			current: &configv1.UpdateHistory{Image: "test:1", Version: ""},
			desired: configv1.Update{Image: "test:1", Version: "0.0.1"},
			want:    true,
		},
		{
			current: &configv1.UpdateHistory{Image: "test:1", Version: "0.0.1"},
			desired: configv1.Update{Image: "test:1", Version: ""},
			want:    true,
		},
		{
			current: &configv1.UpdateHistory{Image: "test:1", Version: "0.0.1"},
			desired: configv1.Update{Image: "", Version: "0.0.1"},
			want:    false,
		},
		{
			current: &configv1.UpdateHistory{Image: "test:1", Version: "0.0.1"},
			desired: configv1.Update{Image: "test:2", Version: "0.0.1"},
			want:    false,
		},
		{
			current: &configv1.UpdateHistory{Image: "test:1", Version: "0.0.1"},
			desired: configv1.Update{Image: "test:1", Version: "0.0.2"},
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

func Test_pruneStatusHistory(t *testing.T) {
	obj := &configv1.ClusterVersion{
		Status: configv1.ClusterVersionStatus{
			History: []configv1.UpdateHistory{
				{State: configv1.PartialUpgrade, Version: "0.0.10"},
				{State: configv1.PartialUpgrade, Version: "0.0.9"},
				{State: configv1.PartialUpgrade, Version: "0.0.8"},
				{State: configv1.CompletedUpgrade, Version: "0.0.7"},
				{State: configv1.PartialUpgrade, Version: "0.0.6"},
			},
		},
	}
	tests := []struct {
		name       string
		config     *configv1.ClusterVersion
		maxHistory int
		want       []configv1.UpdateHistory
	}{
		{
			config:     obj.DeepCopy(),
			maxHistory: 2,
			want: []configv1.UpdateHistory{
				{State: configv1.PartialUpgrade, Version: "0.0.10"},
				{State: configv1.CompletedUpgrade, Version: "0.0.7"},
			},
		},
		{
			config:     obj.DeepCopy(),
			maxHistory: 3,
			want: []configv1.UpdateHistory{
				{State: configv1.PartialUpgrade, Version: "0.0.10"},
				{State: configv1.PartialUpgrade, Version: "0.0.9"},
				{State: configv1.CompletedUpgrade, Version: "0.0.7"},
			},
		},
		{
			config:     obj.DeepCopy(),
			maxHistory: 4,
			want: []configv1.UpdateHistory{
				{State: configv1.PartialUpgrade, Version: "0.0.10"},
				{State: configv1.PartialUpgrade, Version: "0.0.9"},
				{State: configv1.PartialUpgrade, Version: "0.0.8"},
				{State: configv1.CompletedUpgrade, Version: "0.0.7"},
			},
		},
		{
			config:     obj.DeepCopy(),
			maxHistory: 5,
			want: []configv1.UpdateHistory{
				{State: configv1.PartialUpgrade, Version: "0.0.10"},
				{State: configv1.PartialUpgrade, Version: "0.0.9"},
				{State: configv1.PartialUpgrade, Version: "0.0.8"},
				{State: configv1.CompletedUpgrade, Version: "0.0.7"},
				{State: configv1.PartialUpgrade, Version: "0.0.6"},
			},
		},
		{
			config:     obj.DeepCopy(),
			maxHistory: 6,
			want: []configv1.UpdateHistory{
				{State: configv1.PartialUpgrade, Version: "0.0.10"},
				{State: configv1.PartialUpgrade, Version: "0.0.9"},
				{State: configv1.PartialUpgrade, Version: "0.0.8"},
				{State: configv1.CompletedUpgrade, Version: "0.0.7"},
				{State: configv1.PartialUpgrade, Version: "0.0.6"},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			config := tt.config.DeepCopy()
			pruneStatusHistory(config, tt.maxHistory)
			if !reflect.DeepEqual(tt.want, config.Status.History) {
				t.Fatalf("%s", diff.ObjectReflectDiff(tt.want, config.Status.History))
			}
		})
	}
}

func TestOperator_syncFailingStatus(t *testing.T) {
	type args struct {
	}
	tests := []struct {
		name        string
		optr        Operator
		init        func(optr *Operator)
		wantErr     func(*testing.T, error)
		wantActions func(*testing.T, *Operator)
		wantSync    []configv1.Update

		original *configv1.ClusterVersion
		ierr     error
	}{
		{
			ierr: fmt.Errorf("bad"),
			optr: Operator{
				releaseVersion: "4.0.1",
				releaseImage:   "image/image:v4.0.1",
				namespace:      "test",
				name:           "default",
				client: fakeClientsetWithUpgrades(
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
							Desired:     configv1.Update{Version: "4.0.1", Image: "image/image:v4.0.1"},
							VersionHash: "",
							Conditions: []configv1.ClusterOperatorStatusCondition{
								{Type: configv1.OperatorAvailable, Status: configv1.ConditionFalse},
								{Type: ClusterStatusFailing, Status: configv1.ConditionTrue, Reason: "UpdatePayloadIntegrity", Message: "unable to apply object"},
								{Type: configv1.OperatorProgressing, Status: configv1.ConditionTrue, Message: "Working towards 4.0.1"},
								{Type: configv1.RetrievedUpdates, Status: configv1.ConditionFalse},
							},
						},
					},
				),
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
				expectUpgradeStatus(t, act[1], "clusterversions", "", &configv1.ClusterVersion{
					ObjectMeta: metav1.ObjectMeta{
						Name: "default",
					},
					Spec: configv1.ClusterVersionSpec{
						Channel: "fast",
					},
					Status: configv1.ClusterVersionStatus{
						History: []configv1.UpdateHistory{
							{State: configv1.PartialUpgrade, Version: "4.0.1", Image: "image/image:v4.0.1", StartedTime: defaultStartedTime},
						},
						Desired:     configv1.Update{Version: "4.0.1", Image: "image/image:v4.0.1"},
						VersionHash: "",
						Conditions: []configv1.ClusterOperatorStatusCondition{
							{Type: configv1.OperatorAvailable, Status: configv1.ConditionFalse},
							{Type: ClusterStatusFailing, Status: configv1.ConditionTrue, Reason: "", Message: "bad"},
							{Type: configv1.OperatorProgressing, Status: configv1.ConditionTrue, Reason: "", Message: "Error ensuring the cluster version is up to date: bad"},
							{Type: configv1.RetrievedUpdates, Status: configv1.ConditionFalse},
						},
					},
				})
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			optr := &tt.optr
			if tt.init != nil {
				tt.init(optr)
			}
			optr.cvLister = &clientCVLister{client: optr.client}
			optr.coLister = &clientCOLister{client: optr.client}

			var originalCopy *configv1.ClusterVersion
			if tt.original != nil {
				originalCopy = tt.original.DeepCopy()
			}

			err := optr.syncFailingStatus(tt.original, tt.ierr)

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

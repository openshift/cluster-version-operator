package upgradeable_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/tools/cache"

	configv1 "github.com/openshift/api/config/v1"
	"github.com/openshift/client-go/config/clientset/versioned/fake"
	configinformersv1 "github.com/openshift/client-go/config/informers/externalversions"

	"github.com/openshift/cluster-version-operator/lib/resourcemerge"
	"github.com/openshift/cluster-version-operator/pkg/risk/upgradeable"
)

func Test_New(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	co := &configv1.ClusterOperator{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-operator",
		},
		Status: configv1.ClusterOperatorStatus{
			Conditions: []configv1.ClusterOperatorStatusCondition{
				{
					Type:   configv1.OperatorUpgradeable,
					Status: configv1.ConditionTrue,
				},
			},
		},
	}
	fakeClient := fake.NewClientset(co)
	informerFactory := configinformersv1.NewSharedInformerFactory(fakeClient, 0)
	coInformer := informerFactory.Config().V1().ClusterOperators()

	changeChannel := make(chan struct{})
	changeCallback := func() {
		t.Logf("%s sending change notification", time.Now())
		changeChannel <- struct{}{}
	}

	currentVersion := func() configv1.Release {
		return configv1.Release{Version: "4.21.0"}
	}
	versions := []string{"4.21.1", "4.22.0", "5.0.0"}

	expectedName := "ClusterOperatorUpgradeable"
	source, err := upgradeable.New(expectedName, currentVersion, coInformer, changeCallback)
	if err != nil {
		t.Fatal(err)
	}

	t.Run("name", func(t *testing.T) {
		if name := source.Name(); name != expectedName {
			t.Errorf("unexpected name %q diverges from expected %q", name, expectedName)
		}
	})

	informerFactory.Start(ctx.Done())
	cache.WaitForCacheSync(ctx.Done(), coInformer.Informer().HasSynced)

	drained := false
	for !drained { // drain any early notifications
		select {
		case <-changeChannel:
			t.Log("received early change notification")
			continue
		default:
			t.Log("early change notifications drained")
			drained = true
		}
	}

	t.Run("quick change notification on Upgradeable change to False", func(t *testing.T) {
		resourcemerge.SetOperatorStatusCondition(&co.Status.Conditions, configv1.ClusterOperatorStatusCondition{
			Type:    configv1.OperatorUpgradeable,
			Status:  configv1.ConditionFalse,
			Reason:  "TestReason",
			Message: "Cluster is not upgradeable for testing.",
		})

		select {
		case <-changeChannel:
			t.Fatalf("updating the local pointer triggered a change notification, when it should have taken until the fake client update")
		case <-time.After(1 * time.Second):
			t.Logf("updating the ClusterOperator to %s=%s to trigger a change", configv1.OperatorUpgradeable, configv1.ConditionFalse)
		}
		start := time.Now()

		_, err := fakeClient.ConfigV1().ClusterOperators().Update(ctx, co, metav1.UpdateOptions{})
		if err != nil {
			t.Fatalf("failed to update ClusterOperator: %v", err)
		}

		select {
		case <-changeChannel:
			end := time.Now()
			t.Logf("received change notification %s after %s", end, end.Sub(start))
		case <-time.After(1 * time.Second):
			t.Fatalf("did not receive change notification within one second after ClusterOperator change")
		}

		t.Run("expected Risks output", func(t *testing.T) {
			expectedRisks := []configv1.ConditionalUpdateRisk{{
				Name:          "ClusterOperatorUpgradeable-test-operator",
				Message:       "Cluster is not upgradeable for testing.",
				URL:           "https://docs.redhat.com/en/documentation/openshift_container_platform/4.21/html/updating_clusters/understanding-openshift-updates-1#understanding_clusteroperator_conditiontypes_understanding-openshift-updates",
				MatchingRules: []configv1.ClusterCondition{{Type: "Always"}},
			}}
			expectedVersions := map[string][]string{
				"ClusterOperatorUpgradeable-test-operator": {"4.22.0", "5.0.0"},
			}
			risks, versionsMap, err := source.Risks(ctx, versions)
			if err != nil {
				t.Errorf("unexpected error: %v", err)
			}
			if diff := cmp.Diff(expectedRisks, risks); diff != "" {
				t.Errorf("risk mismatch (-want +got):\n%s", diff)
			}
			if diff := cmp.Diff(expectedVersions, versionsMap); diff != "" {
				t.Errorf("versions mismatch (-want +got):\n%s", diff)
			}
		})
	})

	t.Run("no change notification on unrelated change", func(t *testing.T) {
		resourcemerge.SetOperatorStatusCondition(&co.Status.Conditions, configv1.ClusterOperatorStatusCondition{
			Type:    configv1.OperatorAvailable,
			Status:  configv1.ConditionFalse,
			Reason:  "TestReason",
			Message: "Cluster is not available for testing.",
		})

		t.Logf("updating the ClusterOperator to %s=%s, which should not trigger a change", configv1.OperatorAvailable, configv1.ConditionFalse)
		start := time.Now()
		_, err := fakeClient.ConfigV1().ClusterOperators().Update(ctx, co, metav1.UpdateOptions{})
		if err != nil {
			t.Fatalf("failed to update ClusterOperator: %v", err)
		}

		select {
		case <-changeChannel:
			end := time.Now()
			t.Fatalf("received change notification after %s", end.Sub(start))
		case <-time.After(1 * time.Second):
			t.Logf("did not receive change notification within one second after ClusterOperator change")
		}
	})

	t.Run("quick change notification on Upgradeable change to True", func(t *testing.T) {
		if resourcemerge.IsOperatorStatusConditionTrue(co.Status.Conditions, configv1.OperatorUpgradeable) {
			t.Skip("cannot test change-to-True unless the earlier change-to-False test case ran successfully")
		}
		resourcemerge.SetOperatorStatusCondition(&co.Status.Conditions, configv1.ClusterOperatorStatusCondition{
			Type:    configv1.OperatorUpgradeable,
			Status:  configv1.ConditionTrue,
			Reason:  "TestReason",
			Message: "Cluster is upgradeable for testing.",
		})

		t.Logf("updating the ClusterOperator to %s=%s, which should trigger a change", configv1.OperatorUpgradeable, configv1.ConditionTrue)
		start := time.Now()
		_, err := fakeClient.ConfigV1().ClusterOperators().Update(ctx, co, metav1.UpdateOptions{})
		if err != nil {
			t.Fatalf("failed to update ClusterOperator: %v", err)
		}

		select {
		case <-changeChannel:
			end := time.Now()
			t.Logf("received change notification %s after %s", end, end.Sub(start))
		case <-time.After(1 * time.Second):
			t.Fatalf("did not receive change notification within one second after ClusterOperator change")
		}

		t.Run("expected Risks output", func(t *testing.T) {
			var expectedRisks []configv1.ConditionalUpdateRisk
			var expectedVersions map[string][]string
			risks, versionsMap, err := source.Risks(ctx, versions)
			if err != nil {
				t.Errorf("unexpected error: %v", err)
			}
			if diff := cmp.Diff(expectedRisks, risks); diff != "" {
				t.Errorf("risk mismatch (-want +got):\n%s", diff)
			}
			if diff := cmp.Diff(expectedVersions, versionsMap); diff != "" {
				t.Errorf("versions mismatch (-want +got):\n%s", diff)
			}
		})
	})
}

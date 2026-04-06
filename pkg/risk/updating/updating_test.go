package updating_test

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
	"github.com/openshift/cluster-version-operator/pkg/risk/updating"
)

func Test_New(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	cv := &configv1.ClusterVersion{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-version",
		},
		Status: configv1.ClusterVersionStatus{
			Desired: configv1.Release{Version: "4.21.0"},
		},
	}
	fakeClient := fake.NewClientset(cv)
	informerFactory := configinformersv1.NewSharedInformerFactory(fakeClient, 0)
	cvInformer := informerFactory.Config().V1().ClusterVersions()

	changeChannel := make(chan struct{})
	changeCallback := func() {
		t.Logf("%s sending change notification", time.Now())
		changeChannel <- struct{}{}
	}

	versions := []string{"4.21.1", "4.22.0", "5.0.0"}

	expectedName := "ClusterVersionUpdating"
	source, err := updating.New(expectedName, cv.Name, cvInformer, changeCallback)
	if err != nil {
		t.Fatal(err)
	}

	t.Run("name", func(t *testing.T) {
		if name := source.Name(); name != expectedName {
			t.Errorf("unexpected name %q diverges from expected %q", name, expectedName)
		}
	})

	informerFactory.Start(ctx.Done())
	cache.WaitForCacheSync(ctx.Done(), cvInformer.Informer().HasSynced)

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

	t.Run("empty conditions", func(t *testing.T) {
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

	t.Run("quick change notification on progressing true", func(t *testing.T) {
		cv.Status.Conditions = []configv1.ClusterOperatorStatusCondition{{
			Type:    configv1.OperatorProgressing,
			Status:  configv1.ConditionTrue,
			Reason:  "UpdateInProgress",
			Message: "An update is already in progress and the details are in the Progressing condition.",
		}}

		t.Logf("updating the ClusterVersion to set %s=%s, which should trigger a quick change", configv1.OperatorProgressing, configv1.ConditionTrue)
		start := time.Now()
		_, err := fakeClient.ConfigV1().ClusterVersions().UpdateStatus(ctx, cv, metav1.UpdateOptions{})
		if err != nil {
			t.Fatalf("failed to update ClusterVersion: %v", err)
		}

		select {
		case <-changeChannel:
			end := time.Now()
			t.Logf("received change notification %s after %s", end, end.Sub(start))
		case <-time.After(1 * time.Second):
			t.Fatalf("did not receive change notification within one second after ClusterVersion change")
		}

		t.Run("expected Risks output", func(t *testing.T) {
			expectedRisks := []configv1.ConditionalUpdateRisk{{
				Name:          "ClusterVersionUpdating",
				Message:       "An update is already in progress and the details are in the Progressing condition.",
				URL:           "https://docs.redhat.com/en/documentation/openshift_container_platform/4.21/html/updating_clusters/understanding-openshift-updates-1#understanding_clusteroperator_conditiontypes_understanding-openshift-updates",
				MatchingRules: []configv1.ClusterCondition{{Type: "Always"}},
			}}
			expectedVersions := map[string][]string{
				"ClusterVersionUpdating": {"4.22.0", "5.0.0"},
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
		cv.Status.Desired.URL = "https://example.com/release-notes"

		t.Log("updating the ClusterVersion desired release's release notes, which should not trigger a change")
		start := time.Now()
		_, err := fakeClient.ConfigV1().ClusterVersions().UpdateStatus(ctx, cv, metav1.UpdateOptions{})
		if err != nil {
			t.Fatalf("failed to update ClusterVersion: %v", err)
		}

		select {
		case <-changeChannel:
			end := time.Now()
			t.Fatalf("received change notification after %s", end.Sub(start))
		case <-time.After(1 * time.Second):
			t.Logf("did not receive change notification within one second after ClusterVersion change")
		}
	})

	t.Run("quick change notification on progressing false", func(t *testing.T) {
		if !resourcemerge.IsOperatorStatusConditionTrue(cv.Status.Conditions, configv1.OperatorProgressing) {
			t.Skip("cannot test change-to-unset unless the earlier change-to-set test case ran successfully")
		}
		resourcemerge.SetOperatorStatusCondition(&cv.Status.Conditions, configv1.ClusterOperatorStatusCondition{
			Type:    configv1.OperatorProgressing,
			Status:  configv1.ConditionFalse,
			Reason:  "AsExpected",
			Message: "The cluster is running 4.21.0.",
		})

		t.Log("updating the ClusterVersion to unset overrides, which should trigger a change")
		start := time.Now()
		_, err := fakeClient.ConfigV1().ClusterVersions().UpdateStatus(ctx, cv, metav1.UpdateOptions{})
		if err != nil {
			t.Fatalf("failed to update ClusterVersion: %v", err)
		}

		select {
		case <-changeChannel:
			end := time.Now()
			t.Logf("received change notification %s after %s", end, end.Sub(start))
		case <-time.After(1 * time.Second):
			t.Fatalf("did not receive change notification within one second after ClusterVersion change")
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

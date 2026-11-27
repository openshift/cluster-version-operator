package cvo

import (
	"errors"
	"testing"

	"github.com/google/go-cmp/cmp"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	clienttesting "k8s.io/client-go/testing"

	configv1 "github.com/openshift/api/config/v1"
	configfake "github.com/openshift/client-go/config/clientset/versioned/fake"

	"github.com/openshift/cluster-version-operator/pkg/external"
)

func TestUpdateAcceptedRisksRetriesConflict(t *testing.T) {
	cv := &configv1.ClusterVersion{ObjectMeta: metav1.ObjectMeta{
		Name:            external.DefaultClusterVersionName,
		ResourceVersion: "1",
	}}
	client := configfake.NewSimpleClientset(cv)
	concurrent := cv.DeepCopy()
	concurrent.ResourceVersion = "2"
	concurrent.Spec.DesiredUpdate = &configv1.Update{
		Version: "5.2.1",
		Image:   "example.com/release:5.2.1",
		Force:   true,
		Mode:    configv1.UpdateModePreflight,
	}
	firstUpdate := true
	client.PrependReactor("update", "clusterversions", func(action clienttesting.Action) (bool, runtime.Object, error) {
		if !firstUpdate {
			return false, nil, nil
		}
		firstUpdate = false
		if err := client.Tracker().Update(action.GetResource(), concurrent, ""); err != nil {
			return true, nil, err
		}
		return true, nil, apierrors.NewConflict(action.GetResource().GroupResource(), cv.Name, errors.New("resource version changed"))
	})

	versions := client.ConfigV1().ClusterVersions()
	risks := []configv1.AcceptRisk{{Name: "TestRisk"}}
	if err := updateAcceptedRisks(t.Context(), versions, risks); err != nil {
		t.Fatalf("updating accepted risks: %v", err)
	}
	updated, err := versions.Get(t.Context(), cv.Name, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("getting updated ClusterVersion: %v", err)
	}
	want := concurrent.Spec.DesiredUpdate.DeepCopy()
	want.AcceptRisks = risks
	if diff := cmp.Diff(want, updated.Spec.DesiredUpdate); diff != "" {
		t.Errorf("unexpected desired update (-want +got):\n%s", diff)
	}
}

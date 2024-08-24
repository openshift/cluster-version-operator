package updatestatus

import (
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	configv1 "github.com/openshift/api/config/v1"
	configv1alpha "github.com/openshift/api/config/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

var ignoreTimes = cmpopts.IgnoreTypes(metav1.Time{})

func Test_updateStatusForClusterVersion(t *testing.T) {
	testCases := []struct {
		name string

		cpStatus *configv1alpha.ControlPlaneUpdateStatus
		cv       *configv1.ClusterVersion

		expected *configv1alpha.ControlPlaneUpdateStatus
	}{}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			updateStatusForClusterVersion(tc.cpStatus, tc.cv)

			if diff := cmp.Diff(tc.expected, tc.cpStatus, ignoreTimes); diff != "" {
				t.Errorf("updateStatusForClusterVersion() mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func Test_ensurePrototypeInformer(t *testing.T) {
	prototypeInformer := &configv1alpha.UpdateInformer{Name: prototypeInformerName}

	testCases := []struct {
		name      string
		informers []configv1alpha.UpdateInformer
		expected  *configv1alpha.UpdateInformer
	}{
		{
			name:     "no list",
			expected: prototypeInformer,
		},
		{
			name:      "empty list",
			informers: []configv1alpha.UpdateInformer{},
			expected:  prototypeInformer,
		},
		{
			name:      "list without prototype informer",
			informers: []configv1alpha.UpdateInformer{{Name: "foo"}},
			expected:  prototypeInformer,
		},
		{
			name: "list with prototype informer",
			informers: []configv1alpha.UpdateInformer{{
				Name:     prototypeInformerName,
				Insights: []configv1alpha.UpdateInsight{{Type: configv1alpha.UpdateInsightTypeClusterVersionStatusInsight}},
			}},
			expected: &configv1alpha.UpdateInformer{
				Name:     prototypeInformerName,
				Insights: []configv1alpha.UpdateInsight{{Type: configv1alpha.UpdateInsightTypeClusterVersionStatusInsight}},
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			informer := ensurePrototypeInformer(&tc.informers)
			if diff := cmp.Diff(tc.expected, informer); diff != "" {
				t.Fatalf("ensurePrototypeInformer() mismatch (-want +got):\n%s", diff)
			}
			find := findUpdateInformer(tc.informers, informer.Name)
			if diff := cmp.Diff(find, informer); diff != "" {
				t.Fatalf("ensurePrototypeInformer() did not add the informer to the list: %s", diff)
			}
		})
	}
}

func Test_ensureClusterVersionInsight(t *testing.T) {
	cvInsight := &configv1alpha.ClusterVersionStatusInsight{
		Resource: configv1alpha.ResourceRef{
			Kind:     "ClusterVersion",
			APIGroup: "config.openshift.io",
			Name:     "version",
		},
	}

	testCases := []struct {
		name     string
		insights []configv1alpha.UpdateInsight
		expected *configv1alpha.ClusterVersionStatusInsight
	}{
		{
			name:     "no list",
			expected: cvInsight,
		},
		{
			name:     "empty list",
			insights: []configv1alpha.UpdateInsight{},
			expected: cvInsight,
		},
		{
			name:     "list without cluster version insight",
			insights: []configv1alpha.UpdateInsight{{Type: configv1alpha.UpdateInsightTypeClusterOperatorStatusInsight}},
			expected: cvInsight,
		},
		{
			name: "list with cluster version insight",
			insights: []configv1alpha.UpdateInsight{
				{
					Type: configv1alpha.UpdateInsightTypeClusterVersionStatusInsight,
					ClusterVersionStatusInsight: &configv1alpha.ClusterVersionStatusInsight{
						Completion: 42,
					},
				},
			},
			expected: &configv1alpha.ClusterVersionStatusInsight{
				Completion: 42,
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			insight := ensureClusterVersionInsight(&tc.insights, "version")
			if diff := cmp.Diff(tc.expected, insight); diff != "" {
				t.Fatalf("ensureClusterVersionInsight() mismatch (-want +got):\n%s", diff)
			}
			find := findClusterVersionInsight(tc.insights)
			if diff := cmp.Diff(find, insight); diff != "" {
				t.Fatalf("ensureClusterVersionInsight() did not add the insight to the list: %s", diff)
			}
		})
	}
}

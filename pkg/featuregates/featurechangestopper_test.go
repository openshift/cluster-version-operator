package featuregates

import (
	"context"
	"testing"
	"time"

	configv1 "github.com/openshift/api/config/v1"
	fakeconfigv1client "github.com/openshift/client-go/config/clientset/versioned/fake"
	configv1informer "github.com/openshift/client-go/config/informers/externalversions"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestTechPreviewChangeStopper(t *testing.T) {
	versionForGates := "1.2.3"
	tests := []struct {
		name                       string
		startingRequiredFeatureSet string
		startingCvoFeatureGates    CvoGates

		featureSet        string
		featureGateStatus *configv1.FeatureGateStatus

		expectedShutdownCalled bool
	}{
		{
			name:                       "default-no-change",
			startingRequiredFeatureSet: "",
			featureSet:                 "",
			expectedShutdownCalled:     false,
		},
		{
			name:                       "default-with-change-to-tech-preview",
			startingRequiredFeatureSet: "",
			featureSet:                 "TechPreviewNoUpgrade",
			expectedShutdownCalled:     true,
		},
		{
			name:                       "default-with-change-to-other",
			startingRequiredFeatureSet: "",
			featureSet:                 "AnythingElse",
			expectedShutdownCalled:     true,
		},
		{
			name:                       "techpreview-to-techpreview",
			startingRequiredFeatureSet: "TechPreviewNoUpgrade",
			featureSet:                 "TechPreviewNoUpgrade",
			expectedShutdownCalled:     false,
		},
		{
			name:                       "techpreview-to-not-tech-preview", // this isn't allowed today
			startingRequiredFeatureSet: "TechPreviewNoUpgrade",
			featureSet:                 "",
			expectedShutdownCalled:     true,
		},
		{
			name:                       "cvo flags changed",
			startingRequiredFeatureSet: "TechPreviewNoUpgrade",
			startingCvoFeatureGates: CvoGates{
				UnknownVersion: true,
			},
			featureSet: "TechPreviewNoUpgrade",
			featureGateStatus: &configv1.FeatureGateStatus{
				FeatureGates: []configv1.FeatureGateDetails{
					{
						Version: versionForGates,
						Enabled: []configv1.FeatureGateAttributes{{Name: configv1.FeatureGateUpgradeStatus}},
					},
				},
			},
			expectedShutdownCalled: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()

			actualShutdownCalled := false
			shutdownFn := func() {
				actualShutdownCalled = true
			}

			fg := &configv1.FeatureGate{
				ObjectMeta: metav1.ObjectMeta{Name: "cluster"},
				Spec: configv1.FeatureGateSpec{
					FeatureGateSelection: configv1.FeatureGateSelection{
						FeatureSet: configv1.FeatureSet(tt.featureSet),
					},
				},
			}
			if tt.featureGateStatus != nil {
				fg.Status = *tt.featureGateStatus
			} else {
				fg.Status = configv1.FeatureGateStatus{}
				tt.startingCvoFeatureGates = CvoGates{UnknownVersion: true}
			}

			client := fakeconfigv1client.NewSimpleClientset(fg)

			informerFactory := configv1informer.NewSharedInformerFactory(client, 0)
			featureGates := informerFactory.Config().V1().FeatureGates()
			cf := ClusterFeatures{
				StartingRequiredFeatureSet: tt.startingRequiredFeatureSet,
				StartingCvoFeatureGates:    tt.startingCvoFeatureGates,
				VersionForGates:            versionForGates,
			}
			c, err := New(cf, featureGates)
			if err != nil {
				t.Fatal(err)
			}
			informerFactory.Start(ctx.Done())

			if err := c.Run(ctx, shutdownFn); err != nil {
				t.Fatal(err)
			}

			if actualShutdownCalled != tt.expectedShutdownCalled {
				t.Errorf("shutdown called %t, but expected %t", actualShutdownCalled, tt.expectedShutdownCalled)
			}
		})
	}
}

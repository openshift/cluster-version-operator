package featurechangestopper

import (
	"context"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	configv1 "github.com/openshift/api/config/v1"

	configlistersv1 "github.com/openshift/client-go/config/listers/config/v1"

	"k8s.io/client-go/tools/cache"
)

func TestTechPreviewChangeStopper_syncHandler(t *testing.T) {
	tests := []struct {
		name                     string
		startingTechPreviewState bool
		featureGate              string
		expectedShutdownCalled   bool
	}{
		{
			name:                     "default-no-change",
			startingTechPreviewState: false,
			featureGate:              "",
			expectedShutdownCalled:   false,
		},
		{
			name:                     "default-with-change-to-tech-preview",
			startingTechPreviewState: false,
			featureGate:              "TechPreviewNoUpgrade",
			expectedShutdownCalled:   true,
		},
		{
			name:                     "default-with-change-to-other",
			startingTechPreviewState: false,
			featureGate:              "AnythingElse",
			expectedShutdownCalled:   false,
		},
		{
			name:                     "techpreview-to-techpreview",
			startingTechPreviewState: true,
			featureGate:              "TechPreviewNoUpgrade",
			expectedShutdownCalled:   false,
		},
		{
			name:                     "techpreview-to-not-tech-preview", // this isn't allowed today
			startingTechPreviewState: true,
			featureGate:              "",
			expectedShutdownCalled:   true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {

			actualShutdownCalled := false
			shutdownFn := func() {
				actualShutdownCalled = true
			}
			indexer := cache.NewIndexer(cache.MetaNamespaceKeyFunc, cache.Indexers{})
			err := indexer.Add(&configv1.FeatureGate{
				ObjectMeta: metav1.ObjectMeta{Name: "cluster"},
				Spec: configv1.FeatureGateSpec{
					FeatureGateSelection: configv1.FeatureGateSelection{
						FeatureSet: configv1.FeatureSet(tt.featureGate),
					},
				},
			})
			if err != nil {
				t.Fatal(err)
			}
			lister := configlistersv1.NewFeatureGateLister(indexer)

			c := &TechPreviewChangeStopper{
				startingTechPreviewState: tt.startingTechPreviewState,
				featureGateLister:        lister,
			}

			err = c.syncHandler(context.Background(), shutdownFn)
			if err != nil {
				t.Fatal(err)
			}
			if actualShutdownCalled != tt.expectedShutdownCalled {
				t.Error(actualShutdownCalled)
			}

		})
	}
}

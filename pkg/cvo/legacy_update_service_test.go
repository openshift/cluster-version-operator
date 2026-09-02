package cvo

import (
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	configv1 "github.com/openshift/api/config/v1"

	"github.com/openshift/cluster-version-operator/lib/resourcemerge"
	"github.com/openshift/cluster-version-operator/pkg/internal"
)

func TestUpdateOKDLegacyUpdateServiceCondition(t *testing.T) {
	originalTransitionTime := metav1.NewTime(time.Unix(1, 0))
	tests := []struct {
		name                    string
		upstream                configv1.URL
		conditions              []configv1.ClusterOperatorStatusCondition
		want                    bool
		wantPreservedTransition bool
	}{
		{
			name:     "legacy update service",
			upstream: legacyOKDUpdateService,
			want:     true,
		},
		{
			name:     "custom update service",
			upstream: "https://example.com/graph",
		},
		{
			name:     "legacy URL with trailing slash does not match",
			upstream: legacyOKDUpdateService + "/",
		},
		{
			name: "default update service",
		},
		{
			name:     "resolved warning is removed",
			upstream: "https://example.com/graph",
			conditions: []configv1.ClusterOperatorStatusCondition{{
				Type:               internal.ClusterVersionOKDLegacyUpdateService,
				Status:             configv1.ConditionTrue,
				Reason:             "LegacyUpstreamConfigured",
				LastTransitionTime: originalTransitionTime,
			}},
		},
		{
			name:     "unchanged warning preserves transition time",
			upstream: legacyOKDUpdateService,
			conditions: []configv1.ClusterOperatorStatusCondition{{
				Type:               internal.ClusterVersionOKDLegacyUpdateService,
				Status:             configv1.ConditionTrue,
				Reason:             "LegacyUpstreamConfigured",
				LastTransitionTime: originalTransitionTime,
			}},
			want:                    true,
			wantPreservedTransition: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			status := &configv1.ClusterVersionStatus{Conditions: tt.conditions}
			UpdateOKDLegacyUpdateServiceCondition(status, tt.upstream)

			condition := resourcemerge.FindOperatorStatusCondition(status.Conditions, internal.ClusterVersionOKDLegacyUpdateService)
			if tt.want {
				if condition == nil {
					t.Fatal("LegacyUpdateService condition is missing")
				}
				if condition.Status != configv1.ConditionTrue || condition.Reason != "LegacyUpstreamConfigured" || condition.Message == "" {
					t.Fatalf("unexpected LegacyUpdateService condition: %#v", condition)
				}
				if tt.wantPreservedTransition && condition.LastTransitionTime != originalTransitionTime {
					t.Fatalf("lastTransitionTime = %v, want %v", condition.LastTransitionTime, originalTransitionTime)
				}
			} else if condition != nil {
				t.Fatalf("unexpected LegacyUpdateService condition: %#v", condition)
			}
		})
	}
}

package resourcebuilder

import (
	"strings"
	"testing"

	apiextv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestCheckCRDEstablished(t *testing.T) {
	tests := []struct {
		name      string
		crd       *apiextv1.CustomResourceDefinition
		wantErr   bool
		errSubstr string
	}{
		{
			name: "Established=True returns nil",
			crd: &apiextv1.CustomResourceDefinition{
				ObjectMeta: metav1.ObjectMeta{Name: "test.example.io"},
				Status: apiextv1.CustomResourceDefinitionStatus{
					Conditions: []apiextv1.CustomResourceDefinitionCondition{
						{
							Type:   apiextv1.NamesAccepted,
							Status: apiextv1.ConditionTrue,
						},
						{
							Type:   apiextv1.Established,
							Status: apiextv1.ConditionTrue,
						},
					},
				},
			},
			wantErr: false,
		},
		{
			name: "Established=False returns error with reason",
			crd: &apiextv1.CustomResourceDefinition{
				ObjectMeta: metav1.ObjectMeta{Name: "test.example.io"},
				Status: apiextv1.CustomResourceDefinitionStatus{
					Conditions: []apiextv1.CustomResourceDefinitionCondition{
						{
							Type:    apiextv1.Established,
							Status:  apiextv1.ConditionFalse,
							Reason:  "Installing",
							Message: "not yet established",
						},
					},
				},
			},
			wantErr:   true,
			errSubstr: "Established=False",
		},
		{
			name: "empty conditions returns error",
			crd: &apiextv1.CustomResourceDefinition{
				ObjectMeta: metav1.ObjectMeta{Name: "test.example.io"},
				Status: apiextv1.CustomResourceDefinitionStatus{
					Conditions: []apiextv1.CustomResourceDefinitionCondition{},
				},
			},
			wantErr:   true,
			errSubstr: "does not declare an Established status condition",
		},
		{
			name: "nil conditions returns error",
			crd: &apiextv1.CustomResourceDefinition{
				ObjectMeta: metav1.ObjectMeta{Name: "test.example.io"},
				Status:     apiextv1.CustomResourceDefinitionStatus{},
			},
			wantErr:   true,
			errSubstr: "does not declare an Established status condition",
		},
		{
			name: "NamesAccepted but no Established returns error",
			crd: &apiextv1.CustomResourceDefinition{
				ObjectMeta: metav1.ObjectMeta{Name: "test.example.io"},
				Status: apiextv1.CustomResourceDefinitionStatus{
					Conditions: []apiextv1.CustomResourceDefinitionCondition{
						{
							Type:   apiextv1.NamesAccepted,
							Status: apiextv1.ConditionTrue,
						},
					},
				},
			},
			wantErr:   true,
			errSubstr: "does not declare an Established status condition",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := checkCRDEstablished(tt.crd)
			if tt.wantErr {
				if err == nil {
					t.Error("expected error, got nil")
				} else if tt.errSubstr != "" {
					if !strings.Contains(err.Error(), tt.errSubstr) {
						t.Errorf("error %q does not contain %q", err.Error(), tt.errSubstr)
					}
				}
			} else {
				if err != nil {
					t.Errorf("expected nil error, got: %v", err)
				}
			}
		})
	}
}

func TestCheckCustomResourceDefinitionHealthInitializingMode(t *testing.T) {
	b := &builder{mode: InitializingMode}
	crd := &apiextv1.CustomResourceDefinition{
		ObjectMeta: metav1.ObjectMeta{Name: "test.example.io"},
		Status:     apiextv1.CustomResourceDefinitionStatus{},
	}
	if err := b.checkCustomResourceDefinitionHealth(t.Context(), crd); err != nil {
		t.Errorf("InitializingMode should skip health check, got: %v", err)
	}
}

func TestCheckCustomResourceDefinitionHealthEstablished(t *testing.T) {
	b := &builder{mode: ReconcilingMode}
	crd := &apiextv1.CustomResourceDefinition{
		ObjectMeta: metav1.ObjectMeta{Name: "test.example.io"},
		Status: apiextv1.CustomResourceDefinitionStatus{
			Conditions: []apiextv1.CustomResourceDefinitionCondition{
				{
					Type:   apiextv1.Established,
					Status: apiextv1.ConditionTrue,
				},
			},
		},
	}
	if err := b.checkCustomResourceDefinitionHealth(t.Context(), crd); err != nil {
		t.Errorf("Established=True CRD should pass health check, got: %v", err)
	}
}


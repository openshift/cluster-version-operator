package validation

import (
	"testing"

	"github.com/google/go-cmp/cmp"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/validation/field"

	configv1 "github.com/openshift/api/config/v1"
)

func TestValidateClusterVersion(t *testing.T) {
	tests := []struct {
		name                       string
		config                     *configv1.ClusterVersion
		shouldReconcileAcceptRisks bool
		expect                     field.ErrorList
	}{
		{
			name:                       "allow accept risks if shouldReconcileAcceptRisks is true",
			shouldReconcileAcceptRisks: true,
			config: &configv1.ClusterVersion{
				ObjectMeta: metav1.ObjectMeta{
					Name: "version",
				},
				Spec: configv1.ClusterVersionSpec{
					DesiredUpdate: &configv1.Update{
						AcceptRisks: []configv1.AcceptRisk{{
							Name: "SomeInfrastructureThing",
						}, {
							Name: "SomeChannelThing",
						}},
					},
				},
			},
		},
		{
			name: "forbid accept risks if shouldReconcileAcceptRisks is false",
			config: &configv1.ClusterVersion{
				ObjectMeta: metav1.ObjectMeta{
					Name: "version",
				},
				Spec: configv1.ClusterVersionSpec{
					DesiredUpdate: &configv1.Update{
						AcceptRisks: []configv1.AcceptRisk{{
							Name: "SomeInfrastructureThing",
						}, {
							Name: "SomeChannelThing",
						}},
					},
				},
			},
			expect: append(make(field.ErrorList, 0), field.Forbidden(field.NewPath("spec", "desiredUpdate", "acceptRisks"), "acceptRisks must not be specified")),
		},
		{
			name:                       "empty desired update is not allowed if shouldReconcileAcceptRisks is true",
			shouldReconcileAcceptRisks: true,
			config: &configv1.ClusterVersion{
				ObjectMeta: metav1.ObjectMeta{
					Name: "version",
				},
				Spec: configv1.ClusterVersionSpec{DesiredUpdate: &configv1.Update{}},
			},
			expect: append(make(field.ErrorList, 0), field.Required(field.NewPath("spec", "desiredUpdate"), "must specify architecture, version, image, or acceptRisks")),
		},
		{
			name: "empty desired update is not allowed if shouldReconcileAcceptRisks is false",
			config: &configv1.ClusterVersion{
				ObjectMeta: metav1.ObjectMeta{
					Name: "version",
				},
				Spec: configv1.ClusterVersionSpec{DesiredUpdate: &configv1.Update{}},
			},
			expect: append(make(field.ErrorList, 0), field.Required(field.NewPath("spec", "desiredUpdate"), "must specify architecture, version, or image")),
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			actual := ValidateClusterVersion(test.config, test.shouldReconcileAcceptRisks)
			if diff := cmp.Diff(test.expect, actual); diff != "" {
				t.Errorf("(-want, +got): %s", diff)
			}
		})
	}
}

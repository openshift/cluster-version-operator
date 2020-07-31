package resourcemerge

import (
	"k8s.io/apimachinery/pkg/api/equality"
	apiregv1beta1 "k8s.io/kube-aggregator/pkg/apis/apiregistration/v1beta1"
)

// EnsureAPIServicev1beta1 ensures that the existing matches the required.
// modified is set to true when existing had to be updated with required.
func EnsureAPIServicev1beta1(modified *bool, existing *apiregv1beta1.APIService, required apiregv1beta1.APIService) {
	EnsureObjectMeta(modified, &existing.ObjectMeta, required.ObjectMeta)

	// we stomp everything
	if !equality.Semantic.DeepEqual(existing.Spec, required.Spec) {
		*modified = true
		existing.Spec = required.Spec
	}
}

package resourceapply

import (
	"context"

	"github.com/openshift/cluster-version-operator/lib"
	"github.com/openshift/cluster-version-operator/lib/resourcemerge"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	apiregv1beta1 "k8s.io/kube-aggregator/pkg/apis/apiregistration/v1beta1"
	apiregclientv1beta1 "k8s.io/kube-aggregator/pkg/client/clientset_generated/clientset/typed/apiregistration/v1beta1"
	"k8s.io/utils/pointer"
)

func ApplyAPIServicev1beta1(ctx context.Context, client apiregclientv1beta1.APIServicesGetter, required *apiregv1beta1.APIService) (*apiregv1beta1.APIService, bool, error) {
	existing, err := client.APIServices().Get(ctx, required.Name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		actual, err := client.APIServices().Create(ctx, required, lib.Metav1CreateOptions())
		return actual, true, err
	}
	if err != nil {
		return nil, false, err
	}
	// if we only create this resource, we have no need to continue further
	if IsCreateOnly(required) {
		return nil, false, nil
	}

	modified := pointer.BoolPtr(false)
	resourcemerge.EnsureAPIServicev1beta1(modified, existing, *required)
	if !*modified {
		return existing, false, nil
	}

	actual, err := client.APIServices().Update(ctx, existing, metav1.UpdateOptions{})
	return actual, true, err
}

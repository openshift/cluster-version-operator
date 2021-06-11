package resourceapply

import (
	"context"

	"github.com/openshift/cluster-version-operator/lib/resourcemerge"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/diff"
	"k8s.io/klog/v2"
	apiregv1 "k8s.io/kube-aggregator/pkg/apis/apiregistration/v1"
	apiregclientv1 "k8s.io/kube-aggregator/pkg/client/clientset_generated/clientset/typed/apiregistration/v1"
	"k8s.io/utils/pointer"
)

func ApplyAPIServicev1(ctx context.Context, client apiregclientv1.APIServicesGetter, required *apiregv1.APIService) (*apiregv1.APIService, bool, error) {
	existing, err := client.APIServices().Get(ctx, required.Name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		klog.V(2).Infof("APIService %s not found, creating", required.Name)
		actual, err := client.APIServices().Create(ctx, required, metav1.CreateOptions{})
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
	resourcemerge.EnsureAPIService(modified, existing, *required)
	if !*modified {
		return existing, false, nil
	}
	klog.V(2).Infof("Updating APIService %s due to diff: %v", required.Name, diff.ObjectDiff(existing, required))

	actual, err := client.APIServices().Update(ctx, existing, metav1.UpdateOptions{})
	return actual, true, err
}

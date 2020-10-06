package resourceapply

import (
	"context"

	"github.com/openshift/cluster-version-operator/lib/resourcemerge"
	apiextv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	apiextv1beta1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1beta1"
	apiextclientv1 "k8s.io/apiextensions-apiserver/pkg/client/clientset/clientset/typed/apiextensions/v1"
	apiextclientv1beta1 "k8s.io/apiextensions-apiserver/pkg/client/clientset/clientset/typed/apiextensions/v1beta1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/klog/v2"
	"k8s.io/utils/pointer"
)

func ApplyCustomResourceDefinitionv1beta1(
	ctx context.Context,
	client apiextclientv1beta1.CustomResourceDefinitionsGetter,
	required *apiextv1beta1.CustomResourceDefinition,
) (*apiextv1beta1.CustomResourceDefinition, bool, error) {
	existing, err := client.CustomResourceDefinitions().Get(ctx, required.Name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		actual, err := client.CustomResourceDefinitions().Create(ctx, required, metav1.CreateOptions{})
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
	resourcemerge.EnsureCustomResourceDefinitionV1beta1(modified, existing, *required)
	if !*modified {
		return existing, false, nil
	}

	klog.V(2).Infof("Updating CRD %s", required.Name)

	actual, err := client.CustomResourceDefinitions().Update(ctx, existing, metav1.UpdateOptions{})
	return actual, true, err
}

func ApplyCustomResourceDefinitionv1(
	ctx context.Context,
	client apiextclientv1.CustomResourceDefinitionsGetter,
	required *apiextv1.CustomResourceDefinition,
) (*apiextv1.CustomResourceDefinition, bool, error) {
	existing, err := client.CustomResourceDefinitions().Get(ctx, required.Name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		actual, err := client.CustomResourceDefinitions().Create(ctx, required, metav1.CreateOptions{})
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
	resourcemerge.EnsureCustomResourceDefinitionV1(modified, existing, *required)
	if !*modified {
		return existing, false, nil
	}

	klog.V(2).Infof("Updating CRD %s", required.Name)

	actual, err := client.CustomResourceDefinitions().Update(ctx, existing, metav1.UpdateOptions{})
	return actual, true, err
}

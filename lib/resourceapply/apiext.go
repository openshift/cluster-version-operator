package resourceapply

import (
	"context"

	"github.com/google/go-cmp/cmp"
	"github.com/openshift/cluster-version-operator/lib/resourcemerge"
	apiextv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	apiextclientv1 "k8s.io/apiextensions-apiserver/pkg/client/clientset/clientset/typed/apiextensions/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/klog/v2"
	"k8s.io/utils/ptr"
)

func ApplyCustomResourceDefinitionv1(ctx context.Context, client apiextclientv1.CustomResourceDefinitionsGetter, required *apiextv1.CustomResourceDefinition, reconciling bool) (*apiextv1.CustomResourceDefinition, bool, error) {
	existing, err := client.CustomResourceDefinitions().Get(ctx, required.Name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		klog.V(2).Infof("CRD %s not found, creating", required.Name)
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

	var original apiextv1.CustomResourceDefinition
	existing.DeepCopyInto(&original)

	modified := ptr.To(false)
	resourcemerge.EnsureCustomResourceDefinitionV1(modified, existing, *required)
	if !*modified {
		return existing, false, nil
	}
	if reconciling {
		if diff := cmp.Diff(&original, existing); diff != "" {
			klog.V(2).Infof("Updating CRD %s due to diff: %v", required.Name, diff)
		} else {
			klog.V(2).Infof("Updating CRD %s with empty diff: possible hotloop after wrong comparison", required.Name)
		}
	}

	actual, err := client.CustomResourceDefinitions().Update(ctx, existing, metav1.UpdateOptions{})
	return actual, true, err
}

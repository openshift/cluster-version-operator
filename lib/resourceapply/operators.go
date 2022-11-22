package resourceapply

import (
	"context"

	"github.com/google/go-cmp/cmp"
	"k8s.io/klog/v2"

	operatorsv1 "github.com/operator-framework/api/pkg/operators/v1"
	operatorsclientv1 "github.com/operator-framework/operator-lifecycle-manager/pkg/api/client/clientset/versioned/typed/operators/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/pointer"

	"github.com/openshift/cluster-version-operator/lib/resourcemerge"
)

func ApplyOperatorGroupv1(ctx context.Context, client operatorsclientv1.OperatorGroupsGetter, required *operatorsv1.OperatorGroup, reconciling bool) (*operatorsv1.OperatorGroup, bool, error) {
	existing, err := client.OperatorGroups(required.Namespace).Get(ctx, required.Name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		klog.V(2).Infof("OperatorGroup %s/%s not found, creating", required.Namespace, required.Name)
		actual, err := client.OperatorGroups(required.Namespace).Create(ctx, required, metav1.CreateOptions{})
		return actual, true, err
	}
	if err != nil {
		return nil, false, err
	}
	// if we only create this resource, we have no need to continue further
	if IsCreateOnly(required) {
		return nil, false, nil
	}

	var original operatorsv1.OperatorGroup
	existing.DeepCopyInto(&original)

	modified := pointer.Bool(false)
	resourcemerge.EnsureOperatorGroup(modified, existing, *required)
	if !*modified {
		return existing, false, nil
	}
	if reconciling {
		if diff := cmp.Diff(&original, existing); diff != "" {
			klog.V(2).Infof("Updating OperatorGroup %s/%s due to diff: %v", required.Namespace, required.Name, diff)
		} else {
			klog.V(2).Infof("Updating OperatorGroup %s/%s with empty diff: possible hotloop after wrong comparison", required.Namespace, required.Name)
		}
	}

	actual, err := client.OperatorGroups(required.Namespace).Update(ctx, existing, metav1.UpdateOptions{})
	return actual, true, err
}

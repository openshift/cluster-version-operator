package resourceapply

import (
	"context"

	"github.com/google/go-cmp/cmp"
	monv1alpha1 "github.com/openshift/api/monitoring/v1alpha1"
	monclientv1alpha "github.com/openshift/client-go/monitoring/clientset/versioned/typed/monitoring/v1alpha1"
	"github.com/openshift/cluster-version-operator/lib/resourcemerge"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/klog/v2"
	"k8s.io/utils/pointer"
)

func ApplyPrometheusRulev1alpha1(ctx context.Context, client monclientv1alpha.AlertingRulesGetter, required *monv1alpha1.AlertingRule, reconciling bool) (*monv1alpha1.AlertingRule, bool, error) {
	existing, err := client.AlertingRules(required.Namespace).Get(ctx, required.Name, metav1.GetOptions{})
	if errors.IsNotFound(err) {
		actual, err := client.AlertingRules(required.Namespace).Create(ctx, required, metav1.CreateOptions{})
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
	resourcemerge.EnsurePrometheusRulev1alpha1(modified, existing, *required)
	if !*modified {
		return existing, false, nil
	}

	if reconciling {
		klog.V(2).Infof("Updating Namespace %s due to diff: %v", required.Name, cmp.Diff(existing, required))
	}
	actual, err := client.AlertingRules(required.Namespace).Update(ctx, existing, metav1.UpdateOptions{})
	return actual, true, err
}

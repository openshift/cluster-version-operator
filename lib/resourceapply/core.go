package resourceapply

import (
	"context"

	"github.com/google/go-cmp/cmp"

	"k8s.io/klog/v2"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	coreclientv1 "k8s.io/client-go/kubernetes/typed/core/v1"
	"k8s.io/utils/ptr"

	"github.com/openshift/cluster-version-operator/lib/resourcemerge"
)

// ApplyNamespacev1 merges objectmeta, does not worry about anything else
func ApplyNamespacev1(ctx context.Context, client coreclientv1.NamespacesGetter, required *corev1.Namespace, reconciling bool) (*corev1.Namespace, bool, error) {
	existing, err := client.Namespaces().Get(ctx, required.Name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		klog.V(2).Infof("Namespace %s not found, creating", required.Name)
		actual, err := client.Namespaces().Create(ctx, required, metav1.CreateOptions{})
		return actual, true, err
	}
	if err != nil {
		return nil, false, err
	}
	// if we only create this resource, we have no need to continue further
	if IsCreateOnly(required) {
		return nil, false, nil
	}

	modified := ptr.To(false)
	resourcemerge.EnsureObjectMeta(modified, &existing.ObjectMeta, required.ObjectMeta)
	if !*modified {
		return existing, false, nil
	}
	if reconciling {
		klog.V(2).Infof("Updating Namespace %s due to diff: %v", required.Name, cmp.Diff(existing, required))
	}

	actual, err := client.Namespaces().Update(ctx, existing, metav1.UpdateOptions{})
	return actual, true, err
}

// ApplyServicev1 merges objectmeta and requires
// TODO, since this cannot determine whether changes are due to legitimate actors (api server) or illegitimate ones (users), we cannot update
// TODO I've special cased the selector for now
func ApplyServicev1(ctx context.Context, client coreclientv1.ServicesGetter, required *corev1.Service, reconciling bool) (*corev1.Service, bool, error) {
	existing, err := client.Services(required.Namespace).Get(ctx, required.Name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		klog.V(2).Infof("Service %s/%s not found, creating", required.Namespace, required.Name)
		actual, err := client.Services(required.Namespace).Create(ctx, required, metav1.CreateOptions{})
		return actual, true, err
	}
	if err != nil {
		return nil, false, err
	}
	// if we only create this resource, we have no need to continue further
	if IsCreateOnly(required) {
		return nil, false, nil
	}

	modified := ptr.To(false)
	resourcemerge.EnsureObjectMeta(modified, &existing.ObjectMeta, required.ObjectMeta)
	resourcemerge.EnsureServicePorts(modified, &existing.Spec.Ports, required.Spec.Ports)
	resourcemerge.EnsureServiceType(modified, &existing.Spec.Type, required.Spec.Type)
	selectorSame := equality.Semantic.DeepEqual(existing.Spec.Selector, required.Spec.Selector)

	if selectorSame && !*modified {
		return nil, false, nil
	}
	existing.Spec.Selector = required.Spec.Selector

	if reconciling {
		klog.V(2).Infof("Updating Service %s/%s due to diff: %v", required.Namespace, required.Name, cmp.Diff(existing, required))
	}

	actual, err := client.Services(required.Namespace).Update(ctx, existing, metav1.UpdateOptions{})
	return actual, true, err
}

// ApplyServiceAccountv1 applies the required serviceaccount to the cluster.
func ApplyServiceAccountv1(ctx context.Context, client coreclientv1.ServiceAccountsGetter, required *corev1.ServiceAccount, reconciling bool) (*corev1.ServiceAccount, bool, error) {
	existing, err := client.ServiceAccounts(required.Namespace).Get(ctx, required.Name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		klog.V(2).Infof("ServiceAccount %s/%s not found, creating", required.Namespace, required.Name)
		actual, err := client.ServiceAccounts(required.Namespace).Create(ctx, required, metav1.CreateOptions{})
		return actual, true, err
	}
	if err != nil {
		return nil, false, err
	}
	// if we only create this resource, we have no need to continue further
	if IsCreateOnly(required) {
		return nil, false, nil
	}

	modified := ptr.To(false)
	resourcemerge.EnsureServiceAccount(modified, existing, *required)
	if !*modified {
		return existing, false, nil
	}

	if reconciling {
		klog.V(2).Infof("Updating ServiceAccount %s/%s due to diff: %v", required.Namespace, required.Name, cmp.Diff(existing, required))
	}

	actual, err := client.ServiceAccounts(required.Namespace).Update(ctx, existing, metav1.UpdateOptions{})
	return actual, true, err
}

// ApplyConfigMapv1 applies the required serviceaccount to the cluster.
func ApplyConfigMapv1(ctx context.Context, client coreclientv1.ConfigMapsGetter, required *corev1.ConfigMap, reconciling bool) (*corev1.ConfigMap, bool, error) {
	existing, err := client.ConfigMaps(required.Namespace).Get(ctx, required.Name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		klog.V(2).Infof("ConfigMap %s/%s not found, creating", required.Namespace, required.Name)
		actual, err := client.ConfigMaps(required.Namespace).Create(ctx, required, metav1.CreateOptions{})
		return actual, true, err
	}
	if err != nil {
		return nil, false, err
	}
	// if we only create this resource, we have no need to continue further
	if IsCreateOnly(required) {
		return nil, false, nil
	}

	modified := ptr.To(false)
	resourcemerge.EnsureConfigMap(modified, existing, *required)
	if !*modified {
		return existing, false, nil
	}

	if reconciling {
		klog.V(2).Infof("Updating ConfigMap %s/%s due to diff: %v", required.Namespace, required.Name, cmp.Diff(existing, required))
	}

	actual, err := client.ConfigMaps(required.Namespace).Update(ctx, existing, metav1.UpdateOptions{})
	return actual, true, err
}

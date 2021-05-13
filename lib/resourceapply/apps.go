package resourceapply

import (
	"context"

	"github.com/openshift/cluster-version-operator/lib/resourcemerge"
	appsv1 "k8s.io/api/apps/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/diff"
	appsclientv1 "k8s.io/client-go/kubernetes/typed/apps/v1"
	appslisterv1 "k8s.io/client-go/listers/apps/v1"
	"k8s.io/klog/v2"
	"k8s.io/utils/pointer"
)

// ApplyDeploymentv1 applies the required deployment to the cluster.
func ApplyDeploymentv1(ctx context.Context, client appsclientv1.DeploymentsGetter, required *appsv1.Deployment) (*appsv1.Deployment, bool, error) {
	existing, err := client.Deployments(required.Namespace).Get(ctx, required.Name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		klog.V(2).Infof("Deployment %s/%s not found, creating", required.Namespace, required.Name)
		actual, err := client.Deployments(required.Namespace).Create(ctx, required, metav1.CreateOptions{})
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
	resourcemerge.EnsureDeployment(modified, existing, *required)
	if !*modified {
		return existing, false, nil
	}
	klog.V(2).Infof("Updating Deployment %s/%s due to diff: %v", required.Namespace, required.Name, diff.ObjectDiff(existing, required))

	actual, err := client.Deployments(required.Namespace).Update(ctx, existing, metav1.UpdateOptions{})
	return actual, true, err
}

// ApplyDeploymentFromCache applies the required deployment to the cluster.
func ApplyDeploymentFromCache(ctx context.Context, lister appslisterv1.DeploymentLister, client appsclientv1.DeploymentsGetter, required *appsv1.Deployment) (*appsv1.Deployment, bool, error) {
	existing, err := lister.Deployments(required.Namespace).Get(required.Name)
	if apierrors.IsNotFound(err) {
		klog.V(2).Infof("Deployment %s/%s not found, creating", required.Namespace, required.Name)
		actual, err := client.Deployments(required.Namespace).Create(ctx, required, metav1.CreateOptions{})
		return actual, true, err
	}
	if err != nil {
		return nil, false, err
	}
	// if we only create this resource, we have no need to continue further
	if IsCreateOnly(required) {
		return nil, false, nil
	}

	existing = existing.DeepCopy()
	modified := pointer.BoolPtr(false)
	resourcemerge.EnsureDeployment(modified, existing, *required)
	if !*modified {
		return existing, false, nil
	}
	klog.V(2).Infof("Updating Deployment %s/%s due to diff: %v", required.Namespace, required.Name, diff.ObjectDiff(existing, required))

	actual, err := client.Deployments(required.Namespace).Update(ctx, existing, metav1.UpdateOptions{})
	return actual, true, err
}

// ApplyDaemonSetv1 applies the required daemonset to the cluster.
func ApplyDaemonSetv1(ctx context.Context, client appsclientv1.DaemonSetsGetter, required *appsv1.DaemonSet) (*appsv1.DaemonSet, bool, error) {
	existing, err := client.DaemonSets(required.Namespace).Get(ctx, required.Name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		klog.V(2).Infof("DaemonSet %s/%s not found, creating", required.Namespace, required.Name)
		actual, err := client.DaemonSets(required.Namespace).Create(ctx, required, metav1.CreateOptions{})
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
	resourcemerge.EnsureDaemonSet(modified, existing, *required)
	if !*modified {
		return existing, false, nil
	}

	klog.V(2).Infof("Updating DaemonSet %s/%s due to diff: %v", required.Namespace, required.Name, diff.ObjectDiff(existing, required))

	actual, err := client.DaemonSets(required.Namespace).Update(ctx, existing, metav1.UpdateOptions{})
	return actual, true, err
}

// ApplyDaemonSetFromCache applies the required deployment to the cluster.
func ApplyDaemonSetFromCache(ctx context.Context, lister appslisterv1.DaemonSetLister, client appsclientv1.DaemonSetsGetter, required *appsv1.DaemonSet) (*appsv1.DaemonSet, bool, error) {
	existing, err := lister.DaemonSets(required.Namespace).Get(required.Name)
	if apierrors.IsNotFound(err) {
		klog.V(2).Infof("DaemonSet %s/%s not found, creating", required.Namespace, required.Name)
		actual, err := client.DaemonSets(required.Namespace).Create(ctx, required, metav1.CreateOptions{})
		return actual, true, err
	}
	if err != nil {
		return nil, false, err
	}
	// if we only create this resource, we have no need to continue further
	if IsCreateOnly(required) {
		return nil, false, nil
	}

	existing = existing.DeepCopy()
	modified := pointer.BoolPtr(false)
	resourcemerge.EnsureDaemonSet(modified, existing, *required)
	if !*modified {
		return existing, false, nil
	}

	klog.V(2).Infof("Updating DaemonSet %s/%s due to diff: %v", required.Namespace, required.Name, diff.ObjectDiff(existing, required))

	actual, err := client.DaemonSets(required.Namespace).Update(ctx, existing, metav1.UpdateOptions{})
	return actual, true, err
}

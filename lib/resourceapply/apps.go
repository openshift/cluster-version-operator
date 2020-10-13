package resourceapply

import (
	"context"

	"github.com/openshift/cluster-version-operator/lib"
	"github.com/openshift/cluster-version-operator/lib/resourcemerge"
	appsv1 "k8s.io/api/apps/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	appsclientv1 "k8s.io/client-go/kubernetes/typed/apps/v1"
	appslisterv1 "k8s.io/client-go/listers/apps/v1"
	"k8s.io/utils/pointer"
)

// ApplyDeploymentv1 applies the required deployment to the cluster.
func ApplyDeploymentv1(ctx context.Context, client appsclientv1.DeploymentsGetter, required *appsv1.Deployment) (*appsv1.Deployment, bool, error) {
	existing, err := client.Deployments(required.Namespace).Get(ctx, required.Name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		actual, err := client.Deployments(required.Namespace).Create(ctx, required, lib.Metav1CreateOptions())
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

	actual, err := client.Deployments(required.Namespace).Update(ctx, existing, lib.Metav1UpdateOptions())
	return actual, true, err
}

// ApplyDeploymentFromCache applies the required deployment to the cluster.
func ApplyDeploymentFromCache(ctx context.Context, lister appslisterv1.DeploymentLister, client appsclientv1.DeploymentsGetter, required *appsv1.Deployment) (*appsv1.Deployment, bool, error) {
	existing, err := lister.Deployments(required.Namespace).Get(required.Name)
	if apierrors.IsNotFound(err) {
		actual, err := client.Deployments(required.Namespace).Create(ctx, required, lib.Metav1CreateOptions())
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

	actual, err := client.Deployments(required.Namespace).Update(ctx, existing, lib.Metav1UpdateOptions())
	return actual, true, err
}

// ApplyDaemonSetv1 applies the required daemonset to the cluster.
func ApplyDaemonSetv1(ctx context.Context, client appsclientv1.DaemonSetsGetter, required *appsv1.DaemonSet) (*appsv1.DaemonSet, bool, error) {
	existing, err := client.DaemonSets(required.Namespace).Get(ctx, required.Name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		actual, err := client.DaemonSets(required.Namespace).Create(ctx, required, lib.Metav1CreateOptions())
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

	actual, err := client.DaemonSets(required.Namespace).Update(ctx, existing, lib.Metav1UpdateOptions())
	return actual, true, err
}

// ApplyDaemonSetFromCache applies the required deployment to the cluster.
func ApplyDaemonSetFromCache(ctx context.Context, lister appslisterv1.DaemonSetLister, client appsclientv1.DaemonSetsGetter, required *appsv1.DaemonSet) (*appsv1.DaemonSet, bool, error) {
	existing, err := lister.DaemonSets(required.Namespace).Get(required.Name)
	if apierrors.IsNotFound(err) {
		actual, err := client.DaemonSets(required.Namespace).Create(ctx, required, lib.Metav1CreateOptions())
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

	actual, err := client.DaemonSets(required.Namespace).Update(ctx, existing, lib.Metav1UpdateOptions())
	return actual, true, err
}

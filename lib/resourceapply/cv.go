package resourceapply

import (
	"context"

	configv1 "github.com/openshift/api/config/v1"
	configclientv1 "github.com/openshift/client-go/config/clientset/versioned/typed/config/v1"
	configlistersv1 "github.com/openshift/client-go/config/listers/config/v1"
	"github.com/openshift/cluster-version-operator/lib"
	"github.com/openshift/cluster-version-operator/lib/resourcemerge"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/pointer"
)

func ApplyClusterVersionv1(ctx context.Context, client configclientv1.ClusterVersionsGetter, required *configv1.ClusterVersion) (*configv1.ClusterVersion, bool, error) {
	existing, err := client.ClusterVersions().Get(ctx, required.Name, metav1.GetOptions{})
	if errors.IsNotFound(err) {
		actual, err := client.ClusterVersions().Create(ctx, required, lib.Metav1CreateOptions())
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
	resourcemerge.EnsureClusterVersion(modified, existing, *required)
	if !*modified {
		return existing, false, nil
	}

	actual, err := client.ClusterVersions().Update(ctx, existing, metav1.UpdateOptions{})
	return actual, true, err
}

func ApplyClusterVersionFromCache(ctx context.Context, lister configlistersv1.ClusterVersionLister, client configclientv1.ClusterVersionsGetter, required *configv1.ClusterVersion) (*configv1.ClusterVersion, bool, error) {
	obj, err := lister.Get(required.Name)
	if errors.IsNotFound(err) {
		actual, err := client.ClusterVersions().Create(ctx, required, lib.Metav1CreateOptions())
		return actual, true, err
	}
	if err != nil {
		return nil, false, err
	}
	// if we only create this resource, we have no need to continue further
	if IsCreateOnly(required) {
		return nil, false, nil
	}

	// Don't want to mutate cache.
	existing := obj.DeepCopy()
	modified := pointer.BoolPtr(false)
	resourcemerge.EnsureClusterVersion(modified, existing, *required)
	if !*modified {
		return existing, false, nil
	}

	actual, err := client.ClusterVersions().Update(ctx, existing, metav1.UpdateOptions{})
	return actual, true, err
}

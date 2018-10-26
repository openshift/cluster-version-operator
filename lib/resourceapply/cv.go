package resourceapply

import (
	"fmt"

	"github.com/openshift/cluster-version-operator/lib/resourcemerge"
	cvv1 "github.com/openshift/cluster-version-operator/pkg/apis/config.openshift.io/v1"
	osv1 "github.com/openshift/cluster-version-operator/pkg/apis/operatorstatus.openshift.io/v1"
	cvclientv1 "github.com/openshift/cluster-version-operator/pkg/generated/clientset/versioned/typed/config.openshift.io/v1"
	osclientv1 "github.com/openshift/cluster-version-operator/pkg/generated/clientset/versioned/typed/operatorstatus.openshift.io/v1"
	cvlistersv1 "github.com/openshift/cluster-version-operator/pkg/generated/listers/config.openshift.io/v1"
	oslistersv1 "github.com/openshift/cluster-version-operator/pkg/generated/listers/operatorstatus.openshift.io/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/pointer"
)

func ApplyOperatorStatus(client osclientv1.ClusterOperatorsGetter, required *osv1.ClusterOperator) (*osv1.ClusterOperator, bool, error) {
	if required.Status.Extension.Raw != nil && required.Status.Extension.Object != nil {
		return nil, false, fmt.Errorf("both extension.Raw and extension.Object should not be set")
	}
	existing, err := client.ClusterOperators(required.Namespace).Get(required.Name, metav1.GetOptions{})
	if errors.IsNotFound(err) {
		actual, err := client.ClusterOperators(required.Namespace).Create(required)
		return actual, true, err
	}
	if err != nil {
		return nil, false, err
	}

	modified := pointer.BoolPtr(false)
	resourcemerge.EnsureOperatorStatus(modified, existing, *required)
	if !*modified {
		return existing, false, nil
	}

	actual, err := client.ClusterOperators(required.Namespace).Update(existing)
	return actual, true, err
}

func ApplyOperatorStatusFromCache(lister oslistersv1.ClusterOperatorLister, client osclientv1.ClusterOperatorsGetter, required *osv1.ClusterOperator) (*osv1.ClusterOperator, bool, error) {
	if required.Status.Extension.Raw != nil && required.Status.Extension.Object != nil {
		return nil, false, fmt.Errorf("both extension.Raw and extension.Object should not be set")
	}
	existing, err := lister.ClusterOperators(required.Namespace).Get(required.Name)
	if errors.IsNotFound(err) {
		actual, err := client.ClusterOperators(required.Namespace).Create(required)
		return actual, true, err
	}
	if err != nil {
		return nil, false, err
	}

	// Don't want to mutate cache.
	existing = existing.DeepCopy()
	modified := pointer.BoolPtr(false)
	resourcemerge.EnsureOperatorStatus(modified, existing, *required)
	if !*modified {
		return existing, false, nil
	}

	actual, err := client.ClusterOperators(required.Namespace).Update(existing)
	return actual, true, err
}

func ApplyClusterVersion(client cvclientv1.ClusterVersionsGetter, required *cvv1.ClusterVersion) (*cvv1.ClusterVersion, bool, error) {
	existing, err := client.ClusterVersions(required.Namespace).Get(required.Name, metav1.GetOptions{})
	if errors.IsNotFound(err) {
		actual, err := client.ClusterVersions(required.Namespace).Create(required)
		return actual, true, err
	}
	if err != nil {
		return nil, false, err
	}

	modified := pointer.BoolPtr(false)
	resourcemerge.EnsureClusterVersion(modified, existing, *required)
	if !*modified {
		return existing, false, nil
	}

	actual, err := client.ClusterVersions(required.Namespace).Update(existing)
	return actual, true, err
}

func ApplyClusterVersionFromCache(lister cvlistersv1.ClusterVersionLister, client cvclientv1.ClusterVersionsGetter, required *cvv1.ClusterVersion) (*cvv1.ClusterVersion, bool, error) {
	obj, err := lister.ClusterVersions(required.Namespace).Get(required.Name)
	if errors.IsNotFound(err) {
		actual, err := client.ClusterVersions(required.Namespace).Create(required)
		return actual, true, err
	}
	if err != nil {
		return nil, false, err
	}

	// Don't want to mutate cache.
	existing := new(cvv1.ClusterVersion)
	obj.DeepCopyInto(existing)
	modified := pointer.BoolPtr(false)
	resourcemerge.EnsureClusterVersion(modified, existing, *required)
	if !*modified {
		return existing, false, nil
	}

	actual, err := client.ClusterVersions(required.Namespace).Update(existing)
	return actual, true, err
}

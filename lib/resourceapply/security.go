package resourceapply

import (
	"context"

	securityv1 "github.com/openshift/api/security/v1"
	securityclientv1 "github.com/openshift/client-go/security/clientset/versioned/typed/security/v1"
	"github.com/openshift/cluster-version-operator/lib/resourcemerge"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/klog/v2"
	"k8s.io/utils/pointer"
)

// ApplySecurityContextConstraintsv1 applies the required SecurityContextConstraints to the cluster.
func ApplySecurityContextConstraintsv1(ctx context.Context, client securityclientv1.SecurityContextConstraintsGetter, required *securityv1.SecurityContextConstraints) (*securityv1.SecurityContextConstraints, bool, error) {
	existing, err := client.SecurityContextConstraints().Get(ctx, required.Name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		klog.V(2).Infof("SCC %s not found, creating", required.Name)
		actual, err := client.SecurityContextConstraints().Create(ctx, required, metav1.CreateOptions{})
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
	resourcemerge.EnsureObjectMeta(modified, &existing.ObjectMeta, required.ObjectMeta)
	if !*modified {
		return existing, false, nil
	}

	actual, err := client.SecurityContextConstraints().Update(ctx, existing, metav1.UpdateOptions{})
	return actual, true, err
}

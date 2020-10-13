package resourceapply

import (
	"context"

	"github.com/openshift/cluster-version-operator/lib"
	"github.com/openshift/cluster-version-operator/lib/resourcemerge"
	batchv1 "k8s.io/api/batch/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	batchclientv1 "k8s.io/client-go/kubernetes/typed/batch/v1"
	"k8s.io/utils/pointer"
)

// ApplyJobv1 applies the required Job to the cluster.
func ApplyJobv1(ctx context.Context, client batchclientv1.JobsGetter, required *batchv1.Job) (*batchv1.Job, bool, error) {
	existing, err := client.Jobs(required.Namespace).Get(ctx, required.Name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		actual, err := client.Jobs(required.Namespace).Create(ctx, required, lib.Metav1CreateOptions())
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
	resourcemerge.EnsureJob(modified, existing, *required)
	if !*modified {
		return existing, false, nil
	}

	actual, err := client.Jobs(required.Namespace).Update(ctx, existing, metav1.UpdateOptions{})
	return actual, true, err
}

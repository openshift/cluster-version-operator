package resourceapply

import (
	"context"

	"github.com/google/go-cmp/cmp"
	"github.com/openshift/cluster-version-operator/lib/resourcemerge"
	batchv1 "k8s.io/api/batch/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	batchclientv1 "k8s.io/client-go/kubernetes/typed/batch/v1"
	"k8s.io/klog/v2"
	"k8s.io/utils/ptr"
)

// ApplyJobv1 applies the required Job to the cluster.
func ApplyJobv1(ctx context.Context, client batchclientv1.JobsGetter, required *batchv1.Job, reconciling bool) (*batchv1.Job, bool, error) {
	existing, err := client.Jobs(required.Namespace).Get(ctx, required.Name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		klog.V(2).Infof("Job %s/%s not found, creating", required.Namespace, required.Name)
		actual, err := client.Jobs(required.Namespace).Create(ctx, required, metav1.CreateOptions{})
		return actual, true, err
	}
	if err != nil {
		return nil, false, err
	}
	// if we only create this resource, we have no need to continue further
	if IsCreateOnly(required) {
		return nil, false, nil
	}

	var original batchv1.Job
	existing.DeepCopyInto(&original)
	modified := ptr.To(false)
	resourcemerge.EnsureJob(modified, existing, *required)
	if !*modified {
		return existing, false, nil
	}
	if reconciling {
		if diff := cmp.Diff(&original, existing); diff != "" {
			klog.V(2).Infof("Updating Job %s/%s due to diff: %v", required.Namespace, required.Name, diff)
		} else {
			klog.V(2).Infof("Updating Job %s/%s with empty diff: possible hotloop after wrong comparison", required.Namespace, required.Name)
		}
	}

	actual, err := client.Jobs(required.Namespace).Update(ctx, existing, metav1.UpdateOptions{})
	return actual, true, err
}

// ApplyCronJobv1 applies the required CronJob to the cluster.
func ApplyCronJobv1(ctx context.Context, client batchclientv1.CronJobsGetter, required *batchv1.CronJob, reconciling bool) (*batchv1.CronJob, bool, error) {
	existing, err := client.CronJobs(required.Namespace).Get(ctx, required.Name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		klog.V(2).Infof("CronJob %s/%s not found, creating", required.Namespace, required.Name)
		actual, err := client.CronJobs(required.Namespace).Create(ctx, required, metav1.CreateOptions{})
		return actual, true, err
	}
	if err != nil {
		return nil, false, err
	}
	// if we only create this resource, we have no need to continue further
	if IsCreateOnly(required) {
		return nil, false, nil
	}

	var original batchv1.CronJob
	existing.DeepCopyInto(&original)
	modified := ptr.To(false)
	resourcemerge.EnsureCronJob(modified, existing, *required)
	if !*modified {
		return existing, false, nil
	}
	if reconciling {
		if diff := cmp.Diff(&original, existing); diff != "" {
			klog.V(2).Infof("Updating CronJob %s/%s due to diff: %v", required.Namespace, required.Name, diff)
		} else {
			klog.V(2).Infof("Updating CronJob %s/%s with empty diff: possible hotloop after wrong comparison", required.Namespace, required.Name)
		}
	}

	actual, err := client.CronJobs(required.Namespace).Update(ctx, existing, metav1.UpdateOptions{})
	return actual, true, err
}

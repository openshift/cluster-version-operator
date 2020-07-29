package resourcebuilder

import (
	"context"
	"fmt"

	batchv1 "k8s.io/api/batch/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	batchclientv1 "k8s.io/client-go/kubernetes/typed/batch/v1"
	"k8s.io/klog"
)

// WaitForJobCompletion waits for job to complete.
func WaitForJobCompletion(ctx context.Context, client batchclientv1.JobsGetter, job *batchv1.Job) error {
	return wait.PollImmediateUntil(defaultObjectPollInterval, func() (bool, error) {
		if done, err := checkJobHealth(ctx, client, job); err != nil {
			klog.Error(err)
			return false, nil
		} else if !done {
			klog.V(4).Infof("Job %s in namespace %s is not ready, continuing to wait.", job.ObjectMeta.Namespace, job.ObjectMeta.Name)
			return false, nil
		}
		return true, nil
	}, ctx.Done())
}

func (b *builder) checkJobHealth(ctx context.Context, job *batchv1.Job) error {
	if b.mode == InitializingMode {
		return nil
	}

	_, err := checkJobHealth(ctx, b.batchClientv1, job)
	return err
}

// checkJobHealth returns an error if the job status is bad enough to block further manifest application.
func checkJobHealth(ctx context.Context, client batchclientv1.JobsGetter, job *batchv1.Job) (bool, error) {
	j, err := client.Jobs(job.Namespace).Get(ctx, job.Name, metav1.GetOptions{})
	if err != nil {
		return false, fmt.Errorf("error getting Job %s: %v", job.Name, err)
	}

	if j.Status.Succeeded > 0 {
		return true, nil
	}

	// Since we have filled in "activeDeadlineSeconds",
	// the Job will 'Active == 0' if and only if it exceeds the deadline.
	// Failed jobs will be recreated in the next run.
	if j.Status.Active == 0 && j.Status.Failed > 0 {
		reason := "DeadlineExceeded"
		message := "Job was active longer than specified deadline"
		if len(j.Status.Conditions) > 0 {
			reason, message = j.Status.Conditions[0].Reason, j.Status.Conditions[0].Message
		}
		return false, fmt.Errorf("deadline exceeded, reason: %q, message: %q", reason, message)
	}
	return false, nil
}

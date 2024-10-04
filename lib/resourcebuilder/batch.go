package resourcebuilder

import (
	"context"
	"fmt"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	batchclientv1 "k8s.io/client-go/kubernetes/typed/batch/v1"
	"k8s.io/klog/v2"
)

func (b *builder) checkJobHealth(ctx context.Context, job *batchv1.Job) error {
	if b.mode == InitializingMode {
		return nil
	}

	_, err := checkJobHealth(ctx, job)
	return err
}

// checkJobHealth returns an error if the job status is bad enough to block further manifest application.
func checkJobHealth(ctx context.Context, job *batchv1.Job) (bool, error) {
	if job.Status.Succeeded > 0 {
		return true, nil
	}

	// Since we have filled in "activeDeadlineSeconds",
	// the Job will 'Active == 0' if and only if it exceeds the deadline or if the update image could not be pulled.
	// Failed jobs will be recreated in the next run.
	if job.Status.Active == 0 {
		klog.V(2).Infof("No active pods for job %s in namespace %s", job.Name, job.Namespace)
		failed, reason, message := hasJobFailed(job)
		// If there is more than one failed job pod then get the cause for failure
		if job.Status.Failed > 0 {
			failureReason := "DeadlineExceeded"
			failureMessage := "Job was active longer than specified deadline"
			if failed {
				failureReason, failureMessage = reason, message
			}
			return true, fmt.Errorf("deadline exceeded, reason: %q, message: %q", failureReason, failureMessage)
		}

		// When the update image cannot be pulled then the pod is not marked as Failed, but the status condition is set
		// after the job deadline is exceeded.
		if failed {
			if reason == "DeadlineExceeded" {
				return true, fmt.Errorf("deadline exceeded, reason: %q, message: %q", reason, message)
			} else {
				klog.V(2).Infof("Ignoring job %s in namespace %s with condition Failed=True because %s: %s", job.Name, job.Namespace, reason, message)
			}
		}
	}

	return false, nil
}

func hasJobFailed(job *batchv1.Job) (bool, string, string) {
	for _, condition := range job.Status.Conditions {
		if condition.Type == batchv1.JobFailed && condition.Status == corev1.ConditionTrue {
			return true, condition.Reason, condition.Message
		}
	}
	return false, "", ""
}

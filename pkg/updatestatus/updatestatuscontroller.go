package updatestatus

import (
	"context"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/klog/v2"

	configv1alpha1 "github.com/openshift/api/config/v1alpha1"
	configclientv1alpha1 "github.com/openshift/client-go/config/clientset/versioned/typed/config/v1alpha1"
	"github.com/openshift/library-go/pkg/controller/factory"
	"github.com/openshift/library-go/pkg/operator/events"
)

type updateStatusController struct {
	updateStatusClient configclientv1alpha1.UpdateStatusInterface
	recorder           events.Recorder

	// updateStatus is the resource instance local to the controller, on which the USC maintains
	// the most recently observed state. This local status is propagated to the cluster by this controller
	// when it changes.
	updateStatus *configv1alpha1.UpdateStatus
}

func newUpdateStatusController(updateStatusClient configclientv1alpha1.UpdateStatusInterface, eventsRecorder events.Recorder) factory.Controller {
	c := updateStatusController{
		updateStatusClient: updateStatusClient,
		recorder:           eventsRecorder,
	}

	return factory.New().WithSync(c.sync).ResyncEvery(time.Minute).ToController("UpdateStatusController", eventsRecorder.WithComponentSuffix("update-status-controller"))

}

func (c *updateStatusController) sync(ctx context.Context, syncCtx factory.SyncContext) error {
	klog.Info("USC :: SYNC")
	var exists bool
	updateStatus, err := c.updateStatusClient.Get(ctx, "another-cluster", metav1.GetOptions{})
	if err != nil && !apierrors.IsNotFound(err) {
		return err
	}
	exists = err == nil

	if c.updateStatus == nil && exists {
		c.updateStatus = updateStatus
		return nil
	}

	if c.updateStatus == nil && !exists {
		updateStatus = &configv1alpha1.UpdateStatus{
			// TypeMeta: metav1.TypeMeta{
			// 	Kind:       "UpdateStatus",
			// 	APIVersion: "config.openshift.io/v1alpha1",
			// },
			ObjectMeta: metav1.ObjectMeta{
				Name: "another-cluster",
			},
			Spec: configv1alpha1.UpdateStatusSpec{},
			Status: configv1alpha1.UpdateStatusStatus{
				ControlPlane: configv1alpha1.ControlPlaneUpdateStatus{
					Summary: configv1alpha1.ControlPlaneUpdateStatusSummary{
						Assessment:           "aaaa",
						Versions:             configv1alpha1.ControlPlaneUpdateVersions{},
						Completion:           0,
						StartedAt:            metav1.Time{},
						CompletedAt:          metav1.Time{},
						EstimatedCompletedAt: metav1.Time{},
					},
					Informers: []configv1alpha1.UpdateInformer{
						{
							Name: "YADA",
						},
					},
					Conditions: nil,
				},
				WorkerPools: nil,
				Conditions:  nil,
			},
		}
		updateStatus, err := c.updateStatusClient.Create(ctx, updateStatus, metav1.CreateOptions{})
		if err == nil {
			c.updateStatus = updateStatus
			syncCtx.Queue().AddRateLimited(syncCtx.QueueKey())
		}
		return err
	}

	_, err = c.updateStatusClient.UpdateStatus(ctx, c.updateStatus, metav1.UpdateOptions{})
	return err
}

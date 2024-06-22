package updatestatus

import (
	"context"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	configv1alpha1 "github.com/openshift/api/config/v1alpha1"
	configclientv1alpha1 "github.com/openshift/client-go/config/clientset/versioned/typed/config/v1alpha1"
	"github.com/openshift/library-go/pkg/controller/factory"
	"github.com/openshift/library-go/pkg/operator/events"
)

type Controller struct {
	updateStatusClient configclientv1alpha1.UpdateStatusInterface
	controllerFactory  *factory.Factory
	recorder           events.Recorder
}

func NewUpdateStatusController(updateStatusClient configclientv1alpha1.UpdateStatusInterface, eventsRecorder events.Recorder) factory.Controller {
	controller := Controller{
		updateStatusClient: updateStatusClient,
		controllerFactory:  factory.New().ResyncEvery(time.Minute),
		recorder:           eventsRecorder,
	}

	return controller.controllerFactory.WithSync(controller.sync).ToController("UpdateStatusController", eventsRecorder)
}

func (c *Controller) sync(ctx context.Context, syncCtx factory.SyncContext) error {
	updateStatus, err := c.updateStatusClient.Get(ctx, "cluster", metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		updateStatus = &configv1alpha1.UpdateStatus{
			ObjectMeta: metav1.ObjectMeta{
				Name: "cluster",
			},
		}
		_, err := c.updateStatusClient.Create(ctx, updateStatus, metav1.CreateOptions{})
		return err
	}

	return nil
}

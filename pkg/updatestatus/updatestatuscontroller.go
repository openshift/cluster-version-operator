package updatestatus

import (
	"context"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	configv1alpha1 "github.com/openshift/api/config/v1alpha1"
	configclientv1alpha1 "github.com/openshift/client-go/config/clientset/versioned/typed/config/v1alpha1"
	"github.com/openshift/library-go/pkg/controller/factory"
	"github.com/openshift/library-go/pkg/operator/events"
)

type updateStatusController struct {
	updateStatusClient configclientv1alpha1.UpdateStatusInterface
	recorder           events.Recorder
}

func newUpdateStatusController(updateStatusClient configclientv1alpha1.UpdateStatusInterface, eventsRecorder events.Recorder) factory.Controller {
	c := updateStatusController{
		updateStatusClient: updateStatusClient,
		recorder:           eventsRecorder,
	}

	return factory.New().WithSync(c.sync).ToController("UpdateStatusController", eventsRecorder.WithComponentSuffix("update-status-controller"))

}

func (c *updateStatusController) sync(ctx context.Context, syncCtx factory.SyncContext) error {
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

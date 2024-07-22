package updatestatus

import (
	"context"
	"sync"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/klog/v2"

	configv1alpha1 "github.com/openshift/api/config/v1alpha1"
	configclientv1alpha1 "github.com/openshift/client-go/config/clientset/versioned/typed/config/v1alpha1"
	"github.com/openshift/library-go/pkg/controller/factory"
	"github.com/openshift/library-go/pkg/operator/events"
)

type getUpdateStatusFunc func() lockedUpdateStatus

type lockedUpdateStatus struct {
	*configv1alpha1.UpdateStatus
	*sync.Mutex
}

type updateStatusController struct {
	updateStatusClient configclientv1alpha1.UpdateStatusInterface
	recorder           events.Recorder

	// updateStatus is the resource instance local to the controller, on which the USC maintains
	// the most recently observed state. This local status is propagated to the cluster by this controller
	// when it changes.
	updateStatus     *configv1alpha1.UpdateStatus
	updateStatusLock sync.Mutex
}

func newUpdateStatusController(updateStatusClient configclientv1alpha1.UpdateStatusInterface, eventsRecorder events.Recorder) (factory.Controller, getUpdateStatusFunc) {
	c := updateStatusController{
		updateStatusClient: updateStatusClient,
		recorder:           eventsRecorder,
	}
	controller := factory.New().WithSync(c.sync).ResyncEvery(time.Minute).ToController("UpdateStatusController", eventsRecorder.WithComponentSuffix("update-status-controller"))
	getUpdateStatus := func() lockedUpdateStatus {
		return lockedUpdateStatus{Mutex: &c.updateStatusLock, UpdateStatus: c.updateStatus}
	}
	return controller, getUpdateStatus
}

func (c *updateStatusController) sync(ctx context.Context, syncCtx factory.SyncContext) error {
	klog.Info("USC :: SYNC")
	var exists bool
	updateStatus, err := c.updateStatusClient.Get(ctx, "cluster", metav1.GetOptions{})
	if err != nil && !apierrors.IsNotFound(err) {
		return err
	}
	exists = err == nil

	c.updateStatusLock.Lock()
	defer c.updateStatusLock.Unlock()
	if c.updateStatus == nil && exists {
		klog.Info("USC :: SYNC :: Initial Sync :: Load UpdateStatus from cluster")
		c.updateStatus = updateStatus
		syncCtx.Queue().AddRateLimited(syncCtx.QueueKey())
		return nil
	}

	if c.updateStatus == nil && !exists {
		klog.Info("USC :: SYNC :: Initial Sync :: No UpdateStatus found in cluster => Initialize new one")
		updateStatus = &configv1alpha1.UpdateStatus{
			ObjectMeta: metav1.ObjectMeta{
				Name: "cluster",
			},
			Spec: configv1alpha1.UpdateStatusSpec{},
		}
		updateStatus, err := c.updateStatusClient.Create(ctx, updateStatus, metav1.CreateOptions{})
		if err == nil {
			c.updateStatus = updateStatus
			syncCtx.Queue().AddRateLimited(syncCtx.QueueKey())
		}
		return err
	}

	klog.Info("USC :: SYNC :: Update UpdateStatus")
	_, err = c.updateStatusClient.UpdateStatus(ctx, c.updateStatus, metav1.UpdateOptions{})
	return err
}

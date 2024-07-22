package updatestatus

import (
	"context"
	"time"

	"github.com/openshift/api/config/v1alpha1"
	"github.com/openshift/library-go/pkg/controller/factory"
	"github.com/openshift/library-go/pkg/operator/events"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/klog/v2"
)

type updateInsightScraper struct {
	controlPlaneUpdateStatus    controlPlaneUpdateStatus
	getControlPlaneUpdateStatus func() controlPlaneUpdateStatus

	getUpdateStatus getUpdateStatusFunc

	recorder events.Recorder
}

func newUpdateInsightScraper(getControlPlaneUpdateStatus func() controlPlaneUpdateStatus, getUpdateStatus getUpdateStatusFunc, eventsRecorder events.Recorder) factory.Controller {
	c := updateInsightScraper{
		getControlPlaneUpdateStatus: getControlPlaneUpdateStatus,
		getUpdateStatus:             getUpdateStatus,

		recorder: eventsRecorder,
	}

	return factory.New().WithInformers().ResyncEvery(time.Minute).
		WithSync(c.sync).
		ToController("UpdateInsightScraper", eventsRecorder.WithComponentSuffix("update-insight-scraper"))
}

func (c *updateInsightScraper) sync(ctx context.Context, syncCtx factory.SyncContext) error {
	klog.Info("Update Insight Scraper :: SYNC")
	c.controlPlaneUpdateStatus = c.getControlPlaneUpdateStatus()

	progressing := meta.FindStatusCondition(c.controlPlaneUpdateStatus.conditions, "Progressing")
	if progressing == nil {
		klog.Info("Update Insight Scraper :: No Progressing condition found")
		return nil
	}

	klog.Infof("Update Insight Scraper :: Progressing=%s", progressing.Status)

	updateStatus := c.getUpdateStatus()
	updateStatus.Lock()
	ctx.Done()
	defer updateStatus.Unlock()
	cpStatus := &updateStatus.Status.ControlPlane
	meta.SetStatusCondition(&cpStatus.Conditions, *progressing)
	if progressing.Status == metav1.ConditionTrue {
		cpStatus.Assessment = "Progressing"
		cpStatus.StartedAt = progressing.LastTransitionTime
		cpStatus.CompletedAt = metav1.NewTime(time.Time{})
		cpStatus.EstimatedCompletedAt = metav1.NewTime(time.Time{})
		cpStatus.Completion = 0
	}

	cpStatus.Versions = v1alpha1.ControlPlaneUpdateVersions{
		Original:        c.controlPlaneUpdateStatus.versions.previous,
		OriginalPartial: c.controlPlaneUpdateStatus.versions.isPreviousPartial,
		Target:          c.controlPlaneUpdateStatus.versions.target,
	}

	return nil
}

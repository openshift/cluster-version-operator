package updatestatus

import (
	"context"
	"time"

	"github.com/openshift/library-go/pkg/controller/factory"
	"github.com/openshift/library-go/pkg/operator/events"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/klog/v2"
)

type updateInsightScraper struct {
	controlPlaneUpdateStatus    controlPlaneUpdateStatus
	getControlPlaneUpdateStatus func() controlPlaneUpdateStatus

	recorder events.Recorder
}

func newUpdateInsightScraper(getControlPlaneUpdateStatus func() controlPlaneUpdateStatus, eventsRecorder events.Recorder) factory.Controller {
	c := updateInsightScraper{
		getControlPlaneUpdateStatus: getControlPlaneUpdateStatus,
		recorder:                    eventsRecorder,
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
		klog.Info("Update Insight Scraper :: SYNC :: No Progressing condition found")
		return nil
	}

	klog.Infof("Update Insight Scraper :: SYNC :: Progressing=%s", progressing.Status)

	return nil
}

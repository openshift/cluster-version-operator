package updatestatus

import (
	"context"
	"time"

	"github.com/openshift/library-go/pkg/controller/factory"
	"github.com/openshift/library-go/pkg/operator/events"
)

type updateInsightScraper struct {
	recorder events.Recorder
}

func newUpdateInsightScraper(eventsRecorder events.Recorder) factory.Controller {
	c := updateInsightScraper{
		recorder: eventsRecorder,
	}

	return factory.New().WithInformers().ResyncEvery(time.Minute).
		WithSync(c.sync).
		ToController("UpdateInsightScraper", eventsRecorder.WithComponentSuffix("update-insight-scraper"))
}

func (c *updateInsightScraper) sync(ctx context.Context, syncCtx factory.SyncContext) error {
	return nil
}

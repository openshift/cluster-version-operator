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

type getInsightsFunc func() []v1alpha1.UpdateInsight

type updateInsightScraper struct {
	controlPlaneUpdateStatus    controlPlaneUpdateStatus
	getControlPlaneUpdateStatus func() controlPlaneUpdateStatus

	insightsGetters map[string]getInsightsFunc

	getUpdateStatus getUpdateStatusFunc

	recorder events.Recorder
}

func newUpdateInsightScraper(getControlPlaneUpdateStatus func() controlPlaneUpdateStatus, getUpdateStatus getUpdateStatusFunc, eventsRecorder events.Recorder) (factory.Controller, *updateInsightScraper) {
	c := updateInsightScraper{
		getControlPlaneUpdateStatus: getControlPlaneUpdateStatus,
		getUpdateStatus:             getUpdateStatus,

		recorder: eventsRecorder,
	}

	return factory.New().WithInformers().ResyncEvery(time.Minute).
		WithSync(c.sync).
		ToController("UpdateInsightScraper", eventsRecorder.WithComponentSuffix("update-insight-scraper")), &c
}

func (c *updateInsightScraper) registerInsightsInformer(name string, getter getInsightsFunc) {
	c.insightsGetters[name] = getter
}

func (c *updateInsightScraper) sync(ctx context.Context, syncCtx factory.SyncContext) error {
	klog.Info("Update Insight Scraper :: SYNC")
	c.controlPlaneUpdateStatus = c.getControlPlaneUpdateStatus()

	updating := c.controlPlaneUpdateStatus.updating
	if updating == nil {
		klog.Info("Update Insight Scraper :: No UpdateProgressing condition found")
		return nil
	}

	klog.Infof("Update Insight Scraper :: UpdateProgressing=%s", updating.Status)

	updateStatus := c.getUpdateStatus()
	updateStatus.Lock()
	defer updateStatus.Unlock()
	cpStatus := &updateStatus.ControlPlane
	meta.SetStatusCondition(&cpStatus.Conditions, *updating)
	if updating.Status == metav1.ConditionTrue {
		cpStatus.Assessment = "Progressing"
		cpStatus.StartedAt = updating.LastTransitionTime
		cpStatus.CompletedAt = metav1.NewTime(time.Time{})
		cpStatus.EstimatedCompletedAt = metav1.NewTime(time.Time{})
		cpStatus.Completion = 0
	}

	cpStatus.Versions = v1alpha1.ControlPlaneUpdateVersions{
		Previous:          c.controlPlaneUpdateStatus.versions.previous,
		IsPreviousPartial: c.controlPlaneUpdateStatus.versions.isPreviousPartial,
		Target:            c.controlPlaneUpdateStatus.versions.target,
		IsTargetInstall:   c.controlPlaneUpdateStatus.versions.isTargetInstall,
	}

	var updated, total int
	for _, coUpdateProgressing := range c.controlPlaneUpdateStatus.operators {
		total++
		if coUpdateProgressing.Status == metav1.ConditionFalse && coUpdateProgressing.Reason == "Updated" {
			updated++
		}
	}

	cpStatus.Completion = int32(float64(updated) / float64(total) * 100.0)

	cpStatus.Informers = nil

	for name, getter := range c.insightsGetters {
		informer := v1alpha1.UpdateInformer{Name: name}
		for _, insight := range getter() {
			if insight.Scope.Type == v1alpha1.ScopeTypeControlPlane {
				informer.Insights = append(informer.Insights, insight)
			}
		}
		if len(informer.Insights) > 0 {
			cpStatus.Informers = append(cpStatus.Informers, informer)
		}
	}
	return nil
}

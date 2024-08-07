package updatestatus

import (
	"context"
	"time"

	"k8s.io/client-go/informers"
	"k8s.io/klog/v2"

	mcfginformers "github.com/openshift/client-go/machineconfiguration/informers/externalversions"

	"github.com/openshift/library-go/pkg/controller/factory"
	"github.com/openshift/library-go/pkg/operator/events"
)

type workerPoolsUpdateInformer struct {
	recorder events.Recorder
}

func newWorkerPoolsUpdateInformer(coreInformers informers.SharedInformerFactory, mcfgInformers mcfginformers.SharedInformerFactory, eventsRecorder events.Recorder) factory.Controller {
	c := workerPoolsUpdateInformer{
		recorder: eventsRecorder,
	}

	return factory.New().WithInformers(
		coreInformers.Core().V1().Nodes().Informer(),
		mcfgInformers.Machineconfiguration().V1().MachineConfigs().Informer(),
		mcfgInformers.Machineconfiguration().V1().MachineConfigPools().Informer(),
	).ResyncEvery(time.Minute).
		WithSync(c.sync).
		ToController("WorkerPoolUpdateInformer", eventsRecorder.WithComponentSuffix("worker-pool-update-informer"))
}

func (c *workerPoolsUpdateInformer) sync(ctx context.Context, syncCtx factory.SyncContext) error {
	klog.Info("Worker Pools Update Informer :: SYNC")
	return nil
}

package updatestatus

import (
	"context"
	"time"

	configv1informers "github.com/openshift/client-go/config/informers/externalversions"
	configv1listers "github.com/openshift/client-go/config/listers/config/v1"
	"github.com/openshift/library-go/pkg/controller/factory"
	"github.com/openshift/library-go/pkg/operator/events"
)

type controlPlaneUpdateInformer struct {
	clusterVersionLister  configv1listers.ClusterVersionLister
	clusterOperatorLister configv1listers.ClusterOperatorLister

	recorder events.Recorder
}

func newControlPlaneUpdateInformer(configInformers configv1informers.SharedInformerFactory, eventsRecorder events.Recorder) factory.Controller {
	c := controlPlaneUpdateInformer{
		clusterVersionLister:  configInformers.Config().V1().ClusterVersions().Lister(),
		clusterOperatorLister: configInformers.Config().V1().ClusterOperators().Lister(),
		recorder:              eventsRecorder,
	}

	return factory.New().WithInformers(
		configInformers.Config().V1().ClusterVersions().Informer(),
		configInformers.Config().V1().ClusterOperators().Informer(),
	).ResyncEvery(time.Minute).
		WithSync(c.sync).
		ToController("ControlPlaneUpdateInformer", eventsRecorder.WithComponentSuffix("control-plane-update-informer"))
}

func (c *controlPlaneUpdateInformer) sync(ctx context.Context, syncCtx factory.SyncContext) error {
	return nil
}

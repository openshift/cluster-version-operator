package updatestatus

import (
	"context"
	"time"

	"k8s.io/client-go/informers"
	kubeclient "k8s.io/client-go/kubernetes"
	"k8s.io/klog/v2"

	configv1client "github.com/openshift/client-go/config/clientset/versioned"
	configinformers "github.com/openshift/client-go/config/informers/externalversions"

	"github.com/openshift/library-go/pkg/controller/controllercmd"
)

const (
	uscNamespace = "openshift-cluster-version"
)

// Run starts all update status controllers
func Run(ctx context.Context, cc *controllercmd.ControllerContext) error {
	configClient, err := configv1client.NewForConfig(cc.KubeConfig)
	if err != nil {
		return err
	}

	coreClient, err := kubeclient.NewForConfig(cc.KubeConfig)
	if err != nil {
		return err
	}

	configInformers := configinformers.NewSharedInformerFactory(configClient, 10*time.Minute)
	uscNamespaceCoreInformers := informers.NewSharedInformerFactoryWithOptions(coreClient, 10*time.Minute, informers.WithNamespace(uscNamespace))

	updateStatusController, sendInsight := newUpdateStatusController(ctx, coreClient, uscNamespaceCoreInformers, cc.EventRecorder)
	controlPlaneInformerController := newControlPlaneInformerController(configInformers, cc.EventRecorder, sendInsight)

	configInformers.Start(ctx.Done())
	uscNamespaceCoreInformers.Start(ctx.Done())

	go updateStatusController.Run(ctx, 1)
	go controlPlaneInformerController.Run(ctx, 1)

	klog.Info("USC :: Controllers started")

	<-ctx.Done()
	return nil
}

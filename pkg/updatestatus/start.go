package updatestatus

import (
	"context"
	"time"

	configv1alpha1 "github.com/openshift/api/config/v1alpha1"
	configv1client "github.com/openshift/client-go/config/clientset/versioned"
	configv1alpha1client "github.com/openshift/client-go/config/clientset/versioned/typed/config/v1alpha1"
	configinformers "github.com/openshift/client-go/config/informers/externalversions"
	mcfgclient "github.com/openshift/client-go/machineconfiguration/clientset/versioned"
	mcfginformers "github.com/openshift/client-go/machineconfiguration/informers/externalversions"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	coreinformers "k8s.io/client-go/informers"
	corev1client "k8s.io/client-go/kubernetes"
	"k8s.io/klog/v2"

	"github.com/openshift/library-go/pkg/controller/controllercmd"
	"github.com/openshift/library-go/pkg/controller/factory"
)

var cfgScheme = runtime.NewScheme()

func init() {
	utilruntime.Must(configv1alpha1.Install(cfgScheme))
}

func Run(ctx context.Context, cc *controllercmd.ControllerContext) error {
	configV1Alpha1Client, err := configv1alpha1client.NewForConfig(cc.KubeConfig)
	if err != nil {
		return err
	}

	configClient, err := configv1client.NewForConfig(cc.KubeConfig)
	if err != nil {
		return err
	}

	coreClient, err := corev1client.NewForConfig(cc.KubeConfig)
	if err != nil {
		return err
	}

	mcfgClient, err := mcfgclient.NewForConfig(cc.KubeConfig)
	if err != nil {
		return err
	}

	configInformers := configinformers.NewSharedInformerFactory(configClient, 10*time.Minute)
	coreInformers := coreinformers.NewSharedInformerFactory(coreClient, 10*time.Minute)
	mcfgInformers := mcfginformers.NewSharedInformerFactory(mcfgClient, 10*time.Minute)

	klog.Info("Run :: Created clients")

	usc, getUpdateStatus := newUpdateStatusController(configV1Alpha1Client.UpdateStatuses(), cc.EventRecorder)
	cpInformer, getControlPlaneUpdateStatus := newControlPlaneUpdateInformer(configInformers, cc.EventRecorder)
	insightScraper := newUpdateInsightScraper(getControlPlaneUpdateStatus, getUpdateStatus, cc.EventRecorder)
	_ = []factory.Controller{
		cpInformer,
		newWorkerPoolsUpdateInformer(coreInformers, mcfgInformers, cc.EventRecorder),
	}

	configInformers.Start(ctx.Done())
	coreInformers.Start(ctx.Done())
	mcfgInformers.Start(ctx.Done())

	go usc.Run(ctx, 1)
	go insightScraper.Run(ctx, 1)
	go cpInformer.Run(ctx, 1)

	klog.Info("Run :: Launched controllers")

	<-ctx.Done()
	return nil
}

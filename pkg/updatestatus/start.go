package updatestatus

import (
	"context"
	"time"

	configv1client "github.com/openshift/client-go/config/clientset/versioned"
	configv1alpha1client "github.com/openshift/client-go/config/clientset/versioned/typed/config/v1alpha1"
	configinformers "github.com/openshift/client-go/config/informers/externalversions"
	mcfgclient "github.com/openshift/client-go/machineconfiguration/clientset/versioned"
	mcfginformers "github.com/openshift/client-go/machineconfiguration/informers/externalversions"
	coreinformers "k8s.io/client-go/informers"
	corev1client "k8s.io/client-go/kubernetes"
	"k8s.io/klog/v2"

	"github.com/openshift/library-go/pkg/controller/controllercmd"
	"github.com/openshift/library-go/pkg/controller/factory"
)

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

	controllers := []factory.Controller{
		newUpdateStatusController(configV1Alpha1Client.UpdateStatuses(), cc.EventRecorder),
		newUpdateInsightScraper(cc.EventRecorder),
		newControlPlaneUpdateInformer(configInformers, cc.EventRecorder),
		newWorkerPoolsUpdateInformer(coreInformers, mcfgInformers, cc.EventRecorder),
	}

	for _, controller := range controllers {
		go controller.Run(ctx, 1)
	}

	klog.Info("Run :: Launched controllers")

	<-ctx.Done()
	return nil
}

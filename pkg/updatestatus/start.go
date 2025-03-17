package updatestatus

import (
	"context"
	"time"

	"k8s.io/client-go/informers"
	kubeclient "k8s.io/client-go/kubernetes"
	appsv1client "k8s.io/client-go/kubernetes/typed/apps/v1"
	"k8s.io/klog/v2"

	configv1client "github.com/openshift/client-go/config/clientset/versioned"
	configinformers "github.com/openshift/client-go/config/informers/externalversions"
	machineconfigv1client "github.com/openshift/client-go/machineconfiguration/clientset/versioned"
	machineconfiginformers "github.com/openshift/client-go/machineconfiguration/informers/externalversions"
	updatev1alpha1client "github.com/openshift/client-go/update/clientset/versioned"
	updatev1alpha1informers "github.com/openshift/client-go/update/informers/externalversions"

	"github.com/openshift/library-go/pkg/controller/controllercmd"
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

	appsClient, err := appsv1client.NewForConfig(cc.KubeConfig)
	if err != nil {
		return err
	}

	machineConfigClient, err := machineconfigv1client.NewForConfig(cc.KubeConfig)
	if err != nil {
		return err
	}

	updateClient, err := updatev1alpha1client.NewForConfig(cc.KubeConfig)
	if err != nil {
		return err
	}

	configInformers := configinformers.NewSharedInformerFactory(configClient, 10*time.Minute)
	updateInformers := updatev1alpha1informers.NewSharedInformerFactory(updateClient, 10*time.Minute)
	coreInformers := informers.NewSharedInformerFactoryWithOptions(coreClient, 10*time.Minute)
	machineConfigInformers := machineconfiginformers.NewSharedInformerFactory(machineConfigClient, 10*time.Minute)

	updateStatusController, sendInsight := newUpdateStatusController(updateClient, updateInformers, cc.EventRecorder)
	controlPlaneInformerController := newControlPlaneInformerController(appsClient, configInformers, cc.EventRecorder, sendInsight)
	nodeInformerController := newNodeInformerController(configClient, coreInformers, machineConfigInformers, cc.EventRecorder, sendInsight)

	// start the informers, but we do not need to wait for them to sync because each controller waits
	// for synced informers it uses in its Run() method
	configInformers.Start(ctx.Done())
	updateInformers.Start(ctx.Done())
	coreInformers.Start(ctx.Done())
	machineConfigInformers.Start(ctx.Done())

	go updateStatusController.Run(ctx, 1)
	go controlPlaneInformerController.Run(ctx, 1)
	go nodeInformerController.Run(ctx, 1)

	klog.Info("USC :: Controllers started")

	// TODO(USC: TechPreview): Figure out if we need to wait for controllers to terminate
	//                         gracefully (e.g. allowing USC to empty its insight queue?)
	// https://github.com/openshift/cluster-version-operator/pull/1091#discussion_r1810615248
	<-ctx.Done()
	return nil
}

package updatestatus

import (
	"context"

	configv1alpha1 "github.com/openshift/client-go/config/clientset/versioned/typed/config/v1alpha1"
	"k8s.io/klog/v2"

	"github.com/openshift/library-go/pkg/controller/controllercmd"
	"github.com/openshift/library-go/pkg/controller/factory"
)

func Run(ctx context.Context, cc *controllercmd.ControllerContext) error {
	updateStatusClient, err := configv1alpha1.NewForConfig(cc.KubeConfig)
	if err != nil {
		return err
	}

	klog.Info("Run :: Created clients")

	controllers := []factory.Controller{
		NewUpdateStatusController(updateStatusClient.UpdateStatuses(), cc.EventRecorder),
	}

	for _, controller := range controllers {
		go controller.Run(ctx, 1)
	}

	klog.Info("Run :: Launched controllers")

	<-ctx.Done()
	return nil
}

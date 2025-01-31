package configuration

import (
	"context"
	"fmt"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/util/workqueue"
	"k8s.io/klog/v2"

	operatorv1alpha1 "github.com/openshift/api/operator/v1alpha1"
	operatorclientset "github.com/openshift/client-go/operator/clientset/versioned"
	operatorexternalversions "github.com/openshift/client-go/operator/informers/externalversions"
	operatorlistersv1alpha1 "github.com/openshift/client-go/operator/listers/operator/v1alpha1"
	"github.com/openshift/library-go/pkg/operator/loglevel"

	"github.com/openshift/cluster-version-operator/pkg/internal"
)

const clusterVersionOperatorConfigurationName = "cluster"
const clusterVersionOperatorConfigurationResync = time.Minute

type ClusterVersionOperatorConfiguration struct {
	queueKey string
	queue    workqueue.TypedRateLimitingInterface[any]

	client                operatorclientset.Interface
	lister                operatorlistersv1alpha1.ClusterVersionOperatorLister
	sharedInformerFactory operatorexternalversions.SharedInformerFactory

	lastObservedGeneration int64
}

func (config *ClusterVersionOperatorConfiguration) Queue() workqueue.TypedRateLimitingInterface[any] {
	return config.queue
}

// clusterVersionOperatorEventHandler queues an update for the cluster version operator on any change to the given object.
// Callers should use this with an informer.
func (config *ClusterVersionOperatorConfiguration) clusterVersionOperatorEventHandler() cache.ResourceEventHandler {
	return cache.ResourceEventHandlerFuncs{
		AddFunc: func(_ interface{}) {
			config.queue.Add(config.queueKey)
			klog.V(internal.Debug).Infof("ClusterVersionOperator resource was added; queuing a configuration sync")
		},
		UpdateFunc: func(_, _ interface{}) {
			config.queue.Add(config.queueKey)
			klog.V(internal.Debug).Infof("ClusterVersionOperator resource was modified or resync period has passed; queuing a configuration sync")
		},
		DeleteFunc: func(_ interface{}) {
			config.queue.Add(config.queueKey)
			klog.V(internal.Debug).Infof("ClusterVersionOperator resource was deleted; queuing a configuration sync")
		},
	}
}

// NewClusterVersionOperatorConfiguration returns an initialized ClusterVersionOperatorConfiguration, which might be used
// to synchronize with the ClusterVersionOperator resource.
func NewClusterVersionOperatorConfiguration(ctx context.Context, client operatorclientset.Interface) (*ClusterVersionOperatorConfiguration, error) {
	config := ClusterVersionOperatorConfiguration{
		queueKey: fmt.Sprintf("ClusterVersionOperator/%s", clusterVersionOperatorConfigurationName),
		queue: workqueue.NewTypedRateLimitingQueueWithConfig[any](
			workqueue.DefaultTypedControllerRateLimiter[any](),
			workqueue.TypedRateLimitingQueueConfig[any]{Name: "configuration"}),
		client: client,
	}

	config.sharedInformerFactory = operatorexternalversions.NewSharedInformerFactoryWithOptions(
		config.client,
		clusterVersionOperatorConfigurationResync,
		operatorexternalversions.WithTweakListOptions(func(opts *metav1.ListOptions) {
			opts.FieldSelector = fields.OneTermEqualSelector("metadata.name", clusterVersionOperatorConfigurationName).String()
		}))
	cvoInformer := config.sharedInformerFactory.Operator().V1alpha1().ClusterVersionOperators()
	if _, err := cvoInformer.Informer().AddEventHandler(config.clusterVersionOperatorEventHandler()); err != nil {
		config.sharedInformerFactory.Shutdown()
		return nil, err
	}
	config.lister = cvoInformer.Lister()

	stopChannel := ctx.Done()
	config.sharedInformerFactory.Start(stopChannel)
	synced := config.sharedInformerFactory.WaitForCacheSync(stopChannel)
	for _, ok := range synced {
		if !ok {
			config.sharedInformerFactory.Shutdown()
			return nil, fmt.Errorf("caches failed to sync: %w", ctx.Err())
		}
	}

	return &config, nil
}

func (config *ClusterVersionOperatorConfiguration) ShutDown() {
	config.queue.ShutDown()
	config.sharedInformerFactory.Shutdown()
}

func (config *ClusterVersionOperatorConfiguration) Sync(ctx context.Context, key string) error {
	startTime := time.Now()
	klog.V(internal.Normal).Infof("Started syncing CVO configuration %q", key)
	defer func() {
		klog.V(internal.Normal).Infof("Finished syncing CVO configuration (%v)", time.Since(startTime))
	}()

	desiredConfig, err := config.lister.Get(clusterVersionOperatorConfigurationName)
	if apierrors.IsNotFound(err) {
		return nil
	}
	if err != nil {
		return err
	}
	return config.sync(ctx, desiredConfig)
}

func (config *ClusterVersionOperatorConfiguration) sync(ctx context.Context, desiredConfig *operatorv1alpha1.ClusterVersionOperator) error {
	config.lastObservedGeneration = desiredConfig.Generation
	if desiredConfig.Status.ObservedGeneration != desiredConfig.Generation {
		desiredConfig.Status.ObservedGeneration = desiredConfig.Generation
		_, err := config.client.OperatorV1alpha1().ClusterVersionOperators().UpdateStatus(ctx, desiredConfig, metav1.UpdateOptions{})
		if err != nil {
			return fmt.Errorf("Failed to update the ClusterVersionOperator resource, err=%w", err)
		}
	}

	current, notFound := loglevel.GetLogLevel()
	if notFound {
		klog.Warningf("The current log level could not be found; an attempt to set the log level to the desired level will be made")
	}

	desired := desiredConfig.Spec.OperatorLogLevel
	if !notFound && current == desired {
		klog.V(internal.Debug).Infof("No need to update the current CVO log level '%s'; it is already set to the desired value", current)
	} else {
		if err := loglevel.SetLogLevel(desired); err != nil {
			return fmt.Errorf("Failed to set the log level to '%s', err=%w", desired, err)
		}
		klog.V(internal.Debug).Infof("Successfully updated the log level from '%s' to '%s'", current, desired)

		// E2E testing in the origin repository is checking for existence or absence of these logs
		klog.V(internal.Normal).Infof("The CVO logging level is set to the 'Normal' log level or above")
		klog.V(internal.Debug).Infof("The CVO logging level is set to the 'Debug' log level or above")
		klog.V(internal.Trace).Infof("The CVO logging level is set to the 'Trace' log level or above")
		klog.V(internal.TraceAll).Infof("The CVO logging level is set to the 'TraceAll' log level or above")
	}
	return nil
}

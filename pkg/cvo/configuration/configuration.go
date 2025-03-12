package configuration

import (
	"context"
	"fmt"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/util/workqueue"
	"k8s.io/klog/v2"

	operatorv1alpha1 "github.com/openshift/api/operator/v1alpha1"
	operatorclientset "github.com/openshift/client-go/operator/clientset/versioned"
	cvoclientv1alpha1 "github.com/openshift/client-go/operator/clientset/versioned/typed/operator/v1alpha1"
	operatorexternalversions "github.com/openshift/client-go/operator/informers/externalversions"
	operatorlistersv1alpha1 "github.com/openshift/client-go/operator/listers/operator/v1alpha1"
)

const ClusterVersionOperatorConfigurationName = "cluster"

type ClusterVersionOperatorConfiguration struct {
	queueKey string
	// queue tracks checking for the CVO configuration.
	//
	// The type any is used to comply with the worker method of the cvo.Operator struct.
	queue workqueue.TypedRateLimitingInterface[any]

	client  cvoclientv1alpha1.ClusterVersionOperatorInterface
	lister  operatorlistersv1alpha1.ClusterVersionOperatorLister
	factory operatorexternalversions.SharedInformerFactory

	started bool
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
		},
		UpdateFunc: func(_, _ interface{}) {
			config.queue.Add(config.queueKey)
		},
		DeleteFunc: func(_ interface{}) {
			config.queue.Add(config.queueKey)
		},
	}
}

// NewClusterVersionOperatorConfiguration returns ClusterVersionOperatorConfiguration, which might be used
// to synchronize with the ClusterVersionOperator resource.
func NewClusterVersionOperatorConfiguration(client operatorclientset.Interface, factory operatorexternalversions.SharedInformerFactory) *ClusterVersionOperatorConfiguration {
	return &ClusterVersionOperatorConfiguration{
		queueKey: fmt.Sprintf("ClusterVersionOperator/%s", ClusterVersionOperatorConfigurationName),
		queue: workqueue.NewTypedRateLimitingQueueWithConfig[any](
			workqueue.DefaultTypedControllerRateLimiter[any](),
			workqueue.TypedRateLimitingQueueConfig[any]{Name: "configuration"}),
		client:  client.OperatorV1alpha1().ClusterVersionOperators(),
		factory: factory,
	}
}

// Start initializes and starts the configuration's informers. Must be run before Sync is called.
// Blocks until informers caches are synchronized or the context is cancelled.
func (config *ClusterVersionOperatorConfiguration) Start(ctx context.Context) error {
	informer := config.factory.Operator().V1alpha1().ClusterVersionOperators()
	if _, err := informer.Informer().AddEventHandler(config.clusterVersionOperatorEventHandler()); err != nil {
		return err
	}
	config.lister = informer.Lister()

	config.factory.Start(ctx.Done())
	synced := config.factory.WaitForCacheSync(ctx.Done())
	for _, ok := range synced {
		if !ok {
			return fmt.Errorf("caches failed to sync: %w", ctx.Err())
		}
	}

	config.started = true
	return nil
}

func (config *ClusterVersionOperatorConfiguration) Sync(ctx context.Context, key string) error {
	if !config.started {
		panic("ClusterVersionOperatorConfiguration instance was not properly started before its synchronization.")
	}
	startTime := time.Now()
	klog.V(2).Infof("Started syncing CVO configuration %q", key)
	defer func() {
		klog.V(2).Infof("Finished syncing CVO configuration (%v)", time.Since(startTime))
	}()

	desiredConfig, err := config.lister.Get(ClusterVersionOperatorConfigurationName)
	if apierrors.IsNotFound(err) {
		// TODO: Set default values
		return nil
	}
	if err != nil {
		return err
	}
	return config.sync(ctx, desiredConfig)
}

func (config *ClusterVersionOperatorConfiguration) sync(_ context.Context, _ *operatorv1alpha1.ClusterVersionOperator) error {
	klog.Infof("ClusterVersionOperator configuration has been synced")
	return nil
}

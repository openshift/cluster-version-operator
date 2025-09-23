package configuration

import (
	"context"
	"fmt"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/util/workqueue"
	"k8s.io/klog/v2"

	operatorv1 "github.com/openshift/api/operator/v1"
	operatorv1alpha1 "github.com/openshift/api/operator/v1alpha1"
	operatorclientset "github.com/openshift/client-go/operator/clientset/versioned"
	cvoclientv1alpha1 "github.com/openshift/client-go/operator/clientset/versioned/typed/operator/v1alpha1"
	operatorexternalversions "github.com/openshift/client-go/operator/informers/externalversions"
	operatorlistersv1alpha1 "github.com/openshift/client-go/operator/listers/operator/v1alpha1"
	"github.com/openshift/library-go/pkg/operator/loglevel"

	i "github.com/openshift/cluster-version-operator/pkg/internal"
)

const ClusterVersionOperatorConfigurationName = "cluster"

type configuration struct {
	lastObservedGeneration int64
	desiredLogLevel        operatorv1.LogLevel
}

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

	configuration configuration
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
			klog.V(i.Debug).Infof("ClusterVersionOperator resource was added; queuing a sync")
		},
		UpdateFunc: func(_, _ interface{}) {
			config.queue.Add(config.queueKey)
			klog.V(i.Debug).Infof("ClusterVersionOperator resource was modified or resync period has passed; queuing a sync")
		},
		DeleteFunc: func(_ interface{}) {
			config.queue.Add(config.queueKey)
			klog.V(i.Debug).Infof("ClusterVersionOperator resource was deleted; queuing a sync")
		},
	}
}

// NewClusterVersionOperatorConfiguration returns ClusterVersionOperatorConfiguration, which might be used
// to synchronize with the ClusterVersionOperator resource.
func NewClusterVersionOperatorConfiguration(client operatorclientset.Interface, factory operatorexternalversions.SharedInformerFactory) *ClusterVersionOperatorConfiguration {
	var desiredLogLevel operatorv1.LogLevel
	if currentLogLevel, notFound := loglevel.GetLogLevel(); notFound {
		klog.Warningf("The current log level could not be found; assuming the 'Normal' level is the currently desired")
		desiredLogLevel = operatorv1.Normal
	} else {
		desiredLogLevel = currentLogLevel
	}

	return &ClusterVersionOperatorConfiguration{
		queueKey: fmt.Sprintf("ClusterVersionOperator/%s", ClusterVersionOperatorConfigurationName),
		queue: workqueue.NewTypedRateLimitingQueueWithConfig[any](
			workqueue.DefaultTypedControllerRateLimiter[any](),
			workqueue.TypedRateLimitingQueueConfig[any]{Name: "configuration"}),
		client:        client.OperatorV1alpha1().ClusterVersionOperators(),
		factory:       factory,
		configuration: configuration{desiredLogLevel: desiredLogLevel},
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
	klog.V(i.Normal).Infof("Started syncing CVO configuration %q", key)
	defer func() {
		klog.V(i.Normal).Infof("Finished syncing CVO configuration (%v)", time.Since(startTime))
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

// sync synchronizes the local configuration based on the desired configuration
// and updates the status of the Kubernetes resource if needed.
//
// desiredConfig is a read-only representation of the desired configuration.
func (config *ClusterVersionOperatorConfiguration) sync(ctx context.Context, desiredConfig *operatorv1alpha1.ClusterVersionOperator) error {
	if desiredConfig.Status.ObservedGeneration != desiredConfig.Generation {
		newConfig := desiredConfig.DeepCopy()
		newConfig.Status.ObservedGeneration = desiredConfig.Generation
		_, err := config.client.UpdateStatus(ctx, newConfig, metav1.UpdateOptions{})
		if err != nil {
			return fmt.Errorf("failed to update the ClusterVersionOperator resource: %w", err)
		}
	}

	config.configuration.lastObservedGeneration = desiredConfig.Generation
	config.configuration.desiredLogLevel = desiredConfig.Spec.OperatorLogLevel
	if config.configuration.desiredLogLevel == "" {
		config.configuration.desiredLogLevel = operatorv1.Normal
	}

	return applyLogLevel(config.configuration.desiredLogLevel)
}

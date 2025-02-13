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

	"github.com/openshift/cluster-version-operator/pkg/internal"
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

	desiredLogLevel        operatorv1.LogLevel
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
			klog.V(internal.Debug).Infof("ClusterVersionOperator resource was deleted; configuration sync is not queued")
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

	var notFound bool
	config.desiredLogLevel, notFound = loglevel.GetLogLevel()
	if notFound {
		klog.Warningf("The current log level could not be found; assuming the 'Normal' level is the currently desired")
	}

	config.started = true
	return nil
}

func (config *ClusterVersionOperatorConfiguration) Sync(ctx context.Context, key string) error {
	if !config.started {
		panic("ClusterVersionOperatorConfiguration instance was not properly started before its synchronization.")
	}
	startTime := time.Now()
	klog.V(internal.Normal).Infof("Started syncing CVO configuration %q", key)
	defer func() {
		klog.V(internal.Normal).Infof("Finished syncing CVO configuration (%v)", time.Since(startTime))
	}()

	desiredConfig, err := config.lister.Get(ClusterVersionOperatorConfigurationName)
	if apierrors.IsNotFound(err) {
		return nil
	}
	if err != nil {
		return err
	}
	return config.sync(ctx, desiredConfig)
}

func (config *ClusterVersionOperatorConfiguration) sync(ctx context.Context, desiredConfig *operatorv1alpha1.ClusterVersionOperator) error {
	config.desiredLogLevel = desiredConfig.Spec.OperatorLogLevel
	config.lastObservedGeneration = desiredConfig.Generation
	if desiredConfig.Status.ObservedGeneration != desiredConfig.Generation {
		desiredConfig.Status.ObservedGeneration = desiredConfig.Generation
		_, err := config.client.UpdateStatus(ctx, desiredConfig, metav1.UpdateOptions{})
		if err != nil {
			return fmt.Errorf("Failed to update the ClusterVersionOperator resource, err=%w", err)
		}
	}

	currentLogLevel, notFound := loglevel.GetLogLevel()
	if notFound {
		klog.Warningf("The current log level could not be found; an attempt to set the log level to the desired level will be made")
	}

	if !notFound && currentLogLevel == config.desiredLogLevel {
		klog.V(internal.Debug).Infof("No need to update the current CVO log level '%s'; it is already set to the desired value", currentLogLevel)
	} else {
		if err := loglevel.SetLogLevel(config.desiredLogLevel); err != nil {
			return fmt.Errorf("Failed to set the log level to '%s', err=%w", config.desiredLogLevel, err)
		}
		klog.V(internal.Debug).Infof("Successfully updated the log level from '%s' to '%s'", currentLogLevel, config.desiredLogLevel)

		// E2E testing in the origin repository is checking for existence or absence of these logs
		klog.V(internal.Normal).Infof("The CVO logging level is set to the 'Normal' log level or above")
		klog.V(internal.Debug).Infof("The CVO logging level is set to the 'Debug' log level or above")
		klog.V(internal.Trace).Infof("The CVO logging level is set to the 'Trace' log level or above")
		klog.V(internal.TraceAll).Infof("The CVO logging level is set to the 'TraceAll' log level or above")
	}
	return nil
}

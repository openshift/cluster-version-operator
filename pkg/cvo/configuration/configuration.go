package configuration

import (
	"context"
	"fmt"
	"time"

	"k8s.io/client-go/util/workqueue"
	"k8s.io/klog/v2"

	operatorv1 "github.com/openshift/api/operator/v1"
	operatorclientset "github.com/openshift/client-go/operator/clientset/versioned"
	operatorexternalversions "github.com/openshift/client-go/operator/informers/externalversions"
	"github.com/openshift/library-go/pkg/operator/loglevel"

	i "github.com/openshift/cluster-version-operator/pkg/internal"
)

const ClusterVersionOperatorConfigurationName = "cluster"

const defaultQueueKey = "ClusterVersionOperator/" + ClusterVersionOperatorConfigurationName

type configuration struct {
	lastObservedGeneration int64
	desiredLogLevel        operatorv1.LogLevel
}

func defaultConfiguration() configuration {
	return configuration{
		lastObservedGeneration: 0,
		desiredLogLevel:        operatorv1.Normal,
	}
}

type ConfigProvider interface {
	start(ctx context.Context) error
	get(context.Context) (configuration, error)
}

type ClusterVersionOperatorConfiguration struct {
	// queue tracks checking for the CVO configuration.
	//
	// The type any is used to comply with the worker method of the cvo.Operator struct.
	queue workqueue.TypedRateLimitingInterface[any]

	started bool

	configuration configuration
	provider      ConfigProvider

	// Function to handle an update to the internal configuration.
	//
	// In the future, the desired configuration may be consumed via other controllers.
	// This will require some sort of synchronization upon a configuration change.
	// For the moment, the log level is the sole consumer of the configuration.
	// Apply the log level directly here for simplicity for the time being.
	handler func(configuration) error
}

func (config *ClusterVersionOperatorConfiguration) Queue() workqueue.TypedRateLimitingInterface[any] {
	return config.queue
}

// NewClusterVersionOperatorConfiguration returns ClusterVersionOperatorConfiguration, which might be used
// to synchronize with the ClusterVersionOperator resource.
func NewClusterVersionOperatorConfiguration() *ClusterVersionOperatorConfiguration {
	var desiredLogLevel operatorv1.LogLevel
	if currentLogLevel, notFound := loglevel.GetLogLevel(); notFound {
		klog.Warningf("The current log level could not be found; assuming the 'Normal' level is the currently desired")
		desiredLogLevel = operatorv1.Normal
	} else {
		desiredLogLevel = currentLogLevel
	}

	return &ClusterVersionOperatorConfiguration{
		queue: workqueue.NewTypedRateLimitingQueueWithConfig[any](
			workqueue.DefaultTypedControllerRateLimiter[any](),
			workqueue.TypedRateLimitingQueueConfig[any]{Name: "configuration"}),
		configuration: configuration{desiredLogLevel: desiredLogLevel},
		handler:       func(c configuration) error { return applyLogLevel(c.desiredLogLevel) },
	}
}

func (config *ClusterVersionOperatorConfiguration) UsingKubeAPIServer(client operatorclientset.Interface, factory operatorexternalversions.SharedInformerFactory) *ClusterVersionOperatorConfiguration {
	config.provider = newConfigProviderKubeAPIServer(client, factory, config.queue)
	return config
}

// Start initializes and starts the configuration's informers. Must be run before Sync is called.
// Blocks until informers caches are synchronized or the context is cancelled.
func (config *ClusterVersionOperatorConfiguration) Start(ctx context.Context) error {
	if config.provider == nil {
		return fmt.Errorf("the configuration provider must be initialized")
	}
	if err := config.provider.start(ctx); err != nil {
		return err
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

	desiredConfig, err := config.provider.get(ctx)
	if err != nil {
		return err
	}
	if desiredConfig.desiredLogLevel == "" {
		desiredConfig.desiredLogLevel = operatorv1.Normal
	}
	config.configuration = desiredConfig

	if config.handler != nil {
		return config.handler(config.configuration)
	}
	return nil
}

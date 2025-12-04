package configuration

import (
	"context"
	"fmt"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/util/workqueue"
	"k8s.io/klog/v2"

	operatorclientset "github.com/openshift/client-go/operator/clientset/versioned"
	cvoclientv1alpha1 "github.com/openshift/client-go/operator/clientset/versioned/typed/operator/v1alpha1"
	operatorexternalversions "github.com/openshift/client-go/operator/informers/externalversions"
	operatorlistersv1alpha1 "github.com/openshift/client-go/operator/listers/operator/v1alpha1"

	i "github.com/openshift/cluster-version-operator/pkg/internal"
)

type ConfigProviderKubeAPIServer struct {
	queueAdd func(any)
	queueKey string

	client  cvoclientv1alpha1.ClusterVersionOperatorInterface
	lister  operatorlistersv1alpha1.ClusterVersionOperatorLister
	factory operatorexternalversions.SharedInformerFactory
}

func newConfigProviderKubeAPIServer(client operatorclientset.Interface, factory operatorexternalversions.SharedInformerFactory, queue workqueue.TypedRateLimitingInterface[any]) *ConfigProviderKubeAPIServer {
	return &ConfigProviderKubeAPIServer{
		queueAdd: queue.Add,
		queueKey: defaultQueueKey,
		client:   client.OperatorV1alpha1().ClusterVersionOperators(),
		factory:  factory,
	}
}

func (c *ConfigProviderKubeAPIServer) start(ctx context.Context) error {
	informer := c.factory.Operator().V1alpha1().ClusterVersionOperators()
	if _, err := informer.Informer().AddEventHandler(c.clusterVersionOperatorEventHandler()); err != nil {
		return err
	}
	c.lister = informer.Lister()

	c.factory.Start(ctx.Done())
	synced := c.factory.WaitForCacheSync(ctx.Done())
	for _, ok := range synced {
		if !ok {
			return fmt.Errorf("caches failed to sync: %w", ctx.Err())
		}
	}
	return nil
}

func (c *ConfigProviderKubeAPIServer) get(ctx context.Context) (configuration, error) {
	config := defaultConfiguration()

	desiredConfig, err := c.lister.Get(ClusterVersionOperatorConfigurationName)
	if apierrors.IsNotFound(err) {
		return config, nil
	}
	if err != nil {
		return config, err
	}

	if desiredConfig.Status.ObservedGeneration != desiredConfig.Generation {
		newConfig := desiredConfig.DeepCopy()
		newConfig.Status.ObservedGeneration = desiredConfig.Generation
		_, err := c.client.UpdateStatus(ctx, newConfig, metav1.UpdateOptions{})
		if err != nil {
			return config, fmt.Errorf("failed to update the ClusterVersionOperator resource: %w", err)
		}
	}

	config.lastObservedGeneration = desiredConfig.Generation
	config.desiredLogLevel = desiredConfig.Spec.OperatorLogLevel
	return config, nil
}

// clusterVersionOperatorEventHandler queues an update for the cluster version operator on any change to the given object.
// Callers should use this with an informer.
func (c *ConfigProviderKubeAPIServer) clusterVersionOperatorEventHandler() cache.ResourceEventHandler {
	return cache.ResourceEventHandlerFuncs{
		AddFunc: func(_ interface{}) {
			c.queueAdd(c.queueKey)
			klog.V(i.Debug).Infof("ClusterVersionOperator resource was added; queuing a sync")
		},
		UpdateFunc: func(_, _ interface{}) {
			c.queueAdd(c.queueKey)
			klog.V(i.Debug).Infof("ClusterVersionOperator resource was modified or resync period has passed; queuing a sync")
		},
		DeleteFunc: func(_ interface{}) {
			c.queueAdd(c.queueKey)
			klog.V(i.Debug).Infof("ClusterVersionOperator resource was deleted; queuing a sync")
		},
	}
}

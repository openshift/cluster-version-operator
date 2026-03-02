package featuregates

import (
	"context"
	"errors"
	"fmt"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/util/workqueue"
	"k8s.io/klog/v2"

	configv1 "github.com/openshift/api/config/v1"
	configinformersv1 "github.com/openshift/client-go/config/informers/externalversions/config/v1"
	configlistersv1 "github.com/openshift/client-go/config/listers/config/v1"
)

// ChangeStopper calls stop when the value of the featureset changes
type ChangeStopper struct {
	startingRequiredFeatureSet *configv1.FeatureSet
	startingCvoGates           *CvoGates

	featureGateLister configlistersv1.FeatureGateLister
	cacheSynced       []cache.InformerSynced

	queue      workqueue.TypedRateLimitingInterface[any]
	shutdownFn context.CancelFunc
}

// NewChangeStopper returns a new ChangeStopper.
func NewChangeStopper(featureGateInformer configinformersv1.FeatureGateInformer, requiredFeatureSet configv1.FeatureSet, cvoGates CvoGates) (*ChangeStopper, error) {
	c := &ChangeStopper{
		featureGateLister: featureGateInformer.Lister(),
		cacheSynced:       []cache.InformerSynced{featureGateInformer.Informer().HasSynced},
		queue:             workqueue.NewTypedRateLimitingQueueWithConfig[any](workqueue.DefaultTypedControllerRateLimiter[any](), workqueue.TypedRateLimitingQueueConfig[any]{Name: "feature-gate-stopper"}),

		startingRequiredFeatureSet: &requiredFeatureSet,
		startingCvoGates:           &cvoGates,
	}

	c.queue.Add("cluster") // seed an initial sync, in case startingRequiredFeatureSet is wrong
	if _, err := featureGateInformer.Informer().AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: func(_ interface{}) {
			c.queue.Add("cluster")
		},
		UpdateFunc: func(_ interface{}, _ interface{}) {
			c.queue.Add("cluster")
		},
		DeleteFunc: func(_ interface{}) {
			c.queue.Add("cluster")
		},
	}); err != nil {
		return c, err
	}

	return c, nil
}

// syncHandler processes a single work entry, with the
// processNextWorkItem caller handling the queue management.  It returns
// done when there will be no more work (because the feature gate changed).
func (c *ChangeStopper) syncHandler(_ context.Context) (done bool, err error) {
	var current configv1.FeatureSet
	var currentCvoGates CvoGates
	if featureGates, err := c.featureGateLister.Get("cluster"); err == nil {

		current = featureGates.Spec.FeatureSet
		currentCvoGates = CvoGatesFromFeatureGate(featureGates, c.startingCvoGates.desiredVersion)
	} else if !apierrors.IsNotFound(err) {
		return false, err
	}

	featureSetChanged := current != *c.startingRequiredFeatureSet
	cvoFeaturesChanged := currentCvoGates != *c.startingCvoGates
	if featureSetChanged || cvoFeaturesChanged {
		var action string
		if c.shutdownFn == nil {
			action = "no shutdown function configured"
		} else {
			action = "requesting shutdown"
		}
		if featureSetChanged {
			klog.Infof("FeatureSet was %q, but the current feature set is %q; %s.", *c.startingRequiredFeatureSet, current, action)
		}
		if cvoFeaturesChanged {
			klog.Infof("CVO feature flags were %+v, but changed to %+v; %s.", c.startingCvoGates, currentCvoGates, action)
		}

		if c.shutdownFn != nil {
			c.shutdownFn()
		}
		return true, nil
	}
	return false, nil
}

// Run launches the controller and blocks until it is canceled or work completes.
func (c *ChangeStopper) Run(ctx context.Context, shutdownFn context.CancelFunc) error {
	if c.startingRequiredFeatureSet == nil || c.startingCvoGates == nil {
		return errors.New("BUG: startingRequiredFeatureSet and startingCvoGates must be set before calling Run")

	}
	// don't let panics crash the process
	defer utilruntime.HandleCrash()
	// make sure the work queue is shutdown which will trigger workers to end
	defer c.queue.ShutDown()
	childCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	go func() { // queue.Get does not take a Context, so the only way to unblock processNextWorkItem if we are canceled is to close the queue
		<-childCtx.Done()
		c.queue.ShutDown()
	}()
	c.shutdownFn = shutdownFn

	// wait for your secondary caches to fill before starting your work
	if !cache.WaitForCacheSync(ctx.Done(), c.cacheSynced...) {
		return errors.New("feature gate cache failed to sync")
	}

	klog.Infof("Starting stop-on-features-change controller with startingRequiredFeatureSet=%q startingCvoGates=%+v", *c.startingRequiredFeatureSet, *c.startingCvoGates)

	err := wait.PollUntilContextCancel(ctx, 30*time.Second, true, c.runWorker)
	klog.Info("Shutting down stop-on-featureset-change controller")
	return err
}

// runWorker handles a single worker poll round, processing as many
// work items as possible, and returning done when there will be no
// more work.
func (c *ChangeStopper) runWorker(ctx context.Context) (done bool, err error) {
	// hot loop until we're told to stop.  processNextWorkItem will
	// automatically wait until there's work available, so we don't worry
	// about secondary waits
	for {
		if done, err := c.processNextWorkItem(ctx); done || err != nil {
			return done, err
		}
	}
}

// processNextWorkItem deals with one key off the queue.  It returns
// done when there will be no more work.
func (c *ChangeStopper) processNextWorkItem(ctx context.Context) (done bool, err error) {
	// pull the next work item from queue.  It should be a key we use to lookup
	// something in a cache
	key, quit := c.queue.Get()
	if quit {
		return true, nil
	}

	// you always have to indicate to the queue that you've completed a piece of
	// work
	defer c.queue.Done(key)

	// do your work on the key.  This method will contains your "do stuff" logic
	done, err = c.syncHandler(ctx)
	if err == nil {
		// if you had no error, tell the queue to stop tracking history for your
		// key. This will reset things like failure counts for per-item rate
		// limiting
		c.queue.Forget(key)
		return done, nil
	}

	// there was a failure so be sure to report it.  This method allows for
	// pluggable error handling which can be used for things like
	// cluster-monitoring
	utilruntime.HandleError(fmt.Errorf("%v failed with : %w", key, err))

	// since we failed, we should requeue the item to work on later.  This
	// method will add a backoff to avoid hotlooping on particular items
	// (they're probably still not going to work right away) and overall
	// controller protection (everything I've done is broken, this controller
	// needs to calm down or it can starve other useful work) cases.
	c.queue.AddRateLimited(key)

	return done, nil
}

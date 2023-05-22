package featurechangestopper

import (
	"context"
	"errors"
	"fmt"
	"time"

	configv1 "github.com/openshift/api/config/v1"
	configinformersv1 "github.com/openshift/client-go/config/informers/externalversions/config/v1"
	configlistersv1 "github.com/openshift/client-go/config/listers/config/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/util/workqueue"
	"k8s.io/klog/v2"
)

// FeatureChangeStopper calls stop when the value of the featureset changes
type FeatureChangeStopper struct {
	startingRequiredFeatureSet string

	featureGateLister configlistersv1.FeatureGateLister
	cacheSynced       []cache.InformerSynced

	queue      workqueue.RateLimitingInterface
	shutdownFn context.CancelFunc
}

// New returns a new FeatureChangeStopper.
func New(
	startingRequiredFeatureSet string,
	featureGateInformer configinformersv1.FeatureGateInformer,
) *FeatureChangeStopper {
	c := &FeatureChangeStopper{
		startingRequiredFeatureSet: startingRequiredFeatureSet,
		featureGateLister:          featureGateInformer.Lister(),
		cacheSynced:                []cache.InformerSynced{featureGateInformer.Informer().HasSynced},
		queue:                      workqueue.NewNamedRateLimitingQueue(workqueue.DefaultControllerRateLimiter(), "feature-gate-stopper"),
	}

	c.queue.Add("cluster") // seed an initial sync, in case startingRequiredFeatureSet is wrong
	// nolint:errcheck
	featureGateInformer.Informer().AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: func(obj interface{}) {
			c.queue.Add("cluster")
		},
		UpdateFunc: func(old interface{}, new interface{}) {
			c.queue.Add("cluster")
		},
		DeleteFunc: func(obj interface{}) {
			c.queue.Add("cluster")
		},
	})

	return c
}

// syncHandler processes a single work entry, with the
// processNextWorkItem caller handling the queue management.  It returns
// done when there will be no more work (because the feature gate changed).
func (c *FeatureChangeStopper) syncHandler(ctx context.Context) (done bool, err error) {
	var current configv1.FeatureSet
	if featureGates, err := c.featureGateLister.Get("cluster"); err == nil {
		current = featureGates.Spec.FeatureSet
	} else if !apierrors.IsNotFound(err) {
		return false, err
	}

	if string(current) != c.startingRequiredFeatureSet {
		var action string
		if c.shutdownFn == nil {
			action = "no shutdown function configured"
		} else {
			action = "requesting shutdown"
		}
		klog.Infof("FeatureSet was %q, but the current feature set is %q; %s.", c.startingRequiredFeatureSet, current, action)
		if c.shutdownFn != nil {
			c.shutdownFn()
		}
		return true, nil
	}
	return false, nil
}

// Run launches the controller and blocks until it is canceled or work completes.
func (c *FeatureChangeStopper) Run(ctx context.Context, shutdownFn context.CancelFunc) error {
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

	klog.Infof("Starting stop-on-featureset-change controller with %q.", c.startingRequiredFeatureSet)

	// wait for your secondary caches to fill before starting your work
	if !cache.WaitForCacheSync(ctx.Done(), c.cacheSynced...) {
		return errors.New("feature gate cache failed to sync")
	}

	//nolint:staticcheck
	// until https://github.com/kubernetes/kubernetes/issues/116712 is resolved
	err := wait.PollImmediateUntilWithContext(ctx, 30*time.Second, c.runWorker)
	klog.Info("Shutting down stop-on-featureset-change controller")
	return err
}

// runWorker handles a single worker poll round, processing as many
// work items as possible, and returning done when there will be no
// more work.
func (c *FeatureChangeStopper) runWorker(ctx context.Context) (done bool, err error) {
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
func (c *FeatureChangeStopper) processNextWorkItem(ctx context.Context) (done bool, err error) {
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

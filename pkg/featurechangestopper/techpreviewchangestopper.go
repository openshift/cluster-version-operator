package featurechangestopper

import (
	"context"
	"fmt"
	"time"

	configv1 "github.com/openshift/api/config/v1"
	configinformersv1 "github.com/openshift/client-go/config/informers/externalversions/config/v1"
	configlistersv1 "github.com/openshift/client-go/config/listers/config/v1"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/util/workqueue"
	"k8s.io/klog/v2"
)

// TechPreviewChangeStopper calls stop when the value of the featuregate changes from TechPreviewNoUpgrade to anything else
// or from anything to TechPreviewNoUpgrade.
type TechPreviewChangeStopper struct {
	startingTechPreviewState bool

	featureGateLister configlistersv1.FeatureGateLister
	cacheSynced       []cache.InformerSynced

	queue workqueue.RateLimitingInterface
}

// New returns a new TechPreviewChangeStopper.
func New(
	startingTechPreviewState bool,
	featureGateInformer configinformersv1.FeatureGateInformer,
) *TechPreviewChangeStopper {
	c := &TechPreviewChangeStopper{
		startingTechPreviewState: startingTechPreviewState,
		featureGateLister:        featureGateInformer.Lister(),
		cacheSynced:              []cache.InformerSynced{featureGateInformer.Informer().HasSynced},
		queue:                    workqueue.NewNamedRateLimitingQueue(workqueue.DefaultControllerRateLimiter(), "feature-gate-stopper"),
	}

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

func (c *TechPreviewChangeStopper) syncHandler(ctx context.Context, shutdownFn context.CancelFunc) error {
	featureGates, err := c.featureGateLister.Get("cluster")
	if err != nil {
		return err
	}
	techPreviewNowSet := featureGates.Spec.FeatureSet == configv1.TechPreviewNoUpgrade
	if techPreviewNowSet != c.startingTechPreviewState {
		klog.Infof("TechPreviewNoUpgrade status changed from %v to %v, shutting down.", c.startingTechPreviewState, techPreviewNowSet)
		if shutdownFn != nil {
			shutdownFn()
		}
	}
	return nil
}

func (c *TechPreviewChangeStopper) Run(runContext context.Context, shutdownFn context.CancelFunc) {
	// don't let panics crash the process
	defer utilruntime.HandleCrash()
	// make sure the work queue is shutdown which will trigger workers to end
	defer c.queue.ShutDown()

	klog.Infof("Starting stop-on-techpreview-change controller")

	// wait for your secondary caches to fill before starting your work
	if !cache.WaitForCacheSync(runContext.Done(), c.cacheSynced...) {
		return
	}

	// runWorker will loop until "something bad" happens.  The .Until will
	// then rekick the worker after one second
	go wait.UntilWithContext(runContext, func(ctx context.Context) {
		c.runWorker(ctx, shutdownFn)
	}, time.Second)

	// wait until we're told to stop
	<-runContext.Done()
	klog.Infof("Shutting down stop-on-techpreview-change controller")
}

func (c *TechPreviewChangeStopper) runWorker(ctx context.Context, shutdownFn context.CancelFunc) {
	// hot loop until we're told to stop.  processNextWorkItem will
	// automatically wait until there's work available, so we don't worry
	// about secondary waits
	for c.processNextWorkItem(ctx, shutdownFn) {
	}
}

// processNextWorkItem deals with one key off the queue.  It returns false
// when it's time to quit.
func (c *TechPreviewChangeStopper) processNextWorkItem(ctx context.Context, shutdownFn context.CancelFunc) bool {
	// pull the next work item from queue.  It should be a key we use to lookup
	// something in a cache
	key, quit := c.queue.Get()
	if quit {
		return false
	}
	// you always have to indicate to the queue that you've completed a piece of
	// work
	defer c.queue.Done(key)

	// do your work on the key.  This method will contains your "do stuff" logic
	err := c.syncHandler(ctx, shutdownFn)
	if err == nil {
		// if you had no error, tell the queue to stop tracking history for your
		// key. This will reset things like failure counts for per-item rate
		// limiting
		c.queue.Forget(key)
		return true
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

	return true
}

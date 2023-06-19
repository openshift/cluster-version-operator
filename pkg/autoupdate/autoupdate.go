package autoupdate

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/blang/semver/v4"

	v1 "github.com/openshift/api/config/v1"
	clientset "github.com/openshift/client-go/config/clientset/versioned"
	"github.com/openshift/client-go/config/clientset/versioned/scheme"
	configinformersv1 "github.com/openshift/client-go/config/informers/externalversions/config/v1"
	configlistersv1 "github.com/openshift/client-go/config/listers/config/v1"
	"github.com/openshift/cluster-version-operator/lib/resourceapply"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/kubernetes"
	coreclientsetv1 "k8s.io/client-go/kubernetes/typed/core/v1"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/tools/record"
	"k8s.io/client-go/util/workqueue"
	"k8s.io/klog/v2"
)

const (
	// maxRetries is the number of times a work-item will be retried before it is dropped out of the queue.
	maxRetries = 15
)

// Controller defines autoupdate controller.
type Controller struct {
	// namespace and name are used to find the ClusterVersion, ClusterOperator.
	namespace, name string

	client        clientset.Interface
	eventRecorder record.EventRecorder

	syncHandler func(ctx context.Context, key string) error

	cvLister    configlistersv1.ClusterVersionLister
	coLister    configlistersv1.ClusterOperatorLister
	cacheSynced []cache.InformerSynced

	// queue tracks keeping the list of available updates on a cluster version
	queue workqueue.RateLimitingInterface
}

// New returns a new autoupdate controller.
func New(
	namespace, name string,
	cvInformer configinformersv1.ClusterVersionInformer,
	coInformer configinformersv1.ClusterOperatorInformer,
	client clientset.Interface,
	kubeClient kubernetes.Interface,
) (*Controller, error) {
	eventBroadcaster := record.NewBroadcaster()
	eventBroadcaster.StartLogging(klog.Infof)
	eventBroadcaster.StartRecordingToSink(&coreclientsetv1.EventSinkImpl{Interface: kubeClient.CoreV1().Events(namespace)})

	ctrl := &Controller{
		namespace:     namespace,
		name:          name,
		client:        client,
		eventRecorder: eventBroadcaster.NewRecorder(scheme.Scheme, corev1.EventSource{Component: "autoupdater"}),
		queue:         workqueue.NewNamedRateLimitingQueue(workqueue.DefaultControllerRateLimiter(), "autoupdater"),
	}

	if _, err := cvInformer.Informer().AddEventHandler(ctrl.eventHandler()); err != nil {
		return nil, err
	}
	if _, err := coInformer.Informer().AddEventHandler(ctrl.eventHandler()); err != nil {
		return nil, err
	}

	ctrl.syncHandler = ctrl.sync

	ctrl.cvLister = cvInformer.Lister()
	ctrl.cacheSynced = append(ctrl.cacheSynced, cvInformer.Informer().HasSynced)
	ctrl.coLister = coInformer.Lister()
	ctrl.cacheSynced = append(ctrl.cacheSynced, coInformer.Informer().HasSynced)

	return ctrl, nil
}

// Run runs the autoupdate controller.
func (ctrl *Controller) Run(ctx context.Context, workers int) error {
	defer ctrl.queue.ShutDown()

	klog.Info("Starting AutoUpdateController")
	defer klog.Info("Shutting down AutoUpdateController")

	if !cache.WaitForCacheSync(ctx.Done(), ctrl.cacheSynced...) {
		return fmt.Errorf("caches never synchronized: %w", ctx.Err())
	}
	var wg sync.WaitGroup
	wg.Add(workers)
	for i := 0; i < workers; i++ {
		go func() {
			defer wg.Done()
			defer utilruntime.HandleCrash()
			wait.UntilWithContext(ctx, ctrl.worker, time.Second)
		}()
	}

	wg.Wait()
	return nil
}

func (ctrl *Controller) eventHandler() cache.ResourceEventHandler {
	key := fmt.Sprintf("%s/%s", ctrl.namespace, ctrl.name)
	return cache.ResourceEventHandlerFuncs{
		AddFunc:    func(obj interface{}) { ctrl.queue.Add(key) },
		UpdateFunc: func(old, new interface{}) { ctrl.queue.Add(key) },
		DeleteFunc: func(obj interface{}) { ctrl.queue.Add(key) },
	}
}

func (ctrl *Controller) worker(ctx context.Context) {
	for ctrl.processNextWorkItem(ctx) {
	}
}

func (ctrl *Controller) processNextWorkItem(ctx context.Context) bool {
	key, quit := ctrl.queue.Get()
	if quit {
		return false
	}
	defer ctrl.queue.Done(key)

	err := ctrl.syncHandler(ctx, key.(string))
	ctrl.handleErr(err, key)

	return true
}

func (ctrl *Controller) handleErr(err error, key interface{}) {
	if err == nil {
		ctrl.queue.Forget(key)
		return
	}

	if ctrl.queue.NumRequeues(key) < maxRetries {
		klog.V(2).Infof("Error handling %v: %v", key, err)
		ctrl.queue.AddRateLimited(key)
		return
	}

	utilruntime.HandleError(err)
	klog.V(2).Infof("Dropping %q out of the queue: %v", key, err)
	ctrl.queue.Forget(key)
}

func (ctrl *Controller) sync(ctx context.Context, key string) error {
	startTime := time.Now()
	klog.V(2).Infof("Started syncing auto-updates %q", key)
	defer func() {
		klog.V(2).Infof("Finished syncing auto-updates %q (%v)", key, time.Since(startTime))
	}()

	clusterversion, err := ctrl.cvLister.Get(ctrl.name)
	if errors.IsNotFound(err) {
		klog.V(2).Infof("ClusterVersion %v has been deleted", key)
		return nil
	}
	if err != nil {
		return err
	}

	// Deep-copy otherwise we are mutating our cache.
	// TODO: Deep-copy only when needed.
	clusterversion = clusterversion.DeepCopy()

	if !updateAvail(clusterversion.Status.AvailableUpdates) {
		return nil
	}
	up := nextUpdate(clusterversion.Status.AvailableUpdates)
	clusterversion.Spec.DesiredUpdate = &v1.Update{
		Architecture: clusterversion.Spec.DesiredUpdate.Architecture,
		Version:      up.Version,
		Image:        up.Image,
	}

	_, updated, err := resourceapply.ApplyClusterVersionFromCache(ctx, ctrl.cvLister, ctrl.client.ConfigV1(), clusterversion)
	if updated {
		klog.Infof("Auto Update set to %v", up)
	}
	return err
}

func updateAvail(ups []v1.Release) bool {
	return len(ups) > 0
}

func nextUpdate(ups []v1.Release) v1.Release {
	sorted := ups
	sort.Slice(sorted, func(i, j int) bool {
		vi := semver.MustParse(sorted[i].Version)
		vj := semver.MustParse(sorted[j].Version)
		return vi.GTE(vj)
	})
	return sorted[0]
}

package autoupgrade

import (
	"fmt"
	"sort"
	"time"

	"github.com/blang/semver"

	"k8s.io/klog"
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
)

const (
	// maxRetries is the number of times a machineconfig pool will be retried before it is dropped out of the queue.
	// With the current rate-limiter in use (5ms*2^(maxRetries-1)) the following numbers represent the times
	// a machineconfig pool is going to be requeued:
	//
	// 5ms, 10ms, 20ms, 40ms, 80ms, 160ms, 320ms, 640ms, 1.3s, 2.6s, 5.1s, 10.2s, 20.4s, 41s, 82s
	maxRetries = 15
)

// Controller defines autoupgrade controller.
type Controller struct {
	// namespace and name are used to find the ClusterVersion, ClusterOperator.
	namespace, name string

	client        clientset.Interface
	eventRecorder record.EventRecorder

	syncHandler       func(key string) error
	statusSyncHandler func(key string) error

	cvLister    configlistersv1.ClusterVersionLister
	coLister    configlistersv1.ClusterOperatorLister
	cacheSynced []cache.InformerSynced

	// queue tracks keeping the list of available upgrades on a cluster version
	queue workqueue.RateLimitingInterface
}

// New returns a new autoupgrade controller.
func New(
	namespace, name string,
	cvInformer configinformersv1.ClusterVersionInformer,
	coInformer configinformersv1.ClusterOperatorInformer,
	client clientset.Interface,
	kubeClient kubernetes.Interface,
) *Controller {
	eventBroadcaster := record.NewBroadcaster()
	eventBroadcaster.StartLogging(klog.Infof)
	eventBroadcaster.StartRecordingToSink(&coreclientsetv1.EventSinkImpl{Interface: kubeClient.CoreV1().Events(namespace)})

	ctrl := &Controller{
		namespace:     namespace,
		name:          name,
		client:        client,
		eventRecorder: eventBroadcaster.NewRecorder(scheme.Scheme, corev1.EventSource{Component: "autoupgrader"}),
		queue:         workqueue.NewNamedRateLimitingQueue(workqueue.DefaultControllerRateLimiter(), "autoupgrader"),
	}

	cvInformer.Informer().AddEventHandler(ctrl.eventHandler())
	coInformer.Informer().AddEventHandler(ctrl.eventHandler())

	ctrl.syncHandler = ctrl.sync

	ctrl.cvLister = cvInformer.Lister()
	ctrl.cacheSynced = append(ctrl.cacheSynced, cvInformer.Informer().HasSynced)
	ctrl.coLister = coInformer.Lister()
	ctrl.cacheSynced = append(ctrl.cacheSynced, coInformer.Informer().HasSynced)

	return ctrl
}

// Run runs the autoupgrade controller.
func (ctrl *Controller) Run(workers int, stopCh <-chan struct{}) {
	defer utilruntime.HandleCrash()
	defer ctrl.queue.ShutDown()

	klog.Info("Starting AutoUpgradeController")
	defer klog.Info("Shutting down AutoUpgradeController")

	if !cache.WaitForCacheSync(stopCh, ctrl.cacheSynced...) {
		klog.Info("Caches never synchronized")
		return
	}

	for i := 0; i < workers; i++ {
		go wait.Until(ctrl.worker, time.Second, stopCh)
	}

	<-stopCh
}

func (ctrl *Controller) eventHandler() cache.ResourceEventHandler {
	key := fmt.Sprintf("%s/%s", ctrl.namespace, ctrl.name)
	return cache.ResourceEventHandlerFuncs{
		AddFunc:    func(obj interface{}) { ctrl.queue.Add(key) },
		UpgradeFunc: func(old, new interface{}) { ctrl.queue.Add(key) },
		DeleteFunc: func(obj interface{}) { ctrl.queue.Add(key) },
	}
}

func (ctrl *Controller) worker() {
	for ctrl.processNextWorkItem() {
	}
}

func (ctrl *Controller) processNextWorkItem() bool {
	key, quit := ctrl.queue.Get()
	if quit {
		return false
	}
	defer ctrl.queue.Done(key)

	err := ctrl.syncHandler(key.(string))
	ctrl.handleErr(err, key)

	return true
}

func (ctrl *Controller) handleErr(err error, key interface{}) {
	if err == nil {
		ctrl.queue.Forget(key)
		return
	}

	if ctrl.queue.NumRequeues(key) < maxRetries {
		klog.V(2).Infof("Error syncing controller %v: %v", key, err)
		ctrl.queue.AddRateLimited(key)
		return
	}

	utilruntime.HandleError(err)
	klog.V(2).Infof("Dropping controller %q out of the queue: %v", key, err)
	ctrl.queue.Forget(key)
}

func (ctrl *Controller) sync(key string) error {
	startTime := time.Now()
	klog.V(4).Infof("Started syncing auto-upgrades %q (%v)", key, startTime)
	defer func() {
		klog.V(4).Infof("Finished syncing auto-upgrades %q (%v)", key, time.Since(startTime))
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

	if !upgradeAvail(clusterversion.Status.AvailableUpdates) {
		return nil
	}
	up := nextUpgrade(clusterversion.Status.AvailableUpdates)
	clusterversion.Spec.DesiredUpdate = &up

	_, upgraded, err := resourceapply.ApplyClusterVersionFromCache(ctrl.cvLister, ctrl.client.ConfigV1(), clusterversion)
	if upgraded {
		klog.Infof("Auto Upgrade set to %v", up)
	}
	return err
}

func upgradeAvail(ups []v1.Upgrade) bool {
	return len(ups) > 0
}

func nextUpgrade(ups []v1.Upgrade) v1.Upgrade {
	sorted := ups
	sort.Slice(sorted, func(i, j int) bool {
		vi := semver.MustParse(sorted[i].Version)
		vj := semver.MustParse(sorted[j].Version)
		return vi.GTE(vj)
	})
	return sorted[0]
}

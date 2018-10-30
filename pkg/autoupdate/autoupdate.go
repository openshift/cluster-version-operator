package autoupdate

import (
	"fmt"
	"sort"
	"time"

	"github.com/blang/semver"

	"github.com/golang/glog"
	"github.com/openshift/cluster-version-operator/lib/resourceapply"
	"github.com/openshift/cluster-version-operator/pkg/apis/config.openshift.io/v1"
	clientset "github.com/openshift/cluster-version-operator/pkg/generated/clientset/versioned"
	"github.com/openshift/cluster-version-operator/pkg/generated/clientset/versioned/scheme"
	cvinformersv1 "github.com/openshift/cluster-version-operator/pkg/generated/informers/externalversions/config.openshift.io/v1"
	osinformersv1 "github.com/openshift/cluster-version-operator/pkg/generated/informers/externalversions/operatorstatus.openshift.io/v1"
	cvlistersv1 "github.com/openshift/cluster-version-operator/pkg/generated/listers/config.openshift.io/v1"
	oslistersv1 "github.com/openshift/cluster-version-operator/pkg/generated/listers/operatorstatus.openshift.io/v1"
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

// Controller defines autoupdate controller.
type Controller struct {
	// namespace and name are used to find the ClusterVersion, ClusterOperator.
	namespace, name string

	client        clientset.Interface
	eventRecorder record.EventRecorder

	syncHandler func(key string) error

	cvoConfigLister       cvlistersv1.ClusterVersionLister
	clusterOperatorLister oslistersv1.ClusterOperatorLister

	cvoConfigListerSynced cache.InformerSynced
	operatorStatusSynced  cache.InformerSynced

	// queue only ever has one item, but it has nice error handling backoff/retry semantics
	queue workqueue.RateLimitingInterface
}

// New returns a new autoupdate controller.
func New(
	namespace, name string,
	cvoConfigInformer cvinformersv1.ClusterVersionInformer,
	clusterOperatorInformer osinformersv1.ClusterOperatorInformer,
	client clientset.Interface,
	kubeClient kubernetes.Interface,
) *Controller {
	eventBroadcaster := record.NewBroadcaster()
	eventBroadcaster.StartLogging(glog.Infof)
	eventBroadcaster.StartRecordingToSink(&coreclientsetv1.EventSinkImpl{Interface: kubeClient.CoreV1().Events("")})

	ctrl := &Controller{
		namespace:     namespace,
		name:          name,
		client:        client,
		eventRecorder: eventBroadcaster.NewRecorder(scheme.Scheme, corev1.EventSource{Component: "autoupdater"}),
		queue:         workqueue.NewNamedRateLimitingQueue(workqueue.DefaultControllerRateLimiter(), "autoupdater"),
	}

	cvoConfigInformer.Informer().AddEventHandler(ctrl.eventHandler())
	clusterOperatorInformer.Informer().AddEventHandler(ctrl.eventHandler())

	ctrl.syncHandler = ctrl.sync

	ctrl.cvoConfigLister = cvoConfigInformer.Lister()
	ctrl.clusterOperatorLister = clusterOperatorInformer.Lister()

	ctrl.cvoConfigListerSynced = cvoConfigInformer.Informer().HasSynced
	ctrl.operatorStatusSynced = clusterOperatorInformer.Informer().HasSynced

	return ctrl
}

// Run runs the autoupdate controller.
func (ctrl *Controller) Run(workers int, stopCh <-chan struct{}) {
	defer utilruntime.HandleCrash()
	defer ctrl.queue.ShutDown()

	glog.Info("Starting AutoUpdateController")
	defer glog.Info("Shutting down AutoUpdateController")

	if !cache.WaitForCacheSync(stopCh,
		ctrl.cvoConfigListerSynced,
		ctrl.operatorStatusSynced,
	) {
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
		UpdateFunc: func(old, new interface{}) { ctrl.queue.Add(key) },
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
		glog.V(2).Infof("Error syncing controller %v: %v", key, err)
		ctrl.queue.AddRateLimited(key)
		return
	}

	utilruntime.HandleError(err)
	glog.V(2).Infof("Dropping controller %q out of the queue: %v", key, err)
	ctrl.queue.Forget(key)
}

func (ctrl *Controller) sync(key string) error {
	startTime := time.Now()
	glog.V(4).Infof("Started syncing controller %q (%v)", key, startTime)
	defer func() {
		glog.V(4).Infof("Finished syncing controller %q (%v)", key, time.Since(startTime))
	}()

	namespace, name, err := cache.SplitMetaNamespaceKey(key)
	if err != nil {
		return err
	}

	operatorstatus, err := ctrl.clusterOperatorLister.ClusterOperators(namespace).Get(name)
	if errors.IsNotFound(err) {
		glog.V(2).Infof("ClusterOperator %v has been deleted", key)
		return nil
	}
	if err != nil {
		return err
	}

	clusterversion, err := ctrl.cvoConfigLister.ClusterVersions(namespace).Get(name)
	if errors.IsNotFound(err) {
		glog.V(2).Infof("ClusterVersion %v has been deleted", key)
		return nil
	}
	if err != nil {
		return err
	}

	// Deep-copy otherwise we are mutating our cache.
	// TODO: Deep-copy only when needed.
	ops := operatorstatus.DeepCopy()
	config := new(v1.ClusterVersion)
	clusterversion.DeepCopyInto(config)

	obji, _, err := scheme.Codecs.UniversalDecoder().Decode(ops.Status.Extension.Raw, nil, &v1.CVOStatus{})
	if err != nil {
		return fmt.Errorf("unable to decode CVOStatus from extension.Raw: %v", err)
	}
	cvoststatus, ok := obji.(*v1.CVOStatus)
	if !ok {
		return fmt.Errorf("expected *v1.CVOStatus found %T", obji)
	}

	if !updateAvail(cvoststatus.AvailableUpdates) {
		return nil
	}
	up := nextUpdate(cvoststatus.AvailableUpdates)
	config.DesiredUpdate = up

	_, updated, err := resourceapply.ApplyClusterVersionFromCache(ctrl.cvoConfigLister, ctrl.client.ConfigV1(), config)
	if updated {
		glog.Infof("Auto Update set to %s", up)
	}
	return err
}

func updateAvail(ups []v1.Update) bool {
	return len(ups) > 0
}

func nextUpdate(ups []v1.Update) v1.Update {
	sorted := ups
	sort.Slice(sorted, func(i, j int) bool {
		vi := semver.MustParse(sorted[i].Version)
		vj := semver.MustParse(sorted[j].Version)
		return vi.GTE(vj)
	})
	return sorted[0]
}

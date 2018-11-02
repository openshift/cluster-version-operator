package cvo

import (
	"fmt"
	"sync"
	"time"

	"github.com/blang/semver"
	"github.com/golang/glog"
	"github.com/google/uuid"
	corev1 "k8s.io/api/core/v1"
	apiextclientset "k8s.io/apiextensions-apiserver/pkg/client/clientset/clientset"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/scheme"
	coreclientsetv1 "k8s.io/client-go/kubernetes/typed/core/v1"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/tools/record"
	"k8s.io/client-go/util/workqueue"

	"github.com/openshift/cluster-version-operator/lib/resourceapply"
	"github.com/openshift/cluster-version-operator/lib/resourcemerge"
	cvv1 "github.com/openshift/cluster-version-operator/pkg/apis/config.openshift.io/v1"
	osv1 "github.com/openshift/cluster-version-operator/pkg/apis/operatorstatus.openshift.io/v1"
	clientset "github.com/openshift/cluster-version-operator/pkg/generated/clientset/versioned"
	cvinformersv1 "github.com/openshift/cluster-version-operator/pkg/generated/informers/externalversions/config.openshift.io/v1"
	osinformersv1 "github.com/openshift/cluster-version-operator/pkg/generated/informers/externalversions/operatorstatus.openshift.io/v1"
	cvlistersv1 "github.com/openshift/cluster-version-operator/pkg/generated/listers/config.openshift.io/v1"
	oslistersv1 "github.com/openshift/cluster-version-operator/pkg/generated/listers/operatorstatus.openshift.io/v1"
)

const (
	// maxRetries is the number of times a machineconfig pool will be retried before it is dropped out of the queue.
	// With the current rate-limiter in use (5ms*2^(maxRetries-1)) the following numbers represent the times
	// a machineconfig pool is going to be requeued:
	//
	// 5ms, 10ms, 20ms, 40ms, 80ms, 160ms, 320ms, 640ms, 1.3s, 2.6s, 5.1s, 10.2s, 20.4s, 41s, 82s
	maxRetries = 15
)

// ownerKind contains the schema.GroupVersionKind for type that owns objects managed by CVO.
var ownerKind = cvv1.SchemeGroupVersion.WithKind("ClusterVersion")

// Operator defines cluster version operator.
type Operator struct {
	// nodename allows CVO to sync fetchPayload to same node as itself.
	nodename string
	// namespace and name are used to find the ClusterVersion, OperatorStatus.
	namespace, name string

	// releaseImage allows templating CVO deployment manifest.
	releaseImage string
	// releaseVersion is a string identifier for the current version, read
	// from the payload of the operator. It may be empty if no version exists, in
	// which case no available updates will be returned.
	releaseVersion string

	// minimumUpdateCheckInterval is the minimum duration to check for updates from
	// the upstream.
	minimumUpdateCheckInterval time.Duration

	// restConfig is used to create resourcebuilder.
	restConfig *rest.Config

	client        clientset.Interface
	kubeClient    kubernetes.Interface
	apiExtClient  apiextclientset.Interface
	eventRecorder record.EventRecorder

	syncHandler                 func(key string) error
	availableUpdatesSyncHandler func(key string) error

	// payloadDir is intended for testing. If unset it will default to '/'
	payloadDir string
	// defaultUpstreamServer is intended for testing.
	defaultUpstreamServer string

	clusterOperatorLister oslistersv1.ClusterOperatorLister
	cvoConfigLister       cvlistersv1.ClusterVersionLister
	clusterOperatorSynced cache.InformerSynced
	cvoConfigListerSynced cache.InformerSynced

	// queue tracks applying updates to a cluster.
	queue workqueue.RateLimitingInterface
	// availableUpdatesQueue tracks checking for updates from the update server.
	availableUpdatesQueue workqueue.RateLimitingInterface

	lastAtLock     sync.Mutex
	lastSyncAt     time.Time
	lastRetrieveAt time.Time
}

// New returns a new cluster version operator.
func New(
	nodename string,
	namespace, name string,
	releaseImage string,
	overridePayloadDir string,
	minimumInterval time.Duration,
	cvoConfigInformer cvinformersv1.ClusterVersionInformer,
	clusterOperatorInformer osinformersv1.ClusterOperatorInformer,
	restConfig *rest.Config,
	client clientset.Interface,
	kubeClient kubernetes.Interface,
	apiExtClient apiextclientset.Interface,
) *Operator {
	eventBroadcaster := record.NewBroadcaster()
	eventBroadcaster.StartLogging(glog.Infof)
	eventBroadcaster.StartRecordingToSink(&coreclientsetv1.EventSinkImpl{Interface: kubeClient.CoreV1().Events("")})

	optr := &Operator{
		nodename:     nodename,
		namespace:    namespace,
		name:         name,
		releaseImage: releaseImage,
		payloadDir:   overridePayloadDir,

		minimumUpdateCheckInterval: minimumInterval,

		defaultUpstreamServer: "http://localhost:8080/graph",

		restConfig:    restConfig,
		client:        client,
		kubeClient:    kubeClient,
		apiExtClient:  apiExtClient,
		eventRecorder: eventBroadcaster.NewRecorder(scheme.Scheme, corev1.EventSource{Component: "clusterversionoperator"}),
		queue:         workqueue.NewNamedRateLimitingQueue(workqueue.DefaultControllerRateLimiter(), "clusterversion"),
		availableUpdatesQueue: workqueue.NewNamedRateLimitingQueue(workqueue.DefaultControllerRateLimiter(), "availableupdates"),
	}

	cvoConfigInformer.Informer().AddEventHandler(optr.eventHandler())
	clusterOperatorInformer.Informer().AddEventHandler(optr.eventHandler())

	optr.syncHandler = optr.sync
	optr.availableUpdatesSyncHandler = optr.availableUpdatesSync

	optr.clusterOperatorLister = clusterOperatorInformer.Lister()
	optr.clusterOperatorSynced = clusterOperatorInformer.Informer().HasSynced

	optr.cvoConfigLister = cvoConfigInformer.Lister()
	optr.cvoConfigListerSynced = cvoConfigInformer.Informer().HasSynced

	if meta, _, err := loadUpdatePayloadMetadata(optr.baseDirectory(), releaseImage); err != nil {
		glog.Warningf("The local payload is invalid - no current version can be determined from disk: %v", err)
	} else {
		// XXX: set this to the cincinnati version in preference
		if _, err := semver.Parse(meta.imageRef.Name); err != nil {
			glog.Warningf("The local payload name %q is not a valid semantic version - no current version will be reported: %v", meta.imageRef.Name, err)
		} else {
			optr.releaseVersion = meta.imageRef.Name
		}
	}

	return optr
}

// Run runs the cluster version operator.
func (optr *Operator) Run(workers int, stopCh <-chan struct{}) {
	defer utilruntime.HandleCrash()
	defer optr.queue.ShutDown()

	glog.Infof("Starting ClusterVersionOperator with minimum reconcile period %s", optr.minimumUpdateCheckInterval)
	defer glog.Info("Shutting down ClusterVersionOperator")

	if !cache.WaitForCacheSync(stopCh,
		optr.clusterOperatorSynced,
		optr.cvoConfigListerSynced,
	) {
		return
	}

	// trigger the first cluster version reconcile always
	optr.queue.Add(fmt.Sprintf("%s/%s", optr.namespace, optr.name))

	go wait.Until(func() { optr.worker(optr.queue, optr.syncHandler) }, time.Second, stopCh)
	go wait.Until(func() { optr.worker(optr.availableUpdatesQueue, optr.availableUpdatesSyncHandler) }, time.Second, stopCh)

	<-stopCh
}

func (optr *Operator) eventHandler() cache.ResourceEventHandler {
	workQueueKey := fmt.Sprintf("%s/%s", optr.namespace, optr.name)
	return cache.ResourceEventHandlerFuncs{
		AddFunc: func(obj interface{}) {
			optr.queue.Add(workQueueKey)
			optr.availableUpdatesQueue.Add(workQueueKey)
		},
		UpdateFunc: func(old, new interface{}) {
			optr.queue.Add(workQueueKey)
			optr.availableUpdatesQueue.Add(workQueueKey)
		},
		DeleteFunc: func(obj interface{}) {
			optr.queue.Add(workQueueKey)
			optr.availableUpdatesQueue.Add(workQueueKey)
		},
	}
}

func (optr *Operator) worker(queue workqueue.RateLimitingInterface, syncHandler func(string) error) {
	for processNextWorkItem(queue, syncHandler, optr.syncDegradedStatus) {
	}
}

func processNextWorkItem(queue workqueue.RateLimitingInterface, syncHandler func(string) error, syncDegradedStatus func(config *cvv1.ClusterVersion, err error) error) bool {
	key, quit := queue.Get()
	if quit {
		return false
	}
	defer queue.Done(key)

	err := syncHandler(key.(string))
	handleErr(queue, err, key, syncDegradedStatus)
	return true
}

func handleErr(queue workqueue.RateLimitingInterface, err error, key interface{}, syncDegradedStatus func(config *cvv1.ClusterVersion, err error) error) {
	if err == nil {
		queue.Forget(key)
		return
	}

	if queue.NumRequeues(key) < maxRetries {
		glog.V(2).Infof("Error syncing operator %v: %v", key, err)
		queue.AddRateLimited(key)
		return
	}

	err = syncDegradedStatus(nil, err)
	utilruntime.HandleError(err)
	glog.V(2).Infof("Dropping operator %q out of the queue %v: %v", key, queue, err)
	queue.Forget(key)
}

func (optr *Operator) sync(key string) error {
	startTime := time.Now()
	glog.V(4).Infof("Started syncing cluster version %q (%v)", key, startTime)
	defer func() {
		glog.V(4).Infof("Finished syncing cluster version %q (%v)", key, time.Since(startTime))
	}()

	config, err := optr.getOrCreateClusterVersion()
	if err != nil {
		return err
	}

	// when we're up to date, limit how frequently we check the payload
	availableAndUpdated := config.Status.Generation == config.Generation &&
		resourcemerge.IsOperatorStatusConditionTrue(config.Status.Conditions, osv1.OperatorAvailable)
	hasRecentlySynced := availableAndUpdated && optr.hasRecentlySynced()
	if hasRecentlySynced {
		glog.V(2).Infof("Cluster version has been recently synced and no new changes detected")
		return nil
	}

	optr.setLastSyncAt(time.Time{})

	// read the payload
	payload, err := optr.loadUpdatePayload(config)
	if err != nil {
		// the payload is invalid, try and update the status to indicate that
		if sErr := optr.syncDegradedStatus(config, err); sErr != nil {
			glog.V(2).Infof("Unable to write status when payload was invalid: %v", sErr)
		}
		return err
	}

	config = config.DeepCopy()

	update := cvv1.Update{
		Version: payload.releaseVersion,
		Payload: payload.releaseImage,
	}

	// if the current payload is already live, we are reconciling, not updating,
	// and we won't set the progressing status.
	if availableAndUpdated && payload.manifestHash == config.Status.VersionHash {
		glog.V(2).Infof("Reconciling cluster to version %s and image %s (hash=%s)", update.Version, update.Payload, payload.manifestHash)
	} else {
		glog.V(2).Infof("Updating the cluster to version %s and image %s (hash=%s)", update.Version, update.Payload, payload.manifestHash)
		if err := optr.syncProgressingStatus(config); err != nil {
			return err
		}
	}

	if err := optr.syncUpdatePayload(config, payload); err != nil {
		return err
	}

	glog.V(2).Infof("Payload for cluster version %s synced", update.Version)

	// update the status to indicate we have synced
	optr.setLastSyncAt(time.Now())
	config.Status.VersionHash = payload.manifestHash
	config.Status.Current = update
	return optr.syncAvailableStatus(config)
}

// availableUpdatesSync is triggered on cluster version change (and periodic requeues) to
// sync available updates. It only modifies cluster version.
func (optr *Operator) availableUpdatesSync(key string) error {
	startTime := time.Now()
	glog.V(4).Infof("Started syncing available updates %q (%v)", key, startTime)
	defer func() {
		glog.V(4).Infof("Finished syncing available updates %q (%v)", key, time.Since(startTime))
	}()

	config, err := optr.cvoConfigLister.Get(optr.name)
	if apierrors.IsNotFound(err) {
		return nil
	}
	if err != nil {
		return err
	}

	return optr.syncAvailableUpdates(config)
}

// hasRecentlySynced returns true if the most recent sync was newer than the
// minimum check interval.
func (optr *Operator) hasRecentlySynced() bool {
	if optr.minimumUpdateCheckInterval == 0 {
		return false
	}
	optr.lastAtLock.Lock()
	defer optr.lastAtLock.Unlock()
	return optr.lastSyncAt.After(time.Now().Add(-optr.minimumUpdateCheckInterval))
}

// setLastSyncAt sets the time the operator was last synced at.
func (optr *Operator) setLastSyncAt(t time.Time) {
	optr.lastAtLock.Lock()
	defer optr.lastAtLock.Unlock()
	optr.lastSyncAt = t
}

func (optr *Operator) getOrCreateClusterVersion() (*cvv1.ClusterVersion, error) {
	obj, err := optr.cvoConfigLister.Get(optr.name)
	if err == nil {
		return obj, nil
	}
	if !apierrors.IsNotFound(err) {
		return nil, err
	}
	var upstream *cvv1.URL
	if len(optr.defaultUpstreamServer) > 0 {
		u := cvv1.URL(optr.defaultUpstreamServer)
		upstream = &u
	}
	channel := "fast"
	id, _ := uuid.NewRandom()
	if id.Variant() != uuid.RFC4122 {
		return nil, fmt.Errorf("invalid %q, must be an RFC4122-variant UUID: found %s", id, id.Variant())
	}
	if id.Version() != 4 {
		return nil, fmt.Errorf("Invalid %q, must be a version-4 UUID: found %s", id, id.Version())
	}

	// XXX: generate ClusterVersion from options calculated above.
	config := &cvv1.ClusterVersion{
		ObjectMeta: metav1.ObjectMeta{
			Name: optr.name,
		},
		Spec: cvv1.ClusterVersionSpec{
			Upstream:  upstream,
			Channel:   channel,
			ClusterID: cvv1.ClusterID(id.String()),
		},
	}

	actual, _, err := resourceapply.ApplyClusterVersionFromCache(optr.cvoConfigLister, optr.client.ConfigV1(), config)
	return actual, err
}

// versionString returns a string describing the current version.
func (optr *Operator) currentVersionString(config *cvv1.ClusterVersion) string {
	if s := config.Status.Current.Version; len(s) > 0 {
		return s
	}
	if s := config.Status.Current.Payload; len(s) > 0 {
		return s
	}
	if s := optr.releaseVersion; len(s) > 0 {
		return s
	}
	if s := optr.releaseImage; len(s) > 0 {
		return s
	}
	return "<unknown>"
}

// versionString returns a string describing the desired version.
func (optr *Operator) desiredVersionString(config *cvv1.ClusterVersion) string {
	var s string
	if v := config.Spec.DesiredUpdate; v != nil {
		if len(v.Payload) > 0 {
			s = v.Payload
		}
		if len(v.Version) > 0 {
			s = v.Version
		}
	}
	if len(s) == 0 {
		s = optr.currentVersionString(config)
	}
	return s
}

// currentVersion returns an update object describing the current known cluster version.
func (optr *Operator) currentVersion() cvv1.Update {
	return cvv1.Update{
		Version: optr.releaseVersion,
		Payload: optr.releaseImage,
	}
}

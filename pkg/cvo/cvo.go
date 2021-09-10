package cvo

import (
	"context"
	"crypto/tls"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"sync"
	"time"

	"github.com/google/uuid"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/apimachinery/pkg/util/wait"
	informerscorev1 "k8s.io/client-go/informers/core/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/scheme"
	coreclientsetv1 "k8s.io/client-go/kubernetes/typed/core/v1"
	listerscorev1 "k8s.io/client-go/listers/core/v1"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/tools/record"
	"k8s.io/client-go/transport"
	"k8s.io/client-go/util/workqueue"
	"k8s.io/klog/v2"

	configv1 "github.com/openshift/api/config/v1"
	clientset "github.com/openshift/client-go/config/clientset/versioned"
	configinformersv1 "github.com/openshift/client-go/config/informers/externalversions/config/v1"
	configlistersv1 "github.com/openshift/client-go/config/listers/config/v1"
	"github.com/openshift/cluster-version-operator/lib/resourceapply"
	"github.com/openshift/cluster-version-operator/lib/resourcebuilder"
	"github.com/openshift/cluster-version-operator/lib/validation"
	cvointernal "github.com/openshift/cluster-version-operator/pkg/cvo/internal"
	"github.com/openshift/cluster-version-operator/pkg/cvo/internal/dynamicclient"
	"github.com/openshift/cluster-version-operator/pkg/internal"
	"github.com/openshift/cluster-version-operator/pkg/payload"
	"github.com/openshift/cluster-version-operator/pkg/payload/precondition"
	preconditioncv "github.com/openshift/cluster-version-operator/pkg/payload/precondition/clusterversion"
	"github.com/openshift/library-go/pkg/manifest"
	"github.com/openshift/library-go/pkg/verify"
	"github.com/openshift/library-go/pkg/verify/store/configmap"
	"github.com/openshift/library-go/pkg/verify/store/sigstore"
)

const (
	// maxRetries is the number of times a work-item will be retried before it is dropped out of the queue.
	maxRetries = 15
)

// Operator defines cluster version operator. The CVO attempts to reconcile the appropriate image
// onto the cluster, writing status to the ClusterVersion object as it goes. A background loop
// periodically checks for new updates from a server described by spec.upstream and spec.channel.
// The main CVO sync loop is the single writer of ClusterVersion status.
//
// The CVO updates multiple conditions, but synthesizes them into a summary message on the
// Progressing condition to answer the question of "what version is available on the cluster".
// When errors occur, the Failing condition of the status is updated with a detailed message and
// reason, and then the reason is used to summarize the error onto the Progressing condition's
// message for a simple overview.
//
// The CVO periodically syncs the whole image to the cluster even if no version transition is
// detected in order to undo accidental actions.
//
// A release image is expected to contain a CVO binary, the manifests necessary to update the
// CVO, and the manifests of the other operators on the cluster. During an update the operator
// attempts to copy the contents of the image manifests into a temporary directory using a
// batch job and a shared host-path, then applies the CVO manifests using the image image
// for the CVO deployment. The deployment is then expected to launch the new process, and the
// new operator picks up the lease and applies the rest of the image.
type Operator struct {
	// nodename allows CVO to sync fetchPayload to same node as itself.
	nodename string
	// namespace and name are used to find the ClusterVersion, OperatorStatus.
	namespace, name string

	// release is the release the current operator points to and
	// metadata read from the release image.  It allows templating of
	// the CVO deployment manifest.
	//
	// Fetch via currentVersion() to populate metadata from
	// availableUpdates.
	release configv1.Release
	// releaseCreated, if set, is the timestamp of the current update.
	releaseCreated time.Time

	// enableDefaultClusterVersion allows the operator to create a
	// ClusterVersion object if one does not already exist.
	enableDefaultClusterVersion bool

	client        clientset.Interface
	kubeClient    kubernetes.Interface
	eventRecorder record.EventRecorder

	// minimumUpdateCheckInterval is the minimum duration to check for updates from
	// the upstream.
	minimumUpdateCheckInterval time.Duration
	// payloadDir is intended for testing. If unset it will default to '/'
	payloadDir string
	// defaultUpstreamServer is intended for testing.
	defaultUpstreamServer string
	// syncBackoff allows the tests to use a quicker backoff
	syncBackoff wait.Backoff

	cvLister              configlistersv1.ClusterVersionLister
	coLister              configlistersv1.ClusterOperatorLister
	cmConfigLister        listerscorev1.ConfigMapNamespaceLister
	cmConfigManagedLister listerscorev1.ConfigMapNamespaceLister
	proxyLister           configlistersv1.ProxyLister
	cacheSynced           []cache.InformerSynced

	// queue tracks applying updates to a cluster.
	queue workqueue.RateLimitingInterface
	// availableUpdatesQueue tracks checking for updates from the update server.
	availableUpdatesQueue workqueue.RateLimitingInterface
	// upgradeableQueue tracks checking for upgradeable.
	upgradeableQueue workqueue.RateLimitingInterface

	// statusLock guards access to modifying available updates
	statusLock       sync.Mutex
	availableUpdates *availableUpdates

	// upgradeableStatusLock guards access to modifying Upgradeable conditions
	upgradeableStatusLock sync.Mutex
	upgradeable           *upgradeable
	upgradeableChecks     []upgradeableCheck

	// verifier, if provided, will be used to check an update before it is executed.
	// Any error will prevent an update payload from being accessed.
	verifier verify.Interface
	// signatureStore, if set, will be used to periodically persist signatures to
	// the cluster as a config map
	signatureStore *verify.StorePersister

	configSync ConfigSyncWorker
	// statusInterval is how often the configSync worker is allowed to retrigger
	// the main sync status loop.
	statusInterval time.Duration

	// lastAtLock guards access to controller memory about the sync loop
	lastAtLock          sync.Mutex
	lastResourceVersion int64

	// exclude is an optional identifier used to exclude certain manifests
	// via annotation
	exclude string

	clusterProfile string
}

// New returns a new cluster version operator.
func New(
	nodename string,
	namespace, name string,
	releaseImage string,
	enableDefaultClusterVersion bool,
	overridePayloadDir string,
	minimumInterval time.Duration,
	cvInformer configinformersv1.ClusterVersionInformer,
	coInformer configinformersv1.ClusterOperatorInformer,
	cmConfigInformer informerscorev1.ConfigMapInformer,
	cmConfigManagedInformer informerscorev1.ConfigMapInformer,
	proxyInformer configinformersv1.ProxyInformer,
	client clientset.Interface,
	kubeClient kubernetes.Interface,
	exclude string,
	clusterProfile string,
) *Operator {
	eventBroadcaster := record.NewBroadcaster()
	eventBroadcaster.StartLogging(klog.Infof)
	eventBroadcaster.StartRecordingToSink(&coreclientsetv1.EventSinkImpl{Interface: kubeClient.CoreV1().Events(namespace)})

	optr := &Operator{
		nodename:  nodename,
		namespace: namespace,
		name:      name,
		release: configv1.Release{
			Image: releaseImage,
		},

		enableDefaultClusterVersion: enableDefaultClusterVersion,

		statusInterval:             15 * time.Second,
		minimumUpdateCheckInterval: minimumInterval,
		payloadDir:                 overridePayloadDir,
		defaultUpstreamServer:      "https://api.openshift.com/api/upgrades_info/v1/graph",

		client:        client,
		kubeClient:    kubeClient,
		eventRecorder: eventBroadcaster.NewRecorder(scheme.Scheme, corev1.EventSource{Component: namespace}),

		queue:                 workqueue.NewNamedRateLimitingQueue(workqueue.DefaultControllerRateLimiter(), "clusterversion"),
		availableUpdatesQueue: workqueue.NewNamedRateLimitingQueue(workqueue.DefaultControllerRateLimiter(), "availableupdates"),
		upgradeableQueue:      workqueue.NewNamedRateLimitingQueue(workqueue.DefaultControllerRateLimiter(), "upgradeable"),

		exclude:        exclude,
		clusterProfile: clusterProfile,
	}

	cvInformer.Informer().AddEventHandler(optr.eventHandler())

	optr.coLister = coInformer.Lister()
	optr.cacheSynced = append(optr.cacheSynced, coInformer.Informer().HasSynced)

	optr.cvLister = cvInformer.Lister()
	optr.cacheSynced = append(optr.cacheSynced, cvInformer.Informer().HasSynced)

	optr.proxyLister = proxyInformer.Lister()
	optr.cmConfigLister = cmConfigInformer.Lister().ConfigMaps(internal.ConfigNamespace)
	optr.cmConfigManagedLister = cmConfigManagedInformer.Lister().ConfigMaps(internal.ConfigManagedNamespace)

	// make sure this is initialized after all the listers are initialized
	optr.upgradeableChecks = optr.defaultUpgradeableChecks()

	return optr
}

// InitializeFromPayload retrieves the payload contents and verifies the initial state, then configures the
// controller that loads and applies content to the cluster. It returns an error if the payload appears to
// be in error rather than continuing.
func (optr *Operator) InitializeFromPayload(restConfig *rest.Config, burstRestConfig *rest.Config) error {
	update, err := payload.LoadUpdate(optr.defaultPayloadDir(), optr.release.Image, optr.exclude, optr.clusterProfile)
	if err != nil {
		return fmt.Errorf("the local release contents are invalid - no current version can be determined from disk: %v", err)
	}

	optr.release = update.Release
	optr.releaseCreated = update.ImageRef.CreationTimestamp.Time

	httpClientConstructor := sigstore.NewCachedHTTPClientConstructor(optr.HTTPClient, nil)
	configClient, err := coreclientsetv1.NewForConfig(restConfig)
	if err != nil {
		return fmt.Errorf("unable to create a configuration client: %v", err)
	}

	// attempt to load a verifier as defined in the payload
	verifier, signatureStore, err := loadConfigMapVerifierDataFromUpdate(update, httpClientConstructor.HTTPClient, configClient)
	if err != nil {
		return err
	}
	if verifier != nil {
		klog.Infof("Verifying release authenticity: %v", verifier)
	} else {
		klog.Warningf("WARNING: No release authenticity verification is configured, all releases are considered unverified")
		verifier = verify.Reject
	}
	optr.verifier = verifier
	optr.signatureStore = signatureStore

	// after the verifier has been loaded, initialize the sync worker with a payload retriever
	// which will consume the verifier
	optr.configSync = NewSyncWorkerWithPreconditions(
		optr.defaultPayloadRetriever(),
		NewResourceBuilder(restConfig, burstRestConfig, &dummyContextOperatorGetter{wrapped: optr.coLister}),
		optr.defaultPreconditionChecks(),
		optr.minimumUpdateCheckInterval,
		wait.Backoff{
			Duration: time.Second * 10,
			Factor:   1.3,
			Steps:    3,
		},
		optr.exclude,
		optr.eventRecorder,
		optr.clusterProfile,
	)

	return nil
}

// loadConfigMapVerifierDataFromUpdate fetches the first config map in the payload with the correct annotation.
// It returns an error if the data is not valid, or no verifier if no config map is found. See the verify
// package for more details on the algorithm for verification. If the annotation is set, a verifier or error
// is always returned.
func loadConfigMapVerifierDataFromUpdate(update *payload.Update, clientBuilder sigstore.HTTPClient, configMapClient coreclientsetv1.ConfigMapsGetter) (verify.Interface, *verify.StorePersister, error) {
	verifier, err := verify.NewFromManifests(update.Manifests, clientBuilder)
	if err != nil {
		return nil, nil, err
	}
	if verifier == nil {
		return nil, nil, nil
	}

	// allow the verifier to consult the cluster for signature data, and also configure
	// a process that writes signatures back to that store
	signatureStore := configmap.NewStore(configMapClient, nil)
	verifier.AddStore(signatureStore)
	persister := verify.NewSignatureStorePersister(signatureStore, verifier)
	return verifier, persister, nil
}

// Run runs the cluster version operator until runContext.Done() and
// then attempts a clean shutdown limited by shutdownContext.Done().
// Assumes runContext.Done() occurs before or simultaneously with
// shutdownContext.Done().
func (optr *Operator) Run(runContext context.Context, shutdownContext context.Context, workers int) error {
	defer optr.queue.ShutDown()
	defer optr.availableUpdatesQueue.ShutDown()
	defer optr.upgradeableQueue.ShutDown()
	stopCh := runContext.Done()

	klog.Infof("Starting ClusterVersionOperator with minimum reconcile period %s", optr.minimumUpdateCheckInterval)
	defer klog.Info("Shutting down ClusterVersionOperator")

	resultChannel := make(chan asyncResult, 1)
	resultChannelCount := 0

	if !cache.WaitForCacheSync(stopCh, optr.cacheSynced...) {
		return fmt.Errorf("caches never synchronized: %w", runContext.Err())
	}

	// trigger the first cluster version reconcile always
	optr.queue.Add(optr.queueKey())

	resultChannelCount++
	go func() {
		defer utilruntime.HandleCrash()
		runThrottledStatusNotifier(runContext, optr.statusInterval, 2, optr.configSync.StatusCh(), func() { optr.queue.Add(optr.queueKey()) })
		resultChannel <- asyncResult{name: "status notifier"}
	}()

	resultChannelCount++
	go func() {
		defer utilruntime.HandleCrash()
		optr.configSync.Start(runContext, 16, optr.name, optr.cvLister)
		resultChannel <- asyncResult{name: "sync worker"}
	}()

	resultChannelCount++
	go func() {
		defer utilruntime.HandleCrash()
		wait.UntilWithContext(runContext, func(runContext context.Context) {
			optr.worker(runContext, optr.availableUpdatesQueue, optr.availableUpdatesSync)
		}, time.Second)
		resultChannel <- asyncResult{name: "available updates"}
	}()

	resultChannelCount++
	go func() {
		defer utilruntime.HandleCrash()
		wait.UntilWithContext(runContext, func(runContext context.Context) { optr.worker(runContext, optr.upgradeableQueue, optr.upgradeableSync) }, time.Second)
		resultChannel <- asyncResult{name: "upgradeable"}
	}()

	resultChannelCount++
	go func() {
		defer utilruntime.HandleCrash()
		wait.UntilWithContext(runContext, func(runContext context.Context) {
			// run the worker, then when the queue is closed sync one final time to flush any pending status
			optr.worker(runContext, optr.queue, func(runContext context.Context, key string) error { return optr.sync(runContext, key) })
			if err := optr.sync(shutdownContext, optr.queueKey()); err != nil {
				utilruntime.HandleError(fmt.Errorf("unable to perform final sync: %v", err))
			}
		}, time.Second)
		resultChannel <- asyncResult{name: "cluster version sync"}
	}()

	if optr.signatureStore != nil {
		resultChannelCount++
		go func() {
			defer utilruntime.HandleCrash()
			optr.signatureStore.Run(runContext, optr.minimumUpdateCheckInterval*2)
			resultChannel <- asyncResult{name: "signature store"}
		}()
	}

	shutdown := false
	var loopError error
	for resultChannelCount > 0 {
		klog.Infof("Waiting on %d outstanding goroutines.", resultChannelCount)
		if shutdown {
			select {
			case result := <-resultChannel:
				resultChannelCount--
				if result.error == nil {
					klog.Infof("Collected %s goroutine.", result.name)
				} else {
					klog.Errorf("Collected %s goroutine: %v", result.name, result.error)
					loopError = result.error
				}
			case <-shutdownContext.Done(): // out of time
				klog.Errorf("Abandoning %d uncollected goroutines", resultChannelCount)
				return shutdownContext.Err()
			}
		} else {
			select {
			case <-runContext.Done(): // clean shutdown
			case result := <-resultChannel: // crashed before a shutdown was requested
				resultChannelCount--
				if result.error == nil {
					klog.Infof("Collected %s goroutine.", result.name)
				} else {
					klog.Errorf("Collected %s goroutine: %v", result.name, result.error)
					loopError = result.error
				}
			}
			shutdown = true
			optr.queue.ShutDown()
			optr.availableUpdatesQueue.ShutDown()
			optr.upgradeableQueue.ShutDown()
		}
	}

	return loopError
}

func (optr *Operator) queueKey() string {
	return fmt.Sprintf("%s/%s", optr.namespace, optr.name)
}

// eventHandler queues an update for the cluster version on any change to the given object.
// Callers should use this with a scoped informer.
func (optr *Operator) eventHandler() cache.ResourceEventHandler {
	workQueueKey := optr.queueKey()
	return cache.ResourceEventHandlerFuncs{
		AddFunc: func(obj interface{}) {
			optr.queue.Add(workQueueKey)
			optr.availableUpdatesQueue.Add(workQueueKey)
			optr.upgradeableQueue.Add(workQueueKey)
		},
		UpdateFunc: func(old, new interface{}) {
			optr.queue.Add(workQueueKey)
			optr.availableUpdatesQueue.Add(workQueueKey)
			optr.upgradeableQueue.Add(workQueueKey)
		},
		DeleteFunc: func(obj interface{}) {
			optr.queue.Add(workQueueKey)
		},
	}
}

func (optr *Operator) worker(ctx context.Context, queue workqueue.RateLimitingInterface, syncHandler func(context.Context, string) error) {
	for processNextWorkItem(ctx, queue, syncHandler, optr.syncFailingStatus) {
	}
}

type syncFailingStatusFunc func(ctx context.Context, config *configv1.ClusterVersion, err error) error

func processNextWorkItem(ctx context.Context, queue workqueue.RateLimitingInterface, syncHandler func(context.Context, string) error, syncFailingStatus syncFailingStatusFunc) bool {
	key, quit := queue.Get()
	if quit {
		return false
	}
	defer queue.Done(key)

	err := syncHandler(ctx, key.(string))
	handleErr(ctx, queue, err, key, syncFailingStatus)
	return true
}

func handleErr(ctx context.Context, queue workqueue.RateLimitingInterface, err error, key interface{}, syncFailingStatus syncFailingStatusFunc) {
	if err == nil {
		queue.Forget(key)
		return
	}

	if queue.NumRequeues(key) < maxRetries {
		klog.V(2).Infof("Error handling %v: %v", key, err)
		queue.AddRateLimited(key)
		return
	}

	err = syncFailingStatus(ctx, nil, err)
	utilruntime.HandleError(err)
	klog.V(2).Infof("Dropping %q out of the queue %v: %v", key, queue, err)
	queue.Forget(key)
}

// sync ensures:
//
// 1. A ClusterVersion object exists
// 2. The ClusterVersion object has the appropriate status for the state of the cluster
// 3. The configSync object is kept up to date maintaining the user's desired version
//
// It returns an error if it could not update the cluster version object.
func (optr *Operator) sync(ctx context.Context, key string) error {
	startTime := time.Now()
	klog.V(4).Infof("Started syncing cluster version %q (%v)", key, startTime)
	defer func() {
		klog.V(4).Infof("Finished syncing cluster version %q (%v)", key, time.Since(startTime))
	}()

	// ensure the cluster version exists, that the object is valid, and that
	// all initial conditions are set.
	original, changed, err := optr.getOrCreateClusterVersion(ctx, optr.enableDefaultClusterVersion)
	if err != nil {
		return err
	}
	if changed {
		klog.V(4).Infof("Cluster version changed, waiting for newer event")
		return nil
	}
	if original == nil {
		klog.V(4).Infof("No ClusterVersion object and defaulting not enabled, waiting for one")
		return nil
	}

	// ensure that the object we do have is valid
	errs := validation.ValidateClusterVersion(original)
	// for fields that have meaning that are incomplete, clear them
	// prevents us from loading clearly malformed payloads
	config := validation.ClearInvalidFields(original, errs)

	// identify the desired next version
	desired, ok := findUpdateFromConfig(config)
	if ok {
		klog.V(4).Infof("Desired version from spec is %#v", desired)
	} else {
		currentVersion := optr.currentVersion()
		desired = configv1.Update{
			Version: currentVersion.Version,
			Image:   currentVersion.Image,
		}
		klog.V(4).Infof("Desired version from operator is %#v", desired)
	}

	// handle the case of a misconfigured CVO by doing nothing
	if len(desired.Image) == 0 {
		return optr.syncStatus(ctx, original, config, &SyncWorkerStatus{
			Failure: &payload.UpdateError{
				Reason:  "NoDesiredImage",
				Message: "No configured operator version, unable to update cluster",
			},
		}, errs)
	}

	// identify an initial state to inform the sync loop of
	var state payload.State
	switch {
	case hasNeverReachedLevel(config):
		state = payload.InitializingPayload
	case hasReachedLevel(config, desired):
		state = payload.ReconcilingPayload
	default:
		state = payload.UpdatingPayload
	}

	// inform the config sync loop about our desired state
	status := optr.configSync.Update(config.Generation, desired, config.Spec.Overrides, state)

	// write cluster version status
	return optr.syncStatus(ctx, original, config, status, errs)
}

// availableUpdatesSync is triggered on cluster version change (and periodic requeues) to
// sync available updates. It only modifies cluster version.
func (optr *Operator) availableUpdatesSync(ctx context.Context, key string) error {
	startTime := time.Now()
	klog.V(4).Infof("Started syncing available updates %q (%v)", key, startTime)
	defer func() {
		klog.V(4).Infof("Finished syncing available updates %q (%v)", key, time.Since(startTime))
	}()

	config, err := optr.cvLister.Get(optr.name)
	if apierrors.IsNotFound(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if errs := validation.ValidateClusterVersion(config); len(errs) > 0 {
		return nil
	}
	return optr.syncAvailableUpdates(ctx, config)
}

// upgradeableSync is triggered on cluster version change (and periodic requeues) to
// sync upgradeableCondition. It only modifies cluster version.
func (optr *Operator) upgradeableSync(ctx context.Context, key string) error {
	startTime := time.Now()
	klog.V(4).Infof("Started syncing upgradeable %q (%v)", key, startTime)
	defer func() {
		klog.V(4).Infof("Finished syncing upgradeable %q (%v)", key, time.Since(startTime))
	}()

	config, err := optr.cvLister.Get(optr.name)
	if apierrors.IsNotFound(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if errs := validation.ValidateClusterVersion(config); len(errs) > 0 {
		return nil
	}

	return optr.syncUpgradeable(config)
}

// isOlderThanLastUpdate returns true if the cluster version is older than
// the last update we saw.
func (optr *Operator) isOlderThanLastUpdate(config *configv1.ClusterVersion) bool {
	i, err := strconv.ParseInt(config.ResourceVersion, 10, 64)
	if err != nil {
		return false
	}
	optr.lastAtLock.Lock()
	defer optr.lastAtLock.Unlock()
	return i < optr.lastResourceVersion
}

// rememberLastUpdate records the most recent resource version we
// have seen from the server for cluster versions.
func (optr *Operator) rememberLastUpdate(config *configv1.ClusterVersion) {
	if config == nil {
		return
	}
	i, err := strconv.ParseInt(config.ResourceVersion, 10, 64)
	if err != nil {
		return
	}
	optr.lastAtLock.Lock()
	defer optr.lastAtLock.Unlock()
	optr.lastResourceVersion = i
}

func (optr *Operator) getOrCreateClusterVersion(ctx context.Context, enableDefault bool) (*configv1.ClusterVersion, bool, error) {
	obj, err := optr.cvLister.Get(optr.name)
	if err == nil {
		// if we are waiting to see a newer cached version, just exit
		if optr.isOlderThanLastUpdate(obj) {
			return nil, true, nil
		}
		return obj, false, nil
	}

	if !apierrors.IsNotFound(err) {
		return nil, false, err
	}

	if !enableDefault {
		return nil, false, nil
	}

	var upstream configv1.URL
	if len(optr.defaultUpstreamServer) > 0 {
		u := configv1.URL(optr.defaultUpstreamServer)
		upstream = u
	}
	id, _ := uuid.NewRandom()

	// XXX: generate ClusterVersion from options calculated above.
	config := &configv1.ClusterVersion{
		ObjectMeta: metav1.ObjectMeta{
			Name: optr.name,
		},
		Spec: configv1.ClusterVersionSpec{
			Upstream:  upstream,
			Channel:   "fast",
			ClusterID: configv1.ClusterID(id.String()),
		},
	}

	actual, _, err := resourceapply.ApplyClusterVersionFromCache(ctx, optr.cvLister, optr.client.ConfigV1(), config)
	if apierrors.IsAlreadyExists(err) {
		return nil, true, nil
	}
	return actual, true, err
}

// versionString returns a string describing the desired version.
func versionString(update configv1.Release) string {
	if len(update.Version) > 0 {
		return update.Version
	}
	if len(update.Image) > 0 {
		return update.Image
	}
	return "<unknown>"
}

// currentVersion returns an update object describing the current
// known cluster version.  Values from the upstream Cincinnati service
// are used as fallbacks for any properties not defined in the release
// image itself.
func (optr *Operator) currentVersion() configv1.Release {
	return optr.mergeReleaseMetadata(optr.release)
}

// mergeReleaseMetadata returns a deep copy of the input release.
// Values from any matching availableUpdates release are used as
// fallbacks for any properties not defined in the input release.
func (optr *Operator) mergeReleaseMetadata(release configv1.Release) configv1.Release {
	merged := *release.DeepCopy()

	if merged.Version == "" || len(merged.URL) == 0 || merged.Channels == nil {
		// only fill in missing values from availableUpdates, to avoid clobbering data from payload.LoadUpdate.
		availableUpdates := optr.getAvailableUpdates()
		if availableUpdates != nil {
			var update *configv1.Release
			if equalDigest(merged.Image, availableUpdates.Current.Image) {
				update = &availableUpdates.Current
			} else {
				for _, u := range availableUpdates.Updates {
					if equalDigest(u.Image, merged.Image) {
						update = &u
						break
					}
				}
			}
			if update != nil {
				if merged.Version == "" {
					merged.Version = update.Version
				}
				if len(merged.URL) == 0 {
					merged.URL = update.URL
				}
				if merged.Channels == nil {
					merged.Channels = append(update.Channels[:0:0], update.Channels...) // copy
				}
			}
		}
	}

	return merged
}

// SetSyncWorkerForTesting updates the sync worker for whitebox testing.
func (optr *Operator) SetSyncWorkerForTesting(worker ConfigSyncWorker) {
	optr.configSync = worker
}

// wraps configlistersv1.ClusterOperatorLister until it learns about context.
type dummyContextOperatorGetter struct {
	wrapped configlistersv1.ClusterOperatorLister
}

func (d *dummyContextOperatorGetter) Get(ctx context.Context, name string) (*configv1.ClusterOperator, error) {
	return d.wrapped.Get(name)
}

// resourceBuilder provides the default builder implementation for the operator.
// It is abstracted for testing.
type resourceBuilder struct {
	config      *rest.Config
	burstConfig *rest.Config
	modifier    resourcebuilder.MetaV1ObjectModifierFunc

	clusterOperators cvointernal.ClusterOperatorsGetter
}

// NewResourceBuilder creates the default resource builder implementation.
func NewResourceBuilder(config, burstConfig *rest.Config, clusterOperators cvointernal.ClusterOperatorsGetter) payload.ResourceBuilder {
	return &resourceBuilder{
		config:           config,
		burstConfig:      burstConfig,
		clusterOperators: clusterOperators,
	}
}

func (b *resourceBuilder) builderFor(m *manifest.Manifest, state payload.State) (resourcebuilder.Interface, error) {
	config := b.config
	if state == payload.InitializingPayload {
		config = b.burstConfig
	}

	if b.clusterOperators != nil && m.GVK == configv1.SchemeGroupVersion.WithKind("ClusterOperator") {
		client, err := clientset.NewForConfig(config)
		if err != nil {
			return nil, err
		}
		return cvointernal.NewClusterOperatorBuilder(b.clusterOperators, client.ConfigV1().ClusterOperators(), *m), nil
	}
	if resourcebuilder.Mapper.Exists(m.GVK) {
		return resourcebuilder.New(resourcebuilder.Mapper, config, *m)
	}
	client, err := dynamicclient.New(config, m.GVK, m.Obj.GetNamespace())
	if err != nil {
		return nil, err
	}
	return cvointernal.NewGenericBuilder(client, *m)
}

func (b *resourceBuilder) Apply(ctx context.Context, m *manifest.Manifest, state payload.State) error {
	builder, err := b.builderFor(m, state)
	if err != nil {
		return err
	}
	if b.modifier != nil {
		builder = builder.WithModifier(b.modifier)
	}
	return builder.WithMode(stateToMode(state)).Do(ctx)
}

func stateToMode(state payload.State) resourcebuilder.Mode {
	switch state {
	case payload.InitializingPayload:
		return resourcebuilder.InitializingMode
	case payload.UpdatingPayload:
		return resourcebuilder.UpdatingMode
	case payload.ReconcilingPayload:
		return resourcebuilder.ReconcilingMode
	case payload.PrecreatingPayload:
		return resourcebuilder.PrecreatingMode
	default:
		panic(fmt.Sprintf("unexpected payload state %d", int(state)))
	}
}

func hasNeverReachedLevel(cv *configv1.ClusterVersion) bool {
	for _, version := range cv.Status.History {
		if version.State == configv1.CompletedUpdate {
			return false
		}
	}
	// TODO: check the payload, just in case
	return true
}

func hasReachedLevel(cv *configv1.ClusterVersion, desired configv1.Update) bool {
	if len(cv.Status.History) == 0 {
		return false
	}
	if cv.Status.History[0].State != configv1.CompletedUpdate {
		return false
	}
	return desired.Image == cv.Status.History[0].Image
}

func (optr *Operator) defaultPreconditionChecks() precondition.List {
	return []precondition.Precondition{
		preconditioncv.NewUpgradeable(optr.cvLister),
		preconditioncv.NewRecentEtcdBackup(optr.cvLister, optr.coLister),
	}
}

// HTTPClient provides a method for generating an HTTP client
// with the proxy and trust settings, if set in the cluster.
func (optr *Operator) HTTPClient() (*http.Client, error) {
	proxyURL, tlsConfig, err := optr.getTransportOpts()
	if err != nil {
		return nil, err
	}
	transportOption := &http.Transport{
		Proxy:           http.ProxyURL(proxyURL),
		TLSClientConfig: tlsConfig,
	}
	transportConfig := &transport.Config{Transport: transportOption}
	transport, err := transport.New(transportConfig)
	if err != nil {
		return nil, err
	}
	return &http.Client{
		Transport: transport,
	}, nil
}

// getTransportOpts retrieves the URL of the cluster proxy and the CA
// trust, if they exist.
func (optr *Operator) getTransportOpts() (*url.URL, *tls.Config, error) {
	proxyURL, err := optr.getHTTPSProxyURL()
	if err != nil {
		return nil, nil, err
	}

	var tlsConfig *tls.Config
	tlsConfig, err = optr.getTLSConfig()
	if err != nil {
		return nil, nil, err
	}
	return proxyURL, tlsConfig, nil
}

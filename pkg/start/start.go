// package start initializes and launches the core cluster version operator
// loops.
package start

import (
	"context"
	"fmt"
	"math/rand"
	"net/url"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/google/uuid"
	v1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/fields"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/apimachinery/pkg/util/sets"
	coreinformers "k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/scheme"
	coreclientsetv1 "k8s.io/client-go/kubernetes/typed/core/v1"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/tools/leaderelection"
	"k8s.io/client-go/tools/leaderelection/resourcelock"
	"k8s.io/client-go/tools/record"
	"k8s.io/client-go/util/flowcontrol"
	"k8s.io/klog/v2"

	configv1 "github.com/openshift/api/config/v1"
	clientset "github.com/openshift/client-go/config/clientset/versioned"
	configinformers "github.com/openshift/client-go/config/informers/externalversions"
	operatorclientset "github.com/openshift/client-go/operator/clientset/versioned"
	operatorinformers "github.com/openshift/client-go/operator/informers/externalversions"
	"github.com/openshift/library-go/pkg/config/clusterstatus"
	libgoleaderelection "github.com/openshift/library-go/pkg/config/leaderelection"

	"github.com/openshift/cluster-version-operator/pkg/autoupdate"
	"github.com/openshift/cluster-version-operator/pkg/clusterconditions"
	"github.com/openshift/cluster-version-operator/pkg/cvo"
	"github.com/openshift/cluster-version-operator/pkg/cvo/configuration"
	"github.com/openshift/cluster-version-operator/pkg/featuregates"
	"github.com/openshift/cluster-version-operator/pkg/internal"
	"github.com/openshift/cluster-version-operator/pkg/payload"
)

const (
	defaultComponentName      = "version"
	defaultComponentNamespace = "openshift-cluster-version"

	minResyncPeriod = 2 * time.Minute
)

// Options are the valid inputs to starting the CVO.
type Options struct {
	ReleaseImage        string
	ServingCertFile     string
	ServingKeyFile      string
	ServingClientCAFile string

	Kubeconfig string
	NodeName   string
	ListenAddr string

	EnableAutoUpdate bool

	PrometheusURLString       string
	PromQLTarget              clusterconditions.PromQLTarget
	InjectClusterIdIntoPromQL bool

	// UpdateService configures the preferred update service.  If set,
	// this option overrides any upstream value configured in ClusterVersion
	// spec.
	UpdateService string

	// Exclude is used to determine whether to exclude
	// certain manifests based on an annotation:
	// exclude.release.openshift.io/<identifier>=true
	Exclude string

	ClusterProfile string

	HyperShift bool

	// AlwaysEnableCapabilities is a list of cluster version capabilities
	// which will always be implicitly enabled.
	AlwaysEnableCapabilities []string

	// for testing only
	Name            string
	Namespace       string
	PayloadOverride string
	ResyncInterval  time.Duration

	leaderElection configv1.LeaderElection
}

type asyncResult struct {
	name  string
	error error
}

func defaultEnv(name, defaultValue string) string {
	env, ok := os.LookupEnv(name)
	if !ok {
		return defaultValue
	}
	return env
}

// NewOptions creates the default options for the CVO and loads any environment
// variable overrides.
func NewOptions() *Options {
	defaultPromQLTarget := clusterconditions.DefaultPromQLTarget()
	return &Options{
		ListenAddr:          "0.0.0.0:9099",
		NodeName:            os.Getenv("NODE_NAME"),
		PrometheusURLString: defaultPromQLTarget.URL.String(),
		PromQLTarget:        defaultPromQLTarget,

		// exposed only for testing
		Namespace:       defaultEnv("CVO_NAMESPACE", defaultComponentNamespace),
		Name:            defaultEnv("CVO_NAME", defaultComponentName),
		PayloadOverride: os.Getenv("PAYLOAD_OVERRIDE"),
		ResyncInterval:  minResyncPeriod,
		Exclude:         os.Getenv("EXCLUDE_MANIFESTS"),
		ClusterProfile:  defaultEnv("CLUSTER_PROFILE", payload.DefaultClusterProfile),
	}
}

func (o *Options) ValidateAndComplete() error {
	if o.NodeName == "" {
		return fmt.Errorf("node-name is required")
	}
	if o.ReleaseImage == "" {
		return fmt.Errorf("missing --release-image flag, it is required")
	}
	if o.ListenAddr != "" && o.ServingCertFile == "" {
		return fmt.Errorf("--listen was not set empty, so --serving-cert-file must be set")
	}
	if o.ListenAddr != "" && o.ServingKeyFile == "" {
		return fmt.Errorf("--listen was not set empty, so --serving-key-file must be set")
	}
	if o.ListenAddr == "" && o.ServingClientCAFile != "" {
		return fmt.Errorf("--listen was set empty, so --serving-client-certificate-authorities-file must not be set")
	}
	if o.PrometheusURLString == "" {
		return fmt.Errorf("missing --metrics-url flag, it is required")
	}
	if !o.PromQLTarget.UseDNS &&
		(o.PromQLTarget.KubeSvc.Namespace == "" || o.PromQLTarget.KubeSvc.Name == "") {
		return fmt.Errorf("--use-dns-for-services is disabled, so --metrics-service and --metrics-namespace must be set")
	}

	if parsed, err := url.Parse(o.PrometheusURLString); err != nil {
		return fmt.Errorf("error parsing promql url: %v", err)
	} else {
		o.PromQLTarget.URL = parsed
	}

	// Inject the cluster ID into PromQL queries in HyperShift
	o.InjectClusterIdIntoPromQL = o.HyperShift

	if err := validateCapabilities(o.AlwaysEnableCapabilities); err != nil {
		return fmt.Errorf("--always-enable-capabilities: %w", err)
	}

	return nil
}

func (o *Options) Run(ctx context.Context) error {
	if len(o.PayloadOverride) > 0 {
		klog.Warningf("Using an override payload directory for testing only: %s", o.PayloadOverride)
	}
	if len(o.Exclude) > 0 {
		klog.Infof("Excluding manifests for %q", o.Exclude)
	}

	// initialize the core objects
	cb, err := newClientBuilder(o.Kubeconfig)
	if err != nil {
		return fmt.Errorf("error creating clients: %v", err)
	}

	lock, err := createResourceLock(cb, o.Namespace, o.Name)
	if err != nil {
		return err
	}

	clusterVersionConfigInformerFactory, configInformerFactory := o.prepareConfigInformerFactories(cb)
	startingFeatureSet, startingCvoGates, err := o.processInitialFeatureGate(ctx, configInformerFactory)
	if err != nil {
		return fmt.Errorf("error processing feature gates: %w", err)
	}

	// initialize the controllers and attempt to load the payload information
	controllerCtx, err := o.NewControllerContext(cb, startingFeatureSet, startingCvoGates, clusterVersionConfigInformerFactory, configInformerFactory)
	if err != nil {
		return err
	}
	o.leaderElection = getLeaderElectionConfig(ctx, cb.RestConfig(defaultQPS))
	o.run(ctx, controllerCtx, lock, cb.RestConfig(defaultQPS), cb.RestConfig(highQPS))
	return nil
}

func (o *Options) prepareConfigInformerFactories(cb *ClientBuilder) (configinformers.SharedInformerFactory, configinformers.SharedInformerFactory) {
	client := cb.ClientOrDie("shared-informer")
	filterByName := func(opts *metav1.ListOptions) {
		opts.FieldSelector = fields.OneTermEqualSelector("metadata.name", o.Name).String()
	}
	clusterVersionConfigInformerFactory := configinformers.NewSharedInformerFactoryWithOptions(client, resyncPeriod(o.ResyncInterval), configinformers.WithTweakListOptions(filterByName))
	configInformerFactory := configinformers.NewSharedInformerFactory(client, resyncPeriod(o.ResyncInterval))

	return clusterVersionConfigInformerFactory, configInformerFactory
}

// getOpenShiftVersion peeks at the local release metadata to determine the version of OpenShift this CVO belongs to.
// This assumes the CVO is executing in a container from the payload image. This does not and should not fully load
// whole payload content, that is only loaded later once leader lease is acquired. Here we should only read as little
// data as possible to determine the version so we can establish enabled feature gate checker for all following code.
func (o *Options) getOpenShiftVersion() string {
	payloadRoot := payload.DefaultRootPath
	if o.PayloadOverride != "" {
		payloadRoot = payload.RootPath(o.PayloadOverride)
	}

	// We cannot refuse to start CVO if for some reason we cannot determine the OpenShift version on startup from the local
	// release metadata. The only consequence is we fail to determine enabled/disabled feature gates and will have to use
	// some defaults.
	releaseMetadata, err := payloadRoot.LoadReleaseMetadata()
	if err != nil {
		klog.Warningf("Failed to read release metadata to determine OpenShift version for this CVO (will use placeholder version %q): %v", featuregates.StubOpenShiftVersion, err)
		return featuregates.StubOpenShiftVersion
	}

	if releaseMetadata.Version == "" {
		klog.Warningf("Version missing from release metadata, cannot determine OpenShift version for this CVO (will use placeholder version %q)", featuregates.StubOpenShiftVersion)
		return featuregates.StubOpenShiftVersion
	}

	klog.Infof("Determined OpenShift version for this CVO: %q", releaseMetadata.Version)
	return releaseMetadata.Version
}

func (o *Options) processInitialFeatureGate(ctx context.Context, configInformerFactory configinformers.SharedInformerFactory) (configv1.FeatureSet, featuregates.CvoGates, error) {
	var startingFeatureSet configv1.FeatureSet
	var cvoGates featuregates.CvoGates

	featureGates := configInformerFactory.Config().V1().FeatureGates().Lister()
	configInformerFactory.Start(ctx.Done())

	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	for key, synced := range configInformerFactory.WaitForCacheSync(ctx.Done()) {
		if !synced {
			return startingFeatureSet, cvoGates, fmt.Errorf("failed to sync %s informer cache: %w", key.String(), ctx.Err())
		}
	}

	cvoOpenShiftVersion := o.getOpenShiftVersion()
	cvoGates = featuregates.DefaultCvoGates(cvoOpenShiftVersion)

	var clusterFeatureGate *configv1.FeatureGate

	gate, err := featureGates.Get("cluster")
	switch {
	case apierrors.IsNotFound(err):
		// if we have no featuregates, then the cluster is using the default featureset, which is "".
		// This excludes everything that could possibly depend on a different feature set.
		startingFeatureSet = ""
		klog.Infof("FeatureGate not found in cluster, will assume default feature set %q at startup", startingFeatureSet)
	case err != nil:
		// This should not happen because featureGates is backed by the informer cache which successfully synced earlier
		klog.Errorf("Failed to get FeatureGate from cluster: %v", err)
		return startingFeatureSet, cvoGates, fmt.Errorf("failed to get FeatureGate from informer cache: %w", err)
	default:
		clusterFeatureGate = gate
		startingFeatureSet = gate.Spec.FeatureSet
		cvoGates = featuregates.CvoGatesFromFeatureGate(clusterFeatureGate, cvoOpenShiftVersion)
		klog.Infof("FeatureGate found in cluster, using its feature set %q at startup", startingFeatureSet)
	}

	if cvoGates.UnknownVersion() {
		klog.Warningf("CVO features for version %s could not be detected from FeatureGate; will use defaults plus special UnknownVersion feature gate", cvoOpenShiftVersion)
	}
	klog.Infof("CVO features for version %s enabled at startup: %+v", cvoOpenShiftVersion, cvoGates)

	return startingFeatureSet, cvoGates, nil
}

// run launches a number of goroutines to handle manifest application,
// metrics serving, etc.  It continues operating until ctx.Done(),
// and then attempts a clean shutdown limited by an internal context
// with a two-minute cap.  It returns after it successfully collects all
// launched goroutines.
func (o *Options) run(ctx context.Context, controllerCtx *Context, lock resourcelock.Interface, restConfig *rest.Config, burstRestConfig *rest.Config) {
	runContext, runCancel := context.WithCancel(ctx) // so we can cancel internally on errors or TERM
	defer runCancel()
	shutdownContext, shutdownCancel := context.WithCancel(context.Background()) // extends beyond ctx
	defer shutdownCancel()
	postMainContext, postMainCancel := context.WithCancel(context.Background()) // extends beyond ctx
	defer postMainCancel()
	launchedMain := false

	ch := make(chan os.Signal, 1)
	defer func() { signal.Stop(ch) }()
	signal.Notify(ch, os.Interrupt, syscall.SIGTERM)
	go func() {
		defer utilruntime.HandleCrash()
		sig := <-ch
		klog.Infof("Shutting down due to %s", sig)
		runCancel()
		sig = <-ch
		klog.Fatalf("Received shutdown signal twice, exiting: %s", sig)
	}()

	resultChannel := make(chan asyncResult, 1)
	resultChannelCount := 0

	informersDone := postMainContext.Done()
	// FIXME: would be nice if there was a way to collect these.
	controllerCtx.ClusterVersionInformerFactory.Start(informersDone)
	controllerCtx.OpenshiftConfigInformerFactory.Start(informersDone)
	controllerCtx.OpenshiftConfigManagedInformerFactory.Start(informersDone)
	controllerCtx.ConfigInformerFactory.Start(informersDone)
	controllerCtx.OperatorInformerFactory.Start(informersDone)

	allSynced := controllerCtx.ClusterVersionInformerFactory.WaitForCacheSync(informersDone)
	for _, synced := range allSynced {
		if !synced {
			klog.Fatalf("Caches never synchronized: %v", postMainContext.Err())
		}
	}

	resultChannelCount++
	go func() {
		defer utilruntime.HandleCrash()
		var firstError error
		leaderelection.RunOrDie(postMainContext, leaderelection.LeaderElectionConfig{
			Lock:            lock,
			ReleaseOnCancel: true,
			LeaseDuration:   o.leaderElection.LeaseDuration.Duration,
			RenewDeadline:   o.leaderElection.RenewDeadline.Duration,
			RetryPeriod:     o.leaderElection.RetryPeriod.Duration,
			Callbacks: leaderelection.LeaderCallbacks{
				OnStartedLeading: func(_ context.Context) { // no need for this passed-through postMainContext, because goroutines we launch inside will use runContext
					launchedMain = true
					if o.ListenAddr != "" {
						resultChannelCount++
						go func() {
							defer utilruntime.HandleCrash()
							err := cvo.RunMetrics(postMainContext, shutdownContext, o.ListenAddr, o.ServingCertFile, o.ServingKeyFile, o.ServingClientCAFile, restConfig)
							resultChannel <- asyncResult{name: "metrics server", error: err}
						}()
					}
					if err := controllerCtx.CVO.InitializeFromPayload(runContext, restConfig, burstRestConfig); err != nil {
						if firstError == nil {
							firstError = err
						}
						klog.Infof("Failed to initialize from payload; shutting down: %v", err)
						resultChannel <- asyncResult{name: "payload initialization", error: firstError}
						return
					}

					resultChannelCount++
					go func() {
						defer utilruntime.HandleCrash()
						err := controllerCtx.CVO.Run(runContext, shutdownContext)
						resultChannel <- asyncResult{name: "main operator", error: err}
					}()

					resultChannelCount++
					go func() {
						defer utilruntime.HandleCrash()
						err := controllerCtx.StopOnFeatureGateChange.Run(runContext, runCancel)
						resultChannel <- asyncResult{name: "stop-on-techpreview-change controller", error: err}
					}()

					if controllerCtx.AutoUpdate != nil {
						resultChannelCount++
						go func() {
							defer utilruntime.HandleCrash()
							err := controllerCtx.AutoUpdate.Run(runContext, 2)
							resultChannel <- asyncResult{name: "auto-update controller", error: err}
						}()
					}
				},
				OnStoppedLeading: func() {
					klog.Info("Stopped leading; shutting down.")
					runCancel()
				},
			},
		})
		resultChannel <- asyncResult{name: "leader controller", error: firstError}
	}()

	var shutdownTimer *time.Timer
	for resultChannelCount > 0 {
		klog.Infof("Waiting on %d outstanding goroutines.", resultChannelCount)
		if shutdownTimer == nil { // running
			select {
			case <-runContext.Done():
				klog.Info("Run context completed; beginning two-minute graceful shutdown period.")
				shutdownTimer = time.NewTimer(2 * time.Minute)
				if !launchedMain { // no need to give post-main extra time if main never ran
					postMainCancel()
				}
			case result := <-resultChannel:
				resultChannelCount--
				if result.error == nil {
					klog.Infof("Collected %s goroutine.", result.name)
				} else {
					klog.Errorf("Collected %s goroutine: %v", result.name, result.error)
					runCancel() // this will cause shutdownTimer initialization in the next loop
				}
				if result.name == "main operator" {
					postMainCancel()
				}
			}
		} else { // shutting down
			select {
			case <-shutdownTimer.C: // never triggers after the channel is stopped, although it would not matter much if it did because subsequent cancel calls do nothing.
				postMainCancel()
				shutdownCancel()
				shutdownTimer.Stop()
			case result := <-resultChannel:
				resultChannelCount--
				if result.error == nil {
					klog.Infof("Collected %s goroutine.", result.name)
				} else {
					klog.Errorf("Collected %s goroutine: %v", result.name, result.error)
				}
				if result.name == "main operator" {
					postMainCancel()
				}
			}
		}
	}
	klog.Info("Finished collecting operator goroutines.")
}

// createResourceLock initializes the lock.
func createResourceLock(cb *ClientBuilder, namespace, name string) (resourcelock.Interface, error) {
	client := cb.KubeClientOrDie("leader-election")
	eventBroadcaster := record.NewBroadcaster()
	eventBroadcaster.StartLogging(klog.Infof)
	eventBroadcaster.StartRecordingToSink(&coreclientsetv1.EventSinkImpl{Interface: client.CoreV1().Events(namespace)})

	id, err := os.Hostname()
	if err != nil {
		return nil, fmt.Errorf("error creating lock: %v", err)
	}

	uuid, err := uuid.NewRandom()
	if err != nil {
		return nil, fmt.Errorf("Failed to generate UUID: %v", err)
	}

	// add a uniquifier so that two processes on the same host don't accidentally both become active
	id = id + "_" + uuid.String()

	return resourcelock.New(resourcelock.LeasesResourceLock, namespace, name, client.CoreV1(), client.CoordinationV1(), resourcelock.ResourceLockConfig{
		Identity:      id,
		EventRecorder: eventBroadcaster.NewRecorder(scheme.Scheme, v1.EventSource{Component: namespace}),
	})
}

func resyncPeriod(minResyncPeriod time.Duration) time.Duration {
	factor := rand.Float64() + 1
	return time.Duration(float64(minResyncPeriod.Nanoseconds()) * factor)
}

// ClientBuilder simplifies returning Kubernetes client and client configs with
// an appropriate user agent.
type ClientBuilder struct {
	config *rest.Config
}

// RestConfig returns a copy of the ClientBuilder's rest.Config with any overrides
// from the provided configFns applied.
func (cb *ClientBuilder) RestConfig(configFns ...func(*rest.Config)) *rest.Config {
	c := rest.CopyConfig(cb.config)
	for _, fn := range configFns {
		fn(c)
	}
	return c
}

func (cb *ClientBuilder) ClientOrDie(name string, configFns ...func(*rest.Config)) clientset.Interface {
	return clientset.NewForConfigOrDie(rest.AddUserAgent(cb.RestConfig(configFns...), name))
}

func (cb *ClientBuilder) KubeClientOrDie(name string, configFns ...func(*rest.Config)) kubernetes.Interface {
	return kubernetes.NewForConfigOrDie(rest.AddUserAgent(cb.RestConfig(configFns...), name))
}

func (cb *ClientBuilder) OperatorClientOrDie(name string, configFns ...func(*rest.Config)) operatorclientset.Interface {
	return operatorclientset.NewForConfigOrDie(rest.AddUserAgent(cb.RestConfig(configFns...), name))
}

func newClientBuilder(kubeconfig string) (*ClientBuilder, error) {
	clientCfg := clientcmd.NewDefaultClientConfigLoadingRules()
	clientCfg.ExplicitPath = kubeconfig

	kcfg := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(clientCfg, &clientcmd.ConfigOverrides{})
	config, err := kcfg.ClientConfig()
	if err != nil {
		return nil, err
	}

	return &ClientBuilder{
		config: config,
	}, nil
}

func defaultQPS(config *rest.Config) {
	config.RateLimiter = flowcontrol.NewTokenBucketRateLimiter(20, 40)
}

func highQPS(config *rest.Config) {
	config.RateLimiter = flowcontrol.NewTokenBucketRateLimiter(40, 80)
}

func useProtobuf(config *rest.Config) {
	config.AcceptContentTypes = "application/vnd.kubernetes.protobuf,application/json"
	config.ContentType = "application/vnd.kubernetes.protobuf"
}

func getLeaderElectionConfig(ctx context.Context, restcfg *rest.Config) configv1.LeaderElection {

	// Defaults follow conventions
	// https://github.com/openshift/enhancements/pull/832/files#diff-2e28754e69aa417e5b6d89e99e42f05bfb6330800fa823753383db1d170fbc2fR183
	// see rhbz#1986477 for more detail
	defaultLeaderElection := libgoleaderelection.LeaderElectionDefaulting(
		configv1.LeaderElection{},
		"", "",
	)

	if infra, err := clusterstatus.GetClusterInfraStatus(ctx, restcfg); err == nil && infra != nil {
		if infra.ControlPlaneTopology == configv1.SingleReplicaTopologyMode {
			return libgoleaderelection.LeaderElectionSNOConfig(defaultLeaderElection)
		}
	} else {
		klog.Warningf("unable to get cluster infrastructure status, using HA cluster values for leader election: %v", err)
	}

	return defaultLeaderElection
}

// Context holds the controllers for this operator and exposes a unified start command.
type Context struct {
	CVO                     *cvo.Operator
	AutoUpdate              *autoupdate.Controller
	StopOnFeatureGateChange *featuregates.ChangeStopper

	// ClusterVersionInformerFactory should be used to get informers / listers for code that works with ClusterVersion resource
	// singleton in the cluster.
	ClusterVersionInformerFactory configinformers.SharedInformerFactory
	// ConfigInformerFactory should be used to get informers / listers for code that works with resources from the
	// config.openshift.io group, _except_ the ClusterVersion resource singleton.
	ConfigInformerFactory configinformers.SharedInformerFactory
	// OpenshiftConfigManagedInformerFactory should be used to get informers / listers for code that works with core k8s
	// resources in the openshift-config namespace.
	OpenshiftConfigInformerFactory coreinformers.SharedInformerFactory
	// OpenshiftConfigManagedInformerFactory should be used to get informers / listers for code that works with core k8s
	// resources in the openshift-config-managed namespace.
	OpenshiftConfigManagedInformerFactory coreinformers.SharedInformerFactory
	// OperatorInformerFactory should be used to get informers / listers for code that works with resources from the
	// operator.openshift.io group
	OperatorInformerFactory operatorinformers.SharedInformerFactory
}

// NewControllerContext initializes the default Context for the current Options. It does
// not start any background processes.
func (o *Options) NewControllerContext(
	cb *ClientBuilder,
	startingFeatureSet configv1.FeatureSet,
	startingCvoGates featuregates.CvoGates,
	clusterVersionConfigInformerFactory,
	configInformerFactory configinformers.SharedInformerFactory,
) (*Context, error) {
	kubeClient := cb.KubeClientOrDie(internal.ConfigNamespace, useProtobuf)
	openshiftConfigInformerFactory := coreinformers.NewSharedInformerFactoryWithOptions(kubeClient, resyncPeriod(o.ResyncInterval), coreinformers.WithNamespace(internal.ConfigNamespace))
	openshiftConfigManagedInformerFactory := coreinformers.NewSharedInformerFactoryWithOptions(kubeClient, resyncPeriod(o.ResyncInterval), coreinformers.WithNamespace(internal.ConfigManagedNamespace))

	operatorClient := cb.OperatorClientOrDie("operator-client")
	filterByName := func(opts *metav1.ListOptions) {
		opts.FieldSelector = fields.OneTermEqualSelector("metadata.name", configuration.ClusterVersionOperatorConfigurationName).String()
	}
	operatorInformerFactory := operatorinformers.NewSharedInformerFactoryWithOptions(operatorClient, o.ResyncInterval, operatorinformers.WithTweakListOptions(filterByName))

	coInformer := configInformerFactory.Config().V1().ClusterOperators()

	cvoKubeClient := cb.KubeClientOrDie(o.Namespace, useProtobuf)
	o.PromQLTarget.KubeClient = cvoKubeClient

	cvo, err := cvo.New(
		o.NodeName,
		o.Namespace, o.Name,
		o.ReleaseImage,
		o.PayloadOverride,
		resyncPeriod(o.ResyncInterval),
		clusterVersionConfigInformerFactory.Config().V1().ClusterVersions(),
		coInformer,
		openshiftConfigInformerFactory.Core().V1().ConfigMaps(),
		openshiftConfigManagedInformerFactory.Core().V1().ConfigMaps(),
		configInformerFactory.Config().V1().Proxies(),
		operatorInformerFactory,
		cb.ClientOrDie(o.Namespace),
		cvoKubeClient,
		operatorClient,
		o.Exclude,
		o.ClusterProfile,
		o.HyperShift,
		o.PromQLTarget,
		o.InjectClusterIdIntoPromQL,
		o.UpdateService,
		stringsToCapabilities(o.AlwaysEnableCapabilities),
		startingFeatureSet,
		startingCvoGates,
	)
	if err != nil {
		return nil, err
	}

	featureChangeStopper, err := featuregates.NewChangeStopper(configInformerFactory.Config().V1().FeatureGates(), startingFeatureSet, startingCvoGates)
	if err != nil {
		return nil, err
	}

	ctx := &Context{
		ClusterVersionInformerFactory:         clusterVersionConfigInformerFactory,
		ConfigInformerFactory:                 configInformerFactory,
		OpenshiftConfigInformerFactory:        openshiftConfigInformerFactory,
		OpenshiftConfigManagedInformerFactory: openshiftConfigManagedInformerFactory,
		OperatorInformerFactory:               operatorInformerFactory,
		CVO:                                   cvo,
		StopOnFeatureGateChange:               featureChangeStopper,
	}

	if o.EnableAutoUpdate {
		ctx.AutoUpdate, err = autoupdate.New(
			o.Namespace, o.Name,
			clusterVersionConfigInformerFactory.Config().V1().ClusterVersions(),
			configInformerFactory.Config().V1().ClusterOperators(),
			cb.ClientOrDie(o.Namespace),
			cb.KubeClientOrDie(o.Namespace),
		)
		if err != nil {
			return nil, err
		}
	}
	if o.ListenAddr != "" {
		if err := ctx.CVO.RegisterMetrics(coInformer.Informer()); err != nil {
			return nil, err
		}
	}
	return ctx, nil
}

func stringsToCapabilities(names []string) []configv1.ClusterVersionCapability {
	caps := make([]configv1.ClusterVersionCapability, len(names))
	for i, c := range names {
		caps[i] = configv1.ClusterVersionCapability(c)
	}
	return caps
}

func validateCapabilities(caps []string) error {
	unknown := sets.New(caps...)
	for _, kc := range configv1.KnownClusterVersionCapabilities {
		unknown.Delete(string(kc))
	}

	if len(unknown) > 0 {
		return fmt.Errorf("unknown capabilities: %s", sets.List(unknown))
	}
	return nil
}

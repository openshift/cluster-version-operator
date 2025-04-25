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
	configlistersv1 "github.com/openshift/client-go/config/listers/config/v1"
	v1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/fields"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/informers"
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
	"github.com/openshift/client-go/config/informers/externalversions"
	operatorclientset "github.com/openshift/client-go/operator/clientset/versioned"
	operatorexternalversions "github.com/openshift/client-go/operator/informers/externalversions"
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
	ReleaseImage    string
	ServingCertFile string
	ServingKeyFile  string

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

	// AlwaysEnableCapabilities is user-provided list of cluster version capabilities to be always be implicitly enabled
	AlwaysEnableCapabilities []string
	// alwaysEnableCapabilities is the parsed list of cluster version capabilities to be always be implicitly enabled,
	// guaranteed to contain only known capabilities.
	alwaysEnableCapabilities []configv1.ClusterVersionCapability

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

func (o *Options) Run(ctx context.Context) error {
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
	if o.PrometheusURLString == "" {
		return fmt.Errorf("missing --metrics-url flag, it is required")
	}
	if !o.PromQLTarget.UseDNS &&
		(o.PromQLTarget.KubeSvc.Namespace == "" || o.PromQLTarget.KubeSvc.Name == "") {
		return fmt.Errorf("--use-dns-for-services is disabled, so --metrics-service and --metrics-namespace must be set")
	}
	if len(o.PayloadOverride) > 0 {
		klog.Warningf("Using an override payload directory for testing only: %s", o.PayloadOverride)
	}
	if len(o.Exclude) > 0 {
		klog.Infof("Excluding manifests for %q", o.Exclude)
	}
	if err := o.parseAlwaysEnableCapabilities(); err != nil {
		return fmt.Errorf("--always-enable-capability: %w", err)
	}

	// Inject the cluster ID into PromQL queries in HyperShift
	o.InjectClusterIdIntoPromQL = o.HyperShift

	// parse the prometheus url
	var err error
	o.PromQLTarget.URL, err = url.Parse(o.PrometheusURLString)
	if err != nil {
		return fmt.Errorf("error parsing promql url: %v", err)
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

	// initialize the controllers and attempt to load the payload information
	controllerCtx, err := o.NewControllerContext(cb)
	if err != nil {
		return err
	}
	o.leaderElection = getLeaderElectionConfig(ctx, cb.RestConfig(defaultQPS))
	o.run(ctx, controllerCtx, lock, cb.RestConfig(defaultQPS), cb.RestConfig(highQPS))
	return nil
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
	controllerCtx.CVInformerFactory.Start(informersDone)
	controllerCtx.OpenshiftConfigInformerFactory.Start(informersDone)
	controllerCtx.OpenshiftConfigManagedInformerFactory.Start(informersDone)
	controllerCtx.InformerFactory.Start(informersDone)
	controllerCtx.OperatorInformerFactory.Start(informersDone)

	allSynced := controllerCtx.CVInformerFactory.WaitForCacheSync(informersDone)
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
							err := cvo.RunMetrics(postMainContext, shutdownContext, o.ListenAddr, o.ServingCertFile, o.ServingKeyFile)
							resultChannel <- asyncResult{name: "metrics server", error: err}
						}()
					}
					if err := controllerCtx.InitializeFromPayload(runContext, restConfig, burstRestConfig); err != nil {
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

	CVInformerFactory                     externalversions.SharedInformerFactory
	OpenshiftConfigInformerFactory        informers.SharedInformerFactory
	OpenshiftConfigManagedInformerFactory informers.SharedInformerFactory
	InformerFactory                       externalversions.SharedInformerFactory
	OperatorInformerFactory               operatorexternalversions.SharedInformerFactory

	fgLister configlistersv1.FeatureGateLister
}

// NewControllerContext initializes the default Context for the current Options. It does
// not start any background processes.
func (o *Options) NewControllerContext(cb *ClientBuilder) (*Context, error) {
	client := cb.ClientOrDie("shared-informer")
	kubeClient := cb.KubeClientOrDie(internal.ConfigNamespace, useProtobuf)
	operatorClient := cb.OperatorClientOrDie("operator-client")

	cvInformer := externalversions.NewFilteredSharedInformerFactory(client, resyncPeriod(o.ResyncInterval), "", func(opts *metav1.ListOptions) {
		opts.FieldSelector = fmt.Sprintf("metadata.name=%s", o.Name)
	})
	openshiftConfigInformer := informers.NewSharedInformerFactoryWithOptions(kubeClient, resyncPeriod(o.ResyncInterval), informers.WithNamespace(internal.ConfigNamespace))
	openshiftConfigManagedInformer := informers.NewSharedInformerFactoryWithOptions(kubeClient, resyncPeriod(o.ResyncInterval), informers.WithNamespace(internal.ConfigManagedNamespace))
	sharedInformers := externalversions.NewSharedInformerFactory(client, resyncPeriod(o.ResyncInterval))
	operatorInformerFactory := operatorexternalversions.NewSharedInformerFactoryWithOptions(operatorClient, o.ResyncInterval,
		operatorexternalversions.WithTweakListOptions(func(opts *metav1.ListOptions) {
			opts.FieldSelector = fields.OneTermEqualSelector("metadata.name", configuration.ClusterVersionOperatorConfigurationName).String()
		}))

	coInformer := sharedInformers.Config().V1().ClusterOperators()

	cvoKubeClient := cb.KubeClientOrDie(o.Namespace, useProtobuf)
	o.PromQLTarget.KubeClient = cvoKubeClient
	cvo, err := cvo.New(
		o.NodeName,
		o.Namespace, o.Name,
		o.ReleaseImage,
		o.PayloadOverride,
		resyncPeriod(o.ResyncInterval),
		cvInformer.Config().V1().ClusterVersions(),
		coInformer,
		openshiftConfigInformer.Core().V1().ConfigMaps(),
		openshiftConfigManagedInformer.Core().V1().ConfigMaps(),
		sharedInformers.Config().V1().Proxies(),
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
		o.alwaysEnableCapabilities,
	)
	if err != nil {
		return nil, err
	}

	featureChangeStopper, err := featuregates.NewChangeStopper(sharedInformers.Config().V1().FeatureGates())
	if err != nil {
		return nil, err
	}

	ctx := &Context{
		CVInformerFactory:                     cvInformer,
		OpenshiftConfigInformerFactory:        openshiftConfigInformer,
		OpenshiftConfigManagedInformerFactory: openshiftConfigManagedInformer,
		InformerFactory:                       sharedInformers,
		OperatorInformerFactory:               operatorInformerFactory,
		CVO:                                   cvo,
		StopOnFeatureGateChange:               featureChangeStopper,

		fgLister: sharedInformers.Config().V1().FeatureGates().Lister(),
	}

	if o.EnableAutoUpdate {
		ctx.AutoUpdate, err = autoupdate.New(
			o.Namespace, o.Name,
			cvInformer.Config().V1().ClusterVersions(),
			sharedInformers.Config().V1().ClusterOperators(),
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

// InitializeFromPayload initializes the CVO and FeatureGate ChangeStoppers controllers from the payload. It extracts the
// current CVO version from the initial payload and uses it to determine the initial the required featureset and enabled
// feature gates. Both the payload and determined feature information are used to initialize CVO and feature gate
// ChangeStopper controllers.
func (c *Context) InitializeFromPayload(ctx context.Context, restConfig *rest.Config, burstRestConfig *rest.Config) error {
	var startingFeatureSet configv1.FeatureSet
	var clusterFeatureGate *configv1.FeatureGate

	// client-go automatically retries some network blip errors on GETs for 30s by default, and we want to
	// retry the remaining ones ourselves. If we fail longer than that, the operator won't be able to do work
	// anyway. Return the error and crashloop.
	//
	// We implement the timeout with a context because the timeout in PollImmediateWithContext does not behave
	// well when ConditionFunc takes longer time to execute, like here where the GET can be retried by client-go
	var lastError error
	if err := wait.PollUntilContextTimeout(context.Background(), 2*time.Second, 25*time.Second, true, func(ctx context.Context) (bool, error) {
		gate, fgErr := c.fgLister.Get("cluster")
		switch {
		case apierrors.IsNotFound(fgErr):
			// if we have no featuregates, then the cluster is using the default featureset, which is "".
			// This excludes everything that could possibly depend on a different feature set.
			startingFeatureSet = ""
			klog.Infof("FeatureGate not found in cluster, using default feature set %q at startup", startingFeatureSet)
			return true, nil
		case fgErr != nil:
			lastError = fgErr
			klog.Warningf("Failed to get FeatureGate from cluster: %v", fgErr)
			return false, nil
		default:
			clusterFeatureGate = gate
			startingFeatureSet = gate.Spec.FeatureSet
			klog.Infof("FeatureGate found in cluster, using its feature set %q at startup", startingFeatureSet)
			return true, nil
		}
	}); err != nil {
		if lastError != nil {
			return lastError
		}
		return err
	}

	payload, err := c.CVO.LoadInitialPayload(ctx, startingFeatureSet, restConfig)
	if err != nil {
		return err
	}

	var cvoGates featuregates.CvoGates
	if clusterFeatureGate != nil {
		cvoGates = featuregates.CvoGatesFromFeatureGate(clusterFeatureGate, payload.Release.Version)
	} else {
		cvoGates = featuregates.DefaultCvoGates(payload.Release.Version)
	}

	if cvoGates.UnknownVersion() {
		klog.Infof("CVO features for version %s could not be detected from FeatureGate; will use defaults plus special UnknownVersion feature gate", payload.Release.Version)
	}
	klog.Infof("CVO features for version %s enabled at startup: %+v", payload.Release.Version, cvoGates)

	c.StopOnFeatureGateChange.SetStartingFeatures(startingFeatureSet, cvoGates)
	c.CVO.InitializeFromPayload(payload, startingFeatureSet, cvoGates, restConfig, burstRestConfig)

	return nil
}

// parseAlwaysEnableCapabilities parses the string list of capabilities
// into two lists of configv1.ClusterVersionCapability: known and unknown.
func (o *Options) parseAlwaysEnableCapabilities() error {
	var unknownCaps []configv1.ClusterVersionCapability

	for _, c := range o.AlwaysEnableCapabilities {
		known := false
		for _, kc := range configv1.KnownClusterVersionCapabilities {
			if configv1.ClusterVersionCapability(c) == kc {
				o.alwaysEnableCapabilities = append(o.alwaysEnableCapabilities, kc)
				known = true
				break
			}
		}
		if !known {
			unknownCaps = append(unknownCaps, configv1.ClusterVersionCapability(c))
		}
	}

	if len(unknownCaps) > 0 {
		return fmt.Errorf("unknown capabilities: %v", unknownCaps)
	}

	return nil
}

// package start initializes and launches the core cluster version operator
// loops.
package start

import (
	"context"
	"fmt"
	"math/rand"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/google/uuid"
	v1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
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
	externalversions "github.com/openshift/client-go/config/informers/externalversions"
	"github.com/openshift/cluster-version-operator/pkg/autoupdate"
	"github.com/openshift/cluster-version-operator/pkg/cvo"
	"github.com/openshift/cluster-version-operator/pkg/featurechangestopper"
	"github.com/openshift/cluster-version-operator/pkg/internal"
	"github.com/openshift/cluster-version-operator/pkg/payload"
	"github.com/openshift/library-go/pkg/config/clusterstatus"
	libgoleaderelection "github.com/openshift/library-go/pkg/config/leaderelection"
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

	// Exclude is used to determine whether to exclude
	// certain manifests based on an annotation:
	// exclude.release.openshift.io/<identifier>=true
	Exclude string

	ClusterProfile string

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
	return &Options{
		ListenAddr: "0.0.0.0:9099",
		NodeName:   os.Getenv("NODE_NAME"),

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
	// check to see if techpreview should be on or off.  If we cannot read the featuregate for any reason, it is assumed
	// to be off.  If this value changes, the binary will shutdown and expect the pod lifecycle to restart it.
	startingFeatureSet := ""
	gate, err := cb.ClientOrDie("feature-gate-getter").ConfigV1().FeatureGates().Get(ctx, "cluster", metav1.GetOptions{})
	switch {
	case apierrors.IsNotFound(err):
		// if we have no featuregates, then we assume the default featureset, which is "".
		// This excludes everything that could possibly depend on a different feature set.
		startingFeatureSet = ""
	case err != nil:
		klog.Warningf("Error getting featuregate value: %v", err)

	default:
		// otherwise, you're the default
		startingFeatureSet = string(gate.Spec.FeatureSet)
	}

	lock, err := createResourceLock(cb, o.Namespace, o.Name)
	if err != nil {
		return err
	}

	// initialize the controllers and attempt to load the payload information
	controllerCtx := o.NewControllerContext(cb, startingFeatureSet)
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
					if err := controllerCtx.CVO.InitializeFromPayload(runContext, restConfig, burstRestConfig); err != nil {
						if firstError == nil {
							firstError = err
						}
						klog.Infof("Failed to initialize from payload; shutting down: %v", err)
						resultChannel <- asyncResult{name: "payload initialization", error: firstError}
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

	return resourcelock.New(resourcelock.ConfigMapsLeasesResourceLock, namespace, name, client.CoreV1(), client.CoordinationV1(), resourcelock.ResourceLockConfig{
		Identity:      id,
		EventRecorder: eventBroadcaster.NewRecorder(scheme.Scheme, v1.EventSource{Component: namespace}),
	})
}

func resyncPeriod(minResyncPeriod time.Duration) func() time.Duration {
	return func() time.Duration {
		factor := rand.Float64() + 1
		return time.Duration(float64(minResyncPeriod.Nanoseconds()) * factor)
	}
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
	StopOnFeatureGateChange *featurechangestopper.TechPreviewChangeStopper

	CVInformerFactory                     externalversions.SharedInformerFactory
	OpenshiftConfigInformerFactory        informers.SharedInformerFactory
	OpenshiftConfigManagedInformerFactory informers.SharedInformerFactory
	InformerFactory                       externalversions.SharedInformerFactory
}

// NewControllerContext initializes the default Context for the current Options. It does
// not start any background processes.
func (o *Options) NewControllerContext(cb *ClientBuilder, startingFeatureSet string) *Context {
	client := cb.ClientOrDie("shared-informer")
	kubeClient := cb.KubeClientOrDie(internal.ConfigNamespace, useProtobuf)

	cvInformer := externalversions.NewFilteredSharedInformerFactory(client, resyncPeriod(o.ResyncInterval)(), "", func(opts *metav1.ListOptions) {
		opts.FieldSelector = fmt.Sprintf("metadata.name=%s", o.Name)
	})
	openshiftConfigInformer := informers.NewSharedInformerFactoryWithOptions(kubeClient, resyncPeriod(o.ResyncInterval)(), informers.WithNamespace(internal.ConfigNamespace))
	openshiftConfigManagedInformer := informers.NewSharedInformerFactoryWithOptions(kubeClient, resyncPeriod(o.ResyncInterval)(), informers.WithNamespace(internal.ConfigManagedNamespace))

	sharedInformers := externalversions.NewSharedInformerFactory(client, resyncPeriod(o.ResyncInterval)())

	coInformer := sharedInformers.Config().V1().ClusterOperators()
	ctx := &Context{
		CVInformerFactory:                     cvInformer,
		OpenshiftConfigInformerFactory:        openshiftConfigInformer,
		OpenshiftConfigManagedInformerFactory: openshiftConfigManagedInformer,
		InformerFactory:                       sharedInformers,

		CVO: cvo.New(
			o.NodeName,
			o.Namespace, o.Name,
			o.ReleaseImage,
			o.PayloadOverride,
			resyncPeriod(o.ResyncInterval)(),
			cvInformer.Config().V1().ClusterVersions(),
			coInformer,
			openshiftConfigInformer.Core().V1().ConfigMaps(),
			openshiftConfigManagedInformer.Core().V1().ConfigMaps(),
			sharedInformers.Config().V1().Proxies(),
			cb.ClientOrDie(o.Namespace),
			cb.KubeClientOrDie(o.Namespace, useProtobuf),
			o.Exclude,
			startingFeatureSet,
			o.ClusterProfile,
		),

		StopOnFeatureGateChange: featurechangestopper.New(startingFeatureSet, sharedInformers.Config().V1().FeatureGates()),
	}

	if o.EnableAutoUpdate {
		ctx.AutoUpdate = autoupdate.New(
			o.Namespace, o.Name,
			cvInformer.Config().V1().ClusterVersions(),
			sharedInformers.Config().V1().ClusterOperators(),
			cb.ClientOrDie(o.Namespace),
			cb.KubeClientOrDie(o.Namespace),
		)
	}
	if o.ListenAddr != "" {
		if err := ctx.CVO.RegisterMetrics(coInformer.Informer()); err != nil {
			panic(err)
		}
	}
	return ctx
}

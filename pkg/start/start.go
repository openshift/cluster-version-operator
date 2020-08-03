// package start initializes and launches the core cluster version operator
// loops.
package start

import (
	"context"
	"crypto/tls"
	"fmt"
	"io/ioutil"
	"math/rand"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/google/uuid"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
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
	"k8s.io/klog"

	clientset "github.com/openshift/client-go/config/clientset/versioned"
	externalversions "github.com/openshift/client-go/config/informers/externalversions"
	"github.com/openshift/cluster-version-operator/pkg/autoupdate"
	"github.com/openshift/cluster-version-operator/pkg/cvo"
	"github.com/openshift/cluster-version-operator/pkg/internal"
	"github.com/openshift/library-go/pkg/crypto"
)

const (
	defaultComponentName      = "version"
	defaultComponentNamespace = "openshift-cluster-version"

	minResyncPeriod = 2 * time.Minute

	// TODO set the slower lease values after we're able to reliably shut down gracefully.  This fixes the slow status update for now.
	//leaseDuration = 90 * time.Second
	//renewDeadline = 45 * time.Second
	//retryPeriod   = 30 * time.Second
	leaseDuration = 30 * time.Second
	renewDeadline = 15 * time.Second
	retryPeriod   = 10 * time.Second
)

// Options are the valid inputs to starting the CVO.
type Options struct {
	ReleaseImage    string
	ServingCertFile string
	ServingKeyFile  string

	Kubeconfig string
	NodeName   string
	ListenAddr string

	EnableAutoUpdate            bool
	EnableDefaultClusterVersion bool

	// Exclude is used to determine whether to exclude
	// certain manifests based on an annotation:
	// exclude.release.openshift.io/<identifier>=true
	Exclude string

	// for testing only
	Name            string
	Namespace       string
	PayloadOverride string
	ResyncInterval  time.Duration
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
	}
}

func (o *Options) Run(ctx context.Context) error {
	if o.NodeName == "" {
		return fmt.Errorf("node-name is required")
	}
	if o.ReleaseImage == "" {
		return fmt.Errorf("missing --release-image flag, it is required")
	}
	if o.ServingCertFile == "" && o.ServingKeyFile != "" {
		return fmt.Errorf("--serving-key-file was set, so --serving-cert-file must also be set")
	}
	if o.ServingKeyFile == "" && o.ServingCertFile != "" {
		return fmt.Errorf("--serving-cert-file was set, so --serving-key-file must also be set")
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
	lock, err := createResourceLock(cb, o.Namespace, o.Name)
	if err != nil {
		return err
	}

	// initialize the controllers and attempt to load the payload information
	controllerCtx := o.NewControllerContext(cb)
	if err := controllerCtx.CVO.InitializeFromPayload(cb.RestConfig(defaultQPS), cb.RestConfig(highQPS)); err != nil {
		return err
	}

	o.run(ctx, controllerCtx, lock)
	return nil
}

func (o *Options) makeTLSConfig() (*tls.Config, error) {
	// Load the initial certificate contents.
	certBytes, err := ioutil.ReadFile(o.ServingCertFile)
	if err != nil {
		return nil, err
	}
	keyBytes, err := ioutil.ReadFile(o.ServingKeyFile)
	if err != nil {
		return nil, err
	}
	certificate, err := tls.X509KeyPair(certBytes, keyBytes)
	if err != nil {
		return nil, err
	}

	return crypto.SecureTLSConfig(&tls.Config{
		GetCertificate: func(_ *tls.ClientHelloInfo) (*tls.Certificate, error) {
			return &certificate, nil
		},
	}), nil
}

// run launches a number of goroutines to handle manifest application,
// metrics serving, etc.  It continues operating until ctx.Done(),
// and then attempts a clean shutdown limited by an internal context
// with a two-minute cap.  It returns after it successfully collects all
// launched goroutines.
func (o *Options) run(ctx context.Context, controllerCtx *Context, lock *resourcelock.ConfigMapLock) {
	runContext, runCancel := context.WithCancel(ctx) // so we can cancel internally on errors or TERM
	defer runCancel()
	shutdownContext, shutdownCancel := context.WithCancel(context.Background()) // extends beyond ctx
	defer shutdownCancel()
	postMainContext, postMainCancel := context.WithCancel(context.Background()) // extends beyond ctx
	defer postMainCancel()

	ch := make(chan os.Signal, 1)
	defer func() { signal.Stop(ch) }()
	signal.Notify(ch, os.Interrupt, syscall.SIGTERM)
	go func() {
		sig := <-ch
		klog.Infof("Shutting down due to %s", sig)
		runCancel()
		sig = <-ch
		klog.Fatalf("Received shutdown signal twice, exiting: %s", sig)
	}()

	resultChannel := make(chan asyncResult, 1)
	resultChannelCount := 0
	if o.ListenAddr != "" {
		var tlsConfig *tls.Config
		if o.ServingCertFile != "" || o.ServingKeyFile != "" {
			var err error
			tlsConfig, err = o.makeTLSConfig()
			if err != nil {
				klog.Fatalf("Failed to create TLS config: %v", err)
			}
		}
		resultChannelCount++
		go func() {
			err := cvo.RunMetrics(postMainContext, shutdownContext, o.ListenAddr, tlsConfig)
			resultChannel <- asyncResult{name: "metrics server", error: err}
		}()
	}

	informersDone := postMainContext.Done()
	// FIXME: would be nice if there was a way to collect these.
	controllerCtx.CVInformerFactory.Start(informersDone)
	controllerCtx.OpenshiftConfigInformerFactory.Start(informersDone)
	controllerCtx.OpenshiftConfigManagedInformerFactory.Start(informersDone)
	controllerCtx.InformerFactory.Start(informersDone)

	resultChannelCount++
	go func() {
		leaderelection.RunOrDie(postMainContext, leaderelection.LeaderElectionConfig{
			Lock:            lock,
			ReleaseOnCancel: true,
			LeaseDuration:   leaseDuration,
			RenewDeadline:   renewDeadline,
			RetryPeriod:     retryPeriod,
			Callbacks: leaderelection.LeaderCallbacks{
				OnStartedLeading: func(_ context.Context) { // no need for this passed-through postMainContext, because goroutines we launch inside will use runContext
					resultChannelCount++
					go func() {
						err := controllerCtx.CVO.Run(runContext, 2)
						resultChannel <- asyncResult{name: "main operator", error: err}
					}()

					if controllerCtx.AutoUpdate != nil {
						resultChannelCount++
						go func() {
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
		resultChannel <- asyncResult{name: "leader controller", error: nil}
	}()

	var shutdownTimer *time.Timer
	for resultChannelCount > 0 {
		klog.Infof("Waiting on %d outstanding goroutines.", resultChannelCount)
		if shutdownTimer == nil { // running
			select {
			case <-runContext.Done():
				klog.Info("Run context completed; beginning two-minute graceful shutdown period.")
				shutdownTimer = time.NewTimer(2 * time.Minute)
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
func createResourceLock(cb *ClientBuilder, namespace, name string) (*resourcelock.ConfigMapLock, error) {
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

	return &resourcelock.ConfigMapLock{
		ConfigMapMeta: metav1.ObjectMeta{
			Namespace: namespace,
			Name:      name,
		},
		Client: client.CoreV1(),
		LockConfig: resourcelock.ResourceLockConfig{
			Identity:      id,
			EventRecorder: eventBroadcaster.NewRecorder(scheme.Scheme, v1.EventSource{Component: namespace}),
		},
	}, nil
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

// Context holds the controllers for this operator and exposes a unified start command.
type Context struct {
	CVO        *cvo.Operator
	AutoUpdate *autoupdate.Controller

	CVInformerFactory                     externalversions.SharedInformerFactory
	OpenshiftConfigInformerFactory        informers.SharedInformerFactory
	OpenshiftConfigManagedInformerFactory informers.SharedInformerFactory
	InformerFactory                       externalversions.SharedInformerFactory
}

// NewControllerContext initializes the default Context for the current Options. It does
// not start any background processes.
func (o *Options) NewControllerContext(cb *ClientBuilder) *Context {
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
			o.EnableDefaultClusterVersion,
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
		),
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

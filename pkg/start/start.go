// package start initializes and launches the core cluster version operator
// loops.
package start

import (
	"context"
	"crypto/tls"
	"fmt"
	"io/ioutil"
	"math/rand"
	"net"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/cockroachdb/cmux"

	"github.com/google/uuid"
	"github.com/prometheus/client_golang/prometheus/promhttp"
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

	leaseDuration = 90 * time.Second
	renewDeadline = 45 * time.Second
	retryPeriod   = 30 * time.Second
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

func (o *Options) Run() error {
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

	// TODO: Kube 1.14 will contain a ReleaseOnCancel boolean on
	//   LeaderElectionConfig that allows us to have the lock code
	//   release the lease when this context is cancelled. At that
	//   time we can remove our changes to OnStartedLeading.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ch := make(chan os.Signal, 1)
	defer func() { signal.Stop(ch) }()
	signal.Notify(ch, os.Interrupt, syscall.SIGTERM)
	go func() {
		sig := <-ch
		klog.Infof("Shutting down due to %s", sig)
		cancel()

		// exit after 2s no matter what
		select {
		case <-time.After(5 * time.Second):
			klog.Fatalf("Exiting")
		case <-ch:
			klog.Fatalf("Received shutdown signal twice, exiting")
		}
	}()

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

func (o *Options) run(ctx context.Context, controllerCtx *Context, lock *resourcelock.ConfigMapLock) {
	// listen on metrics
	if o.ListenAddr != "" {
		handler := http.NewServeMux()
		handler.Handle("/metrics", promhttp.Handler())
		tcpl, err := net.Listen("tcp", o.ListenAddr)
		if err != nil {
			klog.Fatalf("Listen error: %v", err)
		}

		// if a TLS connection was requested, set up a connection mux that will send TLS requests to
		// the TLS server but send HTTP requests to the HTTP server. Preserves the ability for legacy
		// HTTP, needed during upgrade, while still allowing TLS certs and end to end metrics protection.
		m := cmux.New(tcpl)

		// match HTTP first
		httpl := m.Match(cmux.HTTP1())
		go func() {
			s := &http.Server{
				Handler: handler,
			}
			if err := s.Serve(httpl); err != cmux.ErrListenerClosed {
				klog.Fatalf("HTTP serve error: %v", err)
			}
		}()

		if o.ServingCertFile != "" || o.ServingKeyFile != "" {
			tlsConfig, err := o.makeTLSConfig()
			if err != nil {
				klog.Fatalf("Failed to create TLS config: %v", err)
			}

			tlsListener := tls.NewListener(m.Match(cmux.Any()), tlsConfig)
			klog.Infof("Metrics port listening for HTTP and HTTPS on %v", o.ListenAddr)
			go func() {
				s := &http.Server{
					Handler: handler,
				}
				if err := s.Serve(tlsListener); err != cmux.ErrListenerClosed {
					klog.Fatalf("HTTPS serve error: %v", err)
				}
			}()

			go func() {
				if err := m.Serve(); err != nil {
					klog.Errorf("CMUX serve error: %v", err)
				}
			}()
		} else {
			klog.Infof("Metrics port listening for HTTP on %v", o.ListenAddr)
		}
	}

	exit := make(chan struct{})
	exitClose := sync.Once{}

	// TODO: when we switch to graceful lock shutdown, this can be
	// moved back inside RunOrDie
	// TODO: properly wire ctx here
	go leaderelection.RunOrDie(context.TODO(), leaderelection.LeaderElectionConfig{
		Lock:          lock,
		LeaseDuration: leaseDuration,
		RenewDeadline: renewDeadline,
		RetryPeriod:   retryPeriod,
		Callbacks: leaderelection.LeaderCallbacks{
			OnStartedLeading: func(localCtx context.Context) {
				controllerCtx.Start(ctx)
				select {
				case <-ctx.Done():
					// WARNING: this is not completely safe until we have Kube 1.14 and ReleaseOnCancel
					//   and client-go ContextCancelable, which allows us to block new API requests before
					//   we step down. However, the CVO isn't that sensitive to races and can tolerate
					//   brief overlap.
					klog.Infof("Stepping down as leader")
					// give the controllers some time to shut down
					time.Sleep(100 * time.Millisecond)
					// if we still hold the leader lease, clear the owner identity (other lease watchers
					// still have to wait for expiration) like the new ReleaseOnCancel code will do.
					if err := lock.Update(resourcelock.LeaderElectionRecord{}); err == nil {
						// if we successfully clear the owner identity, we can safely delete the record
						if err := lock.Client.ConfigMaps(lock.ConfigMapMeta.Namespace).Delete(lock.ConfigMapMeta.Name, nil); err != nil {
							klog.Warningf("Unable to step down cleanly: %v", err)
						}
					}
					klog.Infof("Finished shutdown")
					exitClose.Do(func() { close(exit) })
				case <-localCtx.Done():
					// we will exit in OnStoppedLeading
				}
			},
			OnStoppedLeading: func() {
				klog.Warning("leaderelection lost")
				exitClose.Do(func() { close(exit) })
			},
		},
	})

	<-exit
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

	CVInformerFactory              externalversions.SharedInformerFactory
	OpenshiftConfigInformerFactory informers.SharedInformerFactory
	InformerFactory                externalversions.SharedInformerFactory
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

	sharedInformers := externalversions.NewSharedInformerFactory(client, resyncPeriod(o.ResyncInterval)())

	ctx := &Context{
		CVInformerFactory:              cvInformer,
		OpenshiftConfigInformerFactory: openshiftConfigInformer,
		InformerFactory:                sharedInformers,

		CVO: cvo.New(
			o.NodeName,
			o.Namespace, o.Name,
			o.ReleaseImage,
			o.EnableDefaultClusterVersion,
			o.PayloadOverride,
			resyncPeriod(o.ResyncInterval)(),
			cvInformer.Config().V1().ClusterVersions(),
			sharedInformers.Config().V1().ClusterOperators(),
			openshiftConfigInformer.Core().V1().ConfigMaps(),
			sharedInformers.Config().V1().Proxies(),
			cb.ClientOrDie(o.Namespace),
			cb.KubeClientOrDie(o.Namespace, useProtobuf),
			o.ListenAddr != "",
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
	return ctx
}

// Start launches the controllers in the provided context and any supporting
// infrastructure. When ch is closed the controllers will be shut down.
func (c *Context) Start(ctx context.Context) {
	ch := ctx.Done()
	go c.CVO.Run(ctx, 2)
	if c.AutoUpdate != nil {
		go c.AutoUpdate.Run(2, ch)
	}
	c.CVInformerFactory.Start(ch)
	c.OpenshiftConfigInformerFactory.Start(ch)
	c.InformerFactory.Start(ch)
}

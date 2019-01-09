package start

import (
	"context"
	"fmt"
	"math/rand"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/golang/glog"
	"github.com/google/uuid"

	clientset "github.com/openshift/client-go/config/clientset/versioned"
	informers "github.com/openshift/client-go/config/informers/externalversions"
	"github.com/openshift/library-go/pkg/controller/fileobserver"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	v1 "k8s.io/api/core/v1"
	apiext "k8s.io/apiextensions-apiserver/pkg/client/clientset/clientset"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/tools/leaderelection"
	"k8s.io/client-go/tools/leaderelection/resourcelock"
	"k8s.io/client-go/tools/record"

	"github.com/openshift/cluster-version-operator/pkg/autoupdate"
	"github.com/openshift/cluster-version-operator/pkg/cvo"
)

const (
	defaultComponentName      = "version"
	defaultComponentNamespace = "openshift-cluster-version"

	minResyncPeriod = 2 * time.Minute

	leaseDuration = 90 * time.Second
	renewDeadline = 45 * time.Second
	retryPeriod   = 30 * time.Second
)

type Options struct {
	ReleaseImage string

	Kubeconfig string
	NodeName   string
	ListenAddr string

	EnableAutoUpdate bool

	// for testing only
	Name            string
	Namespace       string
	PayloadOverride string
	EnableMetrics   bool
	ResyncInterval  time.Duration
}

func defaultEnv(name, defaultValue string) string {
	env, ok := os.LookupEnv(name)
	if !ok {
		return defaultValue
	}
	return env
}

func NewOptions() *Options {
	return &Options{
		ListenAddr: "0.0.0.0:9099",
		NodeName:   os.Getenv("NODE_NAME"),

		// exposed only for testing
		Namespace:       defaultEnv("CVO_NAMESPACE", defaultComponentNamespace),
		Name:            defaultEnv("CVO_NAME", defaultComponentName),
		PayloadOverride: os.Getenv("PAYLOAD_OVERRIDE"),
		ResyncInterval:  minResyncPeriod,
		EnableMetrics:   true,
	}
}

func (o *Options) Run() error {
	if o.NodeName == "" {
		return fmt.Errorf("node-name is required")
	}
	if o.ReleaseImage == "" {
		return fmt.Errorf("missing --release-image flag, it is required")
	}
	if len(o.PayloadOverride) > 0 {
		glog.Warningf("Using an override payload directory for testing only: %s", o.PayloadOverride)
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
	fobsrv, err := fileobserver.NewObserver(1 * time.Minute)
	if err != nil {
		return err
	}
	controllerCtx := o.NewControllerContext(cb)

	// TODO: Kube 1.14 will contain a ReleaseOnCancel boolean on
	//   LeaderElectionConfig that allows us to have the lock code
	//   release the lease when this context is cancelled. At that
	//   time we can remove our changes to OnStartedLeading.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigCh := make(chan os.Signal, 1)
	defer func() { signal.Stop(sigCh) }()
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)

	fobsrvCh := make(chan string, 1)
	defer close(fobsrvCh)
	fobsrv.AddReactor(func(filename string, _ fileobserver.ActionType) error {
		fobsrvCh <- filename
		return nil
	}, cb.ObservedFiles()...)
	go fobsrv.Run(ctx.Done())

	go func() {
		select {
		case sig := <-sigCh:
			glog.Infof("Shutting down due to %s", sig)
		case fname := <-fobsrvCh:
			glog.Infof("Shutting down due file %s changed", fname)
		}
		cancel()

		// exit after 2s no matter what
		select {
		case <-time.After(2 * time.Second):
			glog.Fatalf("Exiting")
		case <-sigCh:
			glog.Fatalf("Received shutdown signal twice, exiting")
		}
	}()

	o.run(ctx, controllerCtx, lock)
	return nil
}

func (o *Options) run(ctx context.Context, controllerCtx *Context, lock *resourcelock.ConfigMapLock) {
	// listen on metrics
	if len(o.ListenAddr) > 0 {
		mux := http.NewServeMux()
		mux.Handle("/metrics", promhttp.Handler())
		go func() {
			if err := http.ListenAndServe(o.ListenAddr, mux); err != nil {
				glog.Fatalf("Unable to start metrics server: %v", err)
			}
		}()
	}

	exit := make(chan struct{})

	// TODO: when we switch to graceful lock shutdown, this can be
	// moved back inside RunOrDie
	go leaderelection.RunOrDie(leaderelection.LeaderElectionConfig{
		Lock:          lock,
		LeaseDuration: leaseDuration,
		RenewDeadline: renewDeadline,
		RetryPeriod:   retryPeriod,
		Callbacks: leaderelection.LeaderCallbacks{
			OnStartedLeading: func(stop <-chan struct{}) {
				controllerCtx.Start(ctx.Done())
				select {
				case <-ctx.Done():
					// WARNING: this is not completely safe until we have Kube 1.14 and ReleaseOnCancel
					//   and client-go ContextCancelable, which allows us to block new API requests before
					//   we step down. However, the CVO isn't that sensitive to races and can tolerate
					//   brief overlap.
					glog.Infof("Stepping down as leader")
					// give the controllers some time to shut down
					time.Sleep(100 * time.Millisecond)
					// if we still hold the leader lease, clear the owner identity (other lease watchers
					// still have to wait for expiration) like the new ReleaseOnCancel code will do.
					if err := lock.Update(resourcelock.LeaderElectionRecord{}); err == nil {
						// if we successfully clear the owner identity, we can safely delete the record
						if err := lock.Client.ConfigMaps(lock.ConfigMapMeta.Namespace).Delete(lock.ConfigMapMeta.Name, nil); err != nil {
							glog.Warningf("Unable to step down cleanly: %v", err)
						}
					}
					glog.Infof("Finished shutdown")
					close(exit)
				case <-stop:
					// we will exit in OnStoppedLeading
				}
			},
			OnStoppedLeading: func() {
				glog.Warning("leaderelection lost")
				close(exit)
			},
		},
	})

	<-exit
}

// createResourceLock initializes the lock.
func createResourceLock(cb *ClientBuilder, namespace, name string) (*resourcelock.ConfigMapLock, error) {
	recorder := record.NewBroadcaster().NewRecorder(runtime.NewScheme(), v1.EventSource{Component: namespace})

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
		Client: cb.KubeClientOrDie("leader-election").CoreV1(),
		LockConfig: resourcelock.ResourceLockConfig{
			Identity:      id,
			EventRecorder: recorder,
		},
	}, nil
}

func resyncPeriod(minResyncPeriod time.Duration) func() time.Duration {
	return func() time.Duration {
		factor := rand.Float64() + 1
		return time.Duration(float64(minResyncPeriod.Nanoseconds()) * factor)
	}
}

type ClientBuilder struct {
	config *rest.Config

	observedFiles []string
}

func (cb *ClientBuilder) ObservedFiles() []string {
	return cb.observedFiles
}

func (cb *ClientBuilder) RestConfig() *rest.Config {
	c := rest.CopyConfig(cb.config)
	return c
}

func (cb *ClientBuilder) ClientOrDie(name string) clientset.Interface {
	return clientset.NewForConfigOrDie(rest.AddUserAgent(cb.config, name))
}

func (cb *ClientBuilder) KubeClientOrDie(name string) kubernetes.Interface {
	return kubernetes.NewForConfigOrDie(rest.AddUserAgent(cb.config, name))
}

func (cb *ClientBuilder) APIExtClientOrDie(name string) apiext.Interface {
	return apiext.NewForConfigOrDie(rest.AddUserAgent(cb.config, name))
}

func newClientBuilder(kubeconfig string) (*ClientBuilder, error) {
	var observedFiles []string
	if kubeconfig != "" {
		observedFiles = append(observedFiles, kubeconfig)
	} else {
		observedFiles = append(observedFiles, "/var/run/secrets/kubernetes.io/serviceaccount/ca.crt")
		observedFiles = append(observedFiles, "/var/run/secrets/kubernetes.io/serviceaccount/token")
	}
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

type Context struct {
	CVO        *cvo.Operator
	AutoUpdate *autoupdate.Controller

	CVInformerFactory informers.SharedInformerFactory
	InformerFactory   informers.SharedInformerFactory
}

func (o *Options) NewControllerContext(cb *ClientBuilder) *Context {
	client := cb.ClientOrDie("shared-informer")

	cvInformer := informers.NewFilteredSharedInformerFactory(client, resyncPeriod(o.ResyncInterval)(), "", func(opts *metav1.ListOptions) {
		opts.FieldSelector = fmt.Sprintf("metadata.name=%s", o.Name)
	})
	sharedInformers := informers.NewSharedInformerFactory(client, resyncPeriod(o.ResyncInterval)())

	ctx := &Context{
		CVInformerFactory: cvInformer,
		InformerFactory:   sharedInformers,

		CVO: cvo.New(
			o.NodeName,
			o.Namespace, o.Name,
			o.ReleaseImage,
			o.PayloadOverride,
			resyncPeriod(o.ResyncInterval)(),
			cvInformer.Config().V1().ClusterVersions(),
			sharedInformers.Config().V1().ClusterOperators(),
			cb.RestConfig(),
			cb.ClientOrDie(o.Namespace),
			cb.KubeClientOrDie(o.Namespace),
			cb.APIExtClientOrDie(o.Namespace),
			o.EnableMetrics,
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

func (ctx *Context) Start(ch <-chan struct{}) {
	go ctx.CVO.Run(2, ch)
	if ctx.AutoUpdate != nil {
		go ctx.AutoUpdate.Run(2, ch)
	}
	ctx.CVInformerFactory.Start(ch)
	ctx.InformerFactory.Start(ch)
}

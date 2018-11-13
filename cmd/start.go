package main

import (
	"flag"
	"fmt"
	"math/rand"
	"net/http"
	"os"
	"strings"
	"time"

	"k8s.io/apimachinery/pkg/api/errors"

	"k8s.io/apimachinery/pkg/util/wait"

	"github.com/golang/glog"
	"github.com/google/uuid"
	"github.com/openshift/cluster-version-operator/pkg/autoupdate"
	"github.com/openshift/cluster-version-operator/pkg/cvo"
	clientset "github.com/openshift/cluster-version-operator/pkg/generated/clientset/versioned"
	informers "github.com/openshift/cluster-version-operator/pkg/generated/informers/externalversions"
	"github.com/openshift/cluster-version-operator/pkg/version"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/spf13/cobra"
	"k8s.io/api/core/v1"
	apiext "k8s.io/apiextensions-apiserver/pkg/client/clientset/clientset"
	apiextinformers "k8s.io/apiextensions-apiserver/pkg/client/informers/externalversions"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	kubeinformers "k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/tools/leaderelection"
	"k8s.io/client-go/tools/leaderelection/resourcelock"
	"k8s.io/client-go/tools/record"
)

const (
	minResyncPeriod = 2 * time.Minute

	leaseDuration = 90 * time.Second
	renewDeadline = 45 * time.Second
	retryPeriod   = 30 * time.Second
)

var (
	startCmd = &cobra.Command{
		Use:   "start",
		Short: "Starts Cluster Version Operator",
		Long:  "",
		Run:   runStartCmd,
	}

	startOpts struct {
		kubeconfig string
		nodeName   string
		listenAddr string

		enableAutoUpdate bool
	}
)

func init() {
	rootCmd.AddCommand(startCmd)
	startCmd.PersistentFlags().StringVar(&startOpts.listenAddr, "listen", "0.0.0.0:9099", "Address to listen on for metrics")
	startCmd.PersistentFlags().StringVar(&startOpts.kubeconfig, "kubeconfig", "", "Kubeconfig file to access a remote cluster (testing only)")
	startCmd.PersistentFlags().StringVar(&startOpts.nodeName, "node-name", "", "kubernetes node name CVO is scheduled on.")
	startCmd.PersistentFlags().BoolVar(&startOpts.enableAutoUpdate, "enable-auto-update", true, "Enables the autoupdate controller.")
}

func runStartCmd(cmd *cobra.Command, args []string) {
	flag.Set("logtostderr", "true")
	flag.Parse()

	// To help debugging, immediately log version
	glog.Infof("%s", version.String)

	if startOpts.nodeName == "" {
		name, ok := os.LookupEnv("NODE_NAME")
		if !ok || name == "" {
			glog.Fatalf("node-name is required")
		}
		startOpts.nodeName = name
	}

	if len(startOpts.listenAddr) > 0 {
		mux := http.NewServeMux()
		mux.Handle("/metrics", promhttp.Handler())
		go func() {
			if err := http.ListenAndServe(startOpts.listenAddr, mux); err != nil {
				glog.Fatalf("Unable to start metrics server: %v", err)
			}
		}()
	}

	cb, err := newClientBuilder(startOpts.kubeconfig)
	if err != nil {
		glog.Fatalf("error creating clients: %v", err)
	}

	if err := resolveReleaseImageByDigest(cb); err != nil {
		glog.Fatalf("error: %v", err)
	}

	if rootOpts.releaseImage == "" {
		glog.Fatalf("missing --release-image flag, it is required")
	}

	stopCh := make(chan struct{})
	run := func(stop <-chan struct{}) {

		ctx := createControllerContext(cb, stopCh)
		if err := startControllers(ctx); err != nil {
			glog.Fatalf("error starting controllers: %v", err)
		}

		ctx.InformerFactory.Start(ctx.Stop)
		ctx.KubeInformerFactory.Start(ctx.Stop)
		ctx.APIExtInformerFactory.Start(ctx.Stop)
		close(ctx.InformersStarted)

		select {}
	}

	leaderelection.RunOrDie(leaderelection.LeaderElectionConfig{
		Lock:          createResourceLock(cb),
		LeaseDuration: leaseDuration,
		RenewDeadline: renewDeadline,
		RetryPeriod:   retryPeriod,
		Callbacks: leaderelection.LeaderCallbacks{
			OnStartedLeading: run,
			OnStoppedLeading: func() {
				glog.Fatalf("leaderelection lost")
			},
		},
	})
	panic("unreachable")
}

// resolveReleaseImageByDigest will attempt to look up the current pod and get the imageID as reported
// to the apiserver by the kubelet. This allows us to pivot from an image tag to an image digest without
// having to reproduce the logic to pull and access images with secrets. It will report an error if
// no imageID could be resolved within the time allotted.
func resolveReleaseImageByDigest(cb *clientBuilder) error {
	podName, nsName := os.Getenv("POD_NAME"), os.Getenv("POD_NAMESPACE")
	if len(podName) == 0 || len(nsName) == 0 {
		return nil
	}
	// TODO: this may be too hacky, but in practice should allow us to bypass this lookup when we don't
	// need it (after we have updated ourself to a digest during a sync).
	if strings.Contains(rootOpts.releaseImage, "@") {
		return nil
	}
	// it may take a few seconds for the image URL to become available
	err := wait.PollImmediate(5*time.Second, time.Minute, func() (bool, error) {
		pod, err := cb.KubeClientOrDie("initial_info").CoreV1().Pods(nsName).Get(podName, metav1.GetOptions{})
		if err != nil {
			if errors.IsNotFound(err) || errors.IsForbidden(err) || errors.IsUnauthorized(err) {
				return false, nil
			}
			return false, fmt.Errorf("could not look up pod %s/%s to determine authoritative image URL: %v", nsName, podName, err)
		}
		for _, status := range pod.Status.ContainerStatuses {
			if status.Name != "cluster-version-operator" {
				continue
			}
			switch {
			case len(status.ImageID) == 0:
				// loop until imageID is set
				return false, nil
			case rootOpts.releaseImage != status.ImageID:
				glog.Infof("Exact cluster version operator image is %s, will lock to that version", status.ImageID)
				rootOpts.releaseImage = status.ImageID
			}
			return true, nil
		}
		return false, nil
	})
	if err == wait.ErrWaitTimeout {
		return fmt.Errorf("unable to resolve release image tag to digest by looking up cluster-version-operator in pod %s/%s status", nsName, podName)
	}
	return err
}

func createResourceLock(cb *clientBuilder) resourcelock.Interface {
	recorder := record.
		NewBroadcaster().
		NewRecorder(runtime.NewScheme(), v1.EventSource{Component: componentName})

	id, err := os.Hostname()
	if err != nil {
		glog.Fatalf("error creating lock: %v", err)
	}

	uuid, err := uuid.NewRandom()
	if err != nil {
		glog.Fatalf("Failed to generate UUID: %v", err)
	}

	// add a uniquifier so that two processes on the same host don't accidentally both become active
	id = id + "_" + uuid.String()

	return &resourcelock.ConfigMapLock{
		ConfigMapMeta: metav1.ObjectMeta{
			Namespace: componentNamespace,
			Name:      componentName,
		},
		Client: cb.KubeClientOrDie("leader-election").CoreV1(),
		LockConfig: resourcelock.ResourceLockConfig{
			Identity:      id,
			EventRecorder: recorder,
		},
	}
}

func resyncPeriod() func() time.Duration {
	return func() time.Duration {
		factor := rand.Float64() + 1
		return time.Duration(float64(minResyncPeriod.Nanoseconds()) * factor)
	}
}

type clientBuilder struct {
	config *rest.Config
}

func (cb *clientBuilder) RestConfig() *rest.Config {
	c := rest.CopyConfig(cb.config)
	return c
}

func (cb *clientBuilder) ClientOrDie(name string) clientset.Interface {
	return clientset.NewForConfigOrDie(rest.AddUserAgent(cb.config, name))
}

func (cb *clientBuilder) KubeClientOrDie(name string) kubernetes.Interface {
	return kubernetes.NewForConfigOrDie(rest.AddUserAgent(cb.config, name))
}

func (cb *clientBuilder) APIExtClientOrDie(name string) apiext.Interface {
	return apiext.NewForConfigOrDie(rest.AddUserAgent(cb.config, name))
}

func newClientBuilder(kubeconfig string) (*clientBuilder, error) {
	var config *rest.Config
	var err error

	if kubeconfig != "" {
		glog.V(4).Infof("Loading kube client config from path %q", kubeconfig)
		config, err = clientcmd.BuildConfigFromFlags("", kubeconfig)
	} else {
		glog.V(4).Infof("Using in-cluster kube client config")
		config, err = rest.InClusterConfig()
	}
	if err != nil {
		return nil, err
	}

	return &clientBuilder{
		config: config,
	}, nil
}

type controllerContext struct {
	ClientBuilder *clientBuilder

	InformerFactory       informers.SharedInformerFactory
	KubeInformerFactory   kubeinformers.SharedInformerFactory
	APIExtInformerFactory apiextinformers.SharedInformerFactory

	Stop <-chan struct{}

	InformersStarted chan struct{}

	ResyncPeriod func() time.Duration
}

func createControllerContext(cb *clientBuilder, stop <-chan struct{}) *controllerContext {
	client := cb.ClientOrDie("shared-informer")
	kubeClient := cb.KubeClientOrDie("kube-shared-informer")
	apiExtClient := cb.APIExtClientOrDie("apiext-shared-informer")

	sharedInformers := informers.NewSharedInformerFactory(client, resyncPeriod()())
	kubeSharedInformer := kubeinformers.NewSharedInformerFactory(kubeClient, resyncPeriod()())
	apiExtSharedInformer := apiextinformers.NewSharedInformerFactory(apiExtClient, resyncPeriod()())

	return &controllerContext{
		ClientBuilder:         cb,
		InformerFactory:       sharedInformers,
		KubeInformerFactory:   kubeSharedInformer,
		APIExtInformerFactory: apiExtSharedInformer,
		Stop:                  stop,
		InformersStarted:      make(chan struct{}),
		ResyncPeriod:          resyncPeriod(),
	}
}

func startControllers(ctx *controllerContext) error {
	overrideDirectory := os.Getenv("PAYLOAD_OVERRIDE")
	if len(overrideDirectory) > 0 {
		glog.Warningf("Using an override payload directory for testing only: %s", overrideDirectory)
	}

	go cvo.New(
		startOpts.nodeName,
		componentNamespace, componentName,
		rootOpts.releaseImage,
		overrideDirectory,
		ctx.ResyncPeriod(),
		ctx.InformerFactory.Config().V1().ClusterVersions(),
		ctx.InformerFactory.Operatorstatus().V1().ClusterOperators(),
		ctx.ClientBuilder.RestConfig(),
		ctx.ClientBuilder.ClientOrDie(componentName),
		ctx.ClientBuilder.KubeClientOrDie(componentName),
		ctx.ClientBuilder.APIExtClientOrDie(componentName),
	).Run(2, ctx.Stop)

	if startOpts.enableAutoUpdate {
		go autoupdate.New(
			componentNamespace, componentName,
			ctx.InformerFactory.Config().V1().ClusterVersions(),
			ctx.InformerFactory.Operatorstatus().V1().ClusterOperators(),
			ctx.ClientBuilder.ClientOrDie(componentName),
			ctx.ClientBuilder.KubeClientOrDie(componentName),
		).Run(2, ctx.Stop)
	}

	return nil
}

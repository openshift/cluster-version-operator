package main

import (
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/openshift/cluster-version-operator/pkg/cvo"
	"github.com/openshift/cluster-version-operator/pkg/version"

	"github.com/golang/glog"
	"k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/tools/leaderelection"
	"k8s.io/client-go/tools/leaderelection/resourcelock"
	"k8s.io/client-go/tools/record"
)

const (
	componentName = "cluster-version-operator"
	leaseDuration = 90 * time.Second
	renewDeadline = 45 * time.Second
	retryPeriod   = 30 * time.Second
)

var (
	flags struct {
		kubeconfig string
		clusterID  string
		version    bool
	}
)

func init() {
	flag.StringVar(&flags.kubeconfig, "kubeconfig", "", "Kubeconfig file to access a remote cluster. Warning: For testing only, do not use in production.")
	flag.BoolVar(&flags.version, "version", false, "Print the version")
	flag.Parse()
}

func main() {
	flag.Set("logtostderr", "true")
	flag.Parse()

	if flags.version {
		fmt.Println(version.String)
		return
	}

	glog.Infof(version.String)

	config, err := loadClientConfig(flags.kubeconfig)
	if err != nil {
		glog.Fatalf("Failed to load config for REST client: %v", err)
	}

	leaderelection.RunOrDie(leaderelection.LeaderElectionConfig{
		Lock:          createResourceLock(config),
		LeaseDuration: leaseDuration,
		RenewDeadline: renewDeadline,
		RetryPeriod:   retryPeriod,
		Callbacks: leaderelection.LeaderCallbacks{
			OnStartedLeading: startedLeading(config),
			OnStoppedLeading: stoppedLeading,
		},
	})
}

func startedLeading(config *rest.Config) func(<-chan struct{}) {
	return func(stopCh <-chan struct{}) {
		cvo.StartWorkers(stopCh, config)
	}
}

func stoppedLeading() {
	glog.Infof("Stopped leadership, exiting...")
	os.Exit(0)
}

func loadClientConfig(kubeconfig string) (*rest.Config, error) {
	if kubeconfig != "" {
		glog.V(4).Infof("Loading kube client config from path %q", kubeconfig)
		return clientcmd.BuildConfigFromFlags("", kubeconfig)
	}
	glog.V(4).Infof("Using in-cluster kube client config")
	return rest.InClusterConfig()
}

func createResourceLock(config *rest.Config) resourcelock.Interface {
	recorder := record.
		NewBroadcaster().
		NewRecorder(runtime.NewScheme(), v1.EventSource{Component: componentName})

	leaderElectionClient, err := kubernetes.NewForConfig(rest.AddUserAgent(config, "leader-election"))
	if err != nil {
		glog.Fatalf("Failed to create leader-election client: %v", err)
	}

	id := os.Getenv("POD_NAME")
	if id == "" {
		glog.Fatalf("Failed to find POD_NAME in environment")
	}

	return &resourcelock.ConfigMapLock{
		ConfigMapMeta: metav1.ObjectMeta{
			Namespace: "kube-system",
			Name:      componentName,
		},
		Client: leaderElectionClient.CoreV1(),
		LockConfig: resourcelock.ResourceLockConfig{
			Identity:      id,
			EventRecorder: recorder,
		},
	}
}

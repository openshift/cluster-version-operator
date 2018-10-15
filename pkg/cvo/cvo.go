package cvo

import (
	"fmt"
	"time"

	"github.com/golang/glog"
	"github.com/google/uuid"
	corev1 "k8s.io/api/core/v1"
	apiextclientset "k8s.io/apiextensions-apiserver/pkg/client/clientset/clientset"
	apiextinformersv1beta1 "k8s.io/apiextensions-apiserver/pkg/client/informers/externalversions/apiextensions/v1beta1"
	apiextlistersv1beta1 "k8s.io/apiextensions-apiserver/pkg/client/listers/apiextensions/v1beta1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/apimachinery/pkg/util/wait"
	appsinformersv1 "k8s.io/client-go/informers/apps/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/scheme"
	coreclientsetv1 "k8s.io/client-go/kubernetes/typed/core/v1"
	appslisterv1 "k8s.io/client-go/listers/apps/v1"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/tools/record"
	"k8s.io/client-go/util/workqueue"

	"github.com/openshift/cluster-version-operator/lib/resourceapply"
	cvv1 "github.com/openshift/cluster-version-operator/pkg/apis/clusterversion.openshift.io/v1"
	clientset "github.com/openshift/cluster-version-operator/pkg/generated/clientset/versioned"
	cvinformersv1 "github.com/openshift/cluster-version-operator/pkg/generated/informers/externalversions/clusterversion.openshift.io/v1"
	osinformersv1 "github.com/openshift/cluster-version-operator/pkg/generated/informers/externalversions/operatorstatus.openshift.io/v1"
	cvlistersv1 "github.com/openshift/cluster-version-operator/pkg/generated/listers/clusterversion.openshift.io/v1"
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
var ownerKind = cvv1.SchemeGroupVersion.WithKind("CVOConfig")

// Operator defines cluster version operator.
type Operator struct {
	// nodename allows CVO to sync fetchPayload to same node as itself.
	nodename string
	// namespace and name are used to find the CVOConfig, OperatorStatus.
	namespace, name string
	// releaseImage allows templating CVO deployment manifest.
	releaseImage string

	// restConfig is used to create resourcebuilder.
	restConfig *rest.Config

	client        clientset.Interface
	kubeClient    kubernetes.Interface
	apiExtClient  apiextclientset.Interface
	eventRecorder record.EventRecorder

	syncHandler func(key string) error

	clusterOperatorLister oslistersv1.ClusterOperatorLister

	crdLister             apiextlistersv1beta1.CustomResourceDefinitionLister
	deployLister          appslisterv1.DeploymentLister
	cvoConfigLister       cvlistersv1.CVOConfigLister
	crdListerSynced       cache.InformerSynced
	deployListerSynced    cache.InformerSynced
	cvoConfigListerSynced cache.InformerSynced

	// queue only ever has one item, but it has nice error handling backoff/retry semantics
	queue workqueue.RateLimitingInterface
}

// New returns a new cluster version operator.
func New(
	nodename string,
	namespace, name string,
	releaseImage string,
	cvoConfigInformer cvinformersv1.CVOConfigInformer,
	clusterOperatorInformer osinformersv1.ClusterOperatorInformer,
	crdInformer apiextinformersv1beta1.CustomResourceDefinitionInformer,
	deployInformer appsinformersv1.DeploymentInformer,
	restConfig *rest.Config,
	client clientset.Interface,
	kubeClient kubernetes.Interface,
	apiExtClient apiextclientset.Interface,
) *Operator {
	eventBroadcaster := record.NewBroadcaster()
	eventBroadcaster.StartLogging(glog.Infof)
	eventBroadcaster.StartRecordingToSink(&coreclientsetv1.EventSinkImpl{Interface: kubeClient.CoreV1().Events("")})

	optr := &Operator{
		nodename:      nodename,
		namespace:     namespace,
		name:          name,
		releaseImage:  releaseImage,
		restConfig:    restConfig,
		client:        client,
		kubeClient:    kubeClient,
		apiExtClient:  apiExtClient,
		eventRecorder: eventBroadcaster.NewRecorder(scheme.Scheme, corev1.EventSource{Component: "clusterversionoperator"}),
		queue:         workqueue.NewNamedRateLimitingQueue(workqueue.DefaultControllerRateLimiter(), "clusterversionoperator"),
	}

	cvoConfigInformer.Informer().AddEventHandler(optr.eventHandler())
	crdInformer.Informer().AddEventHandler(optr.eventHandler())

	optr.syncHandler = optr.sync

	optr.clusterOperatorLister = clusterOperatorInformer.Lister()

	optr.crdLister = crdInformer.Lister()
	optr.crdListerSynced = crdInformer.Informer().HasSynced
	optr.deployLister = deployInformer.Lister()
	optr.deployListerSynced = deployInformer.Informer().HasSynced
	optr.cvoConfigLister = cvoConfigInformer.Lister()
	optr.cvoConfigListerSynced = cvoConfigInformer.Informer().HasSynced

	return optr
}

// Run runs the cluster version operator.
func (optr *Operator) Run(workers int, stopCh <-chan struct{}) {
	defer utilruntime.HandleCrash()
	defer optr.queue.ShutDown()

	glog.Info("Starting ClusterVersionOperator")
	defer glog.Info("Shutting down ClusterVersionOperator")

	if !cache.WaitForCacheSync(stopCh,
		optr.crdListerSynced,
		optr.deployListerSynced,
		optr.cvoConfigListerSynced,
	) {
		return
	}

	for i := 0; i < workers; i++ {
		go wait.Until(optr.worker, time.Second, stopCh)
	}

	<-stopCh
}

func (optr *Operator) eventHandler() cache.ResourceEventHandler {
	workQueueKey := fmt.Sprintf("%s/%s", optr.namespace, optr.name)
	return cache.ResourceEventHandlerFuncs{
		AddFunc:    func(obj interface{}) { optr.queue.Add(workQueueKey) },
		UpdateFunc: func(old, new interface{}) { optr.queue.Add(workQueueKey) },
		DeleteFunc: func(obj interface{}) { optr.queue.Add(workQueueKey) },
	}
}

func (optr *Operator) worker() {
	for optr.processNextWorkItem() {
	}
}

func (optr *Operator) processNextWorkItem() bool {
	key, quit := optr.queue.Get()
	if quit {
		return false
	}
	defer optr.queue.Done(key)

	err := optr.syncHandler(key.(string))
	optr.handleErr(err, key)

	return true
}

func (optr *Operator) handleErr(err error, key interface{}) {
	if err == nil {
		optr.queue.Forget(key)
		return
	}

	if optr.queue.NumRequeues(key) < maxRetries {
		glog.V(2).Infof("Error syncing operator %v: %v", key, err)
		optr.queue.AddRateLimited(key)
		return
	}

	err = optr.syncDegradedStatus(err)
	utilruntime.HandleError(err)
	glog.V(2).Infof("Dropping operator %q out of the queue: %v", key, err)
	optr.queue.Forget(key)
}

func (optr *Operator) sync(key string) error {
	startTime := time.Now()
	glog.V(4).Infof("Started syncing operator %q (%v)", key, startTime)
	defer func() {
		glog.V(4).Infof("Finished syncing operator %q (%v)", key, time.Since(startTime))
	}()

	// We always run this to make sure CVOConfig can be synced.
	if err := optr.syncCustomResourceDefinitions(); err != nil {
		return err
	}

	namespace, name, err := cache.SplitMetaNamespaceKey(key)
	if err != nil {
		return err
	}

	var obj *cvv1.CVOConfig
	obj, err = optr.cvoConfigLister.CVOConfigs(namespace).Get(name)
	if apierrors.IsNotFound(err) {
		obj, err = optr.getConfig()
	}
	if err != nil {
		return err
	}

	config := &cvv1.CVOConfig{}
	obj.DeepCopyInto(config)

	if err := optr.syncProgressingStatus(config); err != nil {
		return err
	}

	payloadDir, err := optr.updatePayloadDir(config)
	if err != nil {
		return err
	}
	releaseImage := optr.releaseImage
	if config.DesiredUpdate.Payload != "" {
		releaseImage = config.DesiredUpdate.Payload
	}
	payload, err := loadUpdatePayload(payloadDir, releaseImage)
	if err != nil {
		return err
	}

	if err := optr.syncUpdatePayload(config, payload); err != nil {
		return err
	}

	return optr.syncAvailableStatus(config)
}

func (optr *Operator) getConfig() (*cvv1.CVOConfig, error) {
	upstream := cvv1.URL("http://localhost:8080/graph")
	channel := "fast"
	id, _ := uuid.NewRandom()
	if id.Variant() != uuid.RFC4122 {
		return nil, fmt.Errorf("invalid %q, must be an RFC4122-variant UUID: found %s", id, id.Variant())
	}
	if id.Version() != 4 {
		return nil, fmt.Errorf("Invalid %q, must be a version-4 UUID: found %s", id, id.Version())
	}

	// XXX: generate CVOConfig from options calculated above.
	config := &cvv1.CVOConfig{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: optr.namespace,
			Name:      optr.name,
		},
		Upstream:  upstream,
		Channel:   channel,
		ClusterID: cvv1.ClusterID(id.String()),
	}

	actual, _, err := resourceapply.ApplyCVOConfigFromCache(optr.cvoConfigLister, optr.client.ClusterversionV1(), config)
	return actual, err
}

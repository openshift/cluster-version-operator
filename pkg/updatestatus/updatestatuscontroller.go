package updatestatus

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/google/go-cmp/cmp"
	"gopkg.in/yaml.v3"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/klog/v2"

	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"

	kubeinformers "k8s.io/client-go/informers"
	kubeclient "k8s.io/client-go/kubernetes"
	corev1client "k8s.io/client-go/kubernetes/typed/core/v1"

	"github.com/openshift/library-go/pkg/controller/factory"
	"github.com/openshift/library-go/pkg/operator/events"
)

// informerMsg is the communication structure between informers and the update status controller. It contains the UID of
// the insight and the insight itself, serialized as YAML. Passing serialized avoids shared data access problems. Until
// we have the Status API we need to serialize ourselves anyway.
type informerMsg struct {
	informer string
	// knownInsights contains the UIDs of insights known by the informer, so the controller can remove insights formerly
	// reported by the informer but no longer known to it (e.g. because the informer was restarted and the culprit
	// condition ceased to exist in the meantime). The `uid` of the insight in the message payload is always assumed
	// to be known, and is not required to be included in `knownInsights` by the informers (but informers can do so).
	knownInsights []string

	uid     string
	insight []byte
}

func makeControlPlaneInsightMsg(insight ControlPlaneInsight, informer string) (informerMsg, error) {
	rawInsight, err := yaml.Marshal(insight)
	if err != nil {
		return informerMsg{}, err
	}
	msg := informerMsg{
		informer: informer,
		uid:      insight.UID,
		insight:  rawInsight,
	}
	return msg, msg.validate()
}

func makeWorkerPoolsInsightMsg(insight WorkerPoolInsight, informer string) (informerMsg, error) {
	rawInsight, err := yaml.Marshal(insight)
	if err != nil {
		return informerMsg{}, err
	}
	msg := informerMsg{
		informer: informer,
		uid:      insight.UID,
		insight:  rawInsight,
	}
	return msg, msg.validate()
}

type sendInsightFn func(insight informerMsg)

func isStatusInsightKey(k string) bool {
	return strings.HasPrefix(k, "usc.")
}

// updateStatusController is a controller that collects insights from informers and maintains a ConfigMap with the insights
// until we have a proper UpdateStatus API. The controller maintains an internal desired content of the ConfigMap (even
// if it does not exist in the cluster) and updates it in the cluster when new insights are received, or when the ConfigMap
// changes in the cluster. The controller only maintains the ConfigMap in the cluster if it exists, it does not create it
// itself (this serves as a simple opt-in mechanism).
//
// The communication between informers (insight producers) and this controller is performed via a channel. The controller
// constructor returns a sendInsightFn function to be used by other controllers to send insights to this controller. The
// informerMsg structure is the data transfer object.
//
// updateStatusController is set up to spawn the insight receiver after it is started. The receiver reads messages from
// the channel, updates the internal state of the controller, and queues the ConfigMap to be updated in the cluster. The
// sendInsightFn function can be used to send insights to the controller even before the insight receiver is started,
// but the buffered channel has limited capacity so senders can block eventually.
//
// NOTE: The communication mechanism was added in the initial scaffolding PR and does not aspire to be the final
// and 100% efficient solution. Feel free to improve or even replace it if turns out to be unsuitable in practice.
type updateStatusController struct {
	configMaps corev1client.ConfigMapInterface

	// statusApi is the desired state of the status API ConfigMap. It is updated when new insights are received.
	// Any access to the struct should be done with the lock held.
	statusApi struct {
		sync.Mutex
		cm *corev1.ConfigMap

		// processed is the number of insights processed, used for testing
		processed int
	}

	recorder events.Recorder
}

// newUpdateStatusController creates a new update status controller and returns it. The second return value is a function
// the other controllers should use to send insights to this controller.
func newUpdateStatusController(
	coreClient kubeclient.Interface,
	coreInformers kubeinformers.SharedInformerFactory,
	recorder events.Recorder,
) (factory.Controller, sendInsightFn) {
	uscRecorder := recorder.WithComponentSuffix("update-status-controller")

	c := &updateStatusController{
		configMaps: coreClient.CoreV1().ConfigMaps(uscNamespace),
		recorder:   uscRecorder,
	}

	startInsightReceiver, sendInsight := c.setupInsightReceiver()

	cmInformer := coreInformers.Core().V1().ConfigMaps().Informer()
	controller := factory.New().
		// call sync every 5 minutes or on CM events in the openshift-cluster-version namespace
		WithSync(c.sync).ResyncEvery(5*time.Minute).
		WithFilteredEventsInformersQueueKeysFunc(cmNameKey, nsFilter(uscNamespace), cmInformer).
		WithPostStartHooks(startInsightReceiver).
		ToController("UpdateStatusController", c.recorder)

	return controller, sendInsight
}

func (m informerMsg) validate() error {
	switch {
	case m.informer == "":
		return fmt.Errorf("empty informer")
	case m.uid == "":
		return fmt.Errorf("empty uid")
	case len(m.insight) == 0:
		return fmt.Errorf("empty or nil insight")
	}

	return nil
}

// processInsightMsg validates the message and if valid, updates the status API with the included
// insight. Returns true if the message was valid and processed, false otherwise.
func (c *updateStatusController) processInsightMsg(message informerMsg) bool {
	c.statusApi.Lock()
	defer c.statusApi.Unlock()

	c.statusApi.processed++

	if err := message.validate(); err != nil {
		klog.Warningf("USC :: Collector :: Invalid message: %v", err)
		return false
	}
	klog.Infof("USC :: Collector :: Received insight from informer %q (uid=%s)", message.informer, message.uid)
	c.updateInsightInStatusApi(message)
	c.removeUnknownInsights(message)

	return true
}

// setupInsightReceiver creates a communication channel between informers and the update status controller, and returns
// two methods: one to start the insight receiver (to be used as a post start hook so it called after the controller is
// started), and one to be passed to informers to send insights to the controller.
func (c *updateStatusController) setupInsightReceiver() (factory.PostStartHook, sendInsightFn) {
	fromInformers := make(chan informerMsg, 100)

	startInsightReceiver := func(ctx context.Context, syncCtx factory.SyncContext) error {
		klog.V(2).Info("USC :: Collector :: Starting insight collector")
		for {
			select {
			case message := <-fromInformers:
				if c.processInsightMsg(message) {
					syncCtx.Queue().Add(statusApiConfigMap)
				}
			case <-ctx.Done():
				klog.Info("USC :: Collector :: Stopping insight collector")
				return nil
			}
		}
	}

	sendInsight := func(msg informerMsg) {
		fromInformers <- msg
	}

	return startInsightReceiver, sendInsight
}

// updateInsightInStatusApi updates the status API using the message.
// Assumes the statusApi field is locked.
func (c *updateStatusController) updateInsightInStatusApi(msg informerMsg) {
	if c.statusApi.cm == nil {
		c.statusApi.cm = &corev1.ConfigMap{Data: map[string]string{}}
	}

	// Assemble the key because data is flattened in CM compared to UpdateStatus API where we would have a separate
	// container with insights for each informer
	cmKey := fmt.Sprintf("usc.%s.%s", msg.informer, msg.uid)

	var oldContent string
	if klog.V(4).Enabled() {
		oldContent = c.statusApi.cm.Data[cmKey]
	}

	updatedContent := string(msg.insight)

	c.statusApi.cm.Data[cmKey] = updatedContent

	klog.V(2).Infof("USC :: Collector :: Updated insight in status API (uid=%s)", msg.uid)
	if klog.V(4).Enabled() {
		if diff := cmp.Diff(oldContent, updatedContent); diff != "" {
			klog.Infof("USC :: Collector :: Insight (uid=%s) diff:\n%s", msg.uid, diff)
		} else {
			klog.Infof("USC :: Collector :: Insight (uid=%s) content did not change (len=%d)", msg.uid, len(updatedContent))
		}
	}
}

// removeUnknownInsights removes insights from the status API that are no longer reported as known to the informer
// that originally reported them.
// Assumes the statusApi field is locked.
func (c *updateStatusController) removeUnknownInsights(message informerMsg) {
	known := sets.New(message.knownInsights...)
	known.Insert(message.uid)
	informerPrefix := fmt.Sprintf("usc.%s.", message.informer)
	for key := range c.statusApi.cm.Data {
		if strings.HasPrefix(key, informerPrefix) && !known.Has(strings.TrimPrefix(key, informerPrefix)) {
			delete(c.statusApi.cm.Data, key)
			klog.V(2).Infof("USC :: Collector :: Dropped insight %q because it is no longer reported as known by informer %q", key, message.informer)
		}
	}
}

func (c *updateStatusController) commitStatusApiAsConfigMap(ctx context.Context) error {
	// Check whether the CM exists and do nothing if it does not exist; we never create it, only update
	clusterCm, err := c.configMaps.Get(ctx, statusApiConfigMap, metav1.GetOptions{})
	if err != nil {
		if errors.IsNotFound(err) {
			klog.V(2).Info("USC :: Status API CM does not exist -> nothing to update")
			return nil
		}
		klog.Errorf("USC :: Failed to get status API CM: %v", err)
		return err
	}

	c.statusApi.Lock()
	defer c.statusApi.Unlock()

	if c.statusApi.cm == nil {
		// This means we are running on a CM event before first insight arrived, otherwise internal state would exist
		klog.V(2).Infof("USC :: No internal state known yet, setting internal state to cluster state")
		c.statusApi.cm = clusterCm.DeepCopy()
		return nil
	}

	// We have internal state, so we need to overwrite the cluster state with our internal state but keep items that we do
	// not care about
	mergedCm := clusterCm.DeepCopy()
	for k := range mergedCm.Data {
		if isStatusInsightKey(k) {
			delete(mergedCm.Data, k)
		}
	}

	for k, v := range c.statusApi.cm.Data {
		if mergedCm.Data == nil {
			mergedCm.Data = map[string]string{}
		}
		mergedCm.Data[k] = v
	}

	klog.V(2).Infof("USC :: Updating status API CM (%d insights)", len(c.statusApi.cm.Data))
	c.statusApi.cm = mergedCm

	_, err = c.configMaps.Update(ctx, c.statusApi.cm, metav1.UpdateOptions{})
	return err
}

func (c *updateStatusController) sync(ctx context.Context, syncCtx factory.SyncContext) error {
	queueKey := syncCtx.QueueKey()
	if queueKey == "" {
		klog.V(2).Info("USC :: Periodic resync")
		queueKey = statusApiConfigMap
	}
	if queueKey != statusApiConfigMap {
		// We only care about the status API CM
		return nil
	}

	klog.V(2).Infof("USC :: Syncing status API CM (name=%s)", queueKey)
	return c.commitStatusApiAsConfigMap(ctx)
}

const statusApiConfigMap = "status-api-cm-prototype"

func cmNameKey(object runtime.Object) []string {
	if object == nil {
		return nil
	}

	switch o := object.(type) {
	case *corev1.ConfigMap:
		return []string{o.Name}
	}

	klog.Fatalf("USC :: Unknown object type: %T", object)
	return nil
}

func nsFilter(namespace string) factory.EventFilterFunc {
	return func(obj interface{}) bool {
		if obj == nil {
			return false
		}
		switch o := obj.(type) {
		case *corev1.ConfigMap:
			return o.Namespace == namespace
		}
		return false
	}
}

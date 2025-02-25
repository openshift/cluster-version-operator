package updatestatus

import (
	"context"
	"fmt"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/klog/v2"

	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"

	kubeinformers "k8s.io/client-go/informers"
	kubeclient "k8s.io/client-go/kubernetes"
	corev1client "k8s.io/client-go/kubernetes/typed/core/v1"

	"github.com/openshift/library-go/pkg/controller/factory"
	"github.com/openshift/library-go/pkg/operator/events"

	updatestatus "github.com/openshift/api/update/v1alpha1"
)

const (
	unknownInsightGracePeriod = 60 * time.Second
)

// High-level description of the informers -> USC communication protocol:
// ----------------------------------------------------------------------
// Informers send insights to the USC via messages. Communication is performed via a channel (but that is just the
// current implementation detail) and the data sent by informers is encapsulated in the informerMsg structure. The
// communication is unidirectional, from informers to the USC. The USC does not send any messages back to the informers.
//
// The informers send individual insights they want to propagate to the Status API, insights are identified by a UID.
// Insights with the same UID are considered the same insight in the context of the informer that sent it. The received
// insights are stored in the Status API by the USC if they are new, and updated with the new data if they are already
// present.
//
// Informers keep track of active insights, and include a list of all known insights (just the UIDs) in each message.
// On each message, USC compares the insights by the informer it has in the Status API with the list of known insights
// in the message, and when an insight is first not reported as known by the informer, it is marked for expiration. If
// the informer reports the insight as known again before it expires, the expiration is cancelled. If the insight is not
// reported as known again within a grace period, it is dropped from the Status API. This allows informers to restart
// and "learn" about conditions in the cluster without dropping insights that it have not yet learned about while
// still eventually dropping insights that are no longer detected.
//
// Informers can also report insights they want to explicitly drop. This works similarly to the expiration mechanism,
// but there is no grace period.
//
// TL;DR:
// --------
// Whenever an informer has an insight to report, it sends a message containing:
// - The informer's name
// - The insight itself, identified by a UID
// - The list of all insights it knows about (just the UIDs)
// - The list of all insights it wants to explicitly drop (just the UIDs)
//
// For each message received, the USC:
// - Updates the Status API with the insight
// - Marks insights by the informer already in Status API for expiration if informer does not know them
// - Drops insights marked for expiration it grace period is over and informer does not still know them
// - Unmarks the expiration for each insight the informer knows
// - Drops insights explicitly requested by the informer
//
// Implementation status:
// ---------------------
// - [x] USC-side known insight tracking
// - [x] USC-side insight expiration
// - [ ] Informer-side known insight tracking
// - [ ] Informer-side populating known insights in messages
// - [ ] USC-side insight explicit dropping
// - [ ] Informer-side explicit insight drop tracking
// - [ ] Informer-side populating explicit drop insights in messages

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

func makeControlPlaneInsightMsg(insight updatestatus.ControlPlaneInsight, informer string) (informerMsg, error) {
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

func makeWorkerPoolsInsightMsg(insight updatestatus.WorkerPoolInsight, informer string) (informerMsg, error) {
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

// insightExpirations is UID -> expiration time map
type insightExpirations map[string]time.Time

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

	state updateStatusApi

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
		state:      updateStatusApi{now: time.Now},
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
				if c.state.processInsightMsg(message) {
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

	cm := c.state.sync(clusterCm)

	_, err = c.configMaps.Update(ctx, cm, metav1.UpdateOptions{})
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

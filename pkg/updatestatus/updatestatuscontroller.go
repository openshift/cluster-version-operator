package updatestatus

import (
	"context"
	"fmt"
	"time"

	"k8s.io/klog/v2"

	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"

	"github.com/openshift/library-go/pkg/controller/factory"
	"github.com/openshift/library-go/pkg/operator/events"

	updatestatus "github.com/openshift/api/update/v1alpha1"
	updateclient "github.com/openshift/client-go/update/clientset/versioned"
	updatev1alpha1 "github.com/openshift/client-go/update/clientset/versioned/typed/update/v1alpha1"
	updateinformers "github.com/openshift/client-go/update/informers/externalversions"
)

const (
	unknownInsightGracePeriod = 60 * time.Second
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

	uid string

	cpInsight *updatestatus.ControlPlaneInsight
	wpInsight *updatestatus.WorkerPoolInsight
}

func makeControlPlaneInsightMsg(insight updatestatus.ControlPlaneInsight, informer string) (informerMsg, error) {
	msg := informerMsg{
		informer:  informer,
		uid:       insight.UID,
		cpInsight: insight.DeepCopy(),
	}
	return msg, msg.validate()
}

func makeWorkerPoolsInsightMsg(insight updatestatus.WorkerPoolInsight, informer string) (informerMsg, error) {
	msg := informerMsg{
		informer:  informer,
		uid:       insight.UID,
		wpInsight: insight.DeepCopy(),
	}
	return msg, msg.validate()
}

type sendInsightFn func(insight informerMsg)

// updateStatusController is a controller that collects insights from informers and maintains the UpdateStatus API.
// The controller maintains an internal desired content of the UpdateStatus instance (even if it does not exist in the
// cluster) and updates it in the cluster when new insights are received, or when the UpdateStatus changes
// in the cluster. The controller only maintains the UpdateStatus in the cluster if it exists, it does not create it
// itself (this serves as a simple opt-in mechanism).
//
// The communication between informers (insight producers) and this controller is performed via a channel. The controller
// constructor returns a sendInsightFn function to be used by other controllers to send insights to this controller. The
// informerMsg structure is the data transfer object.
//
// updateStatusController is set up to spawn the insight receiver after it is started. The receiver reads messages from
// the channel, updates the internal state of the controller, and queues the UpdateStatus to be updated in the cluster.
// The sendInsightFn function can be used to send insights to the controller even before the insight receiver starts,
// but the buffered channel has limited capacity so senders can block eventually.
//
// NOTE: The communication mechanism was added in the initial scaffolding PR and does not aspire to be the final
// and 100% efficient solution. Feel free to improve or even replace it if turns out to be unsuitable in practice.
type updateStatusController struct {
	updateStatuses updatev1alpha1.UpdateStatusInterface

	state updateStatusApi

	recorder events.Recorder
}

// newUpdateStatusController creates a new update status controller and returns it. The second return value is a function
// the other controllers should use to send insights to this controller.
func newUpdateStatusController(
	updateClient updateclient.Interface,
	updateInformers updateinformers.SharedInformerFactory,
	recorder events.Recorder,
) (factory.Controller, sendInsightFn) {
	uscRecorder := recorder.WithComponentSuffix("update-status-controller")

	c := &updateStatusController{
		updateStatuses: updateClient.UpdateV1alpha1().UpdateStatuses(),
		recorder:       uscRecorder,
		state:          updateStatusApi{now: time.Now},
	}

	startInsightReceiver, sendInsight := c.setupInsightReceiver()

	usInformer := updateInformers.Update().V1alpha1().UpdateStatuses().Informer()
	controller := factory.New().
		// call sync every 5 minutes or on CM events in the openshift-cluster-version namespace
		WithSync(c.sync).ResyncEvery(5*time.Minute).
		WithInformersQueueKeysFunc(queueKey, usInformer).
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
	case m.cpInsight == nil && m.wpInsight == nil:
		return fmt.Errorf("empty insight")
	case m.cpInsight != nil && m.wpInsight != nil:
		return fmt.Errorf("both control plane and worker pool insights set")
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
					syncCtx.Queue().Add(updateStatusResource)
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
	// TODO: We need to change this to:
	//   (1) On startup, load existing API and only then start receiving insights
	//   (2) If the API does not exist on startup, create it
	// Check whether the CM exists and do nothing if it does not exist; we never create it, only update
	clusterUpdateStatus, err := c.updateStatuses.Get(ctx, updateStatusResource, metav1.GetOptions{})
	if err != nil {
		if errors.IsNotFound(err) {
			klog.V(2).Info("USC :: Status API CM does not exist -> nothing to update")
			return nil
		}
		klog.Errorf("USC :: Failed to get status API CM: %v", err)
		return err
	}

	updateStatus := c.state.sync(clusterUpdateStatus)

	_, err = c.updateStatuses.Update(ctx, updateStatus, metav1.UpdateOptions{})
	return err
}

func (c *updateStatusController) sync(ctx context.Context, syncCtx factory.SyncContext) error {
	queueKey := syncCtx.QueueKey()
	if queueKey == "" {
		klog.V(2).Info("USC :: Periodic resync")
		queueKey = updateStatusResource
	}
	if queueKey != updateStatusResource {
		// We only care about the single status API resource
		return nil
	}

	klog.V(2).Infof("USC :: Syncing status API CM (name=%s)", queueKey)
	return c.commitStatusApiAsConfigMap(ctx)
}

const updateStatusResource = "status-api-prototype"

func queueKey(object runtime.Object) []string {
	if object == nil {
		return nil
	}

	switch o := object.(type) {
	case *updatestatus.UpdateStatus:
		return []string{o.Name}
	}

	klog.Fatalf("USC :: Unknown object type: %T", object)
	return nil
}

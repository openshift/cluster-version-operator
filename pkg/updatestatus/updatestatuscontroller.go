package updatestatus

import (
	"context"
	"fmt"
	"sync"
	"time"

	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/klog/v2"

	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"

	"github.com/openshift/library-go/pkg/controller/factory"
	"github.com/openshift/library-go/pkg/operator/events"

	updatestatus "github.com/openshift/api/update/v1alpha1"
	updateclient "github.com/openshift/client-go/update/clientset/versioned/typed/update/v1alpha1"
	updateinformers "github.com/openshift/client-go/update/informers/externalversions"
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
	updateStatuses updateclient.UpdateStatusInterface
	statusApi      statusApi
	recorder       events.Recorder
}

// statusApi is the desired state of the UpdateStatus API. It is updated when new insights are received.
// Any access to the struct should be done with the lock held.
type statusApi struct {
	sync.Mutex

	// our is the desired state of the UpdateStatus API. It is updated when new insights are received.
	us *updatestatus.UpdateStatus

	// processed is the number of insights processed, used for testing
	processed int
}

// newUpdateStatusController creates a new update status controller and returns it. The second return value is a function
// the other controllers should use to send insights to this controller.
func newUpdateStatusController(
	updateClient updateclient.UpdateV1alpha1Interface,
	updateInformers updateinformers.SharedInformerFactory,
	recorder events.Recorder,
) (factory.Controller, sendInsightFn) {
	uscRecorder := recorder.WithComponentSuffix("update-status-controller")

	c := &updateStatusController{
		updateStatuses: updateClient.UpdateStatuses(),
		recorder:       uscRecorder,
	}

	startInsightReceiver, sendInsight := c.setupInsightReceiver()

	usInformer := updateInformers.Update().V1alpha1().UpdateStatuses().Informer()
	controller := factory.New().
		// call sync every 5 minutes or on events on the status API
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

// ensureUpdateStatusExists ensures that the internal state of the status API is initialized.
// Assumes statusApi is locked.
func (c *statusApi) ensureUpdateStatusExists() {
	if c.us != nil {
		return
	}

	c.us = &updatestatus.UpdateStatus{
		ObjectMeta: metav1.ObjectMeta{
			Name: updateStatusResource,
		},
	}
}

func ensureControlPlaneInformer(cp *updatestatus.ControlPlane, informer string) *updatestatus.ControlPlaneInformer {
	for i := range cp.Informers {
		if cp.Informers[i].Name == informer {
			return &cp.Informers[i]
		}
	}

	cp.Informers = append(cp.Informers, updatestatus.ControlPlaneInformer{Name: informer})
	return &cp.Informers[len(cp.Informers)-1]
}

func ensureWorkerPoolInformer(wp *updatestatus.Pool, informerName string) *updatestatus.WorkerPoolInformer {
	for i := range wp.Informers {
		if wp.Informers[i].Name == informerName {
			return &wp.Informers[i]
		}
	}

	wp.Informers = append(wp.Informers, updatestatus.WorkerPoolInformer{Name: informerName})
	return &wp.Informers[len(wp.Informers)-1]
}

func ensureControlPlaneInsightByInformer(informer *updatestatus.ControlPlaneInformer, insight *updatestatus.ControlPlaneInsight) {
	for i := range informer.Insights {
		if informer.Insights[i].UID == insight.UID {
			informer.Insights[i].AcquiredAt = insight.AcquiredAt
			insight.ControlPlaneInsightUnion.DeepCopyInto(&informer.Insights[i].ControlPlaneInsightUnion)
			return
		}
	}

	informer.Insights = append(informer.Insights, *insight.DeepCopy())
}

func ensureWorkerPoolInsightByInformer(informer *updatestatus.WorkerPoolInformer, insight *updatestatus.WorkerPoolInsight) {
	for i := range informer.Insights {
		if informer.Insights[i].UID == insight.UID {
			informer.Insights[i].AcquiredAt = insight.AcquiredAt
			insight.WorkerPoolInsightUnion.DeepCopyInto(&informer.Insights[i].WorkerPoolInsightUnion)
			return
		}
	}

	informer.Insights = append(informer.Insights, *insight.DeepCopy())
}

// updateControlPlaneInsight updates the status API with the control plane insight.
// Assumes statusApi is locked.
func (c *statusApi) updateControlPlaneInsight(informerName string, insight *updatestatus.ControlPlaneInsight) {
	// TODO: Logging - log(2) that we do something, and log(4) the diff
	cp := &c.us.Status.ControlPlane
	switch insight.Type {
	case updatestatus.ClusterVersionStatusInsightType:
		cp.Resource = insight.ClusterVersionStatusInsight.Resource
	case updatestatus.MachineConfigPoolStatusInsightType:
		cp.PoolResource = insight.MachineConfigPoolStatusInsight.Resource.DeepCopy()
	}

	informer := ensureControlPlaneInformer(cp, informerName)
	ensureControlPlaneInsightByInformer(informer, insight)
}

func determineWorkerPool(insight *updatestatus.WorkerPoolInsight) updatestatus.PoolResourceRef {
	switch insight.Type {
	case updatestatus.MachineConfigPoolStatusInsightType:
		return insight.MachineConfigPoolStatusInsight.Resource
	case updatestatus.NodeStatusInsightType:
		return insight.NodeStatusInsight.PoolResource
	case updatestatus.HealthInsightType:
		// TODO: How to map a generic health insight to a worker pool?
		panic("not implemented yet")
	}

	panic(fmt.Sprintf("unknown insight type %q", insight.Type))
}

func (c *statusApi) updateWorkerPoolInsight(informerName string, insight *updatestatus.WorkerPoolInsight) {
	// TODO: Logging - log(2) that we do something, and log(4) the diff
	poolRef := determineWorkerPool(insight)

	// TODO: This should be a ControlPlaneInsight
	if poolRef.Name == "master" {
		c.updateControlPlaneInsight(informerName, &updatestatus.ControlPlaneInsight{
			UID:        insight.UID,
			AcquiredAt: insight.AcquiredAt,
			ControlPlaneInsightUnion: updatestatus.ControlPlaneInsightUnion{
				Type:                           insight.Type,
				MachineConfigPoolStatusInsight: insight.MachineConfigPoolStatusInsight,
				NodeStatusInsight:              insight.NodeStatusInsight,
				HealthInsight:                  insight.HealthInsight,
			},
		})
		return
	}

	wp := c.ensureWorkerPool(poolRef)
	informer := ensureWorkerPoolInformer(wp, informerName)
	ensureWorkerPoolInsightByInformer(informer, insight)
}

func (c *statusApi) ensureWorkerPool(pool updatestatus.PoolResourceRef) *updatestatus.Pool {
	for i := range c.us.Status.WorkerPools {
		if c.us.Status.WorkerPools[i].Name == pool.Name {
			c.us.Status.WorkerPools[i].Resource = pool // TODO: Handle conflicts?
			return &c.us.Status.WorkerPools[i]
		}
	}
	c.us.Status.WorkerPools = append(c.us.Status.WorkerPools, updatestatus.Pool{
		Name:     pool.Name,
		Resource: pool,
	})

	return &c.us.Status.WorkerPools[len(c.us.Status.WorkerPools)-1]
}

func (c *statusApi) removeUnknownInsightsByInformer(informer string, known sets.Set[string]) {
	for i := range c.us.Status.ControlPlane.Informers {
		if c.us.Status.ControlPlane.Informers[i].Name == informer {
			insights := make([]updatestatus.ControlPlaneInsight, 0, len(c.us.Status.ControlPlane.Informers[i].Insights))
			for insight := range c.us.Status.ControlPlane.Informers[i].Insights {
				if known.Has(c.us.Status.ControlPlane.Informers[i].Insights[insight].UID) {
					insights = append(insights, c.us.Status.ControlPlane.Informers[i].Insights[insight])
				}
			}
			c.us.Status.ControlPlane.Informers[i].Insights = insights
		}
	}
}

// updateInsightInStatusApi updates the status API using the message.
// Assumes the statusApi field is locked.
func (c *updateStatusController) updateInsightInStatusApi(msg informerMsg) {
	c.statusApi.ensureUpdateStatusExists()

	if msg.cpInsight != nil {
		c.statusApi.updateControlPlaneInsight(msg.informer, msg.cpInsight)
	} else {
		c.statusApi.updateWorkerPoolInsight(msg.informer, msg.wpInsight)
	}
}

// removeUnknownInsights removes insights from the status API that are no longer reported as known to the informer
// that originally reported them.
// Assumes the statusApi field is locked.
func (c *updateStatusController) removeUnknownInsights(message informerMsg) {
	known := sets.New(message.knownInsights...)
	known.Insert(message.uid)

	c.statusApi.removeUnknownInsightsByInformer(message.informer, known)
}

func (c *updateStatusController) commitStatusApi(ctx context.Context) error {
	// TODO: We need to change this to:
	//   (1) On startup, load existing API and only then start receiving insights
	//   (2) If the API does not exist on startup, create it
	// Check whether the UpdateStatus exists and do nothing if it does not exist; we never create it, only update
	clusterUpdateStatus, err := c.updateStatuses.Get(ctx, updateStatusResource, metav1.GetOptions{})
	if err != nil {
		if errors.IsNotFound(err) {
			klog.V(2).Info("USC :: Status API does not exist -> nothing to update")
			return nil
		}
		klog.Errorf("USC :: Failed to get status API: %v", err)
		return err
	}

	c.statusApi.Lock()
	defer c.statusApi.Unlock()

	if c.statusApi.us == nil {
		// This means we are running on UpdateStatus event before first insight arrived, otherwise internal state would exist
		klog.V(2).Infof("USC :: No internal state known yet, setting internal state to cluster state")
		c.statusApi.us = clusterUpdateStatus.DeepCopy()
		return nil
	}

	_, err = c.updateStatuses.Update(ctx, c.statusApi.us, metav1.UpdateOptions{})
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

	klog.V(2).Infof("USC :: Syncing status API (name=%s)", queueKey)
	return c.commitStatusApi(ctx)
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

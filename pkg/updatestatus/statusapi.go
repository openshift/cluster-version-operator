package updatestatus

import (
	"fmt"
	"slices"
	"sync"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/klog/v2"

	updatev1alpha1 "github.com/openshift/api/update/v1alpha1"
)

// insightExpirations is UID -> expiration time map
type insightExpirations map[string]time.Time

// statusApi is the desired state of the status API ConfigMap. It is updated when new insights are received.
// Any access to the struct should be done with the lock held.
type updateStatusApi struct {
	sync.Mutex
	us *updatev1alpha1.UpdateStatus

	// unknownInsightExpirations is a map of informer -> map of UID -> expiration time. It is used to track insights
	// that were reported by informers but are no longer known to them. The API keeps unknown insights until they
	// expire. If an insight is reported as known again before it expires, it is removed from the map.
	// TODO (muller): Needs to periodically rebuilt to avoid leaking memory
	unknownInsightExpirations map[string]insightExpirations

	// processed is the number of insights processed, used for testing
	processed int

	// TODO: Get rid of this and use `clock.Clock` in all controllers, passed from start.go main function's
	// controllercmd.ControllerContext
	now func() time.Time
}

func (c *updateStatusApi) sort() {
	if c.us == nil {
		return
	}

	slices.SortFunc(c.us.Status.ControlPlane.Informers, func(a, b updatev1alpha1.ControlPlaneInformer) int {
		if a.Name < b.Name {
			return -1
		}
		if a.Name > b.Name {
			return 1
		}
		return 0
	})

	for i := range c.us.Status.ControlPlane.Informers {
		slices.SortFunc(c.us.Status.ControlPlane.Informers[i].Insights, func(a, b updatev1alpha1.ControlPlaneInsight) int {
			if a.UID < b.UID {
				return -1
			}
			if a.UID > b.UID {
				return 1
			}
			return 0
		})
	}

	for i := range c.us.Status.WorkerPools {
		slices.SortFunc(c.us.Status.WorkerPools[i].Informers, func(a, b updatev1alpha1.WorkerPoolInformer) int {
			if a.Name < b.Name {
				return -1
			}
			if a.Name > b.Name {
				return 1
			}
			return 0
		})

		for j := range c.us.Status.WorkerPools[i].Informers {
			slices.SortFunc(c.us.Status.WorkerPools[i].Informers[j].Insights, func(a, b updatev1alpha1.WorkerPoolInsight) int {
				if a.UID < b.UID {
					return -1
				}
				if a.UID > b.UID {
					return 1
				}
				return 0
			})
		}
	}
}

// processInsightMsg validates the message and if valid, updates the status API with the included
// insight. Returns true if the message was valid and processed, false otherwise.
func (c *updateStatusApi) processInsightMsg(message informerMsg) bool {
	c.Lock()
	defer c.Unlock()

	c.processed++

	if err := message.validate(); err != nil {
		klog.Warningf("USC :: Collector :: Invalid message: %v", err)
		return false
	}
	klog.Infof("USC :: Collector :: Received insight from informer %q (uid=%s)", message.informer, message.uid)
	c.updateInsightInStatusApi(message)
	c.removeUnknownInsights(message)

	return true
}

// updateInsightInStatusApi updates the status API using the message.
// Assumes the statusApi field is locked.
func (c *updateStatusApi) updateInsightInStatusApi(msg informerMsg) {
	c.ensureUpdateStatusExists()

	if msg.cpInsight != nil {
		c.updateControlPlaneInsight(msg.informer, msg.cpInsight)
	} else {
		c.updateWorkerPoolInsight(msg.informer, msg.wpInsight)
	}
}

// removeUnknownInsights removes insights from the status API that are no longer reported as known to the informer
// that originally reported them.
// Assumes the statusApi field is locked.
func (c *updateStatusApi) removeUnknownInsights(message informerMsg) {
	known := sets.New(message.knownInsights...)
	known.Insert(message.uid)

	c.handleUnknownInsightsByInformer(message.informer, known)
}

func (c *updateStatusApi) handleUnknownInsightsByInformer(informer string, known sets.Set[string]) {
	cpFilter := c.makeControlPlaneInsightFilter(informer, known)

	for i := range c.us.Status.ControlPlane.Informers {
		if c.us.Status.ControlPlane.Informers[i].Name == informer {
			c.us.Status.ControlPlane.Informers[i].Insights = cpFilter(c.us.Status.ControlPlane.Informers[i].Insights)
		}
	}

	wpFilter := c.makeWorkerPoolInsightFilter(informer, known)
	for pool := range c.us.Status.WorkerPools {
		for i := range c.us.Status.WorkerPools[pool].Informers {
			if c.us.Status.WorkerPools[pool].Informers[i].Name == informer {
				c.us.Status.WorkerPools[pool].Informers[i].Insights = wpFilter(c.us.Status.WorkerPools[pool].Informers[i].Insights)
			}
		}
	}

	if len(c.unknownInsightExpirations[informer]) == 0 {
		delete(c.unknownInsightExpirations, informer)
	}
	if len(c.unknownInsightExpirations) == 0 {
		c.unknownInsightExpirations = nil
	}
}

// // removeUnknownInsights removes insights from the status API that are no longer reported as known to the informer
// // that originally reported them. The insights are kept for a grace period after they are no longer reported as known
// // and eventually dropped if they are not reported as known again within that period.
// // Assumes the statusApi field is locked.
// func (c *updateStatusApi) removeUnknownInsights(message informerMsg) {
// 	known := sets.New(message.knownInsights...)
// 	known.Insert(message.uid)
// 	informerPrefix := fmt.Sprintf("usc.%s.", message.informer)
// 	for key := range c.cm.Data {
// 		if strings.HasPrefix(key, informerPrefix) {
// 			uid := strings.TrimPrefix(key, informerPrefix)
// 			c.handleInsightExpiration(message.informer, known.Has(uid), uid)
// 		}
// 	}
//
// 	if len(c.unknownInsightExpirations) > 0 && len(c.unknownInsightExpirations[message.informer]) == 0 {
// 		delete(c.unknownInsightExpirations, message.informer)
// 	}
// 	if len(c.unknownInsightExpirations) == 0 {
// 		c.unknownInsightExpirations = nil
// 	}
// }

type keepInsightFunc func(uid string) bool
type cpInsightFilter func(insights []updatev1alpha1.ControlPlaneInsight) []updatev1alpha1.ControlPlaneInsight
type wpInsightFilter func(insights []updatev1alpha1.WorkerPoolInsight) []updatev1alpha1.WorkerPoolInsight

func (c *updateStatusApi) makeKeep(informer string, known sets.Set[string]) keepInsightFunc {
	now := c.now()
	return func(uid string) bool {
		if known.Has(uid) {
			if c.unknownInsightExpirations != nil && c.unknownInsightExpirations[informer] != nil {
				delete(c.unknownInsightExpirations[informer], uid)
			}
			return true
		}

		logKeep := func(expire time.Time) {
			klog.V(2).Infof("USC :: Collector :: Keeping insight %q until %s after it is no longer reported as known by informer %q", uid, expire, informer)
		}

		expireIn := now.Add(unknownInsightGracePeriod)
		switch {
		// Two cases when we first consider an insight as unknown -> set expiration
		case c.unknownInsightExpirations == nil:
			c.unknownInsightExpirations = map[string]insightExpirations{informer: {uid: expireIn}}
			logKeep(expireIn)
			return true

		case c.unknownInsightExpirations[informer][uid].IsZero():
			c.unknownInsightExpirations[informer][uid] = expireIn
			logKeep(expireIn)
			return true

		// Already set for expiration but still in grace period -> keep insight
		case c.unknownInsightExpirations[informer][uid].After(now):
			logKeep(c.unknownInsightExpirations[informer][uid])
			return true
		}

		// Already set for expiration and grace period expired -> drop insight
		delete(c.unknownInsightExpirations[informer], uid)
		klog.V(2).Infof("USC :: Collector :: Dropped insight %q because it is no longer reported as known by informer %q", uid, informer)
		return false
	}
}

// makeExpirationFilter considers potential expiration of an insight present in the API based on whether the informer
// knows about it.
// If the informer knows about the insight, it is not dropped from the API and any previous expiration is cancelled.
// If the informer does not know about the insight then it is either set to expire in the future if no expiration is
// set yet, or the expiration is checked to see whether the insight should be dropped.
func (c *updateStatusApi) makeControlPlaneInsightFilter(informer string, known sets.Set[string]) cpInsightFilter {
	return func(insights []updatev1alpha1.ControlPlaneInsight) []updatev1alpha1.ControlPlaneInsight {
		keep := c.makeKeep(informer, known)
		filtered := make([]updatev1alpha1.ControlPlaneInsight, 0, len(insights))

		for i := range insights {
			if keep(insights[i].UID) {
				filtered = append(filtered, insights[i])
			}
		}

		if len(filtered) > 0 {
			return filtered
		}
		return nil
	}
}

func (c *updateStatusApi) makeWorkerPoolInsightFilter(informer string, known sets.Set[string]) wpInsightFilter {
	return func(insights []updatev1alpha1.WorkerPoolInsight) []updatev1alpha1.WorkerPoolInsight {
		keep := c.makeKeep(informer, known)
		filtered := make([]updatev1alpha1.WorkerPoolInsight, 0, len(insights))

		for i := range insights {
			if keep(insights[i].UID) {
				filtered = append(filtered, insights[i])
			}
		}

		if len(filtered) > 0 {
			return filtered
		}
		return nil
	}
}

func (c *updateStatusApi) sync(clusterState *updatev1alpha1.UpdateStatus) *updatev1alpha1.UpdateStatus {
	c.Lock()
	defer c.Unlock()

	if c.us == nil {
		// This means we are running on a CM event before first insight arrived, otherwise internal state would exist
		klog.V(2).Infof("USC :: No internal state known yet, setting internal state to cluster state")
		// c.cm = clusterState.DeepCopy()
		c.us = clusterState.DeepCopy()
	}

	return c.us.DeepCopy()
}

// ensureUpdateStatusExists ensures that the internal state of the status API is initialized.
// Assumes statusApi is locked.
func (c *updateStatusApi) ensureUpdateStatusExists() {
	if c.us != nil {
		return
	}

	c.us = &updatev1alpha1.UpdateStatus{
		ObjectMeta: metav1.ObjectMeta{
			Name: updateStatusResource,
		},
	}
}

func ensureControlPlaneInformer(cp *updatev1alpha1.ControlPlane, informer string) *updatev1alpha1.ControlPlaneInformer {
	for i := range cp.Informers {
		if cp.Informers[i].Name == informer {
			return &cp.Informers[i]
		}
	}

	cp.Informers = append(cp.Informers, updatev1alpha1.ControlPlaneInformer{Name: informer})
	return &cp.Informers[len(cp.Informers)-1]
}

func ensureWorkerPoolInformer(wp *updatev1alpha1.Pool, informerName string) *updatev1alpha1.WorkerPoolInformer {
	for i := range wp.Informers {
		if wp.Informers[i].Name == informerName {
			return &wp.Informers[i]
		}
	}

	wp.Informers = append(wp.Informers, updatev1alpha1.WorkerPoolInformer{Name: informerName})
	return &wp.Informers[len(wp.Informers)-1]
}

func ensureControlPlaneInsightByInformer(informer *updatev1alpha1.ControlPlaneInformer, insight *updatev1alpha1.ControlPlaneInsight) {
	for i := range informer.Insights {
		if informer.Insights[i].UID == insight.UID {
			informer.Insights[i].AcquiredAt = insight.AcquiredAt
			insight.ControlPlaneInsightUnion.DeepCopyInto(&informer.Insights[i].ControlPlaneInsightUnion)
			return
		}
	}

	informer.Insights = append(informer.Insights, *insight.DeepCopy())
}

func ensureWorkerPoolInsightByInformer(informer *updatev1alpha1.WorkerPoolInformer, insight *updatev1alpha1.WorkerPoolInsight) {
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
func (c *updateStatusApi) updateControlPlaneInsight(informerName string, insight *updatev1alpha1.ControlPlaneInsight) {
	// TODO: Logging - log(2) that we do something, and log(4) the diff
	cp := &c.us.Status.ControlPlane
	switch insight.Type {
	case updatev1alpha1.ClusterVersionStatusInsightType:
		cp.Resource = insight.ClusterVersionStatusInsight.Resource
	case updatev1alpha1.MachineConfigPoolStatusInsightType:
		cp.PoolResource = insight.MachineConfigPoolStatusInsight.Resource.DeepCopy()
	}

	informer := ensureControlPlaneInformer(cp, informerName)
	ensureControlPlaneInsightByInformer(informer, insight)
}

func determineWorkerPool(insight *updatev1alpha1.WorkerPoolInsight) updatev1alpha1.PoolResourceRef {
	switch insight.Type {
	case updatev1alpha1.MachineConfigPoolStatusInsightType:
		return insight.MachineConfigPoolStatusInsight.Resource
	case updatev1alpha1.NodeStatusInsightType:
		return insight.NodeStatusInsight.PoolResource
	case updatev1alpha1.HealthInsightType:
		// TODO: How to map a generic health insight to a worker pool?
		panic("not implemented yet")
	}

	panic(fmt.Sprintf("unknown insight type %q", insight.Type))
}

func (c *updateStatusApi) updateWorkerPoolInsight(informerName string, insight *updatev1alpha1.WorkerPoolInsight) {
	// TODO: Logging - log(2) that we do something, and log(4) the diff
	poolRef := determineWorkerPool(insight)

	// TODO: This should be a ControlPlaneInsight
	if poolRef.Name == "master" {
		c.updateControlPlaneInsight(informerName, &updatev1alpha1.ControlPlaneInsight{
			UID:        insight.UID,
			AcquiredAt: insight.AcquiredAt,
			ControlPlaneInsightUnion: updatev1alpha1.ControlPlaneInsightUnion{
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

func (c *updateStatusApi) ensureWorkerPool(pool updatev1alpha1.PoolResourceRef) *updatev1alpha1.Pool {
	for i := range c.us.Status.WorkerPools {
		if c.us.Status.WorkerPools[i].Name == pool.Name {
			c.us.Status.WorkerPools[i].Resource = pool // TODO: Handle conflicts?
			return &c.us.Status.WorkerPools[i]
		}
	}
	c.us.Status.WorkerPools = append(c.us.Status.WorkerPools, updatev1alpha1.Pool{
		Name:     pool.Name,
		Resource: pool,
	})

	return &c.us.Status.WorkerPools[len(c.us.Status.WorkerPools)-1]
}

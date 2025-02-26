package updatestatus

import (
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/google/go-cmp/cmp"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/klog/v2"

	"github.com/openshift/api/update/v1alpha1"
)

// statusApi is the desired state of the status API ConfigMap. It is updated when new insights are received.
// Any access to the struct should be done with the lock held.
type updateStatusApi struct {
	sync.Mutex
	cm *corev1.ConfigMap
	us *v1alpha1.UpdateStatus

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
	if c.cm == nil {
		c.cm = &corev1.ConfigMap{Data: map[string]string{}}
	}

	// Assemble the key because data is flattened in CM compared to UpdateStatus API where we would have a separate
	// container with insights for each informer
	cmKey := fmt.Sprintf("usc.%s.%s", msg.informer, msg.uid)

	var oldContent string
	if klog.V(4).Enabled() {
		oldContent = c.cm.Data[cmKey]
	}

	var updatedContent string
	if msg.cpInsight != nil {
		updatedContent = fmt.Sprintf("%s from %s", msg.cpInsight.UID, msg.informer)
	} else {
		updatedContent = fmt.Sprintf("%s from %s", msg.wpInsight.UID, msg.informer)
	}

	c.cm.Data[cmKey] = updatedContent

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
// that originally reported them. The insights are kept for a grace period after they are no longer reported as known
// and eventually dropped if they are not reported as known again within that period.
// Assumes the statusApi field is locked.
func (c *updateStatusApi) removeUnknownInsights(message informerMsg) {
	known := sets.New(message.knownInsights...)
	known.Insert(message.uid)
	informerPrefix := fmt.Sprintf("usc.%s.", message.informer)
	for key := range c.cm.Data {
		if strings.HasPrefix(key, informerPrefix) {
			uid := strings.TrimPrefix(key, informerPrefix)
			c.handleInsightExpiration(message.informer, known.Has(uid), uid)
		}
	}

	if len(c.unknownInsightExpirations) > 0 && len(c.unknownInsightExpirations[message.informer]) == 0 {
		delete(c.unknownInsightExpirations, message.informer)
	}
	if len(c.unknownInsightExpirations) == 0 {
		c.unknownInsightExpirations = nil
	}
}

// handleInsightExpiration considers potential expiration of an insight present in the API based on whether the informer
// knows about it.
// If the informer knows about the insight, it is not dropped from the API and any previous expiration is cancelled.
// If the informer does not know about the insight then it is either set to expire in the future if no expiration is
// set yet, or the expiration is checked to see whether the insight should be dropped.
func (c *updateStatusApi) handleInsightExpiration(informer string, knows bool, uid string) {
	now := c.now()

	if knows {
		if c.unknownInsightExpirations != nil && c.unknownInsightExpirations[informer] != nil {
			delete(c.unknownInsightExpirations[informer], uid)
		}
		return
	}

	expireIn := now.Add(unknownInsightGracePeriod)
	keepLog := func(expire time.Time) {
		klog.V(2).Infof("USC :: Collector :: Keeping insight %q until %s after it is no longer reported as known by informer %q", uid, expire, informer)
	}
	switch {
	// Two cases when we first consider an insight as unknown -> set expiration
	case c.unknownInsightExpirations == nil:
		c.unknownInsightExpirations = map[string]insightExpirations{informer: {uid: expireIn}}
		keepLog(expireIn)
	case c.unknownInsightExpirations[informer][uid].IsZero():
		c.unknownInsightExpirations[informer] = insightExpirations{uid: expireIn}
		keepLog(expireIn)

	// Already set for expiration but still in grace period -> keep insight
	case c.unknownInsightExpirations[informer][uid].After(now):
		keepLog(c.unknownInsightExpirations[informer][uid])

	// Already set for expiration and grace period expired -> drop insight
	default:
		delete(c.unknownInsightExpirations[informer], uid)
		delete(c.cm.Data, fmt.Sprintf("usc.%s.%s", informer, uid))
		klog.V(2).Infof("USC :: Collector :: Dropped insight %q because it is no longer reported as known by informer %q", uid, informer)
	}
}

func (c *updateStatusApi) sync(clusterState *v1alpha1.UpdateStatus) *v1alpha1.UpdateStatus {
	c.Lock()
	defer c.Unlock()

	cmFromClusterState := &corev1.ConfigMap{}

	if c.cm == nil {
		// This means we are running on a CM event before first insight arrived, otherwise internal state would exist
		klog.V(2).Infof("USC :: No internal state known yet, setting internal state to cluster state")
		// c.cm = clusterState.DeepCopy()
		c.cm = cmFromClusterState.DeepCopy()
		return nil
	}

	// We have internal state, so we need to overwrite the cluster state with our internal state but keep items that we do
	// not care about
	// mergedCm := clusterState.DeepCopy()
	mergedCm := cmFromClusterState.DeepCopy()
	for k := range mergedCm.Data {
		if isStatusInsightKey(k) {
			delete(mergedCm.Data, k)
		}
	}

	for k, v := range c.cm.Data {
		if mergedCm.Data == nil {
			mergedCm.Data = map[string]string{}
		}
		mergedCm.Data[k] = v
	}

	klog.V(2).Infof("USC :: Updating status API CM (%d insights)", len(c.cm.Data))
	c.cm = mergedCm

	// return mergedCm.DeepCopy()
	return clusterState.DeepCopy()
}

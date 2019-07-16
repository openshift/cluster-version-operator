package cvo

import (
	"fmt"
	"time"

	"net/url"

	"github.com/blang/semver"
	"github.com/google/uuid"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/klog"

	"k8s.io/apimachinery/pkg/api/equality"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	configv1 "github.com/openshift/api/config/v1"

	"github.com/openshift/cluster-version-operator/lib/resourcemerge"
	"github.com/openshift/cluster-version-operator/pkg/cincinnati"
)

// syncAvailableUpdates attempts to retrieve the latest updates and update the status of the ClusterVersion
// object. It will set the RetrievedUpdates condition. Updates are only checked if it has been more than
// the minimumUpdateCheckInterval since the last check.
func (optr *Operator) syncAvailableUpdates(config *configv1.ClusterVersion) error {
	usedDefaultUpstream := false
	upstream := string(config.Spec.Upstream)
	if len(upstream) == 0 {
		usedDefaultUpstream = true
		upstream = optr.defaultUpstreamServer
	}
	channel := config.Spec.Channel

	// updates are only checked at most once per minimumUpdateCheckInterval or if the generation changes
	u := optr.getAvailableUpdates()
	if u != nil && u.Upstream == upstream && u.Channel == channel && u.RecentlyChanged(optr.minimumUpdateCheckInterval) {
		klog.V(4).Infof("Available updates were recently retrieved, will try later.")
		return nil
	}

	proxyURL, err := optr.getHTTPSProxyURL()
	if err != nil {
		return err
	}

	updates, condition := calculateAvailableUpdatesStatus(string(config.Spec.ClusterID), proxyURL, upstream, channel, optr.releaseVersion)

	if usedDefaultUpstream {
		upstream = ""
	}
	optr.setAvailableUpdates(&availableUpdates{
		Upstream:  upstream,
		Channel:   config.Spec.Channel,
		Updates:   updates,
		Condition: condition,
	})
	// requeue
	optr.queue.Add(optr.queueKey())
	return nil
}

type availableUpdates struct {
	Upstream string
	Channel  string

	At time.Time

	Updates   []configv1.Update
	Condition configv1.ClusterOperatorStatusCondition
}

func (u *availableUpdates) RecentlyChanged(interval time.Duration) bool {
	return u.At.After(time.Now().Add(-interval))
}

func (u *availableUpdates) NeedsUpdate(original *configv1.ClusterVersion) *configv1.ClusterVersion {
	if u == nil {
		return nil
	}
	if u.Upstream != string(original.Spec.Upstream) || u.Channel != original.Spec.Channel {
		return nil
	}
	if equality.Semantic.DeepEqual(u.Updates, original.Status.AvailableUpdates) &&
		equality.Semantic.DeepEqual(u.Condition, resourcemerge.FindOperatorStatusCondition(original.Status.Conditions, u.Condition.Type)) {
		return nil
	}

	config := original.DeepCopy()
	resourcemerge.SetOperatorStatusCondition(&config.Status.Conditions, u.Condition)
	config.Status.AvailableUpdates = u.Updates
	return config
}

// setAvailableUpdates updates the currently calculated version of updates.
func (optr *Operator) setAvailableUpdates(u *availableUpdates) {
	if u != nil {
		u.At = time.Now()
	}

	optr.statusLock.Lock()
	defer optr.statusLock.Unlock()
	optr.availableUpdates = u
}

// getAvailableUpdates returns the current calculated version of updates. It
// may be nil.
func (optr *Operator) getAvailableUpdates() *availableUpdates {
	optr.statusLock.Lock()
	defer optr.statusLock.Unlock()
	return optr.availableUpdates
}

func calculateAvailableUpdatesStatus(clusterID string, proxyURL *url.URL, upstream, channel, version string) ([]configv1.Update, configv1.ClusterOperatorStatusCondition) {
	if len(upstream) == 0 {
		return nil, configv1.ClusterOperatorStatusCondition{
			Type: configv1.RetrievedUpdates, Status: configv1.ConditionFalse, Reason: "NoUpstream",
			Message: "No upstream server has been set to retrieve updates.",
		}
	}

	if len(version) == 0 {
		return nil, configv1.ClusterOperatorStatusCondition{
			Type: configv1.RetrievedUpdates, Status: configv1.ConditionFalse, Reason: "NoCurrentVersion",
			Message: "The cluster version does not have a semantic version assigned and cannot calculate valid upgrades.",
		}
	}

	if len(channel) == 0 {
		return nil, configv1.ClusterOperatorStatusCondition{
			Type: configv1.RetrievedUpdates, Status: configv1.ConditionFalse, Reason: "NoChannel",
			Message: "The update channel has not been configured.",
		}
	}

	currentVersion, err := semver.Parse(version)
	if err != nil {
		klog.V(2).Infof("Unable to parse current semantic version %q: %v", version, err)
		return nil, configv1.ClusterOperatorStatusCondition{
			Type: configv1.RetrievedUpdates, Status: configv1.ConditionFalse, Reason: "InvalidCurrentVersion",
			Message: "The current cluster version is not a valid semantic version and cannot be used to calculate upgrades.",
		}
	}

	updates, err := checkForUpdate(clusterID, proxyURL, upstream, channel, currentVersion)
	if err != nil {
		klog.V(2).Infof("Upstream server %s could not return available updates: %v", upstream, err)
		return nil, configv1.ClusterOperatorStatusCondition{
			Type: configv1.RetrievedUpdates, Status: configv1.ConditionFalse, Reason: "RemoteFailed",
			Message: fmt.Sprintf("Unable to retrieve available updates: %v", err),
		}
	}

	var cvoUpdates []configv1.Update
	for _, update := range updates {
		cvoUpdates = append(cvoUpdates, configv1.Update{
			Version: update.Version.String(),
			Image:   update.Image,
		})
	}

	return cvoUpdates, configv1.ClusterOperatorStatusCondition{
		Type:   configv1.RetrievedUpdates,
		Status: configv1.ConditionTrue,

		LastTransitionTime: metav1.Now(),
	}
}

func checkForUpdate(clusterID string, proxyURL *url.URL, upstream, channel string, currentVersion semver.Version) ([]cincinnati.Update, error) {
	uuid, err := uuid.Parse(string(clusterID))
	if err != nil {
		return nil, err
	}

	if len(upstream) == 0 {
		return nil, fmt.Errorf("no upstream URL set for cluster version")
	}
	return cincinnati.NewClient(uuid, proxyURL).GetUpdates(upstream, channel, currentVersion)
}

// getHTTPSProxyURL returns a url.URL object for the configured
// https proxy only. It can be nil if does not exist or there is an error.
func (optr *Operator) getHTTPSProxyURL() (*url.URL, error) {
	proxy, err := optr.proxyLister.Get("cluster")

	if errors.IsNotFound(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	if &proxy.Spec != nil {
		if proxy.Spec.HTTPSProxy != "" {
			proxyURL, err := url.Parse(proxy.Spec.HTTPSProxy)
			if err != nil {
				return nil, err
			}
			return proxyURL, nil
		}
	}
	return nil, nil
}

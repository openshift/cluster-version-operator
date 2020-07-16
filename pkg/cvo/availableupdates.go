package cvo

import (
	"crypto/tls"
	"fmt"
	"net/url"
	"runtime"
	"time"

	"github.com/blang/semver/v4"
	"github.com/google/uuid"
	"k8s.io/apimachinery/pkg/api/equality"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/klog"

	configv1 "github.com/openshift/api/config/v1"
	"github.com/openshift/cluster-version-operator/lib/resourcemerge"
	"github.com/openshift/cluster-version-operator/pkg/cincinnati"
)

const noChannel string = "NoChannel"

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
	arch := runtime.GOARCH
	channel := config.Spec.Channel

	// updates are only checked at most once per minimumUpdateCheckInterval or if the generation changes
	u := optr.getAvailableUpdates()
	if u != nil && u.Upstream == upstream && u.Channel == channel && u.RecentlyChanged(optr.minimumUpdateCheckInterval) {
		klog.V(4).Infof("Available updates were recently retrieved, will try later.")
		return nil
	}

	proxyURL, tlsConfig, err := optr.getTransportOpts()
	if err != nil {
		return err
	}

	updates, condition := calculateAvailableUpdatesStatus(string(config.Spec.ClusterID), proxyURL, tlsConfig, upstream, arch, channel, optr.releaseVersion)

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

	// LastAttempt records the time of the most recent attempt at update
	// retrieval, regardless of whether it was successful.
	LastAttempt time.Time

	// LastSyncOrConfigChange records the most recent time when any of
	// the following events occurred:
	//
	// * Upstream changed, reflecting a new authority, and obsoleting
	//   any information retrieved from (or failures // retrieving from) the
	//   previous authority.
	// * Channel changes.  Same reasoning as for Upstream.
	// * A slice of Updates was successfully retrieved, even if that
	//   slice was empty.
	LastSyncOrConfigChange time.Time

	Updates   []configv1.Update
	Condition configv1.ClusterOperatorStatusCondition
}

func (u *availableUpdates) RecentlyChanged(interval time.Duration) bool {
	return u.LastAttempt.After(time.Now().Add(-interval))
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
	success := false
	if u != nil {
		u.LastAttempt = time.Now()
		if u.Condition.Type == configv1.RetrievedUpdates {
			success = u.Condition.Status == configv1.ConditionTrue
		} else {
			klog.Warningf("Unrecognized condition %s=%s (%s: %s): cannot judge update retrieval success", u.Condition.Type, u.Condition.Status, u.Condition.Reason, u.Condition.Message)
		}
	}

	optr.statusLock.Lock()
	defer optr.statusLock.Unlock()
	if u != nil && (optr.availableUpdates == nil ||
		optr.availableUpdates.Upstream != u.Upstream ||
		optr.availableUpdates.Channel != u.Channel ||
		success) {
		u.LastSyncOrConfigChange = u.LastAttempt
	} else if optr.availableUpdates != nil {
		u.LastSyncOrConfigChange = optr.availableUpdates.LastSyncOrConfigChange
	}
	optr.availableUpdates = u
}

// getAvailableUpdates returns the current calculated version of updates. It
// may be nil.
func (optr *Operator) getAvailableUpdates() *availableUpdates {
	optr.statusLock.Lock()
	defer optr.statusLock.Unlock()
	return optr.availableUpdates
}

func calculateAvailableUpdatesStatus(clusterID string, proxyURL *url.URL, tlsConfig *tls.Config, upstream, arch, channel, version string) ([]configv1.Update, configv1.ClusterOperatorStatusCondition) {
	if len(upstream) == 0 {
		return nil, configv1.ClusterOperatorStatusCondition{
			Type: configv1.RetrievedUpdates, Status: configv1.ConditionFalse, Reason: "NoUpstream",
			Message: "No upstream server has been set to retrieve updates.",
		}
	}

	upstreamURI, err := url.Parse(upstream)
	if err != nil {
		return nil, configv1.ClusterOperatorStatusCondition{
			Type: configv1.RetrievedUpdates, Status: configv1.ConditionFalse, Reason: "InvalidURI",
			Message: fmt.Sprintf("failed to parse upstream URL: %s", err),
		}
	}

	uuid, err := uuid.Parse(string(clusterID))
	if err != nil {
		return nil, configv1.ClusterOperatorStatusCondition{
			Type: configv1.RetrievedUpdates, Status: configv1.ConditionFalse, Reason: "InvalidID",
			Message: fmt.Sprintf("invalid cluster ID: %s", err),
		}
	}

	if len(arch) == 0 {
		return nil, configv1.ClusterOperatorStatusCondition{
			Type: configv1.RetrievedUpdates, Status: configv1.ConditionFalse, Reason: "NoArchitecture",
			Message: "The set of architectures has not been configured.",
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
			Type: configv1.RetrievedUpdates, Status: configv1.ConditionFalse, Reason: noChannel,
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

	updates, err := cincinnati.NewClient(uuid, proxyURL, tlsConfig).GetUpdates(upstreamURI, arch, channel, currentVersion)
	if err != nil {
		klog.V(2).Infof("Upstream server %s could not return available updates: %v", upstream, err)
		if updateError, ok := err.(*cincinnati.Error); ok {
			return nil, configv1.ClusterOperatorStatusCondition{
				Type: configv1.RetrievedUpdates, Status: configv1.ConditionFalse, Reason: updateError.Reason,
				Message: fmt.Sprintf("Unable to retrieve available updates: %s", updateError.Message),
			}
		}
		// this should never happen
		return nil, configv1.ClusterOperatorStatusCondition{
			Type: configv1.RetrievedUpdates, Status: configv1.ConditionFalse, Reason: "Unknown",
			Message: fmt.Sprintf("Unable to retrieve available updates: %s", err),
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

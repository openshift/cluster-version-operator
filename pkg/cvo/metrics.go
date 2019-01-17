package cvo

import (
	"time"

	configv1 "github.com/openshift/api/config/v1"
	"github.com/openshift/cluster-version-operator/lib/resourcemerge"
	"github.com/prometheus/client_golang/prometheus"
	"k8s.io/apimachinery/pkg/labels"
)

func (optr *Operator) registerMetrics() error {
	m := newOperatorMetrics(optr)
	return prometheus.Register(m)
}

type operatorMetrics struct {
	optr *Operator

	version                   *prometheus.GaugeVec
	availableUpdates          *prometheus.GaugeVec
	clusterOperatorConditions *prometheus.GaugeVec
	clusterOperatorUp         *prometheus.GaugeVec
}

func newOperatorMetrics(optr *Operator) *operatorMetrics {
	return &operatorMetrics{
		optr: optr,

		version: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "cluster_version",
			Help: `Reports the version of the cluster in terms of seconds since
the epoch. Type 'current' is the version being applied and
the value is the creation date of the payload. The type
'desired' is returned if spec.desiredUpdate is set but the
operator has not yet updated and the value is the most 
recent status transition time. The type 'failure' is set 
if an error is preventing sync or upgrade with the last 
transition timestamp of the condition. The type 'completed' 
is the timestamp when the last image was successfully
applied. The type 'cluster' is the creation date of the
cluster version object.
.`,
		}, []string{"type", "version", "image"}),
		availableUpdates: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "cluster_version_available_updates",
			Help: "Report the count of available versions for an upstream and channel.",
		}, []string{"upstream", "channel"}),
		clusterOperatorUp: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "cluster_operator_up",
			Help: "Reports key highlights of the active cluster operators.",
		}, []string{"namespace", "name", "version"}),
		clusterOperatorConditions: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "cluster_operator_conditions",
			Help: "Report the conditions for active cluster operators. 0 is False and 1 is True.",
		}, []string{"namespace", "name", "condition"}),
	}
}

func (m *operatorMetrics) Describe(ch chan<- *prometheus.Desc) {
	ch <- m.version.WithLabelValues("", "", "").Desc()
}

func (m *operatorMetrics) Collect(ch chan<- prometheus.Metric) {
	current := m.optr.currentVersion()
	g := m.version.WithLabelValues("current", current.Version, current.Image)
	if m.optr.releaseCreated.IsZero() {
		g.Set(0)
	} else {
		g.Set(float64(m.optr.releaseCreated.Unix()))
	}
	ch <- g
	if cv, err := m.optr.cvLister.Get(m.optr.name); err == nil {
		// output cluster version

		g := m.version.WithLabelValues("cluster", current.Version, current.Image)
		g.Set(float64(cv.CreationTimestamp.Unix()))
		ch <- g

		failing := resourcemerge.FindOperatorStatusCondition(cv.Status.Conditions, configv1.OperatorFailing)
		if update := cv.Spec.DesiredUpdate; update != nil && update.Image != current.Image {
			g := m.version.WithLabelValues("desired", update.Version, update.Image)
			g.Set(float64(mostRecentTimestamp(cv)))
			ch <- g
			if failing != nil && failing.Status == configv1.ConditionTrue {
				g = m.version.WithLabelValues("failure", update.Version, update.Image)
				if failing.LastTransitionTime.IsZero() {
					g.Set(0)
				} else {
					g.Set(float64(failing.LastTransitionTime.Unix()))
				}
				ch <- g
			}
		}
		if failing != nil && failing.Status == configv1.ConditionTrue {
			g := m.version.WithLabelValues("failure", current.Version, current.Image)
			if failing.LastTransitionTime.IsZero() {
				g.Set(0)
			} else {
				g.Set(float64(failing.LastTransitionTime.Unix()))
			}
			ch <- g
		}

		// record the last completed update is completed at least once (1) or no completed update has been applied (0)
		var completedUpdate configv1.Update
		completed := float64(0)
		for _, history := range cv.Status.History {
			if history.State == configv1.CompletedUpdate {
				completedUpdate.Image = history.Image
				completedUpdate.Version = history.Version
				completed = float64(history.CompletionTime.Unix())
				break
			}
		}
		g = m.version.WithLabelValues("completed", completedUpdate.Version, completedUpdate.Image)
		g.Set(completed)
		ch <- g

		if len(cv.Spec.Upstream) > 0 || len(cv.Status.AvailableUpdates) > 0 || resourcemerge.IsOperatorStatusConditionTrue(cv.Status.Conditions, configv1.RetrievedUpdates) {
			upstream := "<default>"
			if len(cv.Spec.Upstream) > 0 {
				upstream = string(cv.Spec.Upstream)
			}
			g := m.availableUpdates.WithLabelValues(upstream, cv.Spec.Channel)
			g.Set(float64(len(cv.Status.AvailableUpdates)))
			ch <- g
		}
	}

	// output cluster operator version and condition info
	operators, _ := m.optr.clusterOperatorLister.List(labels.Everything())
	for _, op := range operators {
		// TODO: when we define how version works, report the appropriate version
		var firstVersion string
		for _, v := range op.Status.Versions {
			firstVersion = v.Version
			break
		}
		g := m.clusterOperatorUp.WithLabelValues(op.Namespace, op.Name, firstVersion)
		failing := resourcemerge.IsOperatorStatusConditionTrue(op.Status.Conditions, configv1.OperatorFailing)
		available := resourcemerge.IsOperatorStatusConditionTrue(op.Status.Conditions, configv1.OperatorAvailable)
		if available && !failing {
			g.Set(1)
		} else {
			g.Set(0)
		}
		ch <- g
		for _, condition := range op.Status.Conditions {
			if condition.Status == configv1.ConditionUnknown {
				continue
			}
			g := m.clusterOperatorConditions.WithLabelValues(op.Namespace, op.Name, string(condition.Type))
			if condition.Status == configv1.ConditionTrue {
				g.Set(1)
			} else {
				g.Set(0)
			}
			ch <- g
		}
	}
}

// mostRecentTimestamp finds the most recent change recorded to the status and
// returns the seconds since the epoch.
func mostRecentTimestamp(cv *configv1.ClusterVersion) int64 {
	var latest time.Time
	if len(cv.Status.History) > 0 {
		latest = cv.Status.History[0].StartedTime.Time
		if t := cv.Status.History[0].CompletionTime; t != nil {
			if t.Time.After(latest) {
				latest = t.Time
			}
		}
	}
	for _, condition := range cv.Status.Conditions {
		if condition.LastTransitionTime.After(latest) {
			latest = condition.LastTransitionTime.Time
		}
	}
	if cv.CreationTimestamp.After(latest) {
		latest = cv.CreationTimestamp.Time
	}
	if latest.IsZero() {
		return 0
	}
	return latest.Unix()
}

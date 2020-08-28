package cvo

import (
	"context"
	"crypto/tls"
	"net"
	"net/http"
	"time"

	"github.com/cockroachdb/cmux"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/client-go/tools/cache"
	"k8s.io/klog"

	configv1 "github.com/openshift/api/config/v1"
	"github.com/openshift/cluster-version-operator/lib/resourcemerge"
	"github.com/openshift/cluster-version-operator/pkg/internal"
)

// RegisterMetrics initializes metrics and registers them with the
// Prometheus implementation.
func (optr *Operator) RegisterMetrics(coInformer cache.SharedInformer) error {
	m := newOperatorMetrics(optr)
	coInformer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		UpdateFunc: m.clusterOperatorChanged,
	})
	return prometheus.Register(m)
}

type operatorMetrics struct {
	optr *Operator

	conditionTransitions map[conditionKey]int

	version                                               *prometheus.GaugeVec
	availableUpdates                                      *prometheus.GaugeVec
	clusterOperatorUp                                     *prometheus.GaugeVec
	clusterOperatorConditions                             *prometheus.GaugeVec
	clusterOperatorConditionTransitions                   *prometheus.GaugeVec
	clusterInstaller                                      *prometheus.GaugeVec
	clusterVersionOperatorUpdateRetrievalTimestampSeconds *prometheus.GaugeVec
}

func newOperatorMetrics(optr *Operator) *operatorMetrics {
	return &operatorMetrics{
		optr: optr,

		conditionTransitions: make(map[conditionKey]int),

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
cluster version object and the current version. The type
'updating' is set when the cluster is transitioning to a
new version but has not reached the completed state and
is the time the update was started. The type 'initial' is
set to the oldest entry in the history. The from_version label
will be set to the last completed version, the initial
version for 'cluster', or empty for 'initial'.
.`,
		}, []string{"type", "version", "image", "from_version"}),
		availableUpdates: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "cluster_version_available_updates",
			Help: "Report the count of available versions for an upstream and channel.",
		}, []string{"upstream", "channel"}),
		clusterOperatorUp: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "cluster_operator_up",
			Help: "Reports key highlights of the active cluster operators.",
		}, []string{"name", "version"}),
		clusterOperatorConditions: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "cluster_operator_conditions",
			Help: "Report the conditions for active cluster operators. 0 is False and 1 is True.",
		}, []string{"name", "condition", "reason"}),
		clusterOperatorConditionTransitions: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "cluster_operator_condition_transitions",
			Help: "Reports the number of times that a condition on a cluster operator changes status",
		}, []string{"name", "condition"}),
		clusterInstaller: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "cluster_installer",
			Help: "Reports info about the installation process and, if applicable, the install tool. The type is either 'openshift-install', indicating that openshift-install was used to install the cluster, or 'other', indicating that an unknown process installed the cluster. The invoker is 'user' by default, but it may be overridden by a consuming tool. The version reported is that of the openshift-install that was used to generate the manifests and, if applicable, provision the infrastructure.",
		}, []string{"type", "version", "invoker"}),
		clusterVersionOperatorUpdateRetrievalTimestampSeconds: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "cluster_version_operator_update_retrieval_timestamp_seconds",
			Help: "Reports when updates were last succesfully retrieved.",
		}, []string{"name"}),
	}
}

// RunMetrics launches an server bound to listenAddress serving
// Prometheus metrics at /metrics over HTTP, and, if tlsConfig is
// non-nil, also over HTTPS.  Continues serving until runContext.Done()
// and then attempts a clean shutdown limited by shutdownContext.Done().
// Assumes runContext.Done() occurs before or simultaneously with
// shutdownContext.Done().
func RunMetrics(runContext context.Context, shutdownContext context.Context, listenAddress string, tlsConfig *tls.Config) error {
	handler := http.NewServeMux()
	handler.Handle("/metrics", promhttp.Handler())
	server := &http.Server{
		Handler: handler,
	}

	tcpListener, err := net.Listen("tcp", listenAddress)
	if err != nil {
		return err
	}

	// if a TLS connection was requested, set up a connection mux that will send TLS requests to
	// the TLS server but send HTTP requests to the HTTP server. Preserves the ability for legacy
	// HTTP, needed during upgrade, while still allowing TLS certs and end to end metrics protection.
	mux := cmux.New(tcpListener)

	errorChannel := make(chan error, 1)
	errorChannelCount := 1

	go func() {
		// match HTTP first
		httpListener := mux.Match(cmux.HTTP1())
		klog.Infof("Metrics port listening for HTTP on %v", listenAddress)
		errorChannel <- server.Serve(httpListener)
	}()

	if tlsConfig != nil {
		errorChannelCount++
		go func() {
			tlsListener := tls.NewListener(mux.Match(cmux.Any()), tlsConfig)
			klog.Infof("Metrics port listening for HTTPS on %v", listenAddress)
			errorChannel <- server.Serve(tlsListener)
		}()
	}

	errorChannelCount++
	go func() {
		errorChannel <- mux.Serve()
	}()

	shutdown := false
	var loopError error
	for errorChannelCount > 0 {
		if shutdown {
			err := <-errorChannel
			errorChannelCount--
			if err != nil && err != http.ErrServerClosed && err != cmux.ErrListenerClosed {
				if loopError == nil {
					loopError = err
				} else if err != nil { // log the error we are discarding
					klog.Errorf("Failed to gracefully shut down metrics server: %s", err)
				}
			}
		} else {
			select {
			case <-runContext.Done(): // clean shutdown
			case err := <-errorChannel: // crashed before a shutdown was requested
				errorChannelCount--
				if err != nil && err != http.ErrServerClosed && err != cmux.ErrListenerClosed {
					loopError = err
				}
			}
			shutdown = true
			shutdownError := server.Shutdown(shutdownContext)
			if loopError == nil {
				loopError = shutdownError
			} else if shutdownError != nil { // log the error we are discarding
				klog.Errorf("Failed to gracefully shut down metrics server: %s", shutdownError)
			}
		}
	}

	return loopError
}

type conditionKey struct {
	Name string
	Type string
}

// clusterOperatorChanged detects condition transitions and records them
func (m *operatorMetrics) clusterOperatorChanged(oldObj, obj interface{}) {
	oldCO, ok := oldObj.(*configv1.ClusterOperator)
	if !ok {
		return
	}
	co, ok := obj.(*configv1.ClusterOperator)
	if !ok {
		return
	}
	types := sets.NewString()
	for _, older := range oldCO.Status.Conditions {
		if types.Has(string(older.Type)) {
			continue
		}
		types.Insert(string(older.Type))
		newer := resourcemerge.FindOperatorStatusCondition(co.Status.Conditions, older.Type)
		if newer == nil {
			m.conditionTransitions[conditionKey{Name: co.Name, Type: string(older.Type)}]++
			continue
		}
		if newer.Status != older.Status {
			m.conditionTransitions[conditionKey{Name: co.Name, Type: string(older.Type)}]++
			continue
		}
	}
	for _, newer := range co.Status.Conditions {
		if types.Has(string(newer.Type)) {
			continue
		}
		types.Insert(string(newer.Type))
		m.conditionTransitions[conditionKey{Name: co.Name, Type: string(newer.Type)}]++
	}
}

func (m *operatorMetrics) Describe(ch chan<- *prometheus.Desc) {
	ch <- m.version.WithLabelValues("", "", "", "").Desc()
	ch <- m.availableUpdates.WithLabelValues("", "").Desc()
	ch <- m.clusterOperatorUp.WithLabelValues("", "").Desc()
	ch <- m.clusterOperatorConditions.WithLabelValues("", "", "").Desc()
	ch <- m.clusterOperatorConditionTransitions.WithLabelValues("", "").Desc()
	ch <- m.clusterInstaller.WithLabelValues("", "", "").Desc()
	ch <- m.clusterVersionOperatorUpdateRetrievalTimestampSeconds.WithLabelValues("").Desc()
}

func (m *operatorMetrics) Collect(ch chan<- prometheus.Metric) {
	current := m.optr.currentVersion()
	var completed configv1.UpdateHistory

	if cv, err := m.optr.cvLister.Get(m.optr.name); err == nil {
		// output cluster version

		var initial configv1.UpdateHistory
		if last := len(cv.Status.History); last > 0 {
			initial = cv.Status.History[last-1]
		}

		// if an update ran to completion, report the timestamp of that update and store the completed
		// version for use in other metrics
		for i, history := range cv.Status.History {
			if history.State == configv1.CompletedUpdate {
				completed = history
				var previous configv1.UpdateHistory
				for _, history := range cv.Status.History[i+1:] {
					if history.State == configv1.CompletedUpdate {
						previous = history
						break
					}
				}
				g := m.version.WithLabelValues("completed", history.Version, history.Image, previous.Version)
				g.Set(float64(history.CompletionTime.Unix()))
				ch <- g
				break
			}
		}

		// answers "which images were clusters initially installed with"
		g := m.version.WithLabelValues("initial", initial.Version, initial.Image, "")
		g.Set(float64(cv.CreationTimestamp.Unix()))
		ch <- g

		// answers "how old are clusters at this version currently and what version did they start at"
		initialVersion := initial.Version
		// clear the initial version if we have never "reached level" (i.e., we are still installing),
		// which allows us to exclude clusters that are still being installed
		if len(completed.Version) == 0 {
			initialVersion = ""
		}
		g = m.version.WithLabelValues("cluster", current.Version, current.Image, initialVersion)
		g.Set(float64(cv.CreationTimestamp.Unix()))
		ch <- g

		// answers "is there a desired update we have not yet satisfied"
		if update := cv.Spec.DesiredUpdate; update != nil && update.Image != current.Image {
			g = m.version.WithLabelValues("desired", update.Version, update.Image, completed.Version)
			g.Set(float64(mostRecentTimestamp(cv)))
			ch <- g
		}

		// answers "if we are failing, are we updating or reconciling"
		failing := resourcemerge.FindOperatorStatusCondition(cv.Status.Conditions, ClusterStatusFailing)
		if failing != nil && failing.Status == configv1.ConditionTrue {
			if update := cv.Spec.DesiredUpdate; update != nil && update.Image != current.Image {
				g = m.version.WithLabelValues("failure", update.Version, update.Image, completed.Version)
			} else {
				g = m.version.WithLabelValues("failure", current.Version, current.Image, completed.Version)
			}
			if failing.LastTransitionTime.IsZero() {
				g.Set(0)
			} else {
				g.Set(float64(failing.LastTransitionTime.Unix()))
			}
			ch <- g
		}

		// when the CVO is transitioning towards a new version report a unique series describing it
		if len(cv.Status.History) > 0 && cv.Status.History[0].State == configv1.PartialUpdate {
			updating := cv.Status.History[0]
			g := m.version.WithLabelValues("updating", updating.Version, updating.Image, completed.Version)
			if updating.StartedTime.IsZero() {
				g.Set(0)
			} else {
				g.Set(float64(updating.StartedTime.Unix()))
			}
			ch <- g
		}

		if len(cv.Spec.Upstream) > 0 || len(cv.Status.AvailableUpdates) > 0 || resourcemerge.IsOperatorStatusConditionTrue(cv.Status.Conditions, configv1.RetrievedUpdates) {
			upstream := "<default>"
			if len(cv.Spec.Upstream) > 0 {
				upstream = string(cv.Spec.Upstream)
			}
			g := m.availableUpdates.WithLabelValues(upstream, cv.Spec.Channel)
			g.Set(float64(len(cv.Status.AvailableUpdates)))
			ch <- g
		}

		for _, condition := range cv.Status.Conditions {
			if condition.Status == configv1.ConditionUnknown {
				continue
			}
			g := m.clusterOperatorConditions.WithLabelValues("version", string(condition.Type), string(condition.Reason))
			if condition.Status == configv1.ConditionTrue {
				g.Set(1)
			} else {
				g.Set(0)
			}
			ch <- g
		}
	}

	g := m.version.WithLabelValues("current", current.Version, current.Image, completed.Version)
	if m.optr.releaseCreated.IsZero() {
		g.Set(0)
	} else {
		g.Set(float64(m.optr.releaseCreated.Unix()))
	}
	ch <- g

	// output cluster operator version and condition info
	operators, _ := m.optr.coLister.List(labels.Everything())
	for _, op := range operators {
		// TODO: when we define how version works, report the appropriate version
		var firstVersion string
		for _, v := range op.Status.Versions {
			firstVersion = v.Version
			break
		}
		g := m.clusterOperatorUp.WithLabelValues(op.Name, firstVersion)
		failing := resourcemerge.IsOperatorStatusConditionTrue(op.Status.Conditions, configv1.OperatorDegraded)
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
			g := m.clusterOperatorConditions.WithLabelValues(op.Name, string(condition.Type), string(condition.Reason))
			if condition.Status == configv1.ConditionTrue {
				g.Set(1)
			} else {
				g.Set(0)
			}
			ch <- g
		}
	}

	for key, value := range m.conditionTransitions {
		g := m.clusterOperatorConditionTransitions.WithLabelValues(key.Name, key.Type)
		g.Set(float64(value))
		ch <- g
	}

	if installer, err := m.optr.cmConfigLister.Get(internal.InstallerConfigMap); err == nil {
		ch <- gaugeFromInstallConfigMap(installer, m.clusterInstaller, "openshift-install")
	} else if !apierrors.IsNotFound(err) {
	} else if manifests, err := m.optr.cmConfigLister.Get(internal.ManifestsConfigMap); err == nil {
		ch <- gaugeFromInstallConfigMap(manifests, m.clusterInstaller, "other")
	} else if apierrors.IsNotFound(err) {
		g := m.clusterInstaller.WithLabelValues("", "", "")
		g.Set(1.0)
		ch <- g
	}

	// check ability to retrieve recommended updates
	if availableUpdates := m.optr.getAvailableUpdates(); availableUpdates != nil {
		g := m.clusterVersionOperatorUpdateRetrievalTimestampSeconds.WithLabelValues("")
		g.Set(float64(availableUpdates.LastSyncOrConfigChange.Unix()))
		ch <- g
	} else {
		klog.Warningf("availableUpdates is nil")
	}
}

func gaugeFromInstallConfigMap(cm *corev1.ConfigMap, gauge *prometheus.GaugeVec, installType string) prometheus.Gauge {
	version := "<missing>"
	invoker := "<missing>"

	if v, ok := cm.Data["version"]; ok {
		version = v
	}
	if i, ok := cm.Data["invoker"]; ok {
		invoker = i
	}

	g := gauge.WithLabelValues(installType, version, invoker)
	g.Set(1.0)

	return g
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

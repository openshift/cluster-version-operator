package cvo

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net"
	"net/http"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/apiserver/pkg/server/dynamiccertificates"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/tools/record"
	"k8s.io/klog/v2"

	configv1 "github.com/openshift/api/config/v1"
	"github.com/openshift/cluster-version-operator/lib/resourcemerge"
	"github.com/openshift/cluster-version-operator/pkg/internal"
	"github.com/openshift/library-go/pkg/crypto"
)

// RegisterMetrics initializes metrics and registers them with the
// Prometheus implementation.
func (optr *Operator) RegisterMetrics(coInformer cache.SharedInformer) error {
	m := newOperatorMetrics(optr)
	if _, err := coInformer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		UpdateFunc: m.clusterOperatorChanged,
	}); err != nil {
		return err
	}
	return prometheus.Register(m)
}

type operatorMetrics struct {
	optr *Operator

	conditionTransitions map[conditionKey]int

	version                                               *prometheus.GaugeVec
	availableUpdates                                      *prometheus.GaugeVec
	capability                                            *prometheus.GaugeVec
	clusterOperatorUp                                     *prometheus.GaugeVec
	clusterOperatorConditions                             *prometheus.GaugeVec
	clusterVersionRiskConditions                          *prometheus.GaugeVec
	clusterOperatorConditionTransitions                   *prometheus.GaugeVec
	clusterInstaller                                      *prometheus.GaugeVec
	clusterVersionOperatorUpdateRetrievalTimestampSeconds *prometheus.GaugeVec
	clusterVersionConditionalUpdateConditionSeconds       *prometheus.GaugeVec
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
will be set to the last completed version for most types, the
initial version for 'cluster', empty for 'initial', and the
penultimate completed version for 'completed'.
.`,
		}, []string{"type", "version", "image", "from_version"}),
		availableUpdates: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "cluster_version_available_updates",
			Help: "Report the count of available versions for an upstream and channel.",
		}, []string{"upstream", "channel"}),
		capability: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "cluster_version_capability",
			Help: "Report currently enabled cluster capabilities.  0 is disabled, and 1 is enabled.",
		}, []string{"name"}),
		clusterOperatorUp: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "cluster_operator_up",
			Help: "1 if a cluster operator is Available=True.  0 otherwise, including if a cluster operator sets no Available condition.  The 'version' label tracks the 'operator' version.  The 'reason' label is passed through from the Available condition, unless the cluster operator sets no Available condition, in which case NoAvailableCondition is used.",
		}, []string{"name", "version", "reason"}),
		clusterOperatorConditions: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "cluster_operator_conditions",
			Help: "Report the conditions for active cluster operators. 0 is False and 1 is True.",
		}, []string{"name", "condition", "reason"}),
		clusterVersionRiskConditions: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "cluster_version_risk_conditions",
			Help: "Report the risk conditions for cluster versions. 0 is False and 1 is True.",
		}, []string{"name", "condition", "risk"}),
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
			Help: "Reports when updates were last successfully retrieved.",
		}, []string{"name"}),
		clusterVersionConditionalUpdateConditionSeconds: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "cluster_version_conditional_update_condition_seconds",
			Help: "Reports when the Recommended condition status on a conditional update changed to its current state.",
		}, []string{"version", "condition", "status", "reason"}),
	}
}

type asyncResult struct {
	name  string
	error error
}

func createHttpServer(options MetricsOptions, clientCA dynamiccertificates.CAContentProvider) *http.Server {
	if options.DisableAuthentication && options.DisableAuthorization {
		handler := http.NewServeMux()
		handler.Handle("/metrics", promhttp.Handler())
		return &http.Server{Handler: handler}
	}

	auth := authHandler{
		downstream:           promhttp.Handler(),
		clientCA:             clientCA,
		enableAuthentication: !options.DisableAuthentication,
		enableAuthorization:  !options.DisableAuthorization,
	}
	handler := http.NewServeMux()
	handler.Handle("/metrics", &auth)
	return &http.Server{Handler: handler}
}

type authHandler struct {
	downstream           http.Handler
	clientCA             dynamiccertificates.CAContentProvider
	enableAuthentication bool
	enableAuthorization  bool
}

// ServeHTTP performs application-level authentication and authorization.
func (a *authHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if !a.enableAuthentication && !a.enableAuthorization {
		a.downstream.ServeHTTP(w, r)
		return
	}

	// Both authentication and authorization require a client certificate
	if r.TLS == nil || len(r.TLS.PeerCertificates) == 0 {
		klog.V(4).Info("Client certificate required but not provided")
		http.Error(w, "client certificate required", http.StatusUnauthorized)
		return
	}

	if a.enableAuthentication && !a.authenticate(w, r) {
		return
	}

	if a.enableAuthorization && !a.authorize(w, r) {
		return
	}

	a.downstream.ServeHTTP(w, r)
}

// authenticate verifies the client certificate chain against the configured CA.
// Returns true if authenticated successfully, false otherwise (and writes HTTP error).
func (a *authHandler) authenticate(w http.ResponseWriter, r *http.Request) bool {
	opts, ok := a.clientCA.VerifyOptions()
	if !ok {
		klog.Error("verify options from client CA provider could not be loaded")
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return false
	}

	if len(r.TLS.PeerCertificates) > 1 {
		intermediates := x509.NewCertPool()
		for _, cert := range r.TLS.PeerCertificates[1:] {
			intermediates.AddCert(cert)
		}
		opts.Intermediates = intermediates
	}

	if _, err := r.TLS.PeerCertificates[0].Verify(opts); err != nil {
		klog.V(4).Infof("Client certificate verification failed: %v", err)
		http.Error(w, "client certificate not trusted", http.StatusUnauthorized)
		return false
	}

	return true
}

// authorize verifies the client certificate CN against the allowed CN.
// Returns true if authorized, false otherwise (and writes HTTP error).
func (a *authHandler) authorize(w http.ResponseWriter, r *http.Request) bool {
	// metricsAllowedClientCommonName is the Common Name (CN) of the client certificate
	// that is authorized to access the metrics endpoint. This corresponds to the
	// well-known Prometheus service account in OpenShift monitoring.
	// See: https://github.com/openshift/enhancements/blob/master/CONVENTIONS.md#metrics
	metricsAllowedClientCommonName := "system:serviceaccount:openshift-monitoring:prometheus-k8s"

	commonName := r.TLS.PeerCertificates[0].Subject.CommonName
	if commonName != metricsAllowedClientCommonName {
		klog.V(4).Infof("Access denied for common name: %s", commonName)
		http.Error(w, fmt.Sprintf("unauthorized common name: %s", commonName), http.StatusForbidden)
		return false
	}

	klog.V(5).Infof("Access granted for common name: %s", commonName)
	return true
}

func startListening(svr *http.Server, tlsConfig *tls.Config, lAddr string, resultChannel chan asyncResult) {
	tcpListener, err := net.Listen("tcp", lAddr)
	if err != nil {
		resultChannel <- asyncResult{
			name:  "HTTPS server",
			error: fmt.Errorf("failed to listen to the network address %s reserved for cluster-version-operator metrics: %w", lAddr, err),
		}
		return
	}
	tlsListener := tls.NewListener(tcpListener, tlsConfig)
	klog.Infof("Metrics port listening for HTTPS on %v", lAddr)
	err = svr.Serve(tlsListener)
	resultChannel <- asyncResult{name: "HTTPS server", error: err}
}

func handleServerResult(result asyncResult, lastLoopError error) error {
	lastError := lastLoopError

	// API states "Serve always returns non-nil error" but check in case API changes.
	if result.error == nil || result.error == http.ErrServerClosed {
		klog.Infof("Collected metrics %s goroutine.", result.name)
	} else {
		klog.Errorf("Collected metrics %s goroutine: %v", result.name, result.error)
		lastError = result.error
	}
	return lastError
}

type MetricsOptions struct {
	ListenAddress string

	ServingCertFile string
	ServingKeyFile  string

	DisableAuthentication bool
	DisableAuthorization  bool
}

// RunMetrics launches an HTTPS server bound to listenAddress serving
// Prometheus metrics at /metrics. If configured, enforces mTLS (mutual TLS)
// for client authentication and uses a CN-based authorization.
//
// Continues serving until runContext.Done() and then attempts a clean
// shutdown limited by shutdownContext.Done(). Assumes runContext.Done()
// occurs before or simultaneously with shutdownContext.Done().
func RunMetrics(runContext context.Context, shutdownContext context.Context, restConfig *rest.Config, options MetricsOptions) error {
	if options.ListenAddress == "" {
		return errors.New("listen address is required to serve metrics")
	}

	if options.DisableAuthentication && !options.DisableAuthorization {
		return errors.New("invalid configuration: cannot enable authorization without authentication")
	}

	// Prepare synchronization for to-be created go routines
	metricsContext, metricsContextCancel := context.WithCancel(runContext)
	defer metricsContextCancel()

	resultChannel := make(chan asyncResult, 1)
	resultChannelCount := 0

	// Create a dynamic serving cert/key controller to watch for serving certificate changes from files.
	servingContentController, err := dynamiccertificates.NewDynamicServingContentFromFiles(
		"metrics-serving-cert",
		options.ServingCertFile,
		options.ServingKeyFile,
	)
	if err != nil {
		return fmt.Errorf("failed to create serving certificate controller: %w", err)
	}
	if err := servingContentController.RunOnce(metricsContext); err != nil {
		return fmt.Errorf("failed to initialize serving content controller: %w", err)
	}

	// Start the serving cert controller to begin watching the cert and key files
	resultChannelCount++
	go func() {
		servingContentController.Run(metricsContext, 1)
		resultChannel <- asyncResult{name: "serving content controller"}
	}()

	clientAuth := tls.NoClientCert
	var clientCA dynamiccertificates.CAContentProvider
	var clientCAController *dynamiccertificates.ConfigMapCAController
	if !options.DisableAuthentication {
		// Create a dynamic CA controller to watch for client CA changes from a ConfigMap.
		kubeClient, err := kubernetes.NewForConfig(restConfig)
		if err != nil {
			return fmt.Errorf("failed to create kube client: %w", err)
		}

		clientCAController, err = dynamiccertificates.NewDynamicCAFromConfigMapController(
			"metrics-client-ca",
			"kube-system",
			"extension-apiserver-authentication",
			"client-ca-file",
			kubeClient)
		if err != nil {
			return fmt.Errorf("failed to create client CA controller: %w", err)
		}

		if err := clientCAController.RunOnce(metricsContext); err != nil {
			return fmt.Errorf("failed to initialize client CA controller: %w", err)
		}

		// Start the client CA controller to begin watching the ConfigMap
		resultChannelCount++
		go func() {
			clientCAController.Run(metricsContext, 1)
			resultChannel <- asyncResult{name: "client CA from ConfigMap controller"}
		}()

		// Assign to interface variable to ensure proper nil handling
		clientCA = clientCAController

		// Request client certificates but don't verify at TLS layer.
		// Verification happens in the HTTP handler, which allows returning
		// HTTP error codes that are expected by the origin test suite, instead of TLS errors.
		clientAuth = tls.RequestClientCert
	}

	// Log certificate controller events to stdout because the controller is reported to generate invalid events,
	// which are rejected by the Kubernetes API server when used with DynamicServingContentFromFiles.
	eventBroadcaster := record.NewBroadcaster(record.WithContext(metricsContext))
	eventBroadcaster.StartLogging(klog.Infof)
	defer eventBroadcaster.Shutdown()

	// baseTlSConfig is a template passed to servingCertController,
	// which generates updated configs via GetConfigForClient callback on each TLS handshake.
	// This enables automatic certificate rotation without server restarts.
	baseTlSConfig := crypto.SecureTLSConfig(&tls.Config{ClientAuth: clientAuth})
	servingCertController := dynamiccertificates.NewDynamicServingCertificateController(
		baseTlSConfig,
		clientCA,
		servingContentController,
		nil,
		record.NewEventRecorderAdapter(
			eventBroadcaster.NewRecorder(scheme.Scheme, corev1.EventSource{Component: "cluster-version-operator"}),
		),
	)
	if err := servingCertController.RunOnce(); err != nil {
		return fmt.Errorf("failed to initialize serving certificate controller: %w", err)
	}

	// Register listeners so servingCertController is notified when certificates change.
	if clientCAController != nil {
		clientCAController.AddListener(servingCertController)
	}
	servingContentController.AddListener(servingCertController)

	resultChannelCount++
	go func() {
		servingCertController.Run(1, metricsContext.Done())
		resultChannel <- asyncResult{name: "serving certification controller"}
	}()

	server := createHttpServer(options, clientCA)
	tlsConfig := crypto.SecureTLSConfig(&tls.Config{
		GetConfigForClient: func(clientHello *tls.ClientHelloInfo) (*tls.Config, error) {
			config, err := servingCertController.GetConfigForClient(clientHello)
			if err != nil {
				return nil, err
			}
			if config == nil {
				// To ensure we rather safely fail connections when the desired config is nil. Safety over availability.
				err := fmt.Errorf("serving certificate controller returned nil TLS configuration")
				return nil, err
			}
			return config, nil
		},
	})

	resultChannelCount++
	go func() {
		startListening(server, tlsConfig, options.ListenAddress, resultChannel)
	}()

	// Wait for server to exit or shutdown signal
	shutdown := false
	var loopError error
	for resultChannelCount > 0 {
		klog.Infof("Waiting on %d outstanding goroutines.", resultChannelCount)
		if shutdown {
			select {
			case result := <-resultChannel:
				resultChannelCount--
				loopError = handleServerResult(result, loopError)
			case <-shutdownContext.Done(): // out of time
				klog.Errorf("Abandoning %d uncollected metrics goroutines", resultChannelCount)
				return shutdownContext.Err()
			}
		} else {
			select {
			case <-metricsContext.Done():
				klog.Infof("Clean metrics shutdown requested: %v", metricsContext.Err())
			case result := <-resultChannel:
				resultChannelCount--
				loopError = handleServerResult(result, loopError)
			}
			shutdown = true
			shutdownError := server.Shutdown(shutdownContext)
			if shutdownError != nil { // log the error we are discarding
				klog.Errorf("Failed to gracefully shut down metrics server: %v", shutdownError)
			}

			if loopError == nil {
				loopError = shutdownError
			}

			// Request remaining go routines to shut down
			metricsContextCancel()
		}
	}

	klog.Infof("Graceful shutdown complete for metrics server: %s", loopError)
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
	types := sets.Set[string]{}
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
	ch <- m.capability.WithLabelValues("").Desc()
	ch <- m.clusterOperatorUp.WithLabelValues("", "", "").Desc()
	ch <- m.clusterOperatorConditions.WithLabelValues("", "", "").Desc()
	ch <- m.clusterVersionRiskConditions.WithLabelValues("", "", "").Desc()
	ch <- m.clusterOperatorConditionTransitions.WithLabelValues("", "").Desc()
	ch <- m.clusterInstaller.WithLabelValues("", "", "").Desc()
	ch <- m.clusterVersionOperatorUpdateRetrievalTimestampSeconds.WithLabelValues("").Desc()
	ch <- m.clusterVersionConditionalUpdateConditionSeconds.WithLabelValues("", "", "", "").Desc()
}

func (m *operatorMetrics) collectConditionalUpdates(ch chan<- prometheus.Metric, updates []configv1.ConditionalUpdate) {
	for _, update := range updates {
		for _, condition := range update.Conditions {
			if condition.Type != internal.ConditionalUpdateConditionTypeRecommended {
				continue
			}

			g := m.clusterVersionConditionalUpdateConditionSeconds
			gauge := g.WithLabelValues(update.Release.Version, condition.Type, string(condition.Status), condition.Reason)
			gauge.Set(float64(condition.LastTransitionTime.Unix()))
			ch <- gauge
		}
	}
}

func (m *operatorMetrics) collectConditionalUpdateRisks(ch chan<- prometheus.Metric, risks []configv1.ConditionalUpdateRisk) {
	for _, risk := range risks {
		for _, condition := range risk.Conditions {
			if condition.Type != internal.ConditionalUpdateRiskConditionTypeApplies {
				continue
			}

			g := m.clusterVersionRiskConditions.WithLabelValues("version", condition.Type, risk.Name)
			if condition.Status == metav1.ConditionTrue {
				g.Set(1)
			} else {
				g.Set(0)
			}
			ch <- g
		}
	}
}

// Collect collects metrics from the operator into the channel ch
func (m *operatorMetrics) Collect(ch chan<- prometheus.Metric) {
	current := m.optr.currentVersion()
	var completed configv1.UpdateHistory

	if cv, err := m.optr.cvLister.Get(m.optr.name); apierrors.IsNotFound(err) {
		g := m.clusterOperatorUp.WithLabelValues("version", "", "ClusterVersionNotFound")
		g.Set(0)
		ch <- g

		g = m.clusterOperatorConditions.WithLabelValues("version", string(configv1.OperatorAvailable), "ClusterVersionNotFound")
		g.Set(0)
		ch <- g
	} else if err == nil {

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
		failing := resourcemerge.FindOperatorStatusCondition(cv.Status.Conditions, internal.ClusterStatusFailing)
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
			if len(m.optr.updateService) > 0 {
				upstream = string(m.optr.updateService)
			} else if len(cv.Spec.Upstream) > 0 {
				upstream = string(cv.Spec.Upstream)
			}
			g := m.availableUpdates.WithLabelValues(upstream, cv.Spec.Channel)
			g.Set(float64(len(cv.Status.AvailableUpdates)))
			ch <- g
		}

		enabledCapabilities := make(map[configv1.ClusterVersionCapability]struct{}, len(cv.Status.Capabilities.EnabledCapabilities))
		for _, capability := range cv.Status.Capabilities.EnabledCapabilities {
			g := m.capability.WithLabelValues(string(capability))
			g.Set(float64(1))
			ch <- g
			enabledCapabilities[capability] = struct{}{}
		}
		for _, capability := range cv.Status.Capabilities.KnownCapabilities {
			if _, ok := enabledCapabilities[capability]; !ok {
				g := m.capability.WithLabelValues(string(capability))
				g.Set(float64(0))
				ch <- g
			}
		}

		for _, condition := range cv.Status.Conditions {
			if condition.Status != configv1.ConditionFalse && condition.Status != configv1.ConditionTrue {
				klog.V(2).Infof("skipping metrics for ClusterVersion condition %s=%s (neither True nor False)", condition.Type, condition.Status)
				continue
			}

			if condition.Type == configv1.OperatorAvailable {
				g = m.clusterOperatorUp.WithLabelValues("version", completed.Version, string(condition.Reason))
				if condition.Status == configv1.ConditionTrue {
					g.Set(1)
				} else {
					g.Set(0)
				}
				ch <- g
			}

			g = m.clusterOperatorConditions.WithLabelValues("version", string(condition.Type), string(condition.Reason))
			if condition.Status == configv1.ConditionTrue {
				g.Set(1)
			} else {
				g.Set(0)
			}
			ch <- g
		}

		m.collectConditionalUpdates(ch, cv.Status.ConditionalUpdates)
		if m.optr.shouldReconcileAcceptRisks() {
			m.collectConditionalUpdateRisks(ch, cv.Status.ConditionalUpdateRisks)
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
		var version string
		for _, v := range op.Status.Versions {
			if v.Name == "operator" {
				version = v.Version
				break
			}
		}
		if version == "" {
			klog.V(2).Infof("ClusterOperator %s is not setting the 'operator' version", op.Name)
		}
		var isUp float64
		reason := "NoAvailableCondition"
		if condition := resourcemerge.FindOperatorStatusCondition(op.Status.Conditions, configv1.OperatorAvailable); condition != nil {
			reason = condition.Reason
			if condition.Status == configv1.ConditionTrue {
				isUp = 1
			}
		}
		g := m.clusterOperatorUp.WithLabelValues(op.Name, version, reason)
		g.Set(isUp)
		ch <- g
		for _, condition := range op.Status.Conditions {
			if condition.Status != configv1.ConditionFalse && condition.Status != configv1.ConditionTrue {
				klog.V(2).Infof("skipping metrics for %s ClusterOperator condition %s=%s (neither True nor False)", op.Name, condition.Type, condition.Status)
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
		klog.Warningf("ConfigMap %s not found api error: %v", internal.ManifestsConfigMap, err)
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
			if t.After(latest) {
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

package internal

import "k8s.io/klog/v2"

const (
	ConfigNamespace        = "openshift-config"
	ConfigManagedNamespace = "openshift-config-managed"
	AdminGatesConfigMap    = "admin-gates"
	AdminAcksConfigMap     = "admin-acks"
	InstallerConfigMap     = "openshift-install"
	ManifestsConfigMap     = "openshift-install-manifests"
)

var (
	// Normal is the default. Normal, working log information, everything is fine, but helpful notices for auditing or
	// common operations. In kube, this is probably glog=2.
	Normal klog.Level = 2

	// Debug is used when something went wrong. Even common operations may be logged, and less helpful but more
	// quantity of notices. In kube, this is probably glog=4.
	Debug klog.Level = 4

	// Trace is used when something went really badly and even more verbose logs are needed. Logging every function
	// call as part of a common operation, to tracing execution of a query. In kube, this is probably glog=6.
	Trace klog.Level = 6

	// TraceAll is used when something is broken at the level of API content/decoding. It will dump complete body
	// content. If you turn this on in a production cluster prepare from serious performance issues and massive
	// amounts of logs. In kube, this is probably glog=8.
	TraceAll klog.Level = 8
)

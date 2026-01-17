package internal

import (
	"k8s.io/klog/v2"

	configv1 "github.com/openshift/api/config/v1"
)

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

const (
	// ClusterStatusFailing is set on the ClusterVersion status when a cluster
	// cannot reach the desired state. It is considered more serious than Degraded
	// and indicates the cluster is not healthy.
	ClusterStatusFailing configv1.ClusterStatusConditionType = "Failing"

	// ClusterVersionInvalid indicates that the cluster version has an error that prevents the server from
	// taking action. The cluster version operator will only reconcile the current state as long as this
	// condition is set.
	ClusterVersionInvalid configv1.ClusterStatusConditionType = "Invalid"

	// ReleaseAccepted indicates whether the requested (desired) release payload was successfully loaded
	// and no failures occurred during image verification and precondition checking.
	ReleaseAccepted configv1.ClusterStatusConditionType = "ReleaseAccepted"

	// ImplicitlyEnabledCapabilities is True if there are enabled capabilities which the user is not currently
	// requesting via spec.capabilities, because the cluster version operator does not support uninstalling
	// capabilities if any associated resources were previously managed by the CVO or disabling previously
	// enabled capabilities.
	ImplicitlyEnabledCapabilities configv1.ClusterStatusConditionType = "ImplicitlyEnabledCapabilities"

	// UpgradeableAdminAckRequired is False if there is API removed from the Kubernetes API server which requires admin
	// consideration, and thus update to the next minor or major version is blocked.
	UpgradeableAdminAckRequired configv1.ClusterStatusConditionType = "UpgradeableAdminAckRequired"
	// UpgradeableDeletesInProgress is False if deleting resources is in progress, and thus update to the next minor or major
	// version is blocked.
	UpgradeableDeletesInProgress configv1.ClusterStatusConditionType = "UpgradeableDeletesInProgress"
	// UpgradeableClusterOperators is False if something is wrong with Cluster Operators, and thus update to the next minor or major
	// version is blocked.
	UpgradeableClusterOperators configv1.ClusterStatusConditionType = "UpgradeableClusterOperators"
	// UpgradeableClusterVersionOverrides is False if there are overrides in the Cluster Version, and thus update to the next minor or major
	// version is blocked.
	UpgradeableClusterVersionOverrides configv1.ClusterStatusConditionType = "UpgradeableClusterVersionOverrides"

	// UpgradeableUpgradeInProgress is True if an update is in progress.
	UpgradeableUpgradeInProgress configv1.ClusterStatusConditionType = "UpgradeableUpgradeInProgress"
)

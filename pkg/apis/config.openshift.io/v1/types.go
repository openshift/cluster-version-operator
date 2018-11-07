package v1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

// ClusterVersionList is a list of ClusterVersion resources.
// +k8s:deepcopy-gen=true
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
type ClusterVersionList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata"`

	Items []ClusterVersion `json:"items"`
}

// ClusterVersion is the configuration for the ClusterVersionOperator. This is where
// parameters related to automatic updates can be set.
// +genclient
// +genclient:nonNamespaced
// +k8s:deepcopy-gen=true
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
type ClusterVersion struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	// Spec is the desired state of the cluster version - the operator will work
	// to ensure that the desired version is applied to the cluster.
	Spec ClusterVersionSpec `json:"spec"`
	// Status contains information about the available updates and any in-progress
	// updates.
	Status ClusterVersionStatus `json:"status"`
}

// ClusterVersionSpec is the desired version state of the cluster. It includes
// the version the cluster should be at, how the cluster is identified, and
// where the cluster should look for version updates.
// +k8s:deepcopy-gen=true
type ClusterVersionSpec struct {
	// ClusterID uniquely identifies this cluster. This is expected to be
	// an RFC4122 UUID value (xxxxxxxx-xxxx-xxxx-xxxx-xxxxxxxxxxxx in
	// hexadecimal values). This is a required field.
	ClusterID ClusterID `json:"clusterID"`

	// DesiredUpdate is an optional field that indicates the desired value of
	// the cluster version. Setting this value will trigger an upgrade (if
	// the current version does not match the desired version). The set of
	// recommended update values is listed as part of available updates in
	// status, and setting values outside that range may cause the upgrade
	// to fail.
	//
	// If an upgrade fails the operator will halt and report status
	// about the failing component. Setting the desired update value back to
	// the previous version will cause a rollback to be attempted. Not all
	// rollbacks will succeed.
	//
	// +optional
	DesiredUpdate *Update `json:"desiredUpdate"`

	// Upstream may be used to specify the preferred update server. By default
	// it will use the appropriate update server for the cluster and region.
	//
	// +optional
	Upstream *URL `json:"upstream"`
	// Channel is an identifier for explicitly requesting that a non-default
	// set of updates be applied to this cluster. The default channel will be
	// contain stable updates that are appropriate for production clusters.
	//
	// +optional
	Channel string `json:"channel"`

	// Overrides is list of overides for components that are managed by
	// cluster version operator. Marking a component unmanaged will prevent
	// the operator from creating or updating the object.
	// +optional
	Overrides []ComponentOverride `json:"overrides,omitempty"`
}

// ClusterVersionStatus reports the status of the cluster versioning,
// including any upgrades that are in progress. The current field will
// be set to whichever version the cluster is reconciling to, and the
// conditions array will report whether the update succeeded, is in
// progress, or is failing.
// +k8s:deepcopy-gen=true
type ClusterVersionStatus struct {
	// current is the version that the cluster will be reconciled to. This
	// value may be empty during cluster startup, and then will be set whenever
	// a new update is being applied. Use the conditions array to know whether
	// the update is complete.
	Current Update `json:"current"`

	// generation reports which version of the spec is being processed.
	// If this value is not equal to metadata.generation, then the
	// current and conditions fields have not yet been updated to reflect
	// the latest request.
	Generation int64 `json:"generation"`

	// versionHash is a fingerprint of the content that the cluster will be
	// updated with. It is used by the operator to avoid unnecessary work
	// and is for internal use only.
	VersionHash string `json:"versionHash"`

	// conditions provides information about the cluster version. The condition
	// "Available" is set to true if the desiredUpdate has been reached. The
	// condition "Progressing" is set to true if an update is being applied.
	// The condition "Failing" is set to true if an update is currently blocked
	// by a temporary or permanent error. Conditions are only valid for the
	// current desiredUpdate when metadata.generation is equal to
	// status.generation.
	Conditions []ClusterOperatorStatusCondition `json:"conditions"`

	// AvailableUpdates contains the list of updates that are appropriate
	// for this cluster. This list may be empty if no updates are recommended,
	// if the update service is unavailable, or if an invalid channel has
	// been specified.
	AvailableUpdates []Update `json:"availableUpdates"`
}

// ClusterID is string RFC4122 uuid.
type ClusterID string

// ComponentOverride allows overriding cluster version operator's behavior
// for a component.
// +k8s:deepcopy-gen=true
type ComponentOverride struct {
	// Kind should match the TypeMeta.Kind for object.
	Kind string `json:"kind"`

	// The Namespace and Name for the component.
	Namespace string `json:"namespace"`
	Name      string `json:"name"`

	// Unmanaged controls if cluster version operator should stop managing.
	// Default: false
	Unmanaged bool `json:"unmanaged"`
}

// URL is a thin wrapper around string that ensures the string is a valid URL.
type URL string

// Update represents a release of the ClusterVersionOperator, referenced by the
// Payload member.
// +k8s:deepcopy-gen=true
type Update struct {
	Version string `json:"version"`
	Payload string `json:"payload"`
}

// ClusterOperatorList is a list of OperatorStatus resources.
// +k8s:deepcopy-gen=true
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
type ClusterOperatorList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata"`

	Items []ClusterOperator `json:"items"`
}

// ClusterOperator is the Custom Resource object which holds the current state
// of an operator. This object is used by operators to convey their state to
// the rest of the cluster.
// +genclient
// +k8s:deepcopy-gen=true
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
type ClusterOperator struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata"`

	// Spec hold the intent of how this operator should behave.
	Spec ClusterOperatorSpec `json:"spec"`

	// status holds the information about the state of an operator.  It is consistent with status information across
	// the kube ecosystem.
	Status ClusterOperatorStatus `json:"status"`
}

// ClusterOperatorSpec is empty for now, but you could imagine holding information like "pause".
type ClusterOperatorSpec struct {
}

// ClusterOperatorStatus provides information about the status of the operator.
// +k8s:deepcopy-gen=true
type ClusterOperatorStatus struct {
	// conditions describes the state of the operator's reconciliation functionality.
	// +patchMergeKey=type
	// +patchStrategy=merge
	Conditions []ClusterOperatorStatusCondition `json:"conditions"`

	// version indicates which version of the operator updated the current
	// status object.
	Version string `json:"version"`

	// extension contains any additional status information specific to the
	// operator which owns this status object.
	Extension runtime.RawExtension `json:"extension,omitempty"`
}

type ConditionStatus string

// These are valid condition statuses. "ConditionTrue" means a resource is in the condition.
// "ConditionFalse" means a resource is not in the condition. "ConditionUnknown" means kubernetes
// can't decide if a resource is in the condition or not. In the future, we could add other
// intermediate conditions, e.g. ConditionDegraded.
const (
	ConditionTrue    ConditionStatus = "True"
	ConditionFalse   ConditionStatus = "False"
	ConditionUnknown ConditionStatus = "Unknown"
)

// ClusterOperatorStatusCondition represents the state of the operator's
// reconciliation functionality.
// +k8s:deepcopy-gen=true
type ClusterOperatorStatusCondition struct {
	// type specifies the state of the operator's reconciliation functionality.
	Type ClusterStatusConditionType `json:"type"`

	// Status of the condition, one of True, False, Unknown.
	Status ConditionStatus `json:"status"`

	// LastTransitionTime is the time of the last update to the current status object.
	LastTransitionTime metav1.Time `json:"lastTransitionTime"`

	// reason is the reason for the condition's last transition.  Reasons are CamelCase
	Reason string `json:"reason,omitempty"`

	// message provides additional information about the current condition.
	// This is only to be consumed by humans.
	Message string `json:"message,omitempty"`
}

// ClusterStatusConditionType is the state of the operator's reconciliation functionality.
type ClusterStatusConditionType string

const (
	// OperatorAvailable indicates that the binary maintained by the operator (eg: openshift-apiserver for the
	// openshift-apiserver-operator), is functional and available in the cluster.
	OperatorAvailable ClusterStatusConditionType = "Available"

	// OperatorProgressing indicates that the operator is actively making changes to the binary maintained by the
	// operator (eg: openshift-apiserver for the openshift-apiserver-operator).
	OperatorProgressing ClusterStatusConditionType = "Progressing"

	// OperatorFailing indicates that the operator has encountered an error that is preventing it from working properly.
	// The binary maintained by the operator (eg: openshift-apiserver for the openshift-apiserver-operator) may still be
	// available, but the user intent cannot be fulfilled.
	OperatorFailing ClusterStatusConditionType = "Failing"

	// RetrievedUpdates reports whether available updates have been retrieved from
	// the upstream update server. The condition is Unknown before retrieval, False
	// if the updates could not be retrieved or recently failed, or True if the
	// availableUpdates field is accurate and recent.
	RetrievedUpdates ClusterStatusConditionType = "RetrievedUpdates"
)

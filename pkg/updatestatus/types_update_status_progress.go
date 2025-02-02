package updatestatus

import metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

// ClusterVersionStatusInsight reports the state of a ClusterVersion resource (which represents a control plane
// update in standalone clusters), during the update.
type ClusterVersionStatusInsight struct {
	// resource is the ClusterVersion resource that represents the control plane
	//
	// Note: By OpenShift API conventions, in isolation this should be a specialized reference that refers just to
	// resource name (because the rest is implied by status insight type). However, because we use resource references in
	// many places and this API is intended to be consumed by clients, not produced, consistency seems to be more valuable
	// than type safety for producers.
	// +kubebuilder:validation:Required
	Resource ResourceRef `json:"resource"`

	// assessment is the assessment of the control plane update process
	// +kubebuilder:validation:Required
	Assessment ControlPlaneAssessment `json:"assessment"`

	// versions contains the original and target versions of the upgrade
	// +kubebuilder:validation:Required
	Versions ControlPlaneUpdateVersions `json:"versions"`

	// completion is a percentage of the update completion (0-100)
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Minimum=0
	// +kubebuilder:validation:Maximum=100
	Completion int32 `json:"completion"`

	// startedAt is the time when the update started
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Type=string
	// +kubebuilder:validation:Format=date-time
	StartedAt metav1.Time `json:"startedAt"`

	// completedAt is the time when the update completed
	// +optional
	// +kubebuilder:validation:Type=string
	// +kubebuilder:validation:Format=date-time
	CompletedAt *metav1.Time `json:"completedAt,omitempty"`

	// estimatedCompletedAt is the estimated time when the update will complete
	// +optional
	// +kubebuilder:validation:Type=string
	// +kubebuilder:validation:Format=date-time
	EstimatedCompletedAt *metav1.Time `json:"estimatedCompletedAt,omitempty"`

	// conditions provides detailed observed conditions about ClusterVersion
	// +listType=map
	// +listMapKey=type
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// ControlPlaneAssessment is the assessment of the control plane update process
type ControlPlaneAssessment string

const (
	// Unknown means the update status and health cannot be determined
	ControlPlaneAssessmentUnknown ControlPlaneAssessment = "Unknown"
	// Progressing means the control plane is updating and no problems or slowness are detected
	ControlPlaneAssessmentProgressing ControlPlaneAssessment = "Progressing"
	// Completed means the control plane successfully completed updating and no problems are detected
	ControlPlaneAssessmentCompleted ControlPlaneAssessment = "Completed"
	// Degraded means the process of updating the control plane suffers from an observed problem
	ControlPlaneAssessmentDegraded ControlPlaneAssessment = "Degraded"
)

// ControlPlaneUpdateVersions contains the original and target versions of the upgrade
type ControlPlaneUpdateVersions struct {
	// previous is the version of the control plane before the update. When the cluster is being installed
	// for the first time, the version will have a placeholder value like '<none>' and the target version
	// will have a boolean installation=true metadata
	// +kubebuilder:validation:Required
	Previous Version `json:"previous"`

	// target is the version of the control plane after the update
	// +kubebuilder:validation:Required
	Target Version `json:"target"`
}

// Version describes a version involved in an update, typically on one side of an update edge
type Version struct {
	// version is a semantic version string, or a placeholder '<none>' for the special case where this
	// is a "previous" version in a new installation
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Type=string
	// +kubebuilder:validation:MaxLength=64
	Version string `json:"version"`

	// metadata is a list of metadata associated with the version. It is a list of key-value pairs. The value is optional
	// and when not provided, the metadata item has boolean semantics (presence indicates true)
	// +listType=map
	// +listMapKey=key
	// +optional
	Metadata []VersionMetadata `json:"metadata,omitempty"`
}

type VersionMetadata struct {
	// key is the name of this metadata value
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Enum=Installation;Partial;Architecture
	Key VersionMetadataKey `json:"key"`

	// +optional
	// +kubebuilder:validation:Type=string
	// +kubebuilder:validation:MaxLength=32
	Value string `json:"value,omitempty"`
}

// VersionMetadataKey is a key for a metadata value associated with a version
// +kubebuilder:validation:Enum=Installation;Partial;Architecture
type VersionMetadataKey string

const (
	// Installation denotes a boolean that indicates the update was initiated as an installation
	InstallationMetadata VersionMetadataKey = "Installation"
	// Partial denotes a boolean that indicates the update was initiated in a state where the previous upgrade
	// (to the original version) was not fully completed
	PartialMetadata VersionMetadataKey = "Partial"
	// Architecture denotes a string that indicates the architecture of the payload image of the version,
	// when relevant
	ArchitectureMetadata VersionMetadataKey = "Architecture"
)

// ClusterVersionStatusInsightConditionType are types of conditions that can be reported on ClusterVersion status insight
type ClusterVersionStatusInsightConditionType string

const (
	// Updating condition communicates whether the ClusterVersion is updating
	ClusterVersionStatusInsightUpdating ClusterVersionStatusInsightConditionType = "Updating"
)

// ClusterVersionStatusInsightUpdatingReason are well-known reasons for the Updating condition on ClusterVersion status insights
type ClusterVersionStatusInsightUpdatingReason string

const (
	// CannotDetermineUpdating is used with Updating=Unknown
	ClusterVersionCannotDetermineUpdating ClusterVersionStatusInsightUpdatingReason = "CannotDetermineUpdating"
	// ClusterVersionProgressing means that ClusterVersion is considered to be Updating=True because it has a Progressing=True condition
	ClusterVersionProgressing ClusterVersionStatusInsightUpdatingReason = "ClusterVersionProgressing"
	// ClusterVersionNotProgressing means that ClusterVersion is considered to be Updating=False because it has a Progressing=False condition
	ClusterVersionNotProgressing ClusterVersionStatusInsightUpdatingReason = "ClusterVersionNotProgressing"
)

// ClusterOperatorStatusInsight reports the state of a ClusterOperator resource (which represents a control plane
// component update in standalone clusters), during the update
type ClusterOperatorStatusInsight struct {
	// name is the name of the operator
	// +kubebuilder:validation:Required
	Name string `json:"name"`

	// resource is the ClusterOperator resource that represents the operator
	//
	// Note: By OpenShift API conventions, in isolation this should be a specialized reference that refers just to
	// resource name (because the rest is implied by status insight type). However, because we use resource references in
	// many places and this API is intended to be consumed by clients, not produced, consistency seems to be more valuable
	// than type safety for producers.
	// +kubebuilder:validation:Required
	Resource ResourceRef `json:"resource"`

	// conditions provide details about the operator
	// +listType=map
	// +listMapKey=type
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// ClusterOperatorStatusInsightConditionType are types of conditions that can be reported on ClusterOperator status insights
type ClusterOperatorStatusInsightConditionType string

const (
	// Updating condition communicates whether the ClusterOperator is updating
	ClusterOperatorStatusInsightUpdating ClusterOperatorStatusInsightConditionType = "Updating"
	// Healthy condition communicates whether the ClusterOperator is considered healthy
	ClusterOperatorStatusInsightHealthy ClusterOperatorStatusInsightConditionType = "Healthy"
)

// ClusterOperatorUpdatingReason are well-known reasons for the Updating condition on ClusterOperator status insights
type ClusterOperatorUpdatingReason string

const (
	// Updated is used with Updating=False when the ClusterOperator finished updating
	ClusterOperatorUpdatingReasonUpdated ClusterOperatorUpdatingReason = "Updated"
	// Pending is used with Updating=False when the ClusterOperator is not updating and is still running previous version
	ClusterOperatorUpdatingReasonPending ClusterOperatorUpdatingReason = "Pending"
	// Progressing is used with Updating=True when the ClusterOperator is updating
	ClusterOperatorUpdatingReasonProgressing ClusterOperatorUpdatingReason = "Progressing"
	// CannotDetermine is used with Updating=Unknown
	ClusterOperatorUpdatingCannotDetermine ClusterOperatorUpdatingReason = "CannotDetermine"
)

// ClusterOperatorHealthyReason are well-known reasons for the Healthy condition on ClusterOperator status insights
type ClusterOperatorHealthyReason string

const (
	// AsExpected is used with Healthy=True when no issues are observed
	ClusterOperatorHealthyReasonAsExpected ClusterOperatorHealthyReason = "AsExpected"
	// Unavailable is used with Healthy=False when the ClusterOperator has Available=False condition
	ClusterOperatorHealthyReasonUnavailable ClusterOperatorHealthyReason = "Unavailable"
	// Degraded is used with Healthy=False when the ClusterOperator has Degraded=True condition
	ClusterOperatorHealthyReasonDegraded ClusterOperatorHealthyReason = "Degraded"
	// CannotDetermine is used with Healthy=Unknown
	ClusterOperatorHealthyReasonCannotDetermine ClusterOperatorHealthyReason = "CannotDetermine"
)

// ClusterVersionStatusInsight reports the state of a MachineConfigPool resource during the update
type MachineConfigPoolStatusInsight struct {
	// name is the name of the machine config pool
	// +kubebuilder:validation:Required
	Name string `json:"name"`

	// resource is the MachineConfigPool resource that represents the pool
	//
	// Note: By OpenShift API conventions, in isolation this should be a specialized reference that refers just to
	// resource name (because the rest is implied by status insight type). However, because we use resource references in
	// many places and this API is intended to be consumed by clients, not produced, consistency seems to be more valuable
	// than type safety for producers.
	// +kubebuilder:validation:Required
	Resource PoolResourceRef `json:"resource"`

	// scopeType describes whether the pool is a control plane or a worker pool
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Enum=ControlPlane;WorkerPool
	Scope ScopeType `json:"scopeType"`

	// assessment is the assessment of the machine config pool update process
	// +kubebuilder:validation:Required
	Assessment PoolAssessment `json:"assessment"`

	// completion is a percentage of the update completion (0-100)
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Minimum=0
	// +kubebuilder:validation:Maximum=100
	Completion int32 `json:"completion"`

	// summaries is a list of counts of nodes matching certain criteria (e.g. updated, degraded, etc.)
	// +listType=map
	// +listMapKey=type
	// +optional
	Summaries []NodeSummary `json:"summaries,omitempty"`

	// conditions provide details about the machine config pool update
	// +listType=map
	// +listMapKey=type
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// PoolAssessment is the assessment of the node pool update process
type PoolAssessment string

const (
	// Pending means the nodes in the pool will be updated but none have even started yet
	PoolPending PoolAssessment = "Pending"
	// Completed means all nodes in the pool have been updated
	PoolCompleted PoolAssessment = "Completed"
	// Degraded means the process of updating the pool suffers from an observed problem
	PoolDegraded PoolAssessment = "Degraded"
	// Excluded means some (or all) nodes in the pool would be normally updated but a configuration (such as paused MCP)
	// prevents that from happening
	PoolExcluded PoolAssessment = "Excluded"
	// Progressing means the nodes in the pool are being updated and no problems or slowness are detected
	PoolProgressing PoolAssessment = "Progressing"
)

// NodeSummary is a count of nodes matching certain criteria (e.g. updated, degraded, etc.)
type NodeSummary struct {
	// type is the type of the summary
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Enum=Total;Available;Progressing;Outdated;Draining;Excluded;Degraded
	Type NodeSummaryType `json:"type"`

	// count is the number of nodes matching the criteria
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Minimum=0
	Count int32 `json:"count"`
}

// NodeSummaryType are types of summaries (how many nodes match certain criteria, such as updated, degraded, etc.)
// reported for a node pool
// +kubebuilder:validation:Enum=Total;Available;Progressing;Outdated;Draining;Excluded;Degraded
type NodeSummaryType string

const (
	// Total is the total number of nodes in the pool
	NodesTotal NodeSummaryType = "Total"
	// Available is the number of nodes in the pool that are available (accepting workloads)
	NodesAvailable NodeSummaryType = "Available"
	// Progressing is the number of nodes in the pool that are updating
	NodesProgressing NodeSummaryType = "Progressing"
	// Outdated is the number of nodes in the pool that are running an outdated version
	NodesOutdated NodeSummaryType = "Outdated"
	// Draining is the number of nodes in the pool that are being drained
	NodesDraining NodeSummaryType = "Draining"
	// Excluded is the number of nodes in the pool that would normally be updated but configuration (such as paused MCP)
	// prevents that from happening
	NodesExcluded NodeSummaryType = "Excluded"
	// Degraded is the number of nodes in the pool that are degraded
	NodesDegraded NodeSummaryType = "Degraded"
)

// NodeStatusInsight reports the state of a Node during the update
type NodeStatusInsight struct {
	// name is the name of the node
	// +kubebuilder:validation:Required
	Name string `json:"name"`

	// resource is the Node resource that represents the node
	//
	// Note: By OpenShift API conventions, in isolation this should be a specialized reference that refers just to
	// resource name (because the rest is implied by status insight type). However, because we use resource references in
	// many places and this API is intended to be consumed by clients, not produced, consistency seems to be more valuable
	// than type safety for producers.
	// +kubebuilder:validation:Required
	Resource ResourceRef `json:"resource"`

	// poolResource is the resource that represents the pool the node is a member of
	//
	// Note: By OpenShift API conventions, in isolation this should probably be a specialized reference type that allows
	// only the "correct" resource types to be referenced (here, MachineConfigPool or NodePool). However, because we use
	// resource references in many places and this API is intended to be consumed by clients, not produced, consistency
	// seems to be more valuable than type safety for producers.
	// +kubebuilder:validation:Required
	PoolResource PoolResourceRef `json:"poolResource"`

	// scopeType describes whether the node belongs to control plane or a worker pool
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Enum=ControlPlane;WorkerPool
	Scope ScopeType `json:"scopeType"`

	// version is the version of the node, when known
	// +optional
	// +kubebuilder:validation:Type=string
	Version string `json:"version,omitempty"`

	// estToComplete is the estimated time to complete the update, when known
	// +optional
	// +kubebuilder:validation:Type=string
	EstToComplete *metav1.Duration `json:"estToComplete,omitempty"`

	// message is a short human-readable message about the node update status
	// +optional
	Message string `json:"message,omitempty"`

	// conditions provides details about the control plane update
	// +listType=map
	// +listMapKey=type
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// NodeStatusInsightConditionType are types of conditions that can be reported on Node status insights
type NodeStatusInsightConditionType string

const (
	// Updating condition communicates whether the Node is updating
	NodeStatusInsightUpdating NodeStatusInsightConditionType = "Updating"
	// Degraded condition communicates whether the Node is degraded (problem observed)
	NodeStatusInsightDegraded NodeStatusInsightConditionType = "Degraded"
	// Available condition communicates whether the Node is available (accepting workloads)
	NodeStatusInsightAvailable NodeStatusInsightConditionType = "Available"
)

// NodeUpdatingReason are well-known reasons for the Updating condition on Node status insights
type NodeUpdatingReason string

const (
	// Draining is used with Updating=True when the Node is being drained
	NodeDraining NodeUpdatingReason = "Draining"
	// Updating is used with Updating=True when new node configuration is being applied
	NodeUpdating NodeUpdatingReason = "Updating"
	// Rebooting is used with Updating=True when the Node is rebooting into the new version
	NodeRebooting NodeUpdatingReason = "Rebooting"

	// Updated is used with Updating=False when the Node is prevented by configuration from updating
	NodePaused NodeUpdatingReason = "Paused"
	// Updated is used with Updating=False when the Node is waiting to be eventually updated
	NodeUpdatePending NodeUpdatingReason = "Pending"
	// Updated is used with Updating=False when the Node has been updated
	NodeCompleted NodeUpdatingReason = "Completed"

	// CannotDetermine is used with Updating=Unknown
	NodeCannotDetermine NodeUpdatingReason = "CannotDetermine"
)

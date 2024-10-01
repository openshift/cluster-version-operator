// nolint:gosimple
package updatestatus

// This file was downloaded from o/api PR
// Refresh from:
// https://github.com/petr-muller/api/tree/update-status-api/update/v1alpha1
// Revision: d0af39806bf8d60e44b293fed906ea8856173e41

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

// +genclient
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// UpdateStatus is the API about in-progress updates, kept populated by Update Status Controller by
// aggregating and summarizing UpdateInformers
//
// Compatibility level 4: No compatibility is provided, the API can change at any point for any reason. These capabilities should not be used by applications needing long term support.
// +kubebuilder:object:root=true
// +kubebuilder:resource:path=updatestatuses,scope=Namespaced
// +kubebuilder:subresource:status
// +openshift:api-approved.openshift.io=TODO
// +openshift:file-pattern=cvoRunLevel=0000_00,operatorName=cluster-version-operator,operatorOrdering=02
// +openshift:enable:FeatureGate=UpgradeStatus
// +openshift:compatibility-gen:level=4
type UpdateStatus struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	// +kubebuilder:validation:Required
	Spec UpdateStatusSpec `json:"spec"`
	// +optional
	Status UpdateStatusStatus `json:"status,omitempty"`
}

// UpdateStatusSpec is empty for now, can possibly hold configuration for Update Status Controller in the future
type UpdateStatusSpec struct {
}

// +k8s:deepcopy-gen=true

// UpdateStatusStatus is the API about in-progress updates, kept populated by Update Status Controller by
// aggregating and summarizing UpdateInformers
type UpdateStatusStatus struct {
	// ControlPlaneUpdateStatus contains a summary and insights related to the control plane update
	ControlPlane ControlPlaneUpdateStatus `json:"controlPlane"`

	// WorkerPoolsUpdateStatus contains summaries and insights related to the worker pools update
	WorkerPools []PoolUpdateStatus `json:"workerPools"`

	// Conditions provide details about Update Status Controller operational matters
	// +listType=map
	// +listMapKey=type
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

type ControlPlaneConditionType string

const (
	ControlPlaneConditionTypeUpdating ControlPlaneConditionType = "Updating"
)

type ControlPlaneConditionUpdatingReason string

const (
	ControlPlaneConditionUpdatingReasonClusterVersionProgressing        ControlPlaneConditionUpdatingReason = "ClusterVersionProgressing"
	ControlPlaneConditionUpdatingReasonClusterVersionNotProgressing     ControlPlaneConditionUpdatingReason = "ClusterVersionNotProgressing"
	ControlPlaneConditionUpdatingReasonClusterVersionProgressingUnknown ControlPlaneConditionUpdatingReason = "ClusterVersionProgressingUnknown"
	ControlPlaneConditionUpdatingReasonClusterVersionWithoutProgressing ControlPlaneConditionUpdatingReason = "ClusterVersionWithoutProgressing"
)

// ControlPlaneUpdateStatus contains a summary and insights related to the control plane update
type ControlPlaneUpdateStatus struct {
	// Resource is the resource that represents the control plane. It will typically be a ClusterVersion resource
	// in standalone OpenShift and HostedCluster in HCP.
	Resource PoolResourceRef `json:"resource"`

	// Informers is a list of insight producers, each carries a list of insights
	// +listType=map
	// +listMapKey=name
	Informers []UpdateInformer `json:"informers,omitempty"`

	// Conditions provides details about the control plane update
	// +listType=map
	// +listMapKey=type
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// UpdateInformer is an insight producer identified by a name, carrying a list of insights it produced
type UpdateInformer struct {
	// Name is the name of the insight producer
	// +required
	// +kubebuilder:validation:Required
	Name string `json:"name"`

	// Insights is a list of insights produced by this producer
	Insights []UpdateInsight `json:"insights,omitempty"`
}

type ControlPlaneUpdateAssessment string

const (
	ControlPlaneUpdateAssessmentUnknown     ControlPlaneUpdateAssessment = "Unknown"
	ControlPlaneUpdateAssessmentProgressing ControlPlaneUpdateAssessment = "Progressing"
	ControlPlaneUpdateAssessmentCompleted   ControlPlaneUpdateAssessment = "Completed"
	ControlPlaneUpdateAssessmentDegraded    ControlPlaneUpdateAssessment = "Degraded"
)

type ClusterVersionStatusInsightConditionType string

const (
	ClusterVersionStatusInsightConditionTypeUpdating ClusterVersionStatusInsightConditionType = "Updating"
)

type ClusterVersionStatusInsightUpdatingReason string

const (
	ClusterVersionStatusInsightUpdatingReasonCannotDetermineUpdating ClusterVersionStatusInsightUpdatingReason = "CannotDetermineUpdating"
	ClusterVersionStatusInsightUpdatingReasonProgressing             ClusterVersionStatusInsightUpdatingReason = "ClusterVersionProgressing"
	ClusterVersionStatusInsightUpdatingReasonNotProgressing          ClusterVersionStatusInsightUpdatingReason = "ClusterVersionNotProgressing"
)

type VersionMetadataType string

const (
	VersionMetadataTypeString VersionMetadataType = "string"
	VersionMetadataTypeBool   VersionMetadataType = "bool"
	VersionMetadataTypeInt    VersionMetadataType = "int"
)

type VersionMetadataKey string

const (
	// InstallationMetadataKey denotes a boolean that indicates the update was initiated as an installation
	InstallationMetadataKey VersionMetadataKey = "installation"
	// PartialMetadataKey denotes a boolean that indicates the update was initiated in a state where the previous upgrade
	// (to the original version) was not fully completed
	PartialMetadataKey VersionMetadataKey = "partial"
	// ArchitectureMetadataKey denotes a string that indicates the architecture of the payload image of the version,
	// when relevant
	ArchitectureMetadataKey VersionMetadataKey = "architecture"
)

type VersionMetadata struct {
	// +required
	Key VersionMetadataKey `json:"key"`

	// +unionDiscriminator
	// +required
	Type VersionMetadataType `json:"type"`

	// +optional
	String string `json:"string,omitempty"`

	// +optional
	Bool bool `json:"bool,omitempty"`
}

type UpdateEdgeVersion struct {
	// Version is the version of the edge
	Version string `json:"version,omitempty"`

	// Metadata is a list of metadata associated with the version
	// +listType=map
	// +listMapKey=key
	Metadata []VersionMetadata `json:"metadata,omitempty"`
}

// ControlPlaneUpdateVersions contains the original and target versions of the upgrade
type ControlPlaneUpdateVersions struct {
	// Previous is the version of the control plane before the update
	Previous UpdateEdgeVersion `json:"previous,omitempty"`

	// Target is the version of the control plane after the update
	Target UpdateEdgeVersion `json:"target"`
}

type ClusterVersionStatusInsight struct {
	// Resource is the ClusterVersion resource that represents the control plane
	Resource ResourceRef `json:"resource"`

	// Assessment is the assessment of the control plane update process
	Assessment ControlPlaneUpdateAssessment `json:"assessment"`

	// Versions contains the original and target versions of the upgrade
	Versions ControlPlaneUpdateVersions `json:"versions"`

	// Completion is a percentage of the update completion (0-100)
	Completion uint8 `json:"completion"`

	// StartedAt is the time when the update started
	StartedAt metav1.Time `json:"startedAt"`

	// CompletedAt is the time when the update completed
	CompletedAt metav1.Time `json:"completedAt"`

	// EstimatedCompletedAt is the estimated time when the update will complete
	EstimatedCompletedAt metav1.Time `json:"estimatedCompletedAt"`

	// Conditions provides details about the control plane update
	// +listType=map
	// +listMapKey=type
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}
type ClusterOperatorStatusInsightConditionType string

const (
	ClusterOperatorStatusInsightConditionTypeUpdating ClusterOperatorStatusInsightConditionType = "Updating"
	ClusterOperatorStatusInsightConditionTypeHealthy  ClusterOperatorStatusInsightConditionType = "Healthy"
)

type ClusterOperatorStatusInsightUpdatingReason string

const (
	ClusterOperatorStatusInsightUpdatingReasonUpdated        ClusterOperatorStatusInsightUpdatingReason = "Updated"
	ClusterOperatorStatusInsightUpdatingReasonPending        ClusterOperatorStatusInsightUpdatingReason = "Pending"
	ClusterOperatorStatusInsightUpdatingReasonProgressing    ClusterOperatorStatusInsightUpdatingReason = "Progressing"
	ClusterOperatorStatusInsightUpdatingReasonUnknownUpdate  ClusterOperatorStatusInsightUpdatingReason = "UnclearClusterState"
	ClusterOperatorStatusInsightUpdatingReasonUnknownVersion ClusterOperatorStatusInsightUpdatingReason = "UnknownVersion"
)

type ClusterOperatorStatusInsightHealthyReason string

const (
	ClusterOperatorUpdateStatusInsightHealthyReasonAllIsWell        ClusterOperatorStatusInsightHealthyReason = "AllIsWell"
	ClusterOperatorUpdateStatusInsightHealthyReasonUnavailable      ClusterOperatorStatusInsightHealthyReason = "Unavailable"
	ClusterOperatorUpdateStatusInsightHealthyReasonDegraded         ClusterOperatorStatusInsightHealthyReason = "Degraded"
	ClusterOperatorUpdateStatusInsightHealthyReasonMissingAvailable ClusterOperatorStatusInsightHealthyReason = "MissingAvailable"
	ClusterOperatorUpdateStatusInsightHealthyReasonMissingDegraded  ClusterOperatorStatusInsightHealthyReason = "MissingDegraded"
)

type ClusterOperatorStatusInsight struct {
	// Name is the name of the operator
	Name string `json:"name"`

	// Resource is the ClusterOperator resource that represents the operator
	Resource ResourceRef `json:"resource"`

	// Conditions provide details about the operator
	// +listType=map
	// +listMapKey=type
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// PoolUpdateStatus contains a summary and insights related to a node pool update
type PoolUpdateStatus struct {
	// Name is the name of the pool
	Name string `json:"name"`

	// Resource is the resource that represents the pool
	Resource PoolResourceRef `json:"resource"`

	// Informers is a list of insight producers, each carries a list of insights
	// +listType=map
	// +listMapKey=name
	Informers []UpdateInformer `json:"informers,omitempty"`

	// Conditions provide details about the pool
	// +listType=map
	// +listMapKey=type
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

type PoolUpdateAssessment string

const (
	PoolUpdateAssessmentPending     PoolUpdateAssessment = "Pending"
	PoolUpdateAssessmentCompleted   PoolUpdateAssessment = "Completed"
	PoolUpdateAssessmentDegraded    PoolUpdateAssessment = "Degraded"
	PoolUpdateAssessmentExcluded    PoolUpdateAssessment = "Excluded"
	PoolUpdateAssessmentProgressing PoolUpdateAssessment = "Progressing"
)

type PoolNodesSummaryType string

const (
	PoolNodesSummaryTypeTotal       PoolNodesSummaryType = "Total"
	PoolNodesSummaryTypeAvailable   PoolNodesSummaryType = "Available"
	PoolNodesSummaryTypeProgressing PoolNodesSummaryType = "Progressing"
	PoolNodesSummaryTypeOutdated    PoolNodesSummaryType = "Outdated"
	PoolNodesSummaryTypeDraining    PoolNodesSummaryType = "Draining"
	PoolNodesSummaryTypeExcluded    PoolNodesSummaryType = "Excluded"
	PoolNodesSummaryTypeDegraded    PoolNodesSummaryType = "Degraded"
)

type PoolNodesUpdateSummary struct {
	// Type is the type of the summary
	// +required
	// +kubebuilder:validation:Required
	Type PoolNodesSummaryType `json:"type"`

	// Count is the number of nodes matching the criteria
	Count int32 `json:"count"`
}

type MachineConfigPoolStatusInsight struct {
	// Name is the name of the machine config pool
	Name string `json:"name"`

	// Resource is the MachineConfigPool resource that represents the pool
	Resource ResourceRef `json:"resource"`

	// Scope describes whether the pool is a control plane or a worker pool
	Scope ScopeType `json:"scopeType"`

	// Assessment is the assessment of the machine config pool update process
	Assessment PoolUpdateAssessment `json:"assessment"`

	// Completion is a percentage of the update completion (0-100)
	Completion int32 `json:"completion"`

	// Summaries is a list of counts of nodes matching certain criteria (e.g. updated, degraded, etc.)
	// +listType=map
	// +listMapKey=type
	Summaries []PoolNodesUpdateSummary `json:"summaries,omitempty"`

	// Conditions provide details about the machine config pool
	// +listType=map
	// +listMapKey=type
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

type NodeStatusInsightConditionType string

const (
	NodeStatusInsightConditionTypeUpdating  NodeStatusInsightConditionType = "Updating"
	NodeStatusInsightConditionTypeDegraded  NodeStatusInsightConditionType = "Degraded"
	NodeStatusInsightConditionTypeAvailable NodeStatusInsightConditionType = "Available"
)

type NodeStatusInsightUpdatingReason string

const (
	// Updating=True reasons

	NodeStatusInsightUpdatingReasonDraining  NodeStatusInsightUpdatingReason = "Draining"
	NodeStatusInsightUpdatingReasonUpdating  NodeStatusInsightUpdatingReason = "Updating"
	NodeStatusInsightUpdatingReasonRebooting NodeStatusInsightUpdatingReason = "Rebooting"

	// Updating=False reasons

	NodeStatusInsightUpdatingReasonPaused    NodeStatusInsightUpdatingReason = "Paused"
	NodeStatusInsightUpdatingReasonPending   NodeStatusInsightUpdatingReason = "Pending"
	NodeStatusInsightUpdatingReasonCompleted NodeStatusInsightUpdatingReason = "Completed"
)

type NodeStatusInsight struct {
	// Name is the name of the node
	Name string `json:"name"`

	// Resource is the Node resource that represents the node
	Resource ResourceRef `json:"resource"`

	// PoolResource is the resource that represents the pool the node is a member of
	PoolResource PoolResourceRef `json:"poolResource"`

	// Version is the version of the node, when known
	Version string `json:"version,omitempty"`

	// EstToComplete is the estimated time to complete the update, when known
	EstToComplete metav1.Duration `json:"estToComplete,omitempty"`

	// Message is a human-readable message about the node update status
	Message string `json:"message,omitempty"`

	// Conditions provides details about the control plane update
	// +listType=map
	// +listMapKey=type
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

type UpdateInsightType string

const (
	UpdateInsightTypeClusterVersionStatusInsight    UpdateInsightType = "ClusterVersion"
	UpdateInsightTypeClusterOperatorStatusInsight   UpdateInsightType = "ClusterOperator"
	UpdateInsightTypeMachineConfigPoolStatusInsight UpdateInsightType = "MachineConfigPool"
	UpdateInsightTypeNodeStatusInsight              UpdateInsightType = "Node"
	UpdateInsightTypeUpdateHealthInsight            UpdateInsightType = "UpdateHealth"
)

type UpdateInsight struct {
	// +unionDiscriminator
	Type UpdateInsightType `json:"type"`

	// UID identifies an insight over time
	UID string `json:"uid"`

	// AcquiredAt is the time when the data was acquired by the producer
	AcquiredAt metav1.Time `json:"acquisitionTime"`

	// ClusterVersionStatusInsight is a status insight about the state of a control plane update, where
	// the control plane is represented by a ClusterVersion resource usually managed by CVO
	// +optional
	ClusterVersionStatusInsight *ClusterVersionStatusInsight `json:"clusterVersion,omitempty"`

	// ClusterOperatorStatusInsight is a status insight about the state of a control plane cluster operator update
	// represented by a ClusterOperator resource
	// +optional
	ClusterOperatorStatusInsight *ClusterOperatorStatusInsight `json:"clusterOperator,omitempty"`

	// MachineConfigPoolStatusInsight is a status insight about the state of a worker pool update, where the worker pool
	// is represented by a MachineConfigPool resource
	// +optional
	MachineConfigPoolStatusInsight *MachineConfigPoolStatusInsight `json:"machineConfigPool,omitempty"`

	// NodeStatusInsight is a status insight about the state of a worker node update, where the worker node is represented
	// by a Node resource
	// +optional
	NodeStatusInsight *NodeStatusInsight `json:"node,omitempty"`

	// UpdateHealthInsight is a generic health insight about the update. It does not represent a status of any specific
	// resource but surfaces actionable information about the health of the cluster or an update
	// +optional
	UpdateHealthInsight *UpdateHealthInsight `json:"health,omitempty"`
}

// UpdateHealthInsight is a piece of actionable information produced by an insight producer about the health
// of the cluster or an update
type UpdateHealthInsight struct {
	// StartedAt is the time when the condition reported by the insight started
	StartedAt metav1.Time `json:"startedAt"`

	// Scope is list of objects involved in the insight
	// +optional
	Scope UpdateInsightScope `json:"scope,omitempty"`

	// Impact describes the impact the reported condition has on the cluster or update
	Impact UpdateInsightImpact `json:"impact"`

	// Remediation contains ... TODO
	Remediation UpdateInsightRemediation `json:"remediation"`
}

// ScopeType is one of ControlPlane or WorkerPool
// +kubebuilder:validation:Enum=ControlPlane;WorkerPool
type ScopeType string

const (
	ScopeTypeControlPlane ScopeType = "ControlPlane"
	ScopeTypeWorkerPool   ScopeType = "WorkerPool"
)

// UpdateInsightScope is a list of objects involved in the insight
type UpdateInsightScope struct {
	// Type is either ControlPlane or WorkerPool
	// +kubebuilder:validation:Required
	Type ScopeType `json:"type"`

	// Resources is a list of resources involved in the insight
	// +optional
	Resources []ResourceRef `json:"resources,omitempty"`
}

// ResourceRef is a reference to a kubernetes resource, typically involved in an
// insight
type ResourceRef struct {
	// Kind of object being referenced
	Kind string `json:"kind"`

	// APIGroup of the object being referenced
	// +optional
	APIGroup string `json:"apiGroup,omitempty"`

	// Name of the object being referenced
	Name string `json:"name"`

	// Namespace of the object being referenced, if any
	// +optional
	Namespace string `json:"namespace,omitempty"`
}

// InsightImpactLevel describes the severity of the impact the reported condition has on the cluster or update
// +kubebuilder:validation:Enum=info;warning;error;critical
type InsightImpactLevel string

const (
	// InfoImpactLevel should be used for insights that are strictly informational or even positive (things go well or
	// something recently healed)
	InfoImpactLevel InsightImpactLevel = "info"
	// WarningImpactLevel should be used for insights that explain a minor or transient problem. Anything that requires
	// admin attention or manual action should not be a warning but at least an error.
	WarningImpactLevel InsightImpactLevel = "warning"
	// ErrorImpactLevel should be used for insights that inform about a problem that requires admin attention. Insights of
	// level error and higher should be as actionable as possible, and should be accompanied by links to documentation,
	// KB articles or other resources that help the admin to resolve the problem.
	ErrorImpactLevel InsightImpactLevel = "error"
	// CriticalInfoLevel should be used rarely, for insights that inform about a severe problem, threatening with data
	// loss, destroyed cluster or other catastrophic consequences. Insights of this level should be accompanied by
	// links to documentation, KB articles or other resources that help the admin to resolve the problem, or at least
	// prevent the severe consequences from happening.
	CriticalInfoLevel InsightImpactLevel = "critical"
)

// InsightImpactType describes the type of the impact the reported condition has on the cluster or update
// +kubebuilder:validation:Enum=None;Unknown;API Availability;Cluster Capacity;Application Availability;Application Outage;Data Loss;Update Speed;Update Stalled
type InsightImpactType string

const (
	NoneImpactType                    InsightImpactType = "None"
	UnknownImpactType                 InsightImpactType = "Unknown"
	ApiAvailabilityImpactType         InsightImpactType = "API Availability"
	ClusterCapacityImpactType         InsightImpactType = "Cluster Capacity"
	ApplicationAvailabilityImpactType InsightImpactType = "Application Availability"
	ApplicationOutageImpactType       InsightImpactType = "Application Outage"
	DataLossImpactType                InsightImpactType = "Data Loss"
	UpdateSpeedImpactType             InsightImpactType = "Update Speed"
	UpdateStalledImpactType           InsightImpactType = "Update Stalled"
)

// UpdateInsightImpact describes the impact the reported condition has on the cluster or update
type UpdateInsightImpact struct {
	// Level is the severity of the impact
	Level InsightImpactLevel `json:"level"`

	// Type is the type of the impact
	Type InsightImpactType `json:"type"`

	// Summary is a short summary of the impact
	Summary string `json:"summary"`

	// Description is a human-oriented description of the condition reported by the insight
	Description string `json:"description"`
}

// UpdateInsightRemediation contains ... TODO
type UpdateInsightRemediation struct {
	// Reference is a URL where administrators can find information to resolve or prevent the reported condition
	Reference string `json:"reference"`

	// EstimatedFinish is the estimated time when the informer expects the condition to be resolved, if applicable.
	// This should normally only be provided by system level insights (impact level=status)
	EstimatedFinish metav1.Time `json:"estimatedFinish"`
}

// PoolResourceRef is a reference to a kubernetes resource that represents a worker pool
type PoolResourceRef struct {
	ResourceRef `json:",inline"`
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// UpdateStatusList is a list of UpdateStatus resources
//
// Compatibility level 4: No compatibility is provided, the API can change at any point for any reason. These capabilities should not be used by applications needing long term support.
// +openshift:compatibility-gen:level=4
type UpdateStatusList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata"`

	Items []UpdateStatus `json:"items"`
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *ClusterOperatorStatusInsight) DeepCopyInto(out *ClusterOperatorStatusInsight) {
	*out = *in
	out.Resource = in.Resource
	if in.Conditions != nil {
		in, out := &in.Conditions, &out.Conditions
		*out = make([]v1.Condition, len(*in))
		for i := range *in {
			(*in)[i].DeepCopyInto(&(*out)[i])
		}
	}
	return
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new ClusterOperatorStatusInsight.
func (in *ClusterOperatorStatusInsight) DeepCopy() *ClusterOperatorStatusInsight {
	if in == nil {
		return nil
	}
	out := new(ClusterOperatorStatusInsight)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *ClusterVersionStatusInsight) DeepCopyInto(out *ClusterVersionStatusInsight) {
	*out = *in
	out.Resource = in.Resource
	in.Versions.DeepCopyInto(&out.Versions)
	in.StartedAt.DeepCopyInto(&out.StartedAt)
	in.CompletedAt.DeepCopyInto(&out.CompletedAt)
	in.EstimatedCompletedAt.DeepCopyInto(&out.EstimatedCompletedAt)
	if in.Conditions != nil {
		in, out := &in.Conditions, &out.Conditions
		*out = make([]v1.Condition, len(*in))
		for i := range *in {
			(*in)[i].DeepCopyInto(&(*out)[i])
		}
	}
	return
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new ClusterVersionStatusInsight.
func (in *ClusterVersionStatusInsight) DeepCopy() *ClusterVersionStatusInsight {
	if in == nil {
		return nil
	}
	out := new(ClusterVersionStatusInsight)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *ControlPlaneUpdateStatus) DeepCopyInto(out *ControlPlaneUpdateStatus) {
	*out = *in
	out.Resource = in.Resource
	if in.Informers != nil {
		in, out := &in.Informers, &out.Informers
		*out = make([]UpdateInformer, len(*in))
		for i := range *in {
			(*in)[i].DeepCopyInto(&(*out)[i])
		}
	}
	if in.Conditions != nil {
		in, out := &in.Conditions, &out.Conditions
		*out = make([]v1.Condition, len(*in))
		for i := range *in {
			(*in)[i].DeepCopyInto(&(*out)[i])
		}
	}
	return
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new ControlPlaneUpdateStatus.
func (in *ControlPlaneUpdateStatus) DeepCopy() *ControlPlaneUpdateStatus {
	if in == nil {
		return nil
	}
	out := new(ControlPlaneUpdateStatus)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *ControlPlaneUpdateVersions) DeepCopyInto(out *ControlPlaneUpdateVersions) {
	*out = *in
	in.Previous.DeepCopyInto(&out.Previous)
	in.Target.DeepCopyInto(&out.Target)
	return
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new ControlPlaneUpdateVersions.
func (in *ControlPlaneUpdateVersions) DeepCopy() *ControlPlaneUpdateVersions {
	if in == nil {
		return nil
	}
	out := new(ControlPlaneUpdateVersions)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *MachineConfigPoolStatusInsight) DeepCopyInto(out *MachineConfigPoolStatusInsight) {
	*out = *in
	out.Resource = in.Resource
	if in.Summaries != nil {
		in, out := &in.Summaries, &out.Summaries
		*out = make([]PoolNodesUpdateSummary, len(*in))
		copy(*out, *in)
	}
	if in.Conditions != nil {
		in, out := &in.Conditions, &out.Conditions
		*out = make([]v1.Condition, len(*in))
		for i := range *in {
			(*in)[i].DeepCopyInto(&(*out)[i])
		}
	}
	return
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new MachineConfigPoolStatusInsight.
func (in *MachineConfigPoolStatusInsight) DeepCopy() *MachineConfigPoolStatusInsight {
	if in == nil {
		return nil
	}
	out := new(MachineConfigPoolStatusInsight)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *NodeStatusInsight) DeepCopyInto(out *NodeStatusInsight) {
	*out = *in
	out.Resource = in.Resource
	out.PoolResource = in.PoolResource
	out.EstToComplete = in.EstToComplete
	if in.Conditions != nil {
		in, out := &in.Conditions, &out.Conditions
		*out = make([]v1.Condition, len(*in))
		for i := range *in {
			(*in)[i].DeepCopyInto(&(*out)[i])
		}
	}
	return
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new NodeStatusInsight.
func (in *NodeStatusInsight) DeepCopy() *NodeStatusInsight {
	if in == nil {
		return nil
	}
	out := new(NodeStatusInsight)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *PoolNodesUpdateSummary) DeepCopyInto(out *PoolNodesUpdateSummary) {
	*out = *in
	return
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new PoolNodesUpdateSummary.
func (in *PoolNodesUpdateSummary) DeepCopy() *PoolNodesUpdateSummary {
	if in == nil {
		return nil
	}
	out := new(PoolNodesUpdateSummary)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *PoolResourceRef) DeepCopyInto(out *PoolResourceRef) {
	*out = *in
	out.ResourceRef = in.ResourceRef
	return
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new PoolResourceRef.
func (in *PoolResourceRef) DeepCopy() *PoolResourceRef {
	if in == nil {
		return nil
	}
	out := new(PoolResourceRef)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *PoolUpdateStatus) DeepCopyInto(out *PoolUpdateStatus) {
	*out = *in
	out.Resource = in.Resource
	if in.Informers != nil {
		in, out := &in.Informers, &out.Informers
		*out = make([]UpdateInformer, len(*in))
		for i := range *in {
			(*in)[i].DeepCopyInto(&(*out)[i])
		}
	}
	if in.Conditions != nil {
		in, out := &in.Conditions, &out.Conditions
		*out = make([]v1.Condition, len(*in))
		for i := range *in {
			(*in)[i].DeepCopyInto(&(*out)[i])
		}
	}
	return
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new PoolUpdateStatus.
func (in *PoolUpdateStatus) DeepCopy() *PoolUpdateStatus {
	if in == nil {
		return nil
	}
	out := new(PoolUpdateStatus)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *ResourceRef) DeepCopyInto(out *ResourceRef) {
	*out = *in
	return
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new ResourceRef.
func (in *ResourceRef) DeepCopy() *ResourceRef {
	if in == nil {
		return nil
	}
	out := new(ResourceRef)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *UpdateEdgeVersion) DeepCopyInto(out *UpdateEdgeVersion) {
	*out = *in
	if in.Metadata != nil {
		in, out := &in.Metadata, &out.Metadata
		*out = make([]VersionMetadata, len(*in))
		copy(*out, *in)
	}
	return
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new UpdateEdgeVersion.
func (in *UpdateEdgeVersion) DeepCopy() *UpdateEdgeVersion {
	if in == nil {
		return nil
	}
	out := new(UpdateEdgeVersion)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *UpdateHealthInsight) DeepCopyInto(out *UpdateHealthInsight) {
	*out = *in
	in.StartedAt.DeepCopyInto(&out.StartedAt)
	in.Scope.DeepCopyInto(&out.Scope)
	out.Impact = in.Impact
	in.Remediation.DeepCopyInto(&out.Remediation)
	return
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new UpdateHealthInsight.
func (in *UpdateHealthInsight) DeepCopy() *UpdateHealthInsight {
	if in == nil {
		return nil
	}
	out := new(UpdateHealthInsight)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *UpdateInformer) DeepCopyInto(out *UpdateInformer) {
	*out = *in
	if in.Insights != nil {
		in, out := &in.Insights, &out.Insights
		*out = make([]UpdateInsight, len(*in))
		for i := range *in {
			(*in)[i].DeepCopyInto(&(*out)[i])
		}
	}
	return
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new UpdateInformer.
func (in *UpdateInformer) DeepCopy() *UpdateInformer {
	if in == nil {
		return nil
	}
	out := new(UpdateInformer)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *UpdateInsight) DeepCopyInto(out *UpdateInsight) {
	*out = *in
	in.AcquiredAt.DeepCopyInto(&out.AcquiredAt)
	if in.ClusterVersionStatusInsight != nil {
		in, out := &in.ClusterVersionStatusInsight, &out.ClusterVersionStatusInsight
		*out = new(ClusterVersionStatusInsight)
		(*in).DeepCopyInto(*out)
	}
	if in.ClusterOperatorStatusInsight != nil {
		in, out := &in.ClusterOperatorStatusInsight, &out.ClusterOperatorStatusInsight
		*out = new(ClusterOperatorStatusInsight)
		(*in).DeepCopyInto(*out)
	}
	if in.MachineConfigPoolStatusInsight != nil {
		in, out := &in.MachineConfigPoolStatusInsight, &out.MachineConfigPoolStatusInsight
		*out = new(MachineConfigPoolStatusInsight)
		(*in).DeepCopyInto(*out)
	}
	if in.NodeStatusInsight != nil {
		in, out := &in.NodeStatusInsight, &out.NodeStatusInsight
		*out = new(NodeStatusInsight)
		(*in).DeepCopyInto(*out)
	}
	if in.UpdateHealthInsight != nil {
		in, out := &in.UpdateHealthInsight, &out.UpdateHealthInsight
		*out = new(UpdateHealthInsight)
		(*in).DeepCopyInto(*out)
	}
	return
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new UpdateInsight.
func (in *UpdateInsight) DeepCopy() *UpdateInsight {
	if in == nil {
		return nil
	}
	out := new(UpdateInsight)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *UpdateInsightImpact) DeepCopyInto(out *UpdateInsightImpact) {
	*out = *in
	return
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new UpdateInsightImpact.
func (in *UpdateInsightImpact) DeepCopy() *UpdateInsightImpact {
	if in == nil {
		return nil
	}
	out := new(UpdateInsightImpact)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *UpdateInsightRemediation) DeepCopyInto(out *UpdateInsightRemediation) {
	*out = *in
	in.EstimatedFinish.DeepCopyInto(&out.EstimatedFinish)
	return
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new UpdateInsightRemediation.
func (in *UpdateInsightRemediation) DeepCopy() *UpdateInsightRemediation {
	if in == nil {
		return nil
	}
	out := new(UpdateInsightRemediation)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *UpdateInsightScope) DeepCopyInto(out *UpdateInsightScope) {
	*out = *in
	if in.Resources != nil {
		in, out := &in.Resources, &out.Resources
		*out = make([]ResourceRef, len(*in))
		copy(*out, *in)
	}
	return
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new UpdateInsightScope.
func (in *UpdateInsightScope) DeepCopy() *UpdateInsightScope {
	if in == nil {
		return nil
	}
	out := new(UpdateInsightScope)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *UpdateStatus) DeepCopyInto(out *UpdateStatus) {
	*out = *in
	out.TypeMeta = in.TypeMeta
	in.ObjectMeta.DeepCopyInto(&out.ObjectMeta)
	out.Spec = in.Spec
	in.Status.DeepCopyInto(&out.Status)
	return
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new UpdateStatus.
func (in *UpdateStatus) DeepCopy() *UpdateStatus {
	if in == nil {
		return nil
	}
	out := new(UpdateStatus)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyObject is an autogenerated deepcopy function, copying the receiver, creating a new runtime.Object.
func (in *UpdateStatus) DeepCopyObject() runtime.Object {
	if c := in.DeepCopy(); c != nil {
		return c
	}
	return nil
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *UpdateStatusList) DeepCopyInto(out *UpdateStatusList) {
	*out = *in
	out.TypeMeta = in.TypeMeta
	in.ListMeta.DeepCopyInto(&out.ListMeta)
	if in.Items != nil {
		in, out := &in.Items, &out.Items
		*out = make([]UpdateStatus, len(*in))
		for i := range *in {
			(*in)[i].DeepCopyInto(&(*out)[i])
		}
	}
	return
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new UpdateStatusList.
func (in *UpdateStatusList) DeepCopy() *UpdateStatusList {
	if in == nil {
		return nil
	}
	out := new(UpdateStatusList)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyObject is an autogenerated deepcopy function, copying the receiver, creating a new runtime.Object.
func (in *UpdateStatusList) DeepCopyObject() runtime.Object {
	if c := in.DeepCopy(); c != nil {
		return c
	}
	return nil
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *UpdateStatusSpec) DeepCopyInto(out *UpdateStatusSpec) {
	*out = *in
	return
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new UpdateStatusSpec.
func (in *UpdateStatusSpec) DeepCopy() *UpdateStatusSpec {
	if in == nil {
		return nil
	}
	out := new(UpdateStatusSpec)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *UpdateStatusStatus) DeepCopyInto(out *UpdateStatusStatus) {
	*out = *in
	in.ControlPlane.DeepCopyInto(&out.ControlPlane)
	if in.WorkerPools != nil {
		in, out := &in.WorkerPools, &out.WorkerPools
		*out = make([]PoolUpdateStatus, len(*in))
		for i := range *in {
			(*in)[i].DeepCopyInto(&(*out)[i])
		}
	}
	if in.Conditions != nil {
		in, out := &in.Conditions, &out.Conditions
		*out = make([]v1.Condition, len(*in))
		for i := range *in {
			(*in)[i].DeepCopyInto(&(*out)[i])
		}
	}
	return
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new UpdateStatusStatus.
func (in *UpdateStatusStatus) DeepCopy() *UpdateStatusStatus {
	if in == nil {
		return nil
	}
	out := new(UpdateStatusStatus)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *VersionMetadata) DeepCopyInto(out *VersionMetadata) {
	*out = *in
	return
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new VersionMetadata.
func (in *VersionMetadata) DeepCopy() *VersionMetadata {
	if in == nil {
		return nil
	}
	out := new(VersionMetadata)
	in.DeepCopyInto(out)
	return out
}

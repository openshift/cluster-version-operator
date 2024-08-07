package v1alpha1

import metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

// +genclient
// +genclient:nonNamespaced
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// UpdateStatus is the API about in-progress updates, kept populated by Update Status Controller by
// aggregating and summarizing UpdateInformers
//
// Compatibility level 4: No compatibility is provided, the API can change at any point for any reason. These capabilities should not be used by applications needing long term support.
// +kubebuilder:object:root=true
// +kubebuilder:resource:path=updatestatuses,scope=Cluster
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

type ControlPlaneUpdateStatusConditionType string

const (
	UpdateProgressing ControlPlaneUpdateStatusConditionType = "UpdateProgressing"
)

// ControlPlaneUpdateStatus contains a summary and insights related to the control plane update
type ControlPlaneUpdateStatus struct {
	// Summary contains a summary of the control plane update, forming an Update Status Controller opinion out of insights
	// available to it
	ControlPlaneUpdateStatusSummary `json:",inline"`

	// Informers is a list of insight producers, each carries a list of insights
	// +listType=map
	// +listMapKey=name
	Informers []UpdateInformer `json:"informers,omitempty"`

	// Conditions provides details about the control plane update
	// +listType=map
	// +listMapKey=type
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// ControlPlaneUpdateStatusSummary contains a summary of the control plane update
type ControlPlaneUpdateStatusSummary struct {
	// Assessment summarizes a high-level status of the update
	Assessment string `json:"assessment"`

	// Versions contains the original and target versions of the upgrade
	Versions ControlPlaneUpdateVersions `json:"versions"`

	// Completion is a percentage of the update completion (0-100)
	Completion int32 `json:"completion"`

	// StartedAt is the time when the update started
	StartedAt metav1.Time `json:"startedAt"`

	// CompletedAt is the time when the update completed
	CompletedAt metav1.Time `json:"completedAt"`

	// EstimatedCompletedAt is the estimated time when the update will complete
	EstimatedCompletedAt metav1.Time `json:"estimatedCompletedAt"`
}

// ControlPlaneUpdateVersions contains the original and target versions of the upgrade
type ControlPlaneUpdateVersions struct {
	// Previous is the version of the control plane before the update
	Previous string `json:"previous,omitempty"`

	// IsPreviousPartial is true if the update was initiated in a state where the previous upgrade (to the original version)
	// was not fully completed
	IsPreviousPartial bool `json:"previousPartial,omitempty"`

	// Target is the version of the control plane after the update
	Target string `json:"target"`

	// IsTargetInstall is true if the current (or last completed) work is an installation, not an upgrade
	IsTargetInstall bool `json:"targetInstall,omitempty"`
}

// PoolUpdateStatus contains a summary and insights related to a worker pool update
// Worker pool is represented by a resource
type PoolUpdateStatus struct {
	// Resource is the resource that represents the worker pool
	Resource PoolResourceRef `json:",inline"`

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

// UpdateInsight is a piece of information produced by an insight producer
type UpdateInsight struct {
	// UID identifies an insight over time
	UID string `json:"uid"`

	// AcquiredAt is the time when the data was acquired by the producer
	AcquiredAt metav1.Time `json:"acquisitionTime"`

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

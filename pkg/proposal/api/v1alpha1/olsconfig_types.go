/*
Copyright 2024.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package v1alpha1

import (
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	resource "k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	configv1 "github.com/openshift/api/config/v1"
)

// OLSConfigSpec defines the desired state of OLSConfig
type OLSConfigSpec struct {
	// +kubebuilder:validation:Required
	// +required
	// +operator-sdk:csv:customresourcedefinitions:type=spec,displayName="LLM Settings"
	LLMConfig LLMSpec `json:"llm"`
	// +kubebuilder:validation:Required
	// +required
	// +operator-sdk:csv:customresourcedefinitions:type=spec,displayName="OLS Settings"
	OLSConfig OLSSpec `json:"ols"`
	// +operator-sdk:csv:customresourcedefinitions:type=spec,displayName="OLS Data Collector Settings"
	OLSDataCollectorConfig OLSDataCollectorSpec `json:"olsDataCollector,omitempty"`
	// MCP Server settings
	// +operator-sdk:csv:customresourcedefinitions:type=spec,displayName="MCP Server Settings"
	MCPServers []MCPServerConfig `json:"mcpServers,omitempty"`
	// Skills configuration for agent SDK capability modes.
	// +optional
	// +operator-sdk:csv:customresourcedefinitions:type=spec,displayName="Skills Settings"
	Skills *SkillsConfig `json:"skills,omitempty"`
	// Sandbox configuration for execution modes (design, deploy, remediate).
	// +optional
	// +operator-sdk:csv:customresourcedefinitions:type=spec,displayName="Sandbox Settings"
	Sandbox *SandboxConfig `json:"sandbox,omitempty"`
	// AlertManager webhook configuration for automated remediation proposals.
	// +optional
	// +operator-sdk:csv:customresourcedefinitions:type=spec,displayName="AlertManager Settings"
	AlertManager *AlertManagerConfig `json:"alertManager,omitempty"`
	// Escalation configuration for filing support cases when remediation fails.
	// +optional
	// +operator-sdk:csv:customresourcedefinitions:type=spec,displayName="Escalation Settings"
	// +kubebuilder:validation:XValidation:message="targetRepo is required when escalation is enabled",rule="!self.enabled || has(self.targetRepo)"
	Escalation *EscalationConfig `json:"escalation,omitempty"`
	// Feature Gates holds list of features to be enabled explicitly, otherwise they are disabled by default.
	// possible values: MCPServer, ToolFiltering
	// +kubebuilder:validation:Optional
	// +operator-sdk:csv:customresourcedefinitions:type=spec,displayName="Feature Gates"
	FeatureGates []FeatureGate `json:"featureGates,omitempty"`
}

// +kubebuilder:validation:Enum=MCPServer;ToolFiltering
type FeatureGate string

// OLSConfigStatus defines the observed state of OLS deployment.
type OLSConfigStatus struct {
	// Conditions represent the state of individual components
	// Always populated after first reconciliation
	// +operator-sdk:csv:customresourcedefinitions:type=status
	Conditions []metav1.Condition `json:"conditions"`

	// OverallStatus provides a high-level summary of the entire system's health.
	// Aggregates all component conditions into a single status value.
	// - Ready: All components are healthy
	// - NotReady: At least one component is not ready (check conditions for details)
	// Always set after first reconciliation
	// +optional
	// +kubebuilder:validation:Enum=Ready;NotReady
	// +operator-sdk:csv:customresourcedefinitions:type=status
	OverallStatus OverallStatus `json:"overallStatus,omitempty"`

	// DiagnosticInfo provides detailed troubleshooting information when deployments fail.
	// Each entry contains pod-level error details for a specific component.
	// This array is automatically populated when deployments fail and cleared when they recover.
	// Only present during deployment failures.
	// +optional
	// +operator-sdk:csv:customresourcedefinitions:type=status
	DiagnosticInfo []PodDiagnostic `json:"diagnosticInfo,omitempty"`
}

// PodDiagnostic describes a pod-level issue
type PodDiagnostic struct {
	// FailedComponent identifies which component this diagnostic relates to,
	// using the same type as the Conditions field (e.g., "ApiReady", "CacheReady")
	// This allows easy correlation between condition status and diagnostic details.
	FailedComponent string `json:"failedComponent"`

	// PodName is the name of the pod with issues
	PodName string `json:"podName"`

	// ContainerName is the container within the pod that failed
	// Empty if the issue is at the pod level (e.g., scheduling)
	// +optional
	ContainerName string `json:"containerName,omitempty"`

	// Reason is the failure reason
	// Examples: ImagePullBackOff, CrashLoopBackOff, Unschedulable, OOMKilled
	Reason string `json:"reason"`

	// Message provides detailed error information from Kubernetes
	Message string `json:"message"`

	// ExitCode for terminated containers (only set for container failures)
	// +optional
	ExitCode *int32 `json:"exitCode,omitempty"`

	// Type indicates the diagnostic type
	// +kubebuilder:validation:Enum=ContainerWaiting;ContainerTerminated;PodScheduling;PodCondition
	Type DiagnosticType `json:"type"`

	// LastUpdated is the timestamp when this diagnostic was collected
	LastUpdated metav1.Time `json:"lastUpdated"`
}

// DiagnosticType categorizes the type of diagnostic
// +kubebuilder:validation:Enum=ContainerWaiting;ContainerTerminated;PodScheduling;PodCondition
type DiagnosticType string

const (
	DiagnosticTypeContainerWaiting    DiagnosticType = "ContainerWaiting"
	DiagnosticTypeContainerTerminated DiagnosticType = "ContainerTerminated"
	DiagnosticTypePodScheduling       DiagnosticType = "PodScheduling"
	DiagnosticTypePodCondition        DiagnosticType = "PodCondition"
)

// DeploymentStatus represents the status of a deployment check
type DeploymentStatus string

const (
	DeploymentStatusReady       DeploymentStatus = "Ready"
	DeploymentStatusProgressing DeploymentStatus = "Progressing"
	DeploymentStatusFailed      DeploymentStatus = "Failed"
)

// OverallStatus represents the aggregate status of the entire system
type OverallStatus string

const (
	OverallStatusReady    OverallStatus = "Ready"
	OverallStatusNotReady OverallStatus = "NotReady"
)

// LogLevel defines the logging level for components
// +kubebuilder:validation:Enum=DEBUG;INFO;WARNING;ERROR;CRITICAL
type LogLevel string

const (
	// LogLevelDebug enables debug-level logging (most verbose)
	LogLevelDebug LogLevel = "DEBUG"

	// LogLevelInfo enables info-level logging (default)
	LogLevelInfo LogLevel = "INFO"

	// LogLevelWarning enables warning-level logging
	LogLevelWarning LogLevel = "WARNING"

	// LogLevelError enables error-level logging
	LogLevelError LogLevel = "ERROR"

	// LogLevelCritical enables critical-level logging (least verbose)
	LogLevelCritical LogLevel = "CRITICAL"
)

// LLMSpec defines the desired state of the large language model (LLM).
type LLMSpec struct {
	// +kubebuilder:validation:Required
	// +required
	// +operator-sdk:csv:customresourcedefinitions:type=spec,displayName="Providers"
	Providers []ProviderSpec `json:"providers"`
}

// OLSSpec defines the desired state of OLS deployment.
type OLSSpec struct {
	// Conversation cache settings
	// +operator-sdk:csv:customresourcedefinitions:type=spec,order=2,displayName="Conversation Cache"
	ConversationCache ConversationCacheSpec `json:"conversationCache,omitempty"`
	// OLS deployment settings
	// +operator-sdk:csv:customresourcedefinitions:type=spec,order=1,displayName="Deployment"
	DeploymentConfig DeploymentConfig `json:"deployment,omitempty"`
	// Log level. Valid options are DEBUG, INFO, WARNING, ERROR and CRITICAL. Default: "INFO".
	// +kubebuilder:default=INFO
	// +operator-sdk:csv:customresourcedefinitions:type=spec,displayName="Log level"
	LogLevel LogLevel `json:"logLevel,omitempty"`
	// Default model for usage
	// +kubebuilder:validation:Required
	// +required
	// +operator-sdk:csv:customresourcedefinitions:type=spec,displayName="Default Model",xDescriptors={"urn:alm:descriptor:com.tectonic.ui:text"}
	DefaultModel string `json:"defaultModel"`
	// Default provider for usage
	// +kubebuilder:validation:Required
	// +required
	// +operator-sdk:csv:customresourcedefinitions:type=spec,displayName="Default Provider",xDescriptors={"urn:alm:descriptor:com.tectonic.ui:text"}
	DefaultProvider string `json:"defaultProvider"`
	// Query filters
	// +operator-sdk:csv:customresourcedefinitions:type=spec,displayName="Query Filters"
	QueryFilters []QueryFiltersSpec `json:"queryFilters,omitempty"`
	// User data collection switches
	// +operator-sdk:csv:customresourcedefinitions:type=spec,displayName="User Data Collection"
	UserDataCollection UserDataCollectionSpec `json:"userDataCollection,omitempty"`
	// TLS configuration of the Lightspeed backend's HTTPS endpoint
	// +operator-sdk:csv:customresourcedefinitions:type=spec,displayName="TLS Configuration"
	TLSConfig *TLSConfig `json:"tlsConfig,omitempty"`
	// Additional CA certificates for TLS communication between OLS service and LLM Provider
	// +operator-sdk:csv:customresourcedefinitions:type=spec,displayName="Additional CA Configmap",xDescriptors={"urn:alm:descriptor:com.tectonic.ui:advanced"}
	AdditionalCAConfigMapRef *corev1.LocalObjectReference `json:"additionalCAConfigMapRef,omitempty"`
	// TLS Security Profile used by API endpoints
	// +kubebuilder:validation:Optional
	// +operator-sdk:csv:customresourcedefinitions:type=spec,displayName="TLS Security Profile",xDescriptors={"urn:alm:descriptor:com.tectonic.ui:advanced"}
	TLSSecurityProfile *configv1.TLSSecurityProfile `json:"tlsSecurityProfile,omitempty"`
	// Enable introspection features
	// +kubebuilder:validation:Optional
	// +operator-sdk:csv:customresourcedefinitions:type=spec,displayName="Introspection Enabled",xDescriptors={"urn:alm:descriptor:com.tectonic.ui:booleanSwitch"}
	IntrospectionEnabled bool `json:"introspectionEnabled,omitempty"`
	// Proxy settings for connecting to external servers, such as LLM providers.
	// +operator-sdk:csv:customresourcedefinitions:type=spec,displayName="Proxy Settings",xDescriptors={"urn:alm:descriptor:com.tectonic.ui:advanced"}
	// +kubebuilder:validation:Optional
	ProxyConfig *ProxyConfig `json:"proxyConfig,omitempty"`
	// RAG databases
	// +kubebuilder:validation:Optional
	// +operator-sdk:csv:customresourcedefinitions:type=spec,displayName="RAG Databases",xDescriptors={"urn:alm:descriptor:com.tectonic.ui:advanced"}
	RAG []RAGSpec `json:"rag,omitempty"`
	// LLM Token Quota Configuration
	// +operator-sdk:csv:customresourcedefinitions:type=spec,displayName="LLM Token Quota Configuration"
	QuotaHandlersConfig *QuotaHandlersConfig `json:"quotaHandlersConfig,omitempty"`
	// Persistent Storage Configuration
	// +kubebuilder:validation:Optional
	// +operator-sdk:csv:customresourcedefinitions:type=spec,displayName="Persistent Storage Configuration",xDescriptors={"urn:alm:descriptor:com.tectonic.ui:advanced"}
	Storage *Storage `json:"storage,omitempty"`
	// Only use BYOK RAG sources, ignore the OpenShift documentation RAG
	// +kubebuilder:validation:Optional
	// +operator-sdk:csv:customresourcedefinitions:type=spec,displayName="Only use BYOK RAG sources",xDescriptors={"urn:alm:descriptor:com.tectonic.ui:booleanSwitch"}
	ByokRAGOnly bool `json:"byokRAGOnly,omitempty"`
	// Custom system prompt for LLM queries. If not specified, uses the default OpenShift Lightspeed prompt.
	// +kubebuilder:validation:Optional
	// +operator-sdk:csv:customresourcedefinitions:type=spec,displayName="Query System Prompt",xDescriptors={"urn:alm:descriptor:com.tectonic.ui:advanced"}
	QuerySystemPrompt string `json:"querySystemPrompt,omitempty"`
	// Maximum number of iterations for agent execution. Default: 5
	// +kubebuilder:default=5
	// +kubebuilder:validation:Minimum=1
	// +operator-sdk:csv:customresourcedefinitions:type=spec,displayName="Max Iterations",xDescriptors={"urn:alm:descriptor:com.tectonic.ui:number"}
	MaxIterations int `json:"maxIterations,omitempty"`
	// Pull secrets for BYOK RAG images from image registries requiring authentication
	// +kubebuilder:validation:Optional
	// +operator-sdk:csv:customresourcedefinitions:type=spec,displayName="Image Pull Secrets"
	ImagePullSecrets []corev1.LocalObjectReference `json:"imagePullSecrets,omitempty"`
	// Tool filtering configuration for hybrid RAG retrieval. If not specified, all tools are used.
	// +kubebuilder:validation:Optional
	// +operator-sdk:csv:customresourcedefinitions:type=spec,displayName="Tool Filtering Configuration",xDescriptors={"urn:alm:descriptor:com.tectonic.ui:advanced"}
	ToolFilteringConfig *ToolFilteringConfig `json:"toolFilteringConfig,omitempty"`
	// Tool execution approval configuration. Controls whether tool calls require user approval before execution.
	// ⚠️ WARNING: This feature is not yet fully supported in the current OLS backend version.
	// The operator will generate the configuration, but tool approval behavior may not function as expected.
	// Please verify backend support before enabling.
	// +kubebuilder:validation:Optional
	// +operator-sdk:csv:customresourcedefinitions:type=spec,displayName="Tools Approval Configuration",xDescriptors={"urn:alm:descriptor:com.tectonic.ui:advanced"}
	ToolsApprovalConfig *ToolsApprovalConfig `json:"toolsApprovalConfig,omitempty"`
}

// Persistent Storage Configuration
type Storage struct {
	// Size of the requested volume
	// +operator-sdk:csv:customresourcedefinitions:type=spec,displayName="Size of the Requested Volume"
	// +kubebuilder:validation:Optional
	Size resource.Quantity `json:"size,omitempty"`
	// Storage class of the requested volume
	// +operator-sdk:csv:customresourcedefinitions:type=spec,displayName="Storage Class of the Requested Volume"
	Class string `json:"class,omitempty"`
}

// RAGSpec defines how to retrieve RAG databases.
type RAGSpec struct {
	// The path to the RAG database inside of the container image
	// +kubebuilder:default="/rag/vector_db"
	// +operator-sdk:csv:customresourcedefinitions:type=spec,displayName="Index Path in the Image"
	IndexPath string `json:"indexPath,omitempty"`
	// The Index ID of the RAG database. Only needed if there are multiple indices in the database.
	// +kubebuilder:default=""
	// +operator-sdk:csv:customresourcedefinitions:type=spec,displayName="Index ID"
	IndexID string `json:"indexID,omitempty"`
	// The URL of the container image to use as a RAG source
	// +kubebuilder:validation:Required
	// +required
	// +operator-sdk:csv:customresourcedefinitions:type=spec,displayName="Image"
	Image string `json:"image"`
}

// QuotaHandlersConfig defines the token quota configuration
type QuotaHandlersConfig struct {
	// Token quota limiters
	// +operator-sdk:csv:customresourcedefinitions:type=spec,displayName="Token Quota Limiters",xDescriptors={"urn:alm:descriptor:com.tectonic.ui:advanced"}
	LimitersConfig []LimiterConfig `json:"limitersConfig,omitempty"`
	// Enable token history
	// +kubebuilder:validation:Optional
	// +operator-sdk:csv:customresourcedefinitions:type=spec,displayName="Enable Token History",xDescriptors={"urn:alm:descriptor:com.tectonic.ui:booleanSwitch"}
	EnableTokenHistory bool `json:"enableTokenHistory,omitempty"`
}

// LimiterConfig defines settings for a token quota limiter
type LimiterConfig struct {
	// Name of the limiter
	// +operator-sdk:csv:customresourcedefinitions:type=spec,displayName="Limiter Name"
	Name string `json:"name"`
	// Type of the limiter
	// +kubebuilder:validation:Enum=cluster_limiter;user_limiter
	// +operator-sdk:csv:customresourcedefinitions:type=spec,displayName="Limiter Type. Accepted Values: cluster_limiter, user_limiter."
	Type string `json:"type"`
	// Initial value of the token quota
	// +kubebuilder:validation:Minimum=0
	// +operator-sdk:csv:customresourcedefinitions:type=spec,displayName="Initial Token Quota"
	InitialQuota int `json:"initialQuota"`
	// Token quota increase step
	// +kubebuilder:validation:Minimum=0
	// +operator-sdk:csv:customresourcedefinitions:type=spec,displayName="Token Quota Increase Step"
	QuotaIncrease int `json:"quotaIncrease"`
	// Period of time the token quota is for
	// Examples: "1 hour", "30 minutes", "2 days", "1 h", "30 min", "2 d"
	// Accepts singular (e.g., "1 second") or plural (e.g., "2 seconds") forms
	// Supported units: second(s), minute(s), hour(s), day(s), month(s), year(s) or s, min, h, d, m, y
	// +kubebuilder:validation:Pattern=`^(1\s+(second|minute|hour|day|month|year|s|min|h|d|m|y)|([2-9][0-9]*|[1-9][0-9]{2,})\s+(seconds|minutes|hours|days|months|years|s|min|h|d|m|y))$`
	// +operator-sdk:csv:customresourcedefinitions:type=spec,displayName="Period of Time the Token Quota Is For"
	Period string `json:"period"`
}

// DeploymentConfig defines the schema for overriding deployment of OLS instance.
type DeploymentConfig struct {
	// API container settings.
	// +operator-sdk:csv:customresourcedefinitions:type=spec,displayName="API Deployment"
	APIContainer Config `json:"api,omitempty"`
	// Data Collector container settings.
	// +operator-sdk:csv:customresourcedefinitions:type=spec,displayName="Data Collector Container"
	DataCollectorContainer ContainerConfig `json:"dataCollector,omitempty"`
	// MCP server container settings.
	// +operator-sdk:csv:customresourcedefinitions:type=spec,displayName="MCP Server Container"
	MCPServerContainer ContainerConfig `json:"mcpServer,omitempty"`
	// Llama Stack container settings.
	// +operator-sdk:csv:customresourcedefinitions:type=spec,displayName="Llama Stack Container"
	LlamaStackContainer ContainerConfig `json:"llamaStack,omitempty"`
	// Console container settings.
	// +operator-sdk:csv:customresourcedefinitions:type=spec,displayName="Console Deployment"
	ConsoleContainer Config `json:"console,omitempty"`
	// Database container settings.
	// +operator-sdk:csv:customresourcedefinitions:type=spec,displayName="Database Deployment"
	DatabaseContainer Config `json:"database,omitempty"`
}

// Config defines pod configuration using standard Kubernetes types
type Config struct {
	// Defines the number of desired OLS pods. Default: "1"
	// Note: Replicas can only be changed for APIContainer. For PostgreSQL and Console containers,
	// the number of replicas will always be set to 1.
	// +kubebuilder:default=1
	// +kubebuilder:validation:Minimum=0
	// +operator-sdk:csv:customresourcedefinitions:type=spec,displayName="Number of replicas",xDescriptors={"urn:alm:descriptor:com.tectonic.ui:podCount"}
	Replicas *int32 `json:"replicas,omitempty"`
	// Resource requirements (CPU, memory)
	// Uses standard corev1.ResourceRequirements
	Resources *corev1.ResourceRequirements `json:"resources,omitempty"`

	// Tolerations for pod scheduling
	// Uses standard corev1.Toleration
	Tolerations []corev1.Toleration `json:"tolerations,omitempty"`

	// Node selector constraints
	NodeSelector map[string]string `json:"nodeSelector,omitempty"`

	// Affinity rules (can be added without API version bump)
	// Uses standard corev1.Affinity
	Affinity *corev1.Affinity `json:"affinity,omitempty"`

	// Topology spread constraints (can be added without API version bump)
	// Uses standard corev1.TopologySpreadConstraint
	TopologySpreadConstraints []corev1.TopologySpreadConstraint `json:"topologySpreadConstraints,omitempty"`
}

// ContainerConfig defines container configuration using standard Kubernetes types
type ContainerConfig struct {
	// Resource requirements (CPU, memory)
	// Uses standard corev1.ResourceRequirements
	Resources *corev1.ResourceRequirements `json:"resources,omitempty"`
}

// +kubebuilder:validation:Enum=postgres
type CacheType string

const (
	Postgres CacheType = "postgres"
)

// ConversationCacheSpec defines the desired state of OLS conversation cache.
type ConversationCacheSpec struct {
	// Conversation cache type. Default: "postgres"
	// +kubebuilder:default=postgres
	// +operator-sdk:csv:customresourcedefinitions:type=spec,displayName="Cache Type"
	Type CacheType `json:"type,omitempty"`
	// +optional
	// +operator-sdk:csv:customresourcedefinitions:type=spec,displayName="PostgreSQL Settings"
	Postgres PostgresSpec `json:"postgres,omitempty"`
}

// PostgresSpec defines the desired state of Postgres.
type PostgresSpec struct {
	// Postgres sharedbuffers
	// +kubebuilder:validation:XIntOrString
	// +kubebuilder:default="256MB"
	// +operator-sdk:csv:customresourcedefinitions:type=spec,displayName="Shared Buffer Size"
	SharedBuffers string `json:"sharedBuffers,omitempty"`
	// Postgres maxconnections. Default: "2000"
	// +kubebuilder:default=2000
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=262143
	// +operator-sdk:csv:customresourcedefinitions:type=spec,displayName="Maximum Connections"
	MaxConnections int `json:"maxConnections,omitempty"`
}

// QueryFiltersSpec defines filters to manipulate questions/queries.
type QueryFiltersSpec struct {
	// Filter name.
	// +operator-sdk:csv:customresourcedefinitions:type=spec,displayName="Filter Name"
	Name string `json:"name,omitempty"`
	// Filter pattern.
	// +operator-sdk:csv:customresourcedefinitions:type=spec,displayName="The pattern to replace"
	Pattern string `json:"pattern,omitempty"`
	// Replacement for the matched pattern.
	// +operator-sdk:csv:customresourcedefinitions:type=spec,displayName="Replace With"
	ReplaceWith string `json:"replaceWith,omitempty"`
}

// ModelParametersSpec
type ModelParametersSpec struct {
	// Max tokens for response. The default is 2048 tokens.
	// +operator-sdk:csv:customresourcedefinitions:type=spec,displayName="Max Tokens For Response"
	MaxTokensForResponse int `json:"maxTokensForResponse,omitempty"`
}

// ModelSpec defines the LLM model to use and its parameters.
type ModelSpec struct {
	// Model name
	// +kubebuilder:validation:Required
	// +required
	// +operator-sdk:csv:customresourcedefinitions:type=spec,displayName="Name"
	Name string `json:"name"`
	// Model API URL
	// +kubebuilder:validation:Pattern=`^https?://.*$`
	// +operator-sdk:csv:customresourcedefinitions:type=spec,displayName="URL"
	URL string `json:"url,omitempty"`
	// Defines the model's context window size, in tokens. The default is 128k tokens.
	// +kubebuilder:validation:Minimum=1024
	// +operator-sdk:csv:customresourcedefinitions:type=spec,displayName="Context Window Size"
	ContextWindowSize uint `json:"contextWindowSize,omitempty"`
	// Model API parameters
	// +operator-sdk:csv:customresourcedefinitions:type=spec,displayName="Parameters"
	Parameters ModelParametersSpec `json:"parameters,omitempty"`
}

// ProviderSpec defines the desired state of LLM provider.
// +kubebuilder:validation:XValidation:message="'deploymentName' must be specified for 'azure_openai' provider",rule="self.type != \"azure_openai\" || self.deploymentName != \"\""
// +kubebuilder:validation:XValidation:message="'projectID' must be specified for 'watsonx' provider",rule="self.type != \"watsonx\" || self.projectID != \"\""
type ProviderSpec struct {
	// Provider name
	// +kubebuilder:validation:Required
	// +required
	// +operator-sdk:csv:customresourcedefinitions:type=spec,order=1,displayName="Name"
	Name string `json:"name"`
	// Provider API URL
	// +kubebuilder:validation:Pattern=`^https?://.*$`
	// +operator-sdk:csv:customresourcedefinitions:type=spec,order=2,displayName="URL"
	URL string `json:"url,omitempty"`
	// The name of the secret object that stores API provider credentials
	// +kubebuilder:validation:Required
	// +required
	// +operator-sdk:csv:customresourcedefinitions:type=spec,order=3,displayName="Credential Secret"
	CredentialsSecretRef corev1.LocalObjectReference `json:"credentialsSecretRef"`
	// List of models from the provider
	// +kubebuilder:validation:Required
	// +required
	// +operator-sdk:csv:customresourcedefinitions:type=spec,displayName="Models"
	Models []ModelSpec `json:"models"`
	// Provider type
	// +kubebuilder:validation:Required
	// +required
	// +kubebuilder:validation:Enum=anthropic_vertex;azure_openai;bam;openai;watsonx;rhoai_vllm;rhelai_vllm;fake_provider
	// +operator-sdk:csv:customresourcedefinitions:type=spec,displayName="Provider Type"
	Type string `json:"type"`
	// Deployment name for Azure OpenAI provider
	// +operator-sdk:csv:customresourcedefinitions:type=spec,displayName="Azure Deployment Name"
	AzureDeploymentName string `json:"deploymentName,omitempty"`
	// API Version for Azure OpenAI provider
	// +operator-sdk:csv:customresourcedefinitions:type=spec,displayName="Azure OpenAI API Version"
	APIVersion string `json:"apiVersion,omitempty"`
	// Watsonx Project ID
	// +operator-sdk:csv:customresourcedefinitions:type=spec,displayName="Watsonx Project ID"
	WatsonProjectID string `json:"projectID,omitempty"`
	// Fake Provider MCP Tool Call
	// +operator-sdk:csv:customresourcedefinitions:type=spec,displayName="Fake Provider MCP Tool Call"
	FakeProviderMCPToolCall bool `json:"fakeProviderMCPToolCall,omitempty"`
	// TLS Security Profile used by connection to provider
	// +kubebuilder:validation:Optional
	// +operator-sdk:csv:customresourcedefinitions:type=spec,displayName="TLS Security Profile",xDescriptors={"urn:alm:descriptor:com.tectonic.ui:advanced"}
	TLSSecurityProfile *configv1.TLSSecurityProfile `json:"tlsSecurityProfile,omitempty"`
}

// UserDataCollectionSpec defines how we collect user data.
type UserDataCollectionSpec struct {
	// +operator-sdk:csv:customresourcedefinitions:type=spec,displayName="Do Not Collect User Feedback"
	FeedbackDisabled bool `json:"feedbackDisabled,omitempty"`
	// +operator-sdk:csv:customresourcedefinitions:type=spec,displayName="Do Not Collect Transcripts"
	TranscriptsDisabled bool `json:"transcriptsDisabled,omitempty"`
}

// OLSDataCollectorSpec defines allowed OLS data collector configuration.
type OLSDataCollectorSpec struct {
	// Log level. Valid options are DEBUG, INFO, WARNING, ERROR and CRITICAL. Default: "INFO".
	// +kubebuilder:default=INFO
	// +operator-sdk:csv:customresourcedefinitions:type=spec,displayName="Log level"
	LogLevel LogLevel `json:"logLevel,omitempty"`
}

type TLSConfig struct {
	// KeyCertSecretRef references a Secret containing TLS certificate and key.
	// The Secret must contain the following keys:
	//   - tls.crt: Server certificate (PEM format) - REQUIRED
	//   - tls.key: Private key (PEM format) - REQUIRED
	//   - ca.crt: CA certificate for console proxy trust (PEM format) - OPTIONAL
	//
	// If ca.crt is not provided, the OpenShift Console proxy will use the default system trust store.
	//
	// +operator-sdk:csv:customresourcedefinitions:type=spec,displayName="TLS Certificate Secret Reference"
	// +optional
	KeyCertSecretRef corev1.LocalObjectReference `json:"keyCertSecretRef,omitempty"`
}

// ProxyConfig defines the proxy settings for connecting to external servers, such as LLM providers.
type ProxyConfig struct {
	// Proxy URL, e.g. https://proxy.example.com:8080
	// If not specified, the cluster wide proxy will be used, through env var "https_proxy".
	// +kubebuilder:validation:Pattern=`^https?://.*$`
	// +operator-sdk:csv:customresourcedefinitions:type=spec,displayName="Proxy URL"
	ProxyURL string `json:"proxyURL,omitempty"`
	// The configmap holding proxy CA certificate
	// +operator-sdk:csv:customresourcedefinitions:type=spec,displayName="Proxy CA Certificate"
	ProxyCACertificateRef *corev1.LocalObjectReference `json:"proxyCACertificate,omitempty"`
}

// ToolFilteringConfig defines configuration for tool filtering using hybrid RAG retrieval.
// If this config is present, tool filtering is enabled. If absent, all tools are used.
// The embedding model is not exposed as it's handled by the container image.
// +kubebuilder:validation:XValidation:rule="self.alpha >= 0.0 && self.alpha <= 1.0",message="alpha must be between 0.0 and 1.0"
// +kubebuilder:validation:XValidation:rule="self.threshold >= 0.0 && self.threshold <= 1.0",message="threshold must be between 0.0 and 1.0"
type ToolFilteringConfig struct {
	// Weight for dense vs sparse retrieval (1.0 = full dense, 0.0 = full sparse)
	// +kubebuilder:default=0.8
	// +operator-sdk:csv:customresourcedefinitions:type=spec,displayName="Alpha Weight"
	Alpha float64 `json:"alpha,omitempty"`

	// Number of tools to retrieve
	// +kubebuilder:default=10
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=50
	// +operator-sdk:csv:customresourcedefinitions:type=spec,displayName="Top K"
	TopK int `json:"topK,omitempty"`

	// Minimum similarity threshold for filtering results
	// +kubebuilder:default=0.01
	// +operator-sdk:csv:customresourcedefinitions:type=spec,displayName="Similarity Threshold"
	Threshold float64 `json:"threshold,omitempty"`
}

// ApprovalType defines the approval strategy for tool execution
// +kubebuilder:validation:Enum=never;always;tool_annotations
type ApprovalType string

const (
	// ApprovalTypeNever - all tools execute without approval
	ApprovalTypeNever ApprovalType = "never"
	// ApprovalTypeAlways - all tool calls require approval
	ApprovalTypeAlways ApprovalType = "always"
	// ApprovalTypeToolAnnotations - approval based on per-tool annotations
	ApprovalTypeToolAnnotations ApprovalType = "tool_annotations"
)

// ToolsApprovalConfig defines configuration for tool execution approval.
// Controls whether tool calls require user approval before execution.
type ToolsApprovalConfig struct {
	// Approval strategy for tool execution.
	// 'never' - tools execute without approval
	// 'always' - all tool calls require approval
	// 'tool_annotations' - approval based on per-tool annotations
	// +kubebuilder:default=never
	// +operator-sdk:csv:customresourcedefinitions:type=spec,displayName="Approval Type"
	ApprovalType ApprovalType `json:"approvalType,omitempty"`

	// Timeout in seconds for waiting for user approval
	// +kubebuilder:default=600
	// +kubebuilder:validation:Minimum=1
	// +operator-sdk:csv:customresourcedefinitions:type=spec,displayName="Approval Timeout (seconds)"
	ApprovalTimeout int `json:"approvalTimeout,omitempty"`
}

// MCPHeaderSourceType defines the type of header value source
// +enum
type MCPHeaderSourceType string

const (
	// MCPHeaderSourceTypeSecret uses a value from a Kubernetes secret
	MCPHeaderSourceTypeSecret MCPHeaderSourceType = "secret"
	// MCPHeaderSourceTypeKubernetes uses the Kubernetes service account token
	MCPHeaderSourceTypeKubernetes MCPHeaderSourceType = "kubernetes"
	// MCPHeaderSourceTypeClient uses the client token from the incoming request
	MCPHeaderSourceTypeClient MCPHeaderSourceType = "client"
)

// MCPHeaderValueSource defines where the header value comes from.
// Uses a discriminated union pattern following KEP-1027.
// The Type field determines which of the other fields should be set.
// Secrets must exist in the operator's namespace.
//
// Examples:
//
//	# Use a secret:
//	valueFrom:
//	  type: secret
//	  secretRef:
//	    name: my-mcp-secret
//
//	# Use Kubernetes service account token:
//	valueFrom:
//	  type: kubernetes
//
//	# Pass through client token:
//	valueFrom:
//	  type: client
//
// +kubebuilder:validation:XValidation:rule="self.type == 'secret' ? has(self.secretRef) && size(self.secretRef.name) > 0 : true",message="secretRef with non-empty name is required when type is 'secret'"
// +kubebuilder:validation:XValidation:rule="self.type != 'secret' ? !has(self.secretRef) : true",message="secretRef must not be set when type is 'kubernetes' or 'client'"
type MCPHeaderValueSource struct {
	// Type specifies the source type for the header value
	// +unionDiscriminator
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Enum=secret;kubernetes;client
	// +required
	// +operator-sdk:csv:customresourcedefinitions:type=spec,displayName="Source Type"
	Type MCPHeaderSourceType `json:"type"`

	// Reference to a secret containing the header value.
	// Required when Type is "secret".
	// The secret must exist in the operator's namespace.
	// +unionMember
	// +optional
	// +operator-sdk:csv:customresourcedefinitions:type=spec,displayName="Secret Reference"
	SecretRef *corev1.LocalObjectReference `json:"secretRef,omitempty"`
}

// MCPHeader defines a header to send to the MCP server
type MCPHeader struct {
	// Name of the header (e.g., "Authorization", "X-API-Key")
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:Pattern=`^[A-Za-z0-9-]+$`
	// +required
	// +operator-sdk:csv:customresourcedefinitions:type=spec,displayName="Header Name"
	Name string `json:"name"`

	// Source of the header value
	// +kubebuilder:validation:Required
	// +required
	// +operator-sdk:csv:customresourcedefinitions:type=spec,displayName="Value Source"
	ValueFrom MCPHeaderValueSource `json:"valueFrom"`
}

// MCPServerConfig defines the streamlined configuration for an MCP server
// This configuration only supports HTTP/HTTPS transport
type MCPServerConfig struct {
	// Name of the MCP server
	// +kubebuilder:validation:Required
	// +required
	// +operator-sdk:csv:customresourcedefinitions:type=spec,displayName="Name"
	Name string `json:"name"`

	// URL of the MCP server (HTTP/HTTPS)
	// +kubebuilder:validation:Required
	// +required
	// +kubebuilder:validation:Pattern=`^https?://.*$`
	// +operator-sdk:csv:customresourcedefinitions:type=spec,displayName="URL"
	URL string `json:"url"`

	// Timeout for the MCP server in seconds, default is 5
	// +kubebuilder:default=5
	// +operator-sdk:csv:customresourcedefinitions:type=spec,displayName="Timeout (seconds)"
	Timeout int `json:"timeout,omitempty"`

	// Headers to send to the MCP server
	// Each header can reference a secret or use a special source (kubernetes token, client token)
	// +optional
	// +operator-sdk:csv:customresourcedefinitions:type=spec,displayName="Headers"
	Headers []MCPHeader `json:"headers,omitempty"`
}

// SkillsConfig defines how agent skills are delivered to the service pods.
// Skills are markdown files packaged as an OCI image and mounted via the
// Kubernetes image volume source (requires K8s 1.34+ / OCP 4.20+).
type SkillsConfig struct {
	// image is the OCI image reference containing skills markdown files.
	// Example: "quay.io/openshift-lightspeed/skills:latest"
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	Image string `json:"image"`

	// pullPolicy defines when to pull the skills image.
	// +kubebuilder:default=IfNotPresent
	// +kubebuilder:validation:Enum=Always;IfNotPresent;Never
	// +optional
	PullPolicy string `json:"pullPolicy,omitempty"`

	// mountPath is the directory where skills are mounted inside the service pod.
	// +kubebuilder:default="/app/skills"
	// +optional
	MountPath string `json:"mountPath,omitempty"`
}

// SandboxConfig configures the sandbox runtime for proposal execution.
// Agent configuration (skills, LLM, prompts) is defined via Agent CRs.
// Workflow configuration (which agent per step) is defined via Workflow CRs.
type SandboxConfig struct {
	// baseTemplate is the name of the SandboxTemplate to use as the base for
	// all derived per-agent templates. The operator reads this template and
	// creates variants with the agent's skills image and mode.
	// +optional
	BaseTemplate string `json:"baseTemplate,omitempty"`

	// namespace is the namespace where SandboxClaims are created.
	// Defaults to the operator's namespace.
	// +optional
	Namespace string `json:"namespace,omitempty"`

	// maxAttempts is the maximum retry attempts before auto-escalation.
	// +kubebuilder:default=3
	// +kubebuilder:validation:Minimum=1
	// +optional
	MaxAttempts int `json:"maxAttempts,omitempty"`

	// policy configures the RBAC security model enforcement layers.
	// +optional
	Policy *PolicyConfig `json:"policy,omitempty"`
}

// PolicyRuleRef identifies a set of API resources for policy configuration.
type PolicyRuleRef struct {
	// apiGroups are the API groups to match.
	APIGroups []string `json:"apiGroups"`
	// resources are the resources to match.
	Resources []string `json:"resources"`
}

// PolicyConfig configures the RBAC security model enforcement layers.
type PolicyConfig struct {
	// maxExecutionRules are the admin-configured ceiling for namespace-scoped
	// RBAC rules the operator will grant to execution sandboxes.
	// +optional
	MaxExecutionRules []rbacv1.PolicyRule `json:"maxExecutionRules,omitempty"`

	// maxClusterRules are the admin-configured ceiling for cluster-scoped
	// RBAC rules the operator will grant to execution sandboxes.
	// +optional
	MaxClusterRules []rbacv1.PolicyRule `json:"maxClusterRules,omitempty"`

	// additionalProtectedNamespaces are extra namespaces (beyond the hardcoded
	// set) where execution write access is forbidden. Supports glob patterns.
	// +optional
	AdditionalProtectedNamespaces []string `json:"additionalProtectedNamespaces,omitempty"`

	// analysisRole is the name of a pre-created ClusterRole for read-only
	// analysis access. If set, analysis sandboxes are bound to this ClusterRole.
	// +optional
	AnalysisRole string `json:"analysisRole,omitempty"`

	// excludeFromRead lists resources excluded from analysis read access.
	// Default excludes secrets to prevent credential leakage to the LLM.
	// +optional
	ExcludeFromRead []PolicyRuleRef `json:"excludeFromRead,omitempty"`
}

// AlertManagerConfig configures the Alertmanager webhook receiver that
// auto-creates Proposal CRs with type=remediate.
type AlertManagerConfig struct {
	// enabled controls whether the AlertManager webhook listener is active.
	// +kubebuilder:default=false
	Enabled bool `json:"enabled"`

	// port is the port for the webhook HTTP listener.
	// +kubebuilder:default=9095
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=65535
	// +optional
	Port int32 `json:"port,omitempty"`

	// namespace is the namespace where remediation proposals are created.
	// Defaults to the operator's namespace.
	// +optional
	Namespace string `json:"namespace,omitempty"`

	// cooldownMinutes is the minimum time (in minutes) after a proposal is created
	// before a new proposal can be created for the same alert. Prevents rapid
	// re-creation when alerts re-fire shortly after remediation.
	// +kubebuilder:default=15
	// +kubebuilder:validation:Minimum=0
	// +kubebuilder:validation:Maximum=1440
	// +optional
	CooldownMinutes int32 `json:"cooldownMinutes,omitempty"`
}

// EscalationConfig configures the escalation flow for filing support cases
// when automated remediation fails or a platform bug is identified.
type EscalationConfig struct {
	// enabled controls whether escalation mode is available.
	// +kubebuilder:default=false
	Enabled bool `json:"enabled"`

	// targetRepo is the GitHub repository where issues are filed.
	// Format: "owner/repo" (e.g., "openshift-lightspeed/support-cases").
	// +kubebuilder:validation:Pattern=`^[a-zA-Z0-9._-]+/[a-zA-Z0-9._-]+$`
	// +optional
	TargetRepo string `json:"targetRepo,omitempty"`

	// backend selects where escalation cases are filed.
	// +kubebuilder:default="github"
	// +kubebuilder:validation:Enum=github
	// +optional
	Backend string `json:"backend,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Cluster
// +kubebuilder:validation:XValidation:rule="self.metadata.name == 'cluster'",message=".metadata.name must be 'cluster'"
// Red Hat OpenShift Lightspeed instance. OLSConfig is the Schema for the olsconfigs API
type OLSConfig struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`
	// +kubebuilder:validation:Required
	// +required
	Spec   OLSConfigSpec   `json:"spec"`
	Status OLSConfigStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:resource:scope=Cluster
// OLSConfigList contains a list of OLSConfig
type OLSConfigList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []OLSConfig `json:"items"`
}

func init() {
	SchemeBuilder.Register(&OLSConfig{}, &OLSConfigList{})
}

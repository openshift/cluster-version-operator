/*
Copyright 2026.

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
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// AgentTimeouts configures per-step and per-turn timeout limits.
// All values are in seconds.
//
// +kubebuilder:validation:MinProperties=1
type AgentTimeouts struct {
	// analysisSeconds is the timeout for the analysis step in seconds.
	// +optional
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=3600
	AnalysisSeconds int32 `json:"analysisSeconds,omitempty"`

	// executionSeconds is the timeout for the execution step in seconds.
	// +optional
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=3600
	ExecutionSeconds int32 `json:"executionSeconds,omitempty"`

	// verificationSeconds is the timeout for the verification step in seconds.
	// +optional
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=3600
	VerificationSeconds int32 `json:"verificationSeconds,omitempty"`

	// chatSeconds is the timeout for each chat turn with the LLM in seconds.
	// +optional
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=600
	ChatSeconds int32 `json:"chatSeconds,omitempty"`
}

// AgentSpec defines the desired state of Agent.
type AgentSpec struct {
	// llmProvider references a cluster-scoped LLMProvider CR that supplies the
	// LLM backend for this agent tier.
	// +required
	LLMProvider LLMProviderReference `json:"llmProvider,omitzero"`

	// model is the LLM model identifier as recognized by the provider
	// (e.g., "claude-opus-4-6", "claude-haiku-4-5", "gpt-4o").
	// Must start with an alphanumeric character and may contain
	// alphanumerics, dots, hyphens, underscores, slashes, colons,
	// and at-signs. Maximum 256 characters.
	// +required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=256
	// +kubebuilder:validation:XValidation:rule="self.matches('^[a-zA-Z0-9][a-zA-Z0-9._\\\\-/:@]*$')",message="model must start with an alphanumeric character and contain only alphanumerics, dots, hyphens, underscores, slashes, colons, and at-signs"
	Model string `json:"model,omitempty"`

	// timeouts configures per-step and per-turn timeout limits.
	// When omitted, the agent sandbox uses its built-in defaults.
	// +optional
	Timeouts AgentTimeouts `json:"timeouts,omitzero"`

	// maxTurns is the maximum number of tool-use turns the agent may take
	// in a single step invocation. Prevents runaway loops.
	// When omitted, the agent sandbox uses its built-in default.
	// Minimum 1, maximum 500.
	// +optional
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=500
	MaxTurns int32 `json:"maxTurns,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Cluster
// +kubebuilder:printcolumn:name="LLM",type=string,JSONPath=`.spec.llmProvider.name`
// +kubebuilder:printcolumn:name="Model",type=string,JSONPath=`.spec.model`
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=`.status.conditions[?(@.type=="Ready")].status`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// Agent defines a cluster-scoped agent tier (e.g., "default", "smart", "fast").
// The cluster admin creates Agent resources to configure LLM infrastructure
// and runtime settings. Proposals reference agents by name per step.
//
// Agent is cluster-scoped. The metadata.name serves as the tier identifier.
// The "default" agent must exist; "smart" and "fast" are optional (the
// operator auto-links to "default" if absent).
//
// Example — a high-capability agent tier:
//
//	apiVersion: agentic.openshift.io/v1alpha1
//	kind: Agent
//	metadata:
//	  name: smart
//	spec:
//	  llmProvider:
//	    name: vertex-ai
//	  model: claude-opus-4-6
//	  timeouts:
//	    analysisSeconds: 300
//	    executionSeconds: 600
//	  maxTurns: 200
//
// Example — a fast, cost-efficient agent tier:
//
//	apiVersion: agentic.openshift.io/v1alpha1
//	kind: Agent
//	metadata:
//	  name: fast
//	spec:
//	  llmProvider:
//	    name: vertex-ai
//	  model: claude-haiku-4-5
//	  timeouts:
//	    analysisSeconds: 120
//	    executionSeconds: 300
//	  maxTurns: 100
type Agent struct {
	metav1.TypeMeta `json:",inline"`

	// metadata is the standard object metadata.
	// +optional
	metav1.ObjectMeta `json:"metadata,omitempty"`

	// spec defines the desired state of Agent.
	// +required
	Spec AgentSpec `json:"spec,omitzero"`

	// status defines the observed state of Agent.
	// +optional
	Status AgentStatus `json:"status,omitzero"`
}

const (
	// AgentConditionReady indicates whether all referenced resources
	// (LLMProvider, Secrets) exist and are accessible.
	AgentConditionReady string = "Ready"
)

// AgentStatus defines the observed state of Agent. The operator
// validates that all referenced resources exist and reports readiness
// via standard Kubernetes conditions. An empty status (`status: {}`)
// is the initial state before the operator's first reconcile.
//
// +kubebuilder:validation:MinProperties=1
type AgentStatus struct {
	// conditions represent the latest available observations of the
	// Agent's state. The Ready condition summarizes whether all
	// referenced resources (LLMProvider, Secrets) are present.
	// +listType=map
	// +listMapKey=type
	// +patchStrategy=merge
	// +patchMergeKey=type
	// +optional
	// +kubebuilder:validation:MinItems=1
	// +kubebuilder:validation:MaxItems=8
	Conditions []metav1.Condition `json:"conditions,omitempty" patchStrategy:"merge" patchMergeKey:"type" protobuf:"bytes,1,rep,name=conditions"`
}

// +kubebuilder:object:root=true

// AgentList contains a list of Agent.
type AgentList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Agent `json:"items"`
}

func init() {
	SchemeBuilder.Register(&Agent{}, &AgentList{})
}

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
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
)

// ConfidenceLevel is the agent's self-assessed confidence in its diagnosis.
// Higher confidence generally correlates with clearer symptoms and
// more deterministic root causes.
//
//   - "Low"    — Uncertain diagnosis; symptoms are ambiguous or incomplete.
//   - "Medium" — Reasonable diagnosis; symptoms point to a likely cause.
//   - "High"   — Strong diagnosis; clear symptoms with deterministic root cause.
//
// +kubebuilder:validation:Enum=Low;Medium;High
type ConfidenceLevel string

const (
	ConfidenceLevelLow    ConfidenceLevel = "Low"
	ConfidenceLevelMedium ConfidenceLevel = "Medium"
	ConfidenceLevelHigh   ConfidenceLevel = "High"
)

// RiskLevel is the agent's assessment of how risky a remediation is.
// Critical-risk proposals typically require explicit human review.
//
//   - "Low"      — Minimal risk; safe to apply automatically.
//   - "Medium"   — Moderate risk; review recommended.
//   - "High"     — Significant risk; careful review required.
//   - "Critical" — Extreme risk; manual approval strongly recommended.
//
// +kubebuilder:validation:Enum=Low;Medium;High;Critical
type RiskLevel string

const (
	RiskLevelLow      RiskLevel = "Low"
	RiskLevelMedium   RiskLevel = "Medium"
	RiskLevelHigh     RiskLevel = "High"
	RiskLevelCritical RiskLevel = "Critical"
)

// DiagnosisResult contains the root cause analysis from the analysis agent.
// This is populated by the agent during the analysis step and stored in
// the AnalysisStepStatus as part of a RemediationOption. Users see this
// in the console UI when reviewing the proposal.
type DiagnosisResult struct {
	// summary is a Markdown-formatted diagnosis summary explaining the
	// problem, its symptoms, and the agent's findings. Maximum 8192 characters.
	// +required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=8192
	Summary string `json:"summary,omitempty"`
	// confidence is the agent's self-assessed confidence in its diagnosis.
	// Higher confidence generally correlates with clearer symptoms and
	// more deterministic root causes.
	// +required
	Confidence ConfidenceLevel `json:"confidence,omitempty"`
	// rootCause is a concise Markdown-formatted description of the identified
	// root cause (e.g., "OOMKilled due to memory limit of 256Mi").
	// Maximum 1024 characters.
	// +required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=1024
	RootCause string `json:"rootCause,omitempty"`
}

// ProposedAction describes a single discrete action the analysis agent
// recommends as part of its remediation plan. Actions are displayed to
// the user after analysis for review before approval.
type ProposedAction struct {
	// type is the action category (e.g., "patch", "scale", "restart",
	// "create", "delete", "rollout"). Free-form string to allow agents
	// to express domain-specific action types. Must be 1-256 characters.
	// +required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=256
	Type string `json:"type,omitempty"`
	// description is a Markdown-formatted explanation of what this action
	// will do (e.g., "Increase memory limit from 256Mi to 512Mi").
	// Maximum 4096 characters.
	// +required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=4096
	Description string `json:"description,omitempty"`
}

// Reversibility indicates whether a remediation can be rolled back.
// +kubebuilder:validation:Enum=Reversible;Irreversible;Partial
type Reversibility string

const (
	ReversibilityReversible   Reversibility = "Reversible"
	ReversibilityIrreversible Reversibility = "Irreversible"
	ReversibilityPartial      Reversibility = "Partial"
)

// ProposalResult contains the remediation plan from the analysis agent.
// This is part of a RemediationOption and is presented to the user after
// analysis, before approval. The risk and reversibility assessments help
// users make informed approval decisions.
type ProposalResult struct {
	// description is a Markdown-formatted summary of the overall remediation
	// approach. Maximum 8192 characters.
	// +required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=8192
	Description string `json:"description,omitempty"`
	// actions is the ordered list of discrete actions the agent proposes.
	// Maximum 50 items.
	// +required
	// +listType=atomic
	// +kubebuilder:validation:MinItems=1
	// +kubebuilder:validation:MaxItems=50
	Actions []ProposedAction `json:"actions,omitempty"`
	// risk is the agent's assessment of how risky the remediation is.
	// Critical-risk proposals typically require explicit human review.
	// +required
	Risk RiskLevel `json:"risk,omitempty"`
	// reversible indicates whether the remediation can be rolled back
	// if something goes wrong. See rollbackPlan for details.
	// Must be one of: Reversible, Irreversible, Partial.
	// +optional
	Reversible Reversibility `json:"reversible,omitempty"`
	// estimatedImpact is a Markdown-formatted description of the expected
	// impact of the remediation on the system
	// (e.g., "Brief pod restart, ~30s downtime").
	// Maximum 1024 characters.
	// +required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=1024
	EstimatedImpact string `json:"estimatedImpact,omitempty"`
	// rollbackPlan describes how to undo the remediation if execution fails
	// or causes unexpected issues. Only the execution step mutates cluster
	// state, so rollback lives here alongside the actions it would undo.
	// +optional
	RollbackPlan RollbackPlan `json:"rollbackPlan,omitzero"`
}

// VerificationStep describes a single verification check that the
// verification agent should run after execution. Populated by the
// analysis agent as part of the RemediationOption.
type VerificationStep struct {
	// name is a short identifier for this check (e.g., "pod-running").
	// Must be 1-253 characters.
	// +required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=253
	Name string `json:"name,omitempty"`
	// command is the command or API call to run for this check
	// (e.g., "oc get pod -n production -l app=web -o jsonpath='{.items[0].status.phase}'").
	// Maximum 4096 characters.
	// +optional
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=4096
	Command string `json:"command,omitempty"`
	// expected is the expected output or condition
	// (e.g., "Running", "ready=true"). Maximum 1024 characters.
	// +optional
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=1024
	Expected string `json:"expected,omitempty"`
	// type categorizes the check (e.g., "command", "metric", "condition").
	// Must be 1-256 characters.
	// +required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=256
	Type string `json:"type,omitempty"`
}

// RollbackPlan describes how to undo the remediation if execution fails
// or causes unexpected issues. Populated by the analysis agent.
type RollbackPlan struct {
	// description is a Markdown-formatted explanation of the rollback strategy.
	// Must be 1-4096 characters.
	// +required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=4096
	Description string `json:"description,omitempty"`
	// command is the rollback command or steps to execute.
	// Maximum 4096 characters.
	// +optional
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=4096
	Command string `json:"command,omitempty"`
}

// VerificationPlan describes the complete verification strategy for a
// remediation. Populated by the analysis agent as part of a
// RemediationOption and used by the verification agent (if not skipped)
// to validate the remediation.
type VerificationPlan struct {
	// description is a Markdown-formatted summary of the verification approach.
	// Maximum 4096 characters.
	// +required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=4096
	Description string `json:"description,omitempty"`
	// steps is the ordered list of verification checks to run.
	// Maximum 20 items.
	// +optional
	// +listType=atomic
	// +kubebuilder:validation:MinItems=1
	// +kubebuilder:validation:MaxItems=20
	Steps []VerificationStep `json:"steps,omitempty"`
}

// RBACRule describes a single RBAC permission that the analysis agent
// requests for the execution step. The operator's policy engine validates
// these requests against a 6-layer defense model before creating the
// actual Role/ClusterRole bindings. Each rule must include a justification
// so that users and policy can audit why the permission is needed.
type RBACRule struct {
	// namespace is the target namespace for namespace-scoped rules.
	// Must match one of the proposal's targetNamespaces. Ignored for
	// cluster-scoped rules. Validation is deferred to the operator's
	// policy engine at runtime. Must be a valid RFC 1123 DNS label.
	// +optional
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=63
	// +kubebuilder:validation:XValidation:rule="!format.dns1123Label().validate(self).hasValue()",message="must be a valid DNS label: lowercase alphanumeric characters and hyphens, starting with an alphabetic character and ending with an alphanumeric character"
	Namespace string `json:"namespace,omitempty"`
	// apiGroups are the API groups for this rule (e.g., "", "apps", "batch").
	// The empty string "" represents the core API group (pods, services, etc.).
	// Maximum 20 items, each up to 253 characters.
	// +required
	// +listType=atomic
	// +kubebuilder:validation:MinItems=1
	// +kubebuilder:validation:MaxItems=20
	// +kubebuilder:validation:items:MaxLength=253
	APIGroups []string `json:"apiGroups,omitempty"` //nolint:kubeapilinter // empty string "" is a valid core API group
	// resources are the resource types (e.g., "pods", "deployments").
	// Maximum 20 items.
	// +required
	// +listType=atomic
	// +kubebuilder:validation:MinItems=1
	// +kubebuilder:validation:MaxItems=20
	// +kubebuilder:validation:items:MinLength=1
	// +kubebuilder:validation:items:MaxLength=253
	Resources []string `json:"resources,omitempty"`
	// resourceNames restricts the rule to specific named resources.
	// When empty, the rule applies to all resources of the given type.
	// Maximum 50 items.
	// +optional
	// +listType=atomic
	// +kubebuilder:validation:MinItems=1
	// +kubebuilder:validation:MaxItems=50
	// +kubebuilder:validation:items:MinLength=1
	// +kubebuilder:validation:items:MaxLength=253
	ResourceNames []string `json:"resourceNames,omitempty"`
	// verbs are the allowed operations (e.g., "get", "patch", "delete").
	// Maximum 10 items.
	// +required
	// +listType=atomic
	// +kubebuilder:validation:MinItems=1
	// +kubebuilder:validation:MaxItems=10
	// +kubebuilder:validation:items:MinLength=1
	// +kubebuilder:validation:items:MaxLength=63
	Verbs []string `json:"verbs,omitempty"`
	// justification is a Markdown-formatted explanation of why this
	// permission is needed for the remediation
	// (e.g., "Need to patch deployment to increase memory limit").
	// Required for audit and policy enforcement. Maximum 1024 characters.
	// +required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=1024
	Justification string `json:"justification,omitempty"`
}

// RBACResult contains the RBAC permissions requested by the analysis agent
// for the execution step. The operator creates a dedicated ServiceAccount
// per proposal and binds these permissions via Role (namespace-scoped) or
// ClusterRole (cluster-scoped) before launching the execution sandbox.
// All RBAC resources are cleaned up after the proposal reaches a terminal state.
//
// +kubebuilder:validation:MinProperties=1
type RBACResult struct {
	// namespaceScoped are rules that will be applied via Role + RoleBinding
	// in the proposal's target namespaces. These are the most common rules.
	// Maximum 50 items.
	// +optional
	// +listType=atomic
	// +kubebuilder:validation:MinItems=1
	// +kubebuilder:validation:MaxItems=50
	NamespaceScoped []RBACRule `json:"namespaceScoped,omitempty"`
	// clusterScoped are rules that will be applied via ClusterRole +
	// ClusterRoleBinding. Used when the agent needs cross-namespace or
	// non-namespaced resource access (e.g., reading nodes, CRDs).
	// Maximum 50 items.
	// +optional
	// +listType=atomic
	// +kubebuilder:validation:MinItems=1
	// +kubebuilder:validation:MaxItems=50
	ClusterScoped []RBACRule `json:"clusterScoped,omitempty"`
}

// RemediationOption represents a single remediation approach produced by
// the analysis agent. The agent may return multiple options, each with
// its own diagnosis, remediation plan, verification strategy, and RBAC
// requirements. When the user approves execution, the operator trims
// the AnalysisResult to keep only the approved option and uses its
// RBAC and plan for the execution step.
//
// The components field is an extensibility point for adapter-specific UI
// data. For example, an ACS adapter might include violation details or
// affected deployment information as components that the console plugin
// renders with custom components.
//
// +kubebuilder:validation:XValidation:rule="!has(self.diagnosis) || has(self.proposal)",message="proposal is required when diagnosis is present"
// +kubebuilder:validation:XValidation:rule="!has(self.proposal) || has(self.diagnosis)",message="diagnosis is required when proposal is present"
type RemediationOption struct {
	// title is a short Markdown-formatted name for this option
	// (e.g., "Increase memory limit", "Restart with backoff").
	// Must be 1-256 characters.
	// +required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=256
	Title string `json:"title,omitempty"`
	// summary is an optional Markdown-formatted one-line summary for
	// collapsed views in the console UI. Maximum 1024 characters.
	// +optional
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=1024
	Summary string `json:"summary,omitempty"`
	// diagnosis contains the root cause analysis specific to this option.
	// Present when analysisOutput mode is Default (or omitted). Omitted
	// when mode is Minimal.
	// +optional
	Diagnosis DiagnosisResult `json:"diagnosis,omitzero"`
	// proposal contains the remediation plan for this option.
	// Present when analysisOutput mode is Default (or omitted). Omitted
	// when mode is Minimal without an execution step.
	// +optional
	Proposal ProposalResult `json:"proposal,omitzero"`
	// verification contains the verification plan. Omitted when
	// verification is skipped in the workflow.
	// +optional
	Verification VerificationPlan `json:"verification,omitzero"`
	// rbac contains the RBAC permissions the execution agent will need.
	// The operator's policy engine validates these before creating the
	// actual Kubernetes RBAC resources. Omitted for advisory-only options.
	// +optional
	RBAC RBACResult `json:"rbac,omitzero"`
	// components contains optional adapter-defined structured data whose
	// shape is determined by spec.analysisOutput.schema on the Proposal.
	// The operator passes this through to the AnalysisResult CR; the
	// console renders it using adapter-specific UI components.
	// +optional
	// +kubebuilder:validation:Schemaless
	// +kubebuilder:pruning:PreserveUnknownFields
	Components *apiextensionsv1.JSON `json:"components,omitempty"`
}

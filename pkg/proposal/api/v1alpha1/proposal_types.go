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
	corev1 "k8s.io/api/core/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// ProposalPhase represents the current phase of the proposal lifecycle.
//
// The full lifecycle is:
//
//	Pending -> Analyzing -> Proposed -> [user approves] -> Approved -> Executing -> Verifying -> Completed
//	                                   [user denies]   -> Denied
//	                                   [exec skipped]  -> AwaitingSync (advisory/gitops)
//	                                   [failure]       -> Failed -> [retry] -> Pending (enriched context)
//	                                   [max retries]   -> Escalated (child proposal created)
//
// The operator is the sole writer of this field. Users influence transitions
// indirectly by approving or denying proposals.
//
// +kubebuilder:validation:Enum=Pending;Analyzing;Proposed;Approved;Denied;Executing;AwaitingSync;Verifying;Completed;Failed;Escalated
type ProposalPhase string

const (
	// ProposalPhasePending is the initial phase. The operator picks up the
	// proposal and prepares to launch the analysis sandbox. On retries,
	// the proposal returns to Pending with enriched context from previous
	// attempts.
	ProposalPhasePending ProposalPhase = "Pending"
	// ProposalPhaseAnalyzing means the analysis agent is running in a
	// sandbox pod, examining cluster state and producing a diagnosis,
	// remediation plan, and RBAC request.
	ProposalPhaseAnalyzing ProposalPhase = "Analyzing"
	// ProposalPhaseProposed means analysis is complete and the proposal
	// is waiting for user approval. The user can approve (to proceed with
	// execution), deny, or escalate.
	ProposalPhaseProposed ProposalPhase = "Proposed"
	// ProposalPhaseApproved means the user approved the proposal. The
	// operator creates execution RBAC (ServiceAccount, Role, RoleBinding)
	// and launches the execution sandbox.
	ProposalPhaseApproved ProposalPhase = "Approved"
	// ProposalPhaseDenied means the user rejected the proposal. This is
	// a terminal phase.
	ProposalPhaseDenied ProposalPhase = "Denied"
	// ProposalPhaseExecuting means the execution agent is running in a
	// sandbox pod, carrying out the approved remediation plan.
	ProposalPhaseExecuting ProposalPhase = "Executing"
	// ProposalPhaseAwaitingSync means execution was skipped (advisory-only
	// or gitops workflow). The user is expected to apply changes manually
	// or via GitOps, then mark the proposal as synced.
	ProposalPhaseAwaitingSync ProposalPhase = "AwaitingSync"
	// ProposalPhaseVerifying means the verification agent is running,
	// checking whether the remediation was successful.
	ProposalPhaseVerifying ProposalPhase = "Verifying"
	// ProposalPhaseCompleted means all steps finished successfully.
	// This is a terminal phase.
	ProposalPhaseCompleted ProposalPhase = "Completed"
	// ProposalPhaseFailed means a step failed. If retries remain, the
	// operator resets the proposal to Pending with failure context. If
	// maxAttempts is reached, it transitions to Escalated instead.
	ProposalPhaseFailed ProposalPhase = "Failed"
	// ProposalPhaseEscalated means the proposal exhausted all retry
	// attempts. The operator creates a child proposal (with parentRef
	// pointing back) containing the full failure history for
	// higher-privilege or human-assisted remediation.
	ProposalPhaseEscalated ProposalPhase = "Escalated"
)

// StepPhase represents the phase of a single step (analysis, execution, or
// verification) within a proposal's status. Set by the operator.
// +kubebuilder:validation:Enum=Pending;Running;Completed;Failed;Skipped
type StepPhase string

const (
	// StepPhasePending means the step has not started yet.
	StepPhasePending StepPhase = "Pending"
	// StepPhaseRunning means the agent sandbox is active and processing.
	StepPhaseRunning StepPhase = "Running"
	// StepPhaseCompleted means the step finished successfully.
	StepPhaseCompleted StepPhase = "Completed"
	// StepPhaseFailed means the step encountered an error.
	StepPhaseFailed StepPhase = "Failed"
	// StepPhaseSkipped means the step was skipped (per workflow or override).
	StepPhaseSkipped StepPhase = "Skipped"
)

// SandboxPhase identifies which workflow step a sandbox pod is running for.
// Used in PreviousAttempt to record which phase failed, and internally by the
// operator for sandbox lifecycle management.
// +kubebuilder:validation:Enum=analysis;execution;verification
type SandboxPhase string

const (
	// SandboxPhaseAnalysis is the analysis step sandbox.
	SandboxPhaseAnalysis SandboxPhase = "analysis"
	// SandboxPhaseExecution is the execution step sandbox.
	SandboxPhaseExecution SandboxPhase = "execution"
	// SandboxPhaseVerification is the verification step sandbox.
	SandboxPhaseVerification SandboxPhase = "verification"
)

// Condition types for Proposal. These follow the standard metav1.Condition
// pattern and are set by the operator to provide fine-grained observability
// beyond the top-level phase. Each condition has a type, status (True/False),
// reason, and message.
const (
	// ProposalConditionAnalyzed is set to True when analysis completes
	// successfully, False when analysis fails.
	ProposalConditionAnalyzed string = "Analyzed"
	// ProposalConditionApproved is set to True when the user approves,
	// False when denied.
	ProposalConditionApproved string = "Approved"
	// ProposalConditionExecuted is set to True when execution completes
	// successfully, False when execution fails.
	ProposalConditionExecuted string = "Executed"
	// ProposalConditionVerified is set to True when verification passes,
	// False when verification fails.
	ProposalConditionVerified string = "Verified"
	// ProposalConditionEscalated is set to True when the proposal has been
	// escalated (max retries exhausted).
	ProposalConditionEscalated string = "Escalated"
)

// DiagnosisResult contains the root cause analysis from the analysis agent.
// This is populated by the agent during the Analyzing phase and stored in
// the AnalysisStepStatus as part of a RemediationOption. Users see this
// in the console UI when reviewing the proposal.
type DiagnosisResult struct {
	// summary is a human-readable diagnosis summary explaining the problem,
	// its symptoms, and the agent's findings.
	Summary string `json:"summary"`
	// confidence is the agent's self-assessed confidence in its diagnosis.
	// Higher confidence generally correlates with clearer symptoms and
	// more deterministic root causes.
	// +kubebuilder:validation:Enum=low;medium;high
	Confidence string `json:"confidence"`
	// rootCause is a concise one-line description of the identified root
	// cause (e.g., "OOMKilled due to memory limit of 256Mi").
	RootCause string `json:"rootCause"`
}

// ProposedAction describes a single discrete action the analysis agent
// recommends as part of its remediation plan. Actions are displayed to
// the user in the Proposed phase for review before approval.
type ProposedAction struct {
	// type is the action category (e.g., "patch", "scale", "restart",
	// "create", "delete", "rollout").
	Type string `json:"type"`
	// description is a human-readable explanation of what this action
	// will do (e.g., "Increase memory limit from 256Mi to 512Mi").
	Description string `json:"description"`
}

// ProposalResult contains the remediation plan from the analysis agent.
// This is part of a RemediationOption and is presented to the user in the
// Proposed phase. The risk and reversibility assessments help users make
// informed approval decisions.
type ProposalResult struct {
	// description is a human-readable summary of the overall remediation
	// approach.
	Description string `json:"description"`
	// actions is the ordered list of discrete actions the agent proposes.
	Actions []ProposedAction `json:"actions"`
	// risk is the agent's assessment of how risky the remediation is.
	// Critical-risk proposals typically require explicit human review.
	// +kubebuilder:validation:Enum=low;medium;high;critical
	Risk string `json:"risk"`
	// reversible indicates whether the remediation can be rolled back
	// if something goes wrong. The rollback plan is in the
	// VerificationPlan.
	Reversible bool `json:"reversible"`
	// estimatedImpact describes the expected impact of the remediation
	// on the system (e.g., "Brief pod restart, ~30s downtime").
	// +optional
	EstimatedImpact string `json:"estimatedImpact,omitempty"`
}

// VerificationStep describes a single verification check that the
// verification agent should run after execution. Populated by the
// analysis agent as part of the RemediationOption.
type VerificationStep struct {
	// name is a short identifier for this check (e.g., "pod-running").
	Name string `json:"name"`
	// command is the command or API call to run for this check
	// (e.g., "oc get pod -n production -l app=web -o jsonpath='{.items[0].status.phase}'").
	Command string `json:"command"`
	// expected is the expected output or condition
	// (e.g., "Running", "ready=true").
	Expected string `json:"expected"`
	// type categorizes the check (e.g., "command", "metric", "condition").
	Type string `json:"type"`
}

// RollbackPlan describes how to undo the remediation if verification fails
// or the remediation causes unexpected issues. Populated by the analysis agent.
type RollbackPlan struct {
	// description is a human-readable explanation of the rollback strategy.
	Description string `json:"description"`
	// command is the rollback command or steps to execute.
	Command string `json:"command"`
}

// VerificationPlan describes the complete verification strategy for a
// remediation, including individual checks and a rollback plan. Populated
// by the analysis agent as part of a RemediationOption and used by the
// verification agent (if not skipped) to validate the remediation.
type VerificationPlan struct {
	// description is a human-readable summary of the verification approach.
	Description string `json:"description"`
	// steps is the ordered list of verification checks to run.
	// +optional
	Steps []VerificationStep `json:"steps,omitempty"`
	// rollbackPlan describes how to undo the remediation if verification
	// fails. Displayed to the user and available to the verification agent.
	// +optional
	RollbackPlan RollbackPlan `json:"rollbackPlan,omitempty"`
}

// RBACRule describes a single RBAC permission that the analysis agent
// requests for the execution phase. The operator's policy engine validates
// these requests against a 6-layer defense model before creating the
// actual Role/ClusterRole bindings. Each rule must include a justification
// so that users and policy can audit why the permission is needed.
type RBACRule struct {
	// namespace is the target namespace for namespace-scoped rules.
	// Must match one of the proposal's targetNamespaces. Ignored for
	// cluster-scoped rules.
	// +optional
	Namespace string `json:"namespace,omitempty"`
	// apiGroups are the API groups for this rule (e.g., "", "apps", "batch").
	APIGroups []string `json:"apiGroups"`
	// resources are the resource types (e.g., "pods", "deployments").
	Resources []string `json:"resources"`
	// resourceNames restricts the rule to specific named resources.
	// When empty, the rule applies to all resources of the given type.
	// +optional
	ResourceNames []string `json:"resourceNames,omitempty"`
	// verbs are the allowed operations (e.g., "get", "patch", "delete").
	Verbs []string `json:"verbs"`
	// justification explains why this permission is needed for the
	// remediation (e.g., "Need to patch deployment to increase memory limit").
	// Required for audit and policy enforcement.
	Justification string `json:"justification"`
}

// RBACResult contains the RBAC permissions requested by the analysis agent
// for the execution phase. The operator creates a dedicated ServiceAccount
// per proposal and binds these permissions via Role (namespace-scoped) or
// ClusterRole (cluster-scoped) before launching the execution sandbox.
// All RBAC resources are cleaned up after the proposal reaches a terminal phase.
type RBACResult struct {
	// namespaceScoped are rules that will be applied via Role + RoleBinding
	// in the proposal's target namespaces. These are the most common rules.
	// +optional
	NamespaceScoped []RBACRule `json:"namespaceScoped,omitempty"`
	// clusterScoped are rules that will be applied via ClusterRole +
	// ClusterRoleBinding. Used when the agent needs cross-namespace or
	// non-namespaced resource access (e.g., reading nodes, CRDs).
	// +optional
	ClusterScoped []RBACRule `json:"clusterScoped,omitempty"`
}

// RemediationOption represents a single remediation approach produced by
// the analysis agent. The agent may return multiple options, each with
// its own diagnosis, remediation plan, verification strategy, and RBAC
// requirements. The user selects one option during the Proposed phase
// (recorded in AnalysisStepStatus.selectedOption), and the operator uses
// that option's RBAC and plan for the execution phase.
//
// The components field is an extensibility point for adapter-specific UI
// data. For example, an ACS adapter might include violation details or
// affected deployment information as components that the console plugin
// renders with custom components.
type RemediationOption struct {
	// title is a short human-readable name for this option
	// (e.g., "Increase memory limit", "Restart with backoff").
	// +kubebuilder:validation:MinLength=1
	Title string `json:"title"`
	// summary is an optional one-line summary for collapsed views in the
	// console UI.
	// +optional
	Summary string `json:"summary,omitempty"`
	// diagnosis contains the root cause analysis specific to this option.
	Diagnosis DiagnosisResult `json:"diagnosis"`
	// proposal contains the remediation plan for this option.
	Proposal ProposalResult `json:"proposal"`
	// verification contains the verification plan. Omitted when
	// verification is skipped in the workflow.
	// +optional
	Verification *VerificationPlan `json:"verification,omitempty"`
	// rbac contains the RBAC permissions the execution agent will need.
	// The operator's policy engine validates these before creating the
	// actual Kubernetes RBAC resources. Omitted for advisory-only options.
	// +optional
	RBAC *RBACResult `json:"rbac,omitempty"`
	// components contains optional adapter-defined structured data for
	// custom console UI rendering. Each entry is a raw JSON object.
	// +optional
	Components []apiextensionsv1.JSON `json:"components,omitempty"`
}

// ExecutionAction describes a single action taken by the execution agent
// during the Executing phase. These are recorded in ExecutionStepStatus
// to provide an audit trail of what the agent actually did.
type ExecutionAction struct {
	// type is the action category (e.g., "patch", "scale", "restart").
	Type string `json:"type"`
	// description is what the agent did
	// (e.g., "Patched deployment/web to set memory limit to 512Mi").
	Description string `json:"description"`
	// success indicates whether this individual action succeeded.
	Success bool `json:"success"`
	// output is the command output or API response from the action.
	// +optional
	Output string `json:"output,omitempty"`
	// error is the error message if the action failed.
	// +optional
	Error string `json:"error,omitempty"`
}

// ExecutionVerification is a lightweight inline verification that the
// execution agent performs immediately after completing its actions,
// before the formal verification step. This gives early signal on whether
// the remediation worked. In trust-mode workflows (verification skipped),
// this is the only verification that occurs.
type ExecutionVerification struct {
	// conditionImproved indicates whether the target condition improved
	// after the remediation (e.g., pod is no longer CrashLoopBackOff).
	ConditionImproved bool `json:"conditionImproved"`
	// summary is a human-readable summary of the inline verification.
	Summary string `json:"summary"`
}

// VerifyCheck is a single verification check result from the verification
// agent. Each check corresponds to a VerificationStep from the analysis
// agent's verification plan.
type VerifyCheck struct {
	// name is the check identifier, matching the VerificationStep name.
	Name string `json:"name"`
	// source is what performed the check (e.g., "oc", "promql", "curl").
	Source string `json:"source"`
	// value is the actual observed value (e.g., "Running", "3 replicas").
	Value string `json:"value"`
	// passed indicates whether the check's observed value matches
	// the expected value.
	Passed bool `json:"passed"`
}

// SandboxInfo tracks the sandbox pod used for a workflow step. The operator
// creates a sandbox pod for each active step (analysis, execution,
// verification) and records the claim details here. This enables the
// console UI to stream sandbox pod logs in real time.
type SandboxInfo struct {
	// claimName is the name of the SandboxClaim resource that owns the
	// sandbox pod.
	// +optional
	ClaimName string `json:"claimName,omitempty"`
	// namespace is the namespace where the SandboxClaim and its pod live.
	// +optional
	Namespace string `json:"namespace,omitempty"`
	// startedAt is when the sandbox pod was created.
	// +optional
	StartedAt *metav1.Time `json:"startedAt,omitempty"`
	// completedAt is when the sandbox pod finished (success or failure).
	// +optional
	CompletedAt *metav1.Time `json:"completedAt,omitempty"`
}

// AnalysisStepStatus is the observed state of the analysis step.
// All fields are populated by the operator based on the analysis agent's
// output. The options field is the most important -- it contains the
// remediation options the user chooses from in the Proposed phase.
type AnalysisStepStatus struct {
	// phase is the step phase.
	// +optional
	Phase StepPhase `json:"phase,omitempty"`
	// startedAt is when the step started.
	// +optional
	StartedAt *metav1.Time `json:"startedAt,omitempty"`
	// completedAt is when the step completed.
	// +optional
	CompletedAt *metav1.Time `json:"completedAt,omitempty"`
	// conditions for this step.
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
	// sandbox tracks the sandbox used.
	// +optional
	Sandbox SandboxInfo `json:"sandbox,omitempty"`
	// options contains one or more remediation options returned by the
	// analysis agent. Each option has its own diagnosis, plan, verification
	// strategy, and RBAC requirements. The user reviews these in the
	// Proposed phase and selects one to approve.
	// +optional
	Options []RemediationOption `json:"options,omitempty"`
	// selectedOption is the 0-based index into the options array that the
	// user approved. Set when the user approves the proposal. The operator
	// uses this to determine which option's RBAC and plan to use for
	// execution.
	// +optional
	// +kubebuilder:validation:Minimum=0
	SelectedOption *int `json:"selectedOption,omitempty"`
	// components contains optional adapter-specific UI components that
	// apply to the analysis step as a whole (not to a specific option).
	// +optional
	Components []apiextensionsv1.JSON `json:"components,omitempty"`
}

// ExecutionStepStatus is the observed state of the execution step.
// Populated by the operator from the execution agent's output. Contains
// an audit trail of every action the agent took and whether each succeeded.
type ExecutionStepStatus struct {
	// phase is the step phase.
	// +optional
	Phase StepPhase `json:"phase,omitempty"`
	// startedAt is when the step started.
	// +optional
	StartedAt *metav1.Time `json:"startedAt,omitempty"`
	// completedAt is when the step completed.
	// +optional
	CompletedAt *metav1.Time `json:"completedAt,omitempty"`
	// conditions for this step.
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
	// sandbox tracks the sandbox used.
	// +optional
	Sandbox SandboxInfo `json:"sandbox,omitempty"`
	// success indicates whether execution completed successfully.
	// +optional
	Success *bool `json:"success,omitempty"`
	// actionsTaken lists what the agent did.
	// +optional
	ActionsTaken []ExecutionAction `json:"actionsTaken,omitempty"`
	// verification is the inline verification from the execution agent.
	// +optional
	Verification *ExecutionVerification `json:"verification,omitempty"`
	// components contains optional adapter-defined structured data.
	// +optional
	Components []apiextensionsv1.JSON `json:"components,omitempty"`
}

// VerificationStepStatus is the observed state of the verification step.
// Populated by the operator from the verification agent's output. Contains
// individual check results and an overall success/failure assessment.
type VerificationStepStatus struct {
	// phase is the step phase.
	// +optional
	Phase StepPhase `json:"phase,omitempty"`
	// startedAt is when the step started.
	// +optional
	StartedAt *metav1.Time `json:"startedAt,omitempty"`
	// completedAt is when the step completed.
	// +optional
	CompletedAt *metav1.Time `json:"completedAt,omitempty"`
	// conditions for this step.
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
	// sandbox tracks the sandbox used.
	// +optional
	Sandbox SandboxInfo `json:"sandbox,omitempty"`
	// success indicates whether verification passed.
	// +optional
	Success *bool `json:"success,omitempty"`
	// checks contains individual verification check results.
	// +optional
	Checks []VerifyCheck `json:"checks,omitempty"`
	// summary is a human-readable verification summary.
	// +optional
	Summary string `json:"summary,omitempty"`
	// components contains optional adapter-defined structured data.
	// +optional
	Components []apiextensionsv1.JSON `json:"components,omitempty"`
}

// StepsStatus contains the per-step observed state for all three workflow
// steps. Each step status is populated independently as the proposal
// progresses through its lifecycle. All fields are set by the operator.
type StepsStatus struct {
	// analysis is the observed state of the analysis step.
	// +optional
	Analysis AnalysisStepStatus `json:"analysis,omitempty"`
	// execution is the observed state of the execution step.
	// +optional
	Execution ExecutionStepStatus `json:"execution,omitempty"`
	// verification is the observed state of the verification step.
	// +optional
	Verification VerificationStepStatus `json:"verification,omitempty"`
}

// WorkflowStepOverride allows overriding a single step of the referenced
// workflow without creating a new Workflow CR. Each field is optional --
// only the fields you set are overridden; everything else comes from the
// Workflow.
type WorkflowStepOverride struct {
	// skip overrides the skip flag for this step. When set to true, the step
	// is skipped regardless of the Workflow's setting. When set to false,
	// the step runs even if the Workflow says skip.
	// +optional
	Skip *bool `json:"skip,omitempty"`
	// agentRef overrides the agent used for this step. Allows using a
	// different agent for a specific proposal without changing the Workflow.
	// +optional
	AgentRef *corev1.LocalObjectReference `json:"agentRef,omitempty"`
}

// WorkflowOverride allows per-proposal overrides of the referenced workflow.
// This is useful for one-off customizations: for example, using a
// remediation workflow but skipping execution to make it advisory-only,
// or swapping in a specialized agent for a specific proposal.
//
// Example — skip execution on a remediation workflow to make it advisory:
//
//	workflowOverride:
//	  execution:
//	    skip: true
//	  verification:
//	    skip: true
//
// Example — use a specialized ACS analyzer agent for one proposal:
//
//	workflowOverride:
//	  analysis:
//	    agentRef:
//	      name: acs-analyzer
type WorkflowOverride struct {
	// analysis overrides for the analysis step.
	// +optional
	Analysis *WorkflowStepOverride `json:"analysis,omitempty"`
	// execution overrides for the execution step.
	// +optional
	Execution *WorkflowStepOverride `json:"execution,omitempty"`
	// verification overrides for the verification step.
	// +optional
	Verification *WorkflowStepOverride `json:"verification,omitempty"`
}

// PreviousAttempt captures the state of a failed attempt. When a proposal
// fails and retries, the operator records the failure context here so that
// the analysis agent on the next attempt can learn from previous failures.
// If maxAttempts is reached, the full history of PreviousAttempts is
// included in the escalation child proposal.
type PreviousAttempt struct {
	// attempt is the 1-based attempt number that failed.
	Attempt int `json:"attempt"`
	// failedPhase is which step failed (analysis, execution, or verification).
	// +optional
	FailedPhase SandboxPhase `json:"failedPhase,omitempty"`
	// failureReason is the error message or explanation from the failed step.
	// +optional
	FailureReason string `json:"failureReason,omitempty"`
}

// ProposalSpec defines the desired state of Proposal. This is the user-facing
// (or adapter-facing) configuration -- everything the operator needs to start
// processing the proposal.
type ProposalSpec struct {
	// request is the user's original request, alert description, or a
	// description of what triggered this proposal. This text is passed to
	// the analysis agent as the primary input. For adapter-created proposals,
	// this typically contains the alert summary and relevant details.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	Request string `json:"request"`

	// workflowRef references a cluster-scoped Workflow CR that defines
	// which agents handle each step (analysis, execution, verification)
	// and which steps are skipped. This is the primary routing mechanism.
	// +kubebuilder:validation:Required
	WorkflowRef corev1.LocalObjectReference `json:"workflowRef"`

	// targetNamespaces are the Kubernetes namespace(s) this proposal
	// operates on. The operator uses these to scope RBAC (creating Roles
	// and RoleBindings only in these namespaces) and to pass context to
	// the analysis agent. When empty, the proposal operates at the
	// cluster level only.
	// +optional
	TargetNamespaces []string `json:"targetNamespaces,omitempty"`

	// workflowOverride allows per-proposal overrides of the referenced
	// workflow without creating a new Workflow CR. Useful for one-off
	// customizations like skipping execution on a normally full-lifecycle
	// workflow, or swapping in a specialized agent.
	// +optional
	WorkflowOverride *WorkflowOverride `json:"workflowOverride,omitempty"`

	// parentRef references the parent proposal in an escalation chain.
	// Set automatically by the operator when creating a child proposal
	// after maxAttempts is exhausted. The child proposal inherits the
	// full failure history from its parent. The child is also owned by
	// the parent via Kubernetes owner references for garbage collection.
	// +optional
	ParentRef *corev1.LocalObjectReference `json:"parentRef,omitempty"`

	// maxAttempts overrides the global retry limit for this proposal.
	// When a step fails, the operator resets the proposal to Pending
	// with enriched context (up to maxAttempts times). After that, the
	// proposal transitions to Escalated. Set to 0 to disable retries.
	// When omitted, the operator's global default is used.
	// +optional
	// +kubebuilder:validation:Minimum=0
	// +kubebuilder:validation:Maximum=20
	MaxAttempts *int `json:"maxAttempts,omitempty"`
}

// ProposalStatus defines the observed state of Proposal. All fields are
// set by the operator -- users should not modify status fields directly.
// The status provides complete observability into the proposal's progress,
// including per-step results, retry history, and standard Kubernetes conditions.
type ProposalStatus struct {
	// phase is the current phase of the proposal lifecycle.
	// See ProposalPhase for the full state machine.
	// +kubebuilder:default=Pending
	Phase ProposalPhase `json:"phase"`

	// attempt is the current attempt number (1-based). Incremented each
	// time the proposal is retried after a failure. Starts at 1 for the
	// first attempt.
	// +optional
	Attempt int `json:"attempt,omitempty"`

	// steps contains the per-step observed state (analysis, execution,
	// verification). Each step independently tracks its phase, timing,
	// sandbox info, and results.
	// +optional
	Steps StepsStatus `json:"steps,omitempty"`

	// previousAttempts contains the failure history from earlier attempts.
	// Each entry records which phase failed and why, giving the analysis
	// agent on the next attempt context to avoid repeating the same mistake.
	// +optional
	PreviousAttempts []PreviousAttempt `json:"previousAttempts,omitempty"`

	// conditions represent the latest available observations using the
	// standard Kubernetes condition pattern. Condition types include:
	// Analyzed, Approved, Executed, Verified, and Escalated.
	// +listType=map
	// +listMapKey=type
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Workflow",type=string,JSONPath=`.spec.workflowRef.name`
// +kubebuilder:printcolumn:name="Phase",type=string,JSONPath=`.status.phase`
// +kubebuilder:printcolumn:name="Request",type=string,JSONPath=`.spec.request`,priority=1
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// Proposal represents a unit of work managed by the agentic platform. It is
// the final link in the CRD chain (LlmProvider -> Agent -> Workflow ->
// Proposal) and the primary resource users and adapters interact with.
//
// A Proposal references a Workflow that defines which agents handle each
// step, and tracks the full lifecycle from initial request through analysis,
// user approval, execution, and verification. Proposals are created by
// adapters (AlertManager webhook, ACS violation webhook, manual creation)
// or by the operator itself (escalation child proposals).
//
// Proposal is cluster-scoped. The operator watches for new Proposals and
// drives them through the lifecycle automatically. Users interact with
// proposals in the Proposed phase to approve, deny, or escalate.
//
// Example — a remediation proposal targeting a specific namespace:
//
//	apiVersion: agentic.openshift.io/v1alpha1
//	kind: Proposal
//	metadata:
//	  name: fix-crashloop
//	spec:
//	  request: |
//	    Pod web-frontend-5d4b8c6f-x9k2m in namespace production is in
//	    CrashLoopBackOff. Last restart reason: OOMKilled. Container memory
//	    limit is 256Mi.
//	  workflowRef:
//	    name: remediation
//	  targetNamespaces:
//	    - production
//
// Example — advisory-only via workflowOverride (reuses a remediation workflow
// but skips execution so the user applies changes manually):
//
//	apiVersion: agentic.openshift.io/v1alpha1
//	kind: Proposal
//	metadata:
//	  name: one-off-advisory
//	spec:
//	  request: "Review the nginx deployment in staging for security best practices"
//	  workflowRef:
//	    name: remediation
//	  targetNamespaces:
//	    - staging
//	  workflowOverride:
//	    execution:
//	      skip: true
//	    verification:
//	      skip: true
//
// Example — an upgrade proposal with limited retries:
//
//	apiVersion: agentic.openshift.io/v1alpha1
//	kind: Proposal
//	metadata:
//	  name: upgrade-4-22
//	spec:
//	  request: "Analyze and plan upgrade from OpenShift 4.21 to 4.22"
//	  workflowRef:
//	    name: upgrade
//	  maxAttempts: 2
type Proposal struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	// +kubebuilder:validation:Required
	Spec ProposalSpec `json:"spec"`

	// +optional
	Status ProposalStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// ProposalList contains a list of Proposal.
type ProposalList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Proposal `json:"items"`
}

func init() {
	SchemeBuilder.Register(&Proposal{}, &ProposalList{})
}

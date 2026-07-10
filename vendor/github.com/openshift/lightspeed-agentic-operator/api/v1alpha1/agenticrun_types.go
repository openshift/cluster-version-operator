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
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// AgenticRunPhase summarizes the agentic run's lifecycle state for display.
// This type is used internally by the controller, CLI, and console to
// derive a human-friendly phase from conditions. It is NOT stored on
// the CRD status — use DerivePhase(conditions) to compute it.
type AgenticRunPhase string

const (
	AgenticRunPhasePending          AgenticRunPhase = "Pending"
	AgenticRunPhaseAnalyzing        AgenticRunPhase = "Analyzing"
	AgenticRunPhaseProposed         AgenticRunPhase = "Proposed"
	AgenticRunPhaseExecuting        AgenticRunPhase = "Executing"
	AgenticRunPhaseVerifying        AgenticRunPhase = "Verifying"
	AgenticRunPhaseCompleted        AgenticRunPhase = "Completed"
	AgenticRunPhaseFailed           AgenticRunPhase = "Failed"
	AgenticRunPhaseDenied           AgenticRunPhase = "Denied"
	AgenticRunPhaseEscalating       AgenticRunPhase = "Escalating"
	AgenticRunPhaseEscalated        AgenticRunPhase = "Escalated"
	AgenticRunPhaseEmergencyStopped AgenticRunPhase = "EmergencyStopped"
)

// Condition reasons used by DerivePhase for state transitions.
// SYNC: must match derivePhaseFromConditions in lightspeed-agentic-console/src/models/agenticrun.ts
const (
	ReasonRetryingExecution = "RetryingExecution"
	ReasonRetriesExhausted  = "RetriesExhausted"
)

// DerivePhase computes the display phase from conditions. Conditions are
// the source of truth; this function maps them to a human-friendly phase
// for display in CLI, console, and controller routing.
// SYNC: must match derivePhaseFromConditions in lightspeed-agentic-console/src/models/agenticrun.ts
func DerivePhase(conditions []metav1.Condition) AgenticRunPhase {
	get := func(condType string) *metav1.Condition {
		for i := range conditions {
			if conditions[i].Type == condType {
				return &conditions[i]
			}
		}
		return nil
	}

	if c := get(AgenticRunConditionEmergencyStopped); c != nil && c.Status == metav1.ConditionTrue {
		return AgenticRunPhaseEmergencyStopped
	}

	escalated := get(AgenticRunConditionEscalated)
	if escalated != nil && escalated.Status == metav1.ConditionTrue {
		return AgenticRunPhaseEscalated
	}

	if c := get(AgenticRunConditionDenied); c != nil && c.Status == metav1.ConditionTrue {
		return AgenticRunPhaseDenied
	}

	if escalated != nil {
		switch escalated.Status {
		case metav1.ConditionUnknown:
			return AgenticRunPhaseEscalating
		default:
			return AgenticRunPhaseFailed
		}
	}

	if c := get(AgenticRunConditionVerified); c != nil {
		switch c.Status {
		case metav1.ConditionTrue:
			return AgenticRunPhaseCompleted
		case metav1.ConditionUnknown:
			return AgenticRunPhaseVerifying
		default:
			if c.Reason == ReasonRetryingExecution {
				return AgenticRunPhaseExecuting
			}
			return AgenticRunPhaseFailed
		}
	}

	if c := get(AgenticRunConditionExecuted); c != nil {
		switch c.Status {
		case metav1.ConditionTrue:
			return AgenticRunPhaseVerifying
		case metav1.ConditionUnknown:
			return AgenticRunPhaseExecuting
		default:
			return AgenticRunPhaseFailed
		}
	}

	if c := get(AgenticRunConditionAnalyzed); c != nil {
		switch c.Status {
		case metav1.ConditionTrue:
			return AgenticRunPhaseProposed
		case metav1.ConditionUnknown:
			return AgenticRunPhaseAnalyzing
		default:
			return AgenticRunPhaseFailed
		}
	}

	return AgenticRunPhasePending
}

// StepPhase summarizes a single step's lifecycle state for display.
// Derived from per-step conditions via DeriveStepPhase; never stored on the CRD.
type StepPhase string

const (
	StepPhasePendingApproval StepPhase = "PendingApproval"
	StepPhaseRunning         StepPhase = "Running"
	StepPhaseCompleted       StepPhase = "Completed"
	StepPhaseFailed          StepPhase = "Failed"
	StepPhaseSkipped         StepPhase = "Skipped"
)

// SandboxStep identifies which workflow step a sandbox pod is running for.
// Used in PreviousAttempt to record which step failed, and internally by the
// operator for sandbox lifecycle management.
// +kubebuilder:validation:Enum=Analysis;Execution;Verification;Escalation
type SandboxStep string

const (
	// SandboxStepAnalysis is the analysis step sandbox.
	SandboxStepAnalysis SandboxStep = "Analysis"
	// SandboxStepExecution is the execution step sandbox.
	SandboxStepExecution SandboxStep = "Execution"
	// SandboxStepVerification is the verification step sandbox.
	SandboxStepVerification SandboxStep = "Verification"
	// SandboxStepEscalation is the escalation step sandbox.
	SandboxStepEscalation SandboxStep = "Escalation"
)

// AnalysisOutputMode controls which built-in properties the analysis output
// schema includes. Use Default to get the full schema (diagnosis, remediation plan,
// RBAC, verification). Use Minimal to get only the base structure (options
// array with title) — suitable for analysis-only runs that define
// their own output shape via the schema field.
//
// Allowed values:
//   - "Default" — Full analysis output schema with all built-in properties.
//   - "Minimal" — Base structure only (options array with title per option).
//
// +kubebuilder:validation:Enum=Default;Minimal
type AnalysisOutputMode string

const (
	// AnalysisOutputModeDefault uses the full analysis output schema with
	// all built-in properties (diagnosis, remediation plan, summary, rbac, verification).
	AnalysisOutputModeDefault AnalysisOutputMode = "Default"
	// AnalysisOutputModeMinimal uses a minimal analysis output schema with
	// only the base structure (options array with title per option).
	// Built-in properties are omitted unless required by the workflow
	// (e.g., rbac is added when an execution step exists).
	AnalysisOutputModeMinimal AnalysisOutputMode = "Minimal"
)

// AnalysisOutput configures the analysis step's structured output schema.
// The mode field controls which built-in properties are included. The
// schema field optionally defines adapter-specific structured data that
// is injected as a required "components" property in each option.
//
// +kubebuilder:validation:MinProperties=1
// +kubebuilder:validation:XValidation:rule="self.mode != 'Minimal' || has(self.schema)",message="schema is required when mode is Minimal"
type AnalysisOutput struct {
	// mode controls which built-in properties the analysis output schema
	// includes. Default includes all built-in properties (diagnosis,
	// remediation plan, summary, rbac, verification). Minimal includes only the
	// base structure (options array with title per option). Omit or set
	// to "Default" for standard remediation workflows.
	// +optional
	// +default="Default"
	Mode AnalysisOutputMode `json:"mode,omitempty"`

	// schema is a JSON Schema injected as a required "components"
	// property in each analysis output option. Use this to require
	// adapter-specific structured data beyond the base analysis schema.
	// +optional
	// +kubebuilder:validation:Schemaless
	// +kubebuilder:validation:Type=object
	// +kubebuilder:pruning:PreserveUnknownFields
	Schema *apiextensionsv1.JSONSchemaProps `json:"schema,omitempty"`
}

func (a AnalysisOutput) IsZero() bool {
	return a.Mode == "" && a.Schema == nil
}

// Condition types for AgenticRun. Conditions are the primary mechanism for
// observing agentic run state. The operator sets these as the run
// progresses through its lifecycle. Each condition has a type, status
// (True/False/Unknown), reason (CamelCase token), and message.
//
// The lifecycle is derived from the combination of conditions:
//
//	No conditions       -> just created, pending
//	Analyzed=Unknown    -> analysis in progress
//	Analyzed=True       -> analysis complete, next step queued
//	Executed=Unknown    -> execution in progress
//	Executed=True       -> execution complete
//	Verified=Unknown    -> verification in progress
//	Verified=True       -> verification passed (terminal: success)
//	Denied=True         -> user denied a step (terminal)
//	Escalated=True      -> max retries exhausted (terminal)
//	Any condition=False -> step failed; check reason and message
const (
	// AgenticRunConditionAnalyzed indicates whether analysis has completed.
	// Status=True when analysis succeeds, Status=False on failure,
	// Status=Unknown while analysis is in progress.
	AgenticRunConditionAnalyzed string = "Analyzed"
	// AgenticRunConditionExecuted indicates whether execution has completed.
	// Status=True when execution succeeds, Status=False on failure,
	// Status=Unknown while execution is in progress.
	AgenticRunConditionExecuted string = "Executed"
	// AgenticRunConditionVerified indicates whether verification has passed.
	// Status=True when verification succeeds, Status=False on failure,
	// Status=Unknown while verification is in progress.
	AgenticRunConditionVerified string = "Verified"
	// AgenticRunConditionDenied indicates the user denied a step on the
	// AgenticRunApproval resource. Status=True when denied (terminal).
	AgenticRunConditionDenied string = "Denied"
	// AgenticRunConditionEscalated indicates escalation state. Status=Unknown
	// while escalation is pending approval or in progress, Status=True when
	// escalation completes (terminal), Status=False on escalation failure.
	AgenticRunConditionEscalated        string = "Escalated"
	AgenticRunConditionEmergencyStopped string = "EmergencyStopped"
)

// AgenticRunStep defines per-step configuration on an AgenticRun. The agent
// field selects which cluster-scoped Agent CR handles this step. The
// tools field provides per-step tools that replace the shared spec.tools.
// +kubebuilder:validation:MinProperties=1
type AgenticRunStep struct {
	// agent is the name of the cluster-scoped Agent CR to use for this step.
	// Defaults to "default" when omitted.
	// +optional
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=253
	// +kubebuilder:validation:XValidation:rule="!format.dns1123Subdomain().validate(self).hasValue()",message="must be a valid DNS subdomain: lowercase alphanumeric characters, hyphens, and dots"
	Agent string `json:"agent,omitempty"`

	// tools provides per-step tools that replace the shared spec.tools
	// for this step. Use this when different steps need different skills.
	// +optional
	Tools ToolsSpec `json:"tools,omitzero"`
}

func (s AgenticRunStep) IsZero() bool {
	return s.Agent == "" && s.Tools.IsZero()
}

// AgenticRunSpec defines the desired state of AgenticRun.
//
// A AgenticRun defines the workflow shape inline, specifying which steps
// run and which agent handles each step. Analysis is always required.
// Omit execution and/or verification to skip those steps.
//
// +kubebuilder:validation:XValidation:rule="has(self.analysis)",message="analysis must be provided"
// +kubebuilder:validation:XValidation:rule="!has(oldSelf.targetNamespaces) || (has(self.targetNamespaces) && self.targetNamespaces == oldSelf.targetNamespaces)",message="targetNamespaces is immutable once set"
// +kubebuilder:validation:XValidation:rule="!has(oldSelf.analysisOutput) || (has(self.analysisOutput) && self.analysisOutput == oldSelf.analysisOutput)",message="analysisOutput is immutable once set"
// +kubebuilder:validation:XValidation:rule="!has(self.analysisOutput) || self.analysisOutput.mode != 'Minimal' || (!has(self.execution) && !has(self.verification))",message="analysisOutput mode Minimal is only allowed for analysis-only runs (no execution or verification steps)"
// +kubebuilder:validation:XValidation:rule="!has(oldSelf.tools) || (has(self.tools) && self.tools == oldSelf.tools)",message="tools is immutable once set"
// +kubebuilder:validation:XValidation:rule="!has(oldSelf.analysis) || (has(self.analysis) && self.analysis == oldSelf.analysis)",message="analysis is immutable once set"
// +kubebuilder:validation:XValidation:rule="!has(oldSelf.execution) || (has(self.execution) && self.execution == oldSelf.execution)",message="execution is immutable once set"
// +kubebuilder:validation:XValidation:rule="!has(oldSelf.verification) || (has(self.verification) && self.verification == oldSelf.verification)",message="verification is immutable once set"
type AgenticRunSpec struct {
	// request is the user's original request, alert description, or a
	// description of what triggered this agentic run. This text is passed to
	// the analysis agent as the primary input.
	//
	// Immutable: AgenticRuns are run-to-completion (like Jobs). To change
	// the request, create a new AgenticRun. Use spec.revisionFeedback for
	// iterative feedback on an existing analysis.
	// +required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=32768
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="request is immutable after creation"
	Request string `json:"request,omitempty"`

	// targetNamespaces are the Kubernetes namespace(s) this agentic run
	// operates on. Used for RBAC scoping and context to the analysis agent.
	//
	// When omitted, the run is not namespace-scoped — the analysis
	// agent determines the relevant namespaces from the request context.
	// Adapters (AlertManager, ACS) typically set this automatically from
	// the source event.
	//
	// Immutable: RBAC scoping is fixed at creation. Changing target
	// namespaces mid-flight would invalidate the analysis and any
	// granted execution RBAC.
	// +optional
	// +listType=atomic
	// +kubebuilder:validation:MinItems=1
	// +kubebuilder:validation:MaxItems=50
	// +kubebuilder:validation:XValidation:rule="self.all(ns, !format.dns1123Label().validate(ns).hasValue())",message="each namespace must be a valid DNS label"
	// +kubebuilder:validation:items:MinLength=1
	// +kubebuilder:validation:items:MaxLength=63
	TargetNamespaces []string `json:"targetNamespaces,omitempty"`

	// analysisOutput configures the analysis step's structured output.
	// The mode field controls which built-in properties are included
	// (Default: all; Minimal: only title). The schema field optionally
	// defines adapter-specific structured data injected as "components".
	//
	// When omitted, the analysis uses the full default schema with all
	// built-in properties and no custom components.
	//
	// Immutable: the output contract is fixed at creation.
	// +optional
	AnalysisOutput AnalysisOutput `json:"analysisOutput,omitzero"`

	// tools defines the default tools for all steps: skills images,
	// MCP servers, and required secrets. Per-step tools
	// (analysis.tools, execution.tools, verification.tools) replace
	// this default for individual steps.
	//
	// Immutable: the skills and secrets available to the agent are
	// fixed at creation. Changing tools mid-flight could violate the
	// assumptions of an in-progress analysis or execution.
	// +optional
	Tools ToolsSpec `json:"tools,omitzero"`

	// analysis defines per-step configuration for the analysis step,
	// including which agent handles it and any per-step tools.
	//
	// Immutable: agent and per-step tools are fixed at creation.
	// +required
	Analysis AgenticRunStep `json:"analysis,omitzero"`

	// execution defines per-step configuration for the execution step.
	// Omit to skip execution (advisory/assisted patterns).
	//
	// Immutable: agent and per-step tools are fixed at creation.
	// +optional
	Execution AgenticRunStep `json:"execution,omitzero"`

	// verification defines per-step configuration for the verification step.
	// Omit to skip verification.
	//
	// Immutable: agent and per-step tools are fixed at creation.
	// +optional
	Verification AgenticRunStep `json:"verification,omitzero"`

	// revisionFeedback is the user's free-text feedback requesting changes
	// to the analysis. Patching this field bumps metadata.generation, which
	// the operator detects (generation > observedGeneration) and triggers
	// re-analysis with the feedback appended to the original request.
	//
	// Mutable: this is the only mutable spec field. All other spec fields
	// are immutable via CEL rules, so generation changes signal revision.
	// +optional
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=32768
	RevisionFeedback string `json:"revisionFeedback,omitempty"`
}

// AgenticRunStatus defines the observed state of AgenticRun. All fields are
// set by the operator -- users should not modify status fields directly.
// The status provides complete observability into the run's progress,
// including per-step results, retry history, and standard Kubernetes conditions.
// An empty status (`status: {}`) is the initial state before the operator's
// first reconcile.
//
// +kubebuilder:validation:MinProperties=1
type AgenticRunStatus struct {
	// conditions represent the latest available observations using the
	// standard Kubernetes condition pattern. Condition types include:
	// Analyzed, Approved, Executed, Verified, and Escalated.
	// +listType=map
	// +listMapKey=type
	// +patchStrategy=merge
	// +patchMergeKey=type
	// +optional
	// +kubebuilder:validation:MinItems=1
	// +kubebuilder:validation:MaxItems=9
	Conditions []metav1.Condition `json:"conditions,omitempty" patchStrategy:"merge" patchMergeKey:"type" protobuf:"bytes,1,rep,name=conditions"`

	// steps contains the per-step observed state (analysis, execution,
	// verification). Each step independently tracks its timing, sandbox
	// info, and references to result CRs.
	// +optional
	Steps StepsStatus `json:"steps,omitzero"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Namespaced
// +kubebuilder:printcolumn:name="Request",type=string,JSONPath=`.spec.request`,priority=1
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// AgenticRun represents a unit of work managed by the agentic platform.
// It is the primary resource component teams and adapters interact with.
//
// A AgenticRun defines the workflow shape inline: which steps run and which
// agent handles each step. Analysis is always required. Omit execution
// and/or verification to skip those steps.
//
// Example — analysis only (advisory):
//
//	apiVersion: agentic.openshift.io/v1alpha1
//	kind: AgenticRun
//	metadata:
//	  name: one-off-investigation
//	spec:
//	  request: "Investigate why pod foo is crashlooping"
//	  targetNamespaces:
//	    - lightspeed-demo
//	  tools:
//	    skills:
//	      - image: registry.redhat.io/acs/acs-agentic-skills:latest
//	  analysis:
//	    agent: smart
//
// Example — full remediation (analyze → execute → verify):
//
//	apiVersion: agentic.openshift.io/v1alpha1
//	kind: AgenticRun
//	metadata:
//	  name: fix-nginx-cve-2024-1234
//	  namespace: stackrox
//	spec:
//	  request: "Fix CVE-2024-1234 in nginx:1.21"
//	  targetNamespaces:
//	    - lightspeed-demo
//	  tools:
//	    skills:
//	      - image: registry.redhat.io/acs/acs-agentic-skills:latest
//	    requiredSecrets:
//	      - name: acs-api-token
//	        mountAs:
//	          type: EnvVar
//	          envVar:
//	            name: ACS_API_TOKEN
//	  analysis:
//	    agent: smart
//	  execution: {}
//	  verification:
//	    agent: fast
type AgenticRun struct {
	metav1.TypeMeta `json:",inline"`

	// metadata is the standard object metadata.
	// +optional
	metav1.ObjectMeta `json:"metadata,omitempty"`

	// spec defines the desired state of AgenticRun.
	// +required
	Spec AgenticRunSpec `json:"spec,omitzero"`

	// status defines the observed state of AgenticRun.
	// +optional
	Status AgenticRunStatus `json:"status,omitzero"`
}

// +kubebuilder:object:root=true

// AgenticRunList contains a list of AgenticRun.
type AgenticRunList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []AgenticRun `json:"items"`
}

func init() {
	SchemeBuilder.Register(&AgenticRun{}, &AgenticRunList{})
}

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
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// WorkflowStep defines the configuration for a single step in a workflow.
// Each step either references an Agent to execute it, or is skipped entirely.
//
// Skipping a step changes the proposal lifecycle:
//   - Skip analysis: Not recommended. Analysis produces the diagnosis and
//     remediation plan that drive downstream steps.
//   - Skip execution: The proposal transitions to AwaitingSync after approval,
//     making it advisory-only. The user is expected to apply changes manually
//     or via GitOps. Useful for gitops-remediation and advisory-only workflows.
//   - Skip verification: The proposal completes immediately after execution
//     without a verification check. Useful for trust-mode workflows where
//     the execution agent's inline verification is sufficient.
type WorkflowStep struct {
	// agentRef references a cluster-scoped Agent CR to use for this step.
	// The operator resolves this reference and launches a sandbox pod with
	// the agent's LLM, skills, and system prompt to process the step.
	// Required when skip is false; must be omitted or nil when skip is true.
	// +optional
	AgentRef *corev1.LocalObjectReference `json:"agentRef,omitempty"`

	// skip skips this step entirely. When true, agentRef is not needed and
	// the operator advances the proposal past this step automatically.
	// See WorkflowStep documentation for the effect of skipping each step.
	// +optional
	Skip bool `json:"skip,omitempty"`
}

// WorkflowSpec defines the desired state of Workflow.
//
// A workflow is a 3-step pipeline template. The steps always run in order:
// analysis -> execution -> verification. Between analysis and execution,
// the proposal pauses in the Proposed phase for user approval (unless the
// operator is configured for auto-approve).
type WorkflowSpec struct {
	// analysis defines the analysis step. The analysis agent examines the
	// cluster state, produces a diagnosis (root cause, confidence), a
	// remediation proposal (actions, risk, reversibility), a verification
	// plan, and RBAC permissions needed for execution.
	// +kubebuilder:validation:Required
	Analysis WorkflowStep `json:"analysis"`

	// execution defines the execution step. The execution agent carries out
	// the approved remediation plan using the RBAC permissions granted by the
	// operator. When skipped, the proposal enters AwaitingSync for manual
	// or GitOps-driven application.
	// +kubebuilder:validation:Required
	Execution WorkflowStep `json:"execution"`

	// verification defines the verification step. The verification agent
	// checks whether the remediation was successful by running the
	// verification plan produced during analysis. When skipped, the proposal
	// completes immediately after execution.
	// +kubebuilder:validation:Required
	Verification WorkflowStep `json:"verification"`
}

// +kubebuilder:object:root=true
// +kubebuilder:resource:scope=Cluster
// +kubebuilder:printcolumn:name="Analysis Agent",type=string,JSONPath=`.spec.analysis.agentRef.name`
// +kubebuilder:printcolumn:name="Exec Skip",type=boolean,JSONPath=`.spec.execution.skip`
// +kubebuilder:printcolumn:name="Verify Skip",type=boolean,JSONPath=`.spec.verification.skip`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// Workflow defines a reusable 3-step pipeline template that controls which
// agents handle analysis, execution, and verification, and whether any steps
// are skipped. It is the third link in the CRD chain (LlmProvider -> Agent ->
// Workflow -> Proposal) and is referenced by Proposal resources via
// spec.workflowRef.
//
// Workflow is cluster-scoped. You create workflows representing different
// operational patterns and then reference them from proposals. Per-proposal
// overrides (WorkflowOverride in the Proposal spec) allow customizing
// individual steps without creating a new Workflow.
//
// Example — full remediation (analyze, execute, verify):
//
//	apiVersion: agentic.openshift.io/v1alpha1
//	kind: Workflow
//	metadata:
//	  name: remediation
//	spec:
//	  analysis:
//	    agentRef:
//	      name: analyzer
//	  execution:
//	    agentRef:
//	      name: executor
//	  verification:
//	    agentRef:
//	      name: verifier
//
// Example — advisory-only (analyze only, no execution or verification):
//
//	apiVersion: agentic.openshift.io/v1alpha1
//	kind: Workflow
//	metadata:
//	  name: advisory-only
//	spec:
//	  analysis:
//	    agentRef:
//	      name: analyzer
//	  execution:
//	    skip: true
//	  verification:
//	    skip: true
//
// Example — gitops-remediation (analyze, skip execution, verify after user applies via git):
//
//	apiVersion: agentic.openshift.io/v1alpha1
//	kind: Workflow
//	metadata:
//	  name: gitops-remediation
//	spec:
//	  analysis:
//	    agentRef:
//	      name: analyzer
//	  execution:
//	    skip: true
//	  verification:
//	    agentRef:
//	      name: verifier
//
// Example — trust-mode (analyze, execute, skip verification):
//
//	apiVersion: agentic.openshift.io/v1alpha1
//	kind: Workflow
//	metadata:
//	  name: trust-mode
//	spec:
//	  analysis:
//	    agentRef:
//	      name: analyzer
//	  execution:
//	    agentRef:
//	      name: executor
//	  verification:
//	    skip: true
type Workflow struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	// +kubebuilder:validation:Required
	// +kubebuilder:validation:XValidation:rule="self.analysis.skip || (has(self.analysis.agentRef) && self.analysis.agentRef.name != '')",message="agentRef is required when analysis is not skipped"
	// +kubebuilder:validation:XValidation:rule="self.execution.skip || (has(self.execution.agentRef) && self.execution.agentRef.name != '')",message="agentRef is required when execution is not skipped"
	// +kubebuilder:validation:XValidation:rule="self.verification.skip || (has(self.verification.agentRef) && self.verification.agentRef.name != '')",message="agentRef is required when verification is not skipped"
	Spec WorkflowSpec `json:"spec"`
}

// +kubebuilder:object:root=true

// WorkflowList contains a list of Workflow.
type WorkflowList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Workflow `json:"items"`
}

func init() {
	SchemeBuilder.Register(&Workflow{}, &WorkflowList{})
}

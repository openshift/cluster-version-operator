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

// ApprovalDecision indicates whether a stage is approved or denied.
// +kubebuilder:validation:Enum=Approved;Denied
type ApprovalDecision string

const (
	ApprovalDecisionApproved ApprovalDecision = "Approved"
	ApprovalDecisionDenied   ApprovalDecision = "Denied"
)

// ApprovalStageType identifies which workflow step an approval entry applies to.
// +kubebuilder:validation:Enum=Analysis;Execution;Verification;Escalation
type ApprovalStageType string

const (
	ApprovalStageAnalysis     ApprovalStageType = "Analysis"
	ApprovalStageExecution    ApprovalStageType = "Execution"
	ApprovalStageVerification ApprovalStageType = "Verification"
	ApprovalStageEscalation   ApprovalStageType = "Escalation"
)

// AnalysisApproval contains approval parameters for the analysis step.
//
// +kubebuilder:validation:MinProperties=1
type AnalysisApproval struct {
	// agent is the Agent CR for this step. Defaults to "default".
	// +optional
	// +default="default"
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=253
	// +kubebuilder:validation:XValidation:rule="!format.dns1123Subdomain().validate(self).hasValue()",message="must be a valid DNS subdomain: lowercase alphanumeric characters, hyphens, and dots"
	Agent string `json:"agent,omitempty"`
}

// ExecutionApproval contains approval parameters for the execution step.
//
// +kubebuilder:validation:MinProperties=1
type ExecutionApproval struct {
	// agent is the Agent CR for this step. Defaults to "default".
	// +optional
	// +default="default"
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=253
	// +kubebuilder:validation:XValidation:rule="!format.dns1123Subdomain().validate(self).hasValue()",message="must be a valid DNS subdomain: lowercase alphanumeric characters, hyphens, and dots"
	Agent string `json:"agent,omitempty"`

	// option is the 0-based index into the analysis options array
	// selecting which remediation approach to execute.
	// +optional
	// +kubebuilder:validation:Minimum=0
	Option *int32 `json:"option,omitempty"`

	// maxAttempts is the number of execution retry attempts approved
	// for this proposal. Must not exceed ApprovalPolicy.spec.maxAttempts.
	// Defaults to 1 if unset.
	// +optional
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=3
	MaxAttempts int32 `json:"maxAttempts,omitempty"`
}

// VerificationApproval contains approval parameters for the verification step.
//
// +kubebuilder:validation:MinProperties=1
type VerificationApproval struct {
	// agent is the Agent CR for this step. Defaults to "default".
	// +optional
	// +default="default"
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=253
	// +kubebuilder:validation:XValidation:rule="!format.dns1123Subdomain().validate(self).hasValue()",message="must be a valid DNS subdomain: lowercase alphanumeric characters, hyphens, and dots"
	Agent string `json:"agent,omitempty"`
}

// EscalationApproval contains approval parameters for the escalation step.
//
// +kubebuilder:validation:MinProperties=1
type EscalationApproval struct {
	// agent is the Agent CR for this step. Defaults to "default".
	// +optional
	// +default="default"
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=253
	// +kubebuilder:validation:XValidation:rule="!format.dns1123Subdomain().validate(self).hasValue()",message="must be a valid DNS subdomain: lowercase alphanumeric characters, hyphens, and dots"
	Agent string `json:"agent,omitempty"`
}

// ApprovalStage is a discriminated union representing approval for one
// workflow step. Presence in spec.stages indicates approval; absence means
// not yet approved (controller checks ApprovalPolicy for auto-approve).
//
// +kubebuilder:validation:XValidation:rule="self.type == 'Analysis' ? has(self.analysis) : !has(self.analysis)",message="analysis is required when type is Analysis, and forbidden otherwise"
// +kubebuilder:validation:XValidation:rule="self.type == 'Execution' ? has(self.execution) : !has(self.execution)",message="execution is required when type is Execution, and forbidden otherwise"
// +kubebuilder:validation:XValidation:rule="self.type == 'Verification' ? has(self.verification) : !has(self.verification)",message="verification is required when type is Verification, and forbidden otherwise"
// +kubebuilder:validation:XValidation:rule="self.type == 'Escalation' ? has(self.escalation) : !has(self.escalation)",message="escalation is required when type is Escalation, and forbidden otherwise"
type ApprovalStage struct {
	// type identifies which workflow step this approval is for.
	// +required
	Type ApprovalStageType `json:"type,omitempty"`

	// decision indicates whether this stage is approved or denied.
	// Denying any stage terminates the entire proposal, even if
	// earlier stages were already approved. Once set to Denied,
	// it cannot be changed.
	// +optional
	Decision ApprovalDecision `json:"decision,omitempty"`

	// analysis contains approval parameters for the analysis step.
	// Required when type is Analysis.
	// +optional
	Analysis AnalysisApproval `json:"analysis,omitzero"`

	// execution contains approval parameters for the execution step.
	// Required when type is Execution.
	// +optional
	Execution ExecutionApproval `json:"execution,omitzero"`

	// verification contains approval parameters for the verification step.
	// Required when type is Verification.
	// +optional
	Verification VerificationApproval `json:"verification,omitzero"`

	// escalation contains approval parameters for the escalation step.
	// Required when type is Escalation.
	// +optional
	Escalation EscalationApproval `json:"escalation,omitzero"`
}

// ProposalApprovalSpec defines the desired state of ProposalApproval.
//
// spec.stages is append-only: once a stage is added, it cannot be removed.
// Decisions once set cannot be changed. maxAttempts once set cannot be reduced.
//
// +kubebuilder:validation:XValidation:rule="oldSelf.stages.all(old, self.stages.exists(s, s.type == old.type))",message="stages are append-only: existing stages cannot be removed"
// +kubebuilder:validation:XValidation:rule="oldSelf.stages.all(old, !(has(old.decision) && old.decision == 'Denied') || self.stages.exists(s, s.type == old.type && has(s.decision) && s.decision == 'Denied'))",message="decisions once set cannot be changed"
// +kubebuilder:validation:XValidation:rule="oldSelf.stages.all(old, old.type != 'Execution' || !has(old.execution) || !has(old.execution.maxAttempts) || old.execution.maxAttempts == 0 || self.stages.exists(s, s.type == 'Execution' && has(s.execution) && has(s.execution.maxAttempts) && s.execution.maxAttempts == old.execution.maxAttempts))",message="maxAttempts once set cannot be changed"
// +kubebuilder:validation:MinProperties=1
type ProposalApprovalSpec struct {
	// stages lists the approved (or denied) workflow steps. Each entry is
	// a discriminated union keyed by type. Users add stages one at a time
	// via patch as they approve each step.
	// +optional
	// +listType=map
	// +listMapKey=type
	// +kubebuilder:validation:MinItems=1
	// +kubebuilder:validation:MaxItems=4
	Stages []ApprovalStage `json:"stages,omitempty"`
}

// ApprovalStageStatus is the observed state of a single approval stage.
type ApprovalStageStatus struct {
	// conditions for this approval stage.
	// +listType=map
	// +listMapKey=type
	// +patchStrategy=merge
	// +patchMergeKey=type
	// +optional
	// +kubebuilder:validation:MinItems=1
	// +kubebuilder:validation:MaxItems=8
	Conditions []metav1.Condition `json:"conditions,omitempty" patchStrategy:"merge" patchMergeKey:"type" protobuf:"bytes,1,rep,name=conditions"`

	// name identifies the workflow step.
	// +required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=253
	Name string `json:"name,omitempty"`
}

// ProposalApprovalStatus defines the observed state of ProposalApproval.
//
// +kubebuilder:validation:MinProperties=1
type ProposalApprovalStatus struct {
	// stages contains the per-stage approval status set by the controller.
	// +optional
	// +listType=atomic
	// +kubebuilder:validation:MinItems=1
	// +kubebuilder:validation:MaxItems=4
	Stages []ApprovalStageStatus `json:"stages,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Namespaced
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// ProposalApproval tracks per-step approval state for a Proposal. The
// operator creates it when a Proposal is created. Users update it to
// approve or deny individual workflow steps.
//
// ProposalApproval has a 1:1 relationship with its Proposal (same name,
// same namespace) and is owned by the Proposal via an owner reference
// for garbage collection.
//
// Example:
//
//	apiVersion: agentic.openshift.io/v1alpha1
//	kind: ProposalApproval
//	metadata:
//	  name: fix-crash
//	  namespace: my-namespace
//	  ownerReferences:
//	    - apiVersion: agentic.openshift.io/v1alpha1
//	      kind: Proposal
//	      name: fix-crash
//	spec:
//	  stages:
//	    - type: Analysis
//	      analysis: {}
//	    - type: Execution
//	      execution:
//	        option: 0
//	        agent: fast
type ProposalApproval struct {
	metav1.TypeMeta `json:",inline"`

	// metadata is the standard object metadata.
	// +optional
	metav1.ObjectMeta `json:"metadata,omitempty"`

	// spec defines the desired approval state.
	// +optional
	Spec ProposalApprovalSpec `json:"spec,omitzero"`

	// status defines the observed approval state.
	// +optional
	Status ProposalApprovalStatus `json:"status,omitzero"`
}

// +kubebuilder:object:root=true

// ProposalApprovalList contains a list of ProposalApproval.
type ProposalApprovalList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []ProposalApproval `json:"items"`
}

func init() {
	SchemeBuilder.Register(&ProposalApproval{}, &ProposalApprovalList{})
}

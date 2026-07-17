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

// EscalationResultStatus is the status of an EscalationResult.
//
// +kubebuilder:validation:MinProperties=1
type EscalationResultStatus struct {
	// conditions track the lifecycle of this result.
	// +listType=map
	// +listMapKey=type
	// +patchStrategy=merge
	// +patchMergeKey=type
	// +optional
	// +kubebuilder:validation:MinItems=1
	// +kubebuilder:validation:MaxItems=8
	Conditions []metav1.Condition `json:"conditions,omitempty" patchStrategy:"merge" patchMergeKey:"type" protobuf:"bytes,1,rep,name=conditions"`

	// summary is a Markdown-formatted escalation summary.
	// +optional
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=32768
	Summary string `json:"summary,omitempty"`

	// content is freeform escalation content produced by the agent.
	// +optional
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=65536
	Content string `json:"content,omitempty"`

	// sandbox tracks the sandbox pod used for this escalation.
	// +optional
	Sandbox SandboxInfo `json:"sandbox,omitzero"`

	// failureReason is populated when the step failed due to a system error.
	// +optional
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=8192
	FailureReason string `json:"failureReason,omitempty"`
}

// EscalationResultSpec contains the immutable identity fields for an EscalationResult.
type EscalationResultSpec struct {
	// proposalName is the name of the parent Proposal in the same namespace.
	// +required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=253
	ProposalName string `json:"proposalName,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:resource:scope=Namespaced
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Proposal",type=string,JSONPath=`.spec.proposalName`
// +kubebuilder:printcolumn:name="Outcome",type=string,JSONPath=`.status.conditions[?(@.type=="Completed")].reason`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// EscalationResult records the output of the escalation step. Created by
// the operator after the escalation agent completes. Owned by the parent
// Proposal for garbage collection.
type EscalationResult struct {
	metav1.TypeMeta `json:",inline"`

	// metadata is the standard object metadata.
	// +optional
	metav1.ObjectMeta `json:"metadata,omitempty"`

	// spec contains the immutable identity fields for this result.
	// +required
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="spec is immutable"
	Spec EscalationResultSpec `json:"spec,omitzero"`

	// status contains result data and conditions.
	// +optional
	Status EscalationResultStatus `json:"status,omitzero"`
}

// +kubebuilder:object:root=true

// EscalationResultList contains a list of EscalationResult.
type EscalationResultList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []EscalationResult `json:"items"`
}

func init() {
	SchemeBuilder.Register(&EscalationResult{}, &EscalationResultList{})
}

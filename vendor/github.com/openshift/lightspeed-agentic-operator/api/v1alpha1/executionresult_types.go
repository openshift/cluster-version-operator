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

// ExecutionResultStatus is the status of an ExecutionResult.
//
// +kubebuilder:validation:MinProperties=1
type ExecutionResultStatus struct {
	// conditions track the lifecycle of this result.
	// +listType=map
	// +listMapKey=type
	// +patchStrategy=merge
	// +patchMergeKey=type
	// +optional
	// +kubebuilder:validation:MinItems=1
	// +kubebuilder:validation:MaxItems=8
	Conditions []metav1.Condition `json:"conditions,omitempty" patchStrategy:"merge" patchMergeKey:"type" protobuf:"bytes,1,rep,name=conditions"`

	// actionsTaken lists what the agent did.
	// +optional
	// +listType=atomic
	// +kubebuilder:validation:MinItems=1
	// +kubebuilder:validation:MaxItems=100
	ActionsTaken []ExecutionAction `json:"actionsTaken,omitempty"`

	// verification is the lightweight inline verification the execution
	// agent performs immediately after completing its actions.
	// +optional
	Verification ExecutionVerification `json:"verification,omitzero"`

	// sandbox tracks the sandbox pod used for this execution.
	// +optional
	Sandbox SandboxInfo `json:"sandbox,omitzero"`

	// failureReason is populated when the step failed due to a system error.
	// +optional
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=8192
	FailureReason string `json:"failureReason,omitempty"`
}

// ExecutionResultSpec contains the immutable identity fields for an ExecutionResult.
type ExecutionResultSpec struct {
	// proposalName is the name of the parent Proposal in the same namespace.
	// +required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=253
	ProposalName string `json:"proposalName,omitempty"`

	// retryIndex is the 0-based retry index within the current analysis.
	// First execution has retryIndex 0, first retry has retryIndex 1, etc.
	// +required
	// +kubebuilder:validation:Minimum=0
	// +kubebuilder:validation:Maximum=2
	RetryIndex *int32 `json:"retryIndex,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:resource:scope=Namespaced
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Proposal",type=string,JSONPath=`.spec.proposalName`
// +kubebuilder:printcolumn:name="Retry",type=integer,JSONPath=`.spec.retryIndex`
// +kubebuilder:printcolumn:name="Outcome",type=string,JSONPath=`.status.conditions[?(@.type=="Completed")].reason`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// ExecutionResult records the output of a single execution step execution.
// Created by the operator after the execution agent completes. Owned by
// the parent Proposal for garbage collection.
type ExecutionResult struct {
	metav1.TypeMeta `json:",inline"`

	// metadata is the standard object metadata.
	// +optional
	metav1.ObjectMeta `json:"metadata,omitempty"`

	// spec contains the immutable identity fields for this result.
	// +required
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="spec is immutable"
	Spec ExecutionResultSpec `json:"spec,omitzero"`

	// status contains result data and conditions.
	// +optional
	Status ExecutionResultStatus `json:"status,omitzero"`
}

// +kubebuilder:object:root=true

// ExecutionResultList contains a list of ExecutionResult.
type ExecutionResultList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []ExecutionResult `json:"items"`
}

func init() {
	SchemeBuilder.Register(&ExecutionResult{}, &ExecutionResultList{})
}

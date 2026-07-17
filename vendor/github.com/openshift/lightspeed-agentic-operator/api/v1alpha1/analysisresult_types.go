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

// AnalysisResultStatus is the status of an AnalysisResult.
//
// +kubebuilder:validation:MinProperties=1
type AnalysisResultStatus struct {
	// conditions track the lifecycle of this result.
	// +listType=map
	// +listMapKey=type
	// +patchStrategy=merge
	// +patchMergeKey=type
	// +optional
	// +kubebuilder:validation:MinItems=1
	// +kubebuilder:validation:MaxItems=8
	Conditions []metav1.Condition `json:"conditions,omitempty" patchStrategy:"merge" patchMergeKey:"type" protobuf:"bytes,1,rep,name=conditions"`

	// options contains the remediation options returned by the analysis agent.
	// +optional
	// +listType=atomic
	// +kubebuilder:validation:MinItems=1
	// +kubebuilder:validation:MaxItems=10
	Options []RemediationOption `json:"options,omitempty"`

	// sandbox tracks the sandbox pod used for this analysis.
	// +optional
	Sandbox SandboxInfo `json:"sandbox,omitzero"`

	// failureReason is populated when the step failed due to a system error.
	// +optional
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=8192
	FailureReason string `json:"failureReason,omitempty"`
}

// AnalysisResultSpec contains the immutable identity fields for an AnalysisResult.
type AnalysisResultSpec struct {
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

// AnalysisResult records the output of a single analysis step execution.
// Created by the operator after the analysis agent completes. Owned by
// the parent Proposal for garbage collection.
type AnalysisResult struct {
	metav1.TypeMeta `json:",inline"`

	// metadata is the standard object metadata.
	// +optional
	metav1.ObjectMeta `json:"metadata,omitempty"`

	// spec contains the immutable identity fields for this result.
	// +required
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="spec is immutable"
	Spec AnalysisResultSpec `json:"spec,omitzero"`

	// status contains result data and conditions.
	// +optional
	Status AnalysisResultStatus `json:"status,omitzero"`
}

// +kubebuilder:object:root=true

// AnalysisResultList contains a list of AnalysisResult.
type AnalysisResultList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []AnalysisResult `json:"items"`
}

func init() {
	SchemeBuilder.Register(&AnalysisResult{}, &AnalysisResultList{})
}

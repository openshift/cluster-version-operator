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

// ApprovalMode controls whether a step requires explicit user approval.
// +kubebuilder:validation:Enum=Automatic;Manual
type ApprovalMode string

const (
	ApprovalModeAutomatic ApprovalMode = "Automatic"
	ApprovalModeManual    ApprovalMode = "Manual"
)

const DefaultMaxConcurrentProposals int32 = 5

// ApprovalPolicyStage configures the approval mode for a single workflow step.
type ApprovalPolicyStage struct {
	// name is the workflow step this policy applies to.
	// Allowed values: Analysis, Execution, Verification, Escalation.
	// +required
	Name SandboxStep `json:"name,omitempty"`

	// approval controls whether this step auto-approves or requires
	// explicit user approval on the ProposalApproval resource.
	// Allowed values: Automatic (step runs without user approval),
	// Manual (step waits for explicit approval on ProposalApproval).
	// +required
	Approval ApprovalMode `json:"approval,omitempty"`
}

// ApprovalPolicySpec defines the desired state of ApprovalPolicy.
//
// +kubebuilder:validation:MinProperties=1
type ApprovalPolicySpec struct {
	// stages configures the approval mode for each workflow step.
	// Omitted steps default to Manual.
	// +optional
	// +listType=map
	// +listMapKey=name
	// +kubebuilder:validation:MinItems=1
	// +kubebuilder:validation:MaxItems=4
	Stages []ApprovalPolicyStage `json:"stages,omitempty"`

	// maxAttempts sets the maximum number of execution retry attempts
	// allowed for proposals. When verification fails, the operator retries
	// execution up to this limit before escalating. Defaults to 1 if omitted.
	// +optional
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=3
	MaxAttempts int32 `json:"maxAttempts,omitempty"`

	// maxConcurrentProposals sets the maximum number of proposals the
	// operator reconciles concurrently. Higher values allow more proposals
	// to run in parallel but consume more cluster resources.
	// Defaults to 5 if omitted.
	// +optional
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=20
	// +default=5
	MaxConcurrentProposals int32 `json:"maxConcurrentProposals,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:resource:scope=Cluster
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`
// +kubebuilder:validation:XValidation:rule="self.metadata.name == 'cluster'",message="ApprovalPolicy must be named 'cluster' (singleton)"

// ApprovalPolicy is a cluster-scoped singleton that configures default
// approval behavior for proposal workflow steps. The cluster admin creates
// a single ApprovalPolicy named "cluster" to control which steps auto-approve.
//
// Steps not listed in the policy default to Manual (require explicit
// user approval on the ProposalApproval resource).
//
// Example:
//
//	apiVersion: agentic.openshift.io/v1alpha1
//	kind: ApprovalPolicy
//	metadata:
//	  name: cluster
//	spec:
//	  stages:
//	    - name: Analysis
//	      approval: Automatic
//	    - name: Execution
//	      approval: Manual
//	    - name: Verification
//	      approval: Automatic
type ApprovalPolicy struct {
	metav1.TypeMeta `json:",inline"`

	// metadata is the standard object metadata.
	// +optional
	metav1.ObjectMeta `json:"metadata,omitempty"`

	// spec defines the desired approval policy.
	// +required
	Spec ApprovalPolicySpec `json:"spec,omitzero"`
}

// +kubebuilder:object:root=true

// ApprovalPolicyList contains a list of ApprovalPolicy.
type ApprovalPolicyList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []ApprovalPolicy `json:"items"`
}

func init() {
	SchemeBuilder.Register(&ApprovalPolicy{}, &ApprovalPolicyList{})
}

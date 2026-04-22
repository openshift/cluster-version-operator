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

// OutputFieldType is the JSON type of an output field in the agent's
// structured output schema. The operator merges these fields into the
// base output schema that every agent produces (diagnosis, proposal,
// verification plan, RBAC request), allowing adapters to request
// domain-specific structured data from the agent.
// +kubebuilder:validation:Enum=string;number;boolean;array;object
type OutputFieldType string

const (
	OutputFieldTypeString  OutputFieldType = "string"
	OutputFieldTypeNumber  OutputFieldType = "number"
	OutputFieldTypeBoolean OutputFieldType = "boolean"
	OutputFieldTypeArray   OutputFieldType = "array"
	OutputFieldTypeObject  OutputFieldType = "object"
)

// OutputField defines a top-level field in the agent's structured output.
// These fields are merged into the base output schema that the operator sends
// to the agent. Use outputFields to request adapter-specific structured data
// (e.g., an ACS adapter might add a "violationId" string field).
//
// Supports up to two levels of nesting: top-level fields can contain object
// properties or array items, and those can contain one more level of nesting.
//
// Example — adding an ACS violation ID and affected images to the output:
//
//	outputFields:
//	  - name: violationId
//	    type: string
//	    description: "The ACS violation ID that triggered this proposal"
//	    required: true
//	  - name: affectedImages
//	    type: array
//	    description: "Container images flagged by the violation"
//	    items:
//	      type: string
//
// +kubebuilder:validation:XValidation:rule="self.type == 'array' ? has(self.items) : true",message="items is required when type is array"
// +kubebuilder:validation:XValidation:rule="self.type == 'object' ? has(self.properties) : true",message="properties is required when type is object"
// +kubebuilder:validation:XValidation:rule="has(self.enum) ? self.type == 'string' : true",message="enum is only valid for string fields"
type OutputField struct {
	// name is the field name in the output JSON.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:Pattern="^[a-zA-Z][a-zA-Z0-9_]*$"
	Name string `json:"name"`

	// type is the JSON type of this field.
	// +kubebuilder:validation:Required
	Type OutputFieldType `json:"type"`

	// description explains the purpose of this field (passed to the LLM).
	// +optional
	Description string `json:"description,omitempty"`

	// required indicates whether the agent must populate this field.
	// +optional
	Required bool `json:"required,omitempty"`

	// enum constrains string fields to a set of allowed values.
	// +optional
	Enum []string `json:"enum,omitempty"`

	// items defines the element schema when type is array.
	// +optional
	Items *OutputFieldItems `json:"items,omitempty"`

	// properties defines nested fields when type is object.
	// +optional
	// +listType=map
	// +listMapKey=name
	Properties []OutputSubField `json:"properties,omitempty"`
}

// OutputSubField defines a nested field (one level deep) within an OutputField
// of type "object" or within array items of type "object". At this depth,
// array items are restricted to primitive types (string, number, boolean).
// +kubebuilder:validation:XValidation:rule="self.type == 'array' ? has(self.items) : true",message="items is required when type is array"
// +kubebuilder:validation:XValidation:rule="has(self.enum) ? self.type == 'string' : true",message="enum is only valid for string fields"
type OutputSubField struct {
	// name is the field name in the output JSON.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:Pattern="^[a-zA-Z][a-zA-Z0-9_]*$"
	Name string `json:"name"`

	// type is the JSON type of this field.
	// +kubebuilder:validation:Required
	Type OutputFieldType `json:"type"`

	// description explains the purpose of this field (passed to the LLM).
	// +optional
	Description string `json:"description,omitempty"`

	// required indicates whether the agent must populate this field.
	// +optional
	Required bool `json:"required,omitempty"`

	// enum constrains string fields to a set of allowed values.
	// +optional
	Enum []string `json:"enum,omitempty"`

	// items defines the element schema when type is array (primitive types only at this depth).
	// +optional
	Items *OutputSubFieldItems `json:"items,omitempty"`
}

// OutputFieldItems defines the schema for array elements at the top level.
// Supports primitive types and objects (with nested OutputSubField properties).
type OutputFieldItems struct {
	// type is the JSON type of array elements.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Enum=string;number;boolean;object
	Type OutputFieldType `json:"type"`

	// properties defines fields for object-typed array elements.
	// +optional
	// +listType=map
	// +listMapKey=name
	Properties []OutputSubField `json:"properties,omitempty"`
}

// OutputSubFieldItems defines the schema for array elements at the nested
// level. Only primitive types (string, number, boolean) are allowed here
// to prevent unbounded schema depth.
type OutputSubFieldItems struct {
	// type is the JSON type of array elements.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Enum=string;number;boolean
	Type OutputFieldType `json:"type"`
}

// NOTE: MCPHeaderSourceType, MCPHeaderValueSource, MCPHeader, and MCPServerConfig
// are defined in olsconfig_types.go and shared by both OLSConfig and Agent.

// SkillsSource defines an OCI image containing skills and optionally which
// paths within that image to mount. Skills are mounted as Kubernetes image
// volumes in the agent's sandbox pod.
//
// When paths is omitted, the entire image is mounted. When paths is specified,
// only those directories are mounted (each as a separate subPath volumeMount),
// allowing selective composition of skills from large shared images.
//
// Example — mount all skills from a custom image:
//
//	skills:
//	  - image: quay.io/my-org/my-skills:latest
//
// Example — selectively mount two skills from a shared image:
//
//	skills:
//	  - image: registry.ci.openshift.org/ocp/5.0:agentic-skills
//	    paths:
//	      - /skills/prometheus
//	      - /skills/cluster-update/update-advisor
type SkillsSource struct {
	// image is the OCI image reference containing skills.
	// The operator mounts this as a Kubernetes image volume (requires K8s 1.34+).
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	Image string `json:"image"`

	// paths restricts which directories from the image are mounted.
	// Each path is mounted as a separate subPath volumeMount into the agent's
	// skills directory. The last segment of each path becomes the mount name
	// (e.g., "/skills/prometheus" mounts as "prometheus").
	//
	// When omitted, the entire image is mounted as a single volume.
	// +optional
	Paths []string `json:"paths,omitempty"`
}

// AgentSpec defines the desired state of Agent.
type AgentSpec struct {
	// llmRef references a cluster-scoped LlmProvider CR that supplies the
	// LLM backend for this agent. The operator resolves this reference at
	// reconcile time and configures the sandbox pod with the provider's
	// credentials and model.
	// +kubebuilder:validation:Required
	LLMRef corev1.LocalObjectReference `json:"llmRef"`

	// skills defines one or more OCI images containing skills to mount
	// in the agent's sandbox pod. Each entry specifies an image and optionally
	// which paths within that image to mount. The operator creates Kubernetes
	// image volumes (requires K8s 1.34+) and mounts them into the agent's
	// skills directory.
	//
	// Multiple entries allow composing skills from different images:
	//
	//   skills:
	//     - image: registry.ci.openshift.org/ocp/5.0:agentic-skills
	//       paths:
	//         - /skills/prometheus
	//         - /skills/cluster-update/update-advisor
	//     - image: quay.io/my-org/custom-skills:latest
	//
	// +kubebuilder:validation:MinItems=1
	Skills []SkillsSource `json:"skills"`

	// mcpServers defines external MCP (Model Context Protocol) servers the
	// agent can connect to for additional tools and context beyond its
	// built-in skills. Each server is identified by name and URL.
	// +optional
	// +listType=map
	// +listMapKey=name
	MCPServers []MCPServerConfig `json:"mcpServers,omitempty"`

	// systemPromptRef references a ConfigMap containing the system prompt.
	// The ConfigMap must have a key named "prompt" with the prompt text.
	// The system prompt shapes the agent's behavior for its role (analysis,
	// execution, or verification). When omitted, the agent uses a default
	// prompt appropriate for its workflow step.
	// +optional
	SystemPromptRef *corev1.LocalObjectReference `json:"systemPromptRef,omitempty"`

	// outputFields defines additional structured output fields beyond the
	// base schema that every agent produces (diagnosis, proposal, RBAC,
	// verification plan). Use this to request domain-specific structured
	// data from the agent (e.g., an ACS violation ID, affected images).
	// Mutually exclusive with rawOutputSchema.
	// +optional
	// +listType=map
	// +listMapKey=name
	OutputFields []OutputField `json:"outputFields,omitempty"`

	// rawOutputSchema is an escape hatch that replaces the entire output
	// schema with a raw JSON Schema object. Use this when outputFields
	// cannot express the schema you need (e.g., deeply nested structures,
	// conditional fields). Mutually exclusive with outputFields.
	// +optional
	RawOutputSchema *apiextensionsv1.JSON `json:"rawOutputSchema,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:resource:scope=Cluster
// +kubebuilder:printcolumn:name="LLM",type=string,JSONPath=`.spec.llmRef.name`
// +kubebuilder:printcolumn:name="Skills Image",type=string,JSONPath=`.spec.skills[0].image`,priority=1
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// Agent defines a complete agent configuration: which LLM to use, what
// skills to mount, optional MCP servers, and what system prompt to follow.
// It is the second link in the CRD chain (LlmProvider -> Agent -> Workflow
// -> Proposal) and is referenced by Workflow steps via agentRef.
//
// Agent is cluster-scoped. You typically create a few agents with different
// capabilities and assign them to workflow steps. For example, an analysis
// agent might use a capable model with broad diagnostic skills, while an
// execution agent uses a fast model with targeted remediation skills.
//
// Example — an analysis agent with selective skills and a system prompt:
//
//	apiVersion: agentic.openshift.io/v1alpha1
//	kind: Agent
//	metadata:
//	  name: analyzer
//	spec:
//	  llmRef:
//	    name: smart
//	  skills:
//	    - image: registry.ci.openshift.org/ocp/5.0:agentic-skills
//	      paths:
//	        - /skills/prometheus
//	        - /skills/cluster-ops
//	        - /skills/rbac-security
//	  systemPromptRef:
//	    name: analysis-prompt
//
// Example — an execution agent with a fast model:
//
//	apiVersion: agentic.openshift.io/v1alpha1
//	kind: Agent
//	metadata:
//	  name: executor
//	spec:
//	  llmRef:
//	    name: fast
//	  skills:
//	    - image: registry.ci.openshift.org/ocp/5.0:agentic-skills
//	      paths:
//	        - /skills/cluster-ops
//	  systemPromptRef:
//	    name: execution-prompt
//
// Example — an agent with MCP servers for extended tooling:
//
//	apiVersion: agentic.openshift.io/v1alpha1
//	kind: Agent
//	metadata:
//	  name: analyzer-with-mcp
//	spec:
//	  llmRef:
//	    name: smart
//	  skills:
//	    - image: registry.ci.openshift.org/ocp/5.0:agentic-skills
//	  mcpServers:
//	    - name: openshift
//	      url: https://mcp.openshift-lightspeed.svc:8443/sse
//	      timeout: 10
//	      headers:
//	        - name: Authorization
//	          valueFrom:
//	            type: kubernetes
//	    - name: pagerduty
//	      url: https://mcp-pagerduty.example.com/sse
//	      headers:
//	        - name: X-API-Key
//	          valueFrom:
//	            type: secret
//	            secretRef:
//	              name: pagerduty-api-key
//	  systemPromptRef:
//	    name: analysis-prompt
type Agent struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	// +kubebuilder:validation:Required
	Spec AgentSpec `json:"spec"`
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

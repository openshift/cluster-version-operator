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

// LLMProviderType identifies the hosting backend for an LLM provider.
//
// Each backend has different authentication requirements and API endpoints:
//   - "anthropic"    — Direct Anthropic API. Secret needs ANTHROPIC_API_KEY.
//   - "vertex"       — Google Cloud Vertex AI. Secret needs service account JSON
//     (GOOGLE_APPLICATION_CREDENTIALS) plus GCP_PROJECT and GCP_REGION.
//   - "openai"       — OpenAI-compatible API. Secret needs OPENAI_API_KEY.
//   - "azure_openai" — Azure OpenAI Service. Secret needs AZURE_OPENAI_API_KEY,
//     AZURE_OPENAI_ENDPOINT, and optionally AZURE_OPENAI_API_VERSION.
//   - "bedrock"      — AWS Bedrock. Secret needs AWS_ACCESS_KEY_ID,
//     AWS_SECRET_ACCESS_KEY, and AWS_REGION.
type LLMProviderType string

const (
	// LLMProviderAnthropic uses the Anthropic API directly.
	LLMProviderAnthropic LLMProviderType = "anthropic"
	// LLMProviderVertex uses Google Cloud Vertex AI as the LLM backend.
	LLMProviderVertex LLMProviderType = "vertex"
	// LLMProviderOpenAI uses an OpenAI-compatible API endpoint.
	LLMProviderOpenAI LLMProviderType = "openai"
	// LLMProviderAzureOpenAI uses the Azure OpenAI Service.
	LLMProviderAzureOpenAI LLMProviderType = "azure_openai"
	// LLMProviderBedrock uses AWS Bedrock.
	LLMProviderBedrock LLMProviderType = "bedrock"
)

// LlmProviderSpec defines the desired state of LlmProvider.
type LlmProviderSpec struct {
	// type is the LLM provider backend (e.g., "vertex", "anthropic", "bedrock").
	// See LLMProviderType for the authentication requirements of each backend.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Enum=anthropic;vertex;openai;azure_openai;bedrock
	Type LLMProviderType `json:"type"`

	// credentialsSecretRef references a Secret in the operator's namespace
	// containing the provider credentials. The required keys depend on the
	// provider type (see LLMProviderType for details). The operator reads this
	// secret and injects the credentials into agent sandbox pods at runtime.
	// +kubebuilder:validation:Required
	CredentialsSecretRef corev1.LocalObjectReference `json:"credentialsSecretRef"`

	// model is the LLM model identifier as recognized by the provider
	// (e.g., "claude-opus-4-6", "claude-haiku-4-5", "gpt-4o").
	// Different agents can reference different LlmProviders to use different
	// models for different tasks (e.g., a capable model for analysis,
	// a fast model for execution).
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	Model string `json:"model"`

	// url is an optional override for the provider API endpoint.
	// Most providers have well-known endpoints that the operator resolves
	// automatically, so this is only needed for custom deployments or proxies.
	// +optional
	// +kubebuilder:validation:Pattern=`^https?://.*$`
	URL string `json:"url,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:resource:scope=Cluster
// +kubebuilder:printcolumn:name="Type",type=string,JSONPath=`.spec.type`
// +kubebuilder:printcolumn:name="Model",type=string,JSONPath=`.spec.model`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// LlmProvider defines an LLM provider configuration. It is the first link in
// the CRD chain (LlmProvider -> Agent -> Workflow -> Proposal) and is
// referenced by Agent resources via spec.llmRef.
//
// LlmProvider is cluster-scoped, meaning a single provider can be shared by
// agents across all namespaces. The operator uses the credentials and model
// to configure the LLM client inside agent sandbox pods.
//
// Typically you create a small number of providers representing different
// capability/cost tiers (e.g., "smart" for complex analysis, "fast" for
// routine execution) and then reference them from multiple Agent resources.
//
// Example — a high-capability provider for analysis tasks:
//
//	apiVersion: agentic.openshift.io/v1alpha1
//	kind: LlmProvider
//	metadata:
//	  name: smart
//	spec:
//	  type: vertex
//	  model: claude-opus-4-6
//	  credentialsSecretRef:
//	    name: llm-credentials
//
// Example — a fast, cost-efficient provider for execution tasks:
//
//	apiVersion: agentic.openshift.io/v1alpha1
//	kind: LlmProvider
//	metadata:
//	  name: fast
//	spec:
//	  type: vertex
//	  model: claude-haiku-4-5
//	  credentialsSecretRef:
//	    name: llm-credentials
type LlmProvider struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	// +kubebuilder:validation:Required
	Spec LlmProviderSpec `json:"spec"`
}

// +kubebuilder:object:root=true

// LlmProviderList contains a list of LlmProvider.
type LlmProviderList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []LlmProvider `json:"items"`
}

func init() {
	SchemeBuilder.Register(&LlmProvider{}, &LlmProviderList{})
}

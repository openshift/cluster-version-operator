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

// LLMProviderType identifies the hosting backend for an LLM provider.
//
// Each backend has different authentication requirements and API endpoints.
// The type field acts as the discriminator for a union: exactly one of the
// per-provider configuration fields must be set, matching the type value.
//
// Allowed values:
//   - "Anthropic"          — Direct Anthropic API.
//   - "GoogleCloudVertex"  — Google Cloud Vertex AI.
//   - "OpenAI"             — OpenAI-compatible API.
//   - "AzureOpenAI"        — Azure OpenAI Service.
//   - "AWSBedrock"         — AWS Bedrock.
//
// +kubebuilder:validation:Enum=Anthropic;GoogleCloudVertex;OpenAI;AzureOpenAI;AWSBedrock
type LLMProviderType string

const (
	// LLMProviderAnthropic uses the Anthropic API directly.
	LLMProviderAnthropic LLMProviderType = "Anthropic"
	// LLMProviderGoogleCloudVertex uses Google Cloud Vertex AI as the LLM backend.
	LLMProviderGoogleCloudVertex LLMProviderType = "GoogleCloudVertex"
	// LLMProviderOpenAI uses an OpenAI-compatible API endpoint.
	LLMProviderOpenAI LLMProviderType = "OpenAI"
	// LLMProviderAzureOpenAI uses the Azure OpenAI Service.
	LLMProviderAzureOpenAI LLMProviderType = "AzureOpenAI"
	// LLMProviderAWSBedrock uses AWS Bedrock.
	LLMProviderAWSBedrock LLMProviderType = "AWSBedrock"
)

// AnthropicConfig contains configuration for the Anthropic API provider.
type AnthropicConfig struct {
	// credentialsSecret references a Secret in the operator namespace
	// (openshift-lightspeed) containing the Anthropic API credentials.
	// The Secret must contain the key ANTHROPIC_API_KEY.
	// +required
	CredentialsSecret SecretReference `json:"credentialsSecret,omitzero"`

	// url is an optional override for the Anthropic API endpoint.
	// Only needed for custom deployments or API proxies.
	// Must be a valid HTTP or HTTPS URL with a hostname. Paths and query
	// parameters are allowed. Fragments and userinfo are not permitted.
	// Maximum 2048 characters.
	// +optional
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=2048
	// +kubebuilder:validation:XValidation:rule="isURL(self) && url(self).getScheme() in ['http', 'https']",message="must use http or https scheme"
	// +kubebuilder:validation:XValidation:rule="isURL(self) && url(self).getHostname() != ''",message="must include a hostname"
	// +kubebuilder:validation:XValidation:rule="!self.contains('@')",message="userinfo is not allowed in URL"
	// +kubebuilder:validation:XValidation:rule="!self.contains('#')",message="fragments are not allowed in URL"
	URL string `json:"url,omitempty"`
}

// GoogleCloudVertexConfig contains configuration for the Google Cloud Vertex AI provider.
type GoogleCloudVertexConfig struct {
	// credentialsSecret references a Secret in the operator namespace
	// (openshift-lightspeed) containing a GCP service account JSON key.
	// The Secret must contain the key GOOGLE_APPLICATION_CREDENTIALS.
	// +required
	CredentialsSecret SecretReference `json:"credentialsSecret,omitzero"`

	// projectID is the Google Cloud Project ID where Vertex AI is enabled.
	// A Project ID is a globally unique identifier that must be 6 to 30
	// characters in length, can only contain lowercase letters, digits, and
	// hyphens, must start with a letter, and cannot end with a hyphen.
	// +required
	// +kubebuilder:validation:MinLength=6
	// +kubebuilder:validation:MaxLength=30
	// +kubebuilder:validation:XValidation:rule="self.matches('^[a-z][a-z0-9-]*[a-z0-9]$')",message="projectID must start with a lowercase letter, contain only lowercase letters, digits, and hyphens, and cannot end with a hyphen"
	ProjectID string `json:"projectID,omitempty"`

	// region is the GCP region for the Vertex AI endpoint.
	// Must begin with a lowercase letter and end with a lowercase
	// alphanumeric character. May contain lowercase letters, digits,
	// and hyphens (e.g., "us-central1", "europe-west4", "asia-southeast1").
	// +required
	// +kubebuilder:validation:MinLength=2
	// +kubebuilder:validation:MaxLength=63
	// +kubebuilder:validation:XValidation:rule="self.matches('^[a-z][a-z0-9-]*[a-z0-9]$')",message="region must contain only lowercase letters, digits, and hyphens, start with a letter, and not end with a hyphen"
	Region string `json:"region,omitempty"`

	// url is an optional override for the Vertex AI API endpoint.
	// Only needed for custom deployments or API proxies.
	// Must be a valid HTTP or HTTPS URL with a hostname. Paths and query
	// parameters are allowed. Fragments and userinfo are not permitted.
	// Maximum 2048 characters.
	// +optional
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=2048
	// +kubebuilder:validation:XValidation:rule="isURL(self) && url(self).getScheme() in ['http', 'https']",message="must use http or https scheme"
	// +kubebuilder:validation:XValidation:rule="isURL(self) && url(self).getHostname() != ''",message="must include a hostname"
	// +kubebuilder:validation:XValidation:rule="!self.contains('@')",message="userinfo is not allowed in URL"
	// +kubebuilder:validation:XValidation:rule="!self.contains('#')",message="fragments are not allowed in URL"
	URL string `json:"url,omitempty"`
}

// OpenAIConfig contains configuration for an OpenAI-compatible API provider.
type OpenAIConfig struct {
	// credentialsSecret references a Secret in the operator namespace
	// (openshift-lightspeed) containing the OpenAI API credentials.
	// The Secret must contain the key OPENAI_API_KEY.
	// +required
	CredentialsSecret SecretReference `json:"credentialsSecret,omitzero"`

	// url is an optional override for the OpenAI API endpoint.
	// Only needed for custom deployments or API proxies.
	// Must be a valid HTTP or HTTPS URL with a hostname. Paths and query
	// parameters are allowed. Fragments and userinfo are not permitted.
	// Maximum 2048 characters.
	// +optional
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=2048
	// +kubebuilder:validation:XValidation:rule="isURL(self) && url(self).getScheme() in ['http', 'https']",message="must use http or https scheme"
	// +kubebuilder:validation:XValidation:rule="isURL(self) && url(self).getHostname() != ''",message="must include a hostname"
	// +kubebuilder:validation:XValidation:rule="!self.contains('@')",message="userinfo is not allowed in URL"
	// +kubebuilder:validation:XValidation:rule="!self.contains('#')",message="fragments are not allowed in URL"
	URL string `json:"url,omitempty"`
}

// AzureOpenAIConfig contains configuration for the Azure OpenAI Service provider.
type AzureOpenAIConfig struct {
	// credentialsSecret references a Secret in the operator namespace
	// (openshift-lightspeed) containing the Azure OpenAI API credentials.
	// The Secret must contain the key AZURE_OPENAI_API_KEY.
	// +required
	CredentialsSecret SecretReference `json:"credentialsSecret,omitzero"`

	// endpoint is the Azure OpenAI resource endpoint
	// (e.g., "https://my-resource.openai.azure.com").
	// Must be a valid HTTP or HTTPS URL with a hostname. Paths and query
	// parameters are allowed. Fragments and userinfo are not permitted.
	// Maximum 2048 characters.
	// +required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=2048
	// +kubebuilder:validation:XValidation:rule="isURL(self) && url(self).getScheme() in ['http', 'https']",message="must use http or https scheme"
	// +kubebuilder:validation:XValidation:rule="isURL(self) && url(self).getHostname() != ''",message="must include a hostname"
	// +kubebuilder:validation:XValidation:rule="!self.contains('@')",message="userinfo is not allowed in URL"
	// +kubebuilder:validation:XValidation:rule="!self.contains('#')",message="fragments are not allowed in URL"
	Endpoint string `json:"endpoint,omitempty"`

	// apiVersion is the Azure OpenAI API version. Azure API versions use
	// a date-based format: YYYY-MM-DD with an optional "-preview" suffix
	// (e.g., "2024-02-01", "2024-08-01-preview").
	// When omitted, the SDK default is used.
	// +optional
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=32
	// +kubebuilder:validation:XValidation:rule="self.matches('^[0-9]{4}-[0-9]{2}-[0-9]{2}(-preview)?$')",message="apiVersion must be a date in YYYY-MM-DD format with an optional -preview suffix"
	APIVersion string `json:"apiVersion,omitempty"`

	// url is an optional override for the Azure OpenAI API endpoint.
	// Only needed for custom deployments or API proxies. This is separate
	// from the required 'endpoint' field which identifies the Azure resource.
	// Must be a valid HTTP or HTTPS URL with a hostname. Paths and query
	// parameters are allowed. Fragments and userinfo are not permitted.
	// Maximum 2048 characters.
	// +optional
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=2048
	// +kubebuilder:validation:XValidation:rule="isURL(self) && url(self).getScheme() in ['http', 'https']",message="must use http or https scheme"
	// +kubebuilder:validation:XValidation:rule="isURL(self) && url(self).getHostname() != ''",message="must include a hostname"
	// +kubebuilder:validation:XValidation:rule="!self.contains('@')",message="userinfo is not allowed in URL"
	// +kubebuilder:validation:XValidation:rule="!self.contains('#')",message="fragments are not allowed in URL"
	URL string `json:"url,omitempty"`
}

// AWSBedrockConfig contains configuration for the AWS Bedrock provider.
type AWSBedrockConfig struct {
	// credentialsSecret references a Secret in the operator namespace
	// (openshift-lightspeed) containing AWS credentials. The Secret must
	// contain the keys AWS_ACCESS_KEY_ID and AWS_SECRET_ACCESS_KEY.
	// +required
	CredentialsSecret SecretReference `json:"credentialsSecret,omitzero"`

	// region is the AWS region for the Bedrock endpoint.
	// Must begin with a lowercase letter and end with a lowercase
	// alphanumeric character. May contain lowercase letters, digits,
	// and hyphens (e.g., "us-east-1", "eu-west-2", "ap-southeast-1").
	// +required
	// +kubebuilder:validation:MinLength=2
	// +kubebuilder:validation:MaxLength=63
	// +kubebuilder:validation:XValidation:rule="self.matches('^[a-z][a-z0-9-]*[a-z0-9]$')",message="region must contain only lowercase letters, digits, and hyphens, start with a letter, and not end with a hyphen"
	Region string `json:"region,omitempty"`

	// url is an optional override for the AWS Bedrock API endpoint.
	// Only needed for custom deployments or API proxies.
	// Must be a valid HTTP or HTTPS URL with a hostname. Paths and query
	// parameters are allowed. Fragments and userinfo are not permitted.
	// Maximum 2048 characters.
	// +optional
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=2048
	// +kubebuilder:validation:XValidation:rule="isURL(self) && url(self).getScheme() in ['http', 'https']",message="must use http or https scheme"
	// +kubebuilder:validation:XValidation:rule="isURL(self) && url(self).getHostname() != ''",message="must include a hostname"
	// +kubebuilder:validation:XValidation:rule="!self.contains('@')",message="userinfo is not allowed in URL"
	// +kubebuilder:validation:XValidation:rule="!self.contains('#')",message="fragments are not allowed in URL"
	URL string `json:"url,omitempty"`
}

// LLMProviderSpec defines the desired state of LLMProvider.
//
// The type field is the discriminator. Exactly one of the per-provider
// configuration fields (anthropic, googleCloudVertex, openAI, azureOpenAI,
// awsBedrock) must be set, matching the type value.
//
// +kubebuilder:validation:XValidation:rule="self.type == 'Anthropic' ? has(self.anthropic) : !has(self.anthropic)",message="anthropic is required when type is Anthropic, and forbidden otherwise"
// +kubebuilder:validation:XValidation:rule="self.type == 'GoogleCloudVertex' ? has(self.googleCloudVertex) : !has(self.googleCloudVertex)",message="googleCloudVertex is required when type is GoogleCloudVertex, and forbidden otherwise"
// +kubebuilder:validation:XValidation:rule="self.type == 'OpenAI' ? has(self.openAI) : !has(self.openAI)",message="openAI is required when type is OpenAI, and forbidden otherwise"
// +kubebuilder:validation:XValidation:rule="self.type == 'AzureOpenAI' ? has(self.azureOpenAI) : !has(self.azureOpenAI)",message="azureOpenAI is required when type is AzureOpenAI, and forbidden otherwise"
// +kubebuilder:validation:XValidation:rule="self.type == 'AWSBedrock' ? has(self.awsBedrock) : !has(self.awsBedrock)",message="awsBedrock is required when type is AWSBedrock, and forbidden otherwise"
type LLMProviderSpec struct {
	// type is a required field that configures which LLM provider backend
	// should be used.
	//
	// Allowed values are Anthropic, GoogleCloudVertex, OpenAI, AzureOpenAI,
	// and AWSBedrock.
	//
	// When set to Anthropic, agents referencing this provider will use the
	// Anthropic API directly, and the 'anthropic' field must be configured.
	//
	// When set to GoogleCloudVertex, agents referencing this provider will
	// use Google Cloud Vertex AI, and the 'googleCloudVertex' field must be
	// configured.
	//
	// When set to OpenAI, agents referencing this provider will use an
	// OpenAI-compatible API, and the 'openAI' field must be configured.
	//
	// When set to AzureOpenAI, agents referencing this provider will use
	// the Azure OpenAI Service, and the 'azureOpenAI' field must be
	// configured.
	//
	// When set to AWSBedrock, agents referencing this provider will use
	// AWS Bedrock, and the 'awsBedrock' field must be configured.
	// +required
	Type LLMProviderType `json:"type,omitempty"`

	// anthropic contains Anthropic-specific configuration.
	// Required when type is "Anthropic".
	// +optional
	Anthropic AnthropicConfig `json:"anthropic,omitzero"`

	// googleCloudVertex contains Google Cloud Vertex AI-specific configuration.
	// Required when type is "GoogleCloudVertex".
	// +optional
	GoogleCloudVertex GoogleCloudVertexConfig `json:"googleCloudVertex,omitzero"`

	// openAI contains OpenAI-specific configuration.
	// Required when type is "OpenAI".
	// +optional
	OpenAI OpenAIConfig `json:"openAI,omitzero"`

	// azureOpenAI contains Azure OpenAI Service-specific configuration.
	// Required when type is "AzureOpenAI".
	// +optional
	AzureOpenAI AzureOpenAIConfig `json:"azureOpenAI,omitzero"`

	// awsBedrock contains AWS Bedrock-specific configuration.
	// Required when type is "AWSBedrock".
	// +optional
	AWSBedrock AWSBedrockConfig `json:"awsBedrock,omitzero"`
}

// +kubebuilder:object:root=true
// +kubebuilder:resource:scope=Cluster
// +kubebuilder:printcolumn:name="Type",type=string,JSONPath=`.spec.type`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// LLMProvider defines an LLM provider configuration. It is the first link in
// the CRD chain (LLMProvider -> Agent -> Workflow -> Proposal) and is
// referenced by Agent resources via spec.llmProvider.
//
// LLMProvider is cluster-scoped — the cluster admin manages LLM infrastructure
// centrally. The operator uses the credentials to configure the LLM client
// inside agent sandbox pods. The model is specified on the Agent CR, allowing
// multiple agents to share one LLMProvider with different models.
//
// Typically you create one provider per backend (e.g., one for Vertex AI)
// and then reference it from multiple Agent resources with different models.
//
// Example — a Vertex AI provider (model specified on Agent, not here):
//
//	apiVersion: agentic.openshift.io/v1alpha1
//	kind: LLMProvider
//	metadata:
//	  name: vertex-ai
//	spec:
//	  type: GoogleCloudVertex
//	  googleCloudVertex:
//	    credentialsSecret:
//	      name: llm-credentials
//	    projectID: my-gcp-project
//	    region: us-central1
type LLMProvider struct {
	metav1.TypeMeta `json:",inline"`

	// metadata is the standard object metadata.
	// +optional
	metav1.ObjectMeta `json:"metadata,omitempty"`

	// spec defines the desired state of LLMProvider.
	// +required
	Spec LLMProviderSpec `json:"spec,omitzero"`
}

// +kubebuilder:object:root=true

// LLMProviderList contains a list of LLMProvider.
type LLMProviderList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []LLMProvider `json:"items"`
}

func init() {
	SchemeBuilder.Register(&LLMProvider{}, &LLMProviderList{})
}

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

// SecretReference references a Kubernetes Secret by name within the same
// namespace. Used for credentials and authentication tokens.
type SecretReference struct {
	// name of the Secret. Must be a valid RFC 1123 DNS subdomain.
	// +required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=253
	// +kubebuilder:validation:XValidation:rule="!format.dns1123Subdomain().validate(self).hasValue()",message="must be a valid DNS subdomain: lowercase alphanumeric characters, hyphens, and dots"
	Name string `json:"name,omitempty"`
}

// LLMProviderReference references a cluster-scoped LLMProvider CR by name.
type LLMProviderReference struct {
	// name of the LLMProvider. Must be a valid RFC 1123 DNS subdomain.
	// +required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=253
	// +kubebuilder:validation:XValidation:rule="!format.dns1123Subdomain().validate(self).hasValue()",message="must be a valid DNS subdomain: lowercase alphanumeric characters, hyphens, and dots"
	Name string `json:"name,omitempty"`
}

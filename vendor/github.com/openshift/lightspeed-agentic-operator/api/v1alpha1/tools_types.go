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

// SecretMountType specifies how a secret is exposed in the sandbox pod.
//
// Allowed values:
//   - "EnvVar"    — inject the secret value as an environment variable.
//   - "FilePath"  — mount the secret as a file at a given path.
//
// +kubebuilder:validation:Enum=EnvVar;FilePath
type SecretMountType string

const (
	SecretMountEnvVar   SecretMountType = "EnvVar"
	SecretMountFilePath SecretMountType = "FilePath"
)

// SecretMountEnvVarConfig specifies the environment variable name to
// inject the secret value as.
type SecretMountEnvVarConfig struct {
	// name is the environment variable name (e.g., "GITHUB_TOKEN").
	// Must be uppercase letters, digits, and underscores, starting
	// with a letter or underscore.
	// +required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=256
	// +kubebuilder:validation:XValidation:rule="self.matches('^[A-Z_][A-Z0-9_]*$')",message="must be a valid environment variable name: uppercase letters, digits, and underscores, starting with a letter or underscore"
	Name string `json:"name,omitempty"`
}

// SecretMountFilePathConfig specifies the file path where the secret
// value is mounted.
type SecretMountFilePathConfig struct {
	// path is the absolute file path (e.g., "/etc/secrets/tls.crt").
	// Must start with a forward slash.
	// +required
	// +kubebuilder:validation:MinLength=2
	// +kubebuilder:validation:MaxLength=512
	// +kubebuilder:validation:XValidation:rule="self.startsWith('/')",message="path must be an absolute path starting with '/'"
	Path string `json:"path,omitempty"`
}

// SecretMountSpec specifies how a secret is exposed in the sandbox pod.
// The type field is the discriminator: exactly one of envVar or filePath
// must be set, matching the type value.
//
// +kubebuilder:validation:XValidation:rule="self.type == 'EnvVar' ? has(self.envVar) : !has(self.envVar)",message="envVar is required when type is EnvVar, and forbidden otherwise"
// +kubebuilder:validation:XValidation:rule="self.type == 'FilePath' ? has(self.filePath) : !has(self.filePath)",message="filePath is required when type is FilePath, and forbidden otherwise"
type SecretMountSpec struct {
	// type specifies how the secret is exposed. Allowed values: "EnvVar",
	// "FilePath".
	//
	// When set to EnvVar, the secret value is injected as an environment
	// variable, and the 'envVar' field must be configured.
	//
	// When set to FilePath, the secret is mounted as a file, and the
	// 'filePath' field must be configured.
	// +required
	Type SecretMountType `json:"type,omitempty"`

	// envVar configures environment variable injection.
	// Required when type is "EnvVar".
	// +optional
	EnvVar SecretMountEnvVarConfig `json:"envVar,omitzero"`

	// filePath configures file mount.
	// Required when type is "FilePath".
	// +optional
	FilePath SecretMountFilePathConfig `json:"filePath,omitzero"`
}

// SecretRequirement declares a Kubernetes Secret that the sandbox needs
// at runtime. The cluster admin creates the actual Secret in the same
// namespace as the Proposal.
type SecretRequirement struct {
	// name of the Secret (must exist in the same namespace as the Proposal).
	// Must be a valid RFC 1123 DNS subdomain.
	// +required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=253
	// +kubebuilder:validation:XValidation:rule="!format.dns1123Subdomain().validate(self).hasValue()",message="must be a valid DNS subdomain: lowercase alphanumeric characters, hyphens, and dots"
	Name string `json:"name,omitempty"`

	// description explains what this secret is used for, helping the
	// cluster admin understand what credentials to provide.
	// +optional
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=1024
	Description string `json:"description,omitempty"`

	// mountAs specifies how the secret is exposed in the sandbox pod.
	// +required
	MountAs SecretMountSpec `json:"mountAs,omitzero"`
}

// ToolsSpec defines the tools available to an agent in its sandbox pod.
// This includes skills images, MCP servers, and required secrets.
//
// ToolsSpec is specified on a Proposal either as a shared default
// (spec.tools) or per-step (spec.analysis.tools, spec.execution.tools,
// spec.verification.tools). Per-step tools replace the shared default
// for that step.
//
// +kubebuilder:validation:MinProperties=1
type ToolsSpec struct {
	// skills defines one or more OCI images containing skills to mount
	// in the agent's sandbox pod. The operator creates Kubernetes image
	// volumes (requires K8s 1.34+) and mounts them into the agent's
	// skills directory. Each image must be unique within the list.
	// +optional
	// +listType=map
	// +listMapKey=image
	// +kubebuilder:validation:MinItems=1
	// +kubebuilder:validation:MaxItems=20
	Skills []SkillsSource `json:"skills,omitempty"`

	// mcpServers defines external MCP (Model Context Protocol) servers the
	// agent can connect to for additional tools and context.
	// +optional
	// +listType=map
	// +listMapKey=name
	// +kubebuilder:validation:MinItems=1
	// +kubebuilder:validation:MaxItems=20
	MCPServers []MCPServerConfig `json:"mcpServers,omitempty"`

	// requiredSecrets declares Kubernetes Secrets that the sandbox pod
	// needs at runtime. The cluster admin creates the actual Secrets
	// in the same namespace as the Proposal.
	// +optional
	// +listType=map
	// +listMapKey=name
	// +kubebuilder:validation:MinItems=1
	// +kubebuilder:validation:MaxItems=20
	RequiredSecrets []SecretRequirement `json:"requiredSecrets,omitempty"`
}

func (t ToolsSpec) IsZero() bool {
	return len(t.Skills) == 0 && len(t.MCPServers) == 0 && len(t.RequiredSecrets) == 0
}

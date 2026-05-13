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

// MCPHeaderSourceType defines how a header value is sourced when the
// operator configures MCP server connections for an agent.
//
//   - "Secret"     — The value is read from a Kubernetes Secret referenced
//     by the secret field. Use this for API keys and tokens.
//   - "ServiceAccountToken" — The operator injects a Kubernetes service account
//     token automatically (for MCP servers that accept K8s auth).
//   - "Client"     — The value is provided by the calling client at
//     runtime (e.g., forwarded from a user session).
//
// +kubebuilder:validation:Enum=Secret;ServiceAccountToken;Client
type MCPHeaderSourceType string

const (
	// MCPHeaderSourceTypeSecret reads the header value from a Kubernetes Secret.
	MCPHeaderSourceTypeSecret MCPHeaderSourceType = "Secret"
	// MCPHeaderSourceTypeServiceAccountToken uses an auto-injected Kubernetes SA token.
	MCPHeaderSourceTypeServiceAccountToken MCPHeaderSourceType = "ServiceAccountToken"
	// MCPHeaderSourceTypeClient expects the value to be provided by the caller.
	MCPHeaderSourceTypeClient MCPHeaderSourceType = "Client"
)

// MCPHeaderValueSource defines where to obtain the value for an MCP header.
// Exactly one source is used depending on the type field.
// +kubebuilder:validation:XValidation:rule="self.type == 'Secret' ? has(self.secret) : !has(self.secret)",message="secret is required when type is Secret, and forbidden otherwise"
type MCPHeaderValueSource struct {
	// type specifies the source type for the header value. Allowed values:
	//   - "Secret"     — reads the value from a Kubernetes Secret (use for
	//     API keys and tokens). Requires the secret field to be set.
	//   - "ServiceAccountToken" — auto-injects a Kubernetes service account token
	//     (for MCP servers that accept K8s auth).
	//   - "Client"     — the value is provided by the calling client at
	//     runtime (e.g., forwarded from a user session).
	// +required
	Type MCPHeaderSourceType `json:"type,omitempty"`

	// secret references a Secret containing the header value.
	// Required when type is "Secret".
	// +optional
	Secret SecretReference `json:"secret,omitzero"`
}

// MCPHeader defines an HTTP header to send with every request to an
// MCP server. Used for authentication and routing.
type MCPHeader struct {
	// name of the header (e.g., "Authorization", "X-API-Key").
	// Must be at least 1 character, containing only letters, digits, and hyphens.
	// +required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=253
	// +kubebuilder:validation:XValidation:rule="self.matches('^[A-Za-z][A-Za-z0-9-]*$')",message="name must start with a letter and contain only letters, digits, and hyphens"
	Name string `json:"name,omitempty"`

	// valueFrom is the source of the header value.
	// +required
	ValueFrom MCPHeaderValueSource `json:"valueFrom,omitzero"`
}

// MCPServerConfig defines the configuration for an MCP (Model Context Protocol)
// server that the agent can connect to for additional tools and context.
// MCP servers extend the agent's capabilities beyond its built-in skills.
//
// Example — connecting to an OpenShift MCP server with SA token auth:
//
//	mcpServers:
//	  - name: openshift
//	    url: https://mcp.openshift-lightspeed.svc:8443/sse
//	    timeoutSeconds: 10
//	    headers:
//	      - name: Authorization
//	        valueFrom:
//	          type: ServiceAccountToken
//
// Example — connecting to an external API with secret-based auth:
//
//	mcpServers:
//	  - name: pagerduty
//	    url: https://mcp-pagerduty.example.com/sse
//	    headers:
//	      - name: X-API-Key
//	        valueFrom:
//	          type: Secret
//	          secret:
//	            name: pagerduty-api-key
type MCPServerConfig struct {
	// name of the MCP server. Must start with a letter and contain only
	// lowercase alphanumeric characters and hyphens. Must be 1-253 characters.
	// +required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=253
	// +kubebuilder:validation:XValidation:rule="self.matches('^[a-z][a-z0-9-]*$')",message="name must start with a lowercase letter and contain only lowercase alphanumerics and hyphens"
	Name string `json:"name,omitempty"`

	// url of the MCP server (HTTP/HTTPS). Must be an HTTP or HTTPS URL,
	// maximum 2048 characters.
	// +required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=2048
	// +kubebuilder:validation:XValidation:rule="isURL(self) && url(self).getScheme() in ['http', 'https']",message="url must be a valid HTTP or HTTPS URL"
	URL string `json:"url,omitempty"`

	// timeoutSeconds is the per-request timeout for calls to this MCP server,
	// in seconds. Default is 5.
	// Valid range: 1-300.
	// +optional
	// +default=5
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=300
	TimeoutSeconds int32 `json:"timeoutSeconds,omitempty"`

	// headers to send to the MCP server. Maximum 20 items.
	// +optional
	// +listType=map
	// +listMapKey=name
	// +kubebuilder:validation:MinItems=1
	// +kubebuilder:validation:MaxItems=20
	Headers []MCPHeader `json:"headers,omitempty"`
}

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
	// Must be a valid OCI image pullspec: a domain, followed by a repository path,
	// ending with either a tag (:tag) or a digest (@algorithm:hex).
	// Must be 1-512 characters.
	// +required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=512
	// +kubebuilder:validation:XValidation:rule="self.matches('^([a-zA-Z0-9]|[a-zA-Z0-9][a-zA-Z0-9-]*[a-zA-Z0-9])((\\\\.([a-zA-Z0-9]|[a-zA-Z0-9][a-zA-Z0-9-]*[a-zA-Z0-9]))+)?(:[0-9]+)?\\\\b')",message="must start with a valid domain. valid domains must be alphanumeric characters (lowercase and uppercase) separated by the '.' character."
	// +kubebuilder:validation:XValidation:rule="self.find('(/[a-z0-9]+((([._]|__|[-]*)[a-z0-9]+)+)?((/[a-z0-9]+((([._]|__|[-]*)[a-z0-9]+)+)?)+)?)') != ''",message="a valid name is required. valid names must contain lowercase alphanumeric characters separated only by the '.', '_', '__', '-' characters."
	// +kubebuilder:validation:XValidation:rule="self.find('(@.*:)') != '' || self.find(':.*$') != ''",message="must end with a digest or a tag"
	// +kubebuilder:validation:XValidation:rule="self.find('(@.*:)') == '' ? (self.find(':.*$') != '' ? self.find(':.*$').substring(1).size() <= 127 : true) : true",message="tag must not be more than 127 characters"
	// +kubebuilder:validation:XValidation:rule="self.find('(@.*:)') == '' ? (self.find(':.*$') != '' ? self.find(':.*$').matches(':[\\\\w][\\\\w.-]*$') : true) : true",message="tag is invalid. valid tags must begin with a word character followed by word characters, '.', or '-'"
	// +kubebuilder:validation:XValidation:rule="self.find('(@.*:)') != '' ? self.find('(@.*:)').matches('(@[A-Za-z][A-Za-z0-9]*([_+.][A-Za-z][A-Za-z0-9]*)*[:])') : true",message="digest algorithm is not valid. valid algorithms must start with an alpha character followed by alphanumeric characters and may contain '-', '_', '+', and '.' characters."
	// +kubebuilder:validation:XValidation:rule="self.find('(@.*:)') != '' ? self.find(':.*$').substring(1).size() >= 32 : true",message="digest must be at least 32 characters"
	// +kubebuilder:validation:XValidation:rule="self.find('(@.*:)') != '' ? self.find(':.*$').matches(':[0-9A-Fa-f]*$') : true",message="digest must only contain hex characters (A-F, a-f, 0-9)"
	Image string `json:"image,omitempty"`

	// paths restricts which directories from the image are mounted.
	// Each path is mounted as a separate subPath volumeMount into the agent's
	// skills directory. The last segment of each path becomes the mount name
	// (e.g., "/skills/prometheus" mounts as "prometheus").
	//
	// Each path must be an absolute file path: starts with "/", no ".."
	// or "." segments, no double slashes, no trailing slash, and only
	// alphanumeric characters, hyphens, underscores, dots, and slashes.
	//
	// When omitted, the entire image is mounted as a single volume.
	// Maximum 50 items.
	// +optional
	// +listType=atomic
	// +kubebuilder:validation:MinItems=1
	// +kubebuilder:validation:MaxItems=50
	// +kubebuilder:validation:XValidation:rule="self.all(p, p.size() >= 2 && p.size() <= 512)",message="each path must be 2-512 characters"
	// +kubebuilder:validation:XValidation:rule="self.all(p, p.startsWith('/'))",message="each path must be absolute (start with '/')"
	// +kubebuilder:validation:XValidation:rule="self.all(p, !p.endsWith('/'))",message="paths must not end with '/'"
	// +kubebuilder:validation:XValidation:rule="self.all(p, !p.contains('//'))",message="paths must not contain double slashes"
	// +kubebuilder:validation:XValidation:rule="self.all(p, !p.contains('/../') && !p.endsWith('/..') && !p.contains('/./') && !p.endsWith('/.'))",message="paths must not contain '.' or '..' segments"
	// +kubebuilder:validation:XValidation:rule="self.all(p, p.matches('^[a-zA-Z0-9/_.-]+$'))",message="paths may only contain alphanumeric characters, '/', '_', '.', and '-'"
	// +kubebuilder:validation:items:MinLength=2
	// +kubebuilder:validation:items:MaxLength=512
	Paths []string `json:"paths,omitempty"`
}

package external

import "github.com/openshift/cluster-version-operator/pkg/internal"

// The constants defined here are used by the components, e.g., e2e tests that have no access
// to github.com/openshift/cluster-version-operator/pkg/internal
// See https://pkg.go.dev/cmd/go#hdr-Internal_Directories
const (
	// DefaultCVONamespace is the default namespace for the Cluster Version Operator
	DefaultCVONamespace = internal.DefaultCVONamespace
	// DefaultClusterVersionName is the default name for the Cluster Version resource managed by the Cluster Version Operator
	DefaultClusterVersionName = internal.DefaultClusterVersionName
	// DefaultDeploymentName is the default name of the deployment for the Cluster Version Operator
	DefaultDeploymentName = internal.DefaultDeploymentName
	// DefaultContainerName is the default container name in the deployment for the Cluster Version Operator
	DefaultContainerName = internal.DefaultContainerName

	// ConditionalUpdateConditionTypeRecommended is a type of the condition present on a conditional update
	// that indicates whether the conditional update is recommended or not
	ConditionalUpdateConditionTypeRecommended = internal.ConditionalUpdateConditionTypeRecommended
)

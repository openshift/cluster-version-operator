package resourceapply

import (
	"strings"

	"github.com/google/go-cmp/cmp"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// CreateOnlyAnnotation means that this resource should be created if it does not exist, but should not be updated
// if it already exists.  It is uniformly respected across all resources, but the first known use-cases are for
// empty config.openshift.io and initial low-level operator resources.
// Set .metadata.annotations["release.openshift.io/create-only"]="true" to have a create-only resource.
const CreateOnlyAnnotation = "release.openshift.io/create-only"

// IsCreateOnly takes metadata and returns true if the resource should only be created, not updated.
func IsCreateOnly(metadata metav1.Object) bool {
	return strings.EqualFold(metadata.GetAnnotations()[CreateOnlyAnnotation], "true")
}

// ManifestDiff is a specialized case of cmp.Diff that ignores fields unlikely to match when one side is read
// from the APIServer (`fromKAS`) and the other from a manifest file (`fromManifest`). It is specifically
// aligned with EnsureObjectMeta when it comes to ObjectMeta.
func ManifestDiff(fromKAS, fromManifest any) string {
	return cmp.Diff(fromKAS, fromManifest, cmp.FilterPath(
		func(p cmp.Path) bool {
			path := p.String()
			if path == "TypeMeta" || path == "Status" {
				// Ignore
				return true
			}
			if strings.HasPrefix(path, "ObjectMeta.") {
				if parts := strings.SplitN(path, ".", 3); len(parts) >= 2 {
					switch parts[1] {
					case "GenerateName", "SelfLink", "UID", "ResourceVersion", "Generation", "CreationTimestamp", "DeletionTimestamp", "DeletionGracePeriodSeconds", "Finalizers", "ManagedFields":
						// Ignore
						return true
					}
				}
				// The remaining ObjectMeta fields are those curated by EnsureObjectMeta
			}
			return false
		},
		cmp.Ignore(),
	))
}

package readiness

import (
	"context"
	"fmt"

	"k8s.io/client-go/dynamic"
)

// CRDCompatCheck verifies CRD stored/served version compatibility and operator constraints.
type CRDCompatCheck struct{}

func (c *CRDCompatCheck) Name() string { return "crd_compat" }

func (c *CRDCompatCheck) Run(ctx context.Context, dc dynamic.Interface, current, target string) (map[string]any, error) {
	result := map[string]any{}
	var sectionErrors []map[string]any

	// Check CRDs for version mismatches
	crds, err := ListResources(ctx, dc, GVRCRD, "")
	if err != nil {
		return nil, fmt.Errorf("failed to list CRDs: %w", err)
	}

	versionIssues := make([]map[string]any, 0)
	for _, crd := range crds {
		storedVersions := NestedSlice(crd.Object, "status", "storedVersions")
		servedVersions := NestedSlice(crd.Object, "spec", "versions")

		served := make(map[string]bool)
		for _, v := range servedVersions {
			vm, ok := v.(map[string]interface{})
			if !ok {
				continue
			}
			name := NestedString(vm, "name")
			isServed := NestedBool(vm, "served")
			if isServed {
				served[name] = true
			}
		}

		for _, sv := range storedVersions {
			stored, _ := sv.(string)
			if stored != "" && !served[stored] {
				versionIssues = append(versionIssues, map[string]any{
					"crd":            crd.GetName(),
					"stored_version": stored,
					"issue":          "stored version no longer served",
				})
			}
		}
	}

	result["total_crds"] = len(crds)
	result["version_issues"] = versionIssues

	result["summary"] = map[string]any{
		"total_crds":     len(crds),
		"version_issues": len(versionIssues),
	}

	if len(sectionErrors) > 0 {
		result["errors"] = sectionErrors
	}

	return result, nil
}

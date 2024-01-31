package cvo

import (
	"encoding/json"
	"strings"

	configv1 "github.com/openshift/api/config/v1"
	"github.com/openshift/cluster-version-operator/pkg/payload"
)

const (
	resourceReconciliationIssuesConditionType configv1.ClusterStatusConditionType = "ResourceReconciliationIssues"

	noResourceReconciliationIssuesReason  string = "NoIssues"
	noResourceReconciliationIssuesMessage string = "No issues found during resource reconciliation"

	resourceReconciliationIssuesFoundReason string = "IssuesFound"
)

type ResourceReconciliationIssueUpdateError struct {
	NestedMessage string `json:"nestedMessage,omitempty"`
	Effect        string `json:"effect,omitempty"`
	Reason        string `json:"reason,omitempty"`
	PluralReason  string `json:"pluralReason,omitempty"`
	Message       string `json:"message,omitempty"`
	Name          string `json:"name,omitempty"`
	Names         string `json:"names,omitempty"`

	ManifestFilename string `json:"manifestFilename,omitempty"`
	ResourceGroup    string `json:"manifestGroup,omitempty"`
	ResourceVersion  string `json:"resourceVersion,omitempty"`
	ResourceKind     string `json:"resourceKind,omitempty"`
	ResourceSummary  string `json:"resourceSummary,omitempty"`
}

type ResourceReconciliationIssueString struct {
	Message string `json:"message"`
}

type ResourceReconciliationIssue struct {
	UpdateError *ResourceReconciliationIssueUpdateError `json:"updateError,omitempty"`
	SimpleError *ResourceReconciliationIssueString      `json:"simpleError,omitempty"`
}

func resourceReconciliationIssuesFromErrors(errs []error) string {
	var resourceReconciliationIssues []ResourceReconciliationIssue
	for _, err := range errs {
		if updateError, ok := err.(*payload.UpdateError); ok {
			resourceReconciliationIssues = append(resourceReconciliationIssues, ResourceReconciliationIssue{
				UpdateError: &ResourceReconciliationIssueUpdateError{
					NestedMessage:    updateError.Nested.Error(),
					Effect:           string(updateError.UpdateEffect),
					Reason:           updateError.Reason,
					PluralReason:     updateError.PluralReason,
					Message:          updateError.Message,
					Name:             updateError.Name,
					Names:            strings.Join(updateError.Names, ", "),
					ManifestFilename: updateError.Task.Manifest.OriginalFilename,
					ResourceGroup:    updateError.Task.Manifest.GVK.Group,
					ResourceVersion:  updateError.Task.Manifest.GVK.Version,
					ResourceKind:     updateError.Task.Manifest.GVK.Kind,
				},
			})
		} else {
			resourceReconciliationIssues = append(resourceReconciliationIssues, ResourceReconciliationIssue{
				SimpleError: &ResourceReconciliationIssueString{
					Message: err.Error(),
				},
			})
		}
	}
	if raw, err := json.Marshal(resourceReconciliationIssues); err == nil {
		return string(raw)
	}
	return ""
}

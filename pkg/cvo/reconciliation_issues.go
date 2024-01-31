package cvo

import (
	"encoding/json"
	"strings"

	configv1 "github.com/openshift/api/config/v1"
	"github.com/openshift/cluster-version-operator/pkg/payload"
)

const (
	reconciliationIssuesConditionType configv1.ClusterStatusConditionType = "ReconciliationIssues"

	noReconciliationIssuesReason  string = "NoIssues"
	noReconciliationIssuesMessage string = "No issues found during reconciliation"

	reconciliationIssuesFoundReason string = "IssuesFound"
)

type ReconciliationIssueUpdateError struct {
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

type ReconciliationIssueString struct {
	Message string `json:"message"`
}

type ReconciliationIssue struct {
	UpdateError *ReconciliationIssueUpdateError `json:"updateError,omitempty"`
	SimpleError *ReconciliationIssueString      `json:"simpleError,omitempty"`
}

func reconciliationIssuesFromErrors(errs []error) string {
	var reconciliationIssues []ReconciliationIssue
	for _, err := range errs {
		if updateError, ok := err.(*payload.UpdateError); ok {
			reconciliationIssues = append(reconciliationIssues, ReconciliationIssue{
				UpdateError: &ReconciliationIssueUpdateError{
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
			reconciliationIssues = append(reconciliationIssues, ReconciliationIssue{
				SimpleError: &ReconciliationIssueString{
					Message: err.Error(),
				},
			})
		}
	}
	if raw, err := json.Marshal(reconciliationIssues); err == nil {
		return string(raw)
	}
	return ""
}

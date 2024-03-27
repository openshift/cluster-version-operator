package cvo

import v1 "github.com/openshift/api/config/v1"

const (
	reconciliationIssuesConditionType v1.ClusterStatusConditionType = "ReconciliationIssues"

	noReconciliationIssuesReason  string = "NoIssues"
	noReconciliationIssuesMessage string = "No issues found during reconciliation"

	reconciliationIssuesFoundReason  string = "IssuesFound"
	reconciliationIssuesFoundMessage string = "Issues found during reconciliation"
)

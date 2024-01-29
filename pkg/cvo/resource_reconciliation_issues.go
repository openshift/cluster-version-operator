package cvo

import v1 "github.com/openshift/api/config/v1"

const (
	resourceReconciliationIssuesConditionType v1.ClusterStatusConditionType = "ResourceReconciliationIssues"

	noResourceReconciliationIssuesReason  string = "NoIssues"
	noResourceReconciliationIssuesMessage string = "No issues found during resource reconciliation"

	resourceReconciliationIssuesFoundReason  string = "IssuesFound"
	resourceReconciliationIssuesFoundMessage string = "Issues found during resource reconciliation"
)

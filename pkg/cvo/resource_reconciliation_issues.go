package cvo

import (
	configv1 "github.com/openshift/api/config/v1"
)

const (
	resourceReconciliationIssuesConditionType configv1.ClusterStatusConditionType = "ResourceReconciliationIssues"

	noResourceReconciliationIssuesReason  string = "NoIssues"
	noResourceReconciliationIssuesMessage string = "No issues found during resource reconciliation"

	resourceReconciliationIssuesFoundReason  string = "IssuesFound"
	resourceReconciliationIssuesFoundMessage string = "Issues found during resource reconciliation"
)

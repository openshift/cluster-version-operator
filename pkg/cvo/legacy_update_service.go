package cvo

import (
	configv1 "github.com/openshift/api/config/v1"

	"github.com/openshift/cluster-version-operator/lib/resourcemerge"
	"github.com/openshift/cluster-version-operator/pkg/internal"
)

const legacyOKDUpdateService = "https://amd64.origin.releases.ci.openshift.org/graph"

// UpdateOKDLegacyUpdateServiceCondition updates the status condition that warns about the legacy OKD update service.
func UpdateOKDLegacyUpdateServiceCondition(status *configv1.ClusterVersionStatus, upstream configv1.URL) {
	if string(upstream) != legacyOKDUpdateService {
		resourcemerge.RemoveOperatorStatusCondition(&status.Conditions, internal.ClusterVersionOKDLegacyUpdateService)
		return
	}

	resourcemerge.SetOperatorStatusCondition(&status.Conditions, configv1.ClusterOperatorStatusCondition{
		Type:    internal.ClusterVersionOKDLegacyUpdateService,
		Status:  configv1.ConditionTrue,
		Reason:  "LegacyUpstreamConfigured",
		Message: "ClusterVersion spec.upstream is set to the legacy OKD update service " + legacyOKDUpdateService + ". Clear spec.upstream to use the OKD Cincinnati update service.",
	})
}

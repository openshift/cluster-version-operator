package resourcemerge

import (
	cvv1 "github.com/openshift/cluster-version-operator/pkg/apis/config.openshift.io/v1"
	"k8s.io/apimachinery/pkg/api/equality"
)

func EnsureClusterVersion(modified *bool, existing *cvv1.ClusterVersion, required cvv1.ClusterVersion) {
	EnsureObjectMeta(modified, &existing.ObjectMeta, required.ObjectMeta)
	if !equality.Semantic.DeepEqual(existing.Spec.Upstream, required.Spec.Upstream) {
		*modified = true
		if required.Spec.Upstream != nil {
			copied := *required.Spec.Upstream
			existing.Spec.Upstream = &copied
		} else {
			existing.Spec.Upstream = nil
		}
	}
	if existing.Spec.Channel != required.Spec.Channel {
		*modified = true
		existing.Spec.Channel = required.Spec.Channel
	}
	if existing.Spec.ClusterID != required.Spec.ClusterID {
		*modified = true
		existing.Spec.ClusterID = required.Spec.ClusterID
	}

	if !equality.Semantic.DeepEqual(existing.Spec.DesiredUpdate, required.Spec.DesiredUpdate) {
		*modified = true
		if required.Spec.DesiredUpdate != nil {
			copied := *required.Spec.DesiredUpdate
			existing.Spec.DesiredUpdate = &copied
		} else {
			existing.Spec.DesiredUpdate = nil
		}
	}
}

func EnsureClusterVersionStatus(modified *bool, existing *cvv1.ClusterVersion, required cvv1.ClusterVersion) {
	if !equality.Semantic.DeepEqual(existing.Status, required.Status) {
		*modified = true
		existing.Status = *required.Status.DeepCopy()
	}
}

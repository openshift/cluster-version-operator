package clusterversion

import (
	"context"
	"fmt"

	configv1 "github.com/openshift/api/config/v1"
	configv1listers "github.com/openshift/client-go/config/listers/config/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"

	"github.com/openshift/cluster-version-operator/lib/resourcemerge"
	precondition "github.com/openshift/cluster-version-operator/pkg/payload/precondition"
)

const backupConditionType = "RecentBackup"

// RecentEtcdBackup checks if a recent etcd backup has been taken.
type RecentEtcdBackup struct {
	key      string
	cvLister configv1listers.ClusterVersionLister
	coLister configv1listers.ClusterOperatorLister
}

// NewRecentEtcdBackup returns a new RecentEtcdBackup precondition check.
func NewRecentEtcdBackup(cvLister configv1listers.ClusterVersionLister, coLister configv1listers.ClusterOperatorLister) *RecentEtcdBackup {
	return &RecentEtcdBackup{
		key:      "version",
		cvLister: cvLister,
		coLister: coLister,
	}
}

func recentEtcdBackupCondition(lister configv1listers.ClusterOperatorLister) (reason string, message string) {
	var msgDetail string
	ops, err := lister.Get("etcd")
	if err == nil {
		backupCondition := resourcemerge.FindOperatorStatusCondition(ops.Status.Conditions, backupConditionType)
		if backupCondition == nil {
			reason = "EtcdRecentBackupNotSet"
			msgDetail = "etcd backup condition is not set."
		} else if backupCondition.Status != configv1.ConditionTrue {
			reason = backupCondition.Reason
			msgDetail = backupCondition.Message
		}
	} else {
		reason = "UnableToGetEtcdOperator"
		msgDetail = fmt.Sprintf("Unable to get etcd operator, err=%v.", err)
	}
	if len(msgDetail) > 0 {
		message = fmt.Sprintf("%s: %s", backupConditionType, msgDetail)
	}
	return reason, message
}

// Run runs the RecentEtcdBackup precondition. It returns a PreconditionError until Etcd indicates that a
// recent etcd backup has been taken.
func (pf *RecentEtcdBackup) Run(ctx context.Context, releaseContext precondition.ReleaseContext) error {
	cv, err := pf.cvLister.Get(pf.key)
	if apierrors.IsNotFound(err) || meta.IsNoMatchError(err) {
		return nil
	}
	if err != nil {
		return &precondition.Error{
			Nested:  err,
			Reason:  "UnknownError",
			Message: err.Error(),
			Name:    pf.Name(),
		}
	}

	currentVersion := GetCurrentVersion(cv.Status.History)
	currentMinor := GetEffectiveMinor(currentVersion)
	desiredMinor := GetEffectiveMinor(releaseContext.DesiredVersion)

	if minorVersionUpgrade(currentMinor, desiredMinor) {
		reason, message := recentEtcdBackupCondition(pf.coLister)
		if len(reason) > 0 {
			return &precondition.Error{
				Reason:  reason,
				Message: message,
				Name:    pf.Name(),
			}
		}
	}
	return nil
}

// Name returns Name for the precondition.
func (pf *RecentEtcdBackup) Name() string { return "EtcdRecentBackup" }

package clusterversion

import (
	"context"
	"fmt"
	"strings"

	configv1 "github.com/openshift/api/config/v1"
	configv1listers "github.com/openshift/client-go/config/listers/config/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/sets"

	"github.com/openshift/cluster-version-operator/lib/resourcemerge"
	"github.com/openshift/cluster-version-operator/pkg/internal"
	precondition "github.com/openshift/cluster-version-operator/pkg/payload/precondition"
)

// RecommendedUpdate checks if clusterversion is upgradeable currently.
type RecommendedUpdate struct {
	lister configv1listers.ClusterVersionLister
}

// NewRecommendedUpdate returns a new RecommendedUpdate precondition check.
func NewRecommendedUpdate(lister configv1listers.ClusterVersionLister) *RecommendedUpdate {
	return &RecommendedUpdate{
		lister: lister,
	}
}

// Run runs the RecommendedUpdate precondition.
// Returns PreconditionError when possible, if the requested target release is Recommended=False.
func (ru *RecommendedUpdate) Run(ctx context.Context, releaseContext precondition.ReleaseContext) error {
	clusterVersion, err := ru.lister.Get("version")
	if apierrors.IsNotFound(err) || meta.IsNoMatchError(err) {
		return nil
	}
	if err != nil {
		return &precondition.Error{
			Nested:             err,
			Reason:             "UnknownError",
			Message:            err.Error(),
			Name:               ru.Name(),
			NonBlockingWarning: true,
		}
	}
	// This function should be guarded by shouldReconcileAcceptRisks()
	// https://github.com/openshift/cluster-version-operator/blob/f4b9dfa6f6968d117b919089c6b32918f20843c9/pkg/cvo/cvo.go#L1187
	// However, here we do not have a handler for the operator
	// Now it is guarded by clusterVersion.Status.ConditionalUpdateRisks which is guarded by the above function
	unAcceptRisks := sets.New[string]()
	acceptRisks := sets.New[string]()
	if clusterVersion.Spec.DesiredUpdate != nil {
		for _, r := range clusterVersion.Spec.DesiredUpdate.AcceptRisks {
			acceptRisks.Insert(r.Name)
		}
	}
	for _, risk := range clusterVersion.Status.ConditionalUpdateRisks {
		if acceptRisks.Has(risk.Name) {
			continue
		}
		if condition := meta.FindStatusCondition(risk.Conditions, internal.ConditionalUpdateRiskConditionTypeApplies); condition != nil &&
			condition.Status == metav1.ConditionTrue &&
			internal.IsAlertConditionReason(condition.Reason) {
			unAcceptRisks.Insert(risk.Name)
		}
	}
	var alertError *precondition.Error
	if unAcceptRisks.Len() > 0 {
		alertError = &precondition.Error{
			Reason: "alertMightImpactUpdate",
			Message: fmt.Sprintf("Update from %s to %s is not recommended:\n\n%s",
				clusterVersion.Status.Desired.Version, releaseContext.DesiredVersion,
				fmt.Sprintf("Those alerts have to be accepted as risks before updates: %s", strings.Join(sets.List(unAcceptRisks), ", "))),
			Name:               ru.Name(),
			NonBlockingWarning: true,
		}
	}
	for _, recommended := range clusterVersion.Status.AvailableUpdates {
		if recommended.Version == releaseContext.DesiredVersion {
			return aggregate(alertError, nil)
		}
	}

	for _, conditionalUpdate := range clusterVersion.Status.ConditionalUpdates {
		if conditionalUpdate.Release.Version == releaseContext.DesiredVersion {
			for _, condition := range conditionalUpdate.Conditions {
				if condition.Type == internal.ConditionalUpdateConditionTypeRecommended {
					switch condition.Status {
					case metav1.ConditionTrue:
						return aggregate(alertError, nil)
					case metav1.ConditionFalse:
						return aggregate(alertError, &precondition.Error{
							Reason: condition.Reason,
							Message: fmt.Sprintf("Update from %s to %s is not recommended:\n\n%s",
								clusterVersion.Status.Desired.Version, releaseContext.DesiredVersion, condition.Message),
							Name:               ru.Name(),
							NonBlockingWarning: true,
						})
					default:
						return aggregate(alertError, &precondition.Error{
							Reason: condition.Reason,
							Message: fmt.Sprintf("Update from %s to %s is %s=%s: %s: %s",
								clusterVersion.Status.Desired.Version, releaseContext.DesiredVersion,
								condition.Type, condition.Status, condition.Reason, condition.Message),
							Name:               ru.Name(),
							NonBlockingWarning: true,
						})
					}
				}
			}
			return aggregate(alertError, &precondition.Error{
				Reason: "UnknownConditionType",
				Message: fmt.Sprintf("Update from %s to %s has a status.conditionalUpdates entry, but no Recommended condition.",
					clusterVersion.Status.Desired.Version, releaseContext.DesiredVersion),
				Name:               ru.Name(),
				NonBlockingWarning: true,
			})
		}
	}

	if clusterVersion.Spec.Channel == "" {
		return aggregate(alertError, &precondition.Error{
			Reason: "NoChannel",
			Message: fmt.Sprintf("Configured channel is unset, so the recommended status of updating from %s to %s is unknown.",
				clusterVersion.Status.Desired.Version, releaseContext.DesiredVersion),
			Name:               ru.Name(),
			NonBlockingWarning: true,
		})
	}

	reason := "UnknownUpdate"
	msg := ""
	if retrieved := resourcemerge.FindOperatorStatusCondition(clusterVersion.Status.Conditions, configv1.RetrievedUpdates); retrieved == nil {
		msg = fmt.Sprintf("No %s, so the recommended status of updating from %s to %s is unknown.", configv1.RetrievedUpdates,
			clusterVersion.Status.Desired.Version, releaseContext.DesiredVersion)
	} else if retrieved.Status == configv1.ConditionTrue {
		msg = fmt.Sprintf("%s=%s (%s), so the update from %s to %s is probably neither recommended nor supported.", retrieved.Type,
			retrieved.Status, retrieved.Reason, clusterVersion.Status.Desired.Version, releaseContext.DesiredVersion)
	} else {
		msg = fmt.Sprintf("%s=%s (%s), so the recommended status of updating from %s to %s is unknown.", retrieved.Type,
			retrieved.Status, retrieved.Reason, clusterVersion.Status.Desired.Version, releaseContext.DesiredVersion)
	}

	if msg != "" {
		return aggregate(alertError, &precondition.Error{
			Reason:             reason,
			Message:            msg,
			Name:               ru.Name(),
			NonBlockingWarning: true,
		})
	}
	return aggregate(alertError, nil)
}

func aggregate(e1, e2 *precondition.Error) error {
	if e1 == nil && e2 == nil {
		return nil
	}
	if e1 == nil {
		return e2
	}
	if e2 == nil {
		return e1
	}
	return &precondition.Error{
		Reason:             aggregateString(e1.Reason, e2.Reason, "|"),
		Message:            aggregateString(e1.Message, e2.Message, "\n"),
		Name:               aggregateString(e1.Name, e2.Name, "|"),
		NonBlockingWarning: e1.NonBlockingWarning && e2.NonBlockingWarning,
	}
}

func aggregateString(s1, s2, delimiter string) string {
	if s1 == "" {
		return s2
	}
	if s2 == "" {
		return s1
	}
	return s1 + delimiter + s2
}

// Name returns the name of the precondition.
func (ru *RecommendedUpdate) Name() string { return "ClusterVersionRecommendedUpdate" }

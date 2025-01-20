package updatestatus

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
	kerrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	appsv1client "k8s.io/client-go/kubernetes/typed/apps/v1"
	"k8s.io/klog/v2"

	configv1 "github.com/openshift/api/config/v1"
	configinformers "github.com/openshift/client-go/config/informers/externalversions"
	configv1listers "github.com/openshift/client-go/config/listers/config/v1"
	"github.com/openshift/library-go/pkg/controller/factory"
	"github.com/openshift/library-go/pkg/operator/events"

	"github.com/openshift/cluster-version-operator/lib/resourcemerge"
)

// controlPlaneInformerController is the controller that monitors health of the control plane-related resources
// and produces insights for control plane update.
type controlPlaneInformerController struct {
	clusterVersions  configv1listers.ClusterVersionLister
	clusterOperators configv1listers.ClusterOperatorLister
	recorder         events.Recorder

	// sendInsight should be called to send produced insights to the update status controller
	sendInsight sendInsightFn

	appsClient appsv1client.AppsV1Interface

	// now is a function that returns the current time, used for testing
	now func() metav1.Time
}

func newControlPlaneInformerController(
	appsClient appsv1client.AppsV1Interface,
	configInformers configinformers.SharedInformerFactory,
	recorder events.Recorder,
	sendInsight sendInsightFn,
) factory.Controller {
	cpiRecorder := recorder.WithComponentSuffix("control-plane-informer")

	c := &controlPlaneInformerController{
		clusterVersions:  configInformers.Config().V1().ClusterVersions().Lister(),
		clusterOperators: configInformers.Config().V1().ClusterOperators().Lister(),
		recorder:         cpiRecorder,
		sendInsight:      sendInsight,
		appsClient:       appsClient,

		now: metav1.Now,
	}

	cvInformer := configInformers.Config().V1().ClusterVersions().Informer()
	coInformer := configInformers.Config().V1().ClusterOperators().Informer()

	controller := factory.New().
		// call sync on ClusterVersion changes
		WithInformersQueueKeysFunc(configApiQueueKeys, cvInformer).
		// call sync on ClusterOperator changes with a filter
		WithFilteredEventsInformersQueueKeysFunc(configApiQueueKeys, clusterOperatorEventFilterFunc, coInformer).
		WithSync(c.sync).
		ToController("ControlPlaneInformer", c.recorder)

	return controller
}

func clusterOperatorEventFilterFunc(obj interface{}) bool {
	co, ok := obj.(*configv1.ClusterOperator)
	if ok {
		for annotation := range co.Annotations {
			if strings.HasPrefix(annotation, "exclude.release.openshift.io/") ||
				strings.HasPrefix(annotation, "include.release.openshift.io/") {
				return true
			}
		}
	}
	return false
}

const (
	clusterVersionKindName  = "ClusterVersion"
	clusterOperatorKindName = "ClusterOperator"
)

// sync is called for any controller event. It will assess the state and health of the control plane, indicated by
// the changed resource (ClusterVersion), produce insights, and send them to the update status controller. Status
// insights are not stored between calls, so every call produces a fresh insight. This means some fields do not follow
// conventions, like LastTransitionTime in the Updating condition. Proper continuous insight maintenance will need to
// be added later (not yet sure whether on consumer or producer side).
func (c *controlPlaneInformerController) sync(ctx context.Context, syncCtx factory.SyncContext) error {
	queueKey := syncCtx.QueueKey()

	t, name, err := parseQueueKey(queueKey)
	if err != nil {
		return fmt.Errorf("failed to parse queue key: %w", err)
	}

	var msg informerMsg
	switch t {
	case clusterVersionKindName:
		clusterVersion, err := c.clusterVersions.Get(name)
		if err != nil {
			if kerrors.IsNotFound(err) {
				// TODO: Handle deletes by deleting the status insight
				return nil
			}
			return err
		}

		now := c.now()
		insight := assessClusterVersion(clusterVersion, now)
		msg = makeInsightMsgForClusterVersion(insight, now)

	case clusterOperatorKindName:
		clusterVersion, err := c.clusterVersions.Get("version")
		if err != nil {
			return err
		}
		targetVersion := clusterVersion.Status.Desired.Version

		clusterOperator, err := c.clusterOperators.Get(name)
		if err != nil {
			if kerrors.IsNotFound(err) {
				// TODO: Handle deletes by deleting the status insight
				return nil
			}
			return err
		}

		now := c.now()
		insight, err := assessClusterOperator(ctx, clusterOperator, targetVersion, c.appsClient, now)
		if err != nil {
			return fmt.Errorf("failed to assess cluster operator %s: %w", name, err)
		}
		msg = makeInsightMsgForClusterOperator(insight, now)
	default:
		return fmt.Errorf("invalid queue key %s with unexpected type %s", queueKey, t)
	}
	var msgForLog string
	if klog.V(4).Enabled() {
		msgForLog = fmt.Sprintf(" | msg=%s", string(msg.insight))
	}
	klog.V(2).Infof("CPI :: Syncing %s %s%s", t, name, msgForLog)
	c.sendInsight(msg)

	return nil
}

func makeInsightMsgForClusterOperator(coInsight *ClusterOperatorStatusInsight, acquiredAt metav1.Time) informerMsg {
	uid := fmt.Sprintf("usc-co-%s", coInsight.Name)
	insight := Insight{
		UID:        uid,
		AcquiredAt: acquiredAt,
		InsightUnion: InsightUnion{
			Type:                         ClusterOperatorStatusInsightType,
			ClusterOperatorStatusInsight: coInsight,
		},
	}
	// Should handle errors, but ultimately we will have a proper API and won’t need to serialize ourselves
	rawInsight, _ := yaml.Marshal(insight)
	return informerMsg{
		uid:     uid,
		insight: rawInsight,
	}
}

func assessClusterOperator(ctx context.Context, operator *configv1.ClusterOperator, targetVersion string, appsClient appsv1client.AppsV1Interface, now metav1.Time) (*ClusterOperatorStatusInsight, error) {
	updating := metav1.Condition{
		Type:               string(ClusterOperatorStatusInsightUpdating),
		Status:             metav1.ConditionUnknown,
		Reason:             string(ClusterOperatorUpdatingCannotDetermine),
		LastTransitionTime: now,
	}

	imagePullSpec, err := getImagePullSpec(ctx, operator.Name, appsClient)
	if err != nil && !errors.Is(err, operatorImageNotImplemented) {
		return nil, err
	}

	noOperatorImageVersion := true
	var operatorImageUpdated, versionUpdated bool
	for _, version := range operator.Status.Versions {
		if version.Name == "operator-image" {
			noOperatorImageVersion = false
			if imagePullSpec != "" && imagePullSpec == version.Version {
				operatorImageUpdated = true
			}
		}
		if version.Name == "operator" && version.Version == targetVersion {
			versionUpdated = true
		}
	}

	// "operator-image" might not be implemented by every cluster operator
	updated := (noOperatorImageVersion || operatorImageUpdated) && versionUpdated
	if updated {
		updating.Status = metav1.ConditionFalse
		updating.Reason = string(ClusterOperatorUpdatingReasonUpdated)
	}

	var available *configv1.ClusterOperatorStatusCondition
	var degraded *configv1.ClusterOperatorStatusCondition
	var progressing *configv1.ClusterOperatorStatusCondition

	for _, condition := range operator.Status.Conditions {
		condition := condition
		switch {
		case condition.Type == configv1.OperatorAvailable:
			available = &condition
		case condition.Type == configv1.OperatorDegraded:
			degraded = &condition
		case condition.Type == configv1.OperatorProgressing:
			progressing = &condition
		}
	}

	if !updated && progressing != nil {
		if progressing.Status == configv1.ConditionTrue {
			updating.Status = metav1.ConditionTrue
			updating.Reason = string(ClusterOperatorUpdatingReasonProgressing)
			updating.Message = progressing.Message
		}
		if progressing.Status == configv1.ConditionFalse {
			updating.Status = metav1.ConditionFalse
			updating.Reason = string(ClusterOperatorUpdatingReasonPending)
			updating.Message = progressing.Message
		}
	}

	health := metav1.Condition{
		Type:               string(ClusterOperatorStatusInsightHealthy),
		Status:             metav1.ConditionTrue,
		Reason:             string(ClusterOperatorHealthyReasonAsExpected),
		LastTransitionTime: now,
	}

	if available == nil {
		health.Status = metav1.ConditionUnknown
		health.Reason = string(ClusterOperatorHealthyReasonUnavailable)
		health.Message = "The cluster operator is unavailable because the available condition is not found in the cluster operator's status"
	} else if available.Status != configv1.ConditionTrue {
		health.Status = metav1.ConditionFalse
		health.Reason = string(ClusterOperatorHealthyReasonUnavailable)
		health.Message = available.Message
	} else if degraded != nil && degraded.Status == configv1.ConditionTrue {
		health.Status = metav1.ConditionFalse
		health.Reason = string(ClusterOperatorHealthyReasonDegraded)
		health.Message = degraded.Message
	}

	return &ClusterOperatorStatusInsight{
		Name: operator.Name,
		Resource: ResourceRef{
			Resource: "clusteroperators",
			Group:    configv1.GroupName,
			Name:     operator.Name,
		},
		Conditions: []metav1.Condition{updating, health},
	}, nil
}

var operatorImageNotImplemented = errors.New("operator-image not implemented in the versions from cluster operator's status")

func getImagePullSpec(ctx context.Context, name string, appsClient appsv1client.AppsV1Interface) (string, error) {
	// It is known that the image pull spec for co/machine-config can be accessed from the deployment
	if name == "machine-config" {
		if appsClient == nil {
			return "", errors.New("apps client is nil")
		}
		mcoDeployment, err := appsClient.Deployments("openshift-machine-config-operator").Get(ctx, "machine-config-operator", metav1.GetOptions{})
		if err != nil {
			return "", err
		}
		for _, c := range mcoDeployment.Spec.Template.Spec.Containers {
			if c.Name == "machine-config-operator" {
				return c.Image, nil
			}
		}
		return "", errors.New("machine-config-operator container not found")
	}
	// We may add here retrieval of the image pull spec for other COs when they implement "operator-image" in the status.versions
	return "", operatorImageNotImplemented
}

// makeInsightMsgForClusterVersion creates an informerMsg for the given ClusterVersionStatusInsight. It defines an uid
// name and serializes the insight as YAML. Serialization is convenient because it prevents any data sharing issues
// between controllers.
func makeInsightMsgForClusterVersion(cvInsight *ClusterVersionStatusInsight, acquiredAt metav1.Time) informerMsg {
	uid := fmt.Sprintf("usc-cv-%s", cvInsight.Resource.Name)
	insight := Insight{
		UID:        uid,
		AcquiredAt: acquiredAt,
		InsightUnion: InsightUnion{
			Type:                        ClusterVersionStatusInsightType,
			ClusterVersionStatusInsight: cvInsight,
		},
	}
	// Should handle errors, but ultimately we will have a proper API and won’t need to serialize ourselves
	rawInsight, _ := yaml.Marshal(insight)
	return informerMsg{
		uid:     uid,
		insight: rawInsight,
	}
}

// assessClusterVersion produces a ClusterVersion status insight from the current state of the ClusterVersion resource.
// It does not take previous status insight into account. Many fields of the status insights (such as completion) cannot
// be properly calculated without also watching and processing ClusterOperators, so that functionality will need to be
// added later.
func assessClusterVersion(cv *configv1.ClusterVersion, now metav1.Time) *ClusterVersionStatusInsight {

	var lastHistoryItem *configv1.UpdateHistory
	if len(cv.Status.History) > 0 {
		lastHistoryItem = &cv.Status.History[0]
	}
	cvProgressing := resourcemerge.FindOperatorStatusCondition(cv.Status.Conditions, configv1.OperatorProgressing)

	updating, startedAt, completedAt := isControlPlaneUpdating(cvProgressing, lastHistoryItem)
	updating.LastTransitionTime = now

	klog.V(2).Infof("CPI :: CV/%s :: Updating=%s Started=%s Completed=%s", cv.Name, updating.Status, startedAt, completedAt)

	var assessment ControlPlaneAssessment
	var completion int32
	switch updating.Status {
	case metav1.ConditionTrue:
		assessment = ControlPlaneAssessmentProgressing
	case metav1.ConditionFalse:
		assessment = ControlPlaneAssessmentCompleted
		completion = 100
	case metav1.ConditionUnknown:
		assessment = ControlPlaneAssessmentUnknown
	default:
		assessment = ControlPlaneAssessmentUnknown
	}

	klog.V(2).Infof("CPI :: CV/%s :: Assessment=%s", cv.Name, assessment)

	insight := &ClusterVersionStatusInsight{
		Resource: ResourceRef{
			Resource: "clusterversions",
			Group:    configv1.GroupName,
			Name:     cv.Name,
		},
		Assessment: assessment,
		Versions:   versionsFromHistory(cv.Status.History),
		Completion: completion,
		StartedAt:  startedAt,
		Conditions: []metav1.Condition{updating},
	}

	if !completedAt.IsZero() {
		insight.CompletedAt = &completedAt
	}

	if est := estimateCompletion(startedAt.Time); !est.IsZero() {
		insight.EstimatedCompletedAt = &metav1.Time{Time: est}
	}

	return insight
}

// estimateCompletion returns a time.Time that is 60 minutes after the given time. Proper estimation needs to be added
// once the controller starts handling ClusterOperators.
func estimateCompletion(started time.Time) time.Time {
	return started.Add(60 * time.Minute)
}

// isControlPlaneUpdating determines whether the control plane is updating based on the ClusterVersion's Progressing
// condition and the last history item. It returns an updating condition, the time the update started, and the time the
// update completed. If the updating condition cannot be determined, the condition will have Status=Unknown and the
// Reason and Message fields will explain why.
func isControlPlaneUpdating(cvProgressing *configv1.ClusterOperatorStatusCondition, lastHistoryItem *configv1.UpdateHistory) (metav1.Condition, metav1.Time, metav1.Time) {
	updating := metav1.Condition{
		Type: string(ClusterVersionStatusInsightUpdating),
	}

	if cvProgressing == nil {
		setCannotDetermineUpdating(&updating, "No Progressing condition in ClusterVersion")
		return updating, metav1.Time{}, metav1.Time{}
	}
	if lastHistoryItem == nil {
		setCannotDetermineUpdating(&updating, "Empty history in ClusterVersion")
		return updating, metav1.Time{}, metav1.Time{}
	}

	updating.Status, updating.Reason, updating.Message = cvProgressingToUpdating(*cvProgressing)

	var started metav1.Time
	// Looks like we are updating
	if cvProgressing.Status == configv1.ConditionTrue {
		if lastHistoryItem.State != configv1.PartialUpdate {
			setCannotDetermineUpdating(&updating, "Progressing=True in ClusterVersion but last history item is not Partial")
		} else if lastHistoryItem.CompletionTime != nil {
			setCannotDetermineUpdating(&updating, "Progressing=True in ClusterVersion but last history item has completion time")
		} else {
			started = lastHistoryItem.StartedTime
		}
	}

	var completed metav1.Time
	// Looks like we are not updating
	if cvProgressing.Status == configv1.ConditionFalse {
		if lastHistoryItem.State != configv1.CompletedUpdate {
			setCannotDetermineUpdating(&updating, "Progressing=False in ClusterVersion but last history item is not completed")
		} else if lastHistoryItem.CompletionTime == nil {
			setCannotDetermineUpdating(&updating, "Progressing=False in ClusterVersion but not no completion in last history item")
		} else {
			started = lastHistoryItem.StartedTime
			completed = *lastHistoryItem.CompletionTime
		}
	}

	return updating, started, completed
}

func setCannotDetermineUpdating(cond *metav1.Condition, message string) {
	cond.Status = metav1.ConditionUnknown
	cond.Reason = string(ClusterVersionCannotDetermineUpdating)
	cond.Message = message
}

// cvProgressingToUpdating returns a status, reason and message for the updating condition based on the cvProgressing
// condition.
func cvProgressingToUpdating(cvProgressing configv1.ClusterOperatorStatusCondition) (metav1.ConditionStatus, string, string) {
	status := metav1.ConditionStatus(cvProgressing.Status)
	var reason string
	switch status {
	case metav1.ConditionTrue:
		reason = string(ClusterVersionProgressing)
	case metav1.ConditionFalse:
		reason = string(ClusterVersionNotProgressing)
	case metav1.ConditionUnknown:
		reason = string(ClusterVersionCannotDetermineUpdating)
	default:
		reason = string(ClusterVersionCannotDetermineUpdating)
	}

	message := fmt.Sprintf("ClusterVersion has Progressing=%s(Reason=%s) | Message='%s'", cvProgressing.Status, cvProgressing.Reason, cvProgressing.Message)
	return status, reason, message
}

// versionsFromHistory returns a ControlPlaneUpdateVersions struct with the target version and metadata from the given
// history.
func versionsFromHistory(history []configv1.UpdateHistory) ControlPlaneUpdateVersions {
	var versions ControlPlaneUpdateVersions

	if len(history) == 0 {
		return versions
	}

	versions.Target.Version = history[0].Version

	if len(history) == 1 {
		versions.Target.Metadata = []VersionMetadata{{Key: InstallationMetadata}}
	}
	if len(history) > 1 {
		versions.Previous.Version = history[1].Version
		if history[1].State == configv1.PartialUpdate {
			versions.Previous.Metadata = []VersionMetadata{{Key: PartialMetadata}}
		}
	}
	return versions
}

func parseQueueKey(queueKey string) (string, string, error) {
	splits := strings.Split(queueKey, "/")
	if len(splits) != 2 {
		return "", "", fmt.Errorf("invalid queue key: %s", queueKey)
	}
	return splits[0], splits[1], nil
}

func configApiQueueKeys(object runtime.Object) []string {
	if object == nil {
		return nil
	}

	switch o := object.(type) {
	case *configv1.ClusterVersion:
		return []string{fmt.Sprintf("%s/%s", clusterVersionKindName, o.Name)}
	case *configv1.ClusterOperator:
		return []string{fmt.Sprintf("%s/%s", clusterOperatorKindName, o.Name)}
	}

	msg := fmt.Sprintf("USC :: Unknown object type: %T", object)
	klog.Error(msg)
	panic(msg)
}

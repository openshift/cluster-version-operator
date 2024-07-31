package updatestatus

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	configv1 "github.com/openshift/api/config/v1"
	configv1alpha "github.com/openshift/api/config/v1alpha1"
	configv1informers "github.com/openshift/client-go/config/informers/externalversions"
	configv1listers "github.com/openshift/client-go/config/listers/config/v1"
	"github.com/openshift/cluster-version-operator/lib/resourcemerge"
	"github.com/openshift/library-go/pkg/controller/factory"
	"github.com/openshift/library-go/pkg/operator/events"
	"github.com/openshift/library-go/pkg/operator/v1helpers"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/klog/v2"
)

type versions struct {
	target            string
	previous          string
	isTargetInstall   bool
	isPreviousPartial bool
}

type controlPlaneUpdateStatus struct {
	updating *metav1.Condition
	versions versions

	operators map[string]metav1.Condition

	now func() metav1.Time
}

func (c *controlPlaneUpdateStatus) updateForClusterVersion(cv *configv1.ClusterVersion) ([]configv1alpha.UpdateInsight, error) {
	if c.updating == nil {
		c.updating = &metav1.Condition{
			Type: string(configv1alpha.UpdateProgressing),
		}
	}

	cvProgressing := resourcemerge.FindOperatorStatusCondition(cv.Status.Conditions, configv1.OperatorProgressing)

	versions, insights := versionsFromHistory(cv.Status.History, configv1alpha.ResourceRef{
		APIGroup: "config.openshift.io/v1",
		Kind:     "ClusterVersion",
		Name:     cv.Name,
	})

	c.versions = versions

	if cvProgressing == nil {
		c.updating.Status = metav1.ConditionUnknown
		c.updating.Reason = "ClusterVersionProgressingMissing"
		c.updating.Message = "ClusterVersion resource does not have a Progressing condition"
		c.updating.LastTransitionTime = c.now()
		return insights, nil
	}

	c.updating.Message = cvProgressing.Message
	c.updating.LastTransitionTime = cvProgressing.LastTransitionTime
	switch cvProgressing.Status {
	case configv1.ConditionTrue:
		c.updating.Status = metav1.ConditionTrue
		c.updating.Reason = "ClusterVersionProgressing"
		if len(cv.Status.History) > 0 {
			c.updating.LastTransitionTime = metav1.NewTime(cv.Status.History[0].StartedTime.Time)
		}
	case configv1.ConditionFalse:
		c.updating.Status = metav1.ConditionFalse
		c.updating.Reason = "ClusterVersionNotProgressing"
		if len(cv.Status.History) > 0 {
			c.updating.LastTransitionTime = metav1.NewTime(cv.Status.History[0].CompletionTime.Time)
		}
	case configv1.ConditionUnknown:
		c.updating.Status = metav1.ConditionUnknown
		c.updating.Reason = "ClusterVersionProgressingUnknown"
	}

	return insights, nil
}

func (c *controlPlaneUpdateStatus) updateForClusterOperator(co *configv1.ClusterOperator) ([]configv1alpha.UpdateInsight, error) {
	if c.operators == nil {
		c.operators = make(map[string]metav1.Condition)
	}

	version := v1helpers.FindOperandVersion(co.Status.Versions, "operator")
	if version == nil {
		c.operators[co.Name] = metav1.Condition{
			Type:               string(configv1alpha.UpdateProgressing),
			Status:             metav1.ConditionUnknown,
			LastTransitionTime: c.now(),
			Reason:             "ClusterOperatorVersionMissing",
			Message:            "ClusterOperator status is missing an operator version",
		}
		return nil, nil
	}

	if c.versions.target == "unknown" || c.versions.target == "" {
		c.operators[co.Name] = metav1.Condition{
			Type:               string(configv1alpha.UpdateProgressing),
			Status:             metav1.ConditionUnknown,
			LastTransitionTime: c.now(),
			Reason:             "ClusterVersionUnknown",
			Message:            "Unable to determine current cluster version",
		}
		return nil, nil
	}

	if version.Version == c.versions.target {
		c.operators[co.Name] = metav1.Condition{
			Type:   string(configv1alpha.UpdateProgressing),
			Status: metav1.ConditionFalse,
			// TODO: Do not overwrite times when a condition is already false
			LastTransitionTime: c.now(),
			Reason:             "Updated",
			Message:            fmt.Sprintf("Operator finished updating to %s", c.versions.target),
		}
		return nil, nil
	} else {
		progressing := resourcemerge.FindOperatorStatusCondition(co.Status.Conditions, configv1.OperatorProgressing)
		if progressing == nil || progressing.Status != configv1.ConditionTrue {
			c.operators[co.Name] = metav1.Condition{
				Type:               string(configv1alpha.UpdateProgressing),
				Status:             metav1.ConditionFalse,
				LastTransitionTime: c.updating.LastTransitionTime,
				Reason:             "Pending",
				Message:            fmt.Sprintf("Operator is pending an update to %s", c.versions.target),
			}
		} else {
			c.operators[co.Name] = metav1.Condition{
				Type:               string(configv1alpha.UpdateProgressing),
				Status:             metav1.ConditionTrue,
				LastTransitionTime: progressing.LastTransitionTime,
				Reason:             "Updating",
				Message:            fmt.Sprintf("Operator is updating to %s", c.versions.target),
			}
		}
	}

	return nil, nil
}

type controlPlaneUpdateInformer struct {
	clusterVersionLister  configv1listers.ClusterVersionLister
	clusterOperatorLister configv1listers.ClusterOperatorLister

	status controlPlaneUpdateStatus

	insightsLock sync.Mutex
	insights     []configv1alpha.UpdateInsight

	recorder events.Recorder
}

func queueKeys(obj runtime.Object) []string {
	if obj == nil {
		return nil
	}

	switch controlPlaneObj := obj.(type) {
	case *configv1.ClusterVersion:
		return []string{"cv/" + controlPlaneObj.Name}
	case *configv1.ClusterOperator:
		return []string{"co/" + controlPlaneObj.Name}
	}

	return nil
}

func newControlPlaneUpdateInformer(configInformers configv1informers.SharedInformerFactory, eventsRecorder events.Recorder) (factory.Controller, *controlPlaneUpdateInformer) {
	c := controlPlaneUpdateInformer{
		clusterVersionLister:  configInformers.Config().V1().ClusterVersions().Lister(),
		clusterOperatorLister: configInformers.Config().V1().ClusterOperators().Lister(),

		status: controlPlaneUpdateStatus{
			now: metav1.Now,
		},

		recorder: eventsRecorder,
	}

	return factory.New().WithInformersQueueKeysFunc(
		queueKeys,
		configInformers.Config().V1().ClusterVersions().Informer(),
		configInformers.Config().V1().ClusterOperators().Informer(),
	).ResyncEvery(10*time.Minute).
		WithSync(c.sync).
		ToController("ControlPlaneUpdateInformer", eventsRecorder.WithComponentSuffix("control-plane-update-informer")), &c
}

func versionsFromHistory(history []configv1.UpdateHistory, cvScope configv1alpha.ResourceRef) (versions, []configv1alpha.UpdateInsight) {
	versionData := versions{
		target:   "unknown",
		previous: "unknown",
	}

	if len(history) > 0 {
		versionData.target = history[0].Version
	} else {
		return versionData, nil
	}

	if len(history) == 1 {
		versionData.isTargetInstall = true
		versionData.previous = ""
		return versionData, nil
	}
	if len(history) > 1 {
		versionData.previous = history[1].Version
		versionData.isPreviousPartial = history[1].State == configv1.PartialUpdate
	}

	var insights []configv1alpha.UpdateInsight
	controlPlaneCompleted := history[0].State == configv1.CompletedUpdate
	if !controlPlaneCompleted && versionData.isPreviousPartial {
		lastComplete := "unknown"
		if len(history) > 2 {
			for _, item := range history[2:] {
				if item.State == configv1.CompletedUpdate {
					lastComplete = item.Version
					break
				}
			}
		}
		insights = []configv1alpha.UpdateInsight{
			{
				StartedAt: metav1.NewTime(history[0].StartedTime.Time),
				Scope: configv1alpha.UpdateInsightScope{
					Type:      configv1alpha.ScopeTypeControlPlane,
					Resources: []configv1alpha.ResourceRef{cvScope},
				},
				Impact: configv1alpha.UpdateInsightImpact{
					Level:       configv1alpha.WarningImpactLevel,
					Type:        configv1alpha.NoneImpactType,
					Summary:     fmt.Sprintf("Previous update to %s never completed, last complete update was %s", versionData.previous, lastComplete),
					Description: fmt.Sprintf("Current update to %s was initiated while the previous update to version %s was still in progress", versionData.target, versionData.previous),
				},
				Remediation: configv1alpha.UpdateInsightRemediation{
					Reference: "https://docs.openshift.com/container-platform/latest/updating/troubleshooting_updates/gathering-data-cluster-update.html#gathering-clusterversion-history-cli_troubleshooting_updates",
				},
			},
		}
	}

	return versionData, insights
}

func (c *controlPlaneUpdateInformer) sync(ctx context.Context, syncCtx factory.SyncContext) error {
	klog.Infof("Control Plane Update Informer :: SYNC :: %s", syncCtx.QueueKey())

	queueKey := syncCtx.QueueKey()

	if queueKey == factory.DefaultQueueKey {
		klog.Info("Control Plane Update Informer :: SYNC :: Full Relist")
		return nil
	}

	kindName := strings.Split(queueKey, "/")
	if len(kindName) != 2 {
		klog.Errorf("Control Plane Update Informer :: SYNC :: Invalid Queue Key %s", queueKey)
		return nil
	}

	kind := kindName[0]
	name := kindName[1]

	switch kind {
	case "cv":
		klog.Infof("Control Plane Update Informer :: SYNC :: ClusterVersion :: %s", name)
		cv, err := c.clusterVersionLister.Get(name)
		if err != nil {
			klog.Errorf("Control Plane Update Informer :: SYNC :: ClusterVersion :: %s :: %v", name, err)
			return nil
		}

		insights, err := c.status.updateForClusterVersion(cv)
		if err != nil {
			return err
		}

		// TODO: Merge instead of replace
		c.insightsLock.Lock()
		c.insights = nil
		c.insights = append(c.insights, insights...)
		c.insightsLock.Unlock()

	case "co":
		klog.Infof("Control Plane Update Informer :: SYNC :: ClusterOperator :: %s", name)
		_, err := c.clusterOperatorLister.Get(name)
		if err != nil {
			klog.Errorf("Control Plane Update Informer :: SYNC :: ClusterOperator :: %s :: %v", name, err)
			return nil
		}
	default:
		klog.Errorf("Control Plane Update Informer :: SYNC :: Invalid Kind %s", kind)
		return nil
	}

	return nil
}

func (c *controlPlaneUpdateInformer) getControlPlaneUpdateStatus() controlPlaneUpdateStatus {
	// TODO: Deepcopy (this emulates an remote scrape call)
	return controlPlaneUpdateStatus{
		versions: c.status.versions,
		updating: c.status.updating.DeepCopy(),
	}
}

func (c *controlPlaneUpdateInformer) getInsights() []configv1alpha.UpdateInsight {
	c.insightsLock.Lock()
	defer c.insightsLock.Unlock()

	var insights []configv1alpha.UpdateInsight
	for _, insight := range c.insights {
		insights = append(insights, *insight.DeepCopy())
	}

	return insights
}

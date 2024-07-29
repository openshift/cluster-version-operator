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
}

type controlPlaneUpdateInformer struct {
	clusterVersionLister  configv1listers.ClusterVersionLister
	clusterOperatorLister configv1listers.ClusterOperatorLister

	statusLock sync.Mutex
	status     controlPlaneUpdateStatus

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

func versionsFromHistory(history []configv1.UpdateHistory, cvScope configv1alpha.ResourceRef, controlPlaneCompleted bool) (versions, []configv1alpha.UpdateInsight) {
	versionData := versions{
		target:   "unknown",
		previous: "unknown",
	}
	if len(history) > 0 {
		versionData.target = history[0].Version
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

		cvProgressing := resourcemerge.FindOperatorStatusCondition(cv.Status.Conditions, configv1.OperatorProgressing)
		if cvProgressing == nil {
			klog.Errorf("Control Plane Update Informer :: SYNC :: ClusterVersion :: %s :: no Progressing condition", name)
			return nil
		}

		c.statusLock.Lock()
		defer c.statusLock.Unlock()

		versions, insights := versionsFromHistory(cv.Status.History, configv1alpha.ResourceRef{
			APIGroup: "config.openshift.io/v1",
			Kind:     "ClusterVersion",
			Name:     cv.Name,
		}, cvProgressing.Status == configv1.ConditionFalse)

		c.status.versions = versions

		// TODO: Merge instead of replace
		c.insightsLock.Lock()
		c.insights = nil
		c.insights = append(c.insights, insights...)
		c.insightsLock.Unlock()

		if c.status.updating == nil {
			c.status.updating = &metav1.Condition{
				Type: string(configv1alpha.UpdateProgressing),
			}
		}
		updating := c.status.updating

		if cvProgressing.Status == configv1.ConditionTrue {
			updating.Status = metav1.ConditionTrue
			updating.Reason = "ClusterVersionProgressing"
			updating.Message = cvProgressing.Message
			updating.LastTransitionTime = cvProgressing.LastTransitionTime
			if len(cv.Status.History) > 0 {
				updating.LastTransitionTime = metav1.NewTime(cv.Status.History[0].StartedTime.Time)
			}
		} else {
			updating.Status = metav1.ConditionFalse
			updating.Reason = "ClusterVersionNotProgressing"
			updating.Message = cvProgressing.Message
			updating.LastTransitionTime = cvProgressing.LastTransitionTime
		}

	case "co":
		klog.Infof("Control Plane Update Informer :: SYNC :: ClusterOperator :: %s (TODO)", name)
	default:
		klog.Errorf("Control Plane Update Informer :: SYNC :: Invalid Kind %s", kind)
		return nil
	}

	return nil
}

func (c *controlPlaneUpdateInformer) getControlPlaneUpdateStatus() controlPlaneUpdateStatus {
	c.statusLock.Lock()
	defer c.statusLock.Unlock()

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

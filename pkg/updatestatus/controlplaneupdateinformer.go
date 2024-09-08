package updatestatus

import (
	"context"
	"fmt"
	"strings"
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
	"k8s.io/klog/v2"
)

const (
	coUpdatingReasonVersionMissing = "ClusterOperatorVersionMissing"
	coUpdatingReasonCVUnknown      = "ClusterVersionUnknown"
	coUpdatingReasonPending        = "Pending"
	coUpdatingReasonUpdated        = "Updated"
	coUpdatingReasonUpdating       = "Updating"
)

type controlPlaneUpdateStatus struct {
	updating *metav1.Condition

	versions configv1alpha.ControlPlaneUpdateVersions

	operators map[string]metav1.Condition

	now func() metav1.Time
}

func (c *controlPlaneUpdateStatus) updateForClusterOperator(co *configv1.ClusterOperator) ([]configv1alpha.UpdateInsight, error) {
	if c.operators == nil {
		c.operators = make(map[string]metav1.Condition)
	}

	version := v1helpers.FindOperandVersion(co.Status.Versions, "operator")
	if version == nil {
		c.operators[co.Name] = metav1.Condition{
			Type:               string(configv1alpha.ControlPlaneConditionTypeUpdating),
			Status:             metav1.ConditionUnknown,
			LastTransitionTime: c.now(),
			Reason:             coUpdatingReasonVersionMissing,
			Message:            "ClusterOperator status is missing an operator version",
		}
		return nil, nil
	}

	if c.versions.Target == "unknown" || c.versions.Target == "" {
		c.operators[co.Name] = metav1.Condition{
			Type:               string(configv1alpha.ControlPlaneConditionTypeUpdating),
			Status:             metav1.ConditionUnknown,
			LastTransitionTime: c.now(),
			Reason:             coUpdatingReasonCVUnknown,
			Message:            "Unable to determine current cluster version",
		}
		return nil, nil
	}

	if version.Version == c.versions.Target {
		c.operators[co.Name] = metav1.Condition{
			Type:   string(configv1alpha.ControlPlaneConditionTypeUpdating),
			Status: metav1.ConditionFalse,
			// TODO: Do not overwrite times when a condition is already false
			LastTransitionTime: c.now(),
			Reason:             coUpdatingReasonUpdated,
			Message:            fmt.Sprintf("Operator finished updating to %s", c.versions.Target),
		}
		return nil, nil
	} else {
		progressing := resourcemerge.FindOperatorStatusCondition(co.Status.Conditions, configv1.OperatorProgressing)
		if progressing == nil || progressing.Status != configv1.ConditionTrue {

			c.operators[co.Name] = metav1.Condition{
				Type:               string(configv1alpha.ControlPlaneConditionTypeUpdating),
				Status:             metav1.ConditionFalse,
				LastTransitionTime: c.updating.LastTransitionTime,
				Reason:             coUpdatingReasonPending,
				Message:            fmt.Sprintf("Operator is pending an update to %s", c.versions.Target),
			}
		} else {
			c.operators[co.Name] = metav1.Condition{
				Type:               string(configv1alpha.ControlPlaneConditionTypeUpdating),
				Status:             metav1.ConditionTrue,
				LastTransitionTime: progressing.LastTransitionTime,
				Reason:             coUpdatingReasonUpdating,
				Message:            fmt.Sprintf("Operator is updating to %s", c.versions.Target),
			}
		}
	}

	return nil, nil
}

type controlPlaneUpdateInformer struct {
	clusterVersionLister  configv1listers.ClusterVersionLister
	clusterOperatorLister configv1listers.ClusterOperatorLister

	status controlPlaneUpdateStatus

	insights []configv1alpha.UpdateInsight

	recorder events.Recorder
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

	var insights []configv1alpha.UpdateInsight
	for _, insight := range c.insights {
		insights = append(insights, *insight.DeepCopy())
	}

	return insights
}

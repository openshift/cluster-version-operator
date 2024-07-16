package updatestatus

import (
	"context"
	"strings"
	"sync"
	"time"

	"github.com/barkimedes/go-deepcopy"
	configv1 "github.com/openshift/api/config/v1"
	configv1informers "github.com/openshift/client-go/config/informers/externalversions"
	configv1listers "github.com/openshift/client-go/config/listers/config/v1"
	"github.com/openshift/cluster-version-operator/lib/resourcemerge"
	"github.com/openshift/library-go/pkg/controller/factory"
	"github.com/openshift/library-go/pkg/operator/events"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/klog/v2"
)

type controlPlaneUpdateStatus struct {
	conditions []metav1.Condition
}

type controlPlaneUpdateInformer struct {
	clusterVersionLister  configv1listers.ClusterVersionLister
	clusterOperatorLister configv1listers.ClusterOperatorLister

	statusLock sync.Mutex
	status     controlPlaneUpdateStatus

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

func newControlPlaneUpdateInformer(configInformers configv1informers.SharedInformerFactory, eventsRecorder events.Recorder) (factory.Controller, func() controlPlaneUpdateStatus) {
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
		ToController("ControlPlaneUpdateInformer", eventsRecorder.WithComponentSuffix("control-plane-update-informer")), c.getControlPlaneUpdateStatus
}

func (c *controlPlaneUpdateInformer) sync(ctx context.Context, syncCtx factory.SyncContext) error {
	klog.Infof("Control Plane Update Informer :: SYNC :: %s", syncCtx.QueueKey())

	queueKey := syncCtx.QueueKey()

	if queueKey == factory.DefaultQueueKey {
		klog.Info("Control Plane Update Informer :: SYNC :: Full Relist")
		return nil
	}

	kindName := strings.Split("/", queueKey)
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
		progressing := meta.FindStatusCondition(c.status.conditions, "UpdateProgressing")
		if progressing == nil {
			progressing = &metav1.Condition{Type: "UpdateProgressing"}
			c.status.conditions = append(c.status.conditions, *progressing)
		}

		if cvProgressing.Status == configv1.ConditionTrue {
			progressing.Status = metav1.ConditionTrue
			progressing.Reason = "ClusterVersionProgressing"
			progressing.Message = cvProgressing.Message
			progressing.LastTransitionTime = cvProgressing.LastTransitionTime
			if len(cv.Status.History) > 0 {
				progressing.LastTransitionTime = metav1.NewTime(cv.Status.History[0].StartedTime.Time)
			}
		} else {
			progressing.Status = metav1.ConditionFalse
			progressing.Reason = "ClusterVersionNotProgressing"
			progressing.Message = cvProgressing.Message
			progressing.LastTransitionTime = cvProgressing.LastTransitionTime
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
	return deepcopy.MustAnything(c.status).(controlPlaneUpdateStatus)
}

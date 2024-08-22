package updatestatus

import (
	"context"
	"strings"

	"github.com/google/go-cmp/cmp"
	"github.com/openshift/cluster-version-operator/lib/resourcemerge"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/klog/v2"

	coreInformers "k8s.io/client-go/informers"
	corev1listers "k8s.io/client-go/listers/core/v1"

	configInformers "github.com/openshift/client-go/config/informers/externalversions"
	mcfgInformers "github.com/openshift/client-go/machineconfiguration/informers/externalversions"

	configv1listers "github.com/openshift/client-go/config/listers/config/v1"
	mcfgv1listers "github.com/openshift/client-go/machineconfiguration/listers/machineconfiguration/v1"

	configclientv1alpha1 "github.com/openshift/client-go/config/clientset/versioned/typed/config/v1alpha1"

	configv1 "github.com/openshift/api/config/v1"
	configv1alpha1 "github.com/openshift/api/config/v1alpha1"
	mcfgv1 "github.com/openshift/api/machineconfiguration/v1"

	"github.com/openshift/library-go/pkg/controller/factory"
	"github.com/openshift/library-go/pkg/operator/events"
)

type updateStatusController struct {
	recorder events.Recorder

	updateStatuses configclientv1alpha1.UpdateStatusInterface

	clusterVersions    configv1listers.ClusterVersionLister
	clusterOperators   configv1listers.ClusterOperatorLister
	machineConfigPools mcfgv1listers.MachineConfigPoolLister
	machineConfigs     mcfgv1listers.MachineConfigLister
	nodes              corev1listers.NodeLister
}

type queueKeyKind string

const (
	clusterVersionKey    queueKeyKind = "cv"
	clusterOperatorKey   queueKeyKind = "co"
	machineConfigPoolKey queueKeyKind = "mcp"
	machineConfigKey     queueKeyKind = "mc"
	nodeKey              queueKeyKind = "node"
)

func (c queueKeyKind) withName(name string) string {
	return string(c) + "/" + name
}

func kindName(queueKey string) (queueKeyKind, string) {
	items := strings.Split(queueKey, "/")
	if len(items) != 2 {
		klog.Fatalf("invalid queue key: %s", queueKey)
	}

	switch items[0] {
	case string(clusterVersionKey):
		return clusterVersionKey, items[1]
	case string(clusterOperatorKey):
		return clusterOperatorKey, items[1]
	case string(machineConfigPoolKey):
		return machineConfigPoolKey, items[1]
	case string(machineConfigKey):
		return machineConfigKey, items[1]
	case string(nodeKey):
		return nodeKey, items[1]
	}

	klog.Fatalf("unknown queue key kind: %s", items[0])
	return "", ""
}

func queueKeys(obj runtime.Object) []string {
	if obj == nil {
		return nil
	}

	switch o := obj.(type) {
	case *configv1.ClusterVersion:
		return []string{clusterVersionKey.withName(o.Name)}
	case *configv1.ClusterOperator:
		return []string{clusterOperatorKey.withName(o.Name)}
	case *mcfgv1.MachineConfigPool:
		return []string{machineConfigPoolKey.withName(o.Name)}
	case *mcfgv1.MachineConfig:
		return []string{machineConfigKey.withName(o.Name)}
	case *corev1.Node:
		return []string{nodeKey.withName(o.Name)}
	}

	klog.Fatalf("unknown object type: %T", obj)
	return nil
}

func newUpdateStatusController(
	updateStatusClient configclientv1alpha1.UpdateStatusInterface,

	configInformers configInformers.SharedInformerFactory,
	mcfgInformers mcfgInformers.SharedInformerFactory,
	coreInformers coreInformers.SharedInformerFactory,

	eventsRecorder events.Recorder,
) factory.Controller {
	c := updateStatusController{
		updateStatuses: updateStatusClient,

		clusterVersions: configInformers.Config().V1().ClusterVersions().Lister(),

		recorder: eventsRecorder,
	}
	controller := factory.New().WithInformersQueueKeysFunc(
		queueKeys,
		configInformers.Config().V1().ClusterVersions().Informer(),
		configInformers.Config().V1().ClusterOperators().Informer(),
		mcfgInformers.Machineconfiguration().V1().MachineConfigPools().Informer(),
		mcfgInformers.Machineconfiguration().V1().MachineConfigs().Informer(),
		coreInformers.Core().V1().Nodes().Informer(),
	).
		WithSync(c.sync).ToController("UpdateStatusController", eventsRecorder.WithComponentSuffix("update-status-controller"))
	return controller
}

func (c *updateStatusController) sync(ctx context.Context, syncCtx factory.SyncContext) error {
	queueKey := syncCtx.QueueKey()

	klog.Infof("USC :: SYNC :: syncCtx.QueueKey() = %s", queueKey)

	if queueKey == factory.DefaultQueueKey {
		klog.Info("USC :: SYNC :: Full Relist")
		return nil
	}

	original, err := c.updateStatuses.Get(ctx, "cluster", metav1.GetOptions{})
	if err != nil {
		if errors.IsNotFound(err) {
			original = &configv1alpha1.UpdateStatus{ObjectMeta: metav1.ObjectMeta{Name: "cluster"}}
			original, err = c.updateStatuses.Create(ctx, original, metav1.CreateOptions{})
			if err != nil {
				klog.Fatalf("USC :: SYNC :: Failed to create UpdateStatus: %v", err)
			}
		} else {
			klog.Fatalf("USC :: SYNC :: Failed to get UpdateStatus: %v", err)
		}
	}

	kind, name := kindName(queueKey)

	cpStatus := &original.Status.ControlPlane
	var newCpStatus *configv1alpha1.ControlPlaneUpdateStatus

	switch kind {
	case clusterVersionKey:
		cv, err := c.clusterVersions.Get(name)
		if err != nil {
			klog.Fatalf("USC :: SYNC :: Failed to get ClusterVersion %s: %v", name, err)
		}

		newCpStatus = cpStatus.DeepCopy()
		updateStatusForClusterVersion(newCpStatus, cv)
	case clusterOperatorKey:
	case machineConfigPoolKey:
	case machineConfigKey:
	case nodeKey:
	}

	if newCpStatus != nil && cmp.Diff(cpStatus, newCpStatus) != "" {
		us := original.DeepCopy()
		us.Status.ControlPlane = *newCpStatus
		_, err = c.updateStatuses.UpdateStatus(ctx, original, metav1.UpdateOptions{})
		if err != nil {
			klog.Fatalf("USC :: SYNC :: Failed to update UpdateStatus: %v", err)
		}
	}

	return nil
}

const prototypeInformerName = "ota-1268-prototype"

func versionsFromHistory(history []configv1.UpdateHistory) configv1alpha1.ControlPlaneUpdateVersions {
	versionData := configv1alpha1.ControlPlaneUpdateVersions{
		Target:   "unknown",
		Previous: "unknown",
	}

	if len(history) > 0 {
		versionData.Target = history[0].Version
	} else {
		return versionData
	}

	if len(history) == 1 {
		versionData.IsTargetInstall = true
		versionData.Previous = ""
		return versionData
	}
	if len(history) > 1 {
		versionData.Previous = history[1].Version
		versionData.IsPreviousPartial = history[1].State == configv1.PartialUpdate
	}

	// var insights []configv1alpha.UpdateInsight
	// controlPlaneCompleted := history[0].State == configv1.CompletedUpdate
	// if !controlPlaneCompleted && versionData.isPreviousPartial {
	// 	lastComplete := "unknown"
	// 	if len(history) > 2 {
	// 		for _, item := range history[2:] {
	// 			if item.State == configv1.CompletedUpdate {
	// 				lastComplete = item.Version
	// 				break
	// 			}
	// 		}
	// 	}
	// 	insights = []configv1alpha.UpdateInsight{
	// 		{
	// 			StartedAt: metav1.NewTime(history[0].StartedTime.Time),
	// 			Scope: configv1alpha.UpdateInsightScope{
	// 				Type:      configv1alpha.ScopeTypeControlPlane,
	// 				Resources: []configv1alpha.ResourceRef{cvScope},
	// 			},
	// 			Impact: configv1alpha.UpdateInsightImpact{
	// 				Level:       configv1alpha.WarningImpactLevel,
	// 				Type:        configv1alpha.NoneImpactType,
	// 				Summary:     fmt.Sprintf("Previous update to %s never completed, last complete update was %s", versionData.previous, lastComplete),
	// 				Description: fmt.Sprintf("Current update to %s was initiated while the previous update to version %s was still in progress", versionData.target, versionData.previous),
	// 			},
	// 			Remediation: configv1alpha.UpdateInsightRemediation{
	// 				Reference: "https://docs.openshift.com/container-platform/latest/updating/troubleshooting_updates/gathering-data-cluster-update.html#gathering-clusterversion-history-cli_troubleshooting_updates",
	// 			},
	// 		},
	// 	}
	// }

	return versionData
}

func updateStatusForClusterVersion(cpStatus *configv1alpha1.ControlPlaneUpdateStatus, cv *configv1.ClusterVersion) {
	cpUpdatingType := string(configv1alpha1.ControlPlaneConditionTypeUpdating)

	prototypeInformer := findUpdateInformer(cpStatus.Informers, prototypeInformerName)
	if prototypeInformer == nil {
		cpStatus.Informers = append(cpStatus.Informers, configv1alpha1.UpdateInformer{Name: prototypeInformerName})
		prototypeInformer = &cpStatus.Informers[len(cpStatus.Informers)-1]
	}

	cvInsight := findClusterVersionInsight(prototypeInformer.Insights, cv)
	if cvInsight == nil {
		cvInsight = &configv1alpha1.ClusterVersionStatusInsight{
			Resource: configv1alpha1.ResourceRef{
				Name:     cv.Name,
				Kind:     "ClusterVersion",
				APIGroup: "config.openshift.io",
			},
		}
		prototypeInformer.Insights = append(prototypeInformer.Insights, configv1alpha1.UpdateInsight{
			Type:                        configv1alpha1.UpdateInsightTypeClusterVersionStatusInsight,
			ClusterVersionStatusInsight: cvInsight,
		})
		cvInsight = prototypeInformer.Insights[len(prototypeInformer.Insights)-1].ClusterVersionStatusInsight
	}

	cvInsightUpdatingType := string(configv1alpha1.ClusterVersionStatusInsightConditionTypeUpdating)

	cvInsight.Versions = versionsFromHistory(cv.Status.History)

	if len(cv.Status.History) > 0 {
		cvInsight.StartedAt = metav1.NewTime(cv.Status.History[0].StartedTime.Time)
		cvInsight.CompletedAt = metav1.NewTime(cv.Status.History[0].CompletionTime.Time)
	}

	cvProgressing := resourcemerge.FindOperatorStatusCondition(cv.Status.Conditions, configv1.OperatorProgressing)

	// Create one from the other (CP insight from CV insight)
	cpUpdatingCondition := metav1.Condition{Type: cpUpdatingType}
	cvInsightUpdating := metav1.Condition{Type: cvInsightUpdatingType}

	if cvProgressing == nil {
		cpUpdatingCondition.Status = metav1.ConditionUnknown
		cpUpdatingCondition.Reason = string(configv1alpha1.ControlPlaneConditionUpdatingReasonClusterVersionWithoutProgressing)
		cpUpdatingCondition.Message = "ClusterVersion does not have a Progressing condition"

		cvInsightUpdating.Status = metav1.ConditionUnknown
		cvInsightUpdating.Reason = string(configv1alpha1.ClusterVersionStatusInsightUpdatingReasonNoProgressing)
		cvInsightUpdating.Message = "ClusterVersion does not have a Progressing condition"

		cvInsight.Assessment = configv1alpha1.ControlPlaneUpdateAssessmentDegraded
	} else {
		cpUpdatingCondition.Message = cvProgressing.Message
		cpUpdatingCondition.LastTransitionTime = cvProgressing.LastTransitionTime

		cvInsightUpdating.Message = cvProgressing.Message
		cvInsightUpdating.LastTransitionTime = cvProgressing.LastTransitionTime

		switch cvProgressing.Status {
		case configv1.ConditionTrue:
			cpUpdatingCondition.Status = metav1.ConditionTrue
			cpUpdatingCondition.Reason = string(configv1alpha1.ControlPlaneConditionUpdatingReasonClusterVersionProgressing)

			cvInsightUpdating.Status = metav1.ConditionTrue
			cvInsightUpdating.Reason = cvProgressing.Reason

			cvInsight.Assessment = configv1alpha1.ControlPlaneUpdateAssessmentProgressing

		case configv1.ConditionFalse:
			cpUpdatingCondition.Status = metav1.ConditionFalse
			cpUpdatingCondition.Reason = string(configv1alpha1.ControlPlaneConditionUpdatingReasonClusterVersionNotProgressing)

			cvInsightUpdating.Status = metav1.ConditionFalse
			cvInsightUpdating.Reason = cvProgressing.Reason

			cvInsight.Assessment = configv1alpha1.ControlPlaneUpdateAssessmentProgressing

		case configv1.ConditionUnknown:
			cpUpdatingCondition.Status = metav1.ConditionUnknown
			cpUpdatingCondition.Reason = string(configv1alpha1.ControlPlaneConditionUpdatingReasonClusterVersionProgressingUnknown)

			cvInsightUpdating.Status = metav1.ConditionUnknown
			cvInsightUpdating.Reason = cvProgressing.Reason

			cvInsight.Assessment = configv1alpha1.ControlPlaneUpdateAssessmentDegraded
		}

		if cvInsight.StartedAt.IsZero() {
			cpUpdatingCondition.LastTransitionTime = cvProgressing.LastTransitionTime
			cvInsightUpdating.LastTransitionTime = cvProgressing.LastTransitionTime
		} else {
			cpUpdatingCondition.LastTransitionTime = cvInsight.StartedAt
			cvInsightUpdating.LastTransitionTime = cvInsight.StartedAt
		}
	}

	meta.SetStatusCondition(&cpStatus.Conditions, cpUpdatingCondition)
	meta.SetStatusCondition(&cvInsight.Conditions, cvInsightUpdating)
}

func findClusterVersionInsight(insights []configv1alpha1.UpdateInsight, version *configv1.ClusterVersion) *configv1alpha1.ClusterVersionStatusInsight {
	for i := range insights {
		if insights[i].Type == configv1alpha1.UpdateInsightTypeClusterVersionStatusInsight {
			cvInsight := insights[i].ClusterVersionStatusInsight
			if cvInsight.Resource.Name != version.Name {
				klog.Fatalf("USC :: SYNC :: ClusterVersionStatusInsight for a wrong ClusterVersion: %s", cvInsight.Resource.Name)
			}
			return cvInsight
		}
	}

	return nil
}

func findUpdateInformer(informers []configv1alpha1.UpdateInformer, name string) *configv1alpha1.UpdateInformer {
	for i := range informers {
		if informers[i].Name == name {
			return &informers[i]
		}
	}
	return nil
}

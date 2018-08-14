package cvo

import (
	"fmt"
	"time"

	"github.com/golang/glog"
	"github.com/openshift/cluster-version-operator/lib/resourceapply"
	"github.com/openshift/cluster-version-operator/pkg/apis"
	"github.com/openshift/cluster-version-operator/pkg/apis/clusterversion.openshift.io/v1"
	"github.com/openshift/cluster-version-operator/pkg/version"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apiextv1beta1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1beta1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/utils/pointer"
)

func (optr *Operator) syncCVODeploy(config *v1.CVOConfig) error {
	if config.DesiredUpdate.Payload == "" || config.DesiredUpdate.Version == "" || config.DesiredUpdate.Version == version.Version.String() {
		glog.V(4).Info("no update to be applied.")
		return nil
	}
	cvo := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      optr.name,
			Namespace: optr.namespace,
			Labels: map[string]string{
				"k8s-app": optr.name,
			},
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: pointer.Int32Ptr(1),
			Strategy: appsv1.DeploymentStrategy{
				Type: appsv1.RollingUpdateDeploymentStrategyType,
				RollingUpdate: &appsv1.RollingUpdateDeployment{
					MaxUnavailable: intOrStringPtr(intstr.FromInt(0)),
					MaxSurge:       intOrStringPtr(intstr.FromInt(1)),
				},
			},
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{
					"k8s-app": optr.name,
				},
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						"k8s-app": optr.name,
					},
				},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{{
						Name:  optr.name,
						Image: config.DesiredUpdate.Payload,
					}},
					NodeSelector: map[string]string{
						"node-role.kubernetes.io/master": "",
					},
					Tolerations: []corev1.Toleration{{
						Key: "node-role.kubernetes.io/master",
					}},
				},
			},
		},
	}

	glog.V(4).Infof("Updating CVO to %s...", config.DesiredUpdate.Version)
	_, updated, err := resourceapply.ApplyDeploymentFromCache(optr.deployLister, optr.kubeClient.AppsV1(), cvo)
	if err != nil {
		return err
	}
	if updated {
		if err := optr.waitForDeploymentRollout(cvo); err != nil {
			return err
		}
	}
	return nil
}

func (optr *Operator) syncCVOCRDs() error {
	crds := []*apiextv1beta1.CustomResourceDefinition{{
		ObjectMeta: metav1.ObjectMeta{
			Name:      fmt.Sprintf("operatorstatuses.%s", apis.GroupName),
			Namespace: metav1.NamespaceDefault,
		},
		Spec: apiextv1beta1.CustomResourceDefinitionSpec{
			Group:   apis.GroupName,
			Version: "v1",
			Scope:   "Namespaced",
			Names: apiextv1beta1.CustomResourceDefinitionNames{
				Plural:   "operatorstatuses",
				Singular: "operatorstatus",
				Kind:     "OperatorStatus",
				ListKind: "OperatorStatusList",
			},
		},
	}, {
		ObjectMeta: metav1.ObjectMeta{
			Name:      fmt.Sprintf("cvoconfigs.%s", apis.GroupName),
			Namespace: metav1.NamespaceDefault,
		},
		Spec: apiextv1beta1.CustomResourceDefinitionSpec{
			Group:   apis.GroupName,
			Version: "v1",
			Scope:   "Namespaced",
			Names: apiextv1beta1.CustomResourceDefinitionNames{
				Plural:   "cvoconfigs",
				Singular: "cvoconfig",
				Kind:     "CVOConfig",
				ListKind: "CVOConfigList",
			},
		},
	}}

	for _, crd := range crds {
		_, updated, err := resourceapply.ApplyCustomResourceDefinitionFromCache(optr.crdLister, optr.apiExtClient.ApiextensionsV1beta1(), crd)
		if err != nil {
			return err
		}
		if updated {
			if err := optr.waitForCustomResourceDefinition(crd); err != nil {
				return err
			}
		}
	}
	return nil
}

const (
	deploymentRolloutPollInterval = time.Second
	deploymentRolloutTimeout      = 5 * time.Minute
	customResourceReadyInterval   = time.Second
	customResourceReadyTimeout    = 1 * time.Minute
)

func (optr *Operator) waitForCustomResourceDefinition(resource *apiextv1beta1.CustomResourceDefinition) error {
	return wait.Poll(customResourceReadyInterval, customResourceReadyTimeout, func() (bool, error) {
		crd, err := optr.crdLister.Get(resource.Name)
		if errors.IsNotFound(err) {
			// exit early to recreate the crd.
			return false, err
		}
		if err != nil {
			glog.Errorf("error getting CustomResourceDefinition %s: %v", resource.Name, err)
			return false, nil
		}

		for _, condition := range crd.Status.Conditions {
			if condition.Type == apiextv1beta1.Established && condition.Status == apiextv1beta1.ConditionTrue {
				return true, nil
			}
		}
		glog.V(4).Infof("CustomResourceDefinition %s is not ready. conditions: %v", crd.Name, crd.Status.Conditions)
		return false, nil
	})
}

func (optr *Operator) waitForDeploymentRollout(resource *appsv1.Deployment) error {
	return wait.Poll(deploymentRolloutPollInterval, deploymentRolloutTimeout, func() (bool, error) {
		d, err := optr.deployLister.Deployments(resource.Namespace).Get(resource.Name)
		if errors.IsNotFound(err) {
			// exit early to recreate the deployment.
			return false, err
		}
		if err != nil {
			// Do not return error here, as we could be updating the API Server itself, in which case we
			// want to continue waiting.
			glog.Errorf("error getting Deployment %s during rollout: %v", resource.Name, err)
			return false, nil
		}

		if d.DeletionTimestamp != nil {
			return false, fmt.Errorf("Deployment %s is being deleted", resource.Name)
		}

		if d.Generation <= d.Status.ObservedGeneration && d.Status.UpdatedReplicas == d.Status.Replicas && d.Status.UnavailableReplicas == 0 {
			return true, nil
		}
		glog.V(4).Infof("Deployment %s is not ready. status: (replicas: %d, updated: %d, ready: %d, unavailable: %d)", d.Name, d.Status.Replicas, d.Status.UpdatedReplicas, d.Status.ReadyReplicas, d.Status.UnavailableReplicas)
		return false, nil
	})
}

func intOrStringPtr(v intstr.IntOrString) *intstr.IntOrString {
	return &v
}

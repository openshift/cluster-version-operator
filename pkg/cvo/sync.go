package cvo

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/golang/glog"
	"github.com/openshift/cluster-version-operator/lib"
	"github.com/openshift/cluster-version-operator/lib/resourceapply"
	"github.com/openshift/cluster-version-operator/lib/resourcebuilder"
	"github.com/openshift/cluster-version-operator/pkg/apis"
	"github.com/openshift/cluster-version-operator/pkg/apis/clusterversion.openshift.io/v1"
	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	apiextv1beta1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1beta1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	randutil "k8s.io/apimachinery/pkg/util/rand"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/utils/pointer"
)

func (optr *Operator) syncUpdatePayload(config *v1.CVOConfig, payload [][]lib.Manifest) error {
	for _, manifests := range payload {
		for _, manifest := range manifests {
			glog.V(4).Infof("Running sync for (%s) %s/%s", manifest.GVK.String(), manifest.Object().GetNamespace(), manifest.Object().GetName())
			b, err := resourcebuilder.New(optr.restConfig, manifest)
			if err != nil {
				return err
			}
			if err := b.Do(ownerRefModifier(config)); err != nil {
				return err
			}
			glog.V(4).Infof("Done syncing for (%s) %s/%s", manifest.GVK.String(), manifest.Object().GetNamespace(), manifest.Object().GetName())
		}
	}
	return nil
}

func ownerRefModifier(config *v1.CVOConfig) resourcebuilder.MetaV1ObjectModifierFunc {
	oref := metav1.NewControllerRef(config, ownerKind)
	return func(obj metav1.Object) {
		obj.SetOwnerReferences([]metav1.OwnerReference{*oref})
	}
}

const (
	updatePayloadsPathPrefix = "/etc/cvo/update-payloads"
)

func (optr *Operator) syncUpdatePayloadContents(pathprefix string, config *v1.CVOConfig) ([][]lib.Manifest, error) {
	if !updatedesired(config.DesiredUpdate) {
		return nil, nil
	}
	dv := config.DesiredUpdate.Version
	dp := config.DesiredUpdate.Payload

	dirpath := filepath.Join(pathprefix, dv)
	_, err := os.Stat(dirpath)
	if err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	if os.IsNotExist(err) {
		if err := optr.fetchUpdatePayloadToPath(pathprefix, config); err != nil {
			return nil, err
		}
	}

	// read dirpath to manifests:
	// For each operator in payload the manifests should be ordered as:
	// 1. CRDs
	// 2. others
	mmap, err := lib.LoadManifests(dirpath)
	if err != nil {
		return nil, err
	}

	if len(mmap) == 0 {
		return nil, fmt.Errorf("empty update payload %s", dp)
	}

	sortedkeys := []string{}
	for k := range mmap {
		sortedkeys = append(sortedkeys, k)
	}
	sort.Strings(sortedkeys)

	payload := [][]lib.Manifest{}
	for _, k := range sortedkeys {
		ordered := orderManifests(mmap[k])
		payload = append(payload, ordered)
	}
	return payload, nil
}

func (optr *Operator) fetchUpdatePayloadToPath(pathprefix string, config *v1.CVOConfig) error {
	if !updatedesired(config.DesiredUpdate) {
		return fmt.Errorf("error DesiredUpdate is empty")
	}
	var (
		version         = config.DesiredUpdate.Version
		payload         = config.DesiredUpdate.Payload
		name            = fmt.Sprintf("%s-%s-%s", optr.name, version, randutil.String(5))
		namespace       = optr.namespace
		deadline        = pointer.Int64Ptr(2 * 60)
		nodeSelectorKey = "node-role.kubernetes.io/master"
		nodename        = optr.nodename
		vpath           = filepath.Join(pathprefix, version)
		cmd             = []string{"/bin/sh"}
		args            = []string{"-c", fmt.Sprintf("mv /manifests %s", vpath)}
	)

	job := &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		Spec: batchv1.JobSpec{
			ActiveDeadlineSeconds: deadline,
			Template: corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{{
						Name:    "payload",
						Image:   payload,
						Command: cmd,
						Args:    args,
						VolumeMounts: []corev1.VolumeMount{{
							MountPath: pathprefix,
							Name:      "payload",
						}},
					}},
					Volumes: []corev1.Volume{{
						Name: "payload",
						VolumeSource: corev1.VolumeSource{
							HostPath: &corev1.HostPathVolumeSource{
								Path: pathprefix,
							},
						},
					}},
					NodeName: nodename,
					NodeSelector: map[string]string{
						nodeSelectorKey: "",
					},
					Tolerations: []corev1.Toleration{{
						Key: nodeSelectorKey,
					}},
					RestartPolicy: corev1.RestartPolicyOnFailure,
				},
			},
		},
	}

	_, err := optr.kubeClient.BatchV1().Jobs(job.Namespace).Create(job)
	if err != nil {
		return err
	}
	return resourcebuilder.WaitForJobCompletion(optr.kubeClient.BatchV1(), job)
}

func orderManifests(manifests []lib.Manifest) []lib.Manifest {
	var crds, others []lib.Manifest
	for _, m := range manifests {
		group, version, kind := m.GVK.Group, m.GVK.Version, m.GVK.Kind
		switch {
		case group == apiextv1beta1.SchemeGroupVersion.Group && version == apiextv1beta1.SchemeGroupVersion.Version && kind == resourcebuilder.CRDKind:
			crds = append(crds, m)
		default:
			others = append(others, m)
		}
	}

	out := []lib.Manifest{}
	out = append(out, crds...)
	out = append(out, others...)
	return out
}

func updatedesired(desired v1.Update) bool {
	return desired.Payload != "" &&
		desired.Version != ""
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

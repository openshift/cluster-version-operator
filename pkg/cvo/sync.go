package cvo

import (
	"fmt"
	"time"

	"github.com/golang/glog"
	apiextv1beta1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1beta1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"

	"github.com/openshift/cluster-version-operator/lib"
	"github.com/openshift/cluster-version-operator/lib/resourceapply"
	"github.com/openshift/cluster-version-operator/lib/resourcebuilder"
	"github.com/openshift/cluster-version-operator/pkg/apis"
	cvv1 "github.com/openshift/cluster-version-operator/pkg/apis/clusterversion.openshift.io/v1"
	"github.com/openshift/cluster-version-operator/pkg/cvo/internal"
)

func (optr *Operator) syncUpdatePayload(config *cvv1.CVOConfig, payload *updatePayload) error {
	for _, manifest := range payload.manifests {
		taskName := fmt.Sprintf("(%s) %s/%s", manifest.GVK.String(), manifest.Object().GetNamespace(), manifest.Object().GetName())
		glog.V(4).Infof("Running sync for %s", taskName)
		glog.V(6).Infof("Manifest: %s", string(manifest.Raw))

		ov, ok := getOverrideForManifest(config.Overrides, manifest)
		if ok && ov.Unmanaged {
			glog.V(4).Infof("Skipping %s as unmanaged", taskName)
			continue
		}

		if err := wait.ExponentialBackoff(wait.Backoff{
			Duration: time.Second * 10,
			Factor:   1.3,
			Steps:    3,
		}, func() (bool, error) {
			// build resource builder for manifest
			var b resourcebuilder.Interface
			var err error
			if resourcebuilder.Mapper.Exists(manifest.GVK) {
				b, err = resourcebuilder.New(resourcebuilder.Mapper, optr.restConfig, manifest)
			} else {
				b, err = internal.NewGenericBuilder(optr.restConfig, manifest)
			}
			if err != nil {
				glog.Errorf("error creating resourcebuilder for %s: %v", taskName, err)
				return false, nil
			}
			// run builder for the manifest
			if err := b.Do(); err != nil {
				glog.Errorf("error running apply for %s: %v", taskName, err)
				return false, nil
			}
			return true, nil
		}); err != nil {
			return fmt.Errorf("timed out trying to apply %s", taskName)
		}

		glog.V(4).Infof("Done syncing for %s", taskName)
	}
	return nil
}

// getOverrideForManifest returns the override and true when override exists for manifest.
func getOverrideForManifest(overrides []cvv1.ComponentOverride, manifest lib.Manifest) (cvv1.ComponentOverride, bool) {
	for idx, ov := range overrides {
		kind, namespace, name := manifest.GVK.Kind, manifest.Object().GetNamespace(), manifest.Object().GetName()
		if ov.Kind == kind &&
			(namespace == "" || ov.Namespace == namespace) && // cluster-scoped objects don't have namespace.
			ov.Name == name {
			return overrides[idx], true
		}
	}
	return cvv1.ComponentOverride{}, false
}

func ownerRefModifier(config *cvv1.CVOConfig) resourcebuilder.MetaV1ObjectModifierFunc {
	oref := metav1.NewControllerRef(config, ownerKind)
	return func(obj metav1.Object) {
		obj.SetOwnerReferences([]metav1.OwnerReference{*oref})
	}
}

func (optr *Operator) syncCustomResourceDefinitions() error {
	crds := []*apiextv1beta1.CustomResourceDefinition{
		{
			ObjectMeta: metav1.ObjectMeta{
				Name:      fmt.Sprintf("operatorstatuses.%s", apis.OperatorStatusGroupName),
				Namespace: metav1.NamespaceDefault,
			},
			Spec: apiextv1beta1.CustomResourceDefinitionSpec{
				Group:   apis.OperatorStatusGroupName,
				Version: "v1",
				Scope:   "Namespaced",
				Names: apiextv1beta1.CustomResourceDefinitionNames{
					Plural:   "operatorstatuses",
					Singular: "operatorstatus",
					Kind:     "OperatorStatus",
					ListKind: "OperatorStatusList",
				},
			},
		},
		{
			ObjectMeta: metav1.ObjectMeta{
				Name:      fmt.Sprintf("clusteroperators.%s", apis.OperatorStatusGroupName),
				Namespace: metav1.NamespaceDefault,
			},
			Spec: apiextv1beta1.CustomResourceDefinitionSpec{
				Group:   apis.OperatorStatusGroupName,
				Version: "v1",
				Scope:   "Namespaced",
				Names: apiextv1beta1.CustomResourceDefinitionNames{
					Plural:   "clusteroperators",
					Singular: "clusteroperator",
					Kind:     "ClusterOperator",
					ListKind: "ClusterOperatorList",
				},
			},
		},
	}

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
	customResourceReadyInterval = time.Second
	customResourceReadyTimeout  = 1 * time.Minute
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

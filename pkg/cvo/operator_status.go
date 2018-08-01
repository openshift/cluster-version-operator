package cvo

import (
	"fmt"

	"github.com/openshift/cluster-version-operator/pkg/apis"

	"github.com/golang/glog"
	"k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1beta1"
	"k8s.io/apiextensions-apiserver/pkg/client/clientset/clientset"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// XXX: this needs to ensure that the CRD is correct (e.g. hasn't been modified
//      by the user) rather than just ensuring it exists.
func ensureCRDsExist(apiExtClient clientset.Interface) {
	_, err := apiExtClient.ApiextensionsV1beta1().CustomResourceDefinitions().Create(&v1beta1.CustomResourceDefinition{
		ObjectMeta: metav1.ObjectMeta{
			Name:      fmt.Sprintf("operatorstatuses.%s", apis.GroupName),
			Namespace: metav1.NamespaceDefault,
		},
		Spec: v1beta1.CustomResourceDefinitionSpec{
			Group:   apis.GroupName,
			Version: "v1",
			Scope:   "Namespaced",
			Names: v1beta1.CustomResourceDefinitionNames{
				Plural:   "operatorstatuses",
				Singular: "operatorstatus",
				Kind:     "OperatorStatus",
				ListKind: "OperatorStatusList",
			},
		},
	})
	if err != nil && !errors.IsAlreadyExists(err) {
		glog.Errorf("Failed to create OperatorStatus CRD: %v", err)
		return
	}

	_, err = apiExtClient.ApiextensionsV1beta1().CustomResourceDefinitions().Create(&v1beta1.CustomResourceDefinition{
		ObjectMeta: metav1.ObjectMeta{
			Name:      fmt.Sprintf("cvoconfigs.%s", apis.GroupName),
			Namespace: metav1.NamespaceDefault,
		},
		Spec: v1beta1.CustomResourceDefinitionSpec{
			Group:   apis.GroupName,
			Version: "v1",
			Scope:   "Namespaced",
			Names: v1beta1.CustomResourceDefinitionNames{
				Plural:   "cvoconfigs",
				Singular: "cvoconfig",
				Kind:     "CVOConfig",
				ListKind: "CVOConfigList",
			},
		},
	})
	if err != nil && !errors.IsAlreadyExists(err) {
		glog.Errorf("Failed to create CVOConfig CRD: %v", err)
		return
	}
}

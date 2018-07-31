package cvo

import (
	"fmt"
	"time"

	"github.com/openshift/cluster-version-operator/pkg/apis"
	"github.com/openshift/cluster-version-operator/pkg/apis/clusterversion.openshift.io/v1"
	"github.com/openshift/cluster-version-operator/pkg/cincinnati"
	"github.com/openshift/cluster-version-operator/pkg/generated/clientset/versioned"
	"github.com/openshift/cluster-version-operator/pkg/version"

	"github.com/blang/semver"
	"github.com/golang/glog"
	"k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1beta1"
	"k8s.io/apiextensions-apiserver/pkg/client/clientset/clientset"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/rest"
)

const (
	// updateCheckPeriod is the amount of time (minus jitter) between update
	// checks. Jitter will vary between 0% and 100% of updateCheckPeriod.
	updateCheckPeriod = time.Minute

	// customResourceName is the name of the CVO's OperatorStatus.
	customResourceName = "cluster-version-operator"

	// XXX: namespace is the hardcoded namespace that will be used for the
	// CVO's OperatorStatus.
	namespace = "kube-system"
)

// StartWorkers starts the Cluster Version Operator's reconciliation loops.
// These loops are responsible for the following:
//   - ensuring the OperatorStatus CRD exists and is correct
//   - checking for updates and writing the available updates into the CVO's
//     OperatorStatus.
func StartWorkers(stopCh <-chan struct{}, config *rest.Config, cinClient cincinnati.Client) {
	apiExtClient, err := clientset.NewForConfig(config)
	if err != nil {
		glog.Errorf("Failed to create Kubernetes client (API Extensions): %v", err)
		return
	}

	cvoClient, err := versioned.NewForConfig(config)
	if err != nil {
		glog.Errorf("Failed to create Kubernetes client: %v", err)
		return
	}

	go wait.Until(ensureOperatorStatusExists(apiExtClient), updateCheckPeriod, stopCh)
	go wait.Until(checkForUpdate(cvoClient, &cinClient), updateCheckPeriod, stopCh)
}

func ensureOperatorStatusExists(apiExtClient clientset.Interface) func() {
	return func() {
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
	}
}

func checkForUpdate(cvoClient versioned.Interface, cinClient *cincinnati.Client) func() {
	return func() {
		payloads, err := cinClient.GetUpdatePayloads("http://localhost:8080/graph", semver.MustParse("1.8.9-tectonic.3"), "fast")
		if err != nil {
			glog.Errorf("Failed to check for update: %v", err)
			return
		}
		glog.V(4).Infof("Found update payloads: %v", payloads)

		if updateStatus(cvoClient, payloads) != nil {
			glog.Errorf("Failed to update OperatorStatus for ClusterVersionOperator")
		}
	}
}

func updateStatus(cvoClient versioned.Interface, payloads []string) error {
	status, err := cvoClient.ClusterversionV1().OperatorStatuses(namespace).Get(customResourceName, metav1.GetOptions{})
	if err != nil && !errors.IsNotFound(err) {
		glog.Errorf("Failed to get custom resource: %v", err)
		return err
	}

	if errors.IsNotFound(err) {
		status = &v1.OperatorStatus{
			ObjectMeta: metav1.ObjectMeta{
				Name: customResourceName,
			},
			Condition: v1.OperatorStatusCondition{
				Type: v1.OperatorStatusConditionTypeDone,
			},
			Version:    version.Raw,
			LastUpdate: metav1.Now(),
			Extension: runtime.RawExtension{
				Object: &v1.CVOStatus{
					AvailablePayloads: payloads,
				},
			},
		}

		_, err = cvoClient.ClusterversionV1().OperatorStatuses(namespace).Create(status)
		if err != nil {
			glog.Errorf("Failed to create custom resource: %v", err)
			return err
		}
	} else {
		status.Version = version.Raw
		status.LastUpdate = metav1.Now()
		status.Extension.Object = &v1.CVOStatus{
			AvailablePayloads: payloads,
		}

		_, err = cvoClient.ClusterversionV1().OperatorStatuses(namespace).Update(status)
		if err != nil {
			glog.Errorf("Failed to update custom resource: %v", err)
			return err
		}
	}

	return nil
}

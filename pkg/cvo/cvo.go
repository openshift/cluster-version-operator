package cvo

import (
	"time"

	"github.com/openshift/cluster-version-operator/pkg/generated/clientset/versioned"

	"github.com/golang/glog"
	"k8s.io/apiextensions-apiserver/pkg/client/clientset/clientset"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/rest"
)

const (
	// updateCheckPeriod is the amount of time (minus jitter) between update
	// checks. Jitter will vary between 0% and 100% of updateCheckPeriod.
	updateCheckPeriod = time.Minute

	// customResourceName is the name of the CVO's OperatorStatus and the
	// CVOConfig.
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
func StartWorkers(stopCh <-chan struct{}, config *rest.Config) {
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

	// Create the CRDs before attempting to run the reconciliation loops.
	ensureCRDsExist(apiExtClient)

	go wait.Until(func() { ensureCRDsExist(apiExtClient) }, updateCheckPeriod, stopCh)
	go wait.Until(func() { checkForUpdate(cvoClient) }, updateCheckPeriod, stopCh)
}

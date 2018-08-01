package cvo

import (
	"github.com/openshift/cluster-version-operator/pkg/apis/clusterversion.openshift.io/v1"
	"github.com/openshift/cluster-version-operator/pkg/cincinnati"
	"github.com/openshift/cluster-version-operator/pkg/generated/clientset/versioned"
	"github.com/openshift/cluster-version-operator/pkg/version"

	"github.com/blang/semver"
	"github.com/golang/glog"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

func checkForUpdate(cvoClient versioned.Interface, cinClient *cincinnati.Client) {
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

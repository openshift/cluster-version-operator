package cvo

import (
	"fmt"

	"github.com/openshift/cluster-version-operator/pkg/apis/clusterversion.openshift.io/v1"
	"github.com/openshift/cluster-version-operator/pkg/cincinnati"
	"github.com/openshift/cluster-version-operator/pkg/generated/clientset/versioned"
	"github.com/openshift/cluster-version-operator/pkg/version"

	"github.com/golang/glog"
	"github.com/google/uuid"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

var (
	defaultUpstream = v1.URL("http://localhost:8080/graph")
	defaultChannel  = "fast"
)

func checkForUpdate(cvoClient versioned.Interface) {
	config, err := getConfig(cvoClient)
	if err != nil {
		glog.Errorf("Failed to get CVO config: %v", err)
		return
	}
	glog.V(4).Infof("Found CVO config: %s", config)

	updates, err := cincinnati.NewClient(config.ClusterID).GetUpdates(string(config.Upstream), config.Channel, version.Version)
	if err != nil {
		glog.Errorf("Failed to check for update: %v", err)
		return
	}
	glog.V(4).Infof("Found available updates: %v", updates)

	if updateStatus(cvoClient, updates) != nil {
		glog.Errorf("Failed to update OperatorStatus for ClusterVersionOperator")
	}
}

func getConfig(cvoClient versioned.Interface) (v1.CVOConfig, error) {
	config, err := cvoClient.ClusterversionV1().CVOConfigs(namespace).Get(customResourceName, metav1.GetOptions{})
	if err != nil && !errors.IsNotFound(err) {
		glog.Errorf("Failed to get custom resource: %v", err)
		return v1.CVOConfig{}, err
	}

	if errors.IsNotFound(err) {
		glog.Infof("No CVO config found. Generating a new one...")

		id, err := uuid.NewRandom()
		if err != nil {
			glog.Errorf("Failed to generate new cluster identifier: %v", err)
			return v1.CVOConfig{}, err
		}

		config = &v1.CVOConfig{
			ObjectMeta: metav1.ObjectMeta{
				Name: customResourceName,
			},
			Upstream:  defaultUpstream,
			Channel:   defaultChannel,
			ClusterID: id,
		}

		_, err = cvoClient.ClusterversionV1().CVOConfigs(namespace).Create(config)
		if err != nil {
			glog.Errorf("Failed to create custom resource: %v", err)
			return v1.CVOConfig{}, err
		}
	}

	if config.ClusterID.Variant() != uuid.RFC4122 {
		return v1.CVOConfig{}, fmt.Errorf("invalid ClusterID %q, must be an RFC4122-variant UUID: found %s", config.ClusterID, config.ClusterID.Variant())
	}
	if config.ClusterID.Version() != 4 {
		return v1.CVOConfig{}, fmt.Errorf("Invalid ClusterID %q, must be a version-4 UUID: found %s", config.ClusterID, config.ClusterID.Version())
	}

	return *config, nil
}

func updateStatus(cvoClient versioned.Interface, updates []cincinnati.Update) error {
	status, err := cvoClient.ClusterversionV1().OperatorStatuses(namespace).Get(customResourceName, metav1.GetOptions{})
	if err != nil && !errors.IsNotFound(err) {
		glog.Errorf("Failed to get custom resource: %v", err)
		return err
	}

	var avUpdates []v1.AvailableUpdate
	for _, update := range updates {
		avUpdates = append(avUpdates, v1.AvailableUpdate{
			Version: update.Version.String(),
			Payload: update.Payload,
		})
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
					AvailableUpdates: avUpdates,
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
		// The Raw member of runtime.RawExtension needs to be set to nil in
		// order for Object to be considered when marshalling. Otherwise, it's
		// assumed that Raw should be used.
		status.Extension.Raw = nil
		status.Extension.Object = &v1.CVOStatus{
			AvailableUpdates: avUpdates,
		}

		_, err = cvoClient.ClusterversionV1().OperatorStatuses(namespace).Update(status)
		if err != nil {
			glog.Errorf("Failed to update custom resource: %v", err)
			return err
		}
	}

	return nil
}

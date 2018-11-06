package cvo

import (
	"fmt"
	"time"

	"github.com/golang/glog"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"

	"github.com/openshift/cluster-version-operator/lib"
	"github.com/openshift/cluster-version-operator/lib/resourcebuilder"
	cvv1 "github.com/openshift/cluster-version-operator/pkg/apis/config.openshift.io/v1"
	"github.com/openshift/cluster-version-operator/pkg/cvo/internal"
)

// loadUpdatePayload reads the payload from disk or remote, as necessary.
func (optr *Operator) loadUpdatePayload(config *cvv1.ClusterVersion) (*updatePayload, error) {
	payloadDir, err := optr.updatePayloadDir(config)
	if err != nil {
		return nil, err
	}
	releaseImage := optr.releaseImage
	if config.Spec.DesiredUpdate != nil {
		releaseImage = config.Spec.DesiredUpdate.Payload
	}
	return loadUpdatePayload(payloadDir, releaseImage)
}

// syncUpdatePayload applies the manifests in the payload to the cluster.
func (optr *Operator) syncUpdatePayload(config *cvv1.ClusterVersion, payload *updatePayload) error {
	for _, manifest := range payload.manifests {
		taskName := fmt.Sprintf("(%s) %s/%s", manifest.GVK.String(), manifest.Object().GetNamespace(), manifest.Object().GetName())
		glog.V(4).Infof("Running sync for %s", taskName)
		glog.V(6).Infof("Manifest: %s", string(manifest.Raw))

		ov, ok := getOverrideForManifest(config.Spec.Overrides, manifest)
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

func ownerRefModifier(config *cvv1.ClusterVersion) resourcebuilder.MetaV1ObjectModifierFunc {
	oref := metav1.NewControllerRef(config, ownerKind)
	return func(obj metav1.Object) {
		obj.SetOwnerReferences([]metav1.OwnerReference{*oref})
	}
}

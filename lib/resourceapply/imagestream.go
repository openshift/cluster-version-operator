package resourceapply

import (
	"context"

	"github.com/google/go-cmp/cmp"

	imagev1 "github.com/openshift/api/image/v1"
	imageclientv1 "github.com/openshift/client-go/image/clientset/versioned/typed/image/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/klog/v2"
	"k8s.io/utils/ptr"

	"github.com/openshift/cluster-version-operator/lib/resourcemerge"
)

func ApplyImageStreamv1(ctx context.Context, client imageclientv1.ImageStreamsGetter, required *imagev1.ImageStream, reconciling bool) (*imagev1.ImageStream, bool, error) {
	existing, err := client.ImageStreams(required.Namespace).Get(ctx, required.Name, metav1.GetOptions{})
	if errors.IsNotFound(err) {
		actual, err := client.ImageStreams(required.Namespace).Create(ctx, required, metav1.CreateOptions{})
		return actual, true, err
	}
	if err != nil {
		return nil, false, err
	}
	// if we only create this resource, we have no need to continue further
	if IsCreateOnly(required) {
		return nil, false, nil
	}

	var original imagev1.ImageStream
	existing.DeepCopyInto(&original)
	modified := ptr.To(false)
	resourcemerge.EnsureImagestreamv1(modified, existing, *required)
	if !*modified {
		return existing, false, nil
	}
	if reconciling {
		if diff := cmp.Diff(&original, existing); diff != "" {
			klog.V(2).Infof("Updating ImageStream %s/%s due to diff: %v", required.Namespace, required.Name, diff)
		} else {
			klog.V(2).Infof("Updating ImageStream %s/%s with empty diff: possible hotloop after wrong comparison", required.Namespace, required.Name)
		}
	}

	actual, err := client.ImageStreams(required.Namespace).Update(ctx, existing, metav1.UpdateOptions{})
	return actual, true, err
}

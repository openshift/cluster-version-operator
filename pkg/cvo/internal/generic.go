package internal

import (
	"context"
	"fmt"

	"github.com/google/go-cmp/cmp"
	"github.com/openshift/cluster-version-operator/lib/resourceapply"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/client-go/dynamic"
	"k8s.io/klog/v2"

	"github.com/openshift/client-go/config/clientset/versioned/scheme"
	"github.com/openshift/cluster-version-operator/lib/resourcebuilder"
	"github.com/openshift/cluster-version-operator/lib/resourcedelete"
	"github.com/openshift/library-go/pkg/manifest"
)

// readUnstructuredV1OrDie reads operatorstatus object from bytes. Panics on error.
func readUnstructuredV1OrDie(objBytes []byte) *unstructured.Unstructured {
	udi, _, err := scheme.Codecs.UniversalDecoder().Decode(objBytes, nil, &unstructured.Unstructured{})
	if err != nil {
		panic(err)
	}
	return udi.(*unstructured.Unstructured)
}

func deleteUnstructured(ctx context.Context, client dynamic.ResourceInterface, required *unstructured.Unstructured,
	updateMode bool) (bool, error) {

	if required.GetName() == "" {
		return false, fmt.Errorf("Error running delete, invalid object: name cannot be empty")
	}
	if delAnnoFound, err := resourcedelete.ValidDeleteAnnotation(required.GetAnnotations()); !delAnnoFound || err != nil {
		return delAnnoFound, err
	}
	existing, err := client.Get(ctx, required.GetName(), metav1.GetOptions{})
	resource := resourcedelete.Resource{
		Kind:      required.GetKind(),
		Namespace: required.GetNamespace(),
		Name:      required.GetName(),
	}
	if deleteRequested, err := resourcedelete.GetDeleteProgress(resource, err); err == nil {
		// Only request deletion when in update mode.
		if !deleteRequested && updateMode {
			if err := client.Delete(ctx, required.GetName(), metav1.DeleteOptions{}); err != nil {
				return true, fmt.Errorf("Delete request for %s failed, err=%v", resource, err)
			}
			resourcedelete.SetDeleteRequested(existing, resource)
		}
	} else {
		return true, fmt.Errorf("Error running delete for %s, err=%v", resource, err)
	}
	return true, nil
}

func applyUnstructured(ctx context.Context, client dynamic.ResourceInterface, required *unstructured.Unstructured, reconciling bool) (*unstructured.Unstructured, bool, error) {
	if required.GetName() == "" {
		return nil, false, fmt.Errorf("invalid object: name cannot be empty")
	}
	existing, err := client.Get(ctx, required.GetName(), metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		klog.V(2).Infof("%s %s/%s not found, creating", required.GetKind(), required.GetNamespace(), required.GetName())
		actual, err := client.Create(ctx, required, metav1.CreateOptions{})
		return actual, true, err
	}
	if err != nil {
		return nil, false, err
	}
	// if we only create this resource, we have no need to continue further
	if resourceapply.IsCreateOnly(required) {
		return nil, false, nil
	}

	skipKeys := sets.NewString("apiVersion", "kind", "metadata", "status")

	// create a copy of required, but copy skipKeys from existing
	// this would copy skipKeys data into expected from existing
	expected := required.DeepCopy()
	for k, v := range existing.Object {
		if skipKeys.Has(k) {
			expected.Object[k] = v
		}
	}

	objDiff := cmp.Diff(expected, existing)
	if objDiff == "" {
		// Skip update, as no changes found
		return existing, false, nil
	}

	// copy all keys from required in existing except skipKeys
	for k, v := range required.Object {
		if skipKeys.Has(k) {
			continue
		}
		existing.Object[k] = v
	}

	existing.SetAnnotations(required.GetAnnotations())
	existing.SetLabels(required.GetLabels())
	existing.SetOwnerReferences(required.GetOwnerReferences())

	if reconciling {
		klog.V(4).Infof("Updating %s %s/%s due to diff: %v", required.GetKind(), required.GetNamespace(), required.GetName(), objDiff)
	}

	actual, err := client.Update(ctx, existing, metav1.UpdateOptions{})
	if err != nil {
		return nil, false, err
	}
	return actual, existing.GetResourceVersion() != actual.GetResourceVersion(), nil
}

type genericBuilder struct {
	client   dynamic.ResourceInterface
	raw      []byte
	mode     resourcebuilder.Mode
	modifier resourcebuilder.MetaV1ObjectModifierFunc
}

// NewGenericBuilder returns an implementation of resourcebuilder.Interface that
// uses dynamic clients for applying.
func NewGenericBuilder(client dynamic.ResourceInterface, m manifest.Manifest) (resourcebuilder.Interface, error) {
	return &genericBuilder{
		client: client,
		raw:    m.Raw,
	}, nil
}

func (b *genericBuilder) WithMode(m resourcebuilder.Mode) resourcebuilder.Interface {
	b.mode = m
	return b
}

func (b *genericBuilder) WithModifier(f resourcebuilder.MetaV1ObjectModifierFunc) resourcebuilder.Interface {
	b.modifier = f
	return b
}

func (b *genericBuilder) Do(ctx context.Context) error {
	reconciling := b.mode == resourcebuilder.ReconcilingMode
	ud := readUnstructuredV1OrDie(b.raw)
	if b.modifier != nil {
		b.modifier(ud)
	}

	updatingMode := (b.mode == resourcebuilder.UpdatingMode)
	deleteReq, err := deleteUnstructured(ctx, b.client, ud, updatingMode)
	if err != nil {
		return err
	} else if !deleteReq {
		_, _, err = applyUnstructured(ctx, b.client, ud, reconciling)
	}
	return err
}

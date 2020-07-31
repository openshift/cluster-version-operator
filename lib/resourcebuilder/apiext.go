package resourcebuilder

import (
	"context"
	"fmt"

	"github.com/openshift/cluster-version-operator/lib"
	"github.com/openshift/cluster-version-operator/lib/resourceapply"
	"github.com/openshift/cluster-version-operator/lib/resourceread"
	apiextv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	apiextv1beta1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1beta1"
	apiextclientv1 "k8s.io/apiextensions-apiserver/pkg/client/clientset/clientset/typed/apiextensions/v1"
	apiextclientv1beta1 "k8s.io/apiextensions-apiserver/pkg/client/clientset/clientset/typed/apiextensions/v1beta1"
	"k8s.io/client-go/rest"
)

type crdBuilder struct {
	raw           []byte
	modifier      MetaV1ObjectModifierFunc
	clientV1beta1 *apiextclientv1beta1.ApiextensionsV1beta1Client
	clientV1      *apiextclientv1.ApiextensionsV1Client
}

func newCRDBuilder(config *rest.Config, m lib.Manifest) Interface {
	return &crdBuilder{
		raw:           m.Raw,
		clientV1beta1: apiextclientv1beta1.NewForConfigOrDie(withProtobuf(config)),
		clientV1:      apiextclientv1.NewForConfigOrDie(withProtobuf(config)),
	}
}

func (b *crdBuilder) WithMode(m Mode) Interface {
	return b
}

func (b *crdBuilder) WithModifier(f MetaV1ObjectModifierFunc) Interface {
	b.modifier = f
	return b
}

func (b *crdBuilder) Do(ctx context.Context) error {
	crd := resourceread.ReadCustomResourceDefinitionOrDie(b.raw)

	switch typedCRD := crd.(type) {
	case *apiextv1beta1.CustomResourceDefinition:
		if b.modifier != nil {
			b.modifier(typedCRD)
		}
		if _, _, err := resourceapply.ApplyCustomResourceDefinitionV1beta1(ctx, b.clientV1beta1, typedCRD); err != nil {
			return err
		}
	case *apiextv1.CustomResourceDefinition:
		if b.modifier != nil {
			b.modifier(typedCRD)
		}
		if _, _, err := resourceapply.ApplyCustomResourceDefinitionV1(ctx, b.clientV1, typedCRD); err != nil {
			return err
		}
	default:
		return fmt.Errorf("unrecognized CustomResourceDefinition version: %T", crd)
	}

	return nil
}

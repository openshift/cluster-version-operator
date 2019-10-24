package resourcebuilder

import (
	"context"

	"k8s.io/klog"

	"github.com/openshift/cluster-version-operator/lib"
	"github.com/openshift/cluster-version-operator/lib/resourceapply"
	"github.com/openshift/cluster-version-operator/lib/resourceread"
	apiextv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	apiextv1beta1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1beta1"
	apiextclientv1 "k8s.io/apiextensions-apiserver/pkg/client/clientset/clientset/typed/apiextensions/v1"
	apiextclientv1beta1 "k8s.io/apiextensions-apiserver/pkg/client/clientset/clientset/typed/apiextensions/v1beta1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
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

	var updated bool
	var err error
	var name string

	switch crd := crd.(type) {
	case *apiextv1beta1.CustomResourceDefinition:
		if b.modifier != nil {
			b.modifier(crd)
		}
		_, updated, err = resourceapply.ApplyCustomResourceDefinitionV1beta1(b.clientV1beta1, crd)
		if err != nil {
			return err
		}
		name = crd.Name
	case *apiextv1.CustomResourceDefinition:
		if b.modifier != nil {
			b.modifier(crd)
		}
		_, updated, err = resourceapply.ApplyCustomResourceDefinitionV1(b.clientV1, crd)
		if err != nil {
			return err
		}
		name = crd.Name
	}

	if updated {
		return waitForCustomResourceDefinitionCompletion(ctx, b.clientV1beta1, name)
	}
	return nil
}

func waitForCustomResourceDefinitionCompletion(ctx context.Context, client apiextclientv1beta1.CustomResourceDefinitionsGetter, crd string) error {
	return wait.PollImmediateUntil(defaultObjectPollInterval, func() (bool, error) {
		c, err := client.CustomResourceDefinitions().Get(crd, metav1.GetOptions{})
		if errors.IsNotFound(err) {
			// exit early to recreate the crd.
			return false, err
		}
		if err != nil {
			klog.Errorf("error getting CustomResourceDefinition %s: %v", crd, err)
			return false, nil
		}

		for _, condition := range c.Status.Conditions {
			if condition.Type == apiextv1beta1.Established && condition.Status == apiextv1beta1.ConditionTrue {
				return true, nil
			}
		}
		klog.V(4).Infof("CustomResourceDefinition %s is not ready. conditions: %v", c.Name, c.Status.Conditions)
		return false, nil
	}, ctx.Done())
}

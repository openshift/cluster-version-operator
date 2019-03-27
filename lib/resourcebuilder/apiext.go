package resourcebuilder

import (
	"context"

	"github.com/golang/glog"

	"github.com/openshift/cluster-version-operator/lib"
	"github.com/openshift/cluster-version-operator/lib/resourceapply"
	"github.com/openshift/cluster-version-operator/lib/resourceread"
	apiextv1beta1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1beta1"
	apiextclientv1beta1 "k8s.io/apiextensions-apiserver/pkg/client/clientset/clientset/typed/apiextensions/v1beta1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/rest"
)

type crdBuilder struct {
	client   *apiextclientv1beta1.ApiextensionsV1beta1Client
	raw      []byte
	modifier MetaV1ObjectModifierFunc
}

func newCRDBuilder(config *rest.Config, m lib.Manifest) Interface {
	return &crdBuilder{
		client: apiextclientv1beta1.NewForConfigOrDie(withProtobuf(config)),
		raw:    m.Raw,
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
	crd := resourceread.ReadCustomResourceDefinitionV1Beta1OrDie(b.raw)
	if b.modifier != nil {
		b.modifier(crd)
	}
	_, updated, err := resourceapply.ApplyCustomResourceDefinition(b.client, crd)
	if err != nil {
		return err
	}
	if updated {
		return waitForCustomResourceDefinitionCompletion(ctx, b.client, crd)
	}
	return nil
}

func waitForCustomResourceDefinitionCompletion(ctx context.Context, client apiextclientv1beta1.CustomResourceDefinitionsGetter, crd *apiextv1beta1.CustomResourceDefinition) error {
	return wait.PollImmediateUntil(defaultObjectPollInterval, func() (bool, error) {
		c, err := client.CustomResourceDefinitions().Get(crd.Name, metav1.GetOptions{})
		if errors.IsNotFound(err) {
			// exit early to recreate the crd.
			return false, err
		}
		if err != nil {
			glog.Errorf("error getting CustomResourceDefinition %s: %v", crd.Name, err)
			return false, nil
		}

		for _, condition := range c.Status.Conditions {
			if condition.Type == apiextv1beta1.Established && condition.Status == apiextv1beta1.ConditionTrue {
				return true, nil
			}
		}
		glog.V(4).Infof("CustomResourceDefinition %s is not ready. conditions: %v", c.Name, c.Status.Conditions)
		return false, nil
	}, ctx.Done())
}

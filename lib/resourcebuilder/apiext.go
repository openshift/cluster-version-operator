package resourcebuilder

import (
	"fmt"
	"time"

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

func newAPIExtBuilder(config *rest.Config, m lib.Manifest) (Interface, error) {
	kind := m.GVK.Kind
	switch kind {
	case CRDKind:
		return newCRDBuilder(config, m), nil
	default:
		return nil, fmt.Errorf("no APIExt builder found for %s", kind)
	}
}

const (
	// CRDKind is kind for CustomResourceDefinitions
	CRDKind = "CustomResourceDefinition"
)

type crdBuilder struct {
	client *apiextclientv1beta1.ApiextensionsV1beta1Client
	raw    []byte
}

func newCRDBuilder(config *rest.Config, m lib.Manifest) *crdBuilder {
	return &crdBuilder{
		client: apiextclientv1beta1.NewForConfigOrDie(config),
		raw:    m.Raw,
	}
}

func (b *crdBuilder) Do(modifier MetaV1ObjectModifierFunc) error {
	crd := resourceread.ReadCustomResourceDefinitionV1Beta1OrDie(b.raw)

	modifier(crd)

	_, updated, err := resourceapply.ApplyCustomResourceDefinition(b.client, crd)
	if err != nil {
		return err
	}
	if updated {
		return waitForCustomResourceDefinitionCompletion(b.client, crd)
	}
	return nil
}

const (
	crdPollInterval = 1 * time.Second
	crdPollTimeout  = 1 * time.Minute
)

func waitForCustomResourceDefinitionCompletion(client apiextclientv1beta1.CustomResourceDefinitionsGetter, crd *apiextv1beta1.CustomResourceDefinition) error {
	return wait.Poll(crdPollInterval, crdPollTimeout, func() (bool, error) {
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
	})
}

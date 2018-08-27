package resourcebuilder

import (
	"fmt"

	"github.com/openshift/cluster-version-operator/lib"
	"github.com/openshift/cluster-version-operator/lib/resourceapply"
	"github.com/openshift/cluster-version-operator/lib/resourceread"
	coreclientv1 "k8s.io/client-go/kubernetes/typed/core/v1"
	"k8s.io/client-go/rest"
)

func newCoreBuilder(config *rest.Config, m lib.Manifest) (Interface, error) {
	kind := m.GVK.Kind
	switch kind {
	case serviceAccountKind:
		return newServiceAccountBuilder(config, m), nil
	case configMapKind:
		return newConfigMapBuilder(config, m), nil
	default:
		return nil, fmt.Errorf("no Core builder found for %s", kind)
	}
}

const (
	serviceAccountKind = "ServiceAccount"
)

type serviceAccountBuilder struct {
	client *coreclientv1.CoreV1Client
	raw    []byte
}

func newServiceAccountBuilder(config *rest.Config, m lib.Manifest) *serviceAccountBuilder {
	return &serviceAccountBuilder{
		client: coreclientv1.NewForConfigOrDie(config),
		raw:    m.Raw,
	}
}

func (b *serviceAccountBuilder) Do(modifier MetaV1ObjectModifierFunc) error {
	serviceAccount := resourceread.ReadServiceAccountV1OrDie(b.raw)
	modifier(serviceAccount)
	_, _, err := resourceapply.ApplyServiceAccount(b.client, serviceAccount)
	return err
}

const (
	configMapKind = "ConfigMap"
)

type configMapBuilder struct {
	client *coreclientv1.CoreV1Client
	raw    []byte
}

func newConfigMapBuilder(config *rest.Config, m lib.Manifest) *configMapBuilder {
	return &configMapBuilder{
		client: coreclientv1.NewForConfigOrDie(config),
		raw:    m.Raw,
	}
}

func (b *configMapBuilder) Do(modifier MetaV1ObjectModifierFunc) error {
	configMap := resourceread.ReadConfigMapV1OrDie(b.raw)
	modifier(configMap)
	_, _, err := resourceapply.ApplyConfigMap(b.client, configMap)
	return err
}

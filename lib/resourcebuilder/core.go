package resourcebuilder

import (
	"github.com/openshift/cluster-version-operator/lib"
	"github.com/openshift/cluster-version-operator/lib/resourceapply"
	"github.com/openshift/cluster-version-operator/lib/resourceread"
	coreclientv1 "k8s.io/client-go/kubernetes/typed/core/v1"
	"k8s.io/client-go/rest"
)

type serviceAccountBuilder struct {
	client   *coreclientv1.CoreV1Client
	raw      []byte
	modifier MetaV1ObjectModifierFunc
}

func newServiceAccountBuilder(config *rest.Config, m lib.Manifest) Interface {
	return &serviceAccountBuilder{
		client: coreclientv1.NewForConfigOrDie(withProtobuf(config)),
		raw:    m.Raw,
	}
}

func (b *serviceAccountBuilder) WithModifier(f MetaV1ObjectModifierFunc) Interface {
	b.modifier = f
	return b
}

func (b *serviceAccountBuilder) Do(initial bool) error {
	serviceAccount := resourceread.ReadServiceAccountV1OrDie(b.raw)
	if b.modifier != nil {
		b.modifier(serviceAccount)
	}
	if initial {
		_, err := b.client.ServiceAccounts(serviceAccount.Namespace).Create(serviceAccount)
		return err
	}
	_, _, err := resourceapply.ApplyServiceAccount(b.client, serviceAccount)
	return err
}

type configMapBuilder struct {
	client   *coreclientv1.CoreV1Client
	raw      []byte
	modifier MetaV1ObjectModifierFunc
}

func newConfigMapBuilder(config *rest.Config, m lib.Manifest) Interface {
	return &configMapBuilder{
		client: coreclientv1.NewForConfigOrDie(withProtobuf(config)),
		raw:    m.Raw,
	}
}

func (b *configMapBuilder) WithModifier(f MetaV1ObjectModifierFunc) Interface {
	b.modifier = f
	return b
}

func (b *configMapBuilder) Do(initial bool) error {
	configMap := resourceread.ReadConfigMapV1OrDie(b.raw)
	if b.modifier != nil {
		b.modifier(configMap)
	}
	if initial {
		_, err := b.client.ConfigMaps(configMap.Namespace).Create(configMap)
		return err
	}
	_, _, err := resourceapply.ApplyConfigMap(b.client, configMap)
	return err
}

type namespaceBuilder struct {
	client   *coreclientv1.CoreV1Client
	raw      []byte
	modifier MetaV1ObjectModifierFunc
}

func newNamespaceBuilder(config *rest.Config, m lib.Manifest) Interface {
	return &namespaceBuilder{
		client: coreclientv1.NewForConfigOrDie(withProtobuf(config)),
		raw:    m.Raw,
	}
}

func (b *namespaceBuilder) WithModifier(f MetaV1ObjectModifierFunc) Interface {
	b.modifier = f
	return b
}

func (b *namespaceBuilder) Do(initial bool) error {
	namespace := resourceread.ReadNamespaceV1OrDie(b.raw)
	if b.modifier != nil {
		b.modifier(namespace)
	}
	if initial {
		_, err := b.client.Namespaces().Create(namespace)
		return err
	}
	_, _, err := resourceapply.ApplyNamespace(b.client, namespace)
	return err
}

type serviceBuilder struct {
	client   *coreclientv1.CoreV1Client
	raw      []byte
	modifier MetaV1ObjectModifierFunc
}

func newServiceBuilder(config *rest.Config, m lib.Manifest) Interface {
	return &serviceBuilder{
		client: coreclientv1.NewForConfigOrDie(withProtobuf(config)),
		raw:    m.Raw,
	}
}

func (b *serviceBuilder) WithModifier(f MetaV1ObjectModifierFunc) Interface {
	b.modifier = f
	return b
}

func (b *serviceBuilder) Do(initial bool) error {
	service := resourceread.ReadServiceV1OrDie(b.raw)
	if b.modifier != nil {
		b.modifier(service)
	}
	if initial {
		_, err := b.client.Services(service.Namespace).Create(service)
		return err
	}
	_, _, err := resourceapply.ApplyService(b.client, service)
	return err
}

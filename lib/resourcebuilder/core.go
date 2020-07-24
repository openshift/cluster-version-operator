package resourcebuilder

import (
	"context"

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

func (b *serviceAccountBuilder) WithMode(m Mode) Interface {
	return b
}

func (b *serviceAccountBuilder) WithModifier(f MetaV1ObjectModifierFunc) Interface {
	b.modifier = f
	return b
}

func (b *serviceAccountBuilder) Do(ctx context.Context) error {
	serviceAccount := resourceread.ReadServiceAccountV1OrDie(b.raw)
	if b.modifier != nil {
		b.modifier(serviceAccount)
	}
	_, _, err := resourceapply.ApplyServiceAccount(ctx, b.client, serviceAccount)
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

func (b *configMapBuilder) WithMode(m Mode) Interface {
	return b
}

func (b *configMapBuilder) WithModifier(f MetaV1ObjectModifierFunc) Interface {
	b.modifier = f
	return b
}

func (b *configMapBuilder) Do(ctx context.Context) error {
	configMap := resourceread.ReadConfigMapV1OrDie(b.raw)
	if b.modifier != nil {
		b.modifier(configMap)
	}
	_, _, err := resourceapply.ApplyConfigMap(ctx, b.client, configMap)
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

func (b *namespaceBuilder) WithMode(m Mode) Interface {
	return b
}

func (b *namespaceBuilder) WithModifier(f MetaV1ObjectModifierFunc) Interface {
	b.modifier = f
	return b
}

func (b *namespaceBuilder) Do(ctx context.Context) error {
	namespace := resourceread.ReadNamespaceV1OrDie(b.raw)
	if b.modifier != nil {
		b.modifier(namespace)
	}
	_, _, err := resourceapply.ApplyNamespace(ctx, b.client, namespace)
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

func (b *serviceBuilder) WithMode(m Mode) Interface {
	return b
}

func (b *serviceBuilder) WithModifier(f MetaV1ObjectModifierFunc) Interface {
	b.modifier = f
	return b
}

func (b *serviceBuilder) Do(ctx context.Context) error {
	service := resourceread.ReadServiceV1OrDie(b.raw)
	if b.modifier != nil {
		b.modifier(service)
	}
	_, _, err := resourceapply.ApplyService(ctx, b.client, service)
	return err
}

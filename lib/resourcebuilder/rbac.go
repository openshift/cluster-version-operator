package resourcebuilder

import (
	"github.com/openshift/cluster-version-operator/lib"
	"github.com/openshift/cluster-version-operator/lib/resourceapply"
	"github.com/openshift/cluster-version-operator/lib/resourceread"
	rbacclientv1 "k8s.io/client-go/kubernetes/typed/rbac/v1"
	"k8s.io/client-go/rest"
)

type clusterRoleBuilder struct {
	client   *rbacclientv1.RbacV1Client
	raw      []byte
	modifier MetaV1ObjectModifierFunc
}

func newClusterRoleBuilder(config *rest.Config, m lib.Manifest) Interface {
	return &clusterRoleBuilder{
		client: rbacclientv1.NewForConfigOrDie(withProtobuf(config)),
		raw:    m.Raw,
	}
}

func (b *clusterRoleBuilder) WithModifier(f MetaV1ObjectModifierFunc) Interface {
	b.modifier = f
	return b
}

func (b *clusterRoleBuilder) Do(initial bool) error {
	clusterRole := resourceread.ReadClusterRoleV1OrDie(b.raw)
	if b.modifier != nil {
		b.modifier(clusterRole)
	}
	if initial {
		_, err := b.client.ClusterRoles().Create(clusterRole)
		return err
	}
	_, _, err := resourceapply.ApplyClusterRole(b.client, clusterRole)
	return err
}

type clusterRoleBindingBuilder struct {
	client   *rbacclientv1.RbacV1Client
	raw      []byte
	modifier MetaV1ObjectModifierFunc
}

func newClusterRoleBindingBuilder(config *rest.Config, m lib.Manifest) Interface {
	return &clusterRoleBindingBuilder{
		client: rbacclientv1.NewForConfigOrDie(withProtobuf(config)),
		raw:    m.Raw,
	}
}

func (b *clusterRoleBindingBuilder) WithModifier(f MetaV1ObjectModifierFunc) Interface {
	b.modifier = f
	return b
}

func (b *clusterRoleBindingBuilder) Do(initial bool) error {
	clusterRoleBinding := resourceread.ReadClusterRoleBindingV1OrDie(b.raw)
	if b.modifier != nil {
		b.modifier(clusterRoleBinding)
	}
	if initial {
		_, err := b.client.ClusterRoleBindings().Create(clusterRoleBinding)
		return err
	}
	_, _, err := resourceapply.ApplyClusterRoleBinding(b.client, clusterRoleBinding)
	return err
}

type roleBuilder struct {
	client   *rbacclientv1.RbacV1Client
	raw      []byte
	modifier MetaV1ObjectModifierFunc
}

func newRoleBuilder(config *rest.Config, m lib.Manifest) Interface {
	return &roleBuilder{
		client: rbacclientv1.NewForConfigOrDie(withProtobuf(config)),
		raw:    m.Raw,
	}
}

func (b *roleBuilder) WithModifier(f MetaV1ObjectModifierFunc) Interface {
	b.modifier = f
	return b
}

func (b *roleBuilder) Do(initial bool) error {
	role := resourceread.ReadRoleV1OrDie(b.raw)
	if b.modifier != nil {
		b.modifier(role)
	}
	if initial {
		_, err := b.client.Roles(role.Namespace).Create(role)
		return err
	}
	_, _, err := resourceapply.ApplyRole(b.client, role)
	return err
}

type roleBindingBuilder struct {
	client   *rbacclientv1.RbacV1Client
	raw      []byte
	modifier MetaV1ObjectModifierFunc
}

func newRoleBindingBuilder(config *rest.Config, m lib.Manifest) Interface {
	return &roleBindingBuilder{
		client: rbacclientv1.NewForConfigOrDie(withProtobuf(config)),
		raw:    m.Raw,
	}
}

func (b *roleBindingBuilder) WithModifier(f MetaV1ObjectModifierFunc) Interface {
	b.modifier = f
	return b
}

func (b *roleBindingBuilder) Do(initial bool) error {
	roleBinding := resourceread.ReadRoleBindingV1OrDie(b.raw)
	if b.modifier != nil {
		b.modifier(roleBinding)
	}
	if initial {
		_, err := b.client.RoleBindings(roleBinding.Namespace).Create(roleBinding)
		return err
	}
	_, _, err := resourceapply.ApplyRoleBinding(b.client, roleBinding)
	return err
}

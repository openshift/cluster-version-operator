package resourcebuilder

import (
	"fmt"

	"github.com/openshift/cluster-version-operator/lib"
	"github.com/openshift/cluster-version-operator/lib/resourceapply"
	"github.com/openshift/cluster-version-operator/lib/resourceread"
	rbacclientv1 "k8s.io/client-go/kubernetes/typed/rbac/v1"
	"k8s.io/client-go/rest"
)

func newRbacBuilder(config *rest.Config, m lib.Manifest) (Interface, error) {
	kind := m.GVK.Kind
	switch kind {
	case clusterRoleKind:
		return newClusterRoleBuilder(config, m), nil
	case clusterRoleBindingKind:
		return newClusterRoleBindingBuilder(config, m), nil
	default:
		return nil, fmt.Errorf("no Rbac builder found for %s", kind)
	}
}

const (
	clusterRoleKind = "ClusterRole"
)

type clusterRoleBuilder struct {
	client *rbacclientv1.RbacV1Client
	raw    []byte
}

func newClusterRoleBuilder(config *rest.Config, m lib.Manifest) *clusterRoleBuilder {
	return &clusterRoleBuilder{
		client: rbacclientv1.NewForConfigOrDie(config),
		raw:    m.Raw,
	}
}

func (b *clusterRoleBuilder) Do(modifier MetaV1ObjectModifierFunc) error {
	clusterRole := resourceread.ReadClusterRoleV1OrDie(b.raw)
	modifier(clusterRole)
	_, _, err := resourceapply.ApplyClusterRole(b.client, clusterRole)
	return err
}

const (
	clusterRoleBindingKind = "ClusterRoleBinding"
)

type clusterRoleBindingBuilder struct {
	client *rbacclientv1.RbacV1Client
	raw    []byte
}

func newClusterRoleBindingBuilder(config *rest.Config, m lib.Manifest) *clusterRoleBindingBuilder {
	return &clusterRoleBindingBuilder{
		client: rbacclientv1.NewForConfigOrDie(config),
		raw:    m.Raw,
	}
}

func (b *clusterRoleBindingBuilder) Do(modifier MetaV1ObjectModifierFunc) error {
	clusterRoleBinding := resourceread.ReadClusterRoleBindingV1OrDie(b.raw)
	modifier(clusterRoleBinding)
	_, _, err := resourceapply.ApplyClusterRoleBinding(b.client, clusterRoleBinding)
	return err
}

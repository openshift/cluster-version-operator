package dynamicclient

import (
	internaldynamicclient "github.com/openshift/cluster-version-operator/pkg/cvo/internal/dynamicclient"

	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/rest"
)

// New returns the resource client using a singleton factory
func New(config *rest.Config, gvk schema.GroupVersionKind, namespace string) (dynamic.ResourceInterface, error) {
	return internaldynamicclient.New(config, gvk, namespace)
}

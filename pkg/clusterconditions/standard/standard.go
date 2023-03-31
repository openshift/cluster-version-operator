package standard

import (
	"github.com/openshift/cluster-version-operator/pkg/clusterconditions"
	"github.com/openshift/cluster-version-operator/pkg/clusterconditions/always"
	"github.com/openshift/cluster-version-operator/pkg/clusterconditions/promql"
	"k8s.io/client-go/kubernetes"
)

func NewConditionRegistry(kubeClient kubernetes.Interface) clusterconditions.ConditionRegistry {
	conditionRegistry := clusterconditions.NewConditionRegistry()
	conditionRegistry.Register("Always", &always.Always{})
	conditionRegistry.Register("PromQL", promql.NewPromQL(kubeClient))

	return conditionRegistry
}

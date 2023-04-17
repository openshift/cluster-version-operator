package standard

import (
	"github.com/openshift/cluster-version-operator/pkg/clusterconditions"
	"github.com/openshift/cluster-version-operator/pkg/clusterconditions/always"
	"github.com/openshift/cluster-version-operator/pkg/clusterconditions/promql"
)

func NewConditionRegistry(promqlTarget clusterconditions.PromQLTarget) clusterconditions.ConditionRegistry {
	conditionRegistry := clusterconditions.NewConditionRegistry()
	conditionRegistry.Register("Always", &always.Always{})
	conditionRegistry.Register("PromQL", promql.NewPromQL(promqlTarget))

	return conditionRegistry
}

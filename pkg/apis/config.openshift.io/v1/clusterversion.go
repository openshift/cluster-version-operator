package v1

import (
	"fmt"
)

func (c ClusterVersion) String() string {
	return fmt.Sprintf("{ Upstream: %v Channel: %s ClusterID: %s }", c.Spec.Upstream, c.Spec.Channel, c.Spec.ClusterID)
}

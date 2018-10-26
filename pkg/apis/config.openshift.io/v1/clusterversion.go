package v1

import (
	"fmt"

	"k8s.io/apimachinery/pkg/runtime"
)

// DeepCopyObject copies the ClusterVersion into an Object. This doesn't actually
// require a deep copy, but the code generator (and Go itself) isn't advanced
// enough to determine that.
func (c *ClusterVersion) DeepCopyObject() runtime.Object {
	out := *c
	c.ObjectMeta.DeepCopyInto(&out.ObjectMeta)
	return &out
}

// DeepCopyInto copies the ClusterVersion into another ClusterVersion. This doesn't
// actually require a deep copy, but the code generator (and Go itself) isn't
// advanced enough to determine that.
func (c *ClusterVersion) DeepCopyInto(out *ClusterVersion) {
	*out = *c
	c.ObjectMeta.DeepCopyInto(&out.ObjectMeta)
}

func (c ClusterVersion) String() string {
	return fmt.Sprintf("{ Upstream: %s Channel: %s ClusterID: %s }", c.Upstream, c.Channel, c.ClusterID)
}

package resourcemerge

import (
	"fmt"
	"sync"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/klog/v2"
)

type Resource struct {
	Kind      string
	Namespace string
	Name      string
}

type ResourceId struct {
	ResourceVersion string
	Generation      int64
}

var (
	resourceIds = struct {
		lock sync.RWMutex
		m    map[Resource]ResourceId
	}{m: make(map[Resource]ResourceId)}
)

func (r Resource) uniqueName() string {
	if len(r.Namespace) == 0 {
		return r.Name
	}
	return r.Namespace + "/" + r.Name
}

func (r Resource) String() string {
	return fmt.Sprintf("%s \"%s\"", r.Kind, r.uniqueName())
}

func (r ResourceId) String() string {
	return fmt.Sprintf("%s/%d", r.ResourceVersion, r.Generation)
}

// SetDeleteRequested creates or updates entry in map to indicate resource deletion has been requested.
func SetResourceId(obj metav1.Object, resource Resource) {
	resourceIds.lock.Lock()
	id := ResourceId{ResourceVersion: obj.GetResourceVersion(),
		Generation: obj.GetGeneration()}
	resourceIds.m[resource] = id
	resourceIds.lock.Unlock()
	klog.V(4).Infof("!!!! Resource %s version/generation: %s", resource, resourceIds.m[resource])
}

// GetResourceVersion returns map entry for given resource.
func GetResourceId(resource Resource) (ResourceId, bool) {
	resourceIds.lock.Lock()
	defer resourceIds.lock.Unlock()
	id, ok := resourceIds.m[resource]
	return id, ok
}

func ResourceModified(resourceId ResourceId, existingObj metav1.Object) bool {
	if resourceId.ResourceVersion == existingObj.GetResourceVersion() {
		if resourceId.Generation == existingObj.GetGeneration() {
			return false
		}
	}
	return true
}

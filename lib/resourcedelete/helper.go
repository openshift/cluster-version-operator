package resourcedelete

import (
	"fmt"
	"sync"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/klog/v2"
)

const DeleteAnnotation = "release.openshift.io/delete"

type Resource struct {
	Kind      string
	Namespace string
	Name      string
}

type deleteTimes struct {
	Requested time.Time
	Expected  *metav1.Time
	Verified  time.Time
}

var (
	deletedResources = struct {
		lock sync.RWMutex
		m    map[Resource]deleteTimes
	}{m: make(map[Resource]deleteTimes)}
)

// ValidDeleteAnnotation returns whether the delete annotation is found and an error if it is found
// but not set to "true".
func ValidDeleteAnnotation(annotations map[string]string) (bool, error) {
	if value, ok := annotations[DeleteAnnotation]; !ok {
		return false, nil
	} else if value != "true" {
		return true, fmt.Errorf("Invalid delete annotation \"%s\" value: \"%s\"", DeleteAnnotation, value)
	}
	return true, nil
}

func (r Resource) uniqueName() string {
	if len(r.Namespace) == 0 {
		return r.Name
	}
	return r.Namespace + "/" + r.Name
}

func (r Resource) String() string {
	return fmt.Sprintf("%s \"%s\"", r.Kind, r.uniqueName())
}

// SetDeleteRequested creates or updates entry in map to indicate resource deletion has been requested.
func SetDeleteRequested(obj metav1.Object, resource Resource) {
	times := deleteTimes{
		Requested: time.Now(),
		Expected:  obj.GetDeletionTimestamp(),
	}
	deletedResources.lock.Lock()
	deletedResources.m[resource] = times
	deletedResources.lock.Unlock()
	klog.V(4).Infof("Delete requested for %s.", resource)
}

// SetDeleteVerified updates map entry to indicate resource deletion has been completed.
func SetDeleteVerified(resource Resource) {
	times := deleteTimes{
		Verified: time.Now(),
	}
	deletedResources.lock.Lock()
	deletedResources.m[resource] = times
	deletedResources.lock.Unlock()
	klog.V(4).Infof("Delete of %s completed.", resource)
}

// getDeleteTimes returns map entry for given resource.
func getDeleteTimes(resource Resource) (deleteTimes, bool) {
	deletedResources.lock.Lock()
	defer deletedResources.lock.Unlock()
	deletionTimes, ok := deletedResources.m[resource]
	return deletionTimes, ok
}

// setDeleteRequestedAndVerified creates or updates a map entry to indicate resource deletion has been requested
// and completed.
func setDeleteRequestedAndVerified(resource Resource) {
	times := deleteTimes{
		Requested: time.Now(),
		Verified:  time.Now(),
	}
	deletedResources.lock.Lock()
	deletedResources.m[resource] = times
	deletedResources.lock.Unlock()
	klog.Warningf("%s has already been removed.", resource)
}

// DeleteInProgress returns whether resource deletion has been requested but not yet verified.
func DeleteInProgress(resource Resource) bool {
	if deletionTimes, ok := getDeleteTimes(resource); ok && deletionTimes.Verified.IsZero() {
		return true
	}
	return false
}

// GetDeleteProgress checks if resource deletion has been requested. If it has it checks if the deletion has completed
// and if not logs deletion progress. This method returns an indication of whether resource deletion has already been
// requested and any error that occurs.
func GetDeleteProgress(resource Resource, getError error) (bool, error) {
	if deletionTimes, ok := getDeleteTimes(resource); ok {
		if !deletionTimes.Verified.IsZero() {
			if getError == nil || !apierrors.IsNotFound(getError) {
				klog.Warningf("%s has reappeared after having been deleted at %s.", resource, deletionTimes.Verified)
			}
		} else {
			if apierrors.IsNotFound(getError) {
				SetDeleteVerified(resource)
			} else {
				if deletionTimes.Expected != nil {
					klog.V(4).Infof("Delete of %s is expected by %s.", resource, deletionTimes.Expected.String())
				} else {
					klog.V(4).Infof("Delete of %s has already been requested.", resource)
				}
			}
		}
		return true, nil
	}
	// During an upgrade CVO restarts one or more times. The resource may have been deleted during one of the
	// previous CVO life cycles. Simply set the resource as delete verified and log warning.
	if apierrors.IsNotFound(getError) {
		setDeleteRequestedAndVerified(resource)
		return true, nil
	}
	if getError != nil {
		return false, fmt.Errorf("Cannot get %s to delete, err=%v.", resource, getError)
	}
	return false, nil
}

// DeletesInProgress returns the set of resources for which deletion has been requested but not yet verified.
func DeletesInProgress() []string {
	deletedResources.lock.Lock()
	defer deletedResources.lock.Unlock()
	deletes := make([]string, 0, len(deletedResources.m))
	for k := range deletedResources.m {
		if deletedResources.m[k].Verified.IsZero() {
			deletes = append(deletes, k.String())
		}
	}
	return deletes
}

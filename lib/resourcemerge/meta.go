package resourcemerge

import (
	"k8s.io/apimachinery/pkg/api/equality"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/klog/v2"
)

// EnsureObjectMeta ensures that the existing matches the required.
// modified is set to true when existing had to be updated with required.
func EnsureObjectMeta(modified *bool, existing *metav1.ObjectMeta, required metav1.ObjectMeta) {
	klog.V(2).Infof("EnsureObjectMeta %s/%s (%t)", required.Namespace, required.Name, *modified)
	setStringIfSet(modified, &existing.Namespace, required.Namespace)
	klog.V(2).Infof("EnsureObjectMeta after Namespace %s/%s (%t)", required.Namespace, required.Name, *modified)
	setStringIfSet(modified, &existing.Name, required.Name)
	klog.V(2).Infof("EnsureObjectMeta after Name %s/%s (%t)", required.Namespace, required.Name, *modified)
	mergeMap(modified, &existing.Labels, required.Labels)
	klog.V(2).Infof("EnsureObjectMeta after Labels %s/%s (%t)", required.Namespace, required.Name, *modified)
	mergeMap(modified, &existing.Annotations, required.Annotations)
	klog.V(2).Infof("EnsureObjectMeta after Annotations %s/%s (%t)", required.Namespace, required.Name, *modified)
	mergeOwnerRefs(modified, &existing.OwnerReferences, required.OwnerReferences)
	klog.V(2).Infof("EnsureObjectMeta after OwnerReferences %s/%s (%t)", required.Namespace, required.Name, *modified)
}

func setStringIfSet(modified *bool, existing *string, required string) {
	if len(required) == 0 {
		return
	}
	if required != *existing {
		*existing = required
		*modified = true
	}
}

func mergeMap(modified *bool, existing *map[string]string, required map[string]string) {
	if *existing == nil {
		if required == nil {
			return
		}
		*existing = map[string]string{}
	}
	for k, v := range required {
		if existingV, ok := (*existing)[k]; !ok || v != existingV {
			*modified = true
			(*existing)[k] = v
		}
	}
}

func mergeOwnerRefs(modified *bool, existing *[]metav1.OwnerReference, required []metav1.OwnerReference) {
	for ridx := range required {
		found := false
		for eidx := range *existing {
			if required[ridx].UID == (*existing)[eidx].UID {
				found = true
				if !equality.Semantic.DeepEqual((*existing)[eidx], required[ridx]) {
					*modified = true
					(*existing)[eidx] = required[ridx]
				}
				break
			}
		}
		if !found {
			*modified = true
			*existing = append(*existing, required[ridx])
		}
	}
}

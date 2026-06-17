package resourcemerge

import (
	"github.com/google/go-cmp/cmp"
	"k8s.io/apimachinery/pkg/api/equality"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/klog/v2"
)

// EnsureObjectMeta ensures that the existing matches the required.
// modified is set to true when existing had to be updated with required.
func EnsureObjectMeta(modified *bool, existing *metav1.ObjectMeta, required metav1.ObjectMeta) {
	mod := *modified
	*modified = false
	setStringIfSet(modified, &existing.Namespace, required.Namespace)
	if *modified {
		klog.V(2).Infof("EFRIED: Namespaces differ")
		mod = true
		*modified = false
	}
	setStringIfSet(modified, &existing.Name, required.Name)
	if *modified {
		klog.V(2).Infof("EFRIED: Names differ")
		mod = true
		*modified = false
	}
	mergeMap(modified, &existing.Labels, required.Labels)
	if *modified {
		klog.V(2).Infof("EFRIED: Labels differ")
		mod = true
		*modified = false
	}
	mergeMap(modified, &existing.Annotations, required.Annotations)
	if *modified {
		klog.V(2).Infof("EFRIED: Annotations differ")
		mod = true
		*modified = false
	}
	mergeOwnerRefs(modified, &existing.OwnerReferences, required.OwnerReferences)
	if *modified {
		klog.V(2).Infof("EFRIED: OwnerRefs differ")
		mod = true
	}
	*modified = mod
}

func setStringIfSet(modified *bool, existing *string, required string) {
	if len(required) == 0 {
		return
	}
	if required != *existing {
		klog.V(2).Infof("EFRIED: %s != %s", *existing, required)
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
			klog.V(2).Infof("EFRIED: map[%s]: %s != %s", k, existingV, v)
			*modified = true
			(*existing)[k] = v
		}
	}
}

func mergeByteSliceMap(modified *bool, existing *map[string][]byte, required map[string][]byte) {
	if *existing == nil {
		if required == nil {
			return
		}
		*existing = map[string][]byte{}
	}
	for k, v := range required {
		if existingV, ok := (*existing)[k]; !ok || string(v) != string(existingV) {
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
					klog.V(2).Infof("EFRIED: OwnerReferences differ: %s", cmp.Diff((*existing)[eidx], required[ridx]))
					*modified = true
					(*existing)[eidx] = required[ridx]
				}
				break
			}
		}
		if !found {
			klog.V(2).Infof("EFRIED: OwnerReference not found: %v", required[ridx])
			*modified = true
			*existing = append(*existing, required[ridx])
		}
	}
}

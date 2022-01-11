package resourcemerge

import (
	"k8s.io/apimachinery/pkg/api/equality"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/klog/v2"
)

// EnsureObjectMeta ensures that the existing matches the required.
// modified is set to true when existing had to be updated with required.
func EnsureObjectMeta(modified *bool, existing *metav1.ObjectMeta, required metav1.ObjectMeta) {
	if required.Namespace == "openshift-monitoring" && required.Name == "cluster-monitoring-operator" && *modified {klog.V(2).Infof("ensure object meta already modified")}
	setStringIfSet(modified, &existing.Namespace, required.Namespace)
	if required.Namespace == "openshift-monitoring" && required.Name == "cluster-monitoring-operator" && *modified {klog.V(2).Infof("namespace modified")}
	setStringIfSet(modified, &existing.Name, required.Name)
	if required.Namespace == "openshift-monitoring" && required.Name == "cluster-monitoring-operator" && *modified {klog.V(2).Infof("name modified")}
	mergeMap(modified, &existing.Labels, required.Labels)
	if required.Namespace == "openshift-monitoring" && required.Name == "cluster-monitoring-operator" && *modified {klog.V(2).Infof("labels modified")}
	mergeMap(modified, &existing.Annotations, required.Annotations)
	if required.Namespace == "openshift-monitoring" && required.Name == "cluster-monitoring-operator" && *modified {klog.V(2).Infof("annotations modified")}
	mergeOwnerRefs(modified, &existing.OwnerReferences, required.OwnerReferences)
	if required.Namespace == "openshift-monitoring" && required.Name == "cluster-monitoring-operator" && *modified {klog.V(2).Infof("owner references modified")}
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
					klog.V(2).Infof("clobering owner ref: %v != %v", (*existing)[eidx], required[ridx])
					(*existing)[eidx] = required[ridx]
				}
				break
			}
		}
		if !found {
			*modified = true
			klog.V(2).Infof("injecting owner ref: %v\n  into %v", required[ridx], *existing)
			*existing = append(*existing, required[ridx])
		}
	}
}

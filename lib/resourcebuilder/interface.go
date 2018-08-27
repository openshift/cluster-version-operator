package resourcebuilder

import (
	"fmt"

	"github.com/openshift/cluster-version-operator/lib"
	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	apiextv1beta1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1beta1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/rest"
)

type MetaV1ObjectModifierFunc func(metav1.Object)

type Interface interface {
	Do(MetaV1ObjectModifierFunc) error
}

func New(rest *rest.Config, m lib.Manifest) (Interface, error) {
	group, version := m.GVK.Group, m.GVK.Version
	switch {
	case group == batchv1.SchemeGroupVersion.Group && version == batchv1.SchemeGroupVersion.Version:
		return newBatchBuilder(rest, m)
	case group == corev1.SchemeGroupVersion.Group && version == corev1.SchemeGroupVersion.Version:
		return newCoreBuilder(rest, m)
	case group == appsv1.SchemeGroupVersion.Group && version == appsv1.SchemeGroupVersion.Version:
		return newAppsBuilder(rest, m)
	case group == rbacv1.SchemeGroupVersion.Group && version == rbacv1.SchemeGroupVersion.Version:
		return newRbacBuilder(rest, m)
	case group == apiextv1beta1.SchemeGroupVersion.Group && version == apiextv1beta1.SchemeGroupVersion.Version:
		return newAPIExtBuilder(rest, m)
	default:
		return nil, fmt.Errorf("no builder found for %s,%s", group, version)
	}
}

// auto-generated with generate-lib-resources.py

// Package resourcebuilder reads supported objects from bytes.
package resourcebuilder

import (
	"context"
	"fmt"

	imagev1 "github.com/openshift/api/image/v1"
	securityv1 "github.com/openshift/api/security/v1"
	configclientv1 "github.com/openshift/client-go/config/clientset/versioned/typed/config/v1"
	imageclientv1 "github.com/openshift/client-go/image/clientset/versioned/typed/image/v1"
	securityclientv1 "github.com/openshift/client-go/security/clientset/versioned/typed/security/v1"
	"github.com/openshift/cluster-version-operator/lib/resourceapply"
	"github.com/openshift/cluster-version-operator/lib/resourceread"
	"github.com/openshift/library-go/pkg/manifest"
	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	apiextensionsv1beta1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1beta1"
	apiextensionsclientv1 "k8s.io/apiextensions-apiserver/pkg/client/clientset/clientset/typed/apiextensions/v1"
	apiextensionsclientv1beta1 "k8s.io/apiextensions-apiserver/pkg/client/clientset/clientset/typed/apiextensions/v1beta1"
	appsclientv1 "k8s.io/client-go/kubernetes/typed/apps/v1"
	batchclientv1 "k8s.io/client-go/kubernetes/typed/batch/v1"
	coreclientv1 "k8s.io/client-go/kubernetes/typed/core/v1"
	rbacclientv1 "k8s.io/client-go/kubernetes/typed/rbac/v1"
	"k8s.io/client-go/rest"
	apiregistrationv1 "k8s.io/kube-aggregator/pkg/apis/apiregistration/v1"
	apiregistrationv1beta1 "k8s.io/kube-aggregator/pkg/apis/apiregistration/v1beta1"
	apiregistrationclientv1 "k8s.io/kube-aggregator/pkg/client/clientset_generated/clientset/typed/apiregistration/v1"
	apiregistrationclientv1beta1 "k8s.io/kube-aggregator/pkg/client/clientset_generated/clientset/typed/apiregistration/v1beta1"
)

// builder manages single-manifest cluster reconciliation and monitoring.
type builder struct {
	raw      []byte
	mode     Mode
	modifier MetaV1ObjectModifierFunc

	apiextensionsClientv1        *apiextensionsclientv1.ApiextensionsV1Client
	apiextensionsClientv1beta1   *apiextensionsclientv1beta1.ApiextensionsV1beta1Client
	apiregistrationClientv1      *apiregistrationclientv1.ApiregistrationV1Client
	apiregistrationClientv1beta1 *apiregistrationclientv1beta1.ApiregistrationV1beta1Client
	appsClientv1                 *appsclientv1.AppsV1Client
	batchClientv1                *batchclientv1.BatchV1Client
	configClientv1               *configclientv1.ConfigV1Client
	coreClientv1                 *coreclientv1.CoreV1Client
	imageClientv1                *imageclientv1.ImageV1Client
	rbacClientv1                 *rbacclientv1.RbacV1Client
	securityClientv1             *securityclientv1.SecurityV1Client
}

func newBuilder(config *rest.Config, m manifest.Manifest) Interface {
	return &builder{
		raw: m.Raw,

		apiextensionsClientv1:        apiextensionsclientv1.NewForConfigOrDie(withProtobuf(config)),
		apiextensionsClientv1beta1:   apiextensionsclientv1beta1.NewForConfigOrDie(withProtobuf(config)),
		apiregistrationClientv1:      apiregistrationclientv1.NewForConfigOrDie(config),
		apiregistrationClientv1beta1: apiregistrationclientv1beta1.NewForConfigOrDie(config),
		appsClientv1:                 appsclientv1.NewForConfigOrDie(withProtobuf(config)),
		batchClientv1:                batchclientv1.NewForConfigOrDie(withProtobuf(config)),
		configClientv1:               configclientv1.NewForConfigOrDie(config),
		coreClientv1:                 coreclientv1.NewForConfigOrDie(withProtobuf(config)),
		imageClientv1:                imageclientv1.NewForConfigOrDie(config),
		rbacClientv1:                 rbacclientv1.NewForConfigOrDie(withProtobuf(config)),
		securityClientv1:             securityclientv1.NewForConfigOrDie(config),
	}
}

func (b *builder) WithMode(m Mode) Interface {
	b.mode = m
	return b
}

func (b *builder) WithModifier(f MetaV1ObjectModifierFunc) Interface {
	b.modifier = f
	return b
}

func (b *builder) Do(ctx context.Context) error {
	obj := resourceread.ReadOrDie(b.raw)

	switch typedObject := obj.(type) {
	case *imagev1.ImageStream:
		if b.modifier != nil {
			b.modifier(typedObject)
		}
		if _, _, err := resourceapply.ApplyImageStreamv1(ctx, b.imageClientv1, typedObject); err != nil {
			return err
		}
	case *securityv1.SecurityContextConstraints:
		if b.modifier != nil {
			b.modifier(typedObject)
		}
		if _, _, err := resourceapply.ApplySecurityContextConstraintsv1(ctx, b.securityClientv1, typedObject); err != nil {
			return err
		}
	case *appsv1.DaemonSet:
		if b.modifier != nil {
			b.modifier(typedObject)
		}
		if err := b.modifyDaemonSet(ctx, typedObject); err != nil {
			return err
		}
		if _, _, err := resourceapply.ApplyDaemonSetv1(ctx, b.appsClientv1, typedObject); err != nil {
			return err
		}
		return b.checkDaemonSetHealth(ctx, typedObject)
	case *appsv1.Deployment:
		if b.modifier != nil {
			b.modifier(typedObject)
		}
		if err := b.modifyDeployment(ctx, typedObject); err != nil {
			return err
		}
		if _, _, err := resourceapply.ApplyDeploymentv1(ctx, b.appsClientv1, typedObject); err != nil {
			return err
		}
		return b.checkDeploymentHealth(ctx, typedObject)
	case *batchv1.Job:
		if b.modifier != nil {
			b.modifier(typedObject)
		}
		if _, _, err := resourceapply.ApplyJobv1(ctx, b.batchClientv1, typedObject); err != nil {
			return err
		}
		return b.checkJobHealth(ctx, typedObject)
	case *corev1.ConfigMap:
		if b.modifier != nil {
			b.modifier(typedObject)
		}
		if _, _, err := resourceapply.ApplyConfigMapv1(ctx, b.coreClientv1, typedObject); err != nil {
			return err
		}
	case *corev1.Namespace:
		if b.modifier != nil {
			b.modifier(typedObject)
		}
		if _, _, err := resourceapply.ApplyNamespacev1(ctx, b.coreClientv1, typedObject); err != nil {
			return err
		}
	case *corev1.Service:
		if b.modifier != nil {
			b.modifier(typedObject)
		}
		if _, _, err := resourceapply.ApplyServicev1(ctx, b.coreClientv1, typedObject); err != nil {
			return err
		}
	case *corev1.ServiceAccount:
		if b.modifier != nil {
			b.modifier(typedObject)
		}
		if _, _, err := resourceapply.ApplyServiceAccountv1(ctx, b.coreClientv1, typedObject); err != nil {
			return err
		}
	case *rbacv1.ClusterRole:
		if b.modifier != nil {
			b.modifier(typedObject)
		}
		if _, _, err := resourceapply.ApplyClusterRolev1(ctx, b.rbacClientv1, typedObject); err != nil {
			return err
		}
	case *rbacv1.ClusterRoleBinding:
		if b.modifier != nil {
			b.modifier(typedObject)
		}
		if _, _, err := resourceapply.ApplyClusterRoleBindingv1(ctx, b.rbacClientv1, typedObject); err != nil {
			return err
		}
	case *rbacv1.Role:
		if b.modifier != nil {
			b.modifier(typedObject)
		}
		if _, _, err := resourceapply.ApplyRolev1(ctx, b.rbacClientv1, typedObject); err != nil {
			return err
		}
	case *rbacv1.RoleBinding:
		if b.modifier != nil {
			b.modifier(typedObject)
		}
		if _, _, err := resourceapply.ApplyRoleBindingv1(ctx, b.rbacClientv1, typedObject); err != nil {
			return err
		}
	case *apiextensionsv1.CustomResourceDefinition:
		if b.modifier != nil {
			b.modifier(typedObject)
		}
		if _, _, err := resourceapply.ApplyCustomResourceDefinitionv1(ctx, b.apiextensionsClientv1, typedObject); err != nil {
			return err
		}
	case *apiextensionsv1beta1.CustomResourceDefinition:
		if b.modifier != nil {
			b.modifier(typedObject)
		}
		if _, _, err := resourceapply.ApplyCustomResourceDefinitionv1beta1(ctx, b.apiextensionsClientv1beta1, typedObject); err != nil {
			return err
		}
	case *apiregistrationv1.APIService:
		if b.modifier != nil {
			b.modifier(typedObject)
		}
		if _, _, err := resourceapply.ApplyAPIServicev1(ctx, b.apiregistrationClientv1, typedObject); err != nil {
			return err
		}
	case *apiregistrationv1beta1.APIService:
		if b.modifier != nil {
			b.modifier(typedObject)
		}
		if _, _, err := resourceapply.ApplyAPIServicev1beta1(ctx, b.apiregistrationClientv1beta1, typedObject); err != nil {
			return err
		}
	default:
		return fmt.Errorf("unrecognized manifest type: %T", obj)
	}

	return nil
}

func init() {
	rm := NewResourceMapper()
	rm.RegisterGVK(apiextensionsv1.SchemeGroupVersion.WithKind("CustomResourceDefinition"), newBuilder)
	rm.RegisterGVK(apiextensionsv1beta1.SchemeGroupVersion.WithKind("CustomResourceDefinition"), newBuilder)
	rm.RegisterGVK(apiregistrationv1.SchemeGroupVersion.WithKind("APIService"), newBuilder)
	rm.RegisterGVK(apiregistrationv1beta1.SchemeGroupVersion.WithKind("APIService"), newBuilder)
	rm.RegisterGVK(appsv1.SchemeGroupVersion.WithKind("DaemonSet"), newBuilder)
	rm.RegisterGVK(appsv1.SchemeGroupVersion.WithKind("Deployment"), newBuilder)
	rm.RegisterGVK(batchv1.SchemeGroupVersion.WithKind("Job"), newBuilder)
	rm.RegisterGVK(corev1.SchemeGroupVersion.WithKind("ConfigMap"), newBuilder)
	rm.RegisterGVK(corev1.SchemeGroupVersion.WithKind("Namespace"), newBuilder)
	rm.RegisterGVK(corev1.SchemeGroupVersion.WithKind("Service"), newBuilder)
	rm.RegisterGVK(corev1.SchemeGroupVersion.WithKind("ServiceAccount"), newBuilder)
	rm.RegisterGVK(imagev1.SchemeGroupVersion.WithKind("ImageStream"), newBuilder)
	rm.RegisterGVK(rbacv1.SchemeGroupVersion.WithKind("ClusterRole"), newBuilder)
	rm.RegisterGVK(rbacv1.SchemeGroupVersion.WithKind("ClusterRoleBinding"), newBuilder)
	rm.RegisterGVK(rbacv1.SchemeGroupVersion.WithKind("Role"), newBuilder)
	rm.RegisterGVK(rbacv1.SchemeGroupVersion.WithKind("RoleBinding"), newBuilder)
	rm.RegisterGVK(securityv1.SchemeGroupVersion.WithKind("SecurityContextConstraints"), newBuilder)
	rm.AddToMap(Mapper)
}

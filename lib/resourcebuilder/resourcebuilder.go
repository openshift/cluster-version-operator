// auto-generated with generate-lib-resources.py

// Package resourcebuilder reads supported objects from bytes.
package resourcebuilder

import (
	"context"
	"fmt"
	"sync"

	operatorsv1 "github.com/operator-framework/api/pkg/operators/v1"
	operatorsclientv1 "github.com/operator-framework/operator-lifecycle-manager/pkg/api/client/clientset/versioned/typed/operators/v1"

	admissionregistrationv1 "k8s.io/api/admissionregistration/v1"
	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	apiextensionsclientv1 "k8s.io/apiextensions-apiserver/pkg/client/clientset/clientset/typed/apiextensions/v1"
	admissionregistrationclientv1 "k8s.io/client-go/kubernetes/typed/admissionregistration/v1"
	appsclientv1 "k8s.io/client-go/kubernetes/typed/apps/v1"
	batchclientv1 "k8s.io/client-go/kubernetes/typed/batch/v1"
	coreclientv1 "k8s.io/client-go/kubernetes/typed/core/v1"
	rbacclientv1 "k8s.io/client-go/kubernetes/typed/rbac/v1"
	"k8s.io/client-go/rest"
	apiregistrationclientv1 "k8s.io/kube-aggregator/pkg/client/clientset_generated/clientset/typed/apiregistration/v1"

	imagev1 "github.com/openshift/api/image/v1"
	securityv1 "github.com/openshift/api/security/v1"
	configclientv1 "github.com/openshift/client-go/config/clientset/versioned/typed/config/v1"
	imageclientv1 "github.com/openshift/client-go/image/clientset/versioned/typed/image/v1"
	securityclientv1 "github.com/openshift/client-go/security/clientset/versioned/typed/security/v1"
	"github.com/openshift/library-go/pkg/manifest"

	"github.com/openshift/cluster-version-operator/lib/resourceapply"
	"github.com/openshift/cluster-version-operator/lib/resourcedelete"
	"github.com/openshift/cluster-version-operator/lib/resourceread"
)

// builder manages single-manifest cluster reconciliation and monitoring.
type builder struct {
	raw      []byte
	mode     Mode
	modifier MetaV1ObjectModifierFunc

	admissionregistrationClientv1 admissionregistrationclientv1.AdmissionregistrationV1Interface
	apiextensionsClientv1         apiextensionsclientv1.ApiextensionsV1Interface
	apiregistrationClientv1       apiregistrationclientv1.ApiregistrationV1Interface
	appsClientv1                  appsclientv1.AppsV1Interface
	batchClientv1                 batchclientv1.BatchV1Interface
	configClientv1                configclientv1.ConfigV1Interface
	coreClientv1                  coreclientv1.CoreV1Interface
	imageClientv1                 imageclientv1.ImageV1Interface
	operatorsClientv1             operatorsclientv1.OperatorsV1Interface
	rbacClientv1                  rbacclientv1.RbacV1Interface
	securityClientv1              securityclientv1.SecurityV1Interface
}

// clientSet holds cached typed clients for resource building.
type clientSet struct {
	admissionregistrationClientv1 admissionregistrationclientv1.AdmissionregistrationV1Interface
	apiextensionsClientv1         apiextensionsclientv1.ApiextensionsV1Interface
	apiregistrationClientv1       apiregistrationclientv1.ApiregistrationV1Interface
	appsClientv1                  appsclientv1.AppsV1Interface
	batchClientv1                 batchclientv1.BatchV1Interface
	configClientv1                configclientv1.ConfigV1Interface
	coreClientv1                  coreclientv1.CoreV1Interface
	imageClientv1                 imageclientv1.ImageV1Interface
	operatorsClientv1             operatorsclientv1.OperatorsV1Interface
	rbacClientv1                  rbacclientv1.RbacV1Interface
	securityClientv1              securityclientv1.SecurityV1Interface
}

func newClientSet(config *rest.Config) (*clientSet, error) {
	admissionregistrationClientv1, err := admissionregistrationclientv1.NewForConfig(withProtobuf(config))
	if err != nil {
		return nil, err
	}
	apiextensionsClientv1, err := apiextensionsclientv1.NewForConfig(withProtobuf(config))
	if err != nil {
		return nil, err
	}
	apiregistrationClientv1, err := apiregistrationclientv1.NewForConfig(config)
	if err != nil {
		return nil, err
	}
	appsClientv1, err := appsclientv1.NewForConfig(withProtobuf(config))
	if err != nil {
		return nil, err
	}
	batchClientv1, err := batchclientv1.NewForConfig(withProtobuf(config))
	if err != nil {
		return nil, err
	}
	configClientv1, err := configclientv1.NewForConfig(config)
	if err != nil {
		return nil, err
	}
	coreClientv1, err := coreclientv1.NewForConfig(withProtobuf(config))
	if err != nil {
		return nil, err
	}
	imageClientv1, err := imageclientv1.NewForConfig(config)
	if err != nil {
		return nil, err
	}
	operatorsClientv1, err := operatorsclientv1.NewForConfig(config)
	if err != nil {
		return nil, err
	}
	rbacClientv1, err := rbacclientv1.NewForConfig(withProtobuf(config))
	if err != nil {
		return nil, err
	}
	securityClientv1, err := securityclientv1.NewForConfig(config)
	if err != nil {
		return nil, err
	}
	return &clientSet{
		admissionregistrationClientv1: admissionregistrationClientv1,
		apiextensionsClientv1:         apiextensionsClientv1,
		apiregistrationClientv1:       apiregistrationClientv1,
		appsClientv1:                  appsClientv1,
		batchClientv1:                 batchClientv1,
		configClientv1:                configClientv1,
		coreClientv1:                  coreClientv1,
		imageClientv1:                 imageClientv1,
		operatorsClientv1:             operatorsClientv1,
		rbacClientv1:                  rbacClientv1,
		securityClientv1:              securityClientv1,
	}, nil
}

var clientSetCache sync.Map // *rest.Config → *clientSet

func getOrCreateClientSet(config *rest.Config) (*clientSet, error) {
	if cs, ok := clientSetCache.Load(config); ok {
		return cs.(*clientSet), nil
	}
	cs, err := newClientSet(config)
	if err != nil {
		return nil, err
	}
	actual, _ := clientSetCache.LoadOrStore(config, cs)
	return actual.(*clientSet), nil
}

func newBuilder(config *rest.Config, m manifest.Manifest) (Interface, error) {
	cs, err := getOrCreateClientSet(config)
	if err != nil {
		return nil, err
	}
	return &builder{
		raw: m.Raw,

		admissionregistrationClientv1: cs.admissionregistrationClientv1,
		apiextensionsClientv1:         cs.apiextensionsClientv1,
		apiregistrationClientv1:       cs.apiregistrationClientv1,
		appsClientv1:                  cs.appsClientv1,
		batchClientv1:                 cs.batchClientv1,
		configClientv1:                cs.configClientv1,
		coreClientv1:                  cs.coreClientv1,
		imageClientv1:                 cs.imageClientv1,
		operatorsClientv1:             cs.operatorsClientv1,
		rbacClientv1:                  cs.rbacClientv1,
		securityClientv1:              cs.securityClientv1,
	}, nil
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
	updatingMode := b.mode == UpdatingMode
	reconcilingMode := b.mode == ReconcilingMode

	switch typedObject := obj.(type) {
	case *imagev1.ImageStream:
		if b.modifier != nil {
			b.modifier(typedObject)
		}
		if deleteReq, err := resourcedelete.DeleteImageStreamv1(ctx, b.imageClientv1, typedObject,
			updatingMode); err != nil {
			return err
		} else if !deleteReq {
			if _, _, err := resourceapply.ApplyImageStreamv1(ctx, b.imageClientv1, typedObject, reconcilingMode); err != nil {
				return err
			}
		}
	case *securityv1.SecurityContextConstraints:
		if b.modifier != nil {
			b.modifier(typedObject)
		}
		if deleteReq, err := resourcedelete.DeleteSecurityContextConstraintsv1(ctx, b.securityClientv1, typedObject,
			updatingMode); err != nil {
			return err
		} else if !deleteReq {
			if _, _, err := resourceapply.ApplySecurityContextConstraintsv1(ctx, b.securityClientv1, typedObject, reconcilingMode); err != nil {
				return err
			}
		}
	case *operatorsv1.OperatorGroup:
		if b.modifier != nil {
			b.modifier(typedObject)
		}
		if deleteReq, err := resourcedelete.DeleteOperatorGroupv1(ctx, b.operatorsClientv1, typedObject,
			updatingMode); err != nil {
			return err
		} else if !deleteReq {
			if _, _, err := resourceapply.ApplyOperatorGroupv1(ctx, b.operatorsClientv1, typedObject, reconcilingMode); err != nil {
				return err
			}
		}
	case *admissionregistrationv1.ValidatingWebhookConfiguration:
		if b.modifier != nil {
			b.modifier(typedObject)
		}
		if deleteReq, err := resourcedelete.DeleteValidatingWebhookConfigurationv1(ctx, b.admissionregistrationClientv1, typedObject,
			updatingMode); err != nil {
			return err
		} else if !deleteReq {
			if _, _, err := resourceapply.ApplyValidatingWebhookConfigurationv1(ctx, b.admissionregistrationClientv1, typedObject, reconcilingMode); err != nil {
				return err
			}
		}
	case *appsv1.DaemonSet:
		if b.modifier != nil {
			b.modifier(typedObject)
		}
		if deleteReq, err := resourcedelete.DeleteDaemonSetv1(ctx, b.appsClientv1, typedObject,
			updatingMode); err != nil {
			return err
		} else if !deleteReq {
			if err := b.modifyDaemonSet(ctx, typedObject); err != nil {
				return err
			}
			if actual, _, err := resourceapply.ApplyDaemonSetv1(ctx, b.appsClientv1, typedObject, reconcilingMode); err != nil {
				return err
			} else if actual != nil {
				return b.checkDaemonSetHealth(ctx, actual)
			}
		}
	case *appsv1.Deployment:
		if b.modifier != nil {
			b.modifier(typedObject)
		}
		if deleteReq, err := resourcedelete.DeleteDeploymentv1(ctx, b.appsClientv1, typedObject,
			updatingMode); err != nil {
			return err
		} else if !deleteReq {
			if err := b.modifyDeployment(ctx, typedObject); err != nil {
				return err
			}
			if actual, _, err := resourceapply.ApplyDeploymentv1(ctx, b.appsClientv1, typedObject, reconcilingMode); err != nil {
				return err
			} else if actual != nil {
				return b.checkDeploymentHealth(ctx, actual)
			}
		}
	case *batchv1.CronJob:
		if b.modifier != nil {
			b.modifier(typedObject)
		}
		if deleteReq, err := resourcedelete.DeleteCronJobv1(ctx, b.batchClientv1, typedObject,
			updatingMode); err != nil {
			return err
		} else if !deleteReq {
			if _, _, err := resourceapply.ApplyCronJobv1(ctx, b.batchClientv1, typedObject, reconcilingMode); err != nil {
				return err
			}
		}
	case *batchv1.Job:
		if b.modifier != nil {
			b.modifier(typedObject)
		}
		if deleteReq, err := resourcedelete.DeleteJobv1(ctx, b.batchClientv1, typedObject,
			updatingMode); err != nil {
			return err
		} else if !deleteReq {
			if actual, _, err := resourceapply.ApplyJobv1(ctx, b.batchClientv1, typedObject, reconcilingMode); err != nil {
				return err
			} else if actual != nil {
				return b.checkJobHealth(ctx, actual)
			}
		}
	case *corev1.ConfigMap:
		if b.modifier != nil {
			b.modifier(typedObject)
		}
		if deleteReq, err := resourcedelete.DeleteConfigMapv1(ctx, b.coreClientv1, typedObject,
			updatingMode); err != nil {
			return err
		} else if !deleteReq {
			if err := b.modifyConfigMap(ctx, typedObject); err != nil {
				return err
			}
			if _, _, err := resourceapply.ApplyConfigMapv1(ctx, b.coreClientv1, typedObject, reconcilingMode); err != nil {
				return err
			}
		}
	case *corev1.Namespace:
		if b.modifier != nil {
			b.modifier(typedObject)
		}
		if deleteReq, err := resourcedelete.DeleteNamespacev1(ctx, b.coreClientv1, typedObject,
			updatingMode); err != nil {
			return err
		} else if !deleteReq {
			if _, _, err := resourceapply.ApplyNamespacev1(ctx, b.coreClientv1, typedObject, reconcilingMode); err != nil {
				return err
			}
		}
	case *corev1.Service:
		if b.modifier != nil {
			b.modifier(typedObject)
		}
		if deleteReq, err := resourcedelete.DeleteServicev1(ctx, b.coreClientv1, typedObject,
			updatingMode); err != nil {
			return err
		} else if !deleteReq {
			if _, _, err := resourceapply.ApplyServicev1(ctx, b.coreClientv1, typedObject, reconcilingMode); err != nil {
				return err
			}
		}
	case *corev1.ServiceAccount:
		if b.modifier != nil {
			b.modifier(typedObject)
		}
		if deleteReq, err := resourcedelete.DeleteServiceAccountv1(ctx, b.coreClientv1, typedObject,
			updatingMode); err != nil {
			return err
		} else if !deleteReq {
			if _, _, err := resourceapply.ApplyServiceAccountv1(ctx, b.coreClientv1, typedObject, reconcilingMode); err != nil {
				return err
			}
		}
	case *rbacv1.ClusterRole:
		if b.modifier != nil {
			b.modifier(typedObject)
		}
		if deleteReq, err := resourcedelete.DeleteClusterRolev1(ctx, b.rbacClientv1, typedObject,
			updatingMode); err != nil {
			return err
		} else if !deleteReq {
			if _, _, err := resourceapply.ApplyClusterRolev1(ctx, b.rbacClientv1, typedObject, reconcilingMode); err != nil {
				return err
			}
		}
	case *rbacv1.ClusterRoleBinding:
		if b.modifier != nil {
			b.modifier(typedObject)
		}
		if deleteReq, err := resourcedelete.DeleteClusterRoleBindingv1(ctx, b.rbacClientv1, typedObject,
			updatingMode); err != nil {
			return err
		} else if !deleteReq {
			if _, _, err := resourceapply.ApplyClusterRoleBindingv1(ctx, b.rbacClientv1, typedObject, reconcilingMode); err != nil {
				return err
			}
		}
	case *rbacv1.Role:
		if b.modifier != nil {
			b.modifier(typedObject)
		}
		if deleteReq, err := resourcedelete.DeleteRolev1(ctx, b.rbacClientv1, typedObject,
			updatingMode); err != nil {
			return err
		} else if !deleteReq {
			if _, _, err := resourceapply.ApplyRolev1(ctx, b.rbacClientv1, typedObject, reconcilingMode); err != nil {
				return err
			}
		}
	case *rbacv1.RoleBinding:
		if b.modifier != nil {
			b.modifier(typedObject)
		}
		if deleteReq, err := resourcedelete.DeleteRoleBindingv1(ctx, b.rbacClientv1, typedObject,
			updatingMode); err != nil {
			return err
		} else if !deleteReq {
			if _, _, err := resourceapply.ApplyRoleBindingv1(ctx, b.rbacClientv1, typedObject, reconcilingMode); err != nil {
				return err
			}
		}
	case *apiextensionsv1.CustomResourceDefinition:
		if b.modifier != nil {
			b.modifier(typedObject)
		}
		if deleteReq, err := resourcedelete.DeleteCustomResourceDefinitionv1(ctx, b.apiextensionsClientv1, typedObject,
			updatingMode); err != nil {
			return err
		} else if !deleteReq {
			if actual, _, err := resourceapply.ApplyCustomResourceDefinitionv1(ctx, b.apiextensionsClientv1, typedObject, reconcilingMode); err != nil {
				return err
			} else if actual != nil {
				return b.checkCustomResourceDefinitionHealth(ctx, actual)
			}
		}
	default:
		return fmt.Errorf("unrecognized manifest type: %T", obj)
	}

	return nil
}

func init() {
	rm := NewResourceMapper()
	rm.RegisterGVK(admissionregistrationv1.SchemeGroupVersion.WithKind("ValidatingWebhookConfiguration"), newBuilder)
	rm.RegisterGVK(apiextensionsv1.SchemeGroupVersion.WithKind("CustomResourceDefinition"), newBuilder)
	rm.RegisterGVK(appsv1.SchemeGroupVersion.WithKind("DaemonSet"), newBuilder)
	rm.RegisterGVK(appsv1.SchemeGroupVersion.WithKind("Deployment"), newBuilder)
	rm.RegisterGVK(batchv1.SchemeGroupVersion.WithKind("CronJob"), newBuilder)
	rm.RegisterGVK(batchv1.SchemeGroupVersion.WithKind("Job"), newBuilder)
	rm.RegisterGVK(corev1.SchemeGroupVersion.WithKind("ConfigMap"), newBuilder)
	rm.RegisterGVK(corev1.SchemeGroupVersion.WithKind("Namespace"), newBuilder)
	rm.RegisterGVK(corev1.SchemeGroupVersion.WithKind("Service"), newBuilder)
	rm.RegisterGVK(corev1.SchemeGroupVersion.WithKind("ServiceAccount"), newBuilder)
	rm.RegisterGVK(imagev1.SchemeGroupVersion.WithKind("ImageStream"), newBuilder)
	rm.RegisterGVK(operatorsv1.SchemeGroupVersion.WithKind("OperatorGroup"), newBuilder)
	rm.RegisterGVK(rbacv1.SchemeGroupVersion.WithKind("ClusterRole"), newBuilder)
	rm.RegisterGVK(rbacv1.SchemeGroupVersion.WithKind("ClusterRoleBinding"), newBuilder)
	rm.RegisterGVK(rbacv1.SchemeGroupVersion.WithKind("Role"), newBuilder)
	rm.RegisterGVK(rbacv1.SchemeGroupVersion.WithKind("RoleBinding"), newBuilder)
	rm.RegisterGVK(securityv1.SchemeGroupVersion.WithKind("SecurityContextConstraints"), newBuilder)
	rm.AddToMap(Mapper)
}

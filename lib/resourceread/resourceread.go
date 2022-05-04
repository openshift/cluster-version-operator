// auto-generated with generate-lib-resources.py

// Package resourceread reads supported objects from bytes.
package resourceread

import (
	imagev1 "github.com/openshift/api/image/v1"
	securityv1 "github.com/openshift/api/security/v1"
	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/serializer"
)

var (
	scheme  = runtime.NewScheme()
	codecs  = serializer.NewCodecFactory(scheme)
	decoder runtime.Decoder
)

func init() {
	if err := apiextensionsv1.AddToScheme(scheme); err != nil {
		panic(err)
	}
	if err := appsv1.AddToScheme(scheme); err != nil {
		panic(err)
	}
	if err := batchv1.AddToScheme(scheme); err != nil {
		panic(err)
	}
	if err := corev1.AddToScheme(scheme); err != nil {
		panic(err)
	}
	if err := imagev1.AddToScheme(scheme); err != nil {
		panic(err)
	}
	if err := rbacv1.AddToScheme(scheme); err != nil {
		panic(err)
	}
	if err := securityv1.AddToScheme(scheme); err != nil {
		panic(err)
	}
	decoder = codecs.UniversalDecoder(
		apiextensionsv1.SchemeGroupVersion,
		appsv1.SchemeGroupVersion,
		batchv1.SchemeGroupVersion,
		corev1.SchemeGroupVersion,
		imagev1.SchemeGroupVersion,
		rbacv1.SchemeGroupVersion,
		securityv1.SchemeGroupVersion,
	)
}

// Read reads an object from bytes.
func Read(objBytes []byte) (runtime.Object, error) {
	return runtime.Decode(decoder, objBytes)
}

// ReadOrDie reads an object from bytes.  Panics on error.
func ReadOrDie(objBytes []byte) runtime.Object {
	requiredObj, err := runtime.Decode(decoder, objBytes)
	if err != nil {
		panic(err)
	}
	return requiredObj
}

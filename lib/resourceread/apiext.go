package resourceread

import (
	apiextv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	apiextv1beta1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1beta1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/serializer"
)

var (
	apiExtensionsScheme = runtime.NewScheme()
	apiExtensionsCodecs = serializer.NewCodecFactory(apiExtensionsScheme)
)

func init() {
	if err := apiextv1beta1.AddToScheme(apiExtensionsScheme); err != nil {
		panic(err)
	}
	if err := apiextv1.AddToScheme(apiExtensionsScheme); err != nil {
		panic(err)
	}
}

// ReadCustomResourceDefinitionOrDie reads crd object from bytes as v1 or v1beta1. Panics on error.
func ReadCustomResourceDefinitionOrDie(objBytes []byte) runtime.Object {
	requiredObj, err := runtime.Decode(apiExtensionsCodecs.UniversalDecoder(apiextv1beta1.SchemeGroupVersion, apiextv1.SchemeGroupVersion), objBytes)
	if err != nil {
		panic(err)
	}
	return requiredObj
}

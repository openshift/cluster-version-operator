package resourceread

import (
	imagev1 "github.com/openshift/api/image/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/serializer"
)

var (
	imageScheme = runtime.NewScheme()
	imageCodecs = serializer.NewCodecFactory(imageScheme)
)

func init() {
	if err := imagev1.AddToScheme(imageScheme); err != nil {
		panic(err)
	}
}

// ReadImageStreamV1OrDie reads imagestream object from bytes. Panics on error.
func ReadImageStreamV1OrDie(objBytes []byte) *imagev1.ImageStream {
	requiredObj, err := runtime.Decode(imageCodecs.UniversalDecoder(imagev1.SchemeGroupVersion), objBytes)
	if err != nil {
		panic(err)
	}
	return requiredObj.(*imagev1.ImageStream)
}

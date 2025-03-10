// Package dependencymagnet adds nominal Go dependencies so 'go mod'
// will pull in content we do not actually need to compile our Go, but
// which we do need to build our container image.
package dependencymagnet

import (
	_ "github.com/openshift/api/config/v1/zz_generated.crd-manifests"
	_ "github.com/openshift/api/operator/v1alpha1/zz_generated.crd-manifests"
	_ "github.com/openshift/api/update/v1alpha1/zz_generated.crd-manifests"
)

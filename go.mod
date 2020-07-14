module github.com/openshift/cluster-version-operator

go 1.13

replace golang.org/x/text => golang.org/x/text v0.3.3

require (
	github.com/blang/semver v3.5.0+incompatible
	github.com/davecgh/go-spew v1.1.1
	github.com/evanphx/json-patch v4.5.0+incompatible // indirect
	github.com/ghodss/yaml v1.0.0
	github.com/gogo/protobuf v1.3.0 // indirect
	github.com/golang/groupcache v0.0.0-20191002201903-404acd9df4cc // indirect
	github.com/google/go-cmp v0.3.1 // indirect
	github.com/google/uuid v1.1.1
	github.com/googleapis/gnostic v0.3.1 // indirect
	github.com/hashicorp/golang-lru v0.5.3 // indirect
	github.com/imdario/mergo v0.3.8 // indirect
	github.com/openshift/api v0.0.0-20191028120151-c556078b427f
	github.com/openshift/client-go v0.0.0-20191001081553-3b0e988f8cb0
	github.com/pkg/errors v0.8.1
	github.com/prometheus/client_golang v1.1.0
	github.com/prometheus/client_model v0.0.0-20190812154241-14fe0d1b01d4
	github.com/prometheus/common v0.7.0 // indirect
	github.com/prometheus/procfs v0.0.5 // indirect
	github.com/spf13/cobra v0.0.5
	golang.org/x/crypto v0.0.0-20191002192127-34f69633bfdc
	golang.org/x/sys v0.0.0-20191003212358-c178f38b412c // indirect
	golang.org/x/time v0.0.0-20190921001708-c4c64cad1fd0
	google.golang.org/appengine v1.6.4 // indirect
	k8s.io/api v0.17.3
	k8s.io/apiextensions-apiserver v0.17.0
	k8s.io/apimachinery v0.17.3
	k8s.io/client-go v0.17.3
	k8s.io/klog v1.0.0
	k8s.io/kube-aggregator v0.17.0
	k8s.io/utils v0.0.0-20191114184206-e782cd3c129f
)

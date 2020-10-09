module github.com/openshift/cluster-version-operator

go 1.13

replace golang.org/x/text => golang.org/x/text v0.3.3

require (
	github.com/blang/semver v3.5.0+incompatible
	github.com/davecgh/go-spew v1.1.1
	github.com/ghodss/yaml v1.0.0
	github.com/golang/groupcache v0.0.0-20191002201903-404acd9df4cc // indirect
	github.com/google/go-cmp v0.3.1 // indirect
	github.com/google/uuid v1.1.1
	github.com/googleapis/gnostic v0.3.1 // indirect
	github.com/hashicorp/golang-lru v0.5.3 // indirect
	github.com/imdario/mergo v0.3.8 // indirect
	github.com/openshift/api v0.0.0-20200521101457-60c476765272
	github.com/openshift/client-go v0.0.0-20200521150516-05eb9880269c
	github.com/pkg/errors v0.8.1
	github.com/prometheus/client_golang v1.1.0
	github.com/prometheus/client_model v0.2.0
	github.com/prometheus/common v0.7.0 // indirect
	github.com/prometheus/procfs v0.0.5 // indirect
	github.com/spf13/cobra v0.0.5
	golang.org/x/crypto v0.0.0-20200220183623-bac4c82f6975
	golang.org/x/time v0.0.0-20190921001708-c4c64cad1fd0
	google.golang.org/appengine v1.6.4 // indirect
	k8s.io/api v0.18.9
	k8s.io/apiextensions-apiserver v0.18.9
	k8s.io/apimachinery v0.18.9
	k8s.io/client-go v0.18.9
	k8s.io/klog v1.0.0
	k8s.io/kube-aggregator v0.18.9
	k8s.io/utils v0.0.0-20200324210504-a9aa75ae1b89
)

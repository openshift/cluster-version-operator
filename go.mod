module github.com/openshift/cluster-version-operator

go 1.15

require (
	github.com/blang/semver/v4 v4.0.0
	github.com/davecgh/go-spew v1.1.1
	github.com/ghodss/yaml v1.0.0
	github.com/google/go-cmp v0.5.5
	github.com/google/uuid v1.1.2
	github.com/imdario/mergo v0.3.8 // indirect
	github.com/openshift/api v0.0.0-20220525145417-ee5b62754c68
	github.com/openshift/client-go v0.0.0-20220603133046-984ee5ebedcf
	github.com/openshift/library-go v0.0.0-20220407182450-db47826e7275
	github.com/pkg/errors v0.9.1
	github.com/prometheus/client_golang v1.11.0
	github.com/prometheus/client_model v0.2.0
	github.com/prometheus/common v0.28.0
	github.com/spf13/cobra v1.2.1
	golang.org/x/net v0.0.0-20220127200216-cd36cc0744dd
	golang.org/x/time v0.0.0-20220210224613-90d013bbcef8
	gopkg.in/fsnotify.v1 v1.4.7
	k8s.io/api v0.24.0
	k8s.io/apiextensions-apiserver v0.23.0
	k8s.io/apimachinery v0.24.0
	k8s.io/client-go v0.24.0
	k8s.io/klog/v2 v2.60.1
	k8s.io/kube-aggregator v0.23.0
	k8s.io/utils v0.0.0-20220210201930-3a6ce19ff2f9
)

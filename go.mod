module github.com/openshift/cluster-version-operator

go 1.15

require (
	github.com/blang/semver/v4 v4.0.0
	github.com/davecgh/go-spew v1.1.1
	github.com/ghodss/yaml v1.0.0
	github.com/google/go-cmp v0.5.5
	github.com/google/uuid v1.1.2
	github.com/imdario/mergo v0.3.8 // indirect
	github.com/openshift/api v0.0.0-20210923172539-00988ef88ee0
	github.com/openshift/client-go v0.0.0-20200827190008-3062137373b5
	github.com/openshift/library-go v0.0.0-20201013192036-5bd7c282e3e7
	github.com/pkg/errors v0.9.1
	github.com/prometheus/client_golang v1.11.0
	github.com/prometheus/client_model v0.2.0
	github.com/spf13/cobra v1.1.3
	golang.org/x/net v0.0.0-20210520170846-37e1c6afe023
	golang.org/x/time v0.0.0-20210723032227-1f47c861a9ac
	k8s.io/api v0.22.1
	k8s.io/apiextensions-apiserver v0.22.1
	k8s.io/apimachinery v0.22.1
	k8s.io/client-go v0.22.1
	k8s.io/klog/v2 v2.9.0
	k8s.io/kube-aggregator v0.22.1
	k8s.io/utils v0.0.0-20210707171843-4b05e18ac7d9
)

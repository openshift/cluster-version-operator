module github.com/openshift/cluster-version-operator

go 1.15

require (
	github.com/blang/semver/v4 v4.0.0
	github.com/cockroachdb/cmux v0.0.0-20170110192607-30d10be49292
	github.com/davecgh/go-spew v1.1.1
	github.com/evanphx/json-patch v4.5.0+incompatible // indirect
	github.com/ghodss/yaml v1.0.0
	github.com/google/uuid v1.1.1
	github.com/hashicorp/golang-lru v0.5.3 // indirect
	github.com/imdario/mergo v0.3.8 // indirect
	github.com/openshift/api v0.0.0-20200724204552-3ae6754513d4
	github.com/openshift/client-go v0.0.0-20200723130357-94e1065ab1f8
	github.com/openshift/library-go v0.0.0-20200724192307-1ed21c4fa86c
	github.com/pkg/errors v0.9.1
	github.com/prometheus/client_golang v1.7.1
	github.com/prometheus/client_model v0.2.0
	github.com/spf13/cobra v1.0.0
	golang.org/x/crypto v0.0.0-20200622213623-75b288015ac9
	golang.org/x/time v0.0.0-20191024005414-555d28b269f0
	k8s.io/api v0.19.0-rc.2
	k8s.io/apiextensions-apiserver v0.19.0-rc.2
	k8s.io/apimachinery v0.19.0-rc.2
	k8s.io/client-go v0.19.0-rc.2
	k8s.io/klog/v2 v2.3.0
	k8s.io/kube-aggregator v0.19.0-rc.2
	k8s.io/utils v0.0.0-20200720150651-0bdb4ca86cbc
)

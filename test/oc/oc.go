package oc

import (
	"github.com/openshift/cluster-version-operator/test/oc/api"
	"github.com/openshift/cluster-version-operator/test/oc/cli"
)

// NewOC returns OC that provides utility functions used by the e2e tests
func NewOC(o api.Options) (api.OC, error) {
	return cli.NewOCCli(o)
}

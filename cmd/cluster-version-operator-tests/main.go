package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/openshift-eng/openshift-tests-extension/pkg/cmd"
	"github.com/openshift-eng/openshift-tests-extension/pkg/extension"
	g "github.com/openshift-eng/openshift-tests-extension/pkg/ginkgo"

	_ "github.com/openshift/cluster-version-operator/test/cvo"
)

func main() {
	registry := extension.NewRegistry()
	ext := extension.NewExtension("openshift", "payload", "cluster-version-operator")

	ext.AddSuite(extension.Suite{
		Name: "cluster-version-operator",
	})

	// BuildExtensionTestSpecsFromOpenShiftGinkgoSuite is currently available only in the openshift fork of ginkgo
	// See the "replace" directive in go.mod copied from
	// https://github.com/openshift-eng/openshift-tests-extension/blob/8ef37c67e666954f9b95e52320c9c20d6b53d241/go.mod#L37
	specs, err := g.BuildExtensionTestSpecsFromOpenShiftGinkgoSuite()
	if err != nil {
		panic(fmt.Sprintf("couldn't build extension test specs from ginkgo: %+v", err.Error()))
	}

	ext.AddSpecs(specs)
	registry.Register(ext)

	root := &cobra.Command{
		Long: "OpenShift Tests Extension for Cluster Version Operator",
	}
	root.AddCommand(cmd.DefaultExtensionCommands(registry)...)

	if err := func() error {
		return root.Execute()
	}(); err != nil {
		os.Exit(1)
	}
}

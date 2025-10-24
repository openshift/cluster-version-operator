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

	// Parallel tests must be able to run alongside any other test, and not be disruptive to the cluster’s normal operation.
	// Tests should be as fast as possible; and less than 5 minutes in duration -- and typically much shorter.
	// Longer tests need approval from the OCP architects.
	ext.AddSuite(extension.Suite{
		Name:    "openshift/cluster-version-operator/conformance/parallel",
		Parents: []string{"openshift/conformance/parallel"},
		Qualifiers: []string{
			`!(name.contains("[Serial]") || name.contains("[Slow]"))`,
		},
	})

	// Serial tests run in isolation, however they must return the cluster to its original state upon exiting.
	ext.AddSuite(extension.Suite{
		Name:    "openshift/cluster-version-operator/conformance/serial",
		Parents: []string{"openshift/conformance/serial"},
		Qualifiers: []string{
			`name.contains("[Serial]")`,
		},
	})

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

package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/openshift-eng/openshift-tests-extension/pkg/cmd"
	"github.com/openshift-eng/openshift-tests-extension/pkg/extension"
	g "github.com/openshift-eng/openshift-tests-extension/pkg/ginkgo"
	exutil "github.com/openshift/cluster-version-operator/test/util"
	e2e "k8s.io/kubernetes/test/e2e/framework"

	_ "github.com/openshift/cluster-version-operator/test/cvo"
)

func main() {
	registry := extension.NewRegistry()
	ext := extension.NewExtension("openshift", "payload", "cluster-version-operator")

	ext.AddSuite(extension.Suite{
		Name: "cluster-version-operator",
	})

	ext.AddSuite(extension.Suite{
		Name:    "cvo/parallel",
		Parents: []string{"openshift/conformance/parallel"},
		Qualifiers: []string{
			`!name.contains("[Serial]")`,
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

	specs.AddBeforeAll(func() {
		if err := exutil.InitTest(false); err != nil {
			panic(err)
		}
		e2e.AfterReadingAllFlags(exutil.TestContext)
	})

	root.AddCommand(cmd.DefaultExtensionCommands(registry)...)

	if err := func() error {
		return root.Execute()
	}(); err != nil {
		os.Exit(1)
	}
}

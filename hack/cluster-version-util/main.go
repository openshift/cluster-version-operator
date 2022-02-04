package main

import (
	"log"

	"github.com/spf13/cobra"

	"github.com/openshift/cluster-version-operator/hack/cluster-version-util/releasediff"
)

func main() {
	rootCmd := &cobra.Command{
		Use:   "cluster-version-util",
		Short: "Utilities for understanding cluster-version operator functionality.  Not for production use.",
	}

	rootCmd.AddCommand(newTaskGraphCmd())
	rootCmd.AddCommand(releasediff.NewReleaseResourceDiffCmd())

	if err := rootCmd.Execute(); err != nil {
		log.Fatal(err)
	}
}

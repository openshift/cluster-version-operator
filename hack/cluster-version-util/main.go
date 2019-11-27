package main

import (
	"log"

	"github.com/spf13/cobra"
)

func main() {
	rootCmd := &cobra.Command{
		Use:   "cluster-version-util",
		Short: "Utilities for understanding cluster-version operator functionality.  Not for production use.",
	}

	rootCmd.AddCommand(newTaskGraphCmd())

	if err := rootCmd.Execute(); err != nil {
		log.Fatal(err)
	}
}

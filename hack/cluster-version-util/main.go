package main

import (
	"flag"

	"github.com/spf13/cobra"

	"k8s.io/klog/v2"
)

var (
	rootCmd = &cobra.Command{
		Use:   "cluster-version-util",
		Short: "Utilities for understanding cluster-version operator functionality.  Not for production use.",
	}
)

func init() {
	klog.InitFlags(flag.CommandLine)
	_ = flag.CommandLine.Set("alsologtostderr", "true")
	rootCmd.PersistentFlags().AddGoFlagSet(flag.CommandLine)
}

func main() {
	rootCmd.AddCommand(newTaskGraphCmd())

	if err := rootCmd.Execute(); err != nil {
		klog.Exitf("Error executing: %v", err)
	}
}

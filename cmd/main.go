package main

import (
	"flag"

	"github.com/spf13/cobra"
	"k8s.io/klog"
)

var (
	rootCmd = &cobra.Command{
		Use:   "cluster-version-operator",
		Short: "Run Cluster Version Controller",
		Long:  "",
	}
)

func init() {
	klog.InitFlags(flag.CommandLine)
	flag.CommandLine.Set("alsologtostderr", "true")
	rootCmd.PersistentFlags().AddGoFlagSet(flag.CommandLine)
}

func main() {
	defer klog.Flush()
	if err := rootCmd.Execute(); err != nil {
		klog.Exitf("Error executing mcc: %v", err)
	}
}

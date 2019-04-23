package main

import (
	"flag"

	"github.com/spf13/cobra"
	"k8s.io/klog"
)

const (
	componentName      = "version"
	componentNamespace = "openshift-cluster-version"
)

var (
	rootCmd = &cobra.Command{
		Use:   componentName,
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

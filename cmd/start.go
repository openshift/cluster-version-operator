package main

import (
	"github.com/spf13/cobra"
	"k8s.io/klog"

	"github.com/openshift/cluster-version-operator/pkg/start"
	"github.com/openshift/cluster-version-operator/pkg/version"
)

func init() {
	opts := start.NewOptions()
	cmd := &cobra.Command{
		Use:   "start",
		Short: "Starts Cluster Version Operator",
		Long:  "",
		Run: func(cmd *cobra.Command, args []string) {
			// To help debugging, immediately log version
			klog.Infof("%s", version.String)

			if err := opts.Run(); err != nil {
				klog.Fatalf("error: %v", err)
			}
		},
	}

	cmd.PersistentFlags().StringVar(&opts.ListenAddr, "listen", opts.ListenAddr, "Address to listen on for metrics")
	cmd.PersistentFlags().StringVar(&opts.Kubeconfig, "kubeconfig", opts.Kubeconfig, "Kubeconfig file to access a remote cluster (testing only)")
	cmd.PersistentFlags().StringVar(&opts.NodeName, "node-name", opts.NodeName, "kubernetes node name CVO is scheduled on.")
	cmd.PersistentFlags().BoolVar(&opts.EnableAutoUpdate, "enable-auto-update", opts.EnableAutoUpdate, "Enables the autoupdate controller.")
	cmd.PersistentFlags().StringVar(&opts.ReleaseImage, "release-image", opts.ReleaseImage, "The Openshift release image url.")
	rootCmd.AddCommand(cmd)
}

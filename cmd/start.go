package main

import (
	"context"

	"github.com/spf13/cobra"
	"k8s.io/klog/v2"

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
			klog.Info(version.String)

			if err := opts.Run(context.Background()); err != nil {
				klog.Fatalf("error: %v", err)
			}
			klog.Infof("Graceful shutdown complete for %s.", version.String)
		},
	}

	cmd.PersistentFlags().StringVar(&opts.ListenAddr, "listen", opts.ListenAddr, "Address to listen on for metrics")
	cmd.PersistentFlags().StringVar(&opts.Kubeconfig, "kubeconfig", opts.Kubeconfig, "Kubeconfig file to access a remote cluster (testing only)")
	cmd.PersistentFlags().StringVar(&opts.NodeName, "node-name", opts.NodeName, "kubernetes node name CVO is scheduled on.")
	cmd.PersistentFlags().BoolVar(&opts.EnableAutoUpdate, "enable-auto-update", opts.EnableAutoUpdate, "Enables the autoupdate controller.")
	cmd.PersistentFlags().BoolVar(&opts.WaitForClusterVersion, "wait-for-cluster-version", opts.WaitForClusterVersion, "Blocks manifest reconciliation until the ClusterVersion resource exists.")
	cmd.PersistentFlags().StringVar(&opts.ReleaseImage, "release-image", opts.ReleaseImage, "The Openshift release image url.")
	cmd.PersistentFlags().StringVar(&opts.ServingCertFile, "serving-cert-file", opts.ServingCertFile, "The X.509 certificate file for serving metrics over HTTPS.  You must set both --serving-cert-file and --serving-key-file unless you set --listen empty.")
	cmd.PersistentFlags().StringVar(&opts.ServingKeyFile, "serving-key-file", opts.ServingKeyFile, "The X.509 key file for serving metrics over HTTPS.  You must set both --serving-cert-file and --serving-key-file unless you set --listen empty.")
	rootCmd.AddCommand(cmd)
}

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
	cmd.PersistentFlags().BoolVar(&opts.EnableAutoUpgrade, "enable-auto-upgrade", opts.EnableAutoUpgrade, "Enables the autoupgrade controller.")
	cmd.PersistentFlags().BoolVar(&opts.EnableDefaultClusterVersion, "enable-default-cluster-version", opts.EnableDefaultClusterVersion, "Allows the operator to create a ClusterVersion object if one does not already exist.")
	cmd.PersistentFlags().StringVar(&opts.ReleaseImage, "release-image", opts.ReleaseImage, "The Openshift release image url.")
	cmd.PersistentFlags().StringVar(&opts.ServingCertFile, "serving-cert-file", opts.ServingCertFile, "The X.509 certificate file for serving metrics over HTTPS.  You must set both --serving-cert-file and --serving-key-file, or neither.")
	cmd.PersistentFlags().StringVar(&opts.ServingKeyFile, "serving-key-file", opts.ServingKeyFile, "The X.509 key file for serving metrics over HTTPS.  You must set both --serving-cert-file and --serving-key-file, or neither.")
	rootCmd.AddCommand(cmd)
}

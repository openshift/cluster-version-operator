package main

import (
	"context"
	"math/rand"
	"time"

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

			//Setting seed for all the calls to rand methods in the code
			rand.Seed(time.Now().UnixNano())

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
	cmd.PersistentFlags().StringVar(&opts.ReleaseImage, "release-image", opts.ReleaseImage, "The Openshift release image url.")
	cmd.PersistentFlags().StringVar(&opts.ServingCertFile, "serving-cert-file", opts.ServingCertFile, "The X.509 certificate file for serving metrics over HTTPS.  You must set both --serving-cert-file and --serving-key-file unless you set --listen empty.")
	cmd.PersistentFlags().StringVar(&opts.ServingKeyFile, "serving-key-file", opts.ServingKeyFile, "The X.509 key file for serving metrics over HTTPS.  You must set both --serving-cert-file and --serving-key-file unless you set --listen empty.")
	cmd.PersistentFlags().StringVar(&opts.PromQLTarget.CABundleFile, "metrics-ca-bundle-file", opts.PromQLTarget.CABundleFile, "The service CA bundle file containing one or more X.509 certificate files for validating certificates generated from the service CA for the respective remote PromQL query service.")
	cmd.PersistentFlags().StringVar(&opts.PromQLTarget.BearerTokenFile, "metrics-token-file", opts.PromQLTarget.BearerTokenFile, "The bearer token file used to access the remote PromQL query service.")
	cmd.PersistentFlags().StringVar(&opts.PromQLTarget.KubeSvc.Namespace, "metrics-namespace", opts.PromQLTarget.KubeSvc.Namespace, "The name of the namespace where the the remote PromQL query service resides. Must be specified when --use-dns-for-services is disabled.")
	cmd.PersistentFlags().StringVar(&opts.PromQLTarget.KubeSvc.Name, "metrics-service", opts.PromQLTarget.KubeSvc.Name, "The name of the remote PromQL query service. Must be specified when --use-dns-for-services is disabled.")
	cmd.PersistentFlags().BoolVar(&opts.PromQLTarget.UseDNS, "use-dns-for-services", opts.PromQLTarget.UseDNS, "Configures the CVO to use DNS for resolution of services in the cluster.")
	cmd.PersistentFlags().StringVar(&opts.PrometheusURLString, "metrics-url", opts.PrometheusURLString, "The URL used to access the remote PromQL query service.")
	cmd.PersistentFlags().BoolVar(&opts.InjectClusterIdIntoPromQL, "hypershift", opts.InjectClusterIdIntoPromQL, "This options indicates whether the CVO is running inside a hosted control plane.")
	cmd.PersistentFlags().StringVar(&opts.UpdateService, "update-service", opts.UpdateService, "The preferred update service.  If set, this option overrides any upstream value configured in ClusterVersion spec.")
	cmd.PersistentFlags().StringSliceVar(&opts.AlwaysEnableCapabilities, "always-enable-capabilities", opts.AlwaysEnableCapabilities, "List of the cluster capabilities which will always be implicitly enabled.")
	rootCmd.AddCommand(cmd)
}

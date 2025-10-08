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
		PreRunE: func(_ *cobra.Command, _ []string) error {
			// To help debugging, immediately log version
			klog.Info(version.String)
			return opts.ValidateAndComplete()
		},
		Run: func(_ *cobra.Command, _ []string) {
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

	// metrics serving options
	cmd.PersistentFlags().StringVar(&opts.ServingCertFile, "serving-cert-file", opts.ServingCertFile, "The X.509 certificate file for serving metrics over HTTPS.  You must set both --serving-cert-file and --serving-key-file unless you set --listen empty.")
	cmd.PersistentFlags().StringVar(&opts.ServingKeyFile, "serving-key-file", opts.ServingKeyFile, "The X.509 key file for serving metrics over HTTPS.  You must set both --serving-cert-file and --serving-key-file unless you set --listen empty.")
	cmd.PersistentFlags().StringVar(&opts.ServingClientCAFile, "serving-client-certificate-authorities-file", opts.ServingClientCAFile, "The X.509 certificates file containing a series of PEM encoded certificates for Certificate Authorities trusted to sign metrics client certificates.  If set, only clients who provide mTLS client certs signed by those CAs will be trusted.  If unset, only clients with valid tokens for the prometheus-k8s ServiceAccount in the openshift-monitoring namespace will be trusted.")

	// metrics/PromQL client options
	cmd.PersistentFlags().StringVar(&opts.PromQLTarget.CABundleFile, "metrics-ca-bundle-file", opts.PromQLTarget.CABundleFile, "The service CA bundle file containing one or more X.509 certificate files for validating certificates generated from the service CA for the respective remote PromQL query service.")
	cmd.PersistentFlags().StringVar(&opts.PromQLTarget.BearerTokenFile, "metrics-token-file", opts.PromQLTarget.BearerTokenFile, "The bearer token file used to access the remote PromQL query service.")
	cmd.PersistentFlags().StringVar(&opts.PromQLTarget.KubeSvc.Namespace, "metrics-namespace", opts.PromQLTarget.KubeSvc.Namespace, "The name of the namespace where the the remote PromQL query service resides. Must be specified when --use-dns-for-services is disabled.")
	cmd.PersistentFlags().StringVar(&opts.PromQLTarget.KubeSvc.Name, "metrics-service", opts.PromQLTarget.KubeSvc.Name, "The name of the remote PromQL query service. Must be specified when --use-dns-for-services is disabled.")
	cmd.PersistentFlags().BoolVar(&opts.PromQLTarget.UseDNS, "use-dns-for-services", opts.PromQLTarget.UseDNS, "Configures the CVO to use DNS for resolution of services in the cluster.")
	cmd.PersistentFlags().StringVar(&opts.PrometheusURLString, "metrics-url", opts.PrometheusURLString, "The URL used to access the remote PromQL query service.")

	cmd.PersistentFlags().BoolVar(&opts.HyperShift, "hypershift", opts.HyperShift, "This options indicates whether the CVO is running inside a hosted control plane.")
	cmd.PersistentFlags().StringVar(&opts.UpdateService, "update-service", opts.UpdateService, "The preferred update service.  If set, this option overrides any upstream value configured in ClusterVersion spec.")
	cmd.PersistentFlags().StringSliceVar(&opts.AlwaysEnableCapabilities, "always-enable-capabilities", opts.AlwaysEnableCapabilities, "List of the cluster capabilities which will always be implicitly enabled.")
	rootCmd.AddCommand(cmd)
}

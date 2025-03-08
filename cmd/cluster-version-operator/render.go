package main

import (
	"flag"
	"os"

	"github.com/spf13/cobra"
	"k8s.io/klog/v2"

	"github.com/openshift/cluster-version-operator/pkg/payload"
)

var (
	renderCmd = &cobra.Command{
		Use:   "render",
		Short: "Renders the UpdatePayload to disk.",
		Long:  "",
		Run:   runRenderCmd,
	}

	renderOpts struct {
		releaseImage            string
		featureGateManifestPath string
		outputDir               string
	}
)

func init() {
	rootCmd.AddCommand(renderCmd)
	renderCmd.PersistentFlags().StringVar(&renderOpts.outputDir, "output-dir", "", "The output directory where the manifests will be rendered.")
	renderCmd.PersistentFlags().StringVar(&renderOpts.releaseImage, "release-image", "", "The Openshift release image url.")
	renderCmd.PersistentFlags().StringVar(&renderOpts.featureGateManifestPath, "feature-gate-manifest-path", "", "FeatureGate manifest input path.")
}

func runRenderCmd(cmd *cobra.Command, args []string) {
	_ = flag.Set("logtostderr", "true")
	flag.Parse()

	if renderOpts.outputDir == "" {
		klog.Fatalf("missing --output-dir flag, it is required")
	}
	if renderOpts.releaseImage == "" {
		klog.Fatalf("missing --release-image flag, it is required")
	}
	if err := payload.Render(renderOpts.outputDir, renderOpts.releaseImage, renderOpts.featureGateManifestPath, clusterProfile()); err != nil {
		klog.Fatalf("Render command failed: %v", err)
	}
}

func clusterProfile() string {
	profile, ok := os.LookupEnv("CLUSTER_PROFILE")
	if !ok {
		return payload.DefaultClusterProfile
	}
	return profile
}

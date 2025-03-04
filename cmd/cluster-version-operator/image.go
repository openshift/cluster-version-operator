package main

import (
	"fmt"

	"github.com/spf13/cobra"
	"k8s.io/klog/v2"

	"github.com/openshift/cluster-version-operator/pkg/payload"
)

var (
	imageCmd = &cobra.Command{
		Use:     "image",
		Short:   "Returns image for requested short-name from UpdatePayload",
		Long:    "",
		Example: "%[1] image <short-name>",
		Run:     runImageCmd,
	}
)

func init() {
	rootCmd.AddCommand(imageCmd)
}

func runImageCmd(cmd *cobra.Command, args []string) {
	if len(args) == 0 {
		klog.Fatalf("missing command line argument short-name")
	}
	image, err := payload.ImageForShortName(args[0])
	if err != nil {
		klog.Fatalf("error: %v", err)
	}
	fmt.Print(image)
}

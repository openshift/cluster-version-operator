package main

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/openshift/cluster-version-operator/pkg/version"
)

var (
	versionCmd = &cobra.Command{
		Use:   "version",
		Short: "Print the version number of Cluster Version Operator",
		Long:  `All software has versions. This is Cluster Version Operator's.`,
		Run:   runVersionCmd,
	}
)

func init() {
	rootCmd.AddCommand(versionCmd)
}

func runVersionCmd(cmd *cobra.Command, args []string) {
	fmt.Println(version.String)
}

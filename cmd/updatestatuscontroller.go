package main

import (
	"context"

	"github.com/openshift/cluster-version-operator/pkg/updatestatus"
	"github.com/openshift/cluster-version-operator/pkg/version"
	"github.com/openshift/library-go/pkg/controller/controllercmd"
)

func init() {
	uscCommand := controllercmd.NewControllerCommandConfig(
		"update-status-controller",
		version.Get(),
		updatestatus.Run,
	).NewCommandWithContext(context.Background())
	uscCommand.Short = "Start the update status controller"
	uscCommand.Use = "update-status-controller"

	rootCmd.AddCommand(uscCommand)
}

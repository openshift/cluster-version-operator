package main

import (
	"context"

	"github.com/openshift/library-go/pkg/controller/controllercmd"

	"github.com/openshift/cluster-version-operator/pkg/updatestatus"
	"github.com/openshift/cluster-version-operator/pkg/version"
)

func init() {
	uscCommand := controllercmd.NewControllerCommandConfig(
		"update-status-controller",
		version.Get(),
		updatestatus.Run,
	).NewCommandWithContext(context.Background())

	uscCommand.Short = "The Update Status Controller watches cluster state/health during the update process and exposes it through the UpdateStatus API."
	uscCommand.Use = "update-status-controller"

	rootCmd.AddCommand(uscCommand)
}

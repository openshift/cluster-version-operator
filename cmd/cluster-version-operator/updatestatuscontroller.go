package main

import (
	"context"

	k8sversion "k8s.io/apimachinery/pkg/version"
	"k8s.io/utils/clock"

	"github.com/openshift/library-go/pkg/controller/controllercmd"

	"github.com/openshift/cluster-version-operator/pkg/updatestatus"
	cvoversion "github.com/openshift/cluster-version-operator/pkg/version"
)

func init() {
	uscCommand := controllercmd.NewControllerCommandConfig(
		"update-status-controller",
		// TODO(USC: TechPreview): Unify version handling, potentially modernize CVO pkg/version
		//                         to use k8sversion.Info too.
		// https://github.com/openshift/cluster-version-operator/pull/1091#discussion_r1810601697
		k8sversion.Info{GitVersion: cvoversion.Raw},
		updatestatus.Run,
		clock.RealClock{},
	).NewCommandWithContext(context.Background())

	uscCommand.Short = "The Update Status Controller watches cluster state/health during the update process and exposes it through the UpdateStatus API."
	uscCommand.Use = "update-status-controller"

	rootCmd.AddCommand(uscCommand)
}

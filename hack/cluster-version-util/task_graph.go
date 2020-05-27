package main

import (
	"fmt"
	"math/rand"
	"time"

	"github.com/spf13/cobra"

	"github.com/openshift/cluster-version-operator/pkg/payload"
)

var (
	taskGraphOpts struct {
		parallel string
	}
)

func newTaskGraphCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "task-graph PATH",
		Short: "Render a task graph for a release image extracted into PATH",
		Args:  cobra.ExactArgs(1),
		RunE:  runTaskGraphCmd,
	}

	cmd.Flags().StringVar(&taskGraphOpts.parallel, "parallel", "by-number-and-component", "The output directory where the manifests will be rendered.")
	return cmd
}

func runTaskGraphCmd(cmd *cobra.Command, args []string) error {
	manifestDir := args[0]
	release, err := payload.LoadUpdate(manifestDir, "", "")
	if err != nil {
		return err
	}

	total := len(release.Manifests)
	var tasks []*payload.Task
	for i := range release.Manifests {
		tasks = append(tasks, &payload.Task{
			Index:    i + 1,
			Total:    total,
			Manifest: &release.Manifests[i],
		})
	}
	graph := payload.NewTaskGraph(tasks)
	graph.Split(payload.SplitOnJobs)

	switch taskGraphOpts.parallel {
	case "by-number-and-component":
		graph.Parallelize(payload.ByNumberAndComponent)
	case "flatten-by-number-and-component":
		graph.Parallelize(payload.FlattenByNumberAndComponent)
	case "permute-flatten-by-number-and-component":
		r := rand.New(rand.NewSource(time.Now().UnixNano()))
		graph.Parallelize(payload.PermuteOrder(payload.FlattenByNumberAndComponent, r))
	default:
		return fmt.Errorf("unrecognized parallel strategy %q", taskGraphOpts.parallel)
	}

	fmt.Println(graph.Tree())

	return nil
}

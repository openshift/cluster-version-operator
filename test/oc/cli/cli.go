package cli

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/go-logr/logr"

	"github.com/openshift/cluster-version-operator/test/oc/api"
)

type client struct {
	logger   logr.Logger
	executor executor
}

type executor interface {
	Run(args ...string) ([]byte, error)
}

type ocExecutor struct {
	// logger is used to log oc commands
	logger logr.Logger
	// oc is the path to the oc binary
	oc string
	// execute executes a command
	execute func(dir, command string, args ...string) ([]byte, error)
}

func (e *ocExecutor) Run(args ...string) ([]byte, error) {
	logger := e.logger.WithValues("cmd", e.oc, "args", strings.Join(args, " "))
	b, err := e.execute("", e.oc, args...)
	if err != nil {
		logger.Error(err, "Running command failed", "output", string(b))
	} else {
		logger.Info("Running command succeeded.")
	}
	return b, err
}

func newOCExecutor(oc string, timeout time.Duration, logger logr.Logger) (executor, error) {
	return &ocExecutor{
		logger: logger,
		oc:     oc,
		execute: func(dir, command string, args ...string) ([]byte, error) {
			ctx, cancel := context.WithTimeout(context.Background(), timeout)
			defer cancel()
			c := exec.CommandContext(ctx, command, args...)
			c.Dir = dir
			o, err := c.CombinedOutput()
			if errors.Is(ctx.Err(), context.DeadlineExceeded) {
				return o, fmt.Errorf("execution timed out after %s: %w", timeout.String(), ctx.Err())
			}
			return o, err
		},
	}, nil
}

// NewOCCli return a client for oc-cli.
func NewOCCli(o api.Options) (api.OC, error) {
	oc, err := exec.LookPath("oc")
	if err != nil {
		return nil, err
	}
	if o.Timeout == 0 {
		o.Timeout = 30 * time.Second
	}

	executor, err := newOCExecutor(oc, o.Timeout, o.Logger)
	if err != nil {
		return nil, err
	}
	ret := client{
		logger:   o.Logger,
		executor: executor,
	}
	return &ret, nil
}

func (c *client) AdmReleaseExtract(o api.ReleaseExtractOptions) error {
	args := []string{"adm", "release", "extract", fmt.Sprintf("--to=%s", o.To)}
	_, err := c.executor.Run(args...)
	if err != nil {
		return err
	}
	return nil
}

func (c *client) Version(o api.VersionOptions) (string, error) {
	args := []string{"version", fmt.Sprintf("--client=%t", o.Client)}
	output, err := c.executor.Run(args...)
	if err != nil {
		return "", err
	}
	return string(output), nil
}

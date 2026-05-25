package api

import (
	"time"

	"github.com/go-logr/logr"
)

type ReleaseExtractOptions struct {
	To string
}

type VersionOptions struct {
	Client bool
}

type Options struct {
	Logger  logr.Logger
	Timeout time.Duration
}

type OC interface {
	AdmReleaseExtract(o ReleaseExtractOptions) error
	Version(o VersionOptions) (string, error)

	// AdmWaitForStableCluster runs oc adm wait-for-stable-cluster
	// Non-Empty minimumStablePeriod or timeout overrides the default value in the command
	AdmWaitForStableCluster(minimumStablePeriod, timeout string) (string, error)
}

package api

import (
	"time"

	"github.com/go-logr/logr"
)

type ReleaseExtractOptions struct {
	To string
}

type ReleaseInfoOptions struct {
	Image string
}

type VersionOptions struct {
	Client bool
}

type ExtractOptions struct {
	Resource  string
	Namespace string
	To        string
}

type Options struct {
	Logger  logr.Logger
	Timeout time.Duration
}

type OC interface {
	AdmReleaseExtract(o ReleaseExtractOptions) error
	AdmReleaseInfo(o ReleaseInfoOptions) (string, error)
	Version(o VersionOptions) (string, error)
	Extract(o ExtractOptions) error
}

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
}

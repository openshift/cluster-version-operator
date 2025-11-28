package api

type ReleaseExtractOptions struct {
	To string
}

type VersionOptions struct {
	Client bool
}

type OC interface {
	AdmReleaseExtract(o ReleaseExtractOptions) error
	Version(o VersionOptions) (string, error)
}

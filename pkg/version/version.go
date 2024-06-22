package version

import (
	"fmt"
	"strings"

	"github.com/blang/semver/v4"
	"k8s.io/apimachinery/pkg/version"
)

var (
	// Raw is the string representation of the version. This will be replaced
	// with the calculated version at build time.
	Raw = "v0.0.1"

	// Version is the semver representation of the version.
	Version = semver.MustParse(strings.TrimLeft(Raw, "v"))

	// String is the human-friendly representation of the version.
	String = fmt.Sprintf("ClusterVersionOperator %s", Raw)
)

var (
	// commitFromGit is a constant representing the source version that
	// generated this build. It should be set during build via -ldflags.
	commitFromGit string
	// versionFromGit is a constant representing the version tag that
	// generated this build. It should be set during build via -ldflags.
	versionFromGit string
	// major version
	majorFromGit string
	// minor version
	minorFromGit string
	// build date in ISO8601 format, output of $(date -u +'%Y-%m-%dT%H:%M:%SZ')
	buildDate string
)

// Get returns the overall codebase version. It's for detecting
// what code a binary was built from.
func Get() version.Info {
	return version.Info{
		Major:      majorFromGit,
		Minor:      minorFromGit,
		GitCommit:  commitFromGit,
		GitVersion: versionFromGit,
		BuildDate:  buildDate,
	}
}

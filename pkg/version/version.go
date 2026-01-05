package version

import (
	"fmt"
	"strings"

	"github.com/blang/semver/v4"
)

var (
	// Raw is the string representation of the version. This will be replaced
	// with the calculated version at build time.
	Raw = "v0.0.1"

	// Version is the semver representation of the version.
	Version = semver.MustParse(strings.TrimLeft(Raw, "v"))

	// String is the human-friendly representation of the version.
	String = fmt.Sprintf("ClusterVersionOperator %s", Raw)

	// SCOS is a setting to enable CentOS Stream CoreOS-only modifications.
	// This is set via the scos build tag.
	SCOS = false
)

// IsSCOS returns true if CentOS Stream CoreOS-only modifications are enabled.
// This is enabled via the scos build tag for OKD builds.
func IsSCOS() bool {
	return SCOS
}

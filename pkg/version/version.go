package version

import (
	"fmt"
	"strings"

	"github.com/blang/semver/v4"
)

var (
	// OKD is true when the operator was built for OKD.
	OKD = false

	// Raw is the string representation of the version. This will be replaced
	// with the calculated version at build time.
	Raw = "v0.0.1"

	// Version is the semver representation of the version.
	Version = semver.MustParse(strings.TrimLeft(Raw, "v"))

	// String is the human-friendly representation of the version.
	String = fmt.Sprintf("ClusterVersionOperator %s", Raw)
)

// IsOKD returns true when the operator was built for OKD.
func IsOKD() bool {
	return OKD
}

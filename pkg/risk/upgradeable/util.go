package upgradeable

import (
	"github.com/blang/semver/v4"
)

// MajorAndMinorUpdates returns all versions that are a major or minor
// version beyond the current version.  Versions that cannot be parsed
// as SemVer are included as well.
func MajorAndMinorUpdates(currentVersion semver.Version, versions []string) []string {
	var majorAndMinorUpdates []string
	for _, vs := range versions {
		vp, err := semver.Parse(vs)
		if err != nil {
			majorAndMinorUpdates = append(majorAndMinorUpdates, vs)
			continue
		}
		if vp.Major > currentVersion.Major ||
			(vp.Major == currentVersion.Major && vp.Minor > currentVersion.Minor) {
			majorAndMinorUpdates = append(majorAndMinorUpdates, vs)
		}
	}
	return majorAndMinorUpdates
}

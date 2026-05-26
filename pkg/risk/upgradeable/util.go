package upgradeable

import (
	"github.com/blang/semver/v4"

	"github.com/openshift/cluster-version-operator/pkg/internal"
)

// MajorAndMinorUpdates returns all versions that are a major or minor
// version beyond the current version. Versions that cannot be parsed
// as SemVer are included as well.
func MajorAndMinorUpdates(currentVersion semver.Version, versions []string) []string {
	var majorAndMinorUpdates []string
	for _, vs := range versions {
		vp, err := semver.Parse(vs)
		if err != nil {
			majorAndMinorUpdates = append(majorAndMinorUpdates, vs)
			continue
		}
		if ut := internal.UpdateType(currentVersion, vp); ut == internal.UpdateTypeMajor || ut == internal.UpdateTypeMinor {
			majorAndMinorUpdates = append(majorAndMinorUpdates, vs)
		}
	}
	return majorAndMinorUpdates
}

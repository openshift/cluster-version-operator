package payload

import (
	"fmt"
	"path/filepath"

	"github.com/pkg/errors"
)

// ImageForShortName returns the image using the upgradepayload embedded in
// the Operator.
func ImageForShortName(name string) (string, error) {
	if err := ValidateDirectory(DefaultPayloadDir); err != nil {
		return "", err
	}

	releaseDir := filepath.Join(DefaultPayloadDir, ReleaseManifestDir)

	imageRef, err := loadImageReferences(releaseDir)
	if err != nil {
		return "", errors.Wrapf(err, "error loading image references from %q", releaseDir)
	}

	for _, tag := range imageRef.Spec.Tags {
		if tag.Name == name {
			// we found the short name in ImageStream
			if tag.From != nil && tag.From.Kind == "DockerImage" {
				return tag.From.Name, nil
			}
		}
	}

	return "", fmt.Errorf("error: Unknown name requested, could not find %s in UpgradePayload", name)
}

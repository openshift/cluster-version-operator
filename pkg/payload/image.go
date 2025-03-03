package payload

import (
	"fmt"
	"path/filepath"
)

// ImageForShortName returns the image using the updatepayload embedded in
// the Operator.
func ImageForShortName(name string) (string, error) {
	if err := ValidateDirectory(DefaultPayloadDir); err != nil {
		return "", fmt.Errorf("error validating %q as a release-image directory: %w", DefaultPayloadDir, err)
	}

	releaseDir := filepath.Join(DefaultPayloadDir, ReleaseManifestDir)

	imageRef, err := loadImageReferences(releaseDir)
	if err != nil {
		return "", fmt.Errorf("error loading image references from %q: %w", releaseDir, err)
	}

	for _, tag := range imageRef.Spec.Tags {
		if tag.Name == name {
			// we found the short name in ImageStream
			if tag.From != nil && tag.From.Kind == "DockerImage" {
				return tag.From.Name, nil
			}
		}
	}

	return "", fmt.Errorf("error: Unknown name requested, could not find %s in UpdatePayload", name)
}

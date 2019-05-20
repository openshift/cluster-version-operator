package payload

import (
	"fmt"

	"github.com/pkg/errors"
)

// ImageForShortName returns the image using the payload embedded in
// the Operator.
func ImageForShortName(name string) (string, error) {
	up, err := Load(DefaultPayloadDir, "")
	if err != nil {
		return "", errors.Wrapf(err, "error loading release manifests from %q", DefaultPayloadDir)
	}

	for _, tag := range up.ImageRef.Spec.Tags {
		if tag.Name == name {
			// we found the short name in ImageStream
			if tag.From != nil && tag.From.Kind == "DockerImage" {
				return tag.From.Name, nil
			}
		}
	}

	return "", fmt.Errorf("error: Unknown name requested, could not find %s in payload", name)
}

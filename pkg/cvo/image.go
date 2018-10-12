package cvo

import "fmt"

// ImageForShortName returns the image using the updatepayload embedded in
// the Operator.
func ImageForShortName(name string) (string, error) {
	up, err := loadUpdatePayload(defaultUpdatePayloadDir, "", "")
	if err != nil {
		return "", fmt.Errorf("error loading update payload from %q: %v", defaultUpdatePayloadDir, err)
	}

	for _, tag := range up.imageRef.Spec.Tags {
		if tag.Name == name {
			// we found the short name in ImageStream
			if tag.From != nil && tag.From.Kind == "DockerImage" {
				return tag.From.Name, nil
			}
		}
	}

	return "", fmt.Errorf("error: Unknown name requested, could not find %s in UpdatePayload", name)
}

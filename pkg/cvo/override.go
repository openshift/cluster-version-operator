package cvo

import (
	"fmt"
	"regexp"

	imagev1 "github.com/openshift/api/image/v1"
	imagereference "github.com/openshift/library-go/pkg/image/reference"
)

type manifestMapper func(data []byte) ([]byte, error)

// exactImageFormat attempts to match a string on word boundaries
const exactImageFormat = `\b%s\b`

// newManifestOverrideMapper returns a function that will replace any image defined in imageRef
// with a rewritten image inside of imageRepository that uses the same digest, ensuring the manifest
// points to a different location. For example, if the imageRef contains a tag "etcd" pointing to
// "quay.io/coreos/etcd@sha256:1234...", and imageRepository is set to "myregistry.com/mirror/v1.0",
// any matching image reference in the manifest will be replaced with
// "myregistry.com/mirror/v1.0@sha256:1234...". An error is returned if the imageRepository is not a
// valid image reference as defined by the docker distribution spec, or if the input imageRef has
// tags that point to invalid image references.
func newManifestOverrideMapper(imageRepository string, imageRef *imagev1.ImageStream) (manifestMapper, error) {
	ref, err := imagereference.Parse(imageRepository)
	if err != nil {
		return nil, fmt.Errorf("the payload repository is invalid: %v", err)
	}
	if len(ref.ID) > 0 || len(ref.Tag) > 0 {
		return nil, fmt.Errorf("the payload repository may not have a tag or digest specified")
	}

	replacements := make(map[string]string)
	for _, tag := range imageRef.Spec.Tags {
		if tag.From == nil || tag.From.Kind != "DockerImage" {
			continue
		}
		oldImage := tag.From.Name

		oldRef, err := imagereference.Parse(oldImage)
		if err != nil {
			return nil, fmt.Errorf("unable to parse image reference for tag %q from payload: %v", tag.Name, err)
		}
		if len(oldRef.Tag) > 0 || len(oldRef.ID) == 0 {
			return nil, fmt.Errorf("image reference tag %q in payload does not point to an image digest - unable to rewrite payload", tag.Name)
		}
		newRef := ref
		newRef.ID = oldRef.ID
		replacements[oldImage] = newRef.Exact()
	}

	patterns := make(map[string]*regexp.Regexp)
	for from, to := range replacements {
		pattern := fmt.Sprintf(exactImageFormat, regexp.QuoteMeta(from))
		re, err := regexp.Compile(pattern)
		if err != nil {
			return nil, err
		}
		patterns[to] = re
	}

	return func(data []byte) ([]byte, error) {
		for to, pattern := range patterns {
			data = pattern.ReplaceAll(data, []byte(to))
		}
		return data, nil
	}, nil
}

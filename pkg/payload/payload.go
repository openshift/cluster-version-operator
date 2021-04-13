package payload

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"hash/fnv"
	"io/ioutil"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"k8s.io/klog/v2"

	utilerrors "k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/apimachinery/pkg/util/sets"

	"github.com/blang/semver/v4"
	configv1 "github.com/openshift/api/config/v1"
	imagev1 "github.com/openshift/api/image/v1"

	"github.com/openshift/cluster-version-operator/lib/resourceread"
	"github.com/openshift/library-go/pkg/manifest"
)

// State describes the state of the payload and alters
// how a payload is applied.
type State int

const (
	// UpdatingPayload indicates we are moving from one state to
	// another.
	//
	// When we are moving to a different payload version, we want to
	// be as conservative as possible about ordering of the payload
	// and the errors we might encounter. An error in one operator
	// should prevent dependent operators from changing. We are
	// willing to take longer to roll out an update if it reduces
	// the possibility of error.
	UpdatingPayload State = iota
	// ReconcilingPayload indicates we are attempting to maintain
	// our current state.
	//
	// When the payload has already been applied to the cluster, we
	// prioritize ensuring resources are recreated and don't need to
	// progress in strict order. We also attempt to reset as many
	// resources as possible back to their desired state and report
	// errors after the fact.
	ReconcilingPayload
	// InitializingPayload indicates we are establishing our first
	// state.
	//
	// When we are deploying a payload for the first time we want
	// to make progress quickly but in a predictable order to
	// minimize retries and crash-loops. We wait for operators
	// to report level but tolerate degraded and transient errors.
	// Our goal is to get the entire payload created, even if some
	// operators are still converging.
	InitializingPayload
	// PrecreatingPayload indicates we are selectively creating
	// specific resources during a first pass of the payload to
	// provide better visibility during install and upgrade of
	// error conditions.
	PrecreatingPayload
)

// Initializing is true if the state is InitializingPayload.
func (s State) Initializing() bool { return s == InitializingPayload }

// Reconciling is true if the state is ReconcilingPayload.
func (s State) Reconciling() bool { return s == ReconcilingPayload }

func (s State) String() string {
	switch s {
	case ReconcilingPayload:
		return "Reconciling"
	case UpdatingPayload:
		return "Updating"
	case InitializingPayload:
		return "Initializing"
	default:
		panic(fmt.Sprintf("unrecognized state %d", int(s)))
	}
}

const (
	DefaultPayloadDir = "/"

	CVOManifestDir     = "manifests"
	ReleaseManifestDir = "release-manifests"

	cincinnatiJSONFile  = "release-metadata"
	imageReferencesFile = "image-references"

	DefaultClusterProfile = "self-managed-high-availability"
)

// Update represents the contents of a release image.
type Update struct {
	Release configv1.Release

	VerifiedImage bool
	LoadedAt      time.Time

	ImageRef *imagev1.ImageStream

	// manifestHash is a hash of the manifests included in this payload
	ManifestHash string
	Manifests    []manifest.Manifest
}

// metadata represents Cincinnati metadata.
// https://github.com/openshift/cincinnati/blob/a8abb826ef00cf91fd0f8a84912d4e0c23b1335d/docs/design/cincinnati.md#update-graph
type metadata struct {
	// Kind is the document type.  Must be cincinnati-metadata-v0.
	Kind string `json:"kind"`

	// Version is the version of the release.
	Version string `json:"version"`

	// Previous is a slice of valid previous versions.
	Previous []string `json:"previous,omitempty"`

	// Next is a slice of valid next versions.
	Next []string `json:"next,omitempty"`

	// Metadata is an opaque object that allows a release to convey arbitrary information to its consumers.
	Metadata map[string]interface{}
}

func LoadUpdate(dir, releaseImage, excludeIdentifier string, includeTechPreview func() (bool, error), profile string) (*Update, error) {
	payload, tasks, err := loadUpdatePayloadMetadata(dir, releaseImage, profile)
	if err != nil {
		return nil, err
	}

	techPreviews := false
	if includeTechPreview != nil {
		techPreviews, err = includeTechPreview()
		if err != nil {
			return nil, err
		}
	}

	var manifests []manifest.Manifest
	var errs []error
	for _, task := range tasks {
		files, err := ioutil.ReadDir(task.idir)
		if err != nil {
			return nil, err
		}

		for _, file := range files {
			if file.IsDir() {
				continue
			}

			switch filepath.Ext(file.Name()) {
			case ".yaml", ".yml", ".json":
			default:
				continue
			}

			p := filepath.Join(task.idir, file.Name())
			if task.skipFiles.Has(p) {
				continue
			}

			raw, err := ioutil.ReadFile(p)
			if err != nil {
				errs = append(errs, err)
				continue
			}
			if task.preprocess != nil {
				raw, err = task.preprocess(raw)
				if err != nil {
					errs = append(errs, fmt.Errorf("preprocess %s: %w", file.Name(), err))
					continue
				}
			}
			ms, err := manifest.ParseManifests(bytes.NewReader(raw))
			if err != nil {
				errs = append(errs, fmt.Errorf("parse %s: %w", file.Name(), err))
				continue
			}
			// Filter out manifests that should be excluded based on annotation
			filteredMs := []manifest.Manifest{}
			for _, manifest := range ms {
				if shouldExclude(excludeIdentifier, profile, &manifest, techPreviews) {
					continue
				}
				filteredMs = append(filteredMs, manifest)
			}
			ms = filteredMs
			for i := range ms {
				ms[i].OriginalFilename = filepath.Base(file.Name())
			}
			manifests = append(manifests, ms...)
		}
	}

	agg := utilerrors.NewAggregate(errs)
	if agg != nil {
		return nil, &UpdateError{
			Reason:  "UpdatePayloadIntegrity",
			Message: fmt.Sprintf("Error loading manifests from %s: %v", dir, agg.Error()),
		}
	}

	hash := fnv.New64()
	for _, manifest := range manifests {
		hash.Write(manifest.Raw)
	}

	payload.ManifestHash = base64.URLEncoding.EncodeToString(hash.Sum(nil))
	payload.Manifests = manifests
	return payload, nil
}

func shouldExclude(excludeIdentifier, profile string, manifest *manifest.Manifest, includeTechPreview bool) bool {
	annotations := manifest.Obj.GetAnnotations()
	if annotations == nil {
		return true
	}

	excludeAnnotation := fmt.Sprintf("exclude.release.openshift.io/%s", excludeIdentifier)
	if annotations[excludeAnnotation] == "true" {
		return true
	}

	techPreviewAnnotation := "featuregate.release.openshift.io/tech-preview"
	if annotations[techPreviewAnnotation] == "true" && !includeTechPreview {
		return true
	}

	profileAnnotation := fmt.Sprintf("include.release.openshift.io/%s", profile)
	if val, ok := annotations[profileAnnotation]; ok && val == "true" {
		return false
	}
	return true
}

// ValidateDirectory checks if a directory can be a candidate update by
// looking for known files. It returns an error if the directory cannot
// be an update.
func ValidateDirectory(dir string) error {
	for _, dirname := range []string{CVOManifestDir, ReleaseManifestDir} {
		if _, err := os.Stat(filepath.Join(dir, dirname)); err != nil {
			return err
		}
	}

	for _, filename := range []string{cincinnatiJSONFile, imageReferencesFile} {
		if _, err := os.Stat(filepath.Join(dir, ReleaseManifestDir, filename)); err != nil {
			return err
		}
	}

	return nil
}

type payloadTasks struct {
	idir       string
	preprocess func([]byte) ([]byte, error)
	skipFiles  sets.String
}

func loadUpdatePayloadMetadata(dir, releaseImage, clusterProfile string) (*Update, []payloadTasks, error) {
	klog.V(4).Infof("Loading updatepayload from %q", dir)
	if err := ValidateDirectory(dir); err != nil {
		return nil, nil, err
	}
	var (
		cvoDir     = filepath.Join(dir, CVOManifestDir)
		releaseDir = filepath.Join(dir, ReleaseManifestDir)
	)

	release, err := loadReleaseFromMetadata(releaseDir)
	if err != nil {
		return nil, nil, err
	}
	release.Image = releaseImage

	imageRef, err := loadImageReferences(releaseDir)
	if err != nil {
		return nil, nil, err
	}

	if imageRef.Name != release.Version {
		return nil, nil, fmt.Errorf("Version from %s (%s) differs from %s (%s)", imageReferencesFile, imageRef.Name, cincinnatiJSONFile, release.Version)
	}

	tasks := getPayloadTasks(releaseDir, cvoDir, releaseImage, clusterProfile)

	return &Update{
		Release:  release,
		ImageRef: imageRef,
	}, tasks, nil
}

func getPayloadTasks(releaseDir, cvoDir, releaseImage, clusterProfile string) []payloadTasks {
	cjf := filepath.Join(releaseDir, cincinnatiJSONFile)
	irf := filepath.Join(releaseDir, imageReferencesFile)

	mrc := manifestRenderConfig{
		ReleaseImage:   releaseImage,
		ClusterProfile: clusterProfile,
	}

	return []payloadTasks{{
		idir:       cvoDir,
		preprocess: func(ib []byte) ([]byte, error) { return renderManifest(mrc, ib) },
		skipFiles:  sets.NewString(),
	}, {
		idir:       releaseDir,
		preprocess: nil,
		skipFiles:  sets.NewString(cjf, irf),
	}}
}

func loadReleaseFromMetadata(releaseDir string) (configv1.Release, error) {
	var release configv1.Release
	path := filepath.Join(releaseDir, cincinnatiJSONFile)
	data, err := ioutil.ReadFile(path)
	if err != nil {
		return release, err
	}

	var metadata metadata
	if err := json.Unmarshal(data, &metadata); err != nil {
		return release, fmt.Errorf("unmarshal Cincinnati metadata: %w", err)
	}

	if metadata.Kind != "cincinnati-metadata-v0" {
		return release, fmt.Errorf("unrecognized Cincinnati metadata kind %q", metadata.Kind)
	}

	if metadata.Version == "" {
		return release, errors.New("missing required Cincinnati metadata version")
	}

	if _, err := semver.Parse(metadata.Version); err != nil {
		return release, fmt.Errorf("Cincinnati metadata version %q is not a valid semantic version: %v", metadata.Version, err)
	}

	release.Version = metadata.Version

	if urlInterface, ok := metadata.Metadata["url"]; ok {
		if urlString, ok := urlInterface.(string); ok {
			release.URL = configv1.URL(urlString)
		} else {
			klog.Warningf("URL from %s (%s) is not a string: %v", cincinnatiJSONFile, release.Version, urlInterface)
		}
	}
	if channelsInterface, ok := metadata.Metadata["io.openshift.upgrades.graph.release.channels"]; ok {
		if channelsString, ok := channelsInterface.(string); ok {
			release.Channels = strings.Split(channelsString, ",")
			sort.Strings(release.Channels)
		} else {
			klog.Warningf("channel list from %s (%s) is not a string: %v", cincinnatiJSONFile, release.Version, channelsInterface)
		}
	}

	return release, nil
}

func loadImageReferences(releaseDir string) (*imagev1.ImageStream, error) {
	irf := filepath.Join(releaseDir, imageReferencesFile)
	imageRefData, err := ioutil.ReadFile(irf)
	if err != nil {
		return nil, err
	}

	imageRefObj, err := resourceread.Read(imageRefData)
	if err != nil {
		return nil, fmt.Errorf("unmarshal image-references: %w", err)
	}
	if imageRef, ok := imageRefObj.(*imagev1.ImageStream); ok {
		return imageRef, nil
	}
	return nil, fmt.Errorf("%s is a %T, not a v1 ImageStream", imageReferencesFile, imageRefObj)
}

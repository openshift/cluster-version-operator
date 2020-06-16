package payload

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"hash/fnv"
	"io/ioutil"
	"os"
	"path/filepath"
	"time"

	"github.com/pkg/errors"
	"k8s.io/klog"

	utilerrors "k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/apimachinery/pkg/util/sets"

	imagev1 "github.com/openshift/api/image/v1"

	"github.com/openshift/cluster-version-operator/lib"
	"github.com/openshift/cluster-version-operator/lib/resourceread"
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
	// willing to take longer to roll out an upgrade if it reduces
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
)

type Upgrade struct {
	ReleaseImage   string
	ReleaseVersion string
	// XXX: cincinatti.json struct

	VerifiedImage bool
	LoadedAt      time.Time

	ImageRef *imagev1.ImageStream

	// manifestHash is a hash of the manifests included in this payload
	ManifestHash string
	Manifests    []lib.Manifest
}

func LoadUpgrade(dir, releaseImage, excludeIdentifier string) (*Upgrade, error) {
	payload, tasks, err := loadUpgradePayloadMetadata(dir, releaseImage)
	if err != nil {
		return nil, err
	}

	var manifests []lib.Manifest
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
				errs = append(errs, errors.Wrapf(err, "error reading file %s", file.Name()))
				continue
			}
			if task.preprocess != nil {
				raw, err = task.preprocess(raw)
				if err != nil {
					errs = append(errs, errors.Wrapf(err, "error running preprocess on %s", file.Name()))
					continue
				}
			}
			ms, err := lib.ParseManifests(bytes.NewReader(raw))
			if err != nil {
				errs = append(errs, errors.Wrapf(err, "error parsing %s", file.Name()))
				continue
			}
			// Filter out manifests that should be excluded based on annotation
			filteredMs := []lib.Manifest{}
			for _, manifest := range ms {
				if shouldExclude(excludeIdentifier, &manifest) {
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
		return nil, &UpgradeError{
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

func shouldExclude(excludeIdentifier string, manifest *lib.Manifest) bool {
	excludeAnnotation := fmt.Sprintf("exclude.release.openshift.io/%s", excludeIdentifier)
	annotations := manifest.Object().GetAnnotations()
	return annotations != nil && annotations[excludeAnnotation] == "true"
}

// ValidateDirectory checks if a directory can be a candidate upgrade by
// looking for known files. It returns an error if the directory cannot
// be an upgrade.
func ValidateDirectory(dir string) error {
	// XXX: validate that cincinnati.json is correct
	// 		validate image-references files is correct.

	// make sure cvo and release manifests dirs exist.
	_, err := os.Stat(filepath.Join(dir, CVOManifestDir))
	if err != nil {
		return err
	}
	releaseDir := filepath.Join(dir, ReleaseManifestDir)
	_, err = os.Stat(releaseDir)
	if err != nil {
		return err
	}

	// make sure image-references file exists in releaseDir
	_, err = os.Stat(filepath.Join(releaseDir, imageReferencesFile))
	if err != nil {
		return err
	}
	return nil
}

type payloadTasks struct {
	idir       string
	preprocess func([]byte) ([]byte, error)
	skipFiles  sets.String
}

func loadUpgradePayloadMetadata(dir, releaseImage string) (*Upgrade, []payloadTasks, error) {
	klog.V(4).Infof("Loading upgradepayload from %q", dir)
	if err := ValidateDirectory(dir); err != nil {
		return nil, nil, err
	}
	var (
		cvoDir     = filepath.Join(dir, CVOManifestDir)
		releaseDir = filepath.Join(dir, ReleaseManifestDir)
	)

	imageRef, err := loadImageReferences(releaseDir)
	if err != nil {
		return nil, nil, err
	}

	tasks := getPayloadTasks(releaseDir, cvoDir, releaseImage)

	return &Upgrade{ImageRef: imageRef, ReleaseImage: releaseImage, ReleaseVersion: imageRef.Name}, tasks, nil
}

func getPayloadTasks(releaseDir, cvoDir, releaseImage string) []payloadTasks {
	cjf := filepath.Join(releaseDir, cincinnatiJSONFile)
	irf := filepath.Join(releaseDir, imageReferencesFile)

	mrc := manifestRenderConfig{ReleaseImage: releaseImage}

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

func loadImageReferences(releaseDir string) (*imagev1.ImageStream, error) {
	irf := filepath.Join(releaseDir, imageReferencesFile)
	imageRefData, err := ioutil.ReadFile(irf)
	if err != nil {
		return nil, err
	}

	imageRef, err := resourceread.ReadImageStreamV1(imageRefData)
	if err != nil {
		return nil, errors.Wrapf(err, "invalid image-references data %s", irf)
	}

	return imageRef, nil
}

package payload

import (
	"fmt"
	"io/ioutil"
	"path/filepath"
	"time"

	"github.com/pkg/errors"
	"k8s.io/klog"

	imagev1 "github.com/openshift/api/image/v1"

	"github.com/openshift/cluster-version-operator/lib/resourceread"
	"github.com/openshift/cluster-version-operator/pkg/manifest"
	"github.com/openshift/cluster-version-operator/pkg/manifest/render"
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

type Update struct {
	ReleaseImage   string
	ReleaseVersion string
	// XXX: cincinatti.json struct

	VerifiedImage bool
	LoadedAt      time.Time

	ImageRef *imagev1.ImageStream

	// manifestHash is a hash of the manifests included in this payload
	ManifestHash string
	Manifests    []manifest.Manifest
}

func LoadUpdate(dir, releaseImage string) (*Update, error) {
	payload, err := loadUpdatePayloadImageData(dir, releaseImage)
	if err != nil {
		return nil, err
	}

	manifests, err := render.LoadManifests(dir, releaseImage)
	if err != nil {
		return nil, err
	}
	manifestHash := render.LoadManifestsHash(manifests)

	payload.ManifestHash = manifestHash
	payload.Manifests = manifests
	return payload, nil
}

func loadUpdatePayloadImageData(dir, releaseImage string) (*Update, error) {
	klog.V(4).Infof("Loading updatepayload image data from %q", dir)
	if err := render.ValidateDirectory(dir); err != nil {
		return nil, err
	}
	var releaseDir = filepath.Join(dir, render.ReleaseManifestDir)

	imageRef, err := loadImageReferences(releaseDir)
	if err != nil {
		return nil, err
	}

	return &Update{ImageRef: imageRef, ReleaseImage: releaseImage, ReleaseVersion: imageRef.Name}, nil
}

func loadImageReferences(releaseDir string) (*imagev1.ImageStream, error) {
	irf := filepath.Join(releaseDir, render.ImageReferencesFile)
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

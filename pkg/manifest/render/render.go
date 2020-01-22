package render

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"hash/fnv"
	"io/ioutil"
	"os"
	"path/filepath"
	"text/template"

	"github.com/pkg/errors"
	"k8s.io/klog"

	utilerrors "k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/apimachinery/pkg/util/sets"

	"github.com/openshift/cluster-version-operator/pkg/manifest"
)

const (
	DefaultPayloadDir = "/"

	CVOManifestDir     = "manifests"
	ReleaseManifestDir = "release-manifests"

	cincinnatiJSONFile  = "release-metadata"
	ImageReferencesFile = "image-references"
)

// Render renders all the manifests from /manifests to outputDir.
func Render(outputDir, releaseImage string) error {
	var (
		manifestsDir  = filepath.Join(DefaultPayloadDir, CVOManifestDir)
		oManifestsDir = filepath.Join(outputDir, "manifests")
		bootstrapDir  = "/bootstrap"
		oBootstrapDir = filepath.Join(outputDir, "bootstrap")

		renderConfig = manifestRenderConfig{ReleaseImage: releaseImage}
	)

	tasks := []struct {
		idir      string
		odir      string
		skipFiles sets.String
	}{{
		idir: manifestsDir,
		odir: oManifestsDir,
		skipFiles: sets.NewString(
			"image-references",
			"0000_90_cluster-version-operator_00_prometheusrole.yaml",
			"0000_90_cluster-version-operator_01_prometheusrolebinding.yaml",
			"0000_90_cluster-version-operator_02_servicemonitor.yaml",
		),
	}, {
		idir:      bootstrapDir,
		odir:      oBootstrapDir,
		skipFiles: sets.NewString(),
	}}
	var errs []error
	for _, task := range tasks {
		if err := renderDir(renderConfig, task.idir, task.odir, task.skipFiles); err != nil {
			errs = append(errs, err)
		}
	}

	agg := utilerrors.NewAggregate(errs)
	if agg != nil {
		return fmt.Errorf("error rendering manifests: %v", agg.Error())
	}
	return nil
}

func renderDir(renderConfig manifestRenderConfig, idir, odir string, skipFiles sets.String) error {
	if err := os.MkdirAll(odir, 0666); err != nil {
		return err
	}
	files, err := ioutil.ReadDir(idir)
	if err != nil {
		return err
	}
	var errs []error
	for _, file := range files {
		if file.IsDir() {
			continue
		}
		if skipFiles.Has(file.Name()) {
			continue
		}

		ipath := filepath.Join(idir, file.Name())
		iraw, err := ioutil.ReadFile(ipath)
		if err != nil {
			errs = append(errs, err)
			continue
		}

		rraw, err := renderManifest(renderConfig, iraw)
		if err != nil {
			errs = append(errs, err)
			continue
		}

		opath := filepath.Join(odir, file.Name())
		if err := ioutil.WriteFile(opath, rraw, 0666); err != nil {
			errs = append(errs, err)
			continue
		}
	}

	agg := utilerrors.NewAggregate(errs)
	if agg != nil {
		return fmt.Errorf("error rendering manifests: %v", agg.Error())
	}
	return nil
}

type manifestRenderConfig struct {
	ReleaseImage string
}

// renderManifest Executes go text template from `manifestBytes` with `config`.
func renderManifest(config manifestRenderConfig, manifestBytes []byte) ([]byte, error) {
	tmpl, err := template.New("manifest").Parse(string(manifestBytes))
	if err != nil {
		return nil, errors.Wrapf(err, "failed to parse manifest")
	}

	buf := new(bytes.Buffer)
	if err := tmpl.Execute(buf, config); err != nil {
		return nil, errors.Wrapf(err, "failed to execute template")
	}

	return buf.Bytes(), nil
}

// LoadManifests reads manifests out of the given directory and
// returns a slice of manifests over all loaded manifests.
func LoadManifests(dir, releaseImage string) ([]manifest.Manifest, error) {
	tasks, err := loadManifestMetadata(dir, releaseImage)
	if err != nil {
		return nil, err
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
			ms, err := manifest.ParseManifests(bytes.NewReader(raw))
			if err != nil {
				errs = append(errs, errors.Wrapf(err, "error parsing %s", file.Name()))
				continue
			}
			for i := range ms {
				ms[i].OriginalFilename = filepath.Base(file.Name())
			}
			manifests = append(manifests, ms...)
		}
	}

	agg := utilerrors.NewAggregate(errs)
	if agg != nil {
		return nil, errors.New(fmt.Sprintf("Error loading manifests from %s: %v", dir, agg.Error()))
	}
	return manifests, nil
}

// LoadManifestsHash returns a hash over all given manifests.
func LoadManifestsHash(manifests []manifest.Manifest) string {
	hash := fnv.New64()
	for _, manifest := range manifests {
		hash.Write(manifest.Raw)
	}

	manifestHash := base64.URLEncoding.EncodeToString(hash.Sum(nil))
	return manifestHash
}

// ValidateDirectory checks if a directory can be a candidate update by
// looking for known files. It returns an error if the directory cannot
// be an update.
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
	_, err = os.Stat(filepath.Join(releaseDir, ImageReferencesFile))
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

func loadManifestMetadata(dir, releaseImage string) ([]payloadTasks, error) {
	klog.V(4).Infof("Loading manifest metadata from %q", dir)
	if err := ValidateDirectory(dir); err != nil {
		return nil, err
	}
	var (
		cvoDir     = filepath.Join(dir, CVOManifestDir)
		releaseDir = filepath.Join(dir, ReleaseManifestDir)
	)

	tasks := getPayloadTasks(releaseDir, cvoDir, releaseImage)

	return tasks, nil
}

func getPayloadTasks(releaseDir, cvoDir, releaseImage string) []payloadTasks {
	cjf := filepath.Join(releaseDir, cincinnatiJSONFile)
	irf := filepath.Join(releaseDir, ImageReferencesFile)

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

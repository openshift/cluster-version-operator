package payload

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"text/template"

	"github.com/pkg/errors"

	utilerrors "k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/apimachinery/pkg/util/sets"
)

// Render renders critical manifests from /manifests to outputDir.
func Render(outputDir, releaseImage, clusterProfile string) error {
	var (
		manifestsDir  = filepath.Join(DefaultPayloadDir, CVOManifestDir)
		oManifestsDir = filepath.Join(outputDir, "manifests")
		bootstrapDir  = "/bootstrap"
		oBootstrapDir = filepath.Join(outputDir, "bootstrap")

		renderConfig = manifestRenderConfig{
			ReleaseImage:   releaseImage,
			ClusterProfile: clusterProfile,
		}
	)

	tasks := []struct {
		idir      string
		odir      string
		skipFiles sets.Set[string]
	}{{
		idir: manifestsDir,
		odir: oManifestsDir,
		skipFiles: sets.New[string](
			"image-references",
			"0000_90_cluster-version-operator_00_prometheusrole.yaml",
			"0000_90_cluster-version-operator_01_prometheusrolebinding.yaml",
			"0000_90_cluster-version-operator_02_servicemonitor.yaml",
		),
	}, {
		idir:      bootstrapDir,
		odir:      oBootstrapDir,
		skipFiles: sets.Set[string]{},
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

func renderDir(renderConfig manifestRenderConfig, idir, odir string, skipFiles sets.Set[string]) error {
	if err := os.MkdirAll(odir, 0666); err != nil {
		return err
	}
	files, err := os.ReadDir(idir)
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
		if strings.Contains(file.Name(), "CustomNoUpgrade") || strings.Contains(file.Name(), "TechPreviewNoUpgrade") {
			// CustomNoUpgrade and TechPreviewNoUpgrade may add features to manifests like the ClusterVersion CRD,
			// but we do not need those features during bootstrap-render time.  In those clusters, the production
			// CVO will be along shortly to update the manifests and deliver the gated features.
			continue
		}

		ipath := filepath.Join(idir, file.Name())
		iraw, err := os.ReadFile(ipath)
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
		if err := os.WriteFile(opath, rraw, 0666); err != nil {
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
	ReleaseImage   string
	ClusterProfile string
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

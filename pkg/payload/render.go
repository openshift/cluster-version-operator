package payload

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"text/template"

	"github.com/openshift/api/config"
	configv1 "github.com/openshift/api/config/v1"
	"github.com/openshift/library-go/pkg/manifest"
	"github.com/pkg/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	utilerrors "k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/klog/v2"
	"k8s.io/utils/ptr"
)

// Render renders critical manifests from /manifests to outputDir.
func Render(outputDir, releaseImage, featureGateManifestPath, clusterProfile string) error {
	var (
		manifestsDir        = filepath.Join(DefaultPayloadDir, CVOManifestDir)
		releaseManifestsDir = filepath.Join(DefaultPayloadDir, ReleaseManifestDir)
		oManifestsDir       = filepath.Join(outputDir, "manifests")
		bootstrapDir        = "/bootstrap"
		oBootstrapDir       = filepath.Join(outputDir, "bootstrap")

		renderConfig = manifestRenderConfig{
			ReleaseImage:   releaseImage,
			ClusterProfile: clusterProfile,
		}
	)

	var requiredFeatureSet *string
	if featureGateManifestPath != "" {
		manifests, err := manifest.ManifestsFromFiles([]string{featureGateManifestPath})
		if err != nil {
			return fmt.Errorf("loading FeatureGate manifest: %w", err)
		}
		if len(manifests) != 1 {
			return fmt.Errorf("FeatureGate manifest %s contains %d manifests, but expected only one", featureGateManifestPath, len(manifests))
		}
		featureGateManifest := manifests[0]
		expectedGVK := schema.GroupVersionKind{Kind: "FeatureGate", Version: configv1.GroupVersion.Version, Group: config.GroupName}
		if featureGateManifest.GVK != expectedGVK {
			return fmt.Errorf("FeatureGate manifest %s GroupVersionKind %v, but expected %v", featureGateManifest.OriginalFilename, featureGateManifest.GVK, expectedGVK)
		}
		featureSet, found, err := unstructured.NestedString(featureGateManifest.Obj.Object, "spec", "featureSet")
		if err != nil {
			return fmt.Errorf("%s spec.featureSet lookup was not a string: %w", featureGateManifest.String(), err)
		} else if found {
			requiredFeatureSet = &featureSet
			klog.Infof("--feature-gate-manifest-path=%s results in feature set %q", featureGateManifest.OriginalFilename, featureSet)
		} else {
			requiredFeatureSet = ptr.To[string]("")
			klog.Infof("--feature-gate-manifest-path=%s does not set spec.featureSet, using the default feature set", featureGateManifest.OriginalFilename)
		}
	} else {
		requiredFeatureSet = ptr.To[string]("")
		klog.Info("--feature-gate-manifest-path is unset, using the default feature set")
	}

	tasks := []struct {
		idir            string
		odir            string
		processTemplate bool
		skipFiles       sets.Set[string]
		filterGroupKind sets.Set[schema.GroupKind]
	}{{
		idir:            manifestsDir,
		odir:            oManifestsDir,
		processTemplate: true,
		skipFiles: sets.New[string](
			"0000_90_cluster-version-operator_00_prometheusrole.yaml",
			"0000_90_cluster-version-operator_01_prometheusrolebinding.yaml",
			"0000_90_cluster-version-operator_02_servicemonitor.yaml",
		),
	}, {
		idir: releaseManifestsDir,
		odir: oManifestsDir,
		filterGroupKind: sets.New[schema.GroupKind](
			schema.GroupKind{Group: config.GroupName, Kind: "ClusterImagePolicy"},
			schema.GroupKind{Group: config.GroupName, Kind: "ImagePolicy"},
		),
	}, {
		idir:            bootstrapDir,
		odir:            oBootstrapDir,
		processTemplate: true,
		skipFiles:       sets.Set[string]{},
	}}
	var errs []error
	for _, task := range tasks {
		if err := renderDir(renderConfig, task.idir, task.odir, requiredFeatureSet, &clusterProfile, task.processTemplate, task.skipFiles, task.filterGroupKind); err != nil {
			errs = append(errs, err)
		}
	}

	agg := utilerrors.NewAggregate(errs)
	if agg != nil {
		return fmt.Errorf("error rendering manifests: %v", agg.Error())
	}
	return nil
}

func renderDir(renderConfig manifestRenderConfig, idir, odir string, requiredFeatureSet *string, clusterProfile *string, processTemplate bool, skipFiles sets.Set[string], filterGroupKind sets.Set[schema.GroupKind]) error {
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
		if !hasManifestExtension(file.Name()) {
			continue
		}

		if skipFiles.Has(file.Name()) {
			continue
		}
		if strings.Contains(file.Name(), "CustomNoUpgrade") || strings.Contains(file.Name(), "TechPreviewNoUpgrade") || strings.Contains(file.Name(), "DevPreviewNoUpgrade") {
			// CustomNoUpgrade, TechPreviewNoUpgrade and DevPreviewNoUpgrade may add features to manifests like the ClusterVersion CRD,
			// but we do not need those features during bootstrap-render time.  In those clusters, the production
			// CVO will be along shortly to update the manifests and deliver the gated features.
			// fixme: now that we have requiredFeatureSet, use it to do Manifest.Include() filtering here instead of making filename assumptions
			continue
		}

		ipath := filepath.Join(idir, file.Name())
		iraw, err := os.ReadFile(ipath)
		if err != nil {
			errs = append(errs, err)
			continue
		}

		var rraw []byte
		if processTemplate {
			rraw, err = renderManifest(renderConfig, iraw)
			if err != nil {
				errs = append(errs, fmt.Errorf("render template %s from %s: %w", file.Name(), idir, err))
				continue
			}
		} else {
			rraw = iraw
		}

		if len(filterGroupKind) > 0 {
			manifests, err := manifest.ParseManifests(bytes.NewReader(rraw))
			if err != nil {
				errs = append(errs, fmt.Errorf("parse manifest %s from %s: %w", file.Name(), idir, err))
				continue
			}

			filteredManifests := make([]string, 0, len(manifests))
			for _, manifest := range manifests {
				if !filterGroupKind.Has(manifest.GVK.GroupKind()) {
					klog.Infof("excluding %s because we do not render that group/kind", manifest.String())
				} else if err := manifest.Include(nil, requiredFeatureSet, clusterProfile, nil, nil); err != nil {
					klog.Infof("excluding %s: %v", manifest.String(), err)
				} else {
					filteredManifests = append(filteredManifests, string(manifest.Raw))
					klog.Infof("including %s filtered by feature set %v and cluster profile %v", manifest.String(), requiredFeatureSet, clusterProfile)
				}
			}

			if len(filteredManifests) == 0 {
				continue
			}

			if len(filteredManifests) == 1 {
				rraw = []byte(filteredManifests[0])
			} else {
				rraw = []byte(strings.Join(filteredManifests, "\n---\n"))
			}
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

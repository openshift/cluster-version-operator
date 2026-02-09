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
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	utilerrors "k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/klog/v2"
	"k8s.io/utils/ptr"
)

// Render renders critical manifests from /manifests to outputDir.
func Render(outputDir, releaseImage, clusterVersionManifestPath, featureGateManifestPath, clusterProfile string) error {
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

	overrides, err := parseClusterVersionManifest(clusterVersionManifestPath)
	if err != nil {
		return fmt.Errorf("error parsing cluster version manifest: %w", err)
	}

	requiredFeatureSet, enabledFeatureGates, err := parseFeatureGateManifest(featureGateManifestPath)
	if err != nil {
		return fmt.Errorf("error parsing feature gate manifest: %w", err)
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
		if err := renderDir(renderConfig, task.idir, task.odir, overrides, requiredFeatureSet, enabledFeatureGates, &clusterProfile, task.processTemplate, task.skipFiles, task.filterGroupKind); err != nil {
			errs = append(errs, err)
		}
	}

	agg := utilerrors.NewAggregate(errs)
	if agg != nil {
		return fmt.Errorf("error rendering manifests: %v", agg.Error())
	}
	return nil
}

func renderDir(renderConfig manifestRenderConfig, idir, odir string, overrides []configv1.ComponentOverride, requiredFeatureSet *string, enabledFeatureGates sets.Set[string], clusterProfile *string, processTemplate bool, skipFiles sets.Set[string], filterGroupKind sets.Set[schema.GroupKind]) error {
	klog.Infof("Filtering manifests in %s for feature set %v, cluster profile %v and enabled feature gates %v", idir, *requiredFeatureSet, *clusterProfile, enabledFeatureGates.UnsortedList())

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

		manifests, err := manifest.ParseManifests(bytes.NewReader(rraw))
		if err != nil {
			errs = append(errs, fmt.Errorf("parse manifest %s from %s: %w", file.Name(), idir, err))
			continue
		}

		filteredManifests := make([]string, 0, len(manifests))
		for _, manifest := range manifests {
			if len(filterGroupKind) > 0 && !filterGroupKind.Has(manifest.GVK.GroupKind()) {
				klog.Infof("excluding %s because we do not render that group/kind", manifest.String())
			} else if err := manifest.Include(nil, requiredFeatureSet, clusterProfile, nil, overrides, enabledFeatureGates); err != nil {
				klog.Infof("excluding %s: %v", manifest.String(), err)
			} else {
				filteredManifests = append(filteredManifests, string(manifest.Raw))
				klog.Infof("including %s", manifest.String())
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

func parseClusterVersionManifest(clusterVersionManifestPath string) ([]configv1.ComponentOverride, error) {
	if clusterVersionManifestPath == "" {
		return nil, nil
	}

	manifests, err := manifest.ManifestsFromFiles([]string{clusterVersionManifestPath})
	if err != nil {
		return nil, fmt.Errorf("loading ClusterVersion manifest: %w", err)
	}

	if len(manifests) != 1 {
		return nil, fmt.Errorf("ClusterVersion manifest %s contains %d manifests, but expected only one", clusterVersionManifestPath, len(manifests))
	}

	clusterVersionManifest := manifests[0]
	expectedGVK := schema.GroupVersionKind{Kind: "ClusterVersion", Version: configv1.GroupVersion.Version, Group: config.GroupName}
	if clusterVersionManifest.GVK != expectedGVK {
		return nil, fmt.Errorf("ClusterVersion manifest %s GroupVersionKind %v, but expected %v", clusterVersionManifest.OriginalFilename, clusterVersionManifest.GVK, expectedGVK)
	}

	// Convert unstructured object to structured ClusterVersion
	var clusterVersion configv1.ClusterVersion
	if err := runtime.DefaultUnstructuredConverter.FromUnstructured(clusterVersionManifest.Obj.Object, &clusterVersion); err != nil {
		return nil, fmt.Errorf("failed to convert ClusterVersion manifest %s to structured object: %w", clusterVersionManifest.OriginalFilename, err)
	}

	return clusterVersion.Spec.Overrides, nil
}

func parseFeatureGateManifest(featureGateManifestPath string) (*string, sets.Set[string], error) {
	if featureGateManifestPath == "" {
		return ptr.To(""), sets.Set[string]{}, nil
	}

	manifests, err := manifest.ManifestsFromFiles([]string{featureGateManifestPath})
	if err != nil {
		return nil, nil, fmt.Errorf("loading FeatureGate manifest: %w", err)
	}

	if len(manifests) != 1 {
		return nil, nil, fmt.Errorf("FeatureGate manifest %s contains %d manifests, but expected only one", featureGateManifestPath, len(manifests))
	}

	featureGateManifest := manifests[0]
	expectedGVK := schema.GroupVersionKind{Kind: "FeatureGate", Version: configv1.GroupVersion.Version, Group: config.GroupName}
	if featureGateManifest.GVK != expectedGVK {
		return nil, nil, fmt.Errorf("FeatureGate manifest %s GroupVersionKind %v, but expected %v", featureGateManifest.OriginalFilename, featureGateManifest.GVK, expectedGVK)
	}

	// Convert unstructured object to structured FeatureGate
	var featureGate configv1.FeatureGate
	if err := runtime.DefaultUnstructuredConverter.FromUnstructured(featureGateManifest.Obj.Object, &featureGate); err != nil {
		return nil, nil, fmt.Errorf("failed to convert FeatureGate manifest %s to structured object: %w", featureGateManifest.OriginalFilename, err)
	}

	var requiredFeatureSet *string
	if featureGate.Spec.FeatureSet != "" {
		featureSetString := string(featureGate.Spec.FeatureSet)
		requiredFeatureSet = &featureSetString
		klog.Infof("--feature-gate-manifest-path=%s results in feature set %q", featureGateManifest.OriginalFilename, featureGate.Spec.FeatureSet)
	} else {
		requiredFeatureSet = ptr.To("")
		klog.Infof("--feature-gate-manifest-path=%s does not set spec.featureSet, using the default feature set", featureGateManifest.OriginalFilename)
	}

	if len(featureGate.Status.FeatureGates) == 0 {
		// In case there are no feature gates, fall back to feature set only behaviour.
		return requiredFeatureSet, sets.Set[string]{}, nil
	}

	// A rendered manifest should only include 1 version of the enabled feature gates.
	if len(featureGate.Status.FeatureGates) > 1 {
		return nil, nil, fmt.Errorf("FeatureGate manifest %s contains %d feature gates, but expected exactly one", featureGateManifest.OriginalFilename, len(featureGate.Status.FeatureGates))
	}

	enabledFeatureGates := sets.Set[string]{}
	for _, feature := range featureGate.Status.FeatureGates[0].Enabled {
		enabledFeatureGates.Insert(string(feature.Name))
	}

	return requiredFeatureSet, enabledFeatureGates, nil
}

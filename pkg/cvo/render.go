package cvo

import (
	"bytes"
	"fmt"
	"io/ioutil"
	"path/filepath"
	"strings"
	"text/template"

	"github.com/golang/glog"
	batchv1 "k8s.io/api/batch/v1"
	batchv1beta1 "k8s.io/api/batch/v1beta1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	utilerrors "k8s.io/apimachinery/pkg/util/errors"

	osv1 "github.com/openshift/cluster-version-operator/pkg/apis/operatorstatus.openshift.io/v1"
)

// Render renders all the manifests from updatepayload to outputDir.
// Render skips Jobs, OperatorStatus.
func Render(outputDir, releaseImage string) error {
	up, err := loadUpdatePayload(defaultUpdatePayloadDir, releaseImage)
	if err != nil {
		return fmt.Errorf("error loading update payload from %q: %v", defaultUpdatePayloadDir, err)
	}

	var errs []error
	skipGVKs := []schema.GroupVersionKind{
		batchv1.SchemeGroupVersion.WithKind("Job"), batchv1beta1.SchemeGroupVersion.WithKind("Job"),
		osv1.SchemeGroupVersion.WithKind("OperatorStatus"),
	}
	for idx, manifest := range up.manifests {
		mname := fmt.Sprintf("(%s) %s/%s", manifest.GVK, manifest.Object().GetNamespace(), manifest.Object().GetName())
		skip := false
		for _, gvk := range skipGVKs {
			if gvk == manifest.GVK {
				skip = true
			}
		}
		if skip {
			glog.Infof("skipping Manifest %s", mname)
			continue
		}

		filename := strings.ToLower(fmt.Sprintf("%03d-%s-%s-%s-%s-%s", idx, manifest.GVK.Group, manifest.GVK.Version, manifest.GVK.Kind, manifest.Object().GetNamespace(), manifest.Object().GetName()))
		path := filepath.Join(outputDir, filename)
		if err := ioutil.WriteFile(path, manifest.Raw, 0644); err != nil {
			errs = append(errs, err)
		}
	}

	agg := utilerrors.NewAggregate(errs)
	if agg != nil {
		return fmt.Errorf("error rendering from UpdatePayload: %v", agg.Error())
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
		return nil, fmt.Errorf("failed to parse manifest: %v", err)
	}

	buf := new(bytes.Buffer)
	if err := tmpl.Execute(buf, config); err != nil {
		return nil, fmt.Errorf("failed to execute template: %v", err)
	}

	return buf.Bytes(), nil
}

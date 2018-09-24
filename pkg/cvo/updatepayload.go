package cvo

import (
	"bytes"
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"

	imagev1 "github.com/openshift/api/image/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	randutil "k8s.io/apimachinery/pkg/util/rand"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/utils/pointer"

	"github.com/golang/glog"
	"github.com/openshift/cluster-version-operator/lib"
	"github.com/openshift/cluster-version-operator/lib/resourcebuilder"
	"github.com/openshift/cluster-version-operator/lib/resourceread"
	cvv1 "github.com/openshift/cluster-version-operator/pkg/apis/clusterversion.openshift.io/v1"
)

type updatePayload struct {
	// XXX: cincinatti.json struct

	imageRef *imagev1.ImageStream

	manifests []lib.Manifest
}

const (
	defaultUpdatePayloadDir = "/release-manifests"
	targetUpdatePayloadsDir = "/etc/cvo/updatepayloads"

	cincinnatiJSONFile  = "cincinnati.json"
	imageReferencesFile = "image-references"
)

func loadUpdatePayload(dir, releaseImage string) (*updatePayload, error) {
	glog.V(4).Info("Loading updatepayload from %q", dir)
	if err := validateUpdatePayload(dir); err != nil {
		return nil, err
	}

	// XXX: load cincinnatiJSONFile
	cjf := filepath.Join(dir, cincinnatiJSONFile)
	// XXX: load imageReferencesFile
	irf := filepath.Join(dir, imageReferencesFile)
	imageRefData, err := ioutil.ReadFile(irf)
	if err != nil {
		return nil, err
	}
	imageRef := resourceread.ReadImageStreamV1OrDie(imageRefData)

	skipFiles := sets.NewString(cjf, irf)
	files, err := ioutil.ReadDir(dir)
	if err != nil {
		return nil, err
	}

	mrc := manifestRenderConfig{ReleaseImage: releaseImage}
	var manifests []lib.Manifest
	var errs []error
	for _, file := range files {
		if file.IsDir() {
			continue
		}

		p := filepath.Join(dir, file.Name())
		if skipFiles.Has(p) {
			continue
		}

		raw, err := ioutil.ReadFile(p)
		if err != nil {
			errs = append(errs, fmt.Errorf("error reading file %s: %v", file.Name(), err))
			continue
		}
		rraw, err := renderManifest(mrc, raw)
		if err != nil {
			errs = append(errs, fmt.Errorf("error rendering file %s: %v", file.Name(), err))
			continue
		}
		ms, err := lib.ParseManifests(bytes.NewReader(rraw))
		if err != nil {
			errs = append(errs, fmt.Errorf("error parsing %s: %v", file.Name(), err))
			continue
		}
		manifests = append(manifests, ms...)
	}

	return &updatePayload{
		imageRef:  imageRef,
		manifests: manifests,
	}, nil
}

func (optr *Operator) updatePayloadDir(config *cvv1.CVOConfig) (string, error) {
	ret := defaultUpdatePayloadDir
	tdir, err := optr.targetUpdatePayloadDir(config)
	if err != nil {
		return "", fmt.Errorf("error fetching targetUpdatePayloadDir: %v", err)
	}
	if len(tdir) > 0 {
		ret = tdir
	}
	return ret, nil
}

func (optr *Operator) targetUpdatePayloadDir(config *cvv1.CVOConfig) (string, error) {
	if !isTargetSet(config.DesiredUpdate) {
		return "", nil
	}

	// XXX: check if target and default versions are equal
	// 		requires cincinati.json

	tdir := filepath.Join(targetUpdatePayloadsDir, config.DesiredUpdate.Version)
	_, err := os.Stat(tdir)
	if err != nil && !os.IsNotExist(err) {
		return "", err
	}

	if os.IsNotExist(err) {
		if err := optr.fetchUpdatePayloadToDir(tdir, config); err != nil {
			return "", err
		}
	}
	if err := validateUpdatePayload(tdir); err != nil {
		return "", err
	}
	return tdir, nil
}

func validateUpdatePayload(dir string) error {
	// XXX: validate that cincinnati.json is correct
	// 		validate image-references files is correct.
	return nil
}

func (optr *Operator) fetchUpdatePayloadToDir(dir string, config *cvv1.CVOConfig) error {
	var (
		version         = config.DesiredUpdate.Version
		payload         = config.DesiredUpdate.Payload
		name            = fmt.Sprintf("%s-%s-%s", optr.name, version, randutil.String(5))
		namespace       = optr.namespace
		deadline        = pointer.Int64Ptr(2 * 60)
		nodeSelectorKey = "node-role.kubernetes.io/master"
		nodename        = optr.nodename
		cmd             = []string{"/bin/sh"}
		args            = []string{"-c", fmt.Sprintf("cp -r %s %s", defaultUpdatePayloadDir, dir)}
	)

	job := &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		Spec: batchv1.JobSpec{
			ActiveDeadlineSeconds: deadline,
			Template: corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{{
						Name:    "payload",
						Image:   payload,
						Command: cmd,
						Args:    args,
						VolumeMounts: []corev1.VolumeMount{{
							MountPath: targetUpdatePayloadsDir,
							Name:      "payloads",
						}},
						SecurityContext: &corev1.SecurityContext{
							Privileged: pointer.BoolPtr(true),
						},
					}},
					Volumes: []corev1.Volume{{
						Name: "payloads",
						VolumeSource: corev1.VolumeSource{
							HostPath: &corev1.HostPathVolumeSource{
								Path: targetUpdatePayloadsDir,
							},
						},
					}},
					NodeName: nodename,
					NodeSelector: map[string]string{
						nodeSelectorKey: "",
					},
					Tolerations: []corev1.Toleration{{
						Key: nodeSelectorKey,
					}},
					RestartPolicy: corev1.RestartPolicyOnFailure,
				},
			},
		},
	}

	_, err := optr.kubeClient.BatchV1().Jobs(job.Namespace).Create(job)
	if err != nil {
		return err
	}
	return resourcebuilder.WaitForJobCompletion(optr.kubeClient.BatchV1(), job)
}

func isTargetSet(desired cvv1.Update) bool {
	return desired.Payload != "" &&
		desired.Version != ""
}

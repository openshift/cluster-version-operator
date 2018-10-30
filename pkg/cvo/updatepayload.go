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
	utilerrors "k8s.io/apimachinery/pkg/util/errors"
	randutil "k8s.io/apimachinery/pkg/util/rand"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/utils/pointer"

	"github.com/golang/glog"
	"github.com/openshift/cluster-version-operator/lib"
	"github.com/openshift/cluster-version-operator/lib/resourcebuilder"
	"github.com/openshift/cluster-version-operator/lib/resourceread"
	cvv1 "github.com/openshift/cluster-version-operator/pkg/apis/config.openshift.io/v1"
)

type updatePayload struct {
	// XXX: cincinatti.json struct

	imageRef *imagev1.ImageStream

	manifests []lib.Manifest
}

const (
	defaultUpdatePayloadDir = "/"
	targetUpdatePayloadsDir = "/etc/cvo/updatepayloads"

	cvoManifestDir     = "manifests"
	releaseManifestDir = "release-manifests"

	cincinnatiJSONFile  = "cincinnati.json"
	imageReferencesFile = "image-references"
)

func loadUpdatePayload(dir, releaseImage string) (*updatePayload, error) {
	glog.V(4).Infof("Loading updatepayload from %q", dir)
	if err := validateUpdatePayload(dir); err != nil {
		return nil, err
	}
	var (
		cvoDir     = filepath.Join(dir, cvoManifestDir)
		releaseDir = filepath.Join(dir, releaseManifestDir)
	)

	// XXX: load cincinnatiJSONFile
	cjf := filepath.Join(releaseDir, cincinnatiJSONFile)
	// XXX: load imageReferencesFile
	irf := filepath.Join(releaseDir, imageReferencesFile)
	imageRefData, err := ioutil.ReadFile(irf)
	if err != nil {
		return nil, err
	}

	imageRef := resourceread.ReadImageStreamV1OrDie(imageRefData)
	mrc := manifestRenderConfig{ReleaseImage: releaseImage}

	var manifests []lib.Manifest
	var errs []error
	tasks := []struct {
		idir       string
		preprocess func([]byte) ([]byte, error)
		skipFiles  sets.String
	}{{
		idir:       cvoDir,
		preprocess: func(ib []byte) ([]byte, error) { return renderManifest(mrc, ib) },
		skipFiles:  sets.NewString(),
	}, {
		idir:       releaseDir,
		preprocess: nil,
		skipFiles:  sets.NewString(cjf, irf),
	}}
	for _, task := range tasks {
		files, err := ioutil.ReadDir(task.idir)
		if err != nil {
			return nil, err
		}

		for _, file := range files {
			if file.IsDir() {
				continue
			}

			p := filepath.Join(task.idir, file.Name())
			if task.skipFiles.Has(p) {
				continue
			}

			raw, err := ioutil.ReadFile(p)
			if err != nil {
				errs = append(errs, fmt.Errorf("error reading file %s: %v", file.Name(), err))
				continue
			}
			if task.preprocess != nil {
				raw, err = task.preprocess(raw)
				if err != nil {
					errs = append(errs, fmt.Errorf("error running preprocess on %s: %v", file.Name(), err))
					continue
				}
			}
			ms, err := lib.ParseManifests(bytes.NewReader(raw))
			if err != nil {
				errs = append(errs, fmt.Errorf("error parsing %s: %v", file.Name(), err))
				continue
			}
			manifests = append(manifests, ms...)
		}
	}

	agg := utilerrors.NewAggregate(errs)
	if agg != nil {
		return nil, fmt.Errorf("error loading manifests from %s: %v", dir, agg.Error())
	}
	return &updatePayload{
		imageRef:  imageRef,
		manifests: manifests,
	}, nil
}

func (optr *Operator) updatePayloadDir(config *cvv1.ClusterVersion) (string, error) {
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

func (optr *Operator) targetUpdatePayloadDir(config *cvv1.ClusterVersion) (string, error) {
	if !isTargetSet(config.DesiredUpdate) {
		return "", nil
	}

	tdir := filepath.Join(targetUpdatePayloadsDir, config.DesiredUpdate.Version)
	err := validateUpdatePayload(tdir)
	if os.IsNotExist(err) {
		// the dirs don't exist, try fetching the payload to tdir.
		if err := optr.fetchUpdatePayloadToDir(tdir, config); err != nil {
			return "", err
		}
	}
	if err != nil {
		return "", err
	}

	// now that payload has been loaded check validation.
	if err := validateUpdatePayload(tdir); err != nil {
		return "", err
	}
	return tdir, nil
}

func validateUpdatePayload(dir string) error {
	// XXX: validate that cincinnati.json is correct
	// 		validate image-references files is correct.

	// make sure cvo and release manifests dirs exist.
	_, err := os.Stat(filepath.Join(dir, cvoManifestDir))
	if err != nil {
		return err
	}
	releaseDir := filepath.Join(dir, releaseManifestDir)
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

func (optr *Operator) fetchUpdatePayloadToDir(dir string, config *cvv1.ClusterVersion) error {
	var (
		version         = config.DesiredUpdate.Version
		payload         = config.DesiredUpdate.Payload
		name            = fmt.Sprintf("%s-%s-%s", optr.name, version, randutil.String(5))
		namespace       = optr.namespace
		deadline        = pointer.Int64Ptr(2 * 60)
		nodeSelectorKey = "node-role.kubernetes.io/master"
		nodename        = optr.nodename
		cmd             = []string{"/bin/sh"}
		args            = []string{"-c", copyPayloadCmd(dir)}
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

// copyPayloadCmd returns command that copies cvo and release manifests from deafult location
// to the target dir.
// It is made up of 2 commands:
// `mkdir -p <target dir> && mv <default cvo manifest dir> <target cvo manifests dir>`
// `mkdir -p <target dir> && mv <default release manifest dir> <target release manifests dir>`
func copyPayloadCmd(tdir string) string {
	var (
		fromCVOPath = filepath.Join(defaultUpdatePayloadDir, cvoManifestDir)
		toCVOPath   = filepath.Join(tdir, cvoManifestDir)
		cvoCmd      = fmt.Sprintf("mkdir -p %s && mv %s %s", tdir, fromCVOPath, toCVOPath)

		fromReleasePath = filepath.Join(defaultUpdatePayloadDir, releaseManifestDir)
		toReleasePath   = filepath.Join(tdir, releaseManifestDir)
		releaseCmd      = fmt.Sprintf("mkdir -p %s && mv %s %s", tdir, fromReleasePath, toReleasePath)
	)
	return fmt.Sprintf("%s && %s", cvoCmd, releaseCmd)
}

func isTargetSet(desired cvv1.Update) bool {
	return desired.Payload != "" &&
		desired.Version != ""
}

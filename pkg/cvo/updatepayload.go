package cvo

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"hash/fnv"
	"io/ioutil"
	"os"
	"path/filepath"

	"github.com/golang/glog"
	imagev1 "github.com/openshift/api/image/v1"
	"github.com/pkg/errors"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	utilerrors "k8s.io/apimachinery/pkg/util/errors"
	randutil "k8s.io/apimachinery/pkg/util/rand"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/utils/pointer"

	"github.com/openshift/cluster-version-operator/lib"
	"github.com/openshift/cluster-version-operator/lib/resourcebuilder"
	"github.com/openshift/cluster-version-operator/lib/resourceread"
	cvv1 "github.com/openshift/cluster-version-operator/pkg/apis/config.openshift.io/v1"
)

type updatePayload struct {
	releaseImage   string
	releaseVersion string
	// XXX: cincinatti.json struct

	imageRef *imagev1.ImageStream

	// manifestHash is a hash of the manifests included in this payload
	manifestHash string
	manifests    []lib.Manifest
}

const (
	defaultUpdatePayloadDir = "/"
	targetUpdatePayloadsDir = "/etc/cvo/updatepayloads"

	cvoManifestDir     = "manifests"
	releaseManifestDir = "release-manifests"

	cincinnatiJSONFile  = "cincinnati.json"
	imageReferencesFile = "image-references"
)

type payloadTasks struct {
	idir       string
	preprocess func([]byte) ([]byte, error)
	skipFiles  sets.String
}

func loadUpdatePayloadMetadata(dir, releaseImage string) (*updatePayload, []payloadTasks, error) {
	glog.V(4).Infof("Loading updatepayload from %q", dir)
	if err := validateUpdatePayload(dir); err != nil {
		return nil, nil, err
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
		return nil, nil, err
	}

	imageRef, err := resourceread.ReadImageStreamV1(imageRefData)
	if err != nil {
		return nil, nil, errors.Wrapf(err, "invalid image-references data %s", irf)
	}

	mrc := manifestRenderConfig{ReleaseImage: releaseImage}
	tasks := []payloadTasks{{
		idir:       cvoDir,
		preprocess: func(ib []byte) ([]byte, error) { return renderManifest(mrc, ib) },
		skipFiles:  sets.NewString(),
	}, {
		idir:       releaseDir,
		preprocess: nil,
		skipFiles:  sets.NewString(cjf, irf),
	}}
	return &updatePayload{imageRef: imageRef, releaseImage: releaseImage, releaseVersion: imageRef.Name}, tasks, nil
}

func loadUpdatePayload(dir, releaseImage string) (*updatePayload, error) {
	payload, tasks, err := loadUpdatePayloadMetadata(dir, releaseImage)
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
			manifests = append(manifests, ms...)
		}
	}

	agg := utilerrors.NewAggregate(errs)
	if agg != nil {
		return nil, &updateError{
			Reason:  "UpdatePayloadIntegrity",
			Message: fmt.Sprintf("Error loading manifests from %s: %v", dir, agg.Error()),
		}
	}

	hash := fnv.New64()
	for _, manifest := range manifests {
		hash.Write(manifest.Raw)
	}

	payload.manifestHash = base64.URLEncoding.EncodeToString(hash.Sum(nil))
	payload.manifests = manifests
	return payload, nil
}

func (optr *Operator) baseDirectory() string {
	if len(optr.payloadDir) == 0 {
		return defaultUpdatePayloadDir
	}
	return optr.payloadDir
}

func (optr *Operator) updatePayloadDir(config *cvv1.ClusterVersion) (string, error) {
	tdir, err := optr.targetUpdatePayloadDir(config)
	if err != nil {
		return "", &updateError{
			Reason:  "UpdatePayloadRetrievalFailed",
			Message: fmt.Sprintf("Unable to download and prepare the update: %v", err),
		}
	}
	if len(tdir) > 0 {
		return tdir, nil
	}
	return optr.baseDirectory(), nil
}

func (optr *Operator) targetUpdatePayloadDir(config *cvv1.ClusterVersion) (string, error) {
	if !isTargetSet(config.Spec.DesiredUpdate) {
		return "", nil
	}

	tdir := filepath.Join(targetUpdatePayloadsDir, config.Spec.DesiredUpdate.Version)
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
	if config.Spec.DesiredUpdate == nil {
		return fmt.Errorf("cannot fetch payload for empty desired update")
	}
	var (
		version         = config.Spec.DesiredUpdate.Version
		payload         = config.Spec.DesiredUpdate.Payload
		name            = fmt.Sprintf("%s-%s-%s", optr.name, version, randutil.String(5))
		namespace       = optr.namespace
		deadline        = pointer.Int64Ptr(2 * 60)
		nodeSelectorKey = "node-role.kubernetes.io/master"
		nodename        = optr.nodename
		cmd             = []string{"/bin/sh"}
		args            = []string{"-c", copyPayloadCmd(dir)}
	)

	// pull and resolve the payload image to a digest before we launch the job to copy the results - this gives
	// us better access to messages for helping admins debug why the payload couldn't be retrieved, and also
	// allows us to leverage the container runtime to resolve the payload string to an image digest.
	resolvedPayload, err := optr.pullAndResolvePayloadImage(namespace, name, payload, nodename, nodeSelectorKey)
	if err != nil {
		return err
	}
	payload = resolvedPayload
	config.Spec.DesiredUpdate.Payload = resolvedPayload

	// copy the exact known payload to the desired location
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

	if _, err := optr.kubeClient.BatchV1().Jobs(job.Namespace).Create(job); err != nil {
		return err
	}
	return resourcebuilder.WaitForJobCompletion(optr.kubeClient.BatchV1(), job)
}

// pullAndResolvePayloadImage attempts to pull the pod and resolve the payload string to a digest.
// If the container runtime reports no imageID on container status, we use the original payload string.
func (optr *Operator) pullAndResolvePayloadImage(namespace, name, payload, nodeName, nodeSelectorKey string) (string, error) {
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{{
				Name:    "verify",
				Image:   payload,
				Command: []string{"/bin/sh"},
				Args:    []string{"-c", `echo "ok" > /tmp/msg`},

				TerminationMessagePath: "/tmp/msg",
			}},
			NodeName: nodeName,
			NodeSelector: map[string]string{
				nodeSelectorKey: "",
			},
			Tolerations: []corev1.Toleration{{
				Key: nodeSelectorKey,
			}},
			RestartPolicy: corev1.RestartPolicyOnFailure,
		},
	}
	_, err := optr.kubeClient.CoreV1().Pods(pod.Namespace).Create(pod)
	if err != nil {
		return "", err
	}
	defer func() {
		optr.kubeClient.CoreV1().Pods(pod.Namespace).Delete(pod.Name, nil)
	}()
	pod, err = resourcebuilder.WaitForPodSuccess(optr.kubeClient.CoreV1(), pod)
	if err != nil {
		if pod != nil {
			switch {
			case pod.Status.Phase == corev1.PodFailed && len(pod.Status.Message) > 0:
				return "", fmt.Errorf("payload was not accessible: %s", pod.Status.Message)
			case len(pod.Status.ContainerStatuses) > 0 && pod.Status.ContainerStatuses[0].State.Waiting != nil:
				w := pod.Status.ContainerStatuses[0].State.Waiting
				if len(w.Message) > 0 {
					return "", fmt.Errorf("payload could not be started: %s", w.Message)
				}
				// TODO: return more detailed errors here, such as not authorized, image not found, digest
				// doesn't match tag, image not signed, etc.
			}
		}
		return "", err
	}
	imageID := pod.Status.ContainerStatuses[0].ImageID
	if len(imageID) == 0 {
		// TODO: should this be a hard error? ImageID has been supported for a long time.
		// If the container runtime is racy this could result in not locking to a digest
		// when were perfectly capable of it.
		glog.V(2).Infof("Unable to resolve the payload image by digest: %s", imageID)
		return payload, nil
	}
	glog.V(2).Infof("Identified the payload image by digest: %s", imageID)
	return imageID, nil
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

func isTargetSet(desired *cvv1.Update) bool {
	return desired != nil && desired.Payload != "" &&
		desired.Version != ""
}

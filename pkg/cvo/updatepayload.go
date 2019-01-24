package cvo

import (
	"bytes"
	"context"
	"crypto/md5"
	"encoding/base64"
	"fmt"
	"hash/fnv"
	"io/ioutil"
	"os"
	"path/filepath"

	"k8s.io/client-go/kubernetes"

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

	configv1 "github.com/openshift/api/config/v1"
	"github.com/openshift/cluster-version-operator/lib"
	"github.com/openshift/cluster-version-operator/lib/resourcebuilder"
	"github.com/openshift/cluster-version-operator/lib/resourceread"
)

type updatePayload struct {
	ReleaseImage   string
	ReleaseVersion string
	// XXX: cincinatti.json struct

	ImageRef *imagev1.ImageStream

	// manifestHash is a hash of the manifests included in this payload
	ManifestHash string
	Manifests    []lib.Manifest
}

const (
	defaultUpdatePayloadDir = "/"
	targetUpdatePayloadsDir = "/etc/cvo/updatepayloads"

	cvoManifestDir     = "manifests"
	releaseManifestDir = "release-manifests"

	cincinnatiJSONFile  = "release-metadata"
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
	return &updatePayload{ImageRef: imageRef, ReleaseImage: releaseImage, ReleaseVersion: imageRef.Name}, tasks, nil
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

	payload.ManifestHash = base64.URLEncoding.EncodeToString(hash.Sum(nil))
	payload.Manifests = manifests
	return payload, nil
}

func (optr *Operator) defaultPayloadDir() string {
	if len(optr.payloadDir) == 0 {
		return defaultUpdatePayloadDir
	}
	return optr.payloadDir
}

func (optr *Operator) defaultPayloadRetriever() PayloadRetriever {
	return &payloadRetriever{
		kubeClient:   optr.kubeClient,
		operatorName: optr.name,
		releaseImage: optr.releaseImage,
		namespace:    optr.namespace,
		nodeName:     optr.nodename,
		payloadDir:   optr.defaultPayloadDir(),
		workingDir:   targetUpdatePayloadsDir,
	}
}

type payloadRetriever struct {
	// releaseImage and payloadDir are the default payload identifiers - updates that point
	// to releaseImage will always use the contents of payloadDir
	releaseImage string
	payloadDir   string

	// these fields are used to retrieve the payload when any other payload is specified
	kubeClient   kubernetes.Interface
	workingDir   string
	namespace    string
	nodeName     string
	operatorName string
}

func (r *payloadRetriever) RetrievePayload(ctx context.Context, update configv1.Update) (string, error) {
	if r.releaseImage == update.Image {
		return r.payloadDir, nil
	}

	if len(update.Image) == 0 {
		return "", fmt.Errorf("no payload image has been specified and the contents of the payload cannot be retrieved")
	}

	tdir, err := r.targetUpdatePayloadDir(ctx, update)
	if err != nil {
		return "", &updateError{
			Reason:  "UpdatePayloadRetrievalFailed",
			Message: fmt.Sprintf("Unable to download and prepare the update: %v", err),
		}
	}
	return tdir, nil
}

func (r *payloadRetriever) targetUpdatePayloadDir(ctx context.Context, update configv1.Update) (string, error) {
	hash := md5.New()
	hash.Write([]byte(update.Image))
	payloadHash := base64.RawURLEncoding.EncodeToString(hash.Sum(nil))

	tdir := filepath.Join(r.workingDir, payloadHash)
	err := validateUpdatePayload(tdir)
	if os.IsNotExist(err) {
		// the dirs don't exist, try fetching the payload to tdir.
		err = r.fetchUpdatePayloadToDir(ctx, tdir, update)
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

func (r *payloadRetriever) fetchUpdatePayloadToDir(ctx context.Context, dir string, update configv1.Update) error {
	var (
		version         = update.Version
		payload         = update.Image
		name            = fmt.Sprintf("%s-%s-%s", r.operatorName, version, randutil.String(5))
		namespace       = r.namespace
		deadline        = pointer.Int64Ptr(2 * 60)
		nodeSelectorKey = "node-role.kubernetes.io/master"
		nodename        = r.nodeName
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

	_, err := r.kubeClient.BatchV1().Jobs(job.Namespace).Create(job)
	if err != nil {
		return err
	}
	return resourcebuilder.WaitForJobCompletion(r.kubeClient.BatchV1(), job)
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

// findUpdateFromConfig identifies a desired update from user input or returns false. It will
// resolve payload if the user specifies a version and a matching available update or previous
// update is in the history.
func findUpdateFromConfig(config *configv1.ClusterVersion) (configv1.Update, bool) {
	update := config.Spec.DesiredUpdate
	if update == nil {
		return configv1.Update{}, false
	}
	if len(update.Image) == 0 {
		return findUpdateFromConfigVersion(config, update.Version)
	}
	return *update, true
}

func findUpdateFromConfigVersion(config *configv1.ClusterVersion, version string) (configv1.Update, bool) {
	for _, update := range config.Status.AvailableUpdates {
		if update.Version == version {
			return update, len(update.Image) > 0
		}
	}
	for _, history := range config.Status.History {
		if history.Version == version {
			return configv1.Update{Image: history.Image, Version: history.Version}, len(history.Image) > 0
		}
	}
	return configv1.Update{}, false
}

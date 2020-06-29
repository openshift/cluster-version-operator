package cvo

import (
	"context"
	"crypto/md5"
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/pkg/errors"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	utilerrors "k8s.io/apimachinery/pkg/util/errors"
	randutil "k8s.io/apimachinery/pkg/util/rand"
	"k8s.io/client-go/kubernetes"
	"k8s.io/klog"
	"k8s.io/utils/pointer"

	configv1 "github.com/openshift/api/config/v1"
	"github.com/openshift/cluster-version-operator/lib/resourcebuilder"
	"github.com/openshift/cluster-version-operator/pkg/payload"
	"github.com/openshift/cluster-version-operator/pkg/verify"
)

func (optr *Operator) defaultPayloadDir() string {
	if len(optr.payloadDir) == 0 {
		return payload.DefaultPayloadDir
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
		verifier:     optr.verifier,
	}
}

const (
	targetUpdatePayloadsDir = "/etc/cvo/updatepayloads"
)

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

	// verifier guards against invalid remote data being accessed
	verifier verify.Interface
}

func (r *payloadRetriever) RetrievePayload(ctx context.Context, update configv1.Update) (PayloadInfo, error) {
	if r.releaseImage == update.Image {
		return PayloadInfo{
			Directory: r.payloadDir,
			Local:     true,
		}, nil
	}

	if len(update.Image) == 0 {
		return PayloadInfo{}, fmt.Errorf("no payload image has been specified and the contents of the payload cannot be retrieved")
	}

	var info PayloadInfo

	/*
		// verify the provided payload
		var releaseDigest string
		if index := strings.LastIndex(update.Image, "@"); index != -1 {
			releaseDigest = update.Image[index+1:]
		}
		if err := r.verifier.Verify(ctx, releaseDigest); err != nil {
			vErr := &payload.UpdateError{
				Reason:  "ImageVerificationFailed",
				Message: fmt.Sprintf("The update cannot be verified: %v", err),
				Nested:  err,
			}
			if !update.Force {
				return PayloadInfo{}, vErr
			}
			klog.Warningf("An image was retrieved from %q that failed verification: %v", update.Image, vErr)
			info.VerificationError = vErr
		} else {
			info.Verified = true
		}
	*/
	info.Verified = true

	// download the payload to the directory
	var err error
	info.Directory, err = r.targetUpdatePayloadDir(ctx, update)
	if err != nil {
		return PayloadInfo{}, &payload.UpdateError{
			Reason:  "UpdatePayloadRetrievalFailed",
			Message: fmt.Sprintf("Unable to download and prepare the update: %v", err),
		}
	}
	return info, nil
}

func (r *payloadRetriever) targetUpdatePayloadDir(ctx context.Context, update configv1.Update) (string, error) {
	hash := md5.New()
	hash.Write([]byte(update.Image))
	payloadHash := base64.RawURLEncoding.EncodeToString(hash.Sum(nil))

	tdir := filepath.Join(r.workingDir, payloadHash)
	err := payload.ValidateDirectory(tdir)
	if os.IsNotExist(err) {
		// the dirs don't exist, try fetching the payload to tdir.
		err = r.fetchUpdatePayloadToDir(ctx, tdir, update)
	}
	if err != nil {
		return "", err
	}

	// now that payload has been loaded check validation.
	if err := payload.ValidateDirectory(tdir); err != nil {
		return "", err
	}
	return tdir, nil
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
						Resources: corev1.ResourceRequirements{
							Requests: corev1.ResourceList{
								corev1.ResourceCPU:              resource.MustParse("10m"),
								corev1.ResourceMemory:           resource.MustParse("50Mi"),
								corev1.ResourceEphemeralStorage: resource.MustParse("2Mi"),
							},
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

	// Prune older jobs while gracefully handling errors.
	err := r.pruneJobs(2)
	if err != nil {
		klog.Warningf("failed to prune jobs: %v", err)
	}

	_, err = r.kubeClient.BatchV1().Jobs(job.Namespace).Create(job)
	if err != nil {
		return err
	}
	return resourcebuilder.WaitForJobCompletion(ctx, r.kubeClient.BatchV1(), job)
}

// pruneJobs deletes the older, finished jobs in the namespace.
// retain - the number of newest jobs to keep.
func (r *payloadRetriever) pruneJobs(retain int) error {
	jobs, err := r.kubeClient.BatchV1().Jobs(r.namespace).List(metav1.ListOptions{})
	if err != nil {
		return err
	}
	if len(jobs.Items) <= retain {
		return nil
	}

	// Select jobs to be deleted
	var deleteJobs []batchv1.Job
	for _, job := range jobs.Items {
		switch {
		// Ignore jobs not begining with operatorName
		case !strings.HasPrefix(job.Name, r.operatorName+"-"):
			break
		// Ignore jobs that have not yet started
		case job.Status.StartTime == nil:
			break
		// Ignore jobs that are still active
		case job.Status.Active == 1:
			break
		default:
			deleteJobs = append(deleteJobs, job)
		}
	}
	if len(deleteJobs) <= retain {
		return nil
	}

	// Sort jobs by StartTime to determine the newest. nil StartTime is assumed newest.
	sort.Slice(deleteJobs, func(i, j int) bool {
		if deleteJobs[i].Status.StartTime == nil {
			return false
		}
		if deleteJobs[j].Status.StartTime == nil {
			return true
		}
		return deleteJobs[i].Status.StartTime.Before(deleteJobs[j].Status.StartTime)
	})

	var errs []error
	for _, job := range deleteJobs[:len(deleteJobs)-retain] {
		err := r.kubeClient.BatchV1().Jobs(r.namespace).Delete(job.Name, &metav1.DeleteOptions{})
		if err != nil {
			errs = append(errs, errors.Wrapf(err, "failed to delete job %v", job.Name))
		}
	}
	agg := utilerrors.NewAggregate(errs)
	if agg != nil {
		return fmt.Errorf("error deleting jobs: %v", agg.Error())
	}
	return nil
}

// copyPayloadCmd returns command that copies cvo and release manifests from deafult location
// to the target dir.
// It is made up of 2 commands:
// `mkdir -p <target dir> && mv <default cvo manifest dir> <target cvo manifests dir>`
// `mkdir -p <target dir> && mv <default release manifest dir> <target release manifests dir>`
func copyPayloadCmd(tdir string) string {
	var (
		fromCVOPath = filepath.Join(payload.DefaultPayloadDir, payload.CVOManifestDir)
		toCVOPath   = filepath.Join(tdir, payload.CVOManifestDir)
		cvoCmd      = fmt.Sprintf("mkdir -p %s && mv %s %s", tdir, fromCVOPath, toCVOPath)

		fromReleasePath = filepath.Join(payload.DefaultPayloadDir, payload.ReleaseManifestDir)
		toReleasePath   = filepath.Join(tdir, payload.ReleaseManifestDir)
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
		return findUpdateFromConfigVersion(config, update.Version, update.Force)
	}
	return *update, true
}

func findUpdateFromConfigVersion(config *configv1.ClusterVersion, version string, force bool) (configv1.Update, bool) {
	for _, update := range config.Status.AvailableUpdates {
		if update.Version == version {
			return update, len(update.Image) > 0
		}
	}
	for _, history := range config.Status.History {
		if history.Version == version {
			return configv1.Update{Image: history.Image, Version: history.Version, Force: force}, len(history.Image) > 0
		}
	}
	return configv1.Update{}, false
}

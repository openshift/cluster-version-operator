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
	"time"

	"github.com/pkg/errors"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	utilerrors "k8s.io/apimachinery/pkg/util/errors"
	randutil "k8s.io/apimachinery/pkg/util/rand"
	"k8s.io/client-go/kubernetes"
	"k8s.io/klog/v2"
	"k8s.io/utils/pointer"

	configv1 "github.com/openshift/api/config/v1"
	"github.com/openshift/cluster-version-operator/lib/resourcebuilder"
	"github.com/openshift/cluster-version-operator/pkg/payload"
	"github.com/openshift/library-go/pkg/verify"
)

func (optr *Operator) defaultPayloadDir() string {
	if len(optr.payloadDir) == 0 {
		return payload.DefaultPayloadDir
	}
	return optr.payloadDir
}

func (optr *Operator) defaultPayloadRetriever() PayloadRetriever {
	return &payloadRetriever{
		kubeClient:      optr.kubeClient,
		operatorName:    optr.name,
		releaseImage:    optr.release.Image,
		namespace:       optr.namespace,
		nodeName:        optr.nodename,
		payloadDir:      optr.defaultPayloadDir(),
		workingDir:      targetUpdatePayloadsDir,
		verifier:        optr.verifier,
		retrieveTimeout: 4 * time.Minute,
	}
}

const (
	targetUpdatePayloadsDir = "/etc/cvo/updatepayloads"
)

// downloadFunc downloads the requested update and returns either a path on the local filesystem
// containing extracted manifests or an error
// The type exists so that tests for payloadRetriever.RetrievePayload can mock this functionality
// by setting payloadRetriever.downloader.
type downloadFunc func(context.Context, configv1.Update) (string, error)

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

	// downloader is called to download the requested update to local filesystem. It should only be
	// set to non-nil value in tests. When this is nil, payloadRetriever.targetUpdatePayloadDir
	// is called as a downloader.
	downloader downloadFunc

	// retrieveTimeout limits the time spent in payloadRetriever.RetrievePayload. This timeout is only
	// applied when the context passed into that method is unbounded; otherwise the method respects
	// the deadline set in the input context.
	retrieveTimeout time.Duration
}

// RetrievePayload verifies, downloads and extracts to local filesystem the payload image specified
// by update. If the input context has a deadline, it is respected, otherwise r.retrieveTimeout
// applies. When update.Force is true, the verification is still performed, but the method proceeds
// even when the image cannot be verified successfully.
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

	// verify the provided payload
	var releaseDigest string
	if index := strings.LastIndex(update.Image, "@"); index != -1 {
		releaseDigest = update.Image[index+1:]
	}

	var downloadCtx context.Context
	downloadDeadline, ok := ctx.Deadline()
	if ok {
		downloadCtx = ctx
	} else {
		downloadDeadline = time.Now().Add(r.retrieveTimeout)
		var downloadCtxCancel context.CancelFunc
		downloadCtx, downloadCtxCancel = context.WithDeadline(ctx, downloadDeadline)
		defer downloadCtxCancel()
	}

	verifyTimeout := time.Until(downloadDeadline) / 2
	verifyCtx, verifyCtxCancel := context.WithTimeout(ctx, verifyTimeout)
	defer verifyCtxCancel()

	var info PayloadInfo
	if err := r.verifier.Verify(verifyCtx, releaseDigest); err != nil {
		vErr := &payload.UpdateError{
			Reason:  "ImageVerificationFailed",
			Message: fmt.Sprintf("The update cannot be verified: %v", err),
			Nested:  err,
		}
		if !update.Force {
			return PayloadInfo{}, vErr
		}
		vErr.Message = fmt.Sprintf("Target release version=%q image=%q cannot be verified, but continuing anyway because the update was forced: %v", update.Version, update.Image, err)
		klog.Warning(vErr)
		info.VerificationError = vErr
	} else {
		info.Verified = true
	}

	if r.downloader == nil {
		r.downloader = r.targetUpdatePayloadDir
	}
	// download the payload to the directory
	var err error
	info.Directory, err = r.downloader(downloadCtx, update)
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

	// Prune older jobs and directories while gracefully handling errors.
	if err := r.pruneJobs(ctx, 0); err != nil {
		klog.Warningf("failed to prune jobs: %v", err)
	}

	if err := payload.ValidateDirectory(tdir); os.IsNotExist(err) {
		// the dirs don't exist, try fetching the payload to tdir.
		if err := r.fetchUpdatePayloadToDir(ctx, tdir, update); err != nil {
			return "", err
		}
	} else if err != nil {
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
		image           = update.Image
		name            = fmt.Sprintf("%s-%s-%s", r.operatorName, version, randutil.String(5))
		namespace       = r.namespace
		deadline        = pointer.Int64(2 * 60)
		nodeSelectorKey = "node-role.kubernetes.io/master"
		nodename        = r.nodeName
	)

	baseDir, targetName := filepath.Split(dir)
	tmpDir := filepath.Join(baseDir, fmt.Sprintf("%s-%s", targetName, randutil.String(5)))

	setContainerDefaults := func(container corev1.Container) corev1.Container {
		container.Image = image
		container.VolumeMounts = []corev1.VolumeMount{{
			MountPath: targetUpdatePayloadsDir,
			Name:      "payloads",
		}}
		container.SecurityContext = &corev1.SecurityContext{
			Privileged:             pointer.Bool(true),
			ReadOnlyRootFilesystem: pointer.Bool(false),
		}
		container.Resources = corev1.ResourceRequirements{
			Requests: corev1.ResourceList{
				corev1.ResourceCPU:              resource.MustParse("10m"),
				corev1.ResourceMemory:           resource.MustParse("50Mi"),
				corev1.ResourceEphemeralStorage: resource.MustParse("2Mi"),
			},
		}
		return container
	}

	job := &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		Spec: batchv1.JobSpec{
			ActiveDeadlineSeconds: deadline,
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{
						"openshift.io/required-scc": "privileged",
					},
				},
				Spec: corev1.PodSpec{
					InitContainers: []corev1.Container{
						setContainerDefaults(corev1.Container{
							Name:       "cleanup",
							Command:    []string{"sh", "-c", "rm -fR ./*"},
							WorkingDir: baseDir,
						}),
						setContainerDefaults(corev1.Container{
							Name:    "make-temporary-directory",
							Command: []string{"mkdir", tmpDir},
						}),
						setContainerDefaults(corev1.Container{
							Name: "move-operator-manifests-to-temporary-directory",
							Command: []string{
								"mv",
								filepath.Join(payload.DefaultPayloadDir, payload.CVOManifestDir),
								filepath.Join(tmpDir, payload.CVOManifestDir),
							},
						}),
						setContainerDefaults(corev1.Container{
							Name: "move-release-manifests-to-temporary-directory",
							Command: []string{
								"mv",
								filepath.Join(payload.DefaultPayloadDir, payload.ReleaseManifestDir),
								filepath.Join(tmpDir, payload.ReleaseManifestDir),
							},
						}),
					},
					Containers: []corev1.Container{
						setContainerDefaults(corev1.Container{
							Name:    "rename-to-final-location",
							Command: []string{"mv", tmpDir, dir},
						}),
					},
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
					PriorityClassName: "openshift-user-critical",
					Tolerations: []corev1.Toleration{{
						Key: nodeSelectorKey,
					}},
					RestartPolicy: corev1.RestartPolicyOnFailure,
				},
			},
		},
	}

	if _, err := r.kubeClient.BatchV1().Jobs(job.Namespace).Create(ctx, job, metav1.CreateOptions{}); err != nil {
		return err
	}
	return resourcebuilder.WaitForJobCompletion(ctx, r.kubeClient.BatchV1(), job)
}

// pruneJobs deletes the older, finished jobs in the namespace.
// retain - the number of newest jobs to keep.
func (r *payloadRetriever) pruneJobs(ctx context.Context, retain int) error {
	jobs, err := r.kubeClient.BatchV1().Jobs(r.namespace).List(ctx, metav1.ListOptions{})
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
		// Ignore jobs not beginning with operatorName
		case !strings.HasPrefix(job.Name, r.operatorName+"-"):
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
		err := r.kubeClient.BatchV1().Jobs(r.namespace).Delete(ctx, job.Name, metav1.DeleteOptions{})
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

// findUpdateFromConfig identifies a desired update from user input or returns false. If
// image is specified it simply returns the desired update since image will be used. If
// the desired architecture is changed to multi it resolves the payload using the current
// version since that's the only valid available update. Otherwise it attempts to resolve
// the payload using the specified desired version.
func findUpdateFromConfig(config *configv1.ClusterVersion, currentArch string) (configv1.Update, bool) {
	update := config.Spec.DesiredUpdate
	if update == nil {
		return configv1.Update{}, false
	}
	if len(update.Image) == 0 {
		version := update.Version

		// Architecture changed to multi so only valid update is the multi arch version of current version
		if update.Architecture == configv1.ClusterVersionArchitectureMulti &&
			currentArch != string(configv1.ClusterVersionArchitectureMulti) {
			version = config.Status.Desired.Version
		}
		return findUpdateFromConfigVersion(config, version, update.Force)
	}
	return *update, true
}

func findUpdateFromConfigVersion(config *configv1.ClusterVersion, version string, force bool) (configv1.Update, bool) {
	for _, update := range config.Status.AvailableUpdates {
		if update.Version == version && len(update.Image) > 0 {
			return configv1.Update{
				Version: version,
				Image:   update.Image,
				Force:   force,
			}, true
		}
	}
	return configv1.Update{}, false
}

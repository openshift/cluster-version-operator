package cvo

import (
	"context"
	"crypto/md5"
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/pkg/errors"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/runtime"
	utilerrors "k8s.io/apimachinery/pkg/util/errors"
	randutil "k8s.io/apimachinery/pkg/util/rand"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/cache"
	toolswatch "k8s.io/client-go/tools/watch"
	"k8s.io/klog/v2"
	"k8s.io/utils/ptr"

	configv1 "github.com/openshift/api/config/v1"
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

	// Prune older pods and directories.
	if err := r.prunePods(ctx); err != nil {
		klog.Errorf("failed to prune pods: %v", err)
		return "", fmt.Errorf("failed to prune pods: %w", err)
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
		deadline        = ptr.To(int64(2 * 60))
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
			Privileged:             ptr.To(true),
			ReadOnlyRootFilesystem: ptr.To(false),
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

	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
			Labels: map[string]string{
				"k8s-app": "retrieve-openshift-release",
			},
		},
		Spec: corev1.PodSpec{
			ActiveDeadlineSeconds: deadline,
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
	}

	klog.Infof("Spawning Pod %s ...", name)
	if _, err := r.kubeClient.CoreV1().Pods(pod.Namespace).Create(ctx, pod, metav1.CreateOptions{}); err != nil {
		return err
	}

	return waitForPodCompletion(ctx, r.kubeClient.CoreV1().Pods(pod.Namespace), pod.Name)
}

type PodListerWatcher interface {
	List(ctx context.Context, opts metav1.ListOptions) (*corev1.PodList, error)
	Watch(ctx context.Context, opts metav1.ListOptions) (watch.Interface, error)
}

func collectStatuses(status corev1.PodStatus, waitingConditionFunc func(reason, message string) bool) []string {
	var statuses []string
	for _, cs := range status.ContainerStatuses {
		if cs.State.Waiting != nil && waitingConditionFunc(cs.State.Waiting.Reason, cs.State.Waiting.Message) {
			statuses = append(statuses, fmt.Sprintf("container %s is waiting with reason %q and message %q", cs.Name, cs.State.Waiting.Reason, cs.State.Waiting.Message))
		}
		if cs.State.Terminated != nil && cs.State.Terminated.Message != "" {
			statuses = append(statuses, fmt.Sprintf("container %s is terminated with reason %q and message %q", cs.Name, cs.State.Terminated.Reason, cs.State.Terminated.Message))
		}
	}
	for _, ics := range status.InitContainerStatuses {
		if ics.State.Waiting != nil && waitingConditionFunc(ics.State.Waiting.Reason, ics.State.Waiting.Message) {
			statuses = append(statuses, fmt.Sprintf("initcontainer %s is waiting with reason %q and message %q", ics.Name, ics.State.Waiting.Reason, ics.State.Waiting.Message))
		}
		if ics.State.Terminated != nil && ics.State.Terminated.Message != "" {
			statuses = append(statuses, fmt.Sprintf("initcontainer %s is terminated with reason %q and message %q", ics.Name, ics.State.Terminated.Reason, ics.State.Terminated.Message))
		}
	}
	return statuses
}

func podCompletionCheckFn(name string) toolswatch.ConditionFunc {
	return func(event watch.Event) (bool, error) {
		p, ok := event.Object.(*corev1.Pod)
		if !ok {
			klog.Errorf("expecting Pod but received event with kind: %s", event.Object.GetObjectKind())
			return false, fmt.Errorf("expecting Pod but received event with kind: %s", event.Object.GetObjectKind())
		}
		switch phase := p.Status.Phase; phase {
		case corev1.PodPending:
			klog.V(4).Infof("Pod %s is pending", name)
			// There are two cases at the moment we want to bottle up the waiting message where
			// the details would be lost if we waited until the pod failed.
			// Case 1: "reason: SignatureValidationFailed".
			// The message looks like 'image pull failed for quay.io/openshift-release-dev/ocp-release@sha256:digest because the signature validation failed: Source image rejected: A signature was required, but no signature exists'
			// We do not need Case 1 if https://github.com/kubernetes/kubernetes/pull/127918 lands into OCP.
			// Case 2: "reason: ErrImagePull".
			// The message looks like '...: reading manifest sha256:... in quay.io/openshift-release-dev/ocp-release: manifest unknown'
			// In case those keywords are changed in the future Kubernetes implementation, we will have to follow up accordingly.
			// Otherwise, we will lose these details in the waiting message. It brings no other harms.
			if statuses := collectStatuses(p.Status, func(reason, message string) bool {
				return reason == "SignatureValidationFailed" ||
					(reason == "ErrImagePull" && strings.Contains(message, "manifest unknown"))
			}); len(statuses) > 0 {
				klog.Errorf("Pod %s failed at pending with reason %q and message %q and status %s", name, p.Status.Reason, p.Status.Message, strings.Join(statuses, ","))
				return false, fmt.Errorf("pod %s failed at pending with reason %q and message %q and status %s", name, p.Status.Reason, p.Status.Message, strings.Join(statuses, ","))
			}
			return false, nil
		case corev1.PodRunning:
			klog.V(4).Infof("Pod %s is running, waiting for its completion ...", name)
			return false, nil
		case corev1.PodSucceeded:
			klog.Infof("Pod %s succeeded", name)
			return true, nil
		case corev1.PodFailed:
			statuses := collectStatuses(p.Status, func(reason, message string) bool { return message != "" })
			klog.Errorf("Pod %s failed with reason %q and message %q and status %s", name, p.Status.Reason, p.Status.Message, strings.Join(statuses, ","))
			return false, fmt.Errorf("pod %s failed with reason %q and message %q and status %s", name, p.Status.Reason, p.Status.Message, strings.Join(statuses, ","))
		default:
			klog.Errorf("Pod %s is with unexpected phase %s", name, phase)
			return false, fmt.Errorf("pod %s is with unexpected phase %s", name, phase)
		}
	}
}

func waitForPodCompletion(ctx context.Context, podListerWatcher PodListerWatcher, name string) error {
	fieldSelector := fields.OneTermEqualSelector("metadata.name", name).String()
	_, err := toolswatch.UntilWithSync(
		ctx,
		&cache.ListWatch{
			ListFunc: func(options metav1.ListOptions) (object runtime.Object, e error) {
				return podListerWatcher.List(ctx, metav1.ListOptions{FieldSelector: fieldSelector})
			},
			WatchFunc: func(options metav1.ListOptions) (watch.Interface, error) {
				return podListerWatcher.Watch(ctx, metav1.ListOptions{FieldSelector: fieldSelector})
			},
		},
		&corev1.Pod{},
		nil,
		podCompletionCheckFn(name),
	)
	return err
}

// prunePods deletes the older, finished pods in the namespace.
func (r *payloadRetriever) prunePods(ctx context.Context) error {
	var errs []error

	// begin transitional job pruning, in case any dangled from earlier versions
	jobs, err := r.kubeClient.BatchV1().Jobs(r.namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		errs = append(errs, err)
	}

	for _, job := range jobs.Items {
		if !strings.HasPrefix(job.Name, r.operatorName+"-") {
			// Ignore jobs not beginning with operatorName
			continue
		}
		err := r.kubeClient.BatchV1().Jobs(r.namespace).Delete(ctx, job.Name, metav1.DeleteOptions{})
		if err != nil {
			errs = append(errs, errors.Wrapf(err, "failed to delete job %v", job.Name))
		}
	}
	// end transitional job pruning

	pods, err := r.kubeClient.CoreV1().Pods(r.namespace).List(ctx, metav1.ListOptions{
		LabelSelector: "k8s-app=retrieve-openshift-release",
	})
	if err != nil {
		errs = append(errs, err)
	}

	for _, pod := range pods.Items {
		err := r.kubeClient.CoreV1().Pods(r.namespace).Delete(ctx, pod.Name, metav1.DeleteOptions{})
		if err != nil {
			errs = append(errs, errors.Wrapf(err, "failed to delete pod %v", pod.Name))
		}
	}

	agg := utilerrors.NewAggregate(errs)
	if agg != nil {
		return fmt.Errorf("error deleting pods: %v", agg.Error())
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

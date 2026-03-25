package util

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	g "github.com/onsi/ginkgo/v2"
	o "github.com/onsi/gomega"
	"github.com/pkg/errors"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/apimachinery/pkg/util/yaml"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/tools/remotecommand"

	configv1 "github.com/openshift/api/config/v1"
	clientconfigv1 "github.com/openshift/client-go/config/clientset/versioned"
	libmanifest "github.com/openshift/library-go/pkg/manifest"

	"github.com/openshift/cluster-version-operator/pkg/external"
)

// IsHypershift checks if running on a HyperShift hosted cluster
// Refer to https://github.com/openshift/origin/blob/31704414237b8bd5c66ad247c105c94abc9470b1/test/extended/util/framework.go#L2301
func IsHypershift(ctx context.Context, restConfig *rest.Config) (bool, error) {
	configClient, err := GetConfigClient(restConfig)
	if err != nil {
		return false, err
	}
	infrastructure, err := configClient.ConfigV1().Infrastructures().Get(ctx, "cluster", metav1.GetOptions{})
	if err != nil {
		// If the Infrastructure resource doesn't exist (e.g., in MicroShift),it's not a HyperShift
		if apierrors.IsNotFound(err) {
			return false, nil
		}
		return false, err
	}
	return infrastructure.Status.ControlPlaneTopology == configv1.ExternalTopologyMode, nil
}

// SkipIfHypershift skips the test if running on a HyperShift hosted cluster
func SkipIfHypershift(ctx context.Context, restConfig *rest.Config) error {
	isHypershift, err := IsHypershift(ctx, restConfig)
	if err != nil {
		return err
	}
	if isHypershift {
		g.Skip("Skipping test: running on HyperShift hosted cluster!")
	}
	return nil
}

// IsMicroshift checks if running on a MicroShift cluster
// Refer to https://github.com/openshift/origin/blob/31704414237b8bd5c66ad247c105c94abc9470b1/test/extended/util/framework.go#L2312
func IsMicroshift(ctx context.Context, restConfig *rest.Config) (bool, error) {
	kubeClient, err := GetKubeClient(restConfig)
	if err != nil {
		return false, err
	}
	var cm *corev1.ConfigMap
	duration := 5 * time.Minute
	if err := wait.PollUntilContextTimeout(ctx, 10*time.Second, duration, true, func(ctx context.Context) (bool, error) {
		// MicroShift cluster contains "microshift-version" configmap in "kube-public" namespace
		var err error
		cm, err = kubeClient.CoreV1().ConfigMaps("kube-public").Get(ctx, "microshift-version", metav1.GetOptions{})
		if err == nil {
			return true, nil
		}
		if apierrors.IsNotFound(err) {
			cm = nil
			return true, nil
		}
		g.GinkgoLogr.Info("error accessing microshift-version configmap", "error", err)
		return false, nil
	}); err != nil {
		g.GinkgoLogr.Info("failed to find microshift-version configmap while polling", "duration", duration, "error", err)
		return false, err
	}
	if cm == nil {
		g.GinkgoLogr.Info("microshift-version configmap not found")
		return false, nil
	}
	g.GinkgoLogr.Info("MicroShift cluster detected", "version", cm.Data["version"])
	return true, nil
}

// SkipIfMicroshift skips the test if running on a MicroShift cluster
func SkipIfMicroshift(ctx context.Context, restConfig *rest.Config) error {
	isMicroshift, err := IsMicroshift(ctx, restConfig)
	if err != nil {
		return err
	}
	if isMicroshift {
		g.Skip("Skipping test: running on MicroShift cluster!")
	}
	return nil
}

// GetAuthFile retrieves the auth file from the specified secret and writes it to a temporary file, returning the file path.
// e.g.: ns="openshift-config", secretName="pull-secret", key=".dockerconfigjson"
func GetAuthFile(ctx context.Context, client kubernetes.Interface, ns string, secretName string, key string) (filePath string, err error) {
	sec, err := client.CoreV1().Secrets(ns).Get(ctx, secretName, metav1.GetOptions{})
	if err != nil {
		return "", err
	}
	secretData, ok := sec.Data[key]
	if !ok {
		return "", fmt.Errorf("auth key not found in secret %s/%s", ns, secretName)
	}
	f, err := os.CreateTemp("", "ota-*")
	if err != nil {
		return "", fmt.Errorf("error creating temp file: %v", err)
	}
	defer func() {
		_ = f.Close()
	}()
	if _, err := f.Write(secretData); err != nil {
		_ = os.Remove(f.Name())
		return "", fmt.Errorf("error writing to temp file: %v", err)
	}
	return f.Name(), nil
}

// GetPodsByNamespace retrieves the list of pods in the specified namespace with the given label selector.
func GetPodsByNamespace(ctx context.Context, client kubernetes.Interface, namespace string, labelSelector map[string]string) ([]corev1.Pod, error) {
	podList, err := client.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{
		LabelSelector: labels.FormatLabels(labelSelector),
	})
	if err != nil {
		return nil, fmt.Errorf("error listing pods in namespace %s with label selector %v: %v", namespace, labelSelector, err)
	}
	return podList.Items, nil
}

// ListFilesInPodContainer executes the given command in the specified container of a pod and returns the output as a list of strings.
func ListFilesInPodContainer(ctx context.Context, restConfig *rest.Config, command []string, namespace string, podName string, containerName string) ([]string, error) {
	kubeClient, err := GetKubeClient(restConfig)
	if err != nil {
		return nil, err
	}
	results := []string{}
	req := kubeClient.CoreV1().RESTClient().Post().
		Resource("pods").
		Name(podName).
		Namespace(namespace).
		SubResource("exec").
		VersionedParams(&corev1.PodExecOptions{
			Command:   command,
			Container: containerName,
			Stdout:    true,
			Stderr:    true,
		}, scheme.ParameterCodec)
	exec, err := remotecommand.NewSPDYExecutor(restConfig, "POST", req.URL())
	if err != nil {
		return nil, fmt.Errorf("error creating executor for pod %s/%s: %v", namespace, podName, err)
	}
	var stdoutBuf, stderrBuf bytes.Buffer
	err = exec.StreamWithContext(ctx, remotecommand.StreamOptions{
		Stdout: &stdoutBuf,
		Stderr: &stderrBuf,
	})
	if err != nil {
		return nil, fmt.Errorf("error executing command in pod %s/%s: %v, stderr: %s", namespace, podName, err, stderrBuf.String())
	}
	files := strings.Split(stdoutBuf.String(), "\n")
	for _, file := range files {
		if file == "" {
			continue
		}
		results = append(results, file)
	}
	return results, nil
}

// GetFileContentInPodContainer executes the command to read the content of a file in the specified container of a pod and returns the content as a string.
func GetFileContentInPodContainer(ctx context.Context, restConfig *rest.Config, namespace string, podName string, containerName string, filePath string) (string, error) {
	kubeClient, err := GetKubeClient(restConfig)
	if err != nil {
		return "", err
	}
	command := []string{"cat", filePath}
	req := kubeClient.CoreV1().RESTClient().Post().
		Resource("pods").
		Name(podName).
		Namespace(namespace).
		SubResource("exec").
		VersionedParams(&corev1.PodExecOptions{
			Command:   command,
			Container: containerName,
			Stdout:    true,
			Stderr:    true,
		}, scheme.ParameterCodec)
	exec, err := remotecommand.NewSPDYExecutor(restConfig, "POST", req.URL())
	if err != nil {
		return "", fmt.Errorf("error creating executor for pod %s/%s: %v", namespace, podName, err)
	}
	var stdoutBuf, stderrBuf bytes.Buffer
	err = exec.StreamWithContext(ctx, remotecommand.StreamOptions{
		Stdout: &stdoutBuf,
		Stderr: &stderrBuf,
	})
	if err != nil {
		return "", fmt.Errorf("error executing command in pod %s/%s: %v, stderr: %s", namespace, podName, err, stderrBuf.String())
	}
	return stdoutBuf.String(), nil
}

// ParseManifest parses the given file content as a Kubernetes manifest and returns a Manifest object.
func ParseManifest(fileContent string) (libmanifest.Manifest, error) {
	d := yaml.NewYAMLOrJSONDecoder(strings.NewReader(fileContent), 1024)
	for {
		m := libmanifest.Manifest{}
		if err := d.Decode(&m); err != nil {
			if err == io.EOF {
				return m, nil
			}
			return m, errors.Wrapf(err, "error parsing")
		}
		m.Raw = bytes.TrimSpace(m.Raw)
		if len(m.Raw) == 0 || bytes.Equal(m.Raw, []byte("null")) {
			continue
		}
		return m, nil
	}
}

// GetRestConfig loads the Kubernetes REST configuration from KUBECONFIG environment variable.
func GetRestConfig() (*rest.Config, error) {
	return clientcmd.NewNonInteractiveDeferredLoadingClientConfig(clientcmd.NewDefaultClientConfigLoadingRules(), &clientcmd.ConfigOverrides{}).ClientConfig()
}

// GetKubeClient creates a Kubernetes client from the given REST config.
func GetKubeClient(restConfig *rest.Config) (kubernetes.Interface, error) {
	return kubernetes.NewForConfig(restConfig)
}

// GetConfigClient creates an OpenShift config client from the given REST config.
func GetConfigClient(restConfig *rest.Config) (clientconfigv1.Interface, error) {
	return clientconfigv1.NewForConfig(restConfig)
}

// IsTechPreviewNoUpgrade checks if a cluster is a TechPreviewNoUpgrade cluster
func IsTechPreviewNoUpgrade(ctx context.Context, restConfig *rest.Config) bool {
	configClient, err := GetConfigClient(restConfig)
	o.Expect(err).NotTo(o.HaveOccurred())
	featureGate, err := configClient.ConfigV1().FeatureGates().Get(ctx, "cluster", metav1.GetOptions{})
	if err != nil {
		if apierrors.IsNotFound(err) {
			return false
		}
		o.Expect(err).NotTo(o.HaveOccurred(), "could not retrieve feature-gate: %v", err)
	}
	return featureGate.Spec.FeatureSet == configv1.TechPreviewNoUpgrade
}

// SkipIfNotTechPreviewNoUpgrade skips the test if a cluster is not a TechPreviewNoUpgrade cluster
func SkipIfNotTechPreviewNoUpgrade(ctx context.Context, restConfig *rest.Config) {
	if !IsTechPreviewNoUpgrade(ctx, restConfig) {
		g.Skip("This test is skipped because the Tech Preview NoUpgrade is not enabled")
	}
}

const (
	// fauxinnati mocks Cincinnati Update Graph Server for OpenShift
	FauxinnatiAPIURL = "https://fauxinnati-fauxinnati.apps.ota-stage.q2z4.p1.openshiftapps.com/api/upgrades_info/graph"
)

func accessible(ctx context.Context, restConfig *rest.Config, urls ...string) (bool, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	kubeClient, err := GetKubeClient(restConfig)
	if err != nil {
		return false, err
	}

	pods, err := kubeClient.CoreV1().Pods(external.DefaultCVONamespace).
		List(ctx, metav1.ListOptions{LabelSelector: labels.FormatLabels(map[string]string{"k8s-app": "cluster-version-operator"})})
	if err != nil || len(pods.Items) == 0 {
		return false, fmt.Errorf("could not find CVO pod: %w", err)
	}
	podName := pods.Items[0].Name
	command := []string{"curl", "-sI", "--max-time", "10"}
	command = append(command, urls...)

	req := kubeClient.CoreV1().RESTClient().Post().
		Resource("pods").
		Name(podName).
		Namespace(external.DefaultCVONamespace).
		SubResource("exec").
		VersionedParams(&corev1.PodExecOptions{
			Command: command,
			Stdout:  true,
			Stderr:  true,
		}, scheme.ParameterCodec)
	exec, err := remotecommand.NewSPDYExecutor(restConfig, "POST", req.URL())

	if err != nil {
		return false, err
	}
	stdoutBuf := &bytes.Buffer{}
	stderrBuf := &bytes.Buffer{}

	err = exec.StreamWithContext(ctx, remotecommand.StreamOptions{
		Stdout: stdoutBuf,
		Stderr: stderrBuf,
	})
	if err != nil {
		if strings.Contains(err.Error(), "command terminated with exit code") {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

// NetworkRestricted returns true if there is a given URL
// that is not accessible. Otherwise, false.
func NetworkRestricted(ctx context.Context, restConfig *rest.Config, urls ...string) (bool, error) {
	ok, err := accessible(ctx, restConfig, urls...)
	if err != nil {
		return false, err
	}
	return !ok, nil
}

func SkipIfNetworkRestricted(ctx context.Context, restConfig *rest.Config, urls ...string) error {
	ok, err := NetworkRestricted(ctx, restConfig, urls...)
	if err != nil {
		return err
	}
	if ok {
		g.Skip("This test is skipped because the network is restricted")
	}
	return nil
}

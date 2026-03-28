package util

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"
	"text/template"
	"time"

	g "github.com/onsi/ginkgo/v2"
	o "github.com/onsi/gomega"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/tools/remotecommand"

	configv1 "github.com/openshift/api/config/v1"
	clientconfigv1 "github.com/openshift/client-go/config/clientset/versioned"

	"github.com/openshift/cluster-version-operator/pkg/external"
)

// Define test waiting time const
const (
	defaultMaxWaitingTime = 200 * time.Second
	defaultPollingTime    = 2 * time.Second
)

type Node struct {
	Version string
	Payload string
	Channel string
}
type Edge struct {
	From uint64
	To   uint64
}
type StringEdge struct {
	From string
	To   string
}
type ConditionalEdge struct {
	Edge  StringEdge
	Risks []Risk
}
type Risk struct {
	Url     string
	Name    string
	Message string
	Rule    interface{}
}

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

// WaitForDeploymentReady waits for the deployment become ready
func WaitForDeploymentReady(c kubernetes.Interface, deployName, namespace string, revision int64) error {
	return WaitForDeploymentReadyWithTimeout(c, deployName, namespace, revision, defaultMaxWaitingTime)
}

// WaitForDeploymentReadyWithTimeout waits for the deployment become ready with defined timeout
func WaitForDeploymentReadyWithTimeout(c kubernetes.Interface, deployName, namespace string, revision int64, timeout time.Duration) error {
	var (
		deployment *appsv1.Deployment
		getErr     error
	)
	pollErr := wait.PollUntilContextTimeout(context.Background(), defaultPollingTime, timeout, true, func(context.Context) (isReady bool, err error) {
		deployment, getErr = c.AppsV1().Deployments(namespace).Get(context.Background(), deployName, metav1.GetOptions{})
		if getErr != nil {
			return false, nil
		}
		if deployment.Status.AvailableReplicas == *deployment.Spec.Replicas {
			return true, nil
		}
		if revision >= 0 && deployment.Status.ObservedGeneration != revision {
			return false, nil
		}
		return false, nil
	})

	return pollErr
}

// CreateGraphTemplate returns a graph template string which can be patch as upstream
// for example:
//
//	nodes := []util.Node{
//		{Version: "4.22.0-0.nightly-2026-03-17-033403", Payload: "4.22.0-0.nightly-2023-0", Channel: "stable-4.22"},
//		{Version: "4.22.0-ec.2", Payload: "quay.io/openshift-release-dev/ocp-release@sha256:fc88d0bf145c81989a4116bbb0e3d2724d9ab937efb7d217a10e7d7ff3031c50", Channel: "stable-4.22"},
//		{Version: "4.22.0-ec.3", Payload: "quay.io/openshift-release-dev/ocp-release@sha256:58b98da1492b3f4af6129c4684b8e8cde4f2dc197e4b483bb6025971d59f92a5", Channel: "stable-4.22"},
//	}
//
//	edges := []util.Edge{
//		{From: 0, To: 1},
//	}
//
//	conditionalEdges := []util.ConditionalEdge{
//		{
//			Edge: util.StringEdge{
//				From: versionString,
//				To:   "",
//			},
//			Risks: []util.Risk{
//				{
//					Url:     "https://bugzilla.redhat.com/show_bug.cgi?id=123456",
//					Name:    "Bug 123456",
//					Message: "Empty target node",
//					Rule:    map[string]interface{}{"type": "Always"},
//				},
//			},
//		},
//	}
//
// buf, err := util.CreateGraphTemplate(nodes, edges, conditionalEdges)
func CreateGraphTemplate(
	graphNodes []Node,
	graphEdges []Edge,
	graphConditionalEdges []ConditionalEdge) (*strings.Builder, error) {

	t := template.Must(template.New("text").Funcs(template.FuncMap{
		"marshal": func(v interface{}) (string, error) {
			var ruleStr string
			switch v := v.(type) {
			case string:
				if !json.Valid([]byte(v)) {
					promqlRule := map[string]interface{}{
						"type": "PromQL",
						"promql": map[string]string{
							"promql": v,
						},
					}
					bytes, err := json.Marshal(promqlRule)
					if err != nil {
						return "", fmt.Errorf("failed to marshal promql rule: %w", err)
					}
					ruleStr = string(bytes)
				} else {
					ruleStr = v
				}
			default:
				bytes, err := json.Marshal(v)
				if err != nil {
					return "", fmt.Errorf("failed to marshal rule: %w", err)
				}
				ruleStr = string(bytes)
			}
			return ruleStr, nil
		},
	}).Parse(strings.TrimSpace(`
{
	"nodes": [{{range $index, $node := .Nodes}}{{if $index}},{{end}}
		{"version": "{{$node.Version}}", "payload": "{{$node.Payload}}", "metadata": {"io.openshift.upgrades.graph.release.channels": "{{$node.Channel}}"}}{{end}}
	],
	"edges": [{{range $index, $edge := .Edges}}{{if $index}},{{end}}
		[{{$edge.From}}, {{$edge.To}}]{{end}}
	],
	"conditionalEdges": [{{range $outerindex, $ce := .ConditionalEdges}}{{if $outerindex}},{{end}}
		{
			"edges": [{"from": "{{$ce.Edge.From}}", "to": "{{$ce.Edge.To}}"}],
			"risks": [{{range $index, $risk := $ce.Risks}}{{if $index}},{{end}}
				{
					"url": "{{$risk.Url}}",
					"name": "{{$risk.Name}}",
					"message": "{{$risk.Message}}",
					"matchingRules": [{{marshal $risk.Rule}}]
				}{{end}}
			]
		}{{end}}
	]
}
`)))
	var buf strings.Builder
	err := t.Execute(&buf, struct {
		Nodes            []Node
		Edges            []Edge
		ConditionalEdges []ConditionalEdge
	}{Nodes: graphNodes, Edges: graphEdges, ConditionalEdges: graphConditionalEdges})

	if err != nil {
		return nil, err
	}

	return &buf, nil
}

func RunUpdateService(ctx context.Context, c kubernetes.Interface, namespace string, graph string, label map[string]string) (*appsv1.Deployment, error) {
	deployment, err := c.AppsV1().Deployments(namespace).Create(ctx,
		&appsv1.Deployment{
			ObjectMeta: metav1.ObjectMeta{
				GenerateName: "test-update-service-",
				Labels:       label,
			},
			Spec: appsv1.DeploymentSpec{
				Selector: &metav1.LabelSelector{
					MatchLabels: label,
				},
				Template: corev1.PodTemplateSpec{
					ObjectMeta: metav1.ObjectMeta{
						Labels: label,
					},
					Spec: corev1.PodSpec{
						Containers: []corev1.Container{{
							Name:  "update-service",
							Image: "image-registry.openshift-image-registry.svc:5000/openshift/tools:latest",
							Args: []string{
								"/bin/sh",
								"-c",
								fmt.Sprintf(`DIR="$(mktemp -d)" &&
cd "${DIR}" &&
printf '%%s' '%s' >graph &&
python3 -m http.server 8000
`, graph),
							},
							Ports: []corev1.ContainerPort{{
								Name:          "update-service",
								ContainerPort: 8000,
							}},
							Resources: corev1.ResourceRequirements{
								Requests: corev1.ResourceList{
									corev1.ResourceCPU:    resource.MustParse("10m"),
									corev1.ResourceMemory: resource.MustParse("20Mi"),
								},
							},
						}},
					},
				},
			},
		}, metav1.CreateOptions{})
	if err != nil {
		return nil, err
	}

	if err = WaitForDeploymentReady(c, deployment.Name, namespace, -1); err != nil {
		return nil, err
	}
	return deployment, nil
}

func CreateService(ctx context.Context, c kubernetes.Interface, namespace string, deployment *appsv1.Deployment, port int32) (*corev1.Service, *url.URL, error) {
	service, err := c.CoreV1().Services(namespace).Create(ctx,
		&corev1.Service{
			ObjectMeta: metav1.ObjectMeta{
				Name: deployment.Name,
			},
			Spec: corev1.ServiceSpec{
				Selector: deployment.Spec.Template.Labels,
				Ports: []corev1.ServicePort{{
					Name:       deployment.Spec.Template.Spec.Containers[0].Ports[0].Name,
					Protocol:   corev1.ProtocolTCP,
					Port:       port,
					TargetPort: intstr.IntOrString{IntVal: deployment.Spec.Template.Spec.Containers[0].Ports[0].ContainerPort},
				}},
				Type:            corev1.ServiceTypeClusterIP,
				SessionAffinity: corev1.ServiceAffinityNone,
			},
		}, metav1.CreateOptions{})
	if err != nil {
		return nil, nil, err
	}

	// wait for ClusterIP to be assigned
	var createdSvc *corev1.Service
	if err := wait.PollUntilContextTimeout(context.Background(), 1*time.Second, 10*time.Second, true, func(ctx context.Context) (bool, error) {
		svc, err := c.CoreV1().Services(namespace).Get(ctx, service.Name, metav1.GetOptions{})
		if err != nil {
			return false, err
		}
		if svc.Spec.ClusterIP != "" && svc.Spec.ClusterIP != "None" {
			createdSvc = svc
			return true, nil
		}
		return false, nil
	}); err != nil {
		return nil, nil, fmt.Errorf("timed out waiting for ClusterIP for service %s/%s: %w", namespace, service.Name, err)
	}

	return createdSvc, &url.URL{
		Scheme: "http",
		Host:   net.JoinHostPort(createdSvc.Spec.ClusterIP, strconv.Itoa(int(createdSvc.Spec.Ports[0].Port))),
		Path:   "graph",
	}, nil
}

func CreateNetworkPolicy(ctx context.Context, c kubernetes.Interface, namespace string, policyName string, MatchLabels map[string]string) (*networkingv1.NetworkPolicy, error) {
	policy, err := c.NetworkingV1().NetworkPolicies(namespace).Create(ctx,
		&networkingv1.NetworkPolicy{
			ObjectMeta: metav1.ObjectMeta{
				Name: policyName,
			},
			Spec: networkingv1.NetworkPolicySpec{
				PodSelector: metav1.LabelSelector{
					MatchLabels: MatchLabels,
				},
				PolicyTypes: []networkingv1.PolicyType{networkingv1.PolicyTypeIngress},
				Ingress:     []networkingv1.NetworkPolicyIngressRule{{}},
			},
		}, metav1.CreateOptions{})
	if err != nil {
		return nil, err
	}

	return policy, nil
}

func DeleteNetworkPolicy(ctx context.Context, c kubernetes.Interface, namespace string, policyName string) error {
	err := c.NetworkingV1().NetworkPolicies(namespace).Delete(ctx, policyName, metav1.DeleteOptions{})
	return err
}

func CleanupService(ctx context.Context, c kubernetes.Interface, namespace string, serviceName string) error {
	err := c.CoreV1().Services(namespace).Delete(ctx, serviceName, metav1.DeleteOptions{})
	return err
}

func DeleteDeployment(ctx context.Context, c kubernetes.Interface, namespace string, deploymentName string) error {
	err := c.AppsV1().Deployments(namespace).Delete(ctx, deploymentName, metav1.DeleteOptions{})
	return err
}

func PatchUpstream(ctx context.Context, c *clientconfigv1.Clientset, url string, channel string) (*configv1.ClusterVersion, error) {
	obj, err := c.ConfigV1().ClusterVersions().Get(ctx, "version", metav1.GetOptions{})

	if err != nil {
		return nil, err
	}

	if obj == nil {
		return nil, fmt.Errorf("ClusterVersion object not found")
	}

	exists := obj.DeepCopy()
	exists.Spec.Upstream = configv1.URL(url)
	exists.Spec.Channel = channel

	cv, err := c.ConfigV1().ClusterVersions().Update(ctx, exists, metav1.UpdateOptions{})

	return cv, err
}

package util

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/url"
	"strconv"
	"time"

	"github.com/blang/semver/v4"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/kubernetes"

	configv1 "github.com/openshift/api/config/v1"

	"github.com/openshift/cluster-version-operator/pkg/cincinnati"
)

// toolsImage is the in-cluster OpenShift tools image used for generic shell
// workloads (same pullspec origin's ShellImage() resolves to).
const toolsImage = "image-registry.openshift-image-registry.svc:5000/openshift/tools:latest"

// GenerateGraph builds a Cincinnati update graph for the given channel, using
// current as the cluster's current release. Channel selects the topology
// (e.g. "risks-always"). Release URL and multi-arch metadata are populated to
// match Cincinnati node metadata conventions.
func GenerateGraph(current configv1.Release, channel string) ([]byte, error) {
	currentVersion, err := semver.Parse(current.Version)
	if err != nil {
		return nil, fmt.Errorf("parse current version %q: %w", current.Version, err)
	}

	var graph cincinnati.Graph
	switch channel {
	case "risks-always":
		graph = generateRisksAlwaysGraph(current, currentVersion, channel)
	default:
		return nil, fmt.Errorf("unsupported channel %q", channel)
	}

	return json.Marshal(graph)
}

// generateRisksAlwaysGraph builds a three-node graph with the current version
// plus a patch-bump and a minor-bump target, connected only by conditional
// edges whose risks always apply.
func generateRisksAlwaysGraph(current configv1.Release, currentVersion semver.Version, channel string) cincinnati.Graph {
	versionB := currentVersion
	versionB.Patch++
	versionB.Pre = nil

	versionC := currentVersion
	versionC.Minor++
	versionC.Patch = 0
	versionC.Pre = nil

	currentURL := string(current.URL)
	if currentURL == "" {
		currentURL = generateReleaseURL(currentVersion)
	}

	return cincinnati.Graph{
		Nodes: []cincinnati.Node{
			{
				Version:  currentVersion,
				Image:    current.Image,
				Metadata: nodeMetadata(channel, currentURL, current.Architecture),
			},
			{
				Version:  versionB,
				Image:    "example.com/test@sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
				Metadata: nodeMetadata(channel, generateReleaseURL(versionB), current.Architecture),
			},
			{
				Version:  versionC,
				Image:    "example.com/test@sha256:cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc",
				Metadata: nodeMetadata(channel, generateReleaseURL(versionC), current.Architecture),
			},
		},
		Edges: []cincinnati.Edge{},
		ConditionalEdges: []cincinnati.ConditionalEdges{
			{
				Edges: []cincinnati.ConditionalEdge{{From: currentVersion.String(), To: versionB.String()}},
				Risks: []configv1.ConditionalUpdateRisk{
					{
						URL:     "https://docs.openshift.com/synthetic-risk-a",
						Name:    "SyntheticRiskA",
						Message: "This is a synthetic risk A that always applies for testing purposes",
						MatchingRules: []configv1.ClusterCondition{
							{Type: "Always"},
						},
					},
					{
						URL:     "https://docs.openshift.com/synthetic-risk-b",
						Name:    "SyntheticRiskB",
						Message: "This is a synthetic risk B that always applies for testing purposes",
						MatchingRules: []configv1.ClusterCondition{
							{Type: "Always"},
						},
					},
				},
			},
			{
				Edges: []cincinnati.ConditionalEdge{{From: currentVersion.String(), To: versionC.String()}},
				Risks: []configv1.ConditionalUpdateRisk{
					{
						URL:     "https://docs.openshift.com/synthetic-risk-a",
						Name:    "SyntheticRiskA",
						Message: "This is a synthetic risk A that always applies for testing purposes",
						MatchingRules: []configv1.ClusterCondition{
							{Type: "Always"},
						},
					},
					{
						URL:     "https://docs.openshift.com/synthetic-risk-c",
						Name:    "SyntheticRiskC",
						Message: "This is a synthetic risk C that always applies for testing purposes",
						MatchingRules: []configv1.ClusterCondition{
							{Type: "Always"},
						},
					},
				},
			},
		},
	}
}

// nodeMetadata builds Cincinnati node metadata. Architecture is only set for
// multi-arch releases, matching Cincinnati's release.openshift.io/architecture.
func nodeMetadata(channel, releaseURL string, architecture configv1.ClusterVersionArchitecture) map[string]interface{} {
	metadata := map[string]interface{}{
		"io.openshift.upgrades.graph.release.channels": channel,
		"url": releaseURL,
	}
	if architecture == configv1.ClusterVersionArchitectureMulti {
		metadata["release.openshift.io/architecture"] = "multi"
	}
	return metadata
}

// generateReleaseURL creates a deterministic release URL for a synthetic graph node.
func generateReleaseURL(version semver.Version) string {
	return fmt.Sprintf("https://access.redhat.com/errata/RHSA-2024:%05d", version.Major*1000+version.Minor*100+version.Patch)
}

// RunUpdateService deploys an in-cluster static Cincinnati update service that
// serves graphJSON at /graph, following the pattern used by origin's
// runUpdateService:
// https://github.com/openshift/origin/blob/87d5d4fbdda3e68d57aaf5428dbda9f8d11cd52f/test/extended/cli/adm_upgrade/recommend.go#L317
//
// The caller owns namespace lifecycle (create before, delete after).
func RunUpdateService(ctx context.Context, kubeClient kubernetes.Interface, namespace, graphJSON string) (*url.URL, error) {
	const (
		appLabel = "test-update-service"
		port     = int32(8000)
	)

	replicas := int32(1)
	deployment, err := kubeClient.AppsV1().Deployments(namespace).Create(ctx, &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			GenerateName: "test-update-service-",
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: &replicas,
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{
					"app": appLabel,
				},
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						"app": appLabel,
					},
				},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{{
						Name:  "update-service",
						Image: toolsImage,
						Env: []corev1.EnvVar{{
							Name:  "GRAPH_JSON",
							Value: graphJSON,
						}},
						Args: []string{
							"/bin/sh",
							"-c",
							`DIR="$(mktemp -d)" &&
cd "${DIR}" &&
printf '%s' "${GRAPH_JSON}" >graph &&
python3 -m http.server --bind ::
`,
						},
						Ports: []corev1.ContainerPort{{
							Name:          "update-service",
							ContainerPort: port,
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
		return nil, fmt.Errorf("create update service deployment: %w", err)
	}

	service, err := kubeClient.CoreV1().Services(namespace).Create(ctx, &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name: deployment.Name,
		},
		Spec: corev1.ServiceSpec{
			Selector: deployment.Spec.Template.Labels,
			Ports: []corev1.ServicePort{{
				Name:       deployment.Spec.Template.Spec.Containers[0].Ports[0].Name,
				Port:       port,
				TargetPort: intstr.FromInt32(port),
			}},
		},
	}, metav1.CreateOptions{})
	if err != nil {
		return nil, fmt.Errorf("create update service: %w", err)
	}

	if err := waitForDeploymentAvailable(ctx, kubeClient, namespace, deployment.Name, 2*time.Minute); err != nil {
		return nil, err
	}

	return &url.URL{
		Scheme: "http",
		Host:   net.JoinHostPort(service.Spec.ClusterIP, strconv.Itoa(int(port))),
		Path:   "graph",
	}, nil
}

func waitForDeploymentAvailable(ctx context.Context, kubeClient kubernetes.Interface, namespace, name string, timeout time.Duration) error {
	return wait.PollUntilContextTimeout(ctx, 2*time.Second, timeout, true, func(ctx context.Context) (bool, error) {
		deployment, err := kubeClient.AppsV1().Deployments(namespace).Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			return false, nil
		}
		if deployment.Spec.Replicas == nil {
			return false, nil
		}
		return deployment.Status.AvailableReplicas == *deployment.Spec.Replicas && *deployment.Spec.Replicas > 0, nil
	})
}

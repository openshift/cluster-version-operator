package util

import (
	"context"
	"fmt"
	"strings"

	g "github.com/onsi/ginkgo/v2"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	corev1client "k8s.io/client-go/kubernetes/typed/core/v1"
)

// SkipOnOpenShiftNess skips the test if the cluster type doesn't match the expected type.
func SkipOnOpenShiftNess(expectOpenShift bool) {
	switch IsKubernetesClusterFlag {
	case "yes":
		if expectOpenShift {
			g.Skip("Expecting OpenShift but the active cluster is not, skipping the test")
		}
	// Treat both "no" and "unknown" as OpenShift
	default:
		if !expectOpenShift {
			g.Skip("Expecting non-OpenShift but the active cluster is OpenShift, skipping the test")
		}
	}
}

// IsOpenShiftCluster checks if the active cluster is OpenShift or a derivative
func IsOpenShiftCluster(ctx context.Context, c corev1client.NamespaceInterface) (bool, error) {
	switch _, err := c.Get(ctx, "openshift-controller-manager", metav1.GetOptions{}); {
	case err == nil:
		return true, nil
	case apierrors.IsNotFound(err):
		return false, nil
	default:
		return false, fmt.Errorf("unable to determine if we are running against an OpenShift cluster: %v", err)
	}
}

// CheckPlatform check the cluster's platform
func CheckPlatform(oc *CLI) string {
	output, _ := oc.AsAdmin().WithoutNamespace().Run("get").Args("infrastructure", "cluster", "-o=jsonpath={.status.platformStatus.type}").Output()
	return strings.ToLower(output)
}

// GetReleaseImage returns the release image as string value (Ex: registry.ci.openshift.org/ocp/release@sha256:b13971e61312f5dddd6435ccf061ac1a8447285a85828456edcd4fc2504cfb8f)
func GetReleaseImage(oc *CLI) (string, error) {
	releaseImage, err := oc.AsAdmin().WithoutNamespace().Run("get").Args("clusterversion", "-o", "jsonpath={..desired.image}").Output()
	if err != nil {
		return "", err
	}
	return releaseImage, nil
}

// GetClusterVersion returns the cluster version as string value (Ex: 4.8) and cluster build (Ex: 4.8.0-0.nightly-2021-09-28-165247)
func GetClusterVersion(oc *CLI) (string, string, error) {
	clusterBuild, err := oc.AsAdmin().WithoutNamespace().Run("get").Args("clusterversion", "-o", "jsonpath={..desired.version}").Output()
	if err != nil {
		return "", "", err
	}
	splitValues := strings.Split(clusterBuild, ".")
	clusterVersion := splitValues[0] + "." + splitValues[1]
	return clusterVersion, clusterBuild, err
}

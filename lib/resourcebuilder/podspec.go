package resourcebuilder

import (
	"fmt"

	corev1 "k8s.io/api/core/v1"
)

// updatePodSpecWithProxy mutates the input podspec with proxy env vars for all init containers and containers
// matching the container names.
func updatePodSpecWithProxy(podSpec *corev1.PodSpec, containerNames []string, httpProxy, httpsProxy, noProxy string) error {
	hasProxy := len(httpsProxy) > 0 || len(httpProxy) > 0 || len(noProxy) > 0
	if !hasProxy {
		return nil
	}

	for _, containerName := range containerNames {
		found := false
		for i := range podSpec.Containers {
			if podSpec.Containers[i].Name != containerName {
				continue
			}
			found = true

			podSpec.Containers[i].Env = append(podSpec.Containers[i].Env, corev1.EnvVar{Name: "HTTP_PROXY", Value: httpProxy})
			podSpec.Containers[i].Env = append(podSpec.Containers[i].Env, corev1.EnvVar{Name: "HTTPS_PROXY", Value: httpsProxy})
			podSpec.Containers[i].Env = append(podSpec.Containers[i].Env, corev1.EnvVar{Name: "NO_PROXY", Value: noProxy})
		}
		for i := range podSpec.InitContainers {
			if podSpec.InitContainers[i].Name != containerName {
				continue
			}
			found = true

			podSpec.InitContainers[i].Env = append(podSpec.InitContainers[i].Env, corev1.EnvVar{Name: "HTTP_PROXY", Value: httpProxy})
			podSpec.InitContainers[i].Env = append(podSpec.InitContainers[i].Env, corev1.EnvVar{Name: "HTTPS_PROXY", Value: httpsProxy})
			podSpec.InitContainers[i].Env = append(podSpec.InitContainers[i].Env, corev1.EnvVar{Name: "NO_PROXY", Value: noProxy})
		}

		if !found {
			return fmt.Errorf("requested injection for non-existent container: %q", containerName)
		}
	}

	return nil

}

// updatePodSpecWithInternalLoadBalancerKubeService mutates the input podspec by setting the KUBERNETES_SERVICE_HOST to the internal
// loadbalancer endpoint and the KUBERNETES_SERVICE_PORT to the specified port
func updatePodSpecWithInternalLoadBalancerKubeService(podSpec *corev1.PodSpec, containerNames []string, internalLoadBalancerHost, internalLoadBalancerPort string) error {
	hasInternalLoadBalancer := len(internalLoadBalancerHost) > 0
	if !hasInternalLoadBalancer {
		return nil
	}

	for _, containerName := range containerNames {
		found := false
		for i := range podSpec.Containers {
			if podSpec.Containers[i].Name != containerName {
				continue
			}
			found = true

			podSpec.Containers[i].Env = setKubeServiceValue(podSpec.Containers[i].Env, internalLoadBalancerHost, internalLoadBalancerPort)
		}
		for i := range podSpec.InitContainers {
			if podSpec.InitContainers[i].Name != containerName {
				continue
			}
			found = true

			podSpec.InitContainers[i].Env = setKubeServiceValue(podSpec.Containers[i].Env, internalLoadBalancerHost, internalLoadBalancerPort)
		}

		if !found {
			return fmt.Errorf("requested injection for non-existent container: %q", containerName)
		}
	}

	return nil
}

// setKubeServiceValue replaces values if they are present and adds them if they are not
func setKubeServiceValue(in []corev1.EnvVar, internalLoadBalancerHost, internalLoadBalancerPort string) []corev1.EnvVar {
	ret := []corev1.EnvVar{}

	portVal := "443"
	if len(internalLoadBalancerPort) != 0 {
		portVal = internalLoadBalancerPort
	}

	foundPort := false
	foundHost := false
	for j := range in {
		ret = append(ret, *in[j].DeepCopy())
		if ret[j].Name == "KUBERNETES_SERVICE_PORT" {
			foundPort = true
			ret[j].Value = portVal
		}
		if ret[j].Name == "KUBERNETES_SERVICE_HOST" {
			foundHost = true
			ret[j].Value = internalLoadBalancerHost
		}
	}

	if !foundPort {
		ret = append(ret, corev1.EnvVar{
			Name:  "KUBERNETES_SERVICE_PORT",
			Value: portVal,
		})
	}

	if !foundHost {
		ret = append(ret, corev1.EnvVar{
			Name:  "KUBERNETES_SERVICE_HOST",
			Value: internalLoadBalancerHost,
		})
	}

	return ret
}

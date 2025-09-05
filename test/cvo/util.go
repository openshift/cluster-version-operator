package cvo

import (
	"context"
	"encoding/json"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"strings"
	"time"

	o "github.com/onsi/gomega"
	exutil "github.com/openshift/cluster-version-operator/test/util"
	"k8s.io/apimachinery/pkg/util/wait"
	e2e "k8s.io/kubernetes/test/e2e/framework"
)

// JSONp defines a json struct
type JSONp struct {
	Oper string      `json:"op"`
	Path string      `json:"path"`
	Valu interface{} `json:"value,omitempty"`
}

func getRandomString() string {
	chars := "abcdefghijklmnopqrstuvwxyz0123456789"
	seed := rand.New(rand.NewSource(time.Now().UnixNano()))
	buffer := make([]byte, 8)
	for index := range buffer {
		buffer[index] = chars[seed.Intn(len(chars))]
	}
	return string(buffer)
}

// Run "oc adm release extract" cmd to extract manifests from current live cluster
func extractManifest(oc *exutil.CLI) (string, error) {
	tempDataDir, err := os.MkdirTemp("", "cvo-test-")
	if err != nil {
		return "", err
	}

	if err = oc.AsAdmin().Run("extract").Args("secret/pull-secret", "-n", "openshift-config", "--confirm", "--to="+tempDataDir).Execute(); err != nil {
		err = fmt.Errorf("failed to extract dockerconfig: %v", err)
		return "", err
	}

	manifestDir := filepath.Join(tempDataDir, "manifest")
	if err = oc.AsAdmin().Run("adm").Args("release", "extract", "--to", manifestDir, "-a", tempDataDir+"/.dockerconfigjson").Execute(); err != nil {
		e2e.Logf("warning: release extract failed once with:\n\"%v\"", err)

		//Workaround disconnected baremental clusters that don't have cert for the registry
		platform := exutil.CheckPlatform(oc)
		if strings.Contains(platform, "baremetal") || strings.Contains(platform, "none") {
			var mirror_registry string
			mirror_registry, err = getMirrorRegistry(oc)
			if mirror_registry != "" {
				if err != nil {
					err = fmt.Errorf("error out getting mirror registry: %v", err)
					return "", err
				}
				if err = oc.AsAdmin().Run("adm").Args("release", "extract", "--insecure", "--to", manifestDir, "-a", tempDataDir+"/.dockerconfigjson").Execute(); err != nil {
					err = fmt.Errorf("warning: insecure release extract for disconnected baremetal failed with:\n\"%v\"", err)
				}
				return "", err
			}
		}

		//Workaround c2s/cs2s clusters that only have token to the mirror in pull secret
		var region, image, mirror string
		if region, err = oc.AsAdmin().WithoutNamespace().Run("get").Args("infrastructure",
			"cluster", "-o=jsonpath={.status.platformStatus.aws.region}").Output(); err != nil {
			err = fmt.Errorf("failed to get cluster region: %v", err)
			return "", err
		}

		// region us-iso-* represent C2S, us-isob-* represent SC2S
		if !strings.Contains(region, "us-iso-") && !strings.Contains(region, "us-isob-") {
			err = fmt.Errorf("oc adm release failed, and no retry for non-c2s/cs2s region: %s", region)
			return "", err
		}

		if image, err = exutil.GetReleaseImage(oc); err != nil {
			err = fmt.Errorf("failed to get cluster release image: %v", err)
			return "", err
		}

		if mirror, err = oc.AsAdmin().Run("get").Args("ImageContentSourcePolicy",
			"-o", "jsonpath={.items[0].spec.repositoryDigestMirrors[0].mirrors[0]}").Output(); err != nil {
			err = fmt.Errorf("failed to acquire mirror from ICSP: %v", err)
			return "", err
		}

		if err = oc.AsAdmin().Run("adm").Args("release", "extract",
			"--from", fmt.Sprintf("%s@%s", mirror, strings.Split(image, "@")[1]),
			"--to", manifestDir, "-a", tempDataDir+"/.dockerconfigjson", "--insecure").Execute(); err != nil {
			err = fmt.Errorf("failed to extract manifests: %v", err)
			return "", err
		}
	}
	return tempDataDir, nil
}

// Check if a non-namespace resource existed
func isGlobalResourceExist(oc *exutil.CLI, resourceType string) bool {
	output, err := oc.AsAdmin().WithoutNamespace().Run("get").Args(resourceType).Output()
	o.Expect(err).NotTo(o.HaveOccurred(), "fail to get resource %s", resourceType)
	if strings.Contains(output, "No resources found") {
		e2e.Logf("there is no %s in this cluster!", resourceType)
		return false
	}
	return true
}

// Check ICSP or IDMS to get mirror registry info
func getMirrorRegistry(oc *exutil.CLI) (registry string, err error) {
	if isGlobalResourceExist(oc, "ImageContentSourcePolicy") {
		if registry, err = oc.AsAdmin().WithoutNamespace().Run("get").Args("ImageContentSourcePolicy",
			"-o", "jsonpath={.items[0].spec.repositoryDigestMirrors[0].mirrors[0]}").Output(); err == nil {
			registry, _, _ = strings.Cut(registry, "/")
		} else {
			err = fmt.Errorf("failed to acquire mirror registry from ICSP: %v", err)
		}
		return
	} else if isGlobalResourceExist(oc, "ImageDigestMirrorSet") {
		if registry, err = oc.AsAdmin().WithoutNamespace().Run("get").Args("ImageDigestMirrorSet",
			"-o", "jsonpath={.items[0].spec.imageDigestMirrors[0].mirrors[0]}").Output(); err == nil {
			registry, _, _ = strings.Cut(registry, "/")
		} else {
			err = fmt.Errorf("failed to acquire mirror registry from IDMS: %v", err)
		}
		return
	} else {
		err = fmt.Errorf("no ICSP or IDMS found!")
		return
	}
}

// get clusterversion version object values by jsonpath.
// Returns: object_value(string), error
func getCVObyJP(oc *exutil.CLI, jsonpath string) (string, error) {
	return oc.AsAdmin().WithoutNamespace().Run("get").
		Args("clusterversion", "version",
			"-o", fmt.Sprintf("jsonpath={%s}", jsonpath)).Output()
}

// Returns: object_value(map), error
func getCVOPod(oc *exutil.CLI, jsonpath string) (map[string]interface{}, error) {
	var objectValue map[string]interface{}
	pod, err := oc.AsAdmin().WithoutNamespace().Run("get").Args("pod", "-n", "openshift-cluster-version", "-o=jsonpath={.items[].metadata.name}").Output()
	if err != nil {
		return nil, fmt.Errorf("getting CVO pod name failed: %v", err)
	}
	output, err := oc.AsAdmin().WithoutNamespace().Run("get").
		Args("pod", pod, "-n", "openshift-cluster-version",
			"-o", fmt.Sprintf("jsonpath={%s}", jsonpath)).Output()
	if err != nil {
		return nil, fmt.Errorf("getting CVO pod object values failed: %v", err)
	}
	err = json.Unmarshal([]byte(output), &objectValue)
	if err != nil {
		return nil, fmt.Errorf("unmarshal release info error: %v", err)
	}
	return objectValue, nil
}

// patch resource (namespace - use "" if none, resource_name, patch).
// Returns: result(string), error
func ocJSONPatch(oc *exutil.CLI, namespace string, resource string, patch []JSONp) (patchOutput string, err error) {
	p, err := json.Marshal(patch)
	if err != nil {
		e2e.Logf("ocJSONPatch Error - json.Marshal: '%v'", err)
		o.Expect(err).NotTo(o.HaveOccurred())
	}
	if namespace != "" {
		patchOutput, err = oc.AsAdmin().WithoutNamespace().Run("patch").
			Args("-n", namespace, resource, "--type=json", "--patch", string(p)).Output()
	} else {
		patchOutput, err = oc.AsAdmin().WithoutNamespace().Run("patch").
			Args(resource, "--type=json", "--patch", string(p)).Output()
	}
	e2e.Logf("patching '%s'\nwith '%s'\nresult '%s'", resource, string(p), patchOutput)
	return
}

// Run "oc adm release info" cmd to get release info of the current release
func getReleaseInfo(oc *exutil.CLI) (output string, err error) {
	tempDataDir := filepath.Join("/tmp/", fmt.Sprintf("ota-%s", getRandomString()))
	err = os.Mkdir(tempDataDir, 0755)
	defer os.RemoveAll(tempDataDir)
	if err != nil {
		err = fmt.Errorf("failed to create tempdir %s: %v", tempDataDir, err)
		return
	}

	if err = oc.AsAdmin().Run("extract").Args("secret/pull-secret", "-n", "openshift-config", "--confirm", "--to="+tempDataDir).Execute(); err != nil {
		err = fmt.Errorf("failed to extract dockerconfig: %v", err)
		return
	}

	if output, err = oc.AsAdmin().Run("adm").Args("release", "info", "-a", tempDataDir+"/.dockerconfigjson", "-ojson").Output(); err != nil {
		e2e.Logf("warning: release info failed once with:\n\"%v\"", err)
		//Workaround disconnected baremental clusters that don't have cert for the registry
		platform := exutil.CheckPlatform(oc)
		if strings.Contains(platform, "baremetal") || strings.Contains(platform, "none") {
			var mirror_registry string
			mirror_registry, err = getMirrorRegistry(oc)
			if mirror_registry != "" {
				if err != nil {
					err = fmt.Errorf("error out getting mirror registry: %v", err)
					return
				}
				if err = oc.AsAdmin().Run("adm").Args("release", "info", "--insecure", "-a", tempDataDir+"/.dockerconfigjson", "-ojson").Execute(); err != nil {
					err = fmt.Errorf("warning: insecure release info for disconnected baremetal failed with:\n\"%v\"", err)
				}
				return
			}
		}
		//Workaround c2s/cs2s clusters that only have token to the mirror in pull secret
		var region, image, mirror string
		if region, err = oc.AsAdmin().WithoutNamespace().Run("get").Args("infrastructure",
			"cluster", "-o=jsonpath={.status.platformStatus.aws.region}").Output(); err != nil {
			err = fmt.Errorf("failed to get cluster region: %v", err)
			return
		}

		// region us-iso-* represent C2S, us-isob-* represent SC2S
		if !strings.Contains(region, "us-iso-") && !strings.Contains(region, "us-isob-") {
			err = fmt.Errorf("oc adm release failed, and no retry for non-c2s/cs2s region: %s", region)
			return
		}

		if image, err = exutil.GetReleaseImage(oc); err != nil {
			err = fmt.Errorf("failed to get cluster release image: %v", err)
			return
		}

		if mirror, err = oc.AsAdmin().Run("get").Args("ImageContentSourcePolicy",
			"-o", "jsonpath={.items[0].spec.repositoryDigestMirrors[0].mirrors[0]}").Output(); err != nil {
			err = fmt.Errorf("failed to acquire mirror from ICSP: %v", err)
			return
		}
		if output, err = oc.AsAdmin().Run("adm").Args("release", "info",
			"--insecure", "-a", tempDataDir+"/.dockerconfigjson",
			fmt.Sprintf("%s@%s", mirror, strings.Split(image, "@")[1])).Output(); err != nil {
			err = fmt.Errorf("failed to get release info: %v", err)
			return
		}
	}
	return
}

// change the spec.capabilities
// if base==true, change the baselineCapabilitySet, otherwise, change the additionalEnabledCapabilities
func changeCap(oc *exutil.CLI, base bool, cap interface{}) (string, error) {
	var spec string
	if base {
		spec = "/spec/capabilities/baselineCapabilitySet"
	} else {
		spec = "/spec/capabilities/additionalEnabledCapabilities"
	}
	if cap == nil {
		return ocJSONPatch(oc, "", "clusterversion/version", []JSONp{{"remove", spec, nil}})
	}
	// if spec.capabilities is not present, patch to add capabilities
	orgCap, err := getCVObyJP(oc, ".spec.capabilities")
	if err != nil {
		return "", err
	}
	if orgCap == "" {
		value := make(map[string]interface{})
		_, err = ocJSONPatch(oc, "", "clusterversion/version", []JSONp{{"add", "/spec/capabilities", value}})
		if err != nil {
			return "", err
		}
	}
	return ocJSONPatch(oc, "", "clusterversion/version", []JSONp{{"add", spec, cap}})
}

// getDeploymentsYaml dumps out deployment in yaml format in specific namespace
func getDeploymentsYaml(oc *exutil.CLI, deploymentName string, namespace string) (string, error) {
	e2e.Logf("Dumping deployments %s from namespace %s", deploymentName, namespace)
	out, err := oc.AsAdmin().WithoutNamespace().Run("get").Args("deployment", deploymentName, "-n", namespace, "-o", "yaml").Output()
	if err != nil {
		e2e.Logf("Error dumping deployments: %v", err)
		return "", err
	}
	e2e.Logf("%s", out)
	return out, err
}

// podExec executes a single command or a bash script in the running pod. It returns the
// command output and error if the command finished with non-zero status code or the
// command took longer than 3 minutes to run.
func podExec(oc *exutil.CLI, script string, namespace string, podName string) (string, error) {
	var out string
	waitErr := wait.PollUntilContextTimeout(context.Background(), 1*time.Second, 3*time.Minute, true, func(context.Context) (bool, error) {
		var err error
		out, err = oc.AsAdmin().WithoutNamespace().Run("exec").Args("-n", namespace, podName, "--", "/bin/bash", "-c", script).Output()
		return true, err
	})
	return out, waitErr
}

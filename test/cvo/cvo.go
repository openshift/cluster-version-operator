package cvo

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"time"

	g "github.com/onsi/ginkgo/v2"
	o "github.com/onsi/gomega"
	configv1 "github.com/openshift/api/config/v1"
	exutil "github.com/openshift/cluster-version-operator/test/util"
	"github.com/tidwall/gjson"
	yamlv3 "gopkg.in/yaml.v3"
	e2e "k8s.io/kubernetes/test/e2e/framework"
	"sigs.k8s.io/yaml"
)

var _ = g.Describe("[cvo-testing] cluster-version-operator-tests", func() {
	g.It("should support passing tests", func() {
		o.Expect(true).To(o.BeTrue())
	})

	defer g.GinkgoRecover()
	oc := exutil.NewCLIWithoutNamespace("ota-cvo")

	g.It("Author:jiajliu-Medium-47198-Techpreview operator will not be installed on a fresh installed cluster", func() {
		tpOperatorNames := []string{"cluster-api"}
		tpOperator := []map[string]string{
			{"ns": "openshift-cluster-api", "co": tpOperatorNames[0]}}

		featuregate, err := oc.AsAdmin().WithoutNamespace().Run("get").Args("featuregate", "cluster", "-o=jsonpath={.spec}").Output()
		o.Expect(err).NotTo(o.HaveOccurred())
		e2e.Logf("Featuregate: %s", featuregate)
		if featuregate != "{}" {
			if strings.Contains(featuregate, "TechPreviewNoUpgrade") {
				g.Skip("This case is only suitable for non-techpreview cluster!")
			} else if strings.Contains(featuregate, "CustomNoUpgrade") {
				e2e.Logf("Drop openshift-cluster-api ns and cluster-api co due to CustomNoUpgrade fs enabled!")
				if len(tpOperator) > 0 {
					tpOperator = tpOperator[1:]
				}
				if len(tpOperator) == 0 {
					g.Skip("No tp candidates found for this version's test!")
				}
			} else {
				e2e.Failf("Neither TechPreviewNoUpgrade fs nor CustomNoUpgrade fs enabled, stop here to confirm expected behavior first!")
			}
		}

		g.By("Check annotation release.openshift.io/feature-set=TechPreviewNoUpgrade in manifests are correct.")
		tempDataDir, err := extractManifest(oc)
		defer func() { o.Expect(os.RemoveAll(tempDataDir)).NotTo(o.HaveOccurred()) }()
		o.Expect(err).NotTo(o.HaveOccurred())
		manifestDir := filepath.Join(tempDataDir, "manifest")
		cmd := fmt.Sprintf("grep -rl 'release.openshift.io/feature-set: .*TechPreviewNoUpgrade.*' %s|grep 'cluster.*operator.yaml'", manifestDir)
		featuresetTechPreviewManifest, err := exec.Command("bash", "-c", cmd).CombinedOutput()
		o.Expect(err).NotTo(o.HaveOccurred(), "Command: \"%s\" returned error: %s", cmd, string(featuresetTechPreviewManifest))
		tpOperatorFilePaths := strings.Split(strings.TrimSpace(string(featuresetTechPreviewManifest)), "\n")
		o.Expect(len(tpOperatorFilePaths)).To(o.Equal(len(tpOperator)))
		e2e.Logf("Expected number of cluster operator manifest files with correct annotation found!")

		for _, file := range tpOperatorFilePaths {
			data, err := os.ReadFile(file)
			o.Expect(err).NotTo(o.HaveOccurred())
			var co configv1.ClusterOperator
			err = yaml.Unmarshal(data, &co)
			o.Expect(err).NotTo(o.HaveOccurred())
			for i := 0; i < len(tpOperatorNames); i++ {
				if co.Name == tpOperatorNames[i] {
					e2e.Logf("Found %s in file %v!", tpOperatorNames[i], file)
					tpOperatorNames = append(tpOperatorNames[:i], tpOperatorNames[i+1:]...)
					break
				}
			}
		}
		o.Expect(len(tpOperatorNames)).To(o.Equal(0))
		e2e.Logf("All expected tp operators found in manifests!")

		g.By("Check no TP operator installed by default.")
		for i := 0; i < len(tpOperator); i++ {
			for k, v := range tpOperator[i] {
				output, err := oc.AsAdmin().WithoutNamespace().Run("get").Args(k, v).Output()
				o.Expect(err).To(o.HaveOccurred(), "techpreview operator '%s %s' absence check failed: expecting an error, received: '%s'", k, v, output)
				o.Expect(output).To(o.ContainSubstring("NotFound"))
				e2e.Logf("Expected: Resource %s/%v not found!", k, v)
			}
		}
	})

	g.It("Author:dis-High-56072-CVO pod should not crash", func() {
		g.By("Get CVO container status")
		CVOStatus, err := getCVOPod(oc, ".status.containerStatuses[]")
		o.Expect(err).NotTo(o.HaveOccurred())
		o.Expect(CVOStatus).NotTo(o.BeNil())

		g.By("Check ready is true")
		o.Expect(CVOStatus["ready"]).To(o.BeTrue(), "CVO is not ready: %v", CVOStatus)

		g.By("Check started is true")
		o.Expect(CVOStatus["started"]).To(o.BeTrue(), "CVO is not started: %v", CVOStatus)

		g.By("Check state is running")
		o.Expect(CVOStatus["state"]).NotTo(o.BeNil(), "CVO have no state: %v", CVOStatus)
		o.Expect(CVOStatus["state"].(map[string]interface{})["running"]).NotTo(o.BeNil(), "CVO state have no running: %v", CVOStatus)

		g.By("Check exitCode of lastState is 0 if lastState is not empty")
		lastState := CVOStatus["lastState"]
		o.Expect(lastState).NotTo(o.BeNil(), "CVO have no lastState: %v", CVOStatus)
		if reflect.ValueOf(lastState).Len() == 0 {
			e2e.Logf("lastState is empty which is expected")
		} else {
			o.Expect(lastState.(map[string]interface{})["terminated"]).NotTo(o.BeNil(), "no terminated for non-empty CVO lastState: %v", CVOStatus)
			exitCode := lastState.(map[string]interface{})["terminated"].(map[string]interface{})["exitCode"].(float64)
			if exitCode == 255 && strings.Contains(
				lastState.(map[string]interface{})["terminated"].(map[string]interface{})["message"].(string),
				"Failed to get FeatureGate from cluster") {
				e2e.Logf("detected a known issue OCPBUGS-13873, skipping lastState check")
			} else {
				o.Expect(exitCode).To(o.BeZero(), "CVO terminated with non-zero code: %v", CVOStatus)
				reason := lastState.(map[string]interface{})["terminated"].(map[string]interface{})["reason"]
				o.Expect(reason.(string)).To(o.Equal("Completed"), "CVO terminated with unexpected reason: %v", CVOStatus)
			}
		}
	})

	g.It("Author:jiajliu-Medium-53906-The architecture info in clusterversion’s status should be correct", func() {
		const heterogeneousArchKeyword = "multi"
		expectedArchMsg := "architecture=\"Multi\""
		g.By("Get release info from current cluster")
		releaseInfo, err := getReleaseInfo(oc)
		o.Expect(err).NotTo(o.HaveOccurred())
		o.Expect(releaseInfo).NotTo(o.BeEmpty())

		g.By("Check the arch info cv.status is expected")
		cvArchInfo, err := getCVObyJP(oc, ".status.conditions[?(.type=='ReleaseAccepted')].message")
		o.Expect(err).NotTo(o.HaveOccurred())
		e2e.Logf("Release payload info in cv.status: %v", cvArchInfo)

		if releaseArch := gjson.Get(releaseInfo, `metadata.metadata.release\.openshift\.io/architecture`).String(); releaseArch != heterogeneousArchKeyword {
			e2e.Logf("This current release is a non-heterogeneous payload")
			//It's a non-heterogeneous payload, the architecture info in clusterversion’s status should be consistent with runtime.GOARCH.

			output, err := oc.AsAdmin().WithoutNamespace().
				Run("get").Args("nodes", "-o",
				"jsonpath={.items[*].status.nodeInfo.architecture}").Output()
			o.Expect(err).NotTo(o.HaveOccurred())
			nodesArchInfo := strings.Split(strings.TrimSpace(output), " ")
			e2e.Logf("Nodes arch list: %v", nodesArchInfo)

			for _, nArch := range nodesArchInfo {
				if nArch != nodesArchInfo[0] {
					e2e.Failf("unexpected node arch in non-hetero cluster: %s expecting: %s",
						nArch, nodesArchInfo[0])
				}
			}

			e2e.Logf("Expected arch info: %v", nodesArchInfo[0])
			o.Expect(cvArchInfo).To(o.ContainSubstring(nodesArchInfo[0]))
		} else {
			e2e.Logf("This current release is a heterogeneous payload")
			// It's a heterogeneous payload, the architecture info in clusterversion’s status should be multi.
			e2e.Logf("Expected arch info: %v", expectedArchMsg)
			o.Expect(cvArchInfo).To(o.ContainSubstring(expectedArchMsg))
		}
	})

	g.It("Author:jiajliu-Low-46922-check runlevel in cvo ns", func() {
		g.By("Check runlevel in cvo namespace.")
		runLevel, err := oc.AsAdmin().WithoutNamespace().Run("get").
			Args("ns", "openshift-cluster-version",
				"-o=jsonpath={.metadata.labels.openshift\\.io/run-level}").Output()
		o.Expect(err).NotTo(o.HaveOccurred())
		o.Expect(runLevel).To(o.Equal(""))

		g.By("Check scc of cvo pod.")
		runningPodName, err := oc.AsAdmin().WithoutNamespace().Run("get").
			Args("pod", "-n", "openshift-cluster-version", "-o=jsonpath='{.items[?(@.status.phase == \"Running\")].metadata.name}'").Output()
		o.Expect(err).NotTo(o.HaveOccurred())
		o.Expect(runningPodName).NotTo(o.Equal("''"))
		runningPodList := strings.Fields(runningPodName)
		if len(runningPodList) != 1 {
			e2e.Failf("Unexpected running cvo pods detected: %s", runningPodName)
		}
		scc, err := oc.AsAdmin().WithoutNamespace().Run("get").
			Args("pod", "-n", "openshift-cluster-version", strings.Trim(runningPodList[0], "'"),
				"-o=jsonpath={.metadata.annotations.openshift\\.io/scc}").Output()
		o.Expect(err).NotTo(o.HaveOccurred())
		o.Expect(scc).To(o.Equal("hostaccess"))
	})

	g.It("Author:dis-Low-49670-change spec.capabilities to invalid value", func() {
		orgCap, err := getCVObyJP(oc, ".spec.capabilities")
		o.Expect(err).NotTo(o.HaveOccurred())
		if orgCap == "" {
			defer func() {
				out, err := ocJSONPatch(oc, "", "clusterversion/version", []JSONp{{"remove", "/spec/capabilities", nil}})
				o.Expect(err).NotTo(o.HaveOccurred(), out)
			}()
		} else {
			orgBaseCap, err := getCVObyJP(oc, ".spec.capabilities.baselineCapabilitySet")
			o.Expect(err).NotTo(o.HaveOccurred())
			orgAddCapstr, err := getCVObyJP(oc, ".spec.capabilities.additionalEnabledCapabilities[*]")
			o.Expect(err).NotTo(o.HaveOccurred())
			e2e.Logf("original baseline: '%s', original additional: '%s'", orgBaseCap, orgAddCapstr)

			orgAddCap := strings.Split(orgAddCapstr, " ")
			defer func() {
				if newBaseCap, _ := getCVObyJP(oc, ".spec.capabilities.baselineCapabilitySet"); orgBaseCap != newBaseCap {
					var out string
					var err error
					if orgBaseCap == "" {
						out, err = changeCap(oc, true, nil)
					} else {
						out, err = changeCap(oc, true, orgBaseCap)
					}
					o.Expect(err).NotTo(o.HaveOccurred(), out)
				} else {
					e2e.Logf("defer baselineCapabilitySet skipped for original value already matching '%v'", newBaseCap)
				}
			}()
			defer func() {
				if newAddCap, _ := getCVObyJP(oc, ".spec.capabilities.additionalEnabledCapabilities[*]"); !reflect.DeepEqual(orgAddCap, strings.Split(newAddCap, " ")) {
					var out string
					var err error
					if reflect.DeepEqual(orgAddCap, make([]string, 1)) {
						// need this cause strings.Split of an empty string creates len(1) slice which isn't nil
						out, err = changeCap(oc, false, nil)
					} else {
						out, err = changeCap(oc, false, orgAddCap)
					}
					o.Expect(err).NotTo(o.HaveOccurred(), out)
				} else {
					e2e.Logf("defer additionalEnabledCapabilities skipped for original value already matching '%v'", strings.Split(newAddCap, " "))
				}
			}()
		}

		g.By("Set invalid baselineCapabilitySet")
		cmdOut, err := changeCap(oc, true, "Invalid")
		o.Expect(err).To(o.HaveOccurred())
		clusterVersion, _, err := exutil.GetClusterVersion(oc)
		o.Expect(err).NotTo(o.HaveOccurred())
		version := strings.Split(clusterVersion, ".")
		minor_version := version[1]
		latest_version, err := strconv.Atoi(minor_version)
		o.Expect(err).NotTo(o.HaveOccurred())
		var versions []string
		if latest_version > 18 {
			latest_version = 18
		}
		for i := 11; i <= latest_version; i++ {
			versions = append(versions, "\"v4."+strconv.Itoa(i)+"\"")
		}
		versions = append(versions, "\"vCurrent\"")
		result := "Unsupported value: \"Invalid\": supported values: \"None\", " + strings.Join(versions, ", ")
		o.Expect(cmdOut).To(o.ContainSubstring(result))
		// Important! this one should be updated each version with new capabilities, as they added to openshift.
		g.By("Set invalid additionalEnabledCapabilities")
		cmdOut, err = changeCap(oc, false, []string{"Invalid"})
		o.Expect(err).To(o.HaveOccurred())
		o.Expect(cmdOut).To(o.ContainSubstring("Unsupported value: \"Invalid\": supported values: \"openshift-samples\", \"baremetal\", \"marketplace\", \"Console\", \"Insights\", \"Storage\", \"CSISnapshot\", \"NodeTuning\", \"MachineAPI\", \"Build\", \"DeploymentConfig\", \"ImageRegistry\", \"OperatorLifecycleManager\", \"CloudCredential\", \"Ingress\", \"CloudControllerManager\", \"OperatorLifecycleManagerV1\""))
	})

	g.It("Author:dis-Medium-41391-cvo serves metrics over only https not http", func() {
		g.By("Check cvo delopyment config file...")
		cvoNS := "openshift-cluster-version"
		cvoDeploymentYaml, err := getDeploymentsYaml(oc, "cluster-version-operator", cvoNS)
		o.Expect(err).NotTo(o.HaveOccurred())
		var keywords = []string{"--listen=0.0.0.0:9099",
			"--serving-cert-file=/etc/tls/serving-cert/tls.crt",
			"--serving-key-file=/etc/tls/serving-cert/tls.key"}
		for _, v := range keywords {
			o.Expect(cvoDeploymentYaml).Should(o.ContainSubstring(v))
		}

		g.By("Check cluster-version-operator binary help")
		cvoPodsList, err := exutil.WaitForPods(
			oc.AdminKubeClient().CoreV1().Pods(cvoNS),
			exutil.ParseLabelsOrDie("k8s-app=cluster-version-operator"),
			exutil.CheckPodIsReady, 1, 3*time.Minute)
		o.Expect(err).NotTo(o.HaveOccurred())
		e2e.Logf("Get cvo pods: %v", cvoPodsList)
		output, err := podExec(oc, "/usr/bin/cluster-version-operator start --help", cvoNS, cvoPodsList[0])
		exutil.AssertWaitPollNoErr(err, fmt.Sprintf(
			"/usr/bin/cluster-version-operator start --help executs error on %v", cvoPodsList[0]))
		e2e.Logf("CVO help returned: %s", output)
		keywords = []string{"You must set both --serving-cert-file and --serving-key-file unless you set --listen empty"}
		for _, v := range keywords {
			o.Expect(output).Should(o.ContainSubstring(v))
		}
		g.By("Verify cvo metrics is only exported via https")
		output, err = oc.AsAdmin().WithoutNamespace().Run("get").
			Args("servicemonitor", "cluster-version-operator",
				"-n", cvoNS, "-o=json").Output()
		o.Expect(err).NotTo(o.HaveOccurred())
		var result map[string]interface{}
		err = json.Unmarshal([]byte(output), &result)
		o.Expect(err).NotTo(o.HaveOccurred())
		endpoints := result["spec"].(map[string]interface{})["endpoints"]
		e2e.Logf("Get cvo's spec.endpoints: %v", endpoints)
		o.Expect(endpoints).Should(o.HaveLen(1))

		output, err = oc.AsAdmin().WithoutNamespace().Run("get").
			Args("servicemonitor", "cluster-version-operator",
				"-n", cvoNS, "-o=jsonpath={.spec.endpoints[].scheme}").Output()
		o.Expect(err).NotTo(o.HaveOccurred())
		e2e.Logf("Get cvo's spec.endpoints scheme: %v", output)
		o.Expect(output).Should(o.Equal("https"))
		g.By("Get cvo endpoint URI")
		//output, err = oc.AsAdmin().WithoutNamespace().Run("get").Args("endpoints", "cluster-version-operator", "-n", projectName, "-o=jsonpath='{.subsets[0].addresses[0].ip}:{.subsets[0].ports[0].port}'").Output()
		output, err = oc.AsAdmin().WithoutNamespace().Run("get").
			Args("endpoints", "cluster-version-operator",
				"-n", cvoNS, "--no-headers").Output()
		o.Expect(err).NotTo(o.HaveOccurred())
		re := regexp.MustCompile(`cluster-version-operator\s+([^\s]*)`)
		matchedResult := re.FindStringSubmatch(output)
		e2e.Logf("Regex mached result: %v", matchedResult)
		o.Expect(matchedResult).Should(o.HaveLen(2))
		endpointURI := matchedResult[1]
		e2e.Logf("Get cvo endpoint URI: %v", endpointURI)
		o.Expect(endpointURI).ShouldNot(o.BeEmpty())

		g.By("Check metric server is providing service https, but not http")
		cmd := fmt.Sprintf("curl http://%s/metrics", endpointURI)
		output, err = podExec(oc, cmd, cvoNS, cvoPodsList[0])
		exutil.AssertWaitPollNoErr(err, fmt.Sprintf("cmd %s executs error on %v", cmd, cvoPodsList[0]))
		keywords = []string{"Client sent an HTTP request to an HTTPS server"}
		for _, v := range keywords {
			o.Expect(output).Should(o.ContainSubstring(v))
		}
		g.By("Check metric server is providing service via https correctly.")
		cmd = fmt.Sprintf("curl -k -I https://%s/metrics", endpointURI)
		output, err = podExec(oc, cmd, cvoNS, cvoPodsList[0])
		exutil.AssertWaitPollNoErr(err, fmt.Sprintf("cmd %s executs error on %v", cmd, cvoPodsList[0]))
		keywords = []string{"HTTP/1.1 200 OK"}
		for _, v := range keywords {
			o.Expect(output).Should(o.ContainSubstring(v))
		}
	})

	g.It("Author:jianl-High-42543-the removed resources are not created in a fresh installed cluster", func() {
		g.By("Validate resource with 'release.openshift.io/delete: \"true\"' annotation is not installed")
		tempDataDir, err := extractManifest(oc)
		defer func() { _ = os.RemoveAll(tempDataDir) }()
		o.Expect(err).NotTo(o.HaveOccurred())
		manifestDir := filepath.Join(tempDataDir, "manifest")
		entries, err := os.ReadDir(manifestDir)
		o.Expect(err).NotTo(o.HaveOccurred())
		for _, entry := range entries {
			nameLower := strings.ToLower(entry.Name())
			if strings.Contains(nameLower, "cleanup") {
				e2e.Logf("Skipping file %s because it matches cleanup filter", entry.Name())
				continue
			}
			filePath := filepath.Join(manifestDir, entry.Name())
			file, err := os.Open(filePath)
			o.Expect(err).NotTo(o.HaveOccurred())
			defer file.Close()
			decoder := yamlv3.NewDecoder(file)
			for {
				var doc map[string]interface{}
				if err := decoder.Decode(&doc); err != nil {
					if err == io.EOF {
						break
					}
					continue
				}
				meta, _ := doc["metadata"].(map[string]interface{})
				ann, _ := meta["annotations"].(map[string]interface{})
				if ann == nil || ann["release.openshift.io/delete"] != "true" {
					continue
				}
				kind, _ := doc["kind"].(string)
				name, _ := meta["name"].(string)
				namespace, _ := meta["namespace"].(string)
				args := []string{"get", kind, name}
				if namespace != "" {
					args = append(args, "-n", namespace)
				}
				_, err := oc.AsAdmin().WithoutNamespace().Run(args[0]).Args(args[1:]...).Output()
				o.Expect(err).To(o.HaveOccurred())
			}
		}
	})

})

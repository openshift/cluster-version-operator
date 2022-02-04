package releasediff

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/ioutil"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"gopkg.in/yaml.v2"

	"github.com/spf13/cobra"
)

var (
	releaseResourceDiffOpts struct {
		Log      bool
		Results  string
		Target   string
		Releases string
	}

	targetResources = make(map[ResourceId]string)
)

type ResourceYaml struct {
	APIVersion string `yaml:"apiVersion"`
	Kind       string `yaml:"kind"`
	Metadata   struct {
		Name        string `yaml:"name"`
		Namespace   string `yaml:"namespace"`
		Annotations struct {
			DeleteAnnotation string `yaml:"release.openshift.io/delete"`
		}
	}
}

type ResourceId struct {
	Group     string
	Kind      string
	Name      string
	Namespace string
}

func NewReleaseResourceDiffCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "release-resource-diff",
		Short: "Checks if OCP resources exist in a given newly installed OCP release",
		RunE:  runReleaseResourceDiffCmd,
	}

	flags := cmd.Flags()
	flags.BoolVarP(&releaseResourceDiffOpts.Log, "verbose", "v", false, "verbose logging")
	flags.StringVarP(&releaseResourceDiffOpts.Results, "output", "o", releaseResourceDiffOpts.Results, "results file path (optional)")
	flags.StringVarP(&releaseResourceDiffOpts.Target, "target", "t", releaseResourceDiffOpts.Target, "file containing all resources from a running, newly installed, OCP release")
	flags.StringVarP(&releaseResourceDiffOpts.Releases, "releases", "r", releaseResourceDiffOpts.Releases, "top-level directory containing subdirectories for each OCP release to be checked")
	return cmd
}

func (r ResourceId) uniqueName() string {
	if r.Namespace == "<none>" {
		return r.Name
	}
	return r.Namespace + "/" + r.Name
}

func (r ResourceId) String() string {
	return fmt.Sprintf("%s %s %s", r.Group, r.Kind, r.uniqueName())
}

func (r ResourceId) resourceWithVersionString(version string) string {
	return fmt.Sprintf("{%s/%s %s %s}", r.Group, version, r.Kind, r.uniqueName())
}

type ResourceSource struct {
	Release       string
	LastInRelease string
	YamlFileName  string
	Version       string
}

func runReleaseResourceDiffCmd(cmd *cobra.Command, args []string) error {
	if len(releaseResourceDiffOpts.Target) == 0 {
		return errors.New("must specify target file containing all resources from a running, newly installed, OCP release")
	}
	if len(releaseResourceDiffOpts.Releases) == 0 {
		return errors.New("must specify releases top-level directory containing subdirectories for each OCP release to be checked")
	}
	if len(releaseResourceDiffOpts.Results) == 0 {
		releaseResourceDiffOpts.Results = releaseResourceDiffOpts.Releases + "/delete-candidates.txt"
	}
	if err := loadTargetResources(releaseResourceDiffOpts.Target); err != nil {
		return err
	}

	releaseDirs, err := loadYamlFileDirs(releaseResourceDiffOpts.Releases)
	if err != nil {
		return err
	}

	if len(releaseDirs) == 0 {
		return fmt.Errorf("No directories found under %s", releaseResourceDiffOpts.Releases)
	}
	fmt.Println("Checking...")

	deleteCandidates := make(map[ResourceId]ResourceSource)
	alreadyDeleted := make(map[ResourceId]struct{})
	for _, dir := range releaseDirs {
		fmt.Printf("%s:\n", dir)
		resources, err := getReleaseResources(releaseResourceDiffOpts.Releases+"/"+dir, alreadyDeleted)
		if err != nil {
			return err
		}
		fmt.Printf("%d resources found to check\n", len(resources))
		checkIfOrphaned(resources, deleteCandidates)
	}
	filterDeleteCandidates(deleteCandidates, alreadyDeleted)
	if err := outputDeleteCandidates(deleteCandidates); err != nil {
		return err
	}
	return nil
}

func loadTargetResources(path string) error {
	target, _ := filepath.Abs(path)
	file, err := os.Open(target)
	if err != nil {
		return fmt.Errorf("Unable to read target release file %s; err=%v", path, err)
	}
	scanner := bufio.NewScanner(file)
	scanner.Split(bufio.ScanLines)
	var txtlines []string

	for scanner.Scan() {
		txtlines = append(txtlines, scanner.Text())
	}

	file.Close()

	for _, eachline := range txtlines {
		ids := strings.Fields(eachline)
		if len(ids) != 4 {
			return fmt.Errorf("The target release file should have 4 columns: group, kind, name, namespace. Found %s\n", eachline)
		}
		api, version := splitApiVersion(ids[0])
		resourceId := ResourceId{
			Group:     api,
			Kind:      ids[1],
			Name:      ids[2],
			Namespace: ids[3],
		}
		// save version for use in migrated APIVersion check
		targetResources[resourceId] = version
		logIt(fmt.Sprintf("%v", resourceId))
	}
	return nil
}

func loadYamlFileDirs(dir string) ([]string, error) {
	files, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("Unable to read dir %s; err=%v", dir, err)
	}

	var dirs []string
	for _, file := range files {
		info, _ := file.Info()
		if info.IsDir() {
			dirs = append(dirs, info.Name())
		}
	}
	return dirs, nil
}

func manifestHasDeleteAnno(resourceYaml ResourceYaml) bool {
	return resourceYaml.Metadata.Annotations.DeleteAnnotation == "true"
}

func getReleaseResources(dir string, tbd map[ResourceId]struct{}) (map[ResourceId]ResourceSource, error) {
	files, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("Unable to read dir %s; err=%v", dir, err)
	}

	minor := getMinorRelease(filepath.Base(dir))
	ids := make(map[ResourceId]ResourceSource)

	for _, file := range files {
		info, _ := file.Info()

		if filepath.Ext(info.Name()) != ".yaml" {
			continue
		}
		filename := dir + "/" + info.Name()
		yamlBytes, err := ioutil.ReadFile(filename)

		if err != nil {
			return nil, fmt.Errorf("Unable to read file %s; err=%v", filename, err)
		}

		allByteSlices, err := splitYaml(yamlBytes)
		if err != nil {
			return nil, fmt.Errorf("Unable to split yaml from file %s; err=%v", filename, err)
		}

		resourceSource := ResourceSource{
			Release:       minor,
			LastInRelease: minor,
			YamlFileName:  info.Name(),
		}
		logIt(fmt.Sprintf("%v", resourceSource))

		for _, byteSlice := range allByteSlices {
			var yamlId ResourceYaml
			err = yaml.Unmarshal(byteSlice, &yamlId)
			if err != nil {
				return nil, fmt.Errorf("Unable to unmarshall yaml from file %s; err=%v", filename, err)
			}
			if !validKey(yamlId) {
				logIt(fmt.Sprintf("Ignoring invalid resource key %v", yamlId))
				continue
			}
			if len(yamlId.Metadata.Namespace) == 0 {
				yamlId.Metadata.Namespace = "<none>"
			}
			logIt(fmt.Sprintf("%v", yamlId))

			api, version := splitApiVersion(yamlId.APIVersion)

			resourceId := ResourceId{
				Group:     api,
				Kind:      yamlId.Kind,
				Name:      yamlId.Metadata.Name,
				Namespace: yamlId.Metadata.Namespace,
			}

			// If the resource has a delete annotation it is already setup for removal
			// and we simply save the resource for later filtering of delete candidates.
			if manifestHasDeleteAnno(yamlId) {
				logIt(fmt.Sprintf("%s has delete annotaion", resourceId))
				if _, ok := tbd[resourceId]; !ok {
					tbd[resourceId] = struct{}{}
				}
			} else {
				// save version for use in migrated APIVersion check
				resourceSource.Version = version

				ids[resourceId] = resourceSource
			}
		}
	}
	return ids, nil
}

func logIt(s string) {
	if releaseResourceDiffOpts.Log {
		fmt.Println(s)
	}
}

func splitApiVersion(apiVersion string) (api, version string) {
	split := strings.Split(apiVersion, "/")
	if len(split) != 2 {
		return apiVersion, ""
	}
	return split[0], split[1]
}

func getMinorRelease(release string) string {
	xyz := strings.Split(release, ".")
	if len(xyz) < 2 {
		return release
	}
	return xyz[0] + "." + xyz[1]
}

func splitYaml(resources []byte) ([][]byte, error) {

	dec := yaml.NewDecoder(bytes.NewReader(resources))

	var res [][]byte
	for {
		var value interface{}
		err := dec.Decode(&value)
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		valueBytes, err := yaml.Marshal(value)
		if err != nil {
			return nil, err
		}
		res = append(res, valueBytes)
	}
	return res, nil
}

func validKey(key ResourceYaml) bool {
	if len(key.APIVersion) == 0 || len(key.Kind) == 0 ||
		len(key.Metadata.Name) == 0 {
		return false
	}
	return true
}

// getMigratedApiVersion checks for resource's that used deprecated APIs and have been migrated to
// a different Group per https://kubernetes.io/blog/2019/07/18/api-deprecations-in-1-16.
func getMigratedApiVersion(id ResourceId, originalVersion string) (ResourceId, string) {
	newId := ResourceId{
		Kind:      id.Kind,
		Name:      id.Name,
		Namespace: id.Namespace,
	}
	var newVersion string
	switch id.Kind {
	case "NetworkPolicy":
		if id.Group == "extensions" && originalVersion == "v1beta1" {
			newId.Group = "networking.k8s.io"
			newVersion = "v1"
		}
	case "PodSecurityPolicy":
		if id.Group == "extensions" && originalVersion == "v1beta1" {
			newId.Group = "policy"
			newVersion = "v1beta1"
		}
	case "DaemonSet":
		if id.Group == "extensions" && originalVersion == "v1beta1" {
			newId.Group = "apps"
			newVersion = "v1"
		}
	case "Deployment":
		if id.Group == "extensions" && originalVersion == "v1beta1" {
			newId.Group = "apps"
			newVersion = "v1"
		}
	case "ReplicaSet":
		if id.Group == "extensions" && originalVersion == "v1beta1" {
			newId.Group = "apps"
			newVersion = "v1"
		}
	case "Ingress":
		if id.Group == "extensions" && originalVersion == "v1beta1" {
			newId.Group = "networking.k8s.io"
			newVersion = "v1beta1"
		}
	default:
		return newId, ""
	}
	return newId, newVersion
}

func checkIfOrphaned(resourceIds map[ResourceId]ResourceSource, currentOrphaned map[ResourceId]ResourceSource) {
	for k, v := range resourceIds {
		if _, ok := currentOrphaned[k]; !ok {

			if _, ok = targetResources[k]; ok {
				continue
			}

			// check if this resource's APIVersion has been migrated to later version
			newResourceId, newVersion := getMigratedApiVersion(k, v.Version)
			if newVersion != "" {
				logIt(fmt.Sprintf("%s migrated to %s", k.resourceWithVersionString(v.Version),
					newResourceId.resourceWithVersionString(newVersion)))
				// version check is not necessary
				if _, ok = targetResources[newResourceId]; ok {
					continue
				}
			}
			currentOrphaned[k] = v
		} else {
			setLastInRelease(v.LastInRelease, currentOrphaned, k)
		}
	}
}

func setLastInRelease(thisRelease string, currentOrphaned map[ResourceId]ResourceSource, key ResourceId) {
	if thisRelVal, err := strconv.ParseFloat(thisRelease, 32); err == nil {
		if val, err := strconv.ParseFloat(currentOrphaned[key].LastInRelease, 32); err == nil {
			if thisRelVal > val {
				currentOrphaned[key] = ResourceSource{
					Release:       currentOrphaned[key].Release,
					LastInRelease: thisRelease,
					YamlFileName:  currentOrphaned[key].YamlFileName,
				}
			}
		}
	}
}

func filterDeleteCandidates(resources map[ResourceId]ResourceSource, tbd map[ResourceId]struct{}) {
	for k := range tbd {
		if _, ok := resources[k]; ok {
			logIt(fmt.Sprintf("%s removed from candidate delete list", k))
			delete(resources, k)
		}
	}
}

func outputDeleteCandidates(resources map[ResourceId]ResourceSource) error {
	f, err := os.Create(releaseResourceDiffOpts.Results)
	if err != nil {
		return fmt.Errorf("Unable to create file %s; err=%v", releaseResourceDiffOpts.Results, err)
	}
	defer f.Close()
	for k, v := range resources {
		s := fmt.Sprintf("%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
			k.Group, k.Kind, k.Name, k.Namespace, v.Release, v.LastInRelease, v.YamlFileName)
		_, err := f.WriteString(s)
		if err != nil {
			return fmt.Errorf("Unable to write to file %s; err=%v", releaseResourceDiffOpts.Results, err)
		}
	}
	fmt.Printf("Results file %s created\n", releaseResourceDiffOpts.Results)
	return nil
}

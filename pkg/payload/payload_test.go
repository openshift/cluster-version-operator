package payload

import (
	"errors"
	"flag"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/klog/v2"

	configv1 "github.com/openshift/api/config/v1"
	imagev1 "github.com/openshift/api/image/v1"

	"github.com/openshift/cluster-version-operator/lib/capability"
	"github.com/openshift/library-go/pkg/manifest"
)

var architecture string

func init() {
	klog.InitFlags(flag.CommandLine)
	_ = flag.CommandLine.Lookup("v").Value.Set("2")
	_ = flag.CommandLine.Lookup("alsologtostderr").Value.Set("true")
	architecture = runtime.GOARCH
}

func TestLoadUpdate(t *testing.T) {
	type args struct {
		dir          string
		releaseImage string
	}
	tests := []struct {
		name    string
		args    args
		want    *Update
		wantErr bool
	}{
		{
			name: "ignore files without extensions, load metadata",
			args: args{
				dir:          filepath.Join("..", "cvo", "testdata", "payloadtest"),
				releaseImage: "image:1",
			},
			want: &Update{
				Release: configv1.Release{
					Version:  "1.0.0-abc",
					Image:    "image:1",
					URL:      configv1.URL("https://example.com/v1.0.0-abc"),
					Channels: []string{"channel-a", "channel-b", "channel-c"},
				},
				ImageRef: &imagev1.ImageStream{
					TypeMeta: metav1.TypeMeta{
						Kind:       "ImageStream",
						APIVersion: "image.openshift.io/v1",
					},
					ObjectMeta: metav1.ObjectMeta{
						Name: "1.0.0-abc",
					},
				},
				Architecture: architecture,
				ManifestHash: "DL-FFQ2Uem8=",
				Manifests: []manifest.Manifest{
					{
						OriginalFilename: "0000_10_a_file.json",
						Raw:              mustRead(filepath.Join("..", "cvo", "testdata", "payloadtest", "release-manifests", "0000_10_a_file.json")),
						GVK: schema.GroupVersionKind{
							Kind:    "Test",
							Version: "v1",
						},
						Obj: &unstructured.Unstructured{
							Object: map[string]interface{}{
								"kind":       "Test",
								"apiVersion": "v1",
								"metadata": map[string]interface{}{
									"name":        "file-json",
									"annotations": map[string]interface{}{"include.release.openshift.io/self-managed-high-availability": "true"},
								},
							},
						},
					},
					{
						OriginalFilename: "0000_10_a_file.yaml",
						Raw:              []byte(`{"apiVersion":"v1","kind":"Test","metadata":{"annotations":{"include.release.openshift.io/self-managed-high-availability":"true"},"name":"file-yaml"}}`),
						GVK: schema.GroupVersionKind{
							Kind:    "Test",
							Version: "v1",
						},
						Obj: &unstructured.Unstructured{
							Object: map[string]interface{}{
								"kind":       "Test",
								"apiVersion": "v1",
								"metadata": map[string]interface{}{
									"name":        "file-yaml",
									"annotations": map[string]interface{}{"include.release.openshift.io/self-managed-high-availability": "true"},
								},
							},
						},
					},
					{
						OriginalFilename: "0000_10_a_file.yml",
						Raw:              []byte(`{"apiVersion":"v1","kind":"Test","metadata":{"annotations":{"include.release.openshift.io/self-managed-high-availability":"true"},"name":"file-yml"}}`),
						GVK: schema.GroupVersionKind{
							Kind:    "Test",
							Version: "v1",
						},
						Obj: &unstructured.Unstructured{
							Object: map[string]interface{}{
								"kind":       "Test",
								"apiVersion": "v1",
								"metadata": map[string]interface{}{
									"name":        "file-yml",
									"annotations": map[string]interface{}{"include.release.openshift.io/self-managed-high-availability": "true"},
								},
							},
						},
					},
				},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := LoadUpdate(tt.args.dir, tt.args.releaseImage, "exclude-test", "", DefaultClusterProfile, nil)
			if (err != nil) != tt.wantErr {
				t.Errorf("loadUpdatePayload() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			// Manifest holds an unexported type of 'resourceID' with a field name of 'id'
			// DeepEqual fails, so here we use cmp.Diff to ignore just that field to avoid false positives
			if diff := cmp.Diff(tt.want, got, cmpopts.IgnoreFields(manifest.Manifest{}, "id")); diff != "" {
				t.Errorf("loadUpdatePayload() = %s", diff)
			}
		})
	}
}

func TestLoadUpdateArchitecture(t *testing.T) {
	type args struct {
		dir          string
		releaseImage string
	}
	tests := []struct {
		name    string
		args    args
		want    *Update
		wantErr bool
	}{
		{
			name: "set Multi architecture from payload metadata",
			args: args{
				dir:          filepath.Join("..", "cvo", "testdata", "payloadtestarch"),
				releaseImage: "image:1",
			},
			want: &Update{
				Release: configv1.Release{
					Version:      "1.0.0-abc",
					Image:        "image:1",
					Architecture: configv1.ClusterVersionArchitectureMulti,
					URL:          configv1.URL("https://example.com/v1.0.0-abc"),
					Channels:     []string{"channel-a", "channel-b", "channel-c"},
				},
				ImageRef: &imagev1.ImageStream{
					TypeMeta: metav1.TypeMeta{
						Kind:       "ImageStream",
						APIVersion: "image.openshift.io/v1",
					},
					ObjectMeta: metav1.ObjectMeta{
						Name: "1.0.0-abc",
					},
				},
				Architecture: "Multi",
				ManifestHash: "fvnVhLw92pE=",
				Manifests: []manifest.Manifest{
					{
						OriginalFilename: "0000_10_a_file.json",
						Raw:              mustRead(filepath.Join("..", "cvo", "testdata", "payloadtestarch", "release-manifests", "0000_10_a_file.json")),
						GVK: schema.GroupVersionKind{
							Kind:    "Test",
							Version: "v1",
						},
						Obj: &unstructured.Unstructured{
							Object: map[string]interface{}{
								"kind":       "Test",
								"apiVersion": "v1",
								"metadata": map[string]interface{}{
									"name":        "file",
									"annotations": map[string]interface{}{"include.release.openshift.io/self-managed-high-availability": "true"},
								},
							},
						},
					},
				},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := LoadUpdate(tt.args.dir, tt.args.releaseImage, "exclude-test", "", DefaultClusterProfile, nil)
			if (err != nil) != tt.wantErr {
				t.Errorf("loadUpdatePayload() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			// Manifest holds an unexported type of 'resourceID' with a field name of 'id'
			// DeepEqual fails, so here we use cmp.Diff to ignore just that field to avoid false positives
			if diff := cmp.Diff(tt.want, got, cmpopts.IgnoreFields(manifest.Manifest{}, "id")); diff != "" {
				t.Errorf("loadUpdatePayload() = %s", diff)
			}
		})
	}
}

func TestGetImplicitlyEnabledCapabilities(t *testing.T) {
	const testsPath = "../cvo/testdata/payloadcapabilitytest/"

	tests := []struct {
		name               string
		pathExt            string
		updateAnnotations  map[string]interface{}
		currentAnnotations map[string]interface{}
		capabilities       capability.ClusterCapabilities
		wantImplicit       []configv1.ClusterVersionCapability
		wantImplicitLen    int
	}{
		{
			name:    "basic",
			pathExt: "test1",
			capabilities: capability.ClusterCapabilities{
				Known:   map[configv1.ClusterVersionCapability]struct{}{"cap1": {}, "cap2": {}},
				Enabled: map[configv1.ClusterVersionCapability]struct{}{"cap1": {}},
			},
			wantImplicit: []configv1.ClusterVersionCapability{
				configv1.ClusterVersionCapability("cap2"),
			},
			wantImplicitLen: 1,
		},
		{
			name:    "basic with unknown cap",
			pathExt: "test1",
			capabilities: capability.ClusterCapabilities{
				Known:   map[configv1.ClusterVersionCapability]struct{}{"cap1": {}},
				Enabled: map[configv1.ClusterVersionCapability]struct{}{"cap1": {}},
			},
			wantImplicit: []configv1.ClusterVersionCapability{
				configv1.ClusterVersionCapability("cap2"),
			},
			wantImplicitLen: 1,
		},
		{
			name:    "different manifest",
			pathExt: "test2",
		},
		{
			name:    "current manifest not enabled",
			pathExt: "test3",
			capabilities: capability.ClusterCapabilities{
				Known:   map[configv1.ClusterVersionCapability]struct{}{"cap2": {}},
				Enabled: map[configv1.ClusterVersionCapability]struct{}{"cap2": {}},
			},
		},
		{
			name:    "new cap already enabled",
			pathExt: "test4",
			capabilities: capability.ClusterCapabilities{
				Known:   map[configv1.ClusterVersionCapability]struct{}{"cap1": {}, "cap2": {}},
				Enabled: map[configv1.ClusterVersionCapability]struct{}{"cap1": {}, "cap2": {}},
			},
		},
		{
			name:    "already implicitly enabled",
			pathExt: "test5",
			capabilities: capability.ClusterCapabilities{
				Known:             map[configv1.ClusterVersionCapability]struct{}{"cap1": {}, "cap2": {}},
				Enabled:           map[configv1.ClusterVersionCapability]struct{}{"cap1": {}},
				ImplicitlyEnabled: []configv1.ClusterVersionCapability{"cap2"},
			},
			wantImplicit: []configv1.ClusterVersionCapability{
				configv1.ClusterVersionCapability("cap2"),
			},
			wantImplicitLen: 1,
		},
		{
			name:    "only add cap once",
			pathExt: "test6",
			capabilities: capability.ClusterCapabilities{
				Known:   map[configv1.ClusterVersionCapability]struct{}{"cap1": {}, "cap2": {}},
				Enabled: map[configv1.ClusterVersionCapability]struct{}{"cap1": {}},
			},
			wantImplicit: []configv1.ClusterVersionCapability{
				configv1.ClusterVersionCapability("cap2"),
			},
			wantImplicitLen: 1,
		},
		{
			/*
				Grep manifest file data to understand results:
				$ grep cap ../cvo/testdata/payloadcapabilitytest/test7/current/fil*
				grep cap ../cvo/testdata/payloadcapabilitytest/test7/update/fil*
			*/

			name:    "complex",
			pathExt: "test7",
			capabilities: capability.ClusterCapabilities{
				Known: map[configv1.ClusterVersionCapability]struct{}{
					"cap1": {}, "cap2": {}, "cap3": {}, "cap4": {}, "cap5": {}, "cap6": {},
					"cap7": {}, "cap8": {}, "cap9": {}, "cap10": {}, "cap11": {}, "cap12": {},
					"cap13": {}, "cap14": {}, "cap15": {}, "cap16": {}, "cap17": {}, "cap18": {},
					"cap19": {}, "cap20": {}, "cap21": {}, "cap22": {}, "cap23": {}, "cap24": {},
					"cap111": {}, "cap112": {}, "cap113": {}, "cap114": {}, "cap115": {}, "cap116": {},
					"cap117": {}, "cap118": {}, "cap119": {}, "cap1111": {}, "cap1113": {}, "cap1115": {},
					"cap1110": {}, "cap1112": {}, "cap1114": {}, "cap1116": {},
				},
				Enabled: map[configv1.ClusterVersionCapability]struct{}{
					"cap1": {}, "cap2": {}, "cap3": {}, "cap4": {}, "cap5": {}, "cap6": {},
					"cap7": {}, "cap8": {}, "cap9": {}, "cap10": {}, "cap11": {}, "cap12": {},
					"cap13": {}, "cap14": {}, "cap15": {}, "cap16": {}, "cap17": {}, "cap18": {},
					"cap19": {}, "cap20": {}, "cap21": {}, "cap22": {}, "cap23": {}, "cap24": {},
				},
				ImplicitlyEnabled: []configv1.ClusterVersionCapability{
					configv1.ClusterVersionCapability("cap000"),
					configv1.ClusterVersionCapability("cap111"),
					configv1.ClusterVersionCapability("cap112"),
					configv1.ClusterVersionCapability("cap113"),
					configv1.ClusterVersionCapability("cap114"),
				},
			},
			wantImplicit: []configv1.ClusterVersionCapability{
				configv1.ClusterVersionCapability("cap000"),
				configv1.ClusterVersionCapability("cap111"),
				configv1.ClusterVersionCapability("cap112"),
				configv1.ClusterVersionCapability("cap113"),
				configv1.ClusterVersionCapability("cap114"),
				configv1.ClusterVersionCapability("cap115"),
				configv1.ClusterVersionCapability("cap116"),
				configv1.ClusterVersionCapability("cap117"),
				configv1.ClusterVersionCapability("cap118"),
				configv1.ClusterVersionCapability("cap119"),
				configv1.ClusterVersionCapability("cap1110"),
				configv1.ClusterVersionCapability("cap1111"),
				configv1.ClusterVersionCapability("cap1112"),
				configv1.ClusterVersionCapability("cap1113"),
				configv1.ClusterVersionCapability("cap1114"),
				configv1.ClusterVersionCapability("cap1115"),
				configv1.ClusterVersionCapability("cap1116"),
			},
			wantImplicitLen: 17,
		},
		{
			name:    "no update manifests",
			pathExt: "test8",
			capabilities: capability.ClusterCapabilities{
				Known:             map[configv1.ClusterVersionCapability]struct{}{"cap1": {}},
				Enabled:           map[configv1.ClusterVersionCapability]struct{}{"cap1": {}},
				ImplicitlyEnabled: []configv1.ClusterVersionCapability{"cap1"},
			},
			wantImplicit: []configv1.ClusterVersionCapability{
				configv1.ClusterVersionCapability("cap1"),
			},
			wantImplicitLen: 1,
		},
		{
			name:    "no current manifests",
			pathExt: "test9",
			capabilities: capability.ClusterCapabilities{
				Known:             map[configv1.ClusterVersionCapability]struct{}{"cap1": {}},
				Enabled:           map[configv1.ClusterVersionCapability]struct{}{"cap1": {}},
				ImplicitlyEnabled: []configv1.ClusterVersionCapability{"cap1"},
			},
			wantImplicit: []configv1.ClusterVersionCapability{
				configv1.ClusterVersionCapability("cap1"),
			},
			wantImplicitLen: 1,
		},
		{
			name:    "duplicate manifests",
			pathExt: "test10",
			capabilities: capability.ClusterCapabilities{
				Known:   map[configv1.ClusterVersionCapability]struct{}{"cap1": {}, "cap2": {}},
				Enabled: map[configv1.ClusterVersionCapability]struct{}{"cap1": {}},
			},
			wantImplicit: []configv1.ClusterVersionCapability{
				configv1.ClusterVersionCapability("cap2"),
			},
			wantImplicitLen: 1,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := testsPath + tt.pathExt + "/current"
			currentMans, err := readManifestFiles(path)
			if err != nil {
				t.Fatal(err)
			}
			path = testsPath + tt.pathExt + "/update"
			updateMans, err := readManifestFiles(path)
			if err != nil {
				t.Fatal(err)
			}
			// readManifestFiles does not allow dup manifests so hacking in here.
			if tt.pathExt == "test10" {
				updateMans = append(updateMans, updateMans[0])
			}
			caps := GetImplicitlyEnabledCapabilities(updateMans, currentMans, tt.capabilities)
			if len(caps) != tt.wantImplicitLen {
				t.Errorf("Incorrect number of implicitly enabled keys, wanted: %d. Implicitly enabled capabilities returned: %v", tt.wantImplicitLen, caps)
			}
			for _, wanted := range tt.wantImplicit {
				found := false
				for _, have := range caps {
					if wanted == have {
						found = true
						break
					}
				}
				if !found {
					t.Errorf("Missing implicitly enabled capability %q. Implicitly enabled capabilities returned : %v", wanted, caps)
				}
			}
		})
	}
}

func readManifestFiles(path string) ([]manifest.Manifest, error) {
	readFiles, err := os.ReadDir(path)

	// no dir for nil tests
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	} else if err != nil {
		return nil, err
	}
	files := []string{}
	for _, f := range readFiles {
		if !f.IsDir() {
			files = append(files, path+"/"+f.Name())
		}
	}
	if len(files) == 0 {
		return nil, nil
	}
	return manifest.ManifestsFromFiles(files)
}

func mustRead(path string) []byte {
	data, err := os.ReadFile(path)
	if err != nil {
		panic(err)
	}
	return data
}

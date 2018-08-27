package lib

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/ioutil"
	"os"
	"path/filepath"
	"sort"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	utilerrors "k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/apimachinery/pkg/util/yaml"
	"k8s.io/client-go/kubernetes/scheme"
)

// Manifest stores Kubernetes object in Raw from a file.
// It stores the GroupVersionKind for the manifest.
type Manifest struct {
	Raw []byte
	GVK schema.GroupVersionKind

	obj *unstructured.Unstructured
}

// UnmarshalJSON unmarshals bytes of single kubernetes object to Manifest.
func (m *Manifest) UnmarshalJSON(in []byte) error {
	if m == nil {
		return errors.New("Manifest: UnmarshalJSON on nil pointer")
	}

	// This happens when marshalling
	// <yaml>
	// ---	(this between two `---`)
	// ---
	// <yaml>
	if bytes.Equal(in, []byte("null")) {
		m.Raw = nil
		return nil
	}

	m.Raw = append(m.Raw[0:0], in...)
	udi, _, err := scheme.Codecs.UniversalDecoder().Decode(in, nil, &unstructured.Unstructured{})
	if err != nil {
		return fmt.Errorf("unable to decode manifest: %v", err)
	}
	ud, ok := udi.(*unstructured.Unstructured)
	if !ok {
		return fmt.Errorf("expected manifest to decode into *unstructured.Unstructured, got %T", ud)
	}

	m.GVK = ud.GroupVersionKind()
	m.obj = ud.DeepCopy()
	return nil
}

// Object returns underlying metav1.Object
func (m *Manifest) Object() metav1.Object { return m.obj }

const (
	// rootDirKey is used as key for the manifest files in root dir
	// passed to LoadManifests
	// It is set to `000` to give it more priority if the actor sorts
	// based on keys.
	rootDirKey = "000"
)

// LoadManifests loads manifest from disk.
//
// root/
//     manifest0
//     manifest1
//     00_subdir0/
//         manifest0
//         manifest1
//     01_subdir1/
//         manifest0
//         manifest1
// LoadManifests(<abs path to>/root):
// returns map
// 000: [manifest0, manifest1]
// 00_subdir0: [manifest0, manifest1]
// 01_subdir1: [manifest0, manifest1]
//
// It skips dirs that have not files.
// It only reads dir `p` and its direct subdirs.
func LoadManifests(p string) (map[string][]Manifest, error) {
	var out = make(map[string][]Manifest)

	fs, err := ioutil.ReadDir(p)
	if err != nil {
		return nil, err
	}

	// We want to accumulate all the errors, not returning at the
	// first error encountered when reading subdirs.
	var errs []error

	// load manifest files in p to rootDirKey
	ms, err := loadManifestsFromDir(p)
	if err != nil {
		errs = append(errs, fmt.Errorf("error loading from dir %s: %v", p, err))
	}
	if len(ms) > 0 {
		out[rootDirKey] = ms
	}

	// load manifests from subdirs to subdir-name
	for _, f := range fs {
		if !f.IsDir() {
			continue
		}
		path := filepath.Join(p, f.Name())
		ms, err := loadManifestsFromDir(path)
		if err != nil {
			errs = append(errs, fmt.Errorf("error loading from dir %s: %v", path, err))
			continue
		}
		if len(ms) > 0 {
			out[f.Name()] = ms
		}
	}

	agg := utilerrors.NewAggregate(errs)
	if agg != nil {
		return nil, errors.New(agg.Error())
	}
	return out, nil
}

// loadManifestsFromDir only returns files. not subdirs are traversed.
// returns manifests in increasing order of their filename.
func loadManifestsFromDir(dir string) ([]Manifest, error) {
	var manifests []Manifest
	fs, err := ioutil.ReadDir(dir)
	if err != nil {
		return nil, err
	}

	// ensure sorted.
	sort.Slice(fs, func(i, j int) bool {
		return fs[i].Name() < fs[j].Name()
	})

	var errs []error
	for _, f := range fs {
		if f.IsDir() {
			continue
		}

		path := filepath.Join(dir, f.Name())
		file, err := os.Open(path)
		if err != nil {
			errs = append(errs, fmt.Errorf("error opening %s: %v", path, err))
			continue
		}
		defer file.Close()

		ms, err := parseManifests(file)
		if err != nil {
			errs = append(errs, fmt.Errorf("error parsing %s: %v", path, err))
			continue
		}
		manifests = append(manifests, ms...)
	}

	agg := utilerrors.NewAggregate(errs)
	if agg != nil {
		return nil, fmt.Errorf("error loading manifests from %q: %v", dir, agg.Error())
	}

	return manifests, nil
}

// parseManifests parses a YAML or JSON document that may contain one or more
// kubernetes resources.
func parseManifests(r io.Reader) ([]Manifest, error) {
	d := yaml.NewYAMLOrJSONDecoder(r, 1024)
	var manifests []Manifest
	for {
		m := Manifest{}
		if err := d.Decode(&m); err != nil {
			if err == io.EOF {
				return manifests, nil
			}
			return manifests, fmt.Errorf("error parsing: %v", err)
		}
		m.Raw = bytes.TrimSpace(m.Raw)
		if len(m.Raw) == 0 || bytes.Equal(m.Raw, []byte("null")) {
			continue
		}
		manifests = append(manifests, m)
	}
}

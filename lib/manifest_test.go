package lib

import (
	"io/ioutil"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/davecgh/go-spew/spew"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

func TestParseManifests(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want []Manifest
	}{{
		name: "ingress",
		raw: `
apiVersion: extensions/v1beta1
kind: Ingress
metadata:
  name: test-ingress
  namespace: test-namespace
spec:
  rules:
  - http:
      paths:
      - path: /testpath
        backend:
          serviceName: test
          servicePort: 80
`,
		want: []Manifest{{
			Raw: []byte(`{"apiVersion":"extensions/v1beta1","kind":"Ingress","metadata":{"name":"test-ingress","namespace":"test-namespace"},"spec":{"rules":[{"http":{"paths":[{"backend":{"serviceName":"test","servicePort":80},"path":"/testpath"}]}}]}}`),
			GVK: schema.GroupVersionKind{Group: "extensions", Version: "v1beta1", Kind: "Ingress"},
		}},
	}, {
		name: "configmap",
		raw: `
apiVersion: v1
kind: ConfigMap
metadata:
  name: a-config
  namespace: default
data:
  color: "red"
  multi-line: |
    hello world
    how are you?
`,
		want: []Manifest{{
			Raw: []byte(`{"apiVersion":"v1","data":{"color":"red","multi-line":"hello world\nhow are you?\n"},"kind":"ConfigMap","metadata":{"name":"a-config","namespace":"default"}}`),
			GVK: schema.GroupVersionKind{Group: "", Version: "v1", Kind: "ConfigMap"},
		}},
	}, {
		name: "two-resources",
		raw: `
apiVersion: extensions/v1beta1
kind: Ingress
metadata:
  name: test-ingress
  namespace: test-namespace
spec:
  rules:
  - http:
      paths:
      - path: /testpath
        backend:
          serviceName: test
          servicePort: 80
---
apiVersion: v1
kind: ConfigMap
metadata:
  name: a-config
  namespace: default
data:
  color: "red"
  multi-line: |
    hello world
    how are you?
`,
		want: []Manifest{{
			Raw: []byte(`{"apiVersion":"extensions/v1beta1","kind":"Ingress","metadata":{"name":"test-ingress","namespace":"test-namespace"},"spec":{"rules":[{"http":{"paths":[{"backend":{"serviceName":"test","servicePort":80},"path":"/testpath"}]}}]}}`),
			GVK: schema.GroupVersionKind{Group: "extensions", Version: "v1beta1", Kind: "Ingress"},
		}, {
			Raw: []byte(`{"apiVersion":"v1","data":{"color":"red","multi-line":"hello world\nhow are you?\n"},"kind":"ConfigMap","metadata":{"name":"a-config","namespace":"default"}}`),
			GVK: schema.GroupVersionKind{Group: "", Version: "v1", Kind: "ConfigMap"},
		}},
	}, {
		name: "two-resources-with-empty",
		raw: `
---
apiVersion: extensions/v1beta1
kind: Ingress
metadata:
  name: test-ingress
  namespace: test-namespace
spec:
  rules:
  - http:
      paths:
      - path: /testpath
        backend:
          serviceName: test
          servicePort: 80
---
---
apiVersion: v1
kind: ConfigMap
metadata:
  name: a-config
  namespace: default
data:
  color: "red"
  multi-line: |
    hello world
    how are you?
---
`,
		want: []Manifest{{
			Raw: []byte(`{"apiVersion":"extensions/v1beta1","kind":"Ingress","metadata":{"name":"test-ingress","namespace":"test-namespace"},"spec":{"rules":[{"http":{"paths":[{"backend":{"serviceName":"test","servicePort":80},"path":"/testpath"}]}}]}}`),
			GVK: schema.GroupVersionKind{Group: "extensions", Version: "v1beta1", Kind: "Ingress"},
		}, {
			Raw: []byte(`{"apiVersion":"v1","data":{"color":"red","multi-line":"hello world\nhow are you?\n"},"kind":"ConfigMap","metadata":{"name":"a-config","namespace":"default"}}`),
			GVK: schema.GroupVersionKind{Group: "", Version: "v1", Kind: "ConfigMap"},
		}},
	}}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := parseManifests(strings.NewReader(test.raw))
			if err != nil {
				t.Fatalf("failed to parse manifest: %v", err)
			}

			for i := range got {
				got[i].obj = nil
			}

			if !reflect.DeepEqual(got, test.want) {
				t.Fatalf("mismatch found")
			}
		})
	}

}

func TestLoadManifestsFromDir(t *testing.T) {
	tests := []struct {
		name string
		fs   []dir
		want []Manifest
	}{{
		name: "no-files",
		fs: []dir{{
			name: "a",
		}, {
			name: "a/00_dir",
		}, {
			name: "a/01_dir",
		}},
		want: nil,
	}, {
		name: "all-files",
		fs: []dir{{
			name: "a",
			files: []file{{
				name: "f0",
				contents: `
apiVersion: extensions/v1beta1
kind: Ingress
metadata:
  name: test-ingress
  namespace: test-namespace
spec:
  rules:
  - http:
      paths:
      - path: /testpath
        backend:
          serviceName: test
          servicePort: 80
`,
			}, {
				name: "f1",
				contents: `
apiVersion: v1
kind: ConfigMap
metadata:
  name: a-config
  namespace: default
data:
  color: "red"
  multi-line: |
    hello world
    how are you?
`,
			}},
		}},
		want: []Manifest{{
			GVK: schema.GroupVersionKind{Group: "extensions", Version: "v1beta1", Kind: "Ingress"},
		}, {
			GVK: schema.GroupVersionKind{Group: "", Version: "v1", Kind: "ConfigMap"},
		}},
	}, {
		name: "files-and-subdirs",
		fs: []dir{{
			name: "a",
			files: []file{{
				name: "f0",
				contents: `
apiVersion: extensions/v1beta1
kind: Ingress
metadata:
  name: test-ingress
  namespace: test-namespace
spec:
  rules:
  - http:
      paths:
      - path: /testpath
        backend:
          serviceName: test
          servicePort: 80
`,
			}, {
				name: "f1",
				contents: `
apiVersion: v1
kind: ConfigMap
metadata:
  name: a-config
  namespace: default
data:
  color: "red"
  multi-line: |
    hello world
    how are you?
`,
			}},
		}, {
			name: "a/00_dir",
			files: []file{{
				name: "f0",
				contents: `
apiVersion: extensions/v1beta1
kind: Ingress
metadata:
  name: test-ingress
  namespace: test-namespace
spec:
  rules:
  - http:
      paths:
      - path: /testpath
        backend:
          serviceName: test
          servicePort: 80
`,
			}, {
				name: "f1",
				contents: `
apiVersion: v1
kind: ConfigMap
metadata:
  name: a-config
  namespace: default
data:
  color: "red"
  multi-line: |
    hello world
    how are you?
`,
			}},
		}, {
			name: "a/01_dir",
			files: []file{{
				name: "f0",
				contents: `
apiVersion: extensions/v1beta1
kind: Ingress
metadata:
  name: test-ingress
  namespace: test-namespace
spec:
  rules:
  - http:
      paths:
      - path: /testpath
        backend:
          serviceName: test
          servicePort: 80
`,
			}, {
				name: "f1",
				contents: `
apiVersion: v1
kind: ConfigMap
metadata:
  name: a-config
  namespace: default
data:
  color: "red"
  multi-line: |
    hello world
    how are you?
`,
			}},
		}},
		want: []Manifest{{
			GVK: schema.GroupVersionKind{Group: "extensions", Version: "v1beta1", Kind: "Ingress"},
		}, {
			GVK: schema.GroupVersionKind{Group: "", Version: "v1", Kind: "ConfigMap"},
		}},
	}}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			tmpdir, cleanup := setupTestFS(t, test.fs)
			defer func() {
				if err := cleanup(); err != nil {
					t.Logf("error cleaning %q", tmpdir)
				}
			}()

			got, err := loadManifestsFromDir(filepath.Join(tmpdir, "a"))
			if err != nil {
				t.Fatal(err)
			}
			for i := range got {
				got[i].Raw = nil
				got[i].obj = nil
			}
			if !reflect.DeepEqual(got, test.want) {
				t.Fatalf("mismatch \ngot: %s \nwant: %s", spew.Sdump(got), spew.Sdump(test.want))
			}
		})
	}
}

func TestLoadManifests(t *testing.T) {
	tests := []struct {
		name string
		fs   []dir

		wantkeys []string
		wantlen  []int
	}{{
		name: "empty-root",
		fs: []dir{{
			name: "a",
		}, {
			name: "a/00_dir",
			files: []file{{
				name: "f0",
				contents: `
apiVersion: v1
kind: ConfigMap
metadata:
  name: a-config
  namespace: default
data:
  color: "red"
  multi-line: |
    hello world
    how are you?
`,
			}},
		}, {
			name: "a/01_dir",
			files: []file{{
				name: "f0",
				contents: `
apiVersion: v1
kind: ConfigMap
metadata:
  name: a-config
  namespace: default
data:
  color: "red"
  multi-line: |
    hello world
    how are you?
`,
			}},
		}},
		wantkeys: []string{"00_dir", "01_dir"},
		wantlen:  []int{1, 1},
	}, {
		name: "non-empty-root",
		fs: []dir{{
			name: "a",
			files: []file{{
				name: "f0",
				contents: `
apiVersion: v1
kind: ConfigMap
metadata:
  name: a-config
  namespace: default
data:
  color: "red"
  multi-line: |
    hello world
    how are you?
`,
			}},
		}, {
			name: "a/00_dir",
			files: []file{{
				name: "f0",
				contents: `
apiVersion: v1
kind: ConfigMap
metadata:
  name: a-config
  namespace: default
data:
  color: "red"
  multi-line: |
    hello world
    how are you?
`,
			}},
		}, {
			name: "a/01_dir",
			files: []file{{
				name: "f0",
				contents: `
apiVersion: v1
kind: ConfigMap
metadata:
  name: a-config
  namespace: default
data:
  color: "red"
  multi-line: |
    hello world
    how are you?
`,
			}},
		}},
		wantkeys: []string{"000", "00_dir", "01_dir"},
		wantlen:  []int{1, 1, 1},
	}, {
		name: "empty-sub-dir",
		fs: []dir{{
			name: "a",
			files: []file{{
				name: "f0",
				contents: `
apiVersion: v1
kind: ConfigMap
metadata:
  name: a-config
  namespace: default
data:
  color: "red"
  multi-line: |
    hello world
    how are you?
`,
			}},
		}, {
			name: "a/00_dir",
			files: []file{{
				name: "f0",
				contents: `
apiVersion: v1
kind: ConfigMap
metadata:
  name: a-config
  namespace: default
data:
  color: "red"
  multi-line: |
    hello world
    how are you?
`,
			}},
		}, {
			name: "a/01_dir",
		}},
		wantkeys: []string{"000", "00_dir"},
		wantlen:  []int{1, 1},
	}}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			tmpdir, cleanup := setupTestFS(t, test.fs)
			defer func() {
				if err := cleanup(); err != nil {
					t.Logf("error cleaning %q", tmpdir)
				}
			}()

			got, err := LoadManifests(filepath.Join(tmpdir, "a"))
			if err != nil {
				t.Fatal(err)
			}

			gotkeys := []string{}
			for k := range got {
				gotkeys = append(gotkeys, k)
				for j := range got[k] {
					(got[k])[j].Raw = nil
				}
			}
			sort.Strings(gotkeys)
			gotlen := []int{}
			for _, k := range gotkeys {
				gotlen = append(gotlen, len(got[k]))
			}
			if !reflect.DeepEqual(test.wantkeys, gotkeys) {
				t.Fatalf("mismatch \ngot: %s \nwant: %s", spew.Sdump(gotkeys), spew.Sdump(test.wantkeys))
			}
			if !reflect.DeepEqual(test.wantlen, gotlen) {
				t.Fatalf("mismatch \ngot: %s \nwant: %s", spew.Sdump(gotlen), spew.Sdump(test.wantlen))
			}
		})
	}
}

type file struct {
	name     string
	contents string
}

type dir struct {
	name  string
	files []file
}

// setupTestFS returns path of the tmp dir created and cleanup function.
func setupTestFS(t *testing.T, dirs []dir) (string, func() error) {
	root, err := ioutil.TempDir("", "test")
	if err != nil {
		t.Fatal(err)
	}
	for _, dir := range dirs {
		dpath := filepath.Join(root, dir.name)
		if err := os.MkdirAll(dpath, 0755); err != nil {
			t.Fatal(err)
		}
		for _, file := range dir.files {
			path := filepath.Join(dpath, file.name)
			ioutil.WriteFile(path, []byte(file.contents), 0755)
		}
	}
	cleanup := func() error {
		return os.RemoveAll(root)
	}
	return root, cleanup
}

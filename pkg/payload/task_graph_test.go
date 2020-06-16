package payload

import (
	"context"
	"fmt"
	"math/rand"
	"os"
	"reflect"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/diff"

	"github.com/openshift/cluster-version-operator/lib"
)

func Test_TaskGraph_Split(t *testing.T) {
	var (
		pod = schema.GroupVersionKind{Kind: "Pod", Version: "v1"}
		job = schema.GroupVersionKind{Kind: "Job", Version: "v1", Group: "batch"}
	)
	tasks := func(gvks ...schema.GroupVersionKind) []*Task {
		var arr []*Task
		for _, gvk := range gvks {
			arr = append(arr, &Task{Manifest: &lib.Manifest{GVK: gvk}})
		}
		return arr
	}
	tests := []struct {
		name   string
		nodes  []*TaskNode
		onFn   func(task *Task) bool
		expect []*TaskNode
	}{
		{
			nodes:  []*TaskNode{},
			onFn:   SplitOnJobs,
			expect: []*TaskNode{},
		},
		{
			nodes: []*TaskNode{
				{Tasks: tasks(pod)},
			},
			onFn: SplitOnJobs,
			expect: []*TaskNode{
				{Tasks: tasks(pod)},
			},
		},
		{
			name: "split right",
			nodes: []*TaskNode{
				{Tasks: tasks(job, pod)},
			},
			onFn: SplitOnJobs,
			expect: []*TaskNode{
				{Tasks: tasks(job), Out: []int{1}},
				{Tasks: tasks(pod), In: []int{0}},
			},
		},
		{
			name: "split left",
			nodes: []*TaskNode{
				{Tasks: tasks(pod, job)},
			},
			onFn: SplitOnJobs,
			expect: []*TaskNode{
				{Tasks: tasks(job), In: []int{1}},
				{Tasks: tasks(pod), Out: []int{0}},
			},
		},
		{
			name: "interior",
			nodes: []*TaskNode{
				{Tasks: tasks(pod, pod, job, pod)},
			},
			onFn: SplitOnJobs,
			expect: []*TaskNode{
				{Tasks: tasks(job), In: []int{1}, Out: []int{2}},
				{Tasks: tasks(pod, pod), Out: []int{0}},
				{In: []int{0}, Tasks: tasks(pod)},
			},
		},
		{
			name: "interspersed",
			nodes: []*TaskNode{
				{Tasks: tasks(pod, pod, job, pod, job, pod)},
			},
			onFn: SplitOnJobs,
			expect: []*TaskNode{
				{Tasks: tasks(job), In: []int{1}, Out: []int{3}},
				{Tasks: tasks(pod, pod), Out: []int{0}},
				{Tasks: tasks(job), In: []int{3}, Out: []int{4}},
				{Tasks: tasks(pod), In: []int{0}, Out: []int{2}},
				{In: []int{2}, Tasks: tasks(pod)},
			},
		},
		{
			name: "ends",
			nodes: []*TaskNode{
				{Tasks: tasks(job, pod, pod, job)},
			},
			onFn: SplitOnJobs,
			expect: []*TaskNode{
				{Tasks: tasks(job), Out: []int{2}},
				{Tasks: tasks(job), In: []int{2}},
				{Tasks: tasks(pod, pod), In: []int{0}, Out: []int{1}},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := &TaskGraph{
				Nodes: tt.nodes,
			}
			g.Split(tt.onFn)
			if !reflect.DeepEqual(g.Nodes, tt.expect) {
				t.Fatalf("unexpected:\n%s\n%s", (&TaskGraph{Nodes: tt.expect}).Tree(), g.Tree())
			}
		})
	}
}

func TestByNumberAndComponent(t *testing.T) {
	tasks := func(names ...string) []*Task {
		var arr []*Task
		for _, name := range names {
			arr = append(arr, &Task{Manifest: &lib.Manifest{OriginalFilename: name}})
		}
		return arr
	}
	tests := []struct {
		name  string
		tasks []*Task
		want  [][]*TaskNode
	}{
		{
			name:  "empty tasks",
			tasks: tasks(),
			want:  nil,
		},
		{
			name:  "no grouping possible",
			tasks: tasks("a"),
			want:  nil,
		},
		{
			name:  "no recognizable groups",
			tasks: tasks("a", "b", "c"),
			want: [][]*TaskNode{
				{
					&TaskNode{Tasks: tasks("a", "b", "c")},
				},
			},
		},
		{
			name:  "single grouped item",
			tasks: tasks("0000_01_x-y-z_file1"),
			want:  nil,
		},
		{
			name:  "multiple grouped items in single node",
			tasks: tasks("0000_01_x-y-z_file1", "0000_01_x-y-z_file2"),
			want: [][]*TaskNode{
				{
					&TaskNode{Tasks: tasks("0000_01_x-y-z_file1", "0000_01_x-y-z_file2")},
				},
			},
		},
		{
			tasks: tasks("a", "0000_01_x-y-z_file1", "c"),
			want: [][]*TaskNode{
				{
					&TaskNode{Tasks: tasks("a", "0000_01_x-y-z_file1", "c")},
				},
			},
		},
		{
			tasks: tasks("0000_01_x-y-z_file1", "0000_01_x-y-z_file2"),
			want: [][]*TaskNode{
				{
					&TaskNode{Tasks: tasks("0000_01_x-y-z_file1", "0000_01_x-y-z_file2")},
				},
			},
		},
		{
			tasks: tasks("0000_01_a-b-c_file1", "0000_01_x-y-z_file2"),
			want: [][]*TaskNode{
				{
					&TaskNode{Tasks: tasks("0000_01_a-b-c_file1")},
					&TaskNode{Tasks: tasks("0000_01_x-y-z_file2")},
				},
			},
		},
		{
			tasks: tasks(
				"0000_01_a-b-c_file1",
				"0000_01_x-y-z_file1",
				"0000_01_x-y-z_file2",
				"a",
				"0000_01_x-y-z_file2",
				"0000_01_x-y-z_file3",
			),
			want: [][]*TaskNode{
				{
					&TaskNode{Tasks: tasks(
						"0000_01_a-b-c_file1",
					)},
					&TaskNode{Tasks: tasks(
						"0000_01_x-y-z_file1",
						"0000_01_x-y-z_file2",
					)},
				},
				{
					&TaskNode{Tasks: tasks(
						"a",
						"0000_01_x-y-z_file2",
						"0000_01_x-y-z_file3",
					)},
				},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ByNumberAndComponent(tt.tasks); !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("%s", diff.ObjectReflectDiff(tt.want, got))
			}
		})
	}
}

func TestShiftOrder(t *testing.T) {
	tasks := func(names ...string) []*Task {
		var arr []*Task
		for _, name := range names {
			arr = append(arr, &Task{Manifest: &lib.Manifest{OriginalFilename: name}})
		}
		return arr
	}
	tests := []struct {
		name   string
		step   int
		stride int
		tasks  [][]*TaskNode
		want   [][]*TaskNode
	}{
		{
			step:   0,
			stride: 8,
			tasks: [][]*TaskNode{
				{
					&TaskNode{Tasks: tasks("0000_01_x-y-z_file1")},
					&TaskNode{Tasks: tasks("0000_02_x-y-z_file1")},
				},
			},
			want: [][]*TaskNode{
				{
					&TaskNode{Tasks: tasks("0000_01_x-y-z_file1")},
					&TaskNode{Tasks: tasks("0000_02_x-y-z_file1")},
				},
			},
		},
		{
			step:   1,
			stride: 8,
			tasks: [][]*TaskNode{
				{
					&TaskNode{Tasks: tasks("0000_01_x-y-z_file1")},
					&TaskNode{Tasks: tasks("0000_02_x-y-z_file1")},
				},
			},
			want: [][]*TaskNode{
				{
					&TaskNode{Tasks: tasks("0000_02_x-y-z_file1")},
					&TaskNode{Tasks: tasks("0000_01_x-y-z_file1")},
				},
			},
		},
		{
			step:   2,
			stride: 8,
			tasks: [][]*TaskNode{
				{
					&TaskNode{Tasks: tasks("0000_01_x-y-z_file1")},
					&TaskNode{Tasks: tasks("0000_02_x-y-z_file1")},
				},
			},
			want: [][]*TaskNode{
				{
					&TaskNode{Tasks: tasks("0000_01_x-y-z_file1")},
					&TaskNode{Tasks: tasks("0000_02_x-y-z_file1")},
				},
			},
		},
		{
			step:   1,
			stride: 2,
			tasks: [][]*TaskNode{
				{
					&TaskNode{Tasks: tasks("0000_01_x-y-z_file1")},
					&TaskNode{Tasks: tasks("0000_02_x-y-z_file1")},
					&TaskNode{Tasks: tasks("0000_03_x-y-z_file1")},
				},
			},
			want: [][]*TaskNode{
				{
					&TaskNode{Tasks: tasks("0000_02_x-y-z_file1")},
					&TaskNode{Tasks: tasks("0000_03_x-y-z_file1")},
					&TaskNode{Tasks: tasks("0000_01_x-y-z_file1")},
				},
			},
		},
		{
			step:   2,
			stride: 3,
			tasks: [][]*TaskNode{
				{
					&TaskNode{Tasks: tasks("0000_01_x-y-z_file1")},
					&TaskNode{Tasks: tasks("0000_02_x-y-z_file1")},
					&TaskNode{Tasks: tasks("0000_03_x-y-z_file1")},
				},
			},
			want: [][]*TaskNode{
				{
					&TaskNode{Tasks: tasks("0000_03_x-y-z_file1")},
					&TaskNode{Tasks: tasks("0000_01_x-y-z_file1")},
					&TaskNode{Tasks: tasks("0000_02_x-y-z_file1")},
				},
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fn := func([]*Task) [][]*TaskNode {
				return test.tasks
			}
			if out := ShiftOrder(fn, test.step, test.stride)(nil); !reflect.DeepEqual(test.want, out) {
				t.Errorf("%s", diff.ObjectReflectDiff(test.want, out))
			}
		})
	}
}

func TestFlattenByNumberAndComponent(t *testing.T) {
	tasks := func(names ...string) []*Task {
		var arr []*Task
		for _, name := range names {
			arr = append(arr, &Task{Manifest: &lib.Manifest{OriginalFilename: name}})
		}
		return arr
	}
	tests := []struct {
		name  string
		tasks []*Task
		want  [][]*TaskNode
	}{
		{
			name:  "empty tasks",
			tasks: tasks(),
			want:  nil,
		},
		{
			name:  "no grouping possible",
			tasks: tasks("a"),
			want:  nil,
		},
		{
			name:  "no recognizable groups",
			tasks: tasks("a", "b", "c"),
			want: [][]*TaskNode{
				{
					&TaskNode{Tasks: tasks("a")},
					&TaskNode{Tasks: tasks("b")},
					&TaskNode{Tasks: tasks("c")},
				},
			},
		},
		{
			name:  "single grouped item",
			tasks: tasks("0000_01_x-y-z_file1"),
			want:  nil,
		},
		{
			name:  "multiple grouped items in single node",
			tasks: tasks("0000_01_x-y-z_file1", "0000_01_x-y-z_file2"),
			want: [][]*TaskNode{
				{
					&TaskNode{Tasks: tasks("0000_01_x-y-z_file1", "0000_01_x-y-z_file2")},
				},
			},
		},
		{
			tasks: tasks("a", "0000_01_x-y-z_file1", "c"),
			want: [][]*TaskNode{
				{
					&TaskNode{Tasks: tasks("a")},
					&TaskNode{Tasks: tasks("0000_01_x-y-z_file1")},
					&TaskNode{Tasks: tasks("c")},
				},
			},
		},
		{
			tasks: tasks("0000_01_x-y-z_file1", "0000_01_x-y-z_file2"),
			want: [][]*TaskNode{
				{
					&TaskNode{Tasks: tasks("0000_01_x-y-z_file1", "0000_01_x-y-z_file2")},
				},
			},
		},
		{
			tasks: tasks("0000_01_a-b-c_file1", "0000_01_x-y-z_file2"),
			want: [][]*TaskNode{
				{
					&TaskNode{Tasks: tasks("0000_01_a-b-c_file1")},
					&TaskNode{Tasks: tasks("0000_01_x-y-z_file2")},
				},
			},
		},
		{
			tasks: tasks(
				"0000_01_a-b-c_file1",
				"0000_01_x-y-z_file1",
				"0000_01_x-y-z_file2",
				"a",
				"0000_01_x-y-z_file2",
				"0000_01_x-y-z_file3",
			),
			want: [][]*TaskNode{
				{
					&TaskNode{Tasks: tasks(
						"0000_01_a-b-c_file1",
					)},
					&TaskNode{Tasks: tasks(
						"0000_01_x-y-z_file1",
						"0000_01_x-y-z_file2",
					)},
					&TaskNode{Tasks: tasks(
						"a",
					)},
					&TaskNode{Tasks: tasks(
						"0000_01_x-y-z_file2",
						"0000_01_x-y-z_file3",
					)},
				},
			},
		},
		{
			tasks: tasks(
				"0000_01_a-b-c_file1",
				"0000_01_x-y-z_file1",
				"0000_01_x-y-z_file2",
				"0000_02_x-y-z_file2",
				"0000_02_x-y-z_file3",
			),
			want: [][]*TaskNode{
				{
					&TaskNode{Tasks: tasks(
						"0000_01_a-b-c_file1",
					)},
					&TaskNode{Tasks: tasks(
						"0000_01_x-y-z_file1",
						"0000_01_x-y-z_file2",
					)},
					&TaskNode{Tasks: tasks(
						"0000_02_x-y-z_file2",
						"0000_02_x-y-z_file3",
					)},
				},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := FlattenByNumberAndComponent(tt.tasks); !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("%s", diff.ObjectReflectDiff(tt.want, got))
			}
		})
	}
}

func Test_TaskGraph_real(t *testing.T) {
	path := os.Getenv("TEST_GRAPH_PATH")
	if len(path) == 0 {
		t.Skip("TEST_GRAPH_PATH unset")
	}
	p, err := LoadUpgrade(path, "arbitrary/image:1", "")
	if err != nil {
		t.Fatal(err)
	}
	var tasks []*Task
	for i := range p.Manifests {
		tasks = append(tasks, &Task{
			Manifest: &p.Manifests[i],
		})
	}
	g := NewTaskGraph(tasks)
	g.Split(SplitOnJobs)
	g.Parallelize(ByNumberAndComponent)
	t.Logf("\n%s", g.Tree())
	t.Logf("original depth: %d", len(tasks))
}

func Test_TaskGraph_example(t *testing.T) {
	pod := func(name string) *Task {
		return &Task{
			Manifest: &lib.Manifest{
				GVK:              schema.GroupVersionKind{Kind: "Pod", Version: "v1"},
				OriginalFilename: name,
			},
		}
	}
	job := func(name string) *Task {
		return &Task{
			Manifest: &lib.Manifest{
				GVK:              schema.GroupVersionKind{Kind: "Job", Version: "v1", Group: "batch"},
				OriginalFilename: name,
			},
		}
	}
	tests := []struct {
		name   string
		tasks  []*Task
		expect *TaskGraph
	}{
		{
			tasks: []*Task{pod("a"), job("0000_50_a_0")},
			expect: &TaskGraph{
				Nodes: []*TaskNode{
					{Tasks: []*Task{job("0000_50_a_0")}, In: []int{1}},
					{Tasks: []*Task{pod("a")}, Out: []int{0}},
				},
			},
		},
		{
			tasks: []*Task{
				pod("a"),
				job("0000_50_a_0"),
				pod("0000_50_a_1"),
				pod("0000_50_a_2"),
			},
			expect: &TaskGraph{
				Nodes: []*TaskNode{
					{Tasks: []*Task{job("0000_50_a_0")}, In: []int{1}, Out: []int{2}},
					{Tasks: []*Task{pod("a")}, Out: []int{0}},
					{Tasks: []*Task{pod("0000_50_a_1"), pod("0000_50_a_2")}, In: []int{0}},
				},
			},
		},
		{
			tasks: []*Task{
				job("a"),
				pod("0000_50_a_0"),
				pod("0000_50_b_0"),
				pod("0000_50_b_1"),
			},
			expect: &TaskGraph{
				Nodes: []*TaskNode{
					{Tasks: []*Task{job("a")}, Out: []int{1}},
					{In: []int{0}, Out: []int{2, 3}},
					{Tasks: []*Task{pod("0000_50_a_0")}, In: []int{1}},
					{Tasks: []*Task{pod("0000_50_b_0"), pod("0000_50_b_1")}, In: []int{1}},
				},
			},
		},
		{
			tasks: []*Task{
				job("a"),
				pod("0000_50_a_0"),
				pod("0000_50_b_0"),
				pod("0000_50_b_1"),
				pod("0000_50_c_0"),
				job("b"),
			},
			expect: &TaskGraph{
				Nodes: []*TaskNode{
					{Tasks: []*Task{job("a")}, Out: []int{2}},
					{Tasks: []*Task{job("b")}, In: []int{6}},
					{In: []int{0}, Out: []int{3, 4, 5}},
					{Tasks: []*Task{pod("0000_50_a_0")}, In: []int{2}, Out: []int{6}},
					{Tasks: []*Task{pod("0000_50_b_0"), pod("0000_50_b_1")}, In: []int{2}, Out: []int{6}},
					{Tasks: []*Task{pod("0000_50_c_0")}, In: []int{2}, Out: []int{6}},
					{In: []int{3, 4, 5}, Out: []int{1}},
				},
			},
		},
		{
			tasks: []*Task{
				pod("0000_07_a_0"),
				pod("0000_08_a_0"),
				pod("0000_09_a_0"),
				pod("0000_09_a_1"),
				pod("0000_09_b_0"),
				pod("0000_09_b_1"),
				pod("0000_10_a_0"),
				pod("0000_10_a_1"),
				pod("0000_11_a_0"),
				pod("0000_11_a_1"),
			},
			expect: &TaskGraph{
				Nodes: []*TaskNode{
					{Out: []int{1}},
					{Tasks: []*Task{pod("0000_07_a_0"), pod("0000_08_a_0")}, In: []int{0}, Out: []int{2, 3}},
					{Tasks: []*Task{pod("0000_09_a_0"), pod("0000_09_a_1")}, In: []int{1}, Out: []int{4}},
					{Tasks: []*Task{pod("0000_09_b_0"), pod("0000_09_b_1")}, In: []int{1}, Out: []int{4}},
					{Tasks: []*Task{pod("0000_10_a_0"), pod("0000_10_a_1"), pod("0000_11_a_0"), pod("0000_11_a_1")}, In: []int{2, 3}},
				},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewTaskGraph(tt.tasks)
			g.Split(SplitOnJobs)
			g.Parallelize(ByNumberAndComponent)
			if !reflect.DeepEqual(g, tt.expect) {
				t.Fatalf("unexpected:\n%s\n---\n%s", tt.expect.Tree(), g.Tree())
			}
		})
	}
}

func Test_TaskGraph_bulkAdd(t *testing.T) {
	tasks := func(names ...string) []*Task {
		var arr []*Task
		for _, name := range names {
			arr = append(arr, &Task{Manifest: &lib.Manifest{OriginalFilename: name}})
		}
		return arr
	}
	tests := []struct {
		name   string
		nodes  []*TaskNode
		add    []*TaskNode
		in     []int
		want   []int
		expect []*TaskNode
	}{
		{
			nodes: []*TaskNode{
				{Tasks: tasks("a", "b")},
			},
			add: []*TaskNode{
				{Tasks: tasks("c")},
				{Tasks: tasks("d")},
			},
			in:   []int{0},
			want: []int{1, 2},
			expect: []*TaskNode{
				{Tasks: tasks("a", "b"), Out: []int{1, 2}},
				{Tasks: tasks("c"), In: []int{0}},
				{Tasks: tasks("d"), In: []int{0}},
			},
		},
		{
			nodes: []*TaskNode{
				{Tasks: tasks("a", "b"), Out: []int{1}},
				{Tasks: tasks("e")},
			},
			add: []*TaskNode{
				{Tasks: tasks("c")},
				{Tasks: tasks("d")},
			},
			in:   []int{0},
			want: []int{2, 3},
			expect: []*TaskNode{
				{Tasks: tasks("a", "b"), Out: []int{1, 2, 3}},
				{Tasks: tasks("e")},
				{Tasks: tasks("c"), In: []int{0}},
				{Tasks: tasks("d"), In: []int{0}},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := &TaskGraph{
				Nodes: tt.nodes,
			}
			if got := g.bulkAdd(tt.add, tt.in); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("TaskGraph.bulkAdd() = %v, want %v", got, tt.want)
			}
			if !reflect.DeepEqual(tt.expect, g.Nodes) {
				t.Errorf("unexpected:\n%s\n---\n%s", (&TaskGraph{Nodes: tt.expect}).Tree(), g.Tree())
			}
		})
	}
}

type safeSlice struct {
	lock  sync.Mutex
	items []string
}

func (s *safeSlice) Add(item string) {
	s.lock.Lock()
	defer s.lock.Unlock()
	s.items = append(s.items, item)
}

func TestRunGraph(t *testing.T) {
	tasks := func(names ...string) []*Task {
		var arr []*Task
		for _, name := range names {
			manifest := &lib.Manifest{OriginalFilename: name}
			err := manifest.UnmarshalJSON([]byte(fmt.Sprintf(`
{
  "apiVersion": "v1",
  "kind": "ConfigMap",
  "metadata": {
    "name": "%s",
    "namespace": "default"
  }
}
`, name)))
			if err != nil {
				t.Fatalf("load %s: %v", name, err)
			}
			arr = append(arr, &Task{Manifest: manifest})
		}
		return arr
	}
	tests := []struct {
		name     string
		nodes    []*TaskNode
		parallel int
		sleep    time.Duration
		errorOn  func(t *testing.T, name string, ctx context.Context, cancelFn func()) error

		order      []string
		want       []string
		invariants func(t *testing.T, got []string)
		wantErrs   []string
	}{
		{
			name: "tasks executed in order",
			nodes: []*TaskNode{
				{Tasks: tasks("a", "b")},
			},
			order: []string{"a", "b"},
		},
		{
			name: "nodes executed after dependencies",
			nodes: []*TaskNode{
				{Tasks: tasks("c"), In: []int{3}},
				{Tasks: tasks("d", "e"), In: []int{3}},
				{Tasks: tasks("f"), In: []int{3}, Out: []int{4}},
				{Tasks: tasks("a", "b"), Out: []int{0, 1, 2}},
				{Tasks: tasks("g"), In: []int{2}},
			},
			want:     []string{"a", "b", "c", "d", "e", "f", "g"},
			sleep:    time.Millisecond,
			parallel: 2,
			invariants: func(t *testing.T, got []string) {
				for i := 0; i < len(got)-1; i++ {
					for j := i + 1; j < len(got); j++ {
						a, b := got[i], got[j]
						switch {
						case a == "b" && b == "a":
							t.Fatalf("%d and %d in: %v", i, j, got)
						case a == "e" && b == "d":
							t.Fatalf("%d and %d in: %v", i, j, got)
						case a != "a" && b == "b":
							t.Fatalf("%d and %d in: %v", i, j, got)
						case a == "g" && (b == "f" || b == "a" || b == "b"):
							t.Fatalf("%d and %d in: %v", i, j, got)
						}
					}
				}
			},
		},
		{
			name: "task error interrupts node processing",
			nodes: []*TaskNode{
				{Tasks: tasks("c"), In: []int{2}},
				{Tasks: tasks("d"), In: []int{2}, Out: []int{3}},
				{Tasks: tasks("a", "b"), Out: []int{0, 1}},
				{Tasks: tasks("e"), In: []int{1}},
			},
			sleep:    time.Millisecond,
			parallel: 2,
			errorOn: func(t *testing.T, name string, ctx context.Context, cancelFn func()) error {
				if name == "d" {
					return fmt.Errorf("error A")
				}
				return nil
			},
			want:     []string{"a", "b", "c"},
			wantErrs: []string{"error A"},
			invariants: func(t *testing.T, got []string) {
				for _, s := range got {
					if s == "e" {
						t.Fatalf("shouldn't have reached e")
					}
				}
			},
		},
		{
			name: "mid-task cancellation error interrupts node processing",
			nodes: []*TaskNode{
				{Tasks: tasks("c"), In: []int{2}},
				{Tasks: tasks("d"), In: []int{2}, Out: []int{3}},
				{Tasks: tasks("a", "b"), Out: []int{0, 1}},
				{Tasks: tasks("e"), In: []int{1}},
			},
			sleep:    time.Millisecond,
			parallel: 2,
			errorOn: func(t *testing.T, name string, ctx context.Context, cancelFn func()) error {
				if name == "d" {
					cancelFn()
					select {
					case <-time.After(time.Second):
						t.Fatalf("expected context")
					case <-ctx.Done():
						t.Logf("got canceled context")
						return ctx.Err()
					}
					return fmt.Errorf("error A")
				}
				return nil
			},
			want:     []string{"a", "b", "c"},
			wantErrs: []string{"context canceled"},
			invariants: func(t *testing.T, got []string) {
				for _, s := range got {
					if s == "e" {
						t.Fatalf("shouldn't have reached e")
					}
				}
			},
		},
		{
			name: "task errors in parallel nodes both reported",
			nodes: []*TaskNode{
				{Tasks: tasks("a"), Out: []int{1}},
				{Tasks: tasks("b"), In: []int{0}, Out: []int{2, 4, 8}},
				{Tasks: tasks("c1"), In: []int{1}, Out: []int{3}},
				{Tasks: tasks("c2"), In: []int{2}, Out: []int{7}},
				{Tasks: tasks("d1"), In: []int{1}, Out: []int{5}},
				{Tasks: tasks("d2"), In: []int{4}, Out: []int{6}},
				{Tasks: tasks("d3"), In: []int{5}, Out: []int{7}},
				{Tasks: tasks("e"), In: []int{3, 6}},
				{Tasks: tasks("f"), In: []int{1}},
			},
			sleep:    time.Millisecond,
			parallel: 2,
			errorOn: func(t *testing.T, name string, ctx context.Context, cancelFn func()) error {
				if name == "c1" {
					return fmt.Errorf("error - c1")
				}
				if name == "f" {
					return fmt.Errorf("error - f")
				}
				return nil
			},
			want:     []string{"a", "b", "d1", "d2", "d3"},
			wantErrs: []string{"error - c1", "error - f"},
		},
		{
			name: "cancelation without task errors is reported",
			nodes: []*TaskNode{
				{Tasks: tasks("a"), Out: []int{1}},
				{Tasks: tasks("b"), In: []int{0}},
			},
			sleep:    time.Millisecond,
			parallel: 1,
			errorOn: func(t *testing.T, name string, ctx context.Context, cancelFn func()) error {
				if name == "a" {
					cancelFn()
					time.Sleep(time.Second)
					return nil
				}
				t.Fatalf("task b should never run")
				return nil
			},
			want:     []string{"a"},
			wantErrs: []string{`1 incomplete task nodes, beginning with configmap "default/b" (0 of 0)`, "context canceled"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := &TaskGraph{
				Nodes: tt.nodes,
			}
			ctx, cancelFn := context.WithCancel(context.Background())
			defer cancelFn()
			var order safeSlice
			errs := RunGraph(ctx, g, tt.parallel, func(ctx context.Context, tasks []*Task) error {
				for _, task := range tasks {
					time.Sleep(tt.sleep * time.Duration(rand.Intn(4)))
					if tt.errorOn != nil {
						if err := tt.errorOn(t, task.Manifest.OriginalFilename, ctx, cancelFn); err != nil {
							return err
						}
					}
					order.Add(task.Manifest.OriginalFilename)
				}
				return nil
			})
			if tt.order != nil {
				if !reflect.DeepEqual(tt.order, order.items) {
					t.Fatal(diff.ObjectReflectDiff(tt.order, order.items))
				}
			}
			if tt.invariants != nil {
				tt.invariants(t, order.items)
			}
			if tt.want != nil {
				sort.Strings(tt.want)
				sort.Strings(order.items)
				if !reflect.DeepEqual(tt.want, order.items) {
					t.Fatal(diff.ObjectReflectDiff(tt.want, order.items))
				}
			}

			var messages []string
			for _, err := range errs {
				messages = append(messages, err.Error())
			}
			sort.Strings(messages)
			if len(messages) != len(tt.wantErrs) {
				t.Fatalf("unexpected error: %v", messages)
			}
			for i, want := range tt.wantErrs {
				if !strings.Contains(messages[i], want) {
					t.Errorf("error %d %q doesn't contain %q", i, messages[i], want)
				}
			}
		})
	}
}

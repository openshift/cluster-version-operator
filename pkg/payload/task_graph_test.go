package payload

import (
	"context"
	"fmt"
	"math/rand"
	"os"
	"reflect"
	"slices"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/openshift/library-go/pkg/manifest"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

func Test_TaskGraph_Split(t *testing.T) {
	var (
		pod = schema.GroupVersionKind{Kind: "Pod", Version: "v1"}
		job = schema.GroupVersionKind{Kind: "Job", Version: "v1", Group: "batch"}
	)
	tasks := func(gvks ...schema.GroupVersionKind) []*Task {
		var arr []*Task
		for _, gvk := range gvks {
			arr = append(arr, &Task{Manifest: &manifest.Manifest{GVK: gvk}})
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
			arr = append(arr, &Task{Manifest: &manifest.Manifest{OriginalFilename: name}})
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
				t.Fatalf("%s", cmp.Diff(tt.want, got))
			}
		})
	}
}

func TestShiftOrder(t *testing.T) {
	tasks := func(names ...string) []*Task {
		var arr []*Task
		for _, name := range names {
			arr = append(arr, &Task{Manifest: &manifest.Manifest{OriginalFilename: name}})
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
				t.Errorf("%s", cmp.Diff(test.want, out))
			}
		})
	}
}

func TestFlattenByNumberAndComponent(t *testing.T) {
	tasks := func(names ...string) []*Task {
		var arr []*Task
		for _, name := range names {
			arr = append(arr, &Task{Manifest: &manifest.Manifest{OriginalFilename: name}})
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
				t.Fatalf("%s", cmp.Diff(tt.want, got))
			}
		})
	}
}

func Test_TaskGraph_real(t *testing.T) {
	path := os.Getenv("TEST_GRAPH_PATH")
	if len(path) == 0 {
		t.Skip("TEST_GRAPH_PATH unset")
	}
	p, err := LoadUpdate(path, "arbitrary/image:1", "", "", DefaultClusterProfile, nil)
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
			Manifest: &manifest.Manifest{
				GVK:              schema.GroupVersionKind{Kind: "Pod", Version: "v1"},
				OriginalFilename: name,
			},
		}
	}
	job := func(name string) *Task {
		return &Task{
			Manifest: &manifest.Manifest{
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
			arr = append(arr, &Task{Manifest: &manifest.Manifest{OriginalFilename: name}})
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

type invariant func(t *testing.T, tasks []string)

// before checks that task a was performed before task b in the list of tasks, given both are present
// if one of them is missing, the invariant is ignored
func before(a, b string) invariant {
	return func(t *testing.T, tasks []string) {
		for _, task := range tasks {
			switch task {
			case a:
				return
			case b:
				t.Errorf("expected %s before %s in %s", a, b, tasks)
			}
		}
	}
}

// atIndex checks that the task at index i is equal to the given task.
func atIndex(i int, task string) invariant {
	return func(t *testing.T, tasks []string) {
		if i >= len(tasks) {
			t.Fatalf("index %d out of range", i)
		}
		if tasks[i] != task {
			t.Errorf("expected %s at index %d of %s", task, i, tasks)
		}
	}
}

// subseq checks that the given sequence of tasks is an exact subsequence of the tasks (not interleaved by other tasks).
func subseq(seq ...string) invariant {
	return func(t *testing.T, tasks []string) {
		for i := 0; i < len(tasks)-len(seq)+1; i++ {
			if diff := cmp.Diff(tasks[i:i+len(seq)], seq); diff == "" {
				return
			}
		}
		t.Errorf("missing subsequence %v in %v", seq, tasks)
	}
}

// exactly checks that the list of tasks is exactly the given list of tasks
func exactly(tasks ...string) invariant {
	return func(t *testing.T, got []string) {
		if diff := cmp.Diff(tasks, got); diff != "" {
			t.Errorf("exact sequence of tasks differs from expected (-want +got):\n%s", diff)
		}
	}
}

// permutation checks that the list of tasks is a permutation of the given list of tasks
func permutation(tasks ...string) invariant {
	return func(t *testing.T, got []string) {
		g := slices.Clone(got)
		sort.Strings(g)
		sort.Strings(tasks)

		if diff := cmp.Diff(tasks, g); diff != "" {
			t.Errorf("tasks differs from expected (-want +got):\n%s", diff)
		}
	}
}

type callbackFn func(t *testing.T, name string, ctx context.Context, cancelFn func()) error

func errorOut(err error) callbackFn {
	return func(t *testing.T, name string, ctx context.Context, cancelFn func()) error {
		return err
	}
}

func TestRunGraph(t *testing.T) {
	tasks := func(names ...string) []*Task {
		var arr []*Task
		for _, name := range names {
			m := &manifest.Manifest{OriginalFilename: name}
			if err := m.UnmarshalJSON(configMapJSON(name)); err != nil {
				t.Fatalf("load %s: %v", name, err)
			}
			arr = append(arr, &Task{Manifest: m})
		}
		return arr
	}

	tests := []struct {
		name  string
		nodes []*TaskNode
		// maxParallelism is a RunGraph parameter affecting how many nodes are processed in parallel
		maxParallelism int
		// sleep is a duration to sleep in each task to simulate work
		sleep time.Duration
		// callbacks allows providing a method to be called for a specific task, to simulate work, return error or trigger
		// cancellations. The key is the task name. Special key "*" can be used to provide a callback for any task that does
		// not have a specific one.
		callbacks map[string]callbackFn

		// list of functions to be called to check invariants on the list of tasks executed
		wantInvariants []invariant
		// errors is the list of error strings we expect to be returned by RunGraph
		wantErrors []string
	}{
		{
			name: "tasks executed in order in a single node",
			nodes: []*TaskNode{
				{Tasks: tasks("b", "a")},
			},
			wantInvariants: []invariant{
				exactly("b", "a"),
			},
		},
		{
			name: "nodes executed after dependencies when processed in parallel",
			nodes: []*TaskNode{
				// In: prerequisites (by index)
				// Out: dependents (by index)
				{Tasks: tasks("c"), In: []int{3}},
				{Tasks: tasks("d", "e"), In: []int{3}},
				{Tasks: tasks("f"), In: []int{3}, Out: []int{4}},
				{Tasks: tasks("a", "b"), Out: []int{0, 1, 2}},
				{Tasks: tasks("g"), In: []int{2}},
			},
			sleep:          time.Millisecond,
			maxParallelism: 2,
			wantInvariants: []invariant{
				permutation("a", "b", "c", "d", "e", "f", "g"),
				atIndex(0, "a"),
				subseq("a", "b"),
				before("b", "c"),
				before("b", "d"),
				before("b", "f"),
				before("d", "e"),
				before("f", "g"),
			},
		},
		{
			name: "task error interrupts node processing",
			nodes: []*TaskNode{
				// In: prerequisites (by index)
				// Out: dependents (by index)
				{Tasks: tasks("c"), In: []int{2}},
				{Tasks: tasks("d"), In: []int{2}, Out: []int{3}},
				{Tasks: tasks("a", "b"), Out: []int{0, 1}},
				{Tasks: tasks("e"), In: []int{1}},
			},
			sleep:          time.Millisecond,
			maxParallelism: 2,
			callbacks: map[string]callbackFn{
				"d": errorOut(fmt.Errorf("error A")),
			},
			wantInvariants: []invariant{
				exactly("a", "b", "c"),
			},
			wantErrors: []string{"error A"},
		},
		{
			name: "mid-task cancellation error interrupts node processing",
			nodes: []*TaskNode{
				// In: prerequisites (by index)
				// Out: dependents (by index)
				{Tasks: tasks("c"), In: []int{2}},
				{Tasks: tasks("d"), In: []int{2}, Out: []int{3}},
				{Tasks: tasks("a", "b"), Out: []int{0, 1}},
				{Tasks: tasks("e"), In: []int{1}},
			},
			sleep:          time.Millisecond,
			maxParallelism: 2,
			callbacks: map[string]callbackFn{
				"d": func(t *testing.T, name string, ctx context.Context, cancelFn func()) error {
					cancelFn()
					select {
					case <-time.After(time.Second):
						t.Fatalf("timed out waiting for context to be canceled as expected")
					case <-ctx.Done():
						t.Logf("context was canceled as expected")
						return ctx.Err()
					}
					return nil
				},
			},
			wantInvariants: []invariant{
				exactly("a", "b", "c"),
			},
			wantErrors: []string{"context canceled"},
		},
		{
			name: "mid-task cancellation with work in queue does not deadlock",
			nodes: []*TaskNode{
				{Tasks: tasks("a1", "a2", "a3")},
				{Tasks: tasks("b")},
			},
			sleep:          time.Millisecond,
			maxParallelism: 1,
			callbacks: map[string]callbackFn{
				"a2": func(t *testing.T, name string, ctx context.Context, cancelFn func()) error {
					if err := ctx.Err(); err != nil {
						return err
					}
					cancelFn()
					return nil
				},
				"*": func(t *testing.T, name string, ctx context.Context, cancelFn func()) error {
					return ctx.Err()
				},
			},
			wantInvariants: []invariant{
				exactly("a1", "a2"),
			},
			wantErrors: []string{"context canceled"},
		},
		{
			name: "task errors in parallel nodes both reported",
			nodes: []*TaskNode{
				// In: prerequisites (by index)
				// Out: dependents (by index)
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
			sleep:          time.Millisecond,
			maxParallelism: 2,
			callbacks: map[string]callbackFn{
				"c1": errorOut(fmt.Errorf("error - c1")),
				"f":  errorOut(fmt.Errorf("error - f")),
			},
			wantInvariants: []invariant{
				exactly("a", "b", "d1", "d2", "d3"),
			},
			wantErrors: []string{"error - c1", "error - f"},
		},
		{
			name: "cancellation without task errors is reported",
			nodes: []*TaskNode{
				// In: prerequisites (by index)
				// Out: dependents (by index)
				{Tasks: tasks("a"), Out: []int{1}},
				{Tasks: tasks("b"), In: []int{0}},
			},
			sleep:          time.Millisecond,
			maxParallelism: 1,
			callbacks: map[string]callbackFn{
				"a": func(t *testing.T, name string, ctx context.Context, cancelFn func()) error {
					cancelFn()
					return nil
				},
				"*": func(t *testing.T, name string, ctx context.Context, cancelFn func()) error {
					t.Fatalf("task %s should never run", name)
					return nil
				},
			},
			wantInvariants: []invariant{
				exactly("a"),
			},
			wantErrors: []string{`1 incomplete task nodes, beginning with configmap "default/b" (0 of 0)`, "context canceled"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := &TaskGraph{Nodes: tt.nodes}
			ctx, cancelFn := context.WithCancel(context.Background())
			defer cancelFn()
			var executed safeSlice
			errs := RunGraph(ctx, g, tt.maxParallelism, func(ctx context.Context, tasks []*Task) error {
				for _, task := range tasks {
					time.Sleep(tt.sleep * time.Duration(rand.Intn(4)))
					for _, callbackKey := range []string{task.Manifest.OriginalFilename, "*"} {
						if callback, ok := tt.callbacks[callbackKey]; ok {
							if err := callback(t, task.Manifest.OriginalFilename, ctx, cancelFn); err != nil {
								return err
							}
							break
						}
					}
					executed.Add(task.Manifest.OriginalFilename)
				}
				return nil
			})

			for _, invariant := range tt.wantInvariants {
				invariant(t, executed.items)
			}

			var messages []string
			for _, err := range errs {
				messages = append(messages, err.Error())
			}
			if len(messages) != len(tt.wantErrors) {
				t.Fatalf("unexpected error: %v", messages)
			}
			sort.Strings(messages)
			for i, want := range tt.wantErrors {
				if !strings.Contains(messages[i], want) {
					t.Errorf("error %d %q doesn't contain %q", i, messages[i], want)
				}
			}
		})
	}
}

func configMapJSON(name string) []byte {
	const cmTemplate = `
{
  "apiVersion": "v1",
  "kind": "ConfigMap",
  "metadata": {
    "name": "%s",
    "namespace": "default"
  }
}
`
	return []byte(fmt.Sprintf(cmTemplate, name))
}

package payload

import (
	"context"
	"fmt"
	"math/rand"
	"regexp"
	"strings"
	"sync"

	"k8s.io/apimachinery/pkg/runtime/schema"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/klog/v2"
)

// SplitOnJobs enforces the rule that any Job in the payload prevents reordering or parallelism (either before or after)
func SplitOnJobs(task *Task) bool {
	return task.Manifest.GVK == schema.GroupVersionKind{Kind: "Job", Version: "v1", Group: "batch"}
}

var reMatchPattern = regexp.MustCompile(`^0000_(\d+)_([a-zA-Z0-9]+(-[a-zA-Z0-9]+)*?)_`)

const (
	groupNumber    = 1
	groupComponent = 2
)

// ByNumberAndComponent creates parallelization for tasks whose original filenames are of the form
// 0000_NN_NAME_* - files that share 0000_NN_NAME_ are run in serial, but chunks of files that have
// the same 0000_NN but different NAME can be run in parallel. If the input is not sorted in an order
// such that 0000_NN_NAME elements are next to each other, the splitter will treat those as unsplittable
// elements.
func ByNumberAndComponent(tasks []*Task) [][]*TaskNode {
	if len(tasks) <= 1 {
		return nil
	}
	count := len(tasks)
	matches := make([][]string, 0, count)
	for i := 0; i < len(tasks); i++ {
		matches = append(matches, reMatchPattern.FindStringSubmatch(tasks[i].Manifest.OriginalFilename))
	}

	var buckets [][]*TaskNode
	var lastNode *TaskNode
	for i := 0; i < count; {
		matchBase := matches[i]
		j := i + 1
		var groups []*TaskNode
		for ; j < count; j++ {
			matchNext := matches[j]
			if matchBase == nil || matchNext == nil || matchBase[groupNumber] != matchNext[groupNumber] {
				break
			}
			if matchBase[groupComponent] != matchNext[groupComponent] {
				groups = append(groups, &TaskNode{Tasks: tasks[i:j]})
				i = j
			}
			matchBase = matchNext
		}
		if len(groups) > 0 {
			groups = append(groups, &TaskNode{Tasks: tasks[i:j]})
			i = j
			buckets = append(buckets, groups)
			lastNode = nil
			continue
		}
		if lastNode == nil {
			lastNode = &TaskNode{Tasks: append([]*Task(nil), tasks[i:j]...)}
			i = j
			buckets = append(buckets, []*TaskNode{lastNode})
			continue
		}
		lastNode.Tasks = append(lastNode.Tasks, tasks[i:j]...)
		i = j
	}
	return buckets
}

// FlattenByNumberAndComponent creates parallelization for tasks whose original filenames are of the form
// 0000_NN_NAME_* - files that share 0000_NN_NAME_ are run in serial, but chunks of files that have
// different 0000_NN_NAME can be run in parallel. This splitter does *not* preserve ordering within run
// levels and is intended only for use cases where order is not important.
func FlattenByNumberAndComponent(tasks []*Task) [][]*TaskNode {
	if len(tasks) <= 1 {
		return nil
	}
	count := len(tasks)
	matches := make([][]string, 0, count)
	for i := 0; i < len(tasks); i++ {
		matches = append(matches, reMatchPattern.FindStringSubmatch(tasks[i].Manifest.OriginalFilename))
	}

	var lastNode *TaskNode
	var groups []*TaskNode
	for i := 0; i < count; {
		matchBase := matches[i]
		j := i + 1
		for ; j < count; j++ {
			matchNext := matches[j]
			if matchBase == nil || matchNext == nil || matchBase[groupNumber] != matchNext[groupNumber] {
				break
			}
			if matchBase[groupComponent] != matchNext[groupComponent] {
				groups = append(groups, &TaskNode{Tasks: tasks[i:j]})
				i = j
			}
			matchBase = matchNext
		}
		if len(groups) > 0 {
			groups = append(groups, &TaskNode{Tasks: tasks[i:j]})
			i = j
			lastNode = nil
			continue
		}
		if lastNode == nil {
			lastNode = &TaskNode{Tasks: append([]*Task(nil), tasks[i:j]...)}
			i = j
			groups = append(groups, lastNode)
			continue
		}
		lastNode.Tasks = append(lastNode.Tasks, tasks[i:j]...)
		i = j
	}
	return [][]*TaskNode{groups}
}

type TaskNode struct {
	In    []int
	Tasks []*Task
	Out   []int
}

func (n TaskNode) String() string {
	var arr []string
	for _, t := range n.Tasks {
		if len(t.Manifest.OriginalFilename) > 0 {
			arr = append(arr, t.Manifest.OriginalFilename)
			continue
		}
		arr = append(arr, t.Manifest.GVK.String())
	}
	return "{Tasks: " + strings.Join(arr, ", ") + "}"
}

func (n *TaskNode) replaceIn(index, with int) {
	for i, from := range n.In {
		if from == index {
			n.In[i] = with
		}
	}
}

func (n *TaskNode) replaceOut(index, with int) {
	for i, to := range n.Out {
		if to == index {
			n.Out[i] = with
		}
	}
}

func (n *TaskNode) appendOut(items ...int) {
	for _, in := range items {
		if !containsInt(n.Out, in) {
			n.Out = append(n.Out, in)
		}
	}
}

// TaskGraph provides methods for parallelizing a linear sequence
// of Tasks based on Split or Parallelize functions.
type TaskGraph struct {
	Nodes []*TaskNode
}

// NewTaskGraph creates a graph with a single node containing
// the supplied tasks.
func NewTaskGraph(tasks []*Task) *TaskGraph {
	return &TaskGraph{
		Nodes: []*TaskNode{
			{
				Tasks: tasks,
			},
		},
	}
}

func containsInt(arr []int, value int) bool {
	for _, i := range arr {
		if i == value {
			return true
		}
	}
	return false
}

func (g *TaskGraph) replaceInOf(index, with int) {
	node := g.Nodes[index]
	in := node.In
	for _, pos := range in {
		g.Nodes[pos].replaceOut(index, with)
	}
}

func (g *TaskGraph) replaceOutOf(index, with int) {
	node := g.Nodes[index]
	out := node.Out
	for _, pos := range out {
		g.Nodes[pos].replaceIn(index, with)
	}
}

// Split breaks a graph node with a task that onFn returns true into
// one, two, or three separate nodes, preserving the order of tasks.
// E.g. a node with [a,b,c,d] where onFn returns true of b will result
// in a graph with [a] -> [b] -> [c,d].
func (g *TaskGraph) Split(onFn func(task *Task) bool) {
	for i := 0; i < len(g.Nodes); i++ {
		node := g.Nodes[i]
		tasks := node.Tasks
		if len(tasks) <= 1 {
			continue
		}
		for j, task := range tasks {
			if !onFn(task) {
				continue
			}

			if j > 0 {
				left := tasks[0:j]
				next := len(g.Nodes)
				nextNode := &TaskNode{
					In:    node.In,
					Tasks: left,
					Out:   []int{i},
				}
				g.Nodes = append(g.Nodes, nextNode)
				g.replaceInOf(i, next)
				node.In = []int{next}
			}

			if j < (len(tasks) - 1) {
				right := tasks[j+1:]
				next := len(g.Nodes)
				nextNode := &TaskNode{
					In:    []int{i},
					Tasks: right,
					Out:   node.Out,
				}
				g.Nodes = append(g.Nodes, nextNode)
				g.replaceOutOf(i, next)
				node.Out = []int{next}
			}

			node.Tasks = tasks[j : j+1]
			break
		}
	}
}

// BreakFunc returns the input tasks in order of dependencies with
// explicit parallelizm allowed per task in an array of task nodes.
type BreakFunc func([]*Task) [][]*TaskNode

// ShiftOrder rotates each TaskNode by step*len/stride when stride > len,
// or by step when stride < len, to ensure a different order within the
// task node list is tried.
func ShiftOrder(breakFn BreakFunc, step, stride int) BreakFunc {
	return func(tasks []*Task) [][]*TaskNode {
		steps := breakFn(tasks)
		for i, stepTasks := range steps {
			max := len(stepTasks)
			var shift int
			if max < stride {
				shift = step % max
			} else {
				shift = int(float64(step)*float64(max)/float64(stride)) % max
			}
			copied := make([]*TaskNode, max)
			copy(copied, stepTasks[shift:])
			copy(copied[max-shift:], stepTasks[0:shift])
			steps[i] = copied
		}
		return steps
	}
}

// PermuteOrder returns a split function that ensures the order of
// each step is shuffled based on r.
func PermuteOrder(breakFn BreakFunc, r *rand.Rand) BreakFunc {
	return func(tasks []*Task) [][]*TaskNode {
		steps := breakFn(tasks)
		for _, stepTasks := range steps {
			r.Shuffle(len(stepTasks), func(i, j int) {
				stepTasks[i], stepTasks[j] = stepTasks[j], stepTasks[i]
			})
		}
		return steps
	}
}

// Parallelize takes the given breakFn and splits any TaskNode's tasks up
// into parallel groups. If breakFn returns an empty array or a single
// array item with a single task node, that is considered a no-op.
func (g *TaskGraph) Parallelize(breakFn BreakFunc) {
	for i := 0; i < len(g.Nodes); i++ {
		node := g.Nodes[i]
		results := breakFn(node.Tasks)
		if len(results) == 0 || (len(results) == 1 && len(results[0]) == 1) {
			continue
		}
		node.Tasks = nil
		out := node.Out
		node.Out = nil

		// starting with the left anchor, create chains of nodes,
		// and avoid M x N in/out connections by creating spacers
		in := []int{i}
		for _, inNodes := range results {
			if len(inNodes) == 0 {
				continue
			}
			singleIn, singleOut := len(in) == 1, len(inNodes) == 1

			switch {
			case singleIn && singleOut, singleIn, singleOut:
				in = g.bulkAdd(inNodes, in)
			default:
				in = g.bulkAdd([]*TaskNode{{}}, in)
				in = g.bulkAdd(inNodes, in)
			}
		}

		// make node the left anchor and nextNode the right anchor
		if len(out) > 0 {
			next := len(g.Nodes)
			nextNode := &TaskNode{
				Tasks: nil,
				Out:   out,
			}
			g.Nodes = append(g.Nodes, nextNode)
			for _, j := range out {
				g.Nodes[j].replaceIn(i, next)
			}
			for _, j := range in {
				g.Nodes[j].Out = []int{next}
				nextNode.In = append(nextNode.In, j)
			}
		}
	}
}

func (g *TaskGraph) Roots() []int {
	var roots []int
	for i, n := range g.Nodes {
		if len(n.In) > 0 {
			continue
		}
		roots = append(roots, i)
	}
	return roots
}

// Tree renders the task graph in Graphviz DOT.
func (g *TaskGraph) Tree() string {
	out := []string{
		"digraph tasks {",
		"  labelloc=t;",
		"  rankdir=TB;",
	}
	for i, node := range g.Nodes {
		label := make([]string, 0, len(node.Tasks))
		for _, task := range node.Tasks {
			label = append(label, strings.Replace(task.String(), "\"", "", -1))
		}
		if len(label) == 0 {
			label = append(label, "no manifests")
		}
		out = append(out, fmt.Sprintf("  %d [ label=\"%s\" shape=\"box\" ];", i, strings.Join(label, "\\n")))
	}
	for i, node := range g.Nodes {
		for _, j := range node.Out {
			out = append(out, fmt.Sprintf("  %d -> %d;", i, j))
		}
	}
	out = append(out, "}")
	return strings.Join(out, "\n")
}

func covers(all []int, some []int) bool {
	for _, i := range some {
		if all[i] == 0 {
			return false
		}
	}
	return true
}

func (g *TaskGraph) bulkAdd(nodes []*TaskNode, inNodes []int) []int {
	from := len(g.Nodes)
	g.Nodes = append(g.Nodes, nodes...)
	to := len(g.Nodes)
	if len(inNodes) == 0 {
		toNodes := make([]int, to-from)
		for k := from; k < to; k++ {
			toNodes[k-from] = k
		}
		return toNodes
	}

	next := make([]int, to-from)
	for k := from; k < to; k++ {
		g.Nodes[k].In = append([]int(nil), inNodes...)
		next[k-from] = k
	}
	for _, k := range inNodes {
		g.Nodes[k].appendOut(next...)
	}
	return next
}

type runTasks struct {
	index int
	tasks []*Task
}

type taskStatus struct {
	index int
	error error
}

// RunGraph executes the provided graph in order and in parallel up to maxParallelism. It will not start
// a new TaskNode until all of the prerequisites have completed. If fn returns an error, no dependencies
// of that node will be executed, but other indepedent edges will continue executing.
func RunGraph(ctx context.Context, graph *TaskGraph, maxParallelism int, fn func(ctx context.Context, tasks []*Task) error) []error {
	submitted := make([]bool, len(graph.Nodes))
	results := make([]*taskStatus, len(graph.Nodes))

	canVisit := func(node *TaskNode) bool {
		for _, previous := range node.In {
			if result := results[previous]; result == nil || result.error != nil {
				return false
			}
		}
		return true
	}

	getNextNode := func() int {
		for i, node := range graph.Nodes {
			if submitted[i] {
				continue
			}
			if canVisit(node) {
				return i
			}
		}

		return -1
	}

	// Tasks go out to the workers via workCh, and results come brack
	// from the workers via resultCh.
	workCh := make(chan runTasks, maxParallelism)
	defer close(workCh)

	resultCh := make(chan taskStatus, maxParallelism)
	defer close(resultCh)

	nestedCtx, cancelFn := context.WithCancel(ctx)
	defer cancelFn()

	wg := sync.WaitGroup{}
	if maxParallelism < 1 {
		maxParallelism = 1
	}
	for i := 0; i < maxParallelism; i++ {
		wg.Add(1)
		go func(ctx context.Context, job int) {
			defer utilruntime.HandleCrash()
			defer wg.Done()
			for {
				select {
				case <-ctx.Done():
					klog.V(4).Infof("Canceled worker %d while waiting for work", job)
					return
				case runTask := <-workCh:
					klog.V(4).Infof("Running %d on worker %d", runTask.index, job)
					err := fn(ctx, runTask.tasks)
					resultCh <- taskStatus{index: runTask.index, error: err}
				}
			}
		}(nestedCtx, i)
	}

	var inflight int
	nextNode := getNextNode()
	done := false
	for !done {
		switch {
		case ctx.Err() == nil && nextNode >= 0: // push a task or collect a result
			select {
			case workCh <- runTasks{index: nextNode, tasks: graph.Nodes[nextNode].Tasks}:
				submitted[nextNode] = true
				inflight++
			case result := <-resultCh:
				results[result.index] = &result
				inflight--
			case <-ctx.Done():
			}
		case inflight > 0: // no work available to push; collect results
			select {
			case result := <-resultCh:
				results[result.index] = &result
				inflight--
			case <-ctx.Done():
				select {
				case runTask := <-workCh: // workers canceled, so remove any work from the queue ourselves
					inflight--
					submitted[runTask.index] = false
				default:
				}
			}
		default: // no work to push and nothing in flight.  We're done
			done = true
		}
		if !done {
			nextNode = getNextNode()
		}
	}

	cancelFn()
	wg.Wait()
	klog.V(4).Infof("Workers finished")

	var errs []error
	var firstIncompleteNode *TaskNode
	incompleteCount := 0
	for i, result := range results {
		if result == nil {
			if firstIncompleteNode == nil {
				firstIncompleteNode = graph.Nodes[i]
			}
			incompleteCount++
		} else if result.error != nil {
			errs = append(errs, result.error)
		}
	}

	if len(errs) == 0 && firstIncompleteNode != nil {
		errs = append(errs, fmt.Errorf("%d incomplete task nodes, beginning with %s", incompleteCount, firstIncompleteNode.Tasks[0]))
		if err := ctx.Err(); err != nil {
			errs = append(errs, err)
		}
	}

	klog.V(4).Infof("Result of work: %v", errs)
	if len(errs) > 0 {
		return errs
	}
	return nil
}

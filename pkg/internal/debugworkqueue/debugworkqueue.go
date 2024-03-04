// Package debugworkqueue wraps client-go work queues with trace logging.
package debugworkqueue

import (
	"runtime"
	"strings"
	"time"

	"k8s.io/client-go/util/workqueue"
	"k8s.io/klog/v2"
)

func NewNamedRateLimitingQueue(rateLimiter workqueue.RateLimiter, name string) workqueue.RateLimitingInterface {
	return &rateLimitingInterface{
		name:    name,
		wrapped: workqueue.NewNamedRateLimitingQueue(rateLimiter, name),
	}
}

type rateLimitingInterface struct {
	name    string
	wrapped workqueue.RateLimitingInterface
}

func context() string {
	context := "unknown stack"
	programCounters := make([]uintptr, 5) // ask for the closest 5 entries in the stack
	n := runtime.Callers(3, programCounters)
	if n > 0 {
		programCounters = programCounters[:n] // drop unfilled slots
		frames := runtime.CallersFrames(programCounters)
		stack := make([]string, 0, n)
		for {
			frame, more := frames.Next()
			stack = append(stack, frame.Function)
			if !more {
				break
			}
		}
		context = strings.Join(stack, " < ")
	}
	return context
}

func (q *rateLimitingInterface) Add(item interface{}) {
	klog.V(2).Infof("%q Add %v (%s)\n", q.name, item, context())
	q.wrapped.Add(item)
}

func (q *rateLimitingInterface) AddAfter(item interface{}, duration time.Duration) {
	klog.V(2).Infof("%q AddAfter %v %s (%s)", q.name, item, duration, context())
	q.wrapped.AddAfter(item, duration)
}

func (q *rateLimitingInterface) AddRateLimited(item interface{}) {
	klog.V(2).Infof("%q AddRateLimited %v (%s)", q.name, item, context())
	q.wrapped.AddRateLimited(item)
}

func (q *rateLimitingInterface) Done(item interface{}) {
	klog.V(2).Infof("%q Done %v", q.name, item)
	q.wrapped.Done(item)
}

func (q *rateLimitingInterface) Forget(item interface{}) {
	klog.V(2).Infof("%q Forget %v", q.name, item)
	q.wrapped.Forget(item)
}

func (q *rateLimitingInterface) Get() (interface{}, bool) {
	klog.V(2).Infof("%q Get...", q.name)
	item, shutdown := q.wrapped.Get()
	klog.V(2).Infof("%q ...Get %v (shutdown %t)", q.name, item, shutdown)
	return item, shutdown
}

func (q *rateLimitingInterface) Len() int {
	return q.wrapped.Len()
}

func (q *rateLimitingInterface) NumRequeues(item interface{}) int {
	return q.wrapped.NumRequeues(item)
}

func (q *rateLimitingInterface) ShutDown() {
	klog.V(2).Infof("%q ShutDown", q.name)
	q.wrapped.ShutDown()
}

func (q *rateLimitingInterface) ShutDownWithDrain() {
	klog.V(2).Infof("%q ShutDownWithDrain...", q.name)
	q.wrapped.ShutDownWithDrain()
	klog.V(2).Infof("%q ...ShutDownWithDrain", q.name)
}

func (q *rateLimitingInterface) ShuttingDown() bool {
	return q.wrapped.ShuttingDown()
}

package agenticrun

import (
	"context"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/tools/cache"
	"k8s.io/klog/v2"

	agenticrunv1alpha1 "github.com/openshift/lightspeed-agentic-operator/api/v1alpha1"
)

// RunInformer watches CVO-owned runs independently of the required CVO caches.
// The reflector retries when the optional API is absent or a watch disconnects.
func (c *Controller) RunInformer(ctx context.Context) error {
	if c.dynamicClient == nil {
		<-ctx.Done()
		return nil
	}
	resource := c.dynamicClient.Resource(agenticrunv1alpha1.GroupVersion.WithResource("agenticruns")).Namespace(c.config.Namespace)
	selector := labels.SelectorFromSet(CVOAgenticRunLabels).String()
	listWatch := &cache.ListWatch{
		ListWithContextFunc: func(ctx context.Context, options metav1.ListOptions) (runtime.Object, error) {
			options.LabelSelector = selector
			return resource.List(ctx, options)
		},
		WatchFuncWithContext: func(ctx context.Context, options metav1.ListOptions) (watch.Interface, error) {
			options.LabelSelector = selector
			return resource.Watch(ctx, options)
		},
	}
	informer := cache.NewSharedIndexInformer(cache.ToListWatcherWithWatchListSemantics(listWatch, c.dynamicClient), &unstructured.Unstructured{}, 0, cache.Indexers{})
	if _, err := informer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		// Filtering happens on the server, so tombstones need no decoding.
		DeleteFunc: func(_ interface{}) { c.queue.Add(c.queueKey) },
	}); err != nil {
		return err
	}
	if err := informer.SetWatchErrorHandlerWithContext(func(ctx context.Context, _ *cache.Reflector, err error) {
		klog.FromContext(ctx).V(2).Info("AgenticRun watch unavailable; will retry", "error", err)
	}); err != nil {
		return err
	}
	informer.RunWithContext(ctx)
	return nil
}

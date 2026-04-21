package readiness

import (
	"context"
	"strings"

	semver "github.com/blang/semver/v4"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
)

var (
	GVRClusterVersion    = schema.GroupVersionResource{Group: "config.openshift.io", Version: "v1", Resource: "clusterversions"}
	GVRClusterOperator   = schema.GroupVersionResource{Group: "config.openshift.io", Version: "v1", Resource: "clusteroperators"}
	GVRMachineConfigPool = schema.GroupVersionResource{Group: "machineconfiguration.openshift.io", Version: "v1", Resource: "machineconfigpools"}
	GVRNode              = schema.GroupVersionResource{Group: "", Version: "v1", Resource: "nodes"}
	GVRPod               = schema.GroupVersionResource{Group: "", Version: "v1", Resource: "pods"}
	GVRPDB               = schema.GroupVersionResource{Group: "policy", Version: "v1", Resource: "poddisruptionbudgets"}
	GVRPV                = schema.GroupVersionResource{Group: "", Version: "v1", Resource: "persistentvolumes"}
	GVRSecret            = schema.GroupVersionResource{Group: "", Version: "v1", Resource: "secrets"}
	GVRCRD               = schema.GroupVersionResource{Group: "apiextensions.k8s.io", Version: "v1", Resource: "customresourcedefinitions"}
	GVRCSV               = schema.GroupVersionResource{Group: "operators.coreos.com", Version: "v1alpha1", Resource: "clusterserviceversions"}
	GVRSubscription      = schema.GroupVersionResource{Group: "operators.coreos.com", Version: "v1alpha1", Resource: "subscriptions"}
	GVRInstallPlan       = schema.GroupVersionResource{Group: "operators.coreos.com", Version: "v1alpha1", Resource: "installplans"}
	GVRPackageManifest   = schema.GroupVersionResource{Group: "packages.operators.coreos.com", Version: "v1", Resource: "packagemanifests"}
	GVRAPIRequestCount   = schema.GroupVersionResource{Group: "apiserver.openshift.io", Version: "v1", Resource: "apirequestcounts"}
	GVRInfrastructure    = schema.GroupVersionResource{Group: "config.openshift.io", Version: "v1", Resource: "infrastructures"}
	GVRNetwork           = schema.GroupVersionResource{Group: "config.openshift.io", Version: "v1", Resource: "networks"}
	GVRAPIServer         = schema.GroupVersionResource{Group: "config.openshift.io", Version: "v1", Resource: "apiservers"}
	GVRProxy             = schema.GroupVersionResource{Group: "config.openshift.io", Version: "v1", Resource: "proxies"}
	GVRNodeMetrics       = schema.GroupVersionResource{Group: "metrics.k8s.io", Version: "v1beta1", Resource: "nodes"}
	GVRValidatingWebhook = schema.GroupVersionResource{Group: "admissionregistration.k8s.io", Version: "v1", Resource: "validatingwebhookconfigurations"}
	GVRMutatingWebhook   = schema.GroupVersionResource{Group: "admissionregistration.k8s.io", Version: "v1", Resource: "mutatingwebhookconfigurations"}
)

// GetResource fetches a single cluster-scoped resource by name.
func GetResource(ctx context.Context, c dynamic.Interface, gvr schema.GroupVersionResource, name string) (*unstructured.Unstructured, error) {
	return c.Resource(gvr).Get(ctx, name, metav1.GetOptions{})
}

// GetNamespacedResource fetches a single namespaced resource.
func GetNamespacedResource(ctx context.Context, c dynamic.Interface, gvr schema.GroupVersionResource, namespace, name string) (*unstructured.Unstructured, error) {
	return c.Resource(gvr).Namespace(namespace).Get(ctx, name, metav1.GetOptions{})
}

// ListResources lists cluster-scoped resources, optionally filtered by label selector.
func ListResources(ctx context.Context, c dynamic.Interface, gvr schema.GroupVersionResource, labelSelector string) ([]unstructured.Unstructured, error) {
	opts := metav1.ListOptions{}
	if labelSelector != "" {
		opts.LabelSelector = labelSelector
	}
	list, err := c.Resource(gvr).List(ctx, opts)
	if err != nil {
		return nil, err
	}
	return list.Items, nil
}

// ListNamespacedResources lists resources in a specific namespace.
func ListNamespacedResources(ctx context.Context, c dynamic.Interface, gvr schema.GroupVersionResource, namespace, labelSelector string) ([]unstructured.Unstructured, error) {
	opts := metav1.ListOptions{}
	if labelSelector != "" {
		opts.LabelSelector = labelSelector
	}
	list, err := c.Resource(gvr).Namespace(namespace).List(ctx, opts)
	if err != nil {
		return nil, err
	}
	return list.Items, nil
}

// ListAllNamespacedResources lists resources across all namespaces.
func ListAllNamespacedResources(ctx context.Context, c dynamic.Interface, gvr schema.GroupVersionResource, labelSelector string) ([]unstructured.Unstructured, error) {
	return ListResources(ctx, c, gvr, labelSelector)
}

// Condition represents a parsed Kubernetes status condition.
type Condition struct {
	Status         string `json:"status"`
	Reason         string `json:"reason"`
	Message        string `json:"message"`
	LastTransition string `json:"last_transition"`
}

// GetConditions extracts status.conditions from an unstructured object into a map keyed by type.
func GetConditions(obj *unstructured.Unstructured) map[string]Condition {
	conditions, _, _ := unstructured.NestedSlice(obj.Object, "status", "conditions")
	result := make(map[string]Condition, len(conditions))
	for _, raw := range conditions {
		c, ok := raw.(map[string]interface{})
		if !ok {
			continue
		}
		t, _ := c["type"].(string)
		result[t] = Condition{
			Status:         strVal(c, "status"),
			Reason:         strVal(c, "reason"),
			Message:        strVal(c, "message"),
			LastTransition: strVal(c, "lastTransitionTime"),
		}
	}
	return result
}

// Convenience wrappers for nested field access.

func NestedString(obj map[string]interface{}, fields ...string) string {
	val, _, _ := unstructured.NestedString(obj, fields...)
	return val
}

func NestedInt64(obj map[string]interface{}, fields ...string) int64 {
	val, _, _ := unstructured.NestedInt64(obj, fields...)
	return val
}

func NestedBool(obj map[string]interface{}, fields ...string) bool {
	val, _, _ := unstructured.NestedBool(obj, fields...)
	return val
}

func NestedSlice(obj map[string]interface{}, fields ...string) []interface{} {
	val, _, _ := unstructured.NestedSlice(obj, fields...)
	return val
}

func NestedMap(obj map[string]interface{}, fields ...string) map[string]interface{} {
	val, _, _ := unstructured.NestedMap(obj, fields...)
	return val
}

func strVal(m map[string]interface{}, key string) string {
	v, _ := m[key].(string)
	return v
}

const (
	ConditionTrue  = "True"
	ConditionFalse = "False"
)

const (
	ConditionAvailable   = "Available"
	ConditionDegraded    = "Degraded"
	ConditionProgressing = "Progressing"
	ConditionUpgradeable = "Upgradeable"
	ConditionUpdating    = "Updating"
	ConditionRecommended = "Recommended"
)

// CompareVersions compares two semver strings. Returns -1, 0, or 1.
func CompareVersions(a, b string) int {
	va, err := semver.ParseTolerant(a)
	if err != nil {
		return 0
	}
	vb, err := semver.ParseTolerant(b)
	if err != nil {
		return 0
	}
	return va.Compare(vb)
}

// FormatLabelSelector converts a map of labels to a label selector string.
func FormatLabelSelector(labels map[string]string) string {
	parts := make([]string, 0, len(labels))
	for k, v := range labels {
		parts = append(parts, k+"="+v)
	}
	return strings.Join(parts, ",")
}

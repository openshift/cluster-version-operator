package proposal

import (
	"context"
	"strings"
	"testing"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	clienttesting "k8s.io/client-go/testing"

	configv1 "github.com/openshift/api/config/v1"
)

func newFakeClient(objects ...runtime.Object) *dynamicfake.FakeDynamicClient {
	scheme := runtime.NewScheme()
	proposalGVR := schema.GroupVersionResource{Group: "agentic.openshift.io", Version: "v1alpha1", Resource: "proposals"}
	cmGVR := schema.GroupVersionResource{Group: "", Version: "v1", Resource: "configmaps"}
	scheme.AddKnownTypeWithName(
		schema.GroupVersionKind{Group: "agentic.openshift.io", Version: "v1alpha1", Kind: "Proposal"},
		&unstructured.Unstructured{},
	)
	scheme.AddKnownTypeWithName(
		schema.GroupVersionKind{Group: "agentic.openshift.io", Version: "v1alpha1", Kind: "ProposalList"},
		&unstructured.UnstructuredList{},
	)
	scheme.AddKnownTypeWithName(
		schema.GroupVersionKind{Group: "", Version: "v1", Kind: "ConfigMap"},
		&unstructured.Unstructured{},
	)
	scheme.AddKnownTypeWithName(
		schema.GroupVersionKind{Group: "", Version: "v1", Kind: "ConfigMapList"},
		&unstructured.UnstructuredList{},
	)
	return dynamicfake.NewSimpleDynamicClientWithCustomListKinds(scheme,
		map[schema.GroupVersionResource]string{
			proposalGVR: "ProposalList",
			cmGVR:       "ConfigMapList",
		}, objects...)
}

func TestSelectTarget(t *testing.T) {
	tests := []struct {
		name                string
		updates             []configv1.Release
		conditionalUpdates  []configv1.ConditionalUpdate
		expectedVersion     string
		expectedKind        string
	}{
		{
			name:            "no updates",
			expectedVersion: "",
			expectedKind:    "",
		},
		{
			name: "single recommended",
			updates: []configv1.Release{
				{Version: "4.15.3"},
			},
			expectedVersion: "4.15.3",
			expectedKind:    "recommended",
		},
		{
			name: "multiple recommended returns highest",
			updates: []configv1.Release{
				{Version: "4.15.1"},
				{Version: "4.15.3"},
				{Version: "4.15.2"},
			},
			expectedVersion: "4.15.3",
			expectedKind:    "recommended",
		},
		{
			name: "single conditional",
			conditionalUpdates: []configv1.ConditionalUpdate{
				{Release: configv1.Release{Version: "4.16.0"}},
			},
			expectedVersion: "4.16.0",
			expectedKind:    "conditional",
		},
		{
			name: "recommended preferred over conditional at same version",
			updates: []configv1.Release{
				{Version: "4.15.3"},
			},
			conditionalUpdates: []configv1.ConditionalUpdate{
				{Release: configv1.Release{Version: "4.15.3"}},
			},
			expectedVersion: "4.15.3",
			expectedKind:    "recommended",
		},
		{
			name: "highest recommended even when conditional is higher",
			updates: []configv1.Release{
				{Version: "4.15.3"},
			},
			conditionalUpdates: []configv1.ConditionalUpdate{
				{Release: configv1.Release{Version: "4.16.0"}},
			},
			expectedVersion: "4.15.3",
			expectedKind:    "recommended",
		},
		{
			name: "conditional fallback when no recommended",
			conditionalUpdates: []configv1.ConditionalUpdate{
				{Release: configv1.Release{Version: "4.15.1"}},
				{Release: configv1.Release{Version: "4.15.3"}},
			},
			expectedVersion: "4.15.3",
			expectedKind:    "conditional",
		},
		{
			name: "semver ordering not string ordering",
			updates: []configv1.Release{
				{Version: "4.9.0"},
				{Version: "4.15.0"},
			},
			expectedVersion: "4.15.0",
			expectedKind:    "recommended",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			version, kind := SelectTarget(tt.updates, tt.conditionalUpdates)
			if version != tt.expectedVersion {
				t.Errorf("SelectTarget() version = %q, want %q", version, tt.expectedVersion)
			}
			if kind != tt.expectedKind {
				t.Errorf("SelectTarget() kind = %q, want %q", kind, tt.expectedKind)
			}
		})
	}
}

func TestClassifyUpdate(t *testing.T) {
	tests := []struct {
		name     string
		current  string
		target   string
		expected string
	}{
		{name: "z-stream", current: "4.15.1", target: "4.15.3", expected: "z-stream"},
		{name: "minor", current: "4.15.1", target: "4.16.0", expected: "minor"},
		{name: "major", current: "4.15.1", target: "5.0.0", expected: "minor"},
		{name: "invalid current", current: "bad", target: "4.15.0", expected: "unknown"},
		{name: "invalid target", current: "4.15.0", target: "bad", expected: "unknown"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := classifyUpdate(tt.current, tt.target)
			if got != tt.expected {
				t.Errorf("classifyUpdate(%q, %q) = %q, want %q", tt.current, tt.target, got, tt.expected)
			}
		})
	}
}

func TestProposalName(t *testing.T) {
	tests := []struct {
		current  string
		target   string
		expected string
	}{
		{"4.15.1", "4.15.3", "ota-4-15-1-to-4-15-3"},
		{"4.15.1", "4.16.0", "ota-4-15-1-to-4-16-0"},
	}

	for _, tt := range tests {
		t.Run(tt.current+"->"+tt.target, func(t *testing.T) {
			got := proposalName(tt.current, tt.target)
			if got != tt.expected {
				t.Errorf("proposalName(%q, %q) = %q, want %q", tt.current, tt.target, got, tt.expected)
			}
		})
	}
}

func TestSanitize(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"4.15.1", "4-15-1"},
		{"Hello World", "hello-world"},
		{"a-very-long-version-string-that-is-too-long", "a-very-long-version"},
		{"trailing-dot.", "trailing-dot"},
		{"trailing-dash-", "trailing-dash"},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := sanitize(tt.input)
			if got != tt.expected {
				t.Errorf("sanitize(%q) = %q, want %q", tt.input, got, tt.expected)
			}
		})
	}
}

func TestBuildRequest(t *testing.T) {
	updates := []configv1.Release{
		{Version: "4.16.0", URL: "https://example.com/errata/1"},
		{Version: "4.16.1", URL: "https://example.com/errata/2"},
	}

	t.Run("recommended target", func(t *testing.T) {
		request := buildRequest("", "4.15.3", "4.16.0", "stable-4.16", "minor", "recommended", updates, "")
		if !strings.Contains(request, "Current version: OCP 4.15.3") {
			t.Error("request should contain current version")
		}
		if !strings.Contains(request, "Target version: OCP 4.16.0") {
			t.Error("request should contain target version")
		}
		if !strings.Contains(request, "Update type: minor") {
			t.Error("request should contain update type")
		}
		if !strings.Contains(request, "Update path: recommended") {
			t.Error("request should contain update path")
		}
		if strings.Contains(request, "WARNING") {
			t.Error("recommended target should not have warning")
		}
		if !strings.Contains(request, "Other recommended versions available:") {
			t.Error("should list other versions when more than one update")
		}
		if !strings.Contains(request, "4.16.1") {
			t.Error("should list alternative version")
		}
	})

	t.Run("conditional target", func(t *testing.T) {
		request := buildRequest("", "4.15.3", "4.16.0", "stable-4.16", "minor", "conditional", updates, "")
		if !strings.Contains(request, "WARNING") {
			t.Error("conditional target should have warning")
		}
		if !strings.Contains(request, "CONDITIONAL update") {
			t.Error("conditional target should mention CONDITIONAL")
		}
	})

	t.Run("readiness JSON embedded", func(t *testing.T) {
		request := buildRequest("", "4.15.3", "4.16.0", "stable-4.16", "minor", "recommended", updates, `{"checks":{},"meta":{}}`)
		if !strings.Contains(request, "## Cluster Readiness Data") {
			t.Error("request should contain readiness data header")
		}
		if !strings.Contains(request, `{"checks":{},"meta":{}}`) {
			t.Error("request should contain readiness JSON")
		}
	})
}

func TestSelectTarget_NoUpdates_ReturnsEmpty(t *testing.T) {
	version, kind := SelectTarget(nil, nil)
	if version != "" {
		t.Errorf("expected empty version, got %q", version)
	}
	if kind != "" {
		t.Errorf("expected empty kind, got %q", kind)
	}
}

func TestMaybeCreateProposal_CreatesProposal(t *testing.T) {
	client := newFakeClient()
	creator := NewCreator(client, Config{Namespace: "test-ns", Workflow: "ota-advisory"})

	updates := []configv1.Release{{Version: "4.15.3"}}
	creator.MaybeCreateProposal(context.Background(), "4.15.1", "4.15.3", "recommended", "stable-4.15", updates, "")

	var createAction *clienttesting.CreateActionImpl
	for _, action := range client.Actions() {
		if action.GetVerb() == "create" {
			a := action.(clienttesting.CreateAction)
			ca := clienttesting.CreateActionImpl{ActionImpl: clienttesting.ActionImpl{Verb: "create", Resource: a.GetResource(), Namespace: a.GetNamespace()}, Object: a.GetObject()}
			createAction = &ca
			break
		}
	}

	if createAction == nil {
		t.Fatal("expected a create action")
	}

	obj := createAction.Object.(*unstructured.Unstructured)
	if got := obj.GetName(); got != "ota-4-15-1-to-4-15-3" {
		t.Errorf("proposal name = %q, want %q", got, "ota-4-15-1-to-4-15-3")
	}
	if got := obj.GetNamespace(); got != "test-ns" {
		t.Errorf("proposal namespace = %q, want %q", got, "test-ns")
	}

	labels := obj.GetLabels()
	if got := labels["agentic.openshift.io/source"]; got != "cluster-version-operator" {
		t.Errorf("source label = %q, want %q", got, "cluster-version-operator")
	}
	if got := labels["agentic.openshift.io/update-type"]; got != "z-stream" {
		t.Errorf("update-type label = %q, want %q", got, "z-stream")
	}

	workflow, _, _ := unstructured.NestedString(obj.Object, "spec", "workflowRef", "name")
	if workflow != "ota-advisory" {
		t.Errorf("spec.workflow = %q, want %q", workflow, "ota-advisory")
	}
}

func TestMaybeCreateProposal_DedupSkipsExisting(t *testing.T) {
	client := newFakeClient()
	client.PrependReactor("create", "proposals", func(action clienttesting.Action) (bool, runtime.Object, error) {
		return true, nil, apierrors.NewAlreadyExists(
			schema.GroupResource{Group: "agentic.openshift.io", Resource: "proposals"},
			"ota-4-15-1-to-4-15-3",
		)
	})
	creator := NewCreator(client, Config{Namespace: "test-ns", Workflow: "ota-advisory"})

	updates := []configv1.Release{{Version: "4.15.3"}}
	creator.MaybeCreateProposal(context.Background(), "4.15.1", "4.15.3", "recommended", "stable-4.15", updates, "")

	// Should have attempted create (reactor intercepted it), but no error logged as warning
	var createAttempted bool
	for _, action := range client.Actions() {
		if action.GetVerb() == "create" {
			createAttempted = true
		}
	}
	if !createAttempted {
		t.Error("expected a create attempt that gets rejected with AlreadyExists")
	}
}

func TestMaybeCreateProposal_CreatesForNewTarget(t *testing.T) {
	// A proposal exists for 4.15.1 -> 4.15.3, but now the target is 4.15.4.
	// Different target = different deterministic name = no conflict.
	existing := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "agentic.openshift.io/v1alpha1",
			"kind":       "Proposal",
			"metadata": map[string]interface{}{
				"name":      "ota-4-15-1-to-4-15-3",
				"namespace": "test-ns",
			},
			"status": map[string]interface{}{
				"phase": "Completed",
			},
		},
	}

	client := newFakeClient(existing)
	creator := NewCreator(client, Config{Namespace: "test-ns", Workflow: "ota-advisory"})

	updates := []configv1.Release{{Version: "4.15.4"}}
	creator.MaybeCreateProposal(context.Background(), "4.15.1", "4.15.4", "recommended", "stable-4.15", updates, "")

	var created bool
	for _, action := range client.Actions() {
		if action.GetVerb() == "create" {
			created = true
			break
		}
	}
	if !created {
		t.Error("should create proposal for new target version")
	}
}

func TestReadSystemPrompt(t *testing.T) {
	cm := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "v1",
			"kind":       "ConfigMap",
			"metadata": map[string]interface{}{
				"name":      "ota-advisory-prompt",
				"namespace": "test-ns",
			},
			"data": map[string]interface{}{
				"prompt": "You are a helpful OTA advisory agent.",
			},
		},
	}

	client := newFakeClient(cm)
	creator := NewCreator(client, Config{Namespace: "test-ns", Workflow: "ota-advisory", PromptConfigMap: "ota-advisory-prompt"})

	prompt := creator.readSystemPrompt(context.Background())
	if prompt != "You are a helpful OTA advisory agent." {
		t.Errorf("readSystemPrompt() = %q, want system prompt text", prompt)
	}
}

func TestReadSystemPrompt_Missing(t *testing.T) {
	client := newFakeClient()
	creator := NewCreator(client, Config{Namespace: "test-ns", Workflow: "ota-advisory", PromptConfigMap: "nonexistent"})

	prompt := creator.readSystemPrompt(context.Background())
	if prompt != "" {
		t.Errorf("readSystemPrompt() = %q, want empty for missing ConfigMap", prompt)
	}
}

func TestReadSystemPrompt_EmptyConfigMapName(t *testing.T) {
	client := newFakeClient()
	creator := NewCreator(client, Config{Namespace: "test-ns", Workflow: "ota-advisory", PromptConfigMap: ""})

	prompt := creator.readSystemPrompt(context.Background())
	if prompt != "" {
		t.Errorf("readSystemPrompt() = %q, want empty when no ConfigMap name configured", prompt)
	}
}

func TestMaybeCreateProposal_IncludesSystemPrompt(t *testing.T) {
	cm := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "v1",
			"kind":       "ConfigMap",
			"metadata": map[string]interface{}{
				"name":      "ota-advisory-prompt",
				"namespace": "test-ns",
			},
			"data": map[string]interface{}{
				"prompt": "You are an OTA advisor.",
			},
		},
	}

	client := newFakeClient(cm)
	creator := NewCreator(client, Config{Namespace: "test-ns", Workflow: "ota-advisory", PromptConfigMap: "ota-advisory-prompt"})

	updates := []configv1.Release{{Version: "4.15.3"}}
	creator.MaybeCreateProposal(context.Background(), "4.15.1", "4.15.3", "recommended", "stable-4.15", updates, "")

	for _, action := range client.Actions() {
		if action.GetVerb() == "create" {
			obj := action.(clienttesting.CreateAction).GetObject().(*unstructured.Unstructured)
			request, _, _ := unstructured.NestedString(obj.Object, "spec", "request")
			if !strings.Contains(request, "You are an OTA advisor.") {
				t.Error("proposal request should contain system prompt")
			}
			return
		}
	}
	t.Fatal("expected a create action")
}

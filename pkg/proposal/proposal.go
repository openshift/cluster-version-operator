package proposal

import (
	"context"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/blang/semver/v4"

	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/klog/v2"

	configv1 "github.com/openshift/api/config/v1"
)

var lightspeedProposalGVR = schema.GroupVersionResource{
	Group:    "agentic.openshift.io",
	Version:  "v1alpha1",
	Resource: "proposals",
}

const (
	updateKindRecommended = "recommended"
	updateKindConditional = "conditional"

	updateTypeZStream = "z-stream"
	updateTypeMinor   = "minor"
	updateTypeUnknown = "unknown"
)

// Config holds configuration for proposal creation.
type Config struct {
	Namespace       string
	Workflow        string
	PromptConfigMap string // ConfigMap name containing the system prompt
}

// DefaultConfig returns the default configuration, checking env vars for overrides.
func DefaultConfig() Config {
	return Config{
		Namespace:       envOrDefault("LIGHTSPEED_PROPOSAL_NAMESPACE", "openshift-lightspeed"),
		Workflow:        envOrDefault("LIGHTSPEED_PROPOSAL_WORKFLOW", "ota-advisory"),
		PromptConfigMap: envOrDefault("LIGHTSPEED_PROMPT_CONFIGMAP", "ota-advisory-prompt"),
	}
}

// Creator creates Proposal CRs when updates are available.
type Creator struct {
	client dynamic.Interface
	config Config
}

// DynamicClient returns the underlying dynamic client for use by callers
// that need to run operations (e.g. readiness checks) with the same client.
func (c *Creator) DynamicClient() dynamic.Interface {
	return c.client
}

// NewCreator returns a new proposal Creator.
func NewCreator(client dynamic.Interface, config Config) *Creator {
	return &Creator{
		client: client,
		config: config,
	}
}

// MaybeCreateProposal creates a Proposal CR for the given target if one doesn't already exist.
// readinessJSON contains pre-collected cluster readiness data to embed in the request.
// Errors are logged but never returned — proposal creation must never block CVO.
func (c *Creator) MaybeCreateProposal(ctx context.Context, currentVersion, targetVersion, targetKind, channel string,
	updates []configv1.Release, readinessJSON string) {

	name := proposalName(currentVersion, targetVersion)

	systemPrompt := c.readSystemPrompt(ctx)

	updateType := classifyUpdate(currentVersion, targetVersion)
	request := buildRequest(systemPrompt, currentVersion, targetVersion, channel, updateType, targetKind, updates, readinessJSON)

	if err := c.createProposal(ctx, name, request, currentVersion, targetVersion, updateType); err != nil {
		if errors.IsAlreadyExists(err) {
			klog.V(4).Infof("Proposal %s/%s already exists, skipping", c.config.Namespace, name)
			return
		}
		if isNoMatchError(err) {
			klog.V(4).Infof("Proposal CRD not found, skipping proposal creation")
			return
		}
		klog.Warningf("Failed to create Proposal %s/%s: %v", c.config.Namespace, name, err)
		return
	}

	klog.Infof("Created Proposal %s/%s for upgrade %s -> %s (%s)",
		c.config.Namespace, name, currentVersion, targetVersion, updateType)
}

type versionCandidate struct {
	version semver.Version
	raw     string
	kind    string
}

// SelectTarget picks the highest recommended version, falling back to the highest conditional.
func SelectTarget(updates []configv1.Release, conditionalUpdates []configv1.ConditionalUpdate) (string, string) {
	var candidates []versionCandidate

	for _, u := range updates {
		v, err := semver.Parse(u.Version)
		if err != nil {
			klog.V(4).Infof("Skipping unparseable recommended version %q: %v", u.Version, err)
			continue
		}
		candidates = append(candidates, versionCandidate{version: v, raw: u.Version, kind: updateKindRecommended})
	}

	for _, u := range conditionalUpdates {
		v, err := semver.Parse(u.Release.Version)
		if err != nil {
			klog.V(4).Infof("Skipping unparseable conditional version %q: %v", u.Release.Version, err)
			continue
		}
		candidates = append(candidates, versionCandidate{version: v, raw: u.Release.Version, kind: updateKindConditional})
	}

	if len(candidates) == 0 {
		return "", ""
	}

	sort.Slice(candidates, func(i, j int) bool {
		cmp := candidates[i].version.Compare(candidates[j].version)
		if cmp != 0 {
			return cmp > 0
		}
		// Same version: prefer recommended over conditional
		return candidates[i].kind == updateKindRecommended
	})

	// Return highest recommended if one exists
	for _, c := range candidates {
		if c.kind == updateKindRecommended {
			return c.raw, c.kind
		}
	}

	// Fallback to highest conditional
	return candidates[0].raw, candidates[0].kind
}

// classifyUpdate returns "z-stream" if major.minor match, otherwise "minor".
func classifyUpdate(current, target string) string {
	cv, cerr := semver.Parse(current)
	tv, terr := semver.Parse(target)
	if cerr != nil || terr != nil {
		return updateTypeUnknown
	}
	if cv.Major == tv.Major && cv.Minor == tv.Minor {
		return updateTypeZStream
	}
	return updateTypeMinor
}

var configMapGVR = schema.GroupVersionResource{Group: "", Version: "v1", Resource: "configmaps"}

func (c *Creator) readSystemPrompt(ctx context.Context) string {
	if c.config.PromptConfigMap == "" {
		return ""
	}
	obj, err := c.client.Resource(configMapGVR).Namespace(c.config.Namespace).Get(ctx, c.config.PromptConfigMap, metav1.GetOptions{})
	if err != nil {
		klog.V(4).Infof("Could not read system prompt ConfigMap %s/%s: %v", c.config.Namespace, c.config.PromptConfigMap, err)
		return ""
	}
	data, _, _ := unstructured.NestedMap(obj.Object, "data")
	if prompt, ok := data["prompt"].(string); ok {
		return prompt
	}
	return ""
}

// buildRequest constructs the proposal request with system prompt, metadata, and readiness data.
func buildRequest(systemPrompt, current, target, channel, updateType, targetType string,
	updates []configv1.Release, readinessJSON string) string {

	var b strings.Builder

	if systemPrompt != "" {
		b.WriteString(systemPrompt)
		b.WriteString("\n\n---\n\n")
	}

	fmt.Fprintf(&b, "Current version: OCP %s\n", current)
	fmt.Fprintf(&b, "Target version: OCP %s\n", target)
	fmt.Fprintf(&b, "Channel: %s\n", channel)
	fmt.Fprintf(&b, "Update type: %s\n", updateType)
	fmt.Fprintf(&b, "Update path: %s\n\n", targetType)

	if targetType == updateKindConditional {
		b.WriteString("WARNING: This target version is available as a CONDITIONAL update.\n")
		b.WriteString("OSUS has flagged known risks that may apply to this cluster.\n")
		b.WriteString("The assessment MUST evaluate each conditional risk against cluster state.\n\n")
	}

	if len(updates) > 1 {
		b.WriteString("Other recommended versions available:\n")
		count := 0
		for _, u := range updates {
			if u.Version != target {
				if u.URL != "" {
					fmt.Fprintf(&b, "  - %s (errata: %s)\n", u.Version, u.URL)
				} else {
					fmt.Fprintf(&b, "  - %s\n", u.Version)
				}
				count++
				if count >= 5 {
					remaining := len(updates) - count - 1
					if remaining > 0 {
						fmt.Fprintf(&b, "  ... and %d more\n", remaining)
					}
					break
				}
			}
		}
		b.WriteString("\n")
	}

	if readinessJSON != "" {
		b.WriteString("## Cluster Readiness Data\n\n")
		b.WriteString("```json\n")
		b.WriteString(readinessJSON)
		b.WriteString("\n```\n")
	}

	return b.String()
}

// proposalName generates a deterministic proposal name from the version pair.
func proposalName(current, target string) string {
	return fmt.Sprintf("ota-%s-to-%s", sanitize(current), sanitize(target))
}

// sanitize converts a version string into a valid DNS-1035 label component.
// DNS-1035 requires: lowercase alphanumeric or '-', start with alpha, end with alphanum.
func sanitize(s string) string {
	s = strings.ToLower(s)
	s = strings.ReplaceAll(s, ".", "-")
	s = strings.ReplaceAll(s, " ", "-")
	if len(s) > 20 {
		s = s[:20]
	}
	return strings.TrimRight(s, "-")
}

func (c *Creator) createProposal(ctx context.Context, name, request, currentVersion, targetVersion, updateType string) error {
	proposal := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "agentic.openshift.io/v1alpha1",
			"kind":       "Proposal",
			"metadata": map[string]interface{}{
				"name":      name,
				"namespace": c.config.Namespace,
				"labels": map[string]interface{}{
					"agentic.openshift.io/source":          "cluster-version-operator",
					"agentic.openshift.io/current-version": sanitize(currentVersion),
					"agentic.openshift.io/target-version":  sanitize(targetVersion),
					"agentic.openshift.io/update-type":     updateType,
				},
			},
			"spec": map[string]interface{}{
				"workflowRef": map[string]interface{}{
					"name": c.config.Workflow,
				},
				"request":     request,
				"maxAttempts": int64(2),
			},
		},
	}
	_, err := c.client.Resource(lightspeedProposalGVR).Namespace(c.config.Namespace).Create(ctx, proposal, metav1.CreateOptions{})
	return err
}

func isNoMatchError(err error) bool {
	return meta.IsNoMatchError(err) || errors.IsNotFound(err)
}

func envOrDefault(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

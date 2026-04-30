package proposal

import (
	"context"
	"fmt"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/blang/semver/v4"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"

	corev1 "k8s.io/api/core/v1"
	kerrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	kutilerrors "k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/client-go/util/workqueue"
	"k8s.io/klog/v2"
	"k8s.io/utils/ptr"

	configv1 "github.com/openshift/api/config/v1"

	i "github.com/openshift/cluster-version-operator/pkg/internal"
	proposalv1alpha1 "github.com/openshift/cluster-version-operator/pkg/proposal/api/v1alpha1"
)

type Controller struct {
	queueKey              string
	queue                 workqueue.TypedRateLimitingInterface[any]
	updatesGetterFunc     updatesGetterFunc
	client                ctrlruntimeclient.Client
	cvGetterFunc          cvGetterFunc
	configMapGetterFunc   configMapGetterFunc
	getCurrentVersionFunc getCurrentVersionFunc
	config                Config
}

const controllerName = "proposal-lifecycle-controller"

type updatesGetterFunc func() ([]configv1.Release, []configv1.ConditionalUpdate, error)

type cvGetterFunc func(name string) (*configv1.ClusterVersion, error)

type getCurrentVersionFunc func() string

type configMapGetterFunc func(ctx context.Context, namespace, name string, opts metav1.GetOptions) (*corev1.ConfigMap, error)

// NewController returns Controller to manage Proposals.
// It monitors available and conditional updates, and creates a LightspeedProposal for every target version of them.
// It expires (and replace) any previous LightspeedProposals owned by the CVO after 24h.
// It deletes any CVO-owned LightspeedProposals (without replacement) that are associated with target releases
// that are no longer supported next-hop options (e.g. because a channel change or cluster update), but preserves
// LightspeedProposals associated with versions in the ClusterVersion status.history (history already has its own
// garbage-collection).
func NewController(
	updatesGetterFunc updatesGetterFunc,
	client ctrlruntimeclient.Client,
	cvGetterFunc cvGetterFunc,
	configMapGetterFunc configMapGetterFunc,
	getCurrentVersionFunc getCurrentVersionFunc,
) *Controller {
	return &Controller{
		queueKey: fmt.Sprintf("ClusterVersionOperator/%s", controllerName),
		queue: workqueue.NewTypedRateLimitingQueueWithConfig[any](
			workqueue.DefaultTypedControllerRateLimiter[any](),
			workqueue.TypedRateLimitingQueueConfig[any]{Name: controllerName}),
		updatesGetterFunc:     updatesGetterFunc,
		client:                client,
		cvGetterFunc:          cvGetterFunc,
		configMapGetterFunc:   configMapGetterFunc,
		getCurrentVersionFunc: getCurrentVersionFunc,
		config:                DefaultConfig(),
	}
}

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

func envOrDefault(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func (c *Controller) Queue() workqueue.TypedRateLimitingInterface[any] {
	return c.queue
}

func (c *Controller) QueueKey() string {
	return c.queueKey
}

func (c *Controller) Sync(ctx context.Context, key string) error {
	startTime := time.Now()
	klog.V(i.Normal).Infof("Started syncing CVO configuration %q", key)
	defer func() {
		klog.V(i.Normal).Infof("Finished syncing CVO configuration (%v)", time.Since(startTime))
	}()

	updates, conditionalUpdates, err := c.updatesGetterFunc()
	if err != nil {
		klog.Errorf("Error getting available updates: %v", err)
		return err
	}
	klog.V(i.Debug).Infof("Got available updates: %#v", updates)
	klog.V(i.Debug).Infof("Got conditional updates: %#v", conditionalUpdates)

	cv, err := c.cvGetterFunc(i.DefaultClusterVersionName)
	if err != nil {
		klog.V(i.Normal).Infof("Failed to get ClusterVersion %s: %v", i.DefaultClusterVersionName, err)
		return fmt.Errorf("failed to get ClusterVersion %s: %w", i.DefaultClusterVersionName, err)
	}

	var prompt string
	promptConfigMap, err := c.configMapGetterFunc(ctx, c.config.Namespace, c.config.PromptConfigMap, metav1.GetOptions{})
	if err != nil {
		klog.V(i.Normal).Infof("Failed to get prompt ConfigMap %s/%s: %v", c.config.Namespace, c.config.PromptConfigMap, err)
		return fmt.Errorf("failed to get prompt ConfigMap %s/%s: %w", c.config.Namespace, c.config.PromptConfigMap, err)
	}
	promptKey := "prompt"
	if v, ok := promptConfigMap.Data[promptKey]; ok {
		prompt = v
	} else {
		klog.V(i.Normal).Infof("ConfigMap %s/%s has no key %s in data", c.config.Namespace, c.config.PromptConfigMap, promptKey)
		// raise error?
	}

	currentVersion := c.getCurrentVersionFunc()

	var errs []error
	if err := deleteProposals(ctx, c.client, updates, conditionalUpdates, cv.Status.History, currentVersion); err != nil {
		errs = append(errs, err)
	}

	if len(updates) == 0 && len(conditionalUpdates) == 0 {
		return kutilerrors.NewAggregate(errs)
	}

	// TODO: Implement it
	readinessJSON := "{}"
	proposals, err := getProposals(updates, conditionalUpdates, c.config.Namespace, currentVersion, cv.Spec.Channel, c.config.Workflow, prompt, readinessJSON)
	if err != nil {
		klog.V(i.Normal).Infof("Getting proposals hit an error: %v", err)
		return kutilerrors.NewAggregate(append(errs, err))
	}

	for _, proposal := range proposals {
		existing := &proposalv1alpha1.Proposal{}
		err := c.client.Get(ctx, ctrlruntimeclient.ObjectKey{Name: proposal.Name, Namespace: proposal.Namespace}, existing)
		if err != nil {
			if !kerrors.IsNotFound(err) {
				klog.V(i.Normal).Infof("Failed to get proposal %s/%s: %v", proposal.Namespace, proposal.Name, err)
				errs = append(errs, err)
				continue
			}
		} else {
			if !ownedByCVO(existing) {
				klog.V(i.Normal).Infof("Ignored proposal %s/%s not owned by CVO", proposal.Namespace, proposal.Name)
				continue
			}
			if expired(existing) {
				if err := deleteProposal(ctx, c.client, existing, "expired"); err != nil {
					errs = append(errs, err)
					continue
				}
			} else {
				klog.V(i.Debug).Infof("The existing proposal %s/%s is not expired", proposal.Namespace, proposal.Name)
				continue
			}
		}

		if err := c.client.Create(ctx, proposal); err != nil {
			if !kerrors.IsAlreadyExists(err) {
				klog.V(i.Normal).Infof("Failed to create proposal %s/%s: %v", proposal.Namespace, proposal.Name, err)
				errs = append(errs, err)
			} else {
				klog.V(i.Debug).Infof("The proposal %s/%s existed already", proposal.Namespace, proposal.Name)
			}
		} else {
			klog.V(i.Debug).Infof("Created proposal %s/%s", proposal.Namespace, proposal.Name)
		}
	}

	return kutilerrors.NewAggregate(errs)
}

func ownedByCVO(p *proposalv1alpha1.Proposal) bool {
	if p == nil {
		return false
	}
	return p.Labels[labelKeySource] == labelValueSource
}

func expired(p *proposalv1alpha1.Proposal) bool {
	if p == nil {
		return false
	}
	return time.Now().After(p.CreationTimestamp.Add(proposalExpiration))
}

func deleteProposals(ctx context.Context, client ctrlruntimeclient.Client, availableUpdates []configv1.Release, conditionalUpdates []configv1.ConditionalUpdate, history []configv1.UpdateHistory, currentVersion string) error {
	targets := sets.New[string]()
	for _, update := range availableUpdates {
		targets.Insert(labelValueFromVersion(update.Version))
	}
	for _, update := range conditionalUpdates {
		targets.Insert(labelValueFromVersion(update.Release.Version))
	}
	associatedWithHistory := sets.New[string]()
	for _, h := range history {
		associatedWithHistory.Insert(labelValueFromVersion(h.Version))
	}

	list := &proposalv1alpha1.ProposalList{}
	if err := client.List(ctx, list, ctrlruntimeclient.MatchingLabels(CVOProposalLabels)); err != nil {
		return fmt.Errorf("failed to list proposals: %w", err)
	}
	var errs []error
	for _, proposal := range list.Items {
		if !ownedByCVO(&proposal) {
			klog.V(i.Debug).Infof("Keeping proposal %s/%s not owned by CVO", proposal.Namespace, proposal.Name)
			continue
		}
		cv, cvOk := proposal.Labels[labelKeyCurrentVersion]
		tv, tvOk := proposal.Labels[labelKeyTargetVersion]
		if cvOk && tvOk && cv == currentVersion && targets.Has(tv) {
			klog.V(i.Debug).Infof("Keeping relevant proposal %s/%s from %s to %s", proposal.Namespace, proposal.Name, cv, tv)
			continue
		}
		if tvOk && associatedWithHistory.Has(tv) {
			klog.V(i.Debug).Infof("Keeping proposal %s/%s for a version %s associated with history", proposal.Namespace, proposal.Name, tv)
			continue
		}
		err := deleteProposal(ctx, client, &proposal, "irrelevant")
		if err != nil {
			errs = append(errs, err)
		}
	}

	return kutilerrors.NewAggregate(errs)
}

func deleteProposal(ctx context.Context, client ctrlruntimeclient.Client, proposal *proposalv1alpha1.Proposal, adjective string) error {
	if proposal == nil {
		return nil
	}
	klog.V(i.Normal).Infof("Deleting %s proposal %s/%s ...", adjective, proposal.Namespace, proposal.Name)
	err := client.Delete(ctx, proposal)
	if err == nil {
		klog.V(i.Normal).Infof("Deleted %s proposal %s/%s", adjective, proposal.Namespace, proposal.Name)
		return nil
	}

	if !kerrors.IsNotFound(err) {
		klog.V(i.Normal).Infof("Failed to delete %s proposal %s/%s: %v", adjective, proposal.Namespace, proposal.Name, err)
		return err
	}

	klog.V(i.Normal).Infof("Failed to delete not-found proposal %s/%s", proposal.Namespace, proposal.Name)
	return nil
}

func getProposals(
	availableUpdates []configv1.Release,
	conditionalUpdates []configv1.ConditionalUpdate,
	namespace string,
	currentVersion, channel,
	workflowRefName string,
	systemPrompt string,
	readinessJSON string,
) ([]*proposalv1alpha1.Proposal, error) {
	var errs []error
	var proposals []*proposalv1alpha1.Proposal
	for _, au := range availableUpdates {
		targetVersion := au.Version
		if proposal, err := getProposal(namespace, currentVersion, targetVersion, channel, updateKindRecommended, workflowRefName, systemPrompt, readinessJSON, availableUpdates); err != nil {
			errs = append(errs, err)
			continue
		} else {
			proposals = append(proposals, proposal)
		}
	}

	for _, cu := range conditionalUpdates {
		targetVersion := cu.Release.Version
		if proposal, err := getProposal(namespace, currentVersion, targetVersion, channel, updateKindConditional, workflowRefName, systemPrompt, readinessJSON, availableUpdates); err != nil {
			errs = append(errs, err)
			continue
		} else {
			proposals = append(proposals, proposal)
		}
	}

	return proposals, kutilerrors.NewAggregate(errs)
}

func getProposal(namespace, currentVersion, targetVersion, channel, updateKind, workflowRefName, systemPrompt, readinessJSON string, availableUpdates []configv1.Release) (*proposalv1alpha1.Proposal, error) {

	var errs []error
	for _, v := range []string{currentVersion, targetVersion} {
		if _, err := semver.Parse(v); err != nil {
			errs = append(errs, fmt.Errorf("invalid version %s: %w", v, err))
		}
	}
	if len(errs) > 0 {
		return nil, kutilerrors.NewAggregate(errs)
	}

	name := proposalName(currentVersion, targetVersion)
	updateType := classifyUpdate(currentVersion, targetVersion)
	request := buildRequest(systemPrompt, currentVersion, targetVersion, channel, updateType, updateKind, availableUpdates, readinessJSON)
	return &proposalv1alpha1.Proposal{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
			Labels: map[string]string{
				labelKeySource:                     labelValueSource,
				labelKeyCurrentVersion:             labelValueFromVersion(currentVersion),
				labelKeyTargetVersion:              labelValueFromVersion(targetVersion),
				"agentic.openshift.io/update-type": updateType,
			},
		},
		Spec: proposalv1alpha1.ProposalSpec{
			Request: request,
			WorkflowRef: corev1.LocalObjectReference{
				Name: workflowRefName,
			},
			MaxAttempts: ptr.To(2),
		},
	}, nil
}

// https://kubernetes.io/docs/concepts/overview/working-with-objects/labels/#syntax-and-character-set
func labelValueFromVersion(version string) string {
	return trim63(version)
}

func trim63(value string) string {
	if len(value) > 63 {
		return value[:60] + "xxx"
	}
	return value
}

// proposalName generates a deterministic proposal name from the version pair.
func proposalName(current, target string) string {
	return toDNS1035(fmt.Sprintf("ota-%s-to-%s", current, target))
}

// https://kubernetes.io/docs/concepts/overview/working-with-objects/names/#rfc-1035-label-names
func toDNS1035(name string) string {
	// Convert to lowercase
	ret := strings.ToLower(name)

	// Replace any non-alphanumeric character with a hyphen
	reg := regexp.MustCompile(`[^a-z0-9]+`)
	ret = reg.ReplaceAllString(ret, "-")

	// Trim hyphens from the ends
	ret = strings.Trim(ret, "-")

	return trim63(ret)
}

const (
	labelKeySource         = "agentic.openshift.io/source"
	labelValueSource       = "cluster-version-operator"
	labelKeyCurrentVersion = "agentic.openshift.io/current-version"
	labelKeyTargetVersion  = "agentic.openshift.io/target-version"

	updateKindRecommended = "Recommended"
	updateKindConditional = "Conditional"

	proposalExpiration = 24 * time.Hour
)

var (
	CVOProposalLabels = map[string]string{labelKeySource: labelValueSource}
)

// classifyUpdate returns "z-stream" if major.minor match, otherwise "minor".
func classifyUpdate(current, target string) string {
	cv, cerr := semver.Parse(current)
	tv, terr := semver.Parse(target)
	if cerr != nil || terr != nil {
		return i.UpdateTypeUnknown
	}
	return i.UpdateType(cv, tv)
}

// buildRequest constructs the proposal request with system prompt, metadata, and readiness data.
func buildRequest(systemPrompt, current, target, channel, updateType, targetType string,
	updates []configv1.Release, readinessJSON string) string {

	var b strings.Builder

	if systemPrompt != "" {
		b.WriteString(systemPrompt)
		b.WriteString("\n\n---\n\n")
	}

	_, _ = fmt.Fprintf(&b, "Current version: OCP %s\n", current)
	_, _ = fmt.Fprintf(&b, "Target version: OCP %s\n", target)
	_, _ = fmt.Fprintf(&b, "Channel: %s\n", channel)
	_, _ = fmt.Fprintf(&b, "Update type: %s\n", updateType)
	_, _ = fmt.Fprintf(&b, "Update path: %s\n\n", targetType)

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
					_, _ = fmt.Fprintf(&b, "  - %s (errata: %s)\n", u.Version, u.URL)
				} else {
					_, _ = fmt.Fprintf(&b, "  - %s\n", u.Version)
				}
				count++
				if count >= 5 {
					remaining := len(updates) - count - 1
					if remaining > 0 {
						_, _ = fmt.Fprintf(&b, "  ... and %d more\n", remaining)
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

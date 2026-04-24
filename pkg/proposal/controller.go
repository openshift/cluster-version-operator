package proposal

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/blang/semver/v4"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"

	corev1 "k8s.io/api/core/v1"
	kerrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	kutilerrors "k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/client-go/util/workqueue"
	"k8s.io/klog/v2"

	configv1 "github.com/openshift/api/config/v1"

	i "github.com/openshift/cluster-version-operator/pkg/internal"
	proposalv1alpha1 "github.com/openshift/cluster-version-operator/pkg/proposal/api/v1alpha1"
)

type Controller struct {
	queueKey          string
	queue             workqueue.TypedRateLimitingInterface[any]
	updatesGetterFunc UpdatesGetterFunc
	client            ctrlruntimeclient.Client
	cvGetterFunc      cvGetterFunc
}

const controllerName = "proposal-lifecycle-controller"

type UpdatesGetterFunc func() ([]configv1.Release, []configv1.ConditionalUpdate, error)

type cvGetterFunc func(name string) (*configv1.ClusterVersion, error)

// NewController returns Controller to manage Proposals.
// It monitors available and conditional updates, and creates a LightspeedProposal for every target version of them.
// It expires (and replace) any previous LightspeedProposals owned by the CVO after 24h.
// It deletes any CVO-owned LightspeedProposals (without replacement) that are associated with target releases
// that are no longer supported next-hop options (e.g. because a channel change or cluster update), but preserves
// LightspeedProposals associated with versions in the ClusterVersion status.history (history already has its own
// garbage-collection).
func NewController(updatesGetterFunc UpdatesGetterFunc, client ctrlruntimeclient.Client, cvGetterFunc cvGetterFunc) *Controller {
	return &Controller{
		queueKey: fmt.Sprintf("ClusterVersionOperator/%s", controllerName),
		queue: workqueue.NewTypedRateLimitingQueueWithConfig[any](
			workqueue.DefaultTypedControllerRateLimiter[any](),
			workqueue.TypedRateLimitingQueueConfig[any]{Name: controllerName}),
		updatesGetterFunc: updatesGetterFunc,
		client:            client,
		cvGetterFunc:      cvGetterFunc,
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

	proposals := getProposals(updates, conditionalUpdates)

	var errs []error

	cv, err := c.cvGetterFunc(i.DefaultClusterVersionName)
	if err != nil {
		klog.V(i.Normal).Infof("Failed to get ClusterVersion %s: %v", i.DefaultClusterVersionName, err)
		errs = append(errs, err)
	} else if err := deleteProposals(c.client, updates, conditionalUpdates, cv.Status.History); err != nil {
		errs = append(errs, err)
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
			if expired(existing) {
				err := c.client.Delete(ctx, existing)
				if err != nil && !kerrors.IsNotFound(err) {
					klog.V(i.Normal).Infof("Failed to delete proposal %s/%s: %v", proposal.Namespace, proposal.Name, err)
					errs = append(errs, err)
					continue
				}
			} else {
				klog.V(i.Debug).Infof("The existing proposal %s/%s is not expired", proposal.Namespace, proposal.Name)
				continue
			}
		}

		if c.client.Create(ctx, proposal) != nil {
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

// TODO:
func expired(_ *proposalv1alpha1.Proposal) bool {
	return false
}

// TODO:
func deleteProposals(_ ctrlruntimeclient.Client, _ []configv1.Release, _ []configv1.ConditionalUpdate, _ []configv1.UpdateHistory) error {
	return nil
}

// TODO: make it real
func getProposals(_ []configv1.Release, _ []configv1.ConditionalUpdate) []*proposalv1alpha1.Proposal {
	return []*proposalv1alpha1.Proposal{
		{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-proposal",
				Namespace: i.DefaultCVONamespace,
			},
			// Feed required fields only to pass API server validation
			// The workflow does not exist but that should cause no trouble because no controller watches it before lightspeed-operator is installed on the cluster
			Spec: proposalv1alpha1.ProposalSpec{
				Request: "some-request",
				WorkflowRef: corev1.LocalObjectReference{
					Name: "ota-advisory",
				},
			},
		},
	}
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

const (
	updateKindRecommended = "recommended"
	updateKindConditional = "conditional"

	updateTypeZStream = "z-stream"
	updateTypeMinor   = "minor"
	updateTypeUnknown = "unknown"
)

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

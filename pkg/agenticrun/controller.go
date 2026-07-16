package agenticrun

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/blang/semver/v4"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"

	corev1 "k8s.io/api/core/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	kerrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	kutilerrors "k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/util/workqueue"
	"k8s.io/klog/v2"

	configv1 "github.com/openshift/api/config/v1"
	agenticrunv1alpha1 "github.com/openshift/lightspeed-agentic-operator/api/v1alpha1"

	i "github.com/openshift/cluster-version-operator/pkg/internal"
	"github.com/openshift/cluster-version-operator/pkg/readiness"
)

//go:embed analysis_schema.json
var analysisSchemaJSON []byte

func analysisOutputSchema() *apiextensionsv1.JSONSchemaProps {
	schema := &apiextensionsv1.JSONSchemaProps{}
	if err := json.Unmarshal(analysisSchemaJSON, schema); err != nil {
		panic(fmt.Sprintf("failed to unmarshal embedded analysis schema: %v", err))
	}
	return schema
}

type Controller struct {
	queueKey              string
	queue                 workqueue.TypedRateLimitingInterface[any]
	updatesGetterFunc     updatesGetterFunc
	client                ctrlruntimeclient.Client
	dynamicClient         dynamic.Interface
	cvGetterFunc          cvGetterFunc
	configMapGetterFunc   configMapGetterFunc
	getCurrentVersionFunc getCurrentVersionFunc
	config                Config
}

const controllerName = "agenticrun-lifecycle-controller"

type updatesGetterFunc func() ([]configv1.Release, []configv1.ConditionalUpdate, error)

type cvGetterFunc func(name string) (*configv1.ClusterVersion, error)

type getCurrentVersionFunc func() string

type configMapGetterFunc func(ctx context.Context, namespace, name string, opts metav1.GetOptions) (*corev1.ConfigMap, error)

// NewController returns Controller to manage AgenticRuns.
// It monitors available and conditional updates, and creates an AgenticRun for every target version of them.
// It expires (and replaces) any previous AgenticRuns owned by the CVO after 24h.
// It deletes any CVO-owned AgenticRuns (without replacement) that are associated with target releases
// that are no longer supported next-hop options (e.g. because a channel change or cluster update), but preserves
// AgenticRuns associated with versions in the ClusterVersion status.history (history already has its own
// garbage-collection).
func NewController(
	updatesGetterFunc updatesGetterFunc,
	client ctrlruntimeclient.Client,
	dynamicClient dynamic.Interface,
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
		dynamicClient:         dynamicClient,
		cvGetterFunc:          cvGetterFunc,
		configMapGetterFunc:   configMapGetterFunc,
		getCurrentVersionFunc: getCurrentVersionFunc,
		config:                DefaultConfig(),
	}
}

// Config holds configuration for agentic run creation.
type Config struct {
	Namespace       string
	PromptConfigMap string // ConfigMap name containing the system prompt
	SkillsImage     string // OCI image containing agentic skills
}

// DefaultConfig returns the default configuration, checking env vars for overrides.
func DefaultConfig() Config {
	return Config{
		Namespace:       envOrDefault("LIGHTSPEED_AGENTIC_RUN_NAMESPACE", "openshift-lightspeed"),
		PromptConfigMap: envOrDefault("LIGHTSPEED_PROMPT_CONFIGMAP", "cluster-update-advisory-prompt"),
		SkillsImage:     envOrDefault("LIGHTSPEED_SKILLS_IMAGE", "quay.io/openshift/ci:ocp_5.0_agentic-skills"),
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

	currentVersion := c.getCurrentVersionFunc()

	var errs []error
	if err := deleteAgenticRuns(ctx, c.client, updates, conditionalUpdates, cv.Status.History, currentVersion); err != nil {
		errs = append(errs, err)
	}

	// Don't create agentic runs while CVO is reconciling — readiness data would
	// reflect the transient reconciliation state, not actual cluster health.
	for _, cond := range cv.Status.Conditions {
		if cond.Type == "Progressing" && cond.Status == configv1.ConditionTrue {
			klog.V(i.Normal).Infof("Skipping agentic run creation: cluster is progressing (%s)", cond.Message)
			return kutilerrors.NewAggregate(errs)
		}
	}

	if len(updates) == 0 && len(conditionalUpdates) == 0 {
		return kutilerrors.NewAggregate(errs)
	}

	var prompt string
	promptConfigMap, err := c.configMapGetterFunc(ctx, c.config.Namespace, c.config.PromptConfigMap, metav1.GetOptions{})
	if err != nil {
		klog.V(i.Normal).Infof("Failed to get prompt ConfigMap %s/%s: %v", c.config.Namespace, c.config.PromptConfigMap, err)
		errs = append(errs, fmt.Errorf("failed to get prompt ConfigMap %s/%s: %w", c.config.Namespace, c.config.PromptConfigMap, err))
		return kutilerrors.NewAggregate(errs)
	}
	promptKey := "prompt"
	if v, ok := promptConfigMap.Data[promptKey]; ok {
		prompt = v
	} else {
		klog.V(i.Normal).Infof("ConfigMap %s/%s has no key %s in data", c.config.Namespace, c.config.PromptConfigMap, promptKey)
		errs = append(errs, fmt.Errorf("failed to get key/%s from ConfigMap %s/%s", promptKey, c.config.Namespace, c.config.PromptConfigMap))
		return kutilerrors.NewAggregate(errs)
	}

	agenticRuns, err := getAgenticRuns(ctx, c.dynamicClient, updates, conditionalUpdates, c.config.Namespace, currentVersion, cv.Spec.Channel, prompt, c.config.SkillsImage)
	if err != nil {
		klog.V(i.Normal).Infof("Getting agentic runs hit an error: %v", err)
		return kutilerrors.NewAggregate(append(errs, err))
	}

	for _, agenticRun := range agenticRuns {
		existing := &agenticrunv1alpha1.AgenticRun{}
		err := c.client.Get(ctx, ctrlruntimeclient.ObjectKey{Name: agenticRun.Name, Namespace: agenticRun.Namespace}, existing)
		if err != nil {
			if !kerrors.IsNotFound(err) {
				klog.V(i.Normal).Infof("Failed to get agentic run %s/%s: %v", agenticRun.Namespace, agenticRun.Name, err)
				errs = append(errs, err)
				continue
			}
		} else {
			if !ownedByCVO(existing) {
				klog.V(i.Normal).Infof("Ignored agentic run %s/%s not owned by CVO", agenticRun.Namespace, agenticRun.Name)
				continue
			}
			if expired(existing) {
				if err := deleteAgenticRun(ctx, c.client, existing, "expired"); err != nil {
					errs = append(errs, err)
					continue
				}
			} else {
				klog.V(i.Debug).Infof("The existing agentic run %s/%s is not expired", agenticRun.Namespace, agenticRun.Name)
				continue
			}
		}

		if err := c.client.Create(ctx, agenticRun); err != nil {
			if !kerrors.IsAlreadyExists(err) {
				klog.V(i.Normal).Infof("Failed to create agentic run %s/%s: %v", agenticRun.Namespace, agenticRun.Name, err)
				errs = append(errs, err)
			} else {
				klog.V(i.Debug).Infof("The agentic run %s/%s existed already", agenticRun.Namespace, agenticRun.Name)
			}
		} else {
			klog.V(i.Debug).Infof("Created agentic run %s/%s", agenticRun.Namespace, agenticRun.Name)
		}
	}

	return kutilerrors.NewAggregate(errs)
}

func ownedByCVO(p *agenticrunv1alpha1.AgenticRun) bool {
	if p == nil {
		return false
	}
	return p.Labels[labelKeySource] == labelValueSource
}

func expired(p *agenticrunv1alpha1.AgenticRun) bool {
	if p == nil {
		return false
	}
	return time.Now().After(p.CreationTimestamp.Add(agenticRunExpiration))
}

func deleteAgenticRuns(ctx context.Context, client ctrlruntimeclient.Client, availableUpdates []configv1.Release, conditionalUpdates []configv1.ConditionalUpdate, history []configv1.UpdateHistory, currentVersion string) error {
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

	list := &agenticrunv1alpha1.AgenticRunList{}
	if err := client.List(ctx, list, ctrlruntimeclient.MatchingLabels(CVOAgenticRunLabels)); err != nil {
		return fmt.Errorf("failed to list agentic runs: %w", err)
	}
	var errs []error
	for _, agenticRun := range list.Items {
		if !ownedByCVO(&agenticRun) {
			klog.V(i.Debug).Infof("Keeping agentic run %s/%s not owned by CVO", agenticRun.Namespace, agenticRun.Name)
			continue
		}
		cv, cvOk := agenticRun.Labels[labelKeyCurrentVersion]
		tv, tvOk := agenticRun.Labels[labelKeyTargetVersion]
		if cvOk && tvOk && cv == currentVersion && targets.Has(tv) {
			klog.V(i.Debug).Infof("Keeping relevant agentic run %s/%s from %s to %s", agenticRun.Namespace, agenticRun.Name, cv, tv)
			continue
		}
		if tvOk && associatedWithHistory.Has(tv) {
			klog.V(i.Debug).Infof("Keeping agentic run %s/%s for a version %s associated with history", agenticRun.Namespace, agenticRun.Name, tv)
			continue
		}
		err := deleteAgenticRun(ctx, client, &agenticRun, "irrelevant")
		if err != nil {
			errs = append(errs, err)
		}
	}

	return kutilerrors.NewAggregate(errs)
}

func deleteAgenticRun(ctx context.Context, client ctrlruntimeclient.Client, agenticRun *agenticrunv1alpha1.AgenticRun, adjective string) error {
	if agenticRun == nil {
		return nil
	}
	klog.V(i.Normal).Infof("Deleting %s agentic run %s/%s ...", adjective, agenticRun.Namespace, agenticRun.Name)
	err := client.Delete(ctx, agenticRun)
	if err == nil {
		klog.V(i.Normal).Infof("Deleted %s agentic run %s/%s", adjective, agenticRun.Namespace, agenticRun.Name)
		return nil
	}

	if !kerrors.IsNotFound(err) {
		klog.V(i.Normal).Infof("Failed to delete %s agentic run %s/%s: %v", adjective, agenticRun.Namespace, agenticRun.Name, err)
		return err
	}

	klog.V(i.Normal).Infof("Failed to delete not-found agentic run %s/%s", agenticRun.Namespace, agenticRun.Name)
	return nil
}

func getAgenticRuns(
	ctx context.Context,
	dynamicClient dynamic.Interface,
	availableUpdates []configv1.Release,
	conditionalUpdates []configv1.ConditionalUpdate,
	namespace string,
	currentVersion, channel,
	systemPrompt string,
	skillsImage string,
) ([]*agenticrunv1alpha1.AgenticRun, error) {
	// TODO: Only 2 of 9 readiness checks (api_deprecations, olm_lifecycle) use the target version.
	// The other 7 query cluster-wide state identical across targets. For clusters with many available
	// updates, split into target-independent checks (run once) and target-dependent checks (run per
	// target) to reduce redundant API calls.
	var errs []error
	var agenticRuns []*agenticrunv1alpha1.AgenticRun
	for _, au := range availableUpdates {
		targetVersion := au.Version
		readinessJSON := runReadinessJSON(ctx, dynamicClient, currentVersion, targetVersion)
		if agenticRun, err := getAgenticRun(namespace, currentVersion, targetVersion, channel, updateKindRecommended, systemPrompt, readinessJSON, availableUpdates, skillsImage); err != nil {
			errs = append(errs, err)
			continue
		} else {
			agenticRuns = append(agenticRuns, agenticRun)
		}
	}

	for _, cu := range conditionalUpdates {
		targetVersion := cu.Release.Version
		readinessJSON := runReadinessJSON(ctx, dynamicClient, currentVersion, targetVersion)
		if agenticRun, err := getAgenticRun(namespace, currentVersion, targetVersion, channel, updateKindConditional, systemPrompt, readinessJSON, availableUpdates, skillsImage); err != nil {
			errs = append(errs, err)
			continue
		} else {
			agenticRuns = append(agenticRuns, agenticRun)
		}
	}

	return agenticRuns, kutilerrors.NewAggregate(errs)
}

func getAgenticRun(namespace, currentVersion, targetVersion, channel, updateKind, systemPrompt, readinessJSON string, availableUpdates []configv1.Release, skillsImage string) (*agenticrunv1alpha1.AgenticRun, error) {

	var errs []error
	for _, v := range []string{currentVersion, targetVersion} {
		if _, err := semver.Parse(v); err != nil {
			errs = append(errs, fmt.Errorf("invalid version %s: %w", v, err))
		}
	}
	if len(errs) > 0 {
		return nil, kutilerrors.NewAggregate(errs)
	}

	name := agenticRunName(currentVersion, targetVersion)
	updateType := classifyUpdate(currentVersion, targetVersion)
	request := buildRequest(systemPrompt, currentVersion, targetVersion, channel, updateType, updateKind, availableUpdates, readinessJSON)
	return &agenticrunv1alpha1.AgenticRun{
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
		Spec: agenticrunv1alpha1.AgenticRunSpec{
			Request: request,
			// CVO consumes some Agent maintained by the cluster admin
			Analysis: agenticrunv1alpha1.AgenticRunStep{
				Agent: "smart",
			},
			Tools: agenticrunv1alpha1.ToolsSpec{
				Skills: []agenticrunv1alpha1.SkillsSource{
					{
						Image: skillsImage,
						Paths: []string{
							"/skills/cluster-update/update-advisor",
							"/skills/cluster-update/product-lifecycle",
						},
					},
				},
			},
			AnalysisOutput: agenticrunv1alpha1.AnalysisOutput{
				Mode:   agenticrunv1alpha1.AnalysisOutputModeMinimal,
				Schema: analysisOutputSchema(),
			},
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

// agenticRunName generates a deterministic agentic run name from the version pair.
func agenticRunName(current, target string) string {
	return toDNS1035(fmt.Sprintf("ota-%s-to-%s", current, target))
}

var alphanumericRegex = regexp.MustCompile(`[^a-z0-9]+`)

// https://kubernetes.io/docs/concepts/overview/working-with-objects/names/#rfc-1035-label-names
func toDNS1035(name string) string {
	// Convert to lowercase
	ret := strings.ToLower(name)

	// Replace any non-alphanumeric character with a hyphen
	ret = alphanumericRegex.ReplaceAllString(ret, "-")

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

	agenticRunExpiration = 24 * time.Hour
)

var (
	CVOAgenticRunLabels = map[string]string{labelKeySource: labelValueSource}
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

func runReadinessJSON(ctx context.Context, dynamicClient dynamic.Interface, currentVersion, targetVersion string) string {
	if dynamicClient == nil {
		klog.V(i.Normal).Infof("Dynamic client is nil; skipping readiness checks for %s -> %s", currentVersion, targetVersion)
		return "{}"
	}
	output := readiness.RunAll(ctx, dynamicClient, currentVersion, targetVersion)
	data, err := json.Marshal(output)
	if err != nil {
		klog.V(i.Normal).Infof("Failed to marshal readiness output for %s -> %s: %v", currentVersion, targetVersion, err)
		return "{}"
	}
	return string(data)
}

// buildRequest constructs the agentic run request with system prompt, metadata, and readiness data.
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

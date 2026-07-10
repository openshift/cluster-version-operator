package agenticrun

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	corev1 "k8s.io/api/core/v1"
	kerrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	kutilerrors "k8s.io/apimachinery/pkg/util/errors"
	dynamicfake "k8s.io/client-go/dynamic/fake"

	configv1 "github.com/openshift/api/config/v1"
	agenticrunv1alpha1 "github.com/openshift/lightspeed-agentic-operator/api/v1alpha1"

	"github.com/openshift/cluster-version-operator/pkg/internal"
	"github.com/openshift/cluster-version-operator/pkg/readiness"
)

func init() {
	if err := internal.AddSchemes(); err != nil {
		panic(err)
	}
}

func TestController_Sync(t *testing.T) {
	tests := []struct {
		name              string
		updatesGetterFunc updatesGetterFunc
		client            ctrlruntimeclient.Client
		cvGetterFunc      cvGetterFunc
		expected          error
		verifyFunc        func(client ctrlruntimeclient.Client) error
	}{
		{
			name: "basic case",
			updatesGetterFunc: func() ([]configv1.Release, []configv1.ConditionalUpdate, error) {
				return []configv1.Release{
					{
						Version: "5.0.0-ec.0",
					},
				}, nil, nil
			},
			cvGetterFunc: func(_ string) (*configv1.ClusterVersion, error) {
				return &configv1.ClusterVersion{
					ObjectMeta: metav1.ObjectMeta{
						Name: "version",
					},
					Spec: configv1.ClusterVersionSpec{
						Channel: "stable-5.0",
					},
				}, nil
			},
			client: fake.NewClientBuilder().Build(),
			verifyFunc: func(client ctrlruntimeclient.Client) error {
				agenticRuns := &agenticrunv1alpha1.AgenticRunList{}
				if err := client.List(context.Background(), agenticRuns); err != nil {
					return err
				}
				expect := &agenticrunv1alpha1.AgenticRunList{
					Items: []agenticrunv1alpha1.AgenticRun{
						{
							ObjectMeta: metav1.ObjectMeta{
								Name:      "ota-4-22-1-to-5-0-0-ec-0",
								Namespace: "openshift-lightspeed",
								Labels: map[string]string{
									"agentic.openshift.io/current-version": "4.22.1",
									"agentic.openshift.io/source":          "cluster-version-operator",
									"agentic.openshift.io/target-version":  "5.0.0-ec.0",
									"agentic.openshift.io/update-type":     "Major",
								},
							},
							Spec: agenticrunv1alpha1.AgenticRunSpec{
								Request: `prompt-abc

---

Current version: OCP 4.22.1
Target version: OCP 5.0.0-ec.0
Channel: stable-5.0
Update type: Major
Update path: Recommended

## Cluster Readiness Data

` + "```json\n" +
									`{}` + "\n```\n",
								Analysis: agenticrunv1alpha1.AgenticRunStep{
									Agent: "smart",
								},
								Tools: agenticrunv1alpha1.ToolsSpec{
									Skills: []agenticrunv1alpha1.SkillsSource{
										{
											Image: "quay.io/openshift/ci:ocp_5.0_agentic-skills",
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
						},
					}}
				if diff := cmp.Diff(expect, agenticRuns, cmpopts.IgnoreFields(metav1.ObjectMeta{}, "ResourceVersion")); diff != "" {
					return fmt.Errorf("unexpected AgenticRunList (-want, +got) = \n%v", diff)
				}
				return nil
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := NewController(tt.updatesGetterFunc, tt.client, nil, tt.cvGetterFunc, func(_ context.Context, namespace, name string, _ metav1.GetOptions) (*corev1.ConfigMap, error) {
				if namespace == "openshift-lightspeed" && name == "cluster-update-advisory-prompt" {
					return &corev1.ConfigMap{
						Data: map[string]string{
							"prompt": "prompt-abc",
							"foo":    "bar",
						},
					}, nil
				}
				return nil, fmt.Errorf("ConfigMap %s not found in namespace %s", name, namespace)
			}, func() string {
				return "4.22.1"
			})
			actual := c.Sync(context.Background(), tt.name)
			if diff := cmp.Diff(tt.expected, actual, cmp.Transformer("Error", func(e error) string {
				if e == nil {
					return ""
				}
				return e.Error()
			})); diff != "" {
				t.Errorf("unexpected error (-want +got):\n%s", diff)
			}
			if tt.verifyFunc != nil {
				if err := tt.verifyFunc(tt.client); err != nil {
					t.Errorf("unexpected error: %v", err)
				}
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
		{name: "z-stream", current: "4.15.1", target: "4.15.3", expected: "Patch"},
		{name: "minor", current: "4.15.1", target: "4.16.0", expected: "Minor"},
		{name: "major", current: "4.15.1", target: "5.0.0", expected: "Major"},
		{name: "invalid current", current: "bad", target: "4.15.0", expected: "Unknown"},
		{name: "invalid target", current: "4.15.0", target: "bad", expected: "Unknown"},
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

func TestAgenticRunName(t *testing.T) {
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
			got := agenticRunName(tt.current, tt.target)
			if got != tt.expected {
				t.Errorf("agenticRunName(%q, %q) = %q, want %q", tt.current, tt.target, got, tt.expected)
			}
		})
	}
}

func Test_labelValueFromVersion(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"4.15.1", "4.15.1"},
		{"4.15.1-a-very-long-version-string-that-is-too-long-long-version-string-that-is-too-long", "4.15.1-a-very-long-version-string-that-is-too-long-long-versxxx"},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := labelValueFromVersion(tt.input)
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
		request := buildRequest("", "4.15.3", "4.16.0", "stable-4.16", "minor", "Conditional", updates, "")
		if !strings.Contains(request, "WARNING") {
			t.Error("conditional target should have warning")
		}
		if !strings.Contains(request, "CONDITIONAL update") {
			t.Error("conditional target should mention CONDITIONAL")
		}
	})

	t.Run("readiness JSON embedded", func(t *testing.T) {
		request := buildRequest("", "4.15.3", "4.16.0", "stable-4.16", "minor", "Recommended", updates, `{"checks":{},"meta":{}}`)
		if !strings.Contains(request, "## Cluster Readiness Data") {
			t.Error("request should contain readiness data header")
		}
		if !strings.Contains(request, `{"checks":{},"meta":{}}`) {
			t.Error("request should contain readiness JSON")
		}
	})
}

func TestDeleteAgenticRuns(t *testing.T) {
	tests := []struct {
		name                string
		availableUpdates    []configv1.Release
		conditionalUpdates  []configv1.ConditionalUpdate
		history             []configv1.UpdateHistory
		currentVersion      string
		existingAgenticRuns []agenticrunv1alpha1.AgenticRun
		expectedDeleted     []string
		expectedKept        []string
	}{
		{
			name: "keeps relevant agentic runs matching current version and target",
			availableUpdates: []configv1.Release{
				{Version: "4.16.0"},
				{Version: "4.16.1"},
			},
			currentVersion: "4.15.3",
			existingAgenticRuns: []agenticrunv1alpha1.AgenticRun{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "proposal-4-15-3-to-4-16-0",
						Namespace: "openshift-lightspeed",
						Labels: map[string]string{
							labelKeySource:         labelValueSource,
							labelKeyCurrentVersion: "4.15.3",
							labelKeyTargetVersion:  "4.16.0",
						},
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "proposal-4-15-3-to-4-16-1",
						Namespace: "openshift-lightspeed",
						Labels: map[string]string{
							labelKeySource:         labelValueSource,
							labelKeyCurrentVersion: "4.15.3",
							labelKeyTargetVersion:  "4.16.1",
						},
					},
				},
			},
			expectedKept:    []string{"proposal-4-15-3-to-4-16-0", "proposal-4-15-3-to-4-16-1"},
			expectedDeleted: []string{},
		},
		{
			name: "deletes agentic runs with outdated current version",
			availableUpdates: []configv1.Release{
				{Version: "4.16.0"},
			},
			currentVersion: "4.15.3",
			existingAgenticRuns: []agenticrunv1alpha1.AgenticRun{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "proposal-4-15-2-to-4-16-0",
						Namespace: "openshift-lightspeed",
						Labels: map[string]string{
							labelKeySource:         labelValueSource,
							labelKeyCurrentVersion: "4.15.2",
							labelKeyTargetVersion:  "4.16.0",
						},
					},
				},
			},
			expectedKept:    []string{},
			expectedDeleted: []string{"proposal-4-15-2-to-4-16-0"},
		},
		{
			name: "deletes agentic runs for targets no longer in available updates",
			availableUpdates: []configv1.Release{
				{Version: "4.16.1"},
			},
			currentVersion: "4.15.3",
			existingAgenticRuns: []agenticrunv1alpha1.AgenticRun{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "proposal-4-15-3-to-4-16-0",
						Namespace: "openshift-lightspeed",
						Labels: map[string]string{
							labelKeySource:         labelValueSource,
							labelKeyCurrentVersion: "4.15.3",
							labelKeyTargetVersion:  "4.16.0",
						},
					},
				},
			},
			expectedKept:    []string{},
			expectedDeleted: []string{"proposal-4-15-3-to-4-16-0"},
		},
		{
			name: "keeps agentic runs associated with history",
			availableUpdates: []configv1.Release{
				{Version: "4.16.2"},
			},
			history: []configv1.UpdateHistory{
				{Version: "4.16.1"},
			},
			currentVersion: "4.16.1",
			existingAgenticRuns: []agenticrunv1alpha1.AgenticRun{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "proposal-old-to-4-16-1",
						Namespace: "openshift-lightspeed",
						Labels: map[string]string{
							labelKeySource:         labelValueSource,
							labelKeyCurrentVersion: "4.15.0",
							labelKeyTargetVersion:  "4.16.1",
						},
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "proposal-old-to-4-15-3",
						Namespace: "openshift-lightspeed",
						Labels: map[string]string{
							labelKeySource:         labelValueSource,
							labelKeyCurrentVersion: "4.15.0",
							labelKeyTargetVersion:  "4.15.3",
						},
					},
				},
			},
			expectedKept:    []string{"proposal-old-to-4-16-1"},
			expectedDeleted: []string{"proposal-old-to-4-15-3"},
		},
		{
			name: "keeps agentic runs not owned by CVO",
			availableUpdates: []configv1.Release{
				{Version: "4.16.0"},
			},
			currentVersion: "4.15.3",
			existingAgenticRuns: []agenticrunv1alpha1.AgenticRun{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "user-created-proposal",
						Namespace: "openshift-lightspeed",
						Labels: map[string]string{
							labelKeySource:         "manual",
							labelKeyCurrentVersion: "4.14.0",
							labelKeyTargetVersion:  "4.15.0",
						},
					},
				},
			},
			expectedKept:    []string{"user-created-proposal"},
			expectedDeleted: []string{},
		},
		{
			name: "handles conditional updates",
			conditionalUpdates: []configv1.ConditionalUpdate{
				{Release: configv1.Release{Version: "4.16.2"}},
			},
			currentVersion: "4.15.3",
			existingAgenticRuns: []agenticrunv1alpha1.AgenticRun{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "proposal-4-15-3-to-4-16-2",
						Namespace: "openshift-lightspeed",
						Labels: map[string]string{
							labelKeySource:         labelValueSource,
							labelKeyCurrentVersion: "4.15.3",
							labelKeyTargetVersion:  "4.16.2",
						},
					},
				},
			},
			expectedKept:    []string{"proposal-4-15-3-to-4-16-2"},
			expectedDeleted: []string{},
		},
		{
			name: "deletes agentic runs with missing labels",
			availableUpdates: []configv1.Release{
				{Version: "4.16.0"},
			},
			currentVersion: "4.15.3",
			existingAgenticRuns: []agenticrunv1alpha1.AgenticRun{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "proposal-missing-labels",
						Namespace: "openshift-lightspeed",
						Labels: map[string]string{
							labelKeySource: labelValueSource,
							// Missing current and target version labels
						},
					},
				},
			},
			expectedKept:    []string{},
			expectedDeleted: []string{"proposal-missing-labels"},
		},
		{
			name:             "handles empty updates and history",
			availableUpdates: []configv1.Release{},
			currentVersion:   "4.15.3",
			existingAgenticRuns: []agenticrunv1alpha1.AgenticRun{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "proposal-4-15-3-to-4-16-0",
						Namespace: "openshift-lightspeed",
						Labels: map[string]string{
							labelKeySource:         labelValueSource,
							labelKeyCurrentVersion: "4.15.3",
							labelKeyTargetVersion:  "4.16.0",
						},
					},
				},
			},
			expectedKept:    []string{},
			expectedDeleted: []string{"proposal-4-15-3-to-4-16-0"},
		},
		{
			name: "mixed scenario: keep some, delete some",
			availableUpdates: []configv1.Release{
				{Version: "4.16.1"},
			},
			conditionalUpdates: []configv1.ConditionalUpdate{
				{Release: configv1.Release{Version: "4.16.2"}},
			},
			history: []configv1.UpdateHistory{
				{Version: "4.15.3"},
				{Version: "4.16.0"},
			},
			currentVersion: "4.16.0",
			existingAgenticRuns: []agenticrunv1alpha1.AgenticRun{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "keep-relevant",
						Namespace: "openshift-lightspeed",
						Labels: map[string]string{
							labelKeySource:         labelValueSource,
							labelKeyCurrentVersion: "4.16.0",
							labelKeyTargetVersion:  "4.16.2",
						},
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "keep-in-history",
						Namespace: "openshift-lightspeed",
						Labels: map[string]string{
							labelKeySource:         labelValueSource,
							labelKeyCurrentVersion: "4.15.0",
							labelKeyTargetVersion:  "4.15.3",
						},
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "delete-outdated",
						Namespace: "openshift-lightspeed",
						Labels: map[string]string{
							labelKeySource:         labelValueSource,
							labelKeyCurrentVersion: "4.15.0",
							labelKeyTargetVersion:  "4.15.10",
						},
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "keep-user-created",
						Namespace: "openshift-lightspeed",
						Labels: map[string]string{
							labelKeySource:         "manual",
							labelKeyCurrentVersion: "4.14.0",
							labelKeyTargetVersion:  "4.15.0",
						},
					},
				},
			},
			expectedKept:    []string{"keep-relevant", "keep-in-history", "keep-user-created"},
			expectedDeleted: []string{"delete-outdated"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()

			// Create fake client with existing proposals
			clientBuilder := fake.NewClientBuilder()
			for _, p := range tt.existingAgenticRuns {
				clientBuilder.WithObjects(&p)
			}
			client := clientBuilder.Build()

			err := deleteAgenticRuns(ctx, client, tt.availableUpdates, tt.conditionalUpdates, tt.history, tt.currentVersion)
			if err != nil {
				t.Errorf("deleteAgenticRuns() returned unexpected error: %v", err)
			}

			for _, name := range tt.expectedKept {
				agenticRun := &agenticrunv1alpha1.AgenticRun{}
				err := client.Get(ctx, ctrlruntimeclient.ObjectKey{Name: name, Namespace: "openshift-lightspeed"}, agenticRun)
				if err != nil {
					t.Errorf("expected agentic run %s to be kept, but got error: %v", name, err)
				}
			}

			for _, name := range tt.expectedDeleted {
				agenticRun := &agenticrunv1alpha1.AgenticRun{}
				err := client.Get(ctx, ctrlruntimeclient.ObjectKey{Name: name, Namespace: "openshift-lightspeed"}, agenticRun)
				if err == nil {
					t.Errorf("expected agentic run %s to be deleted, but it still exists", name)
				}
			}
		})
	}
}

func TestDeleteAgenticRun(t *testing.T) {
	tests := []struct {
		name            string
		agenticRun      *agenticrunv1alpha1.AgenticRun
		adjective       string
		setupClient     func() ctrlruntimeclient.Client
		expect          error
		shouldBeDeleted bool
	}{
		{
			name: "successfully deletes existing agentic run",
			agenticRun: &agenticrunv1alpha1.AgenticRun{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-agenticrun",
					Namespace: "openshift-lightspeed",
				},
			},
			adjective: "expired",
			setupClient: func() ctrlruntimeclient.Client {
				return fake.NewClientBuilder().WithObjects(&agenticrunv1alpha1.AgenticRun{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test-agenticrun",
						Namespace: "openshift-lightspeed",
					},
				}).Build()
			},
			shouldBeDeleted: true,
		},
		{
			name: "handles not found error gracefully",
			agenticRun: &agenticrunv1alpha1.AgenticRun{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "nonexistent-agenticrun",
					Namespace: "openshift-lightspeed",
				},
			},
			adjective: "irrelevant",
			setupClient: func() ctrlruntimeclient.Client {
				return fake.NewClientBuilder().Build()
			},
			shouldBeDeleted: true,
		},
		{
			name:      "handles nil agentic run",
			adjective: "nil",
			setupClient: func() ctrlruntimeclient.Client {
				return fake.NewClientBuilder().Build()
			},
			shouldBeDeleted: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			client := tt.setupClient()

			err := deleteAgenticRun(ctx, client, tt.agenticRun, tt.adjective)

			if diff := cmp.Diff(tt.expect, err, cmp.Transformer("Error", func(e error) string {
				if e == nil {
					return ""
				}
				return e.Error()
			})); diff != "" {
				t.Errorf("unexpected error (-want +got):\n%s", diff)
			}

			if tt.agenticRun != nil {
				agenticRun := &agenticrunv1alpha1.AgenticRun{}
				err = client.Get(ctx, ctrlruntimeclient.ObjectKey{
					Name:      tt.agenticRun.Name,
					Namespace: tt.agenticRun.Namespace,
				}, agenticRun)
				if tt.shouldBeDeleted {
					if !kerrors.IsNotFound(err) {
						t.Error("expected agentic run to be deleted but it still exists")
					}
				} else {
					if err != nil {
						t.Errorf("expected no error, but got error: %v", err)
					}
				}
			}
		})
	}
}

func TestOwnedByCVO(t *testing.T) {
	tests := []struct {
		name       string
		agenticRun *agenticrunv1alpha1.AgenticRun
		expected   bool
	}{
		{
			name: "nil agentic run",
		},
		{
			name: "agentic run owned by CVO",
			agenticRun: &agenticrunv1alpha1.AgenticRun{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						labelKeySource: labelValueSource,
					},
				},
			},
			expected: true,
		},
		{
			name: "agentic run not owned by CVO",
			agenticRun: &agenticrunv1alpha1.AgenticRun{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						labelKeySource: "manual",
					},
				},
			},
		},
		{
			name: "agentic run with missing labels",
			agenticRun: &agenticrunv1alpha1.AgenticRun{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{},
				},
			},
		},
		{
			name:       "agentic run with nil labels",
			agenticRun: &agenticrunv1alpha1.AgenticRun{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ownedByCVO(tt.agenticRun)
			if result != tt.expected {
				t.Errorf("ownedByCVO() = %v, want %v", result, tt.expected)
			}
		})
	}
}

func TestGetAgenticRuns(t *testing.T) {
	tests := []struct {
		name               string
		availableUpdates   []configv1.Release
		conditionalUpdates []configv1.ConditionalUpdate
		namespace          string
		currentVersion     string
		channel            string
		systemPrompt       string
		expected           []*agenticrunv1alpha1.AgenticRun
		expectError        error
	}{
		{
			name: "available updates only",
			availableUpdates: []configv1.Release{
				{Version: "4.16.0"},
				{Version: "4.16.1"},
			},
			namespace:      "openshift-lightspeed",
			currentVersion: "4.15.3",
			channel:        "stable-4.16",
			systemPrompt:   "Test prompt",
			expected: []*agenticrunv1alpha1.AgenticRun{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "ota-4-15-3-to-4-16-0",
						Namespace: "openshift-lightspeed",
						Labels: map[string]string{
							"agentic.openshift.io/current-version": "4.15.3",
							"agentic.openshift.io/source":          "cluster-version-operator",
							"agentic.openshift.io/target-version":  "4.16.0",
							"agentic.openshift.io/update-type":     "Minor",
						},
					},
					Spec: agenticrunv1alpha1.AgenticRunSpec{
						Request: `Test prompt

---

Current version: OCP 4.15.3
Target version: OCP 4.16.0
Channel: stable-4.16
Update type: Minor
Update path: Recommended

Other recommended versions available:
  - 4.16.1

## Cluster Readiness Data

` + "```json\n" +
							`{}` + "\n```\n",
						Analysis: agenticrunv1alpha1.AgenticRunStep{
							Agent: "smart",
						},
						Tools: agenticrunv1alpha1.ToolsSpec{
							Skills: []agenticrunv1alpha1.SkillsSource{
								{
									Image: "quay.io/openshift/ci:ocp_5.0_agentic-skills",
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
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "ota-4-15-3-to-4-16-1",
						Namespace: "openshift-lightspeed",
						Labels: map[string]string{
							"agentic.openshift.io/current-version": "4.15.3",
							"agentic.openshift.io/source":          "cluster-version-operator",
							"agentic.openshift.io/target-version":  "4.16.1",
							"agentic.openshift.io/update-type":     "Minor",
						},
					},
					Spec: agenticrunv1alpha1.AgenticRunSpec{
						Request: `Test prompt

---

Current version: OCP 4.15.3
Target version: OCP 4.16.1
Channel: stable-4.16
Update type: Minor
Update path: Recommended

Other recommended versions available:
  - 4.16.0

## Cluster Readiness Data

` + "```json\n" +
							`{}` + "\n```\n",
						Analysis: agenticrunv1alpha1.AgenticRunStep{
							Agent: "smart",
						},
						Tools: agenticrunv1alpha1.ToolsSpec{
							Skills: []agenticrunv1alpha1.SkillsSource{
								{
									Image: "quay.io/openshift/ci:ocp_5.0_agentic-skills",
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
				},
			},
		},
		{
			name: "both available and conditional updates",
			availableUpdates: []configv1.Release{
				{Version: "4.16.0"},
				{Version: "4.16.1"},
			},
			conditionalUpdates: []configv1.ConditionalUpdate{
				{Release: configv1.Release{Version: "4.16.2"}},
				{Release: configv1.Release{Version: "4.16.3"}},
			},
			namespace:      "openshift-lightspeed",
			currentVersion: "4.15.3",
			channel:        "stable-4.16",
			expected: []*agenticrunv1alpha1.AgenticRun{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "ota-4-15-3-to-4-16-0",
						Namespace: "openshift-lightspeed",
						Labels: map[string]string{
							"agentic.openshift.io/current-version": "4.15.3",
							"agentic.openshift.io/source":          "cluster-version-operator",
							"agentic.openshift.io/target-version":  "4.16.0",
							"agentic.openshift.io/update-type":     "Minor",
						},
					},
					Spec: agenticrunv1alpha1.AgenticRunSpec{
						Request: `Current version: OCP 4.15.3
Target version: OCP 4.16.0
Channel: stable-4.16
Update type: Minor
Update path: Recommended

Other recommended versions available:
  - 4.16.1

` + "## Cluster Readiness Data\n\n```json\n{}\n```\n",
						Analysis: agenticrunv1alpha1.AgenticRunStep{
							Agent: "smart",
						},
						Tools: agenticrunv1alpha1.ToolsSpec{
							Skills: []agenticrunv1alpha1.SkillsSource{
								{
									Image: "quay.io/openshift/ci:ocp_5.0_agentic-skills",
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
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "ota-4-15-3-to-4-16-1",
						Namespace: "openshift-lightspeed",
						Labels: map[string]string{
							"agentic.openshift.io/current-version": "4.15.3",
							"agentic.openshift.io/source":          "cluster-version-operator",
							"agentic.openshift.io/target-version":  "4.16.1",
							"agentic.openshift.io/update-type":     "Minor",
						},
					},
					Spec: agenticrunv1alpha1.AgenticRunSpec{
						Request: `Current version: OCP 4.15.3
Target version: OCP 4.16.1
Channel: stable-4.16
Update type: Minor
Update path: Recommended

Other recommended versions available:
  - 4.16.0

` + "## Cluster Readiness Data\n\n```json\n{}\n```\n",
						Analysis: agenticrunv1alpha1.AgenticRunStep{
							Agent: "smart",
						},
						Tools: agenticrunv1alpha1.ToolsSpec{
							Skills: []agenticrunv1alpha1.SkillsSource{
								{
									Image: "quay.io/openshift/ci:ocp_5.0_agentic-skills",
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
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "ota-4-15-3-to-4-16-2",
						Namespace: "openshift-lightspeed",
						Labels: map[string]string{
							"agentic.openshift.io/current-version": "4.15.3",
							"agentic.openshift.io/source":          "cluster-version-operator",
							"agentic.openshift.io/target-version":  "4.16.2",
							"agentic.openshift.io/update-type":     "Minor",
						},
					},
					Spec: agenticrunv1alpha1.AgenticRunSpec{
						Request: `Current version: OCP 4.15.3
Target version: OCP 4.16.2
Channel: stable-4.16
Update type: Minor
Update path: Conditional

WARNING: This target version is available as a CONDITIONAL update.
OSUS has flagged known risks that may apply to this cluster.
The assessment MUST evaluate each conditional risk against cluster state.

Other recommended versions available:
  - 4.16.0
  - 4.16.1

` + "## Cluster Readiness Data\n\n```json\n{}\n```\n",
						Analysis: agenticrunv1alpha1.AgenticRunStep{
							Agent: "smart",
						},
						Tools: agenticrunv1alpha1.ToolsSpec{
							Skills: []agenticrunv1alpha1.SkillsSource{
								{
									Image: "quay.io/openshift/ci:ocp_5.0_agentic-skills",
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
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "ota-4-15-3-to-4-16-3",
						Namespace: "openshift-lightspeed",
						Labels: map[string]string{
							"agentic.openshift.io/current-version": "4.15.3",
							"agentic.openshift.io/source":          "cluster-version-operator",
							"agentic.openshift.io/target-version":  "4.16.3",
							"agentic.openshift.io/update-type":     "Minor",
						},
					},
					Spec: agenticrunv1alpha1.AgenticRunSpec{
						Request: `Current version: OCP 4.15.3
Target version: OCP 4.16.3
Channel: stable-4.16
Update type: Minor
Update path: Conditional

WARNING: This target version is available as a CONDITIONAL update.
OSUS has flagged known risks that may apply to this cluster.
The assessment MUST evaluate each conditional risk against cluster state.

Other recommended versions available:
  - 4.16.0
  - 4.16.1

` + "## Cluster Readiness Data\n\n```json\n{}\n```\n",
						Analysis: agenticrunv1alpha1.AgenticRunStep{
							Agent: "smart",
						},
						Tools: agenticrunv1alpha1.ToolsSpec{
							Skills: []agenticrunv1alpha1.SkillsSource{
								{
									Image: "quay.io/openshift/ci:ocp_5.0_agentic-skills",
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
				},
			},
		},
		{
			name:           "empty updates",
			namespace:      "openshift-lightspeed",
			currentVersion: "4.15.3",
			channel:        "stable-4.16",
		},
		{
			name: "invalid version in available update",
			availableUpdates: []configv1.Release{
				{Version: "invalid-version"},
			},
			namespace:      "openshift-lightspeed",
			currentVersion: "4.15.3",
			channel:        "stable-4.16",
			expectError:    kutilerrors.NewAggregate([]error{fmt.Errorf("invalid version invalid-version: No Major.Minor.Patch elements found")}),
		},
		{
			name: "invalid current version",
			availableUpdates: []configv1.Release{
				{Version: "4.16.0"},
			},
			namespace:      "openshift-lightspeed",
			currentVersion: "bad-version",
			channel:        "stable-4.16",
			expectError:    kutilerrors.NewAggregate([]error{fmt.Errorf("invalid version bad-version: No Major.Minor.Patch elements found")}),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			agenticRuns, err := getAgenticRuns(
				context.Background(),
				nil,
				tt.availableUpdates,
				tt.conditionalUpdates,
				tt.namespace,
				tt.currentVersion,
				tt.channel,
				tt.systemPrompt,
				"quay.io/openshift/ci:ocp_5.0_agentic-skills",
			)

			if diff := cmp.Diff(err, tt.expectError, cmp.Transformer("Error", func(e error) string {
				if e == nil {
					return ""
				}
				return e.Error()
			})); diff != "" {
				t.Errorf("unexpected error (-want +got):\n%s", diff)
			}

			if diff := cmp.Diff(tt.expected, agenticRuns); diff != "" {
				t.Errorf("unexpected agentic runs (-want +got):\n%s", diff)
			}
		})
	}
}

func Test_expired(t *testing.T) {
	tests := []struct {
		name       string
		agenticRun *agenticrunv1alpha1.AgenticRun
		expected   bool
	}{
		{
			name: "nil agentic run",
		},
		{
			name: "agentic run not expired",
			agenticRun: &agenticrunv1alpha1.AgenticRun{
				ObjectMeta: metav1.ObjectMeta{
					CreationTimestamp: metav1.Time{Time: time.Now().Add(-12 * time.Hour)},
				},
			},
		},
		{
			name: "agentic run expired",
			agenticRun: &agenticrunv1alpha1.AgenticRun{
				ObjectMeta: metav1.ObjectMeta{
					CreationTimestamp: metav1.Time{Time: time.Now().Add(-25 * time.Hour)},
				},
			},
			expected: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := expired(tt.agenticRun)
			if result != tt.expected {
				t.Errorf("expired() = %v, want %v", result, tt.expected)
			}
		})
	}
}

func newFakeDynamicClient(objects ...runtime.Object) *dynamicfake.FakeDynamicClient {
	s := runtime.NewScheme()
	gvrs := map[schema.GroupVersionResource]string{
		readiness.GVRClusterVersion:    "ClusterVersionList",
		readiness.GVRClusterOperator:   "ClusterOperatorList",
		readiness.GVRMachineConfigPool: "MachineConfigPoolList",
		readiness.GVRNode:              "NodeList",
		readiness.GVRPod:               "PodList",
		readiness.GVRPDB:               "PodDisruptionBudgetList",
		readiness.GVRSubscription:      "SubscriptionList",
		readiness.GVRCSV:               "ClusterServiceVersionList",
		readiness.GVRInstallPlan:       "InstallPlanList",
		readiness.GVRPackageManifest:   "PackageManifestList",
		readiness.GVRAPIRequestCount:   "APIRequestCountList",
		readiness.GVRNetwork:           "NetworkList",
		readiness.GVRProxy:             "ProxyList",
		readiness.GVRAPIServer:         "APIServerList",
	}
	for gvr, listKind := range gvrs {
		gvk := schema.GroupVersionKind{Group: gvr.Group, Version: gvr.Version, Kind: listKind}
		s.AddKnownTypeWithName(gvk, &unstructured.UnstructuredList{})
	}
	return dynamicfake.NewSimpleDynamicClientWithCustomListKinds(s, gvrs, objects...)
}

func TestGetAgenticRuns_WithReadinessData(t *testing.T) {
	dc := newFakeDynamicClient(
		// ClusterVersion
		&unstructured.Unstructured{Object: map[string]interface{}{
			"apiVersion": "config.openshift.io/v1", "kind": "ClusterVersion",
			"metadata": map[string]interface{}{"name": "version"},
			"spec":     map[string]interface{}{"channel": "stable-4.21", "clusterID": "test-id"},
			"status": map[string]interface{}{
				"conditions": []interface{}{
					map[string]interface{}{"type": "Available", "status": "True"},
					map[string]interface{}{"type": "Progressing", "status": "False"},
					map[string]interface{}{"type": "Upgradeable", "status": "True"},
				},
				"history": []interface{}{
					map[string]interface{}{"version": "4.21.5", "state": "Completed"},
				},
			},
		}},
		// ClusterOperators
		&unstructured.Unstructured{Object: map[string]interface{}{
			"apiVersion": "config.openshift.io/v1", "kind": "ClusterOperator",
			"metadata": map[string]interface{}{"name": "etcd"},
			"status": map[string]interface{}{
				"conditions": []interface{}{
					map[string]interface{}{"type": "Available", "status": "True"},
					map[string]interface{}{"type": "Degraded", "status": "False"},
					map[string]interface{}{"type": "Upgradeable", "status": "True"},
				},
			},
		}},
		&unstructured.Unstructured{Object: map[string]interface{}{
			"apiVersion": "config.openshift.io/v1", "kind": "ClusterOperator",
			"metadata": map[string]interface{}{"name": "dns"},
			"status": map[string]interface{}{
				"conditions": []interface{}{
					map[string]interface{}{"type": "Available", "status": "True"},
					map[string]interface{}{"type": "Degraded", "status": "False"},
					map[string]interface{}{"type": "Upgradeable", "status": "True"},
				},
			},
		}},
		// MachineConfigPool
		&unstructured.Unstructured{Object: map[string]interface{}{
			"apiVersion": "machineconfiguration.openshift.io/v1", "kind": "MachineConfigPool",
			"metadata": map[string]interface{}{"name": "master"},
			"spec":     map[string]interface{}{"paused": false},
			"status": map[string]interface{}{
				"machineCount": int64(3), "readyMachineCount": int64(3), "updatedMachineCount": int64(3),
				"conditions": []interface{}{
					map[string]interface{}{"type": "Degraded", "status": "False"},
					map[string]interface{}{"type": "Updating", "status": "False"},
				},
			},
		}},
		// Etcd pods
		&unstructured.Unstructured{Object: map[string]interface{}{
			"apiVersion": "v1", "kind": "Pod",
			"metadata": map[string]interface{}{"name": "etcd-master-0", "namespace": "openshift-etcd", "labels": map[string]interface{}{"app": "etcd"}},
			"spec":     map[string]interface{}{"nodeName": "master-0"}, "status": map[string]interface{}{"phase": "Running", "conditions": []interface{}{map[string]interface{}{"type": "Ready", "status": "True"}}},
		}},
		&unstructured.Unstructured{Object: map[string]interface{}{
			"apiVersion": "v1", "kind": "Pod",
			"metadata": map[string]interface{}{"name": "etcd-master-1", "namespace": "openshift-etcd", "labels": map[string]interface{}{"app": "etcd"}},
			"spec":     map[string]interface{}{"nodeName": "master-1"}, "status": map[string]interface{}{"phase": "Running", "conditions": []interface{}{map[string]interface{}{"type": "Ready", "status": "True"}}},
		}},
		&unstructured.Unstructured{Object: map[string]interface{}{
			"apiVersion": "v1", "kind": "Pod",
			"metadata": map[string]interface{}{"name": "etcd-master-2", "namespace": "openshift-etcd", "labels": map[string]interface{}{"app": "etcd"}},
			"spec":     map[string]interface{}{"nodeName": "master-2"}, "status": map[string]interface{}{"phase": "Running", "conditions": []interface{}{map[string]interface{}{"type": "Ready", "status": "True"}}},
		}},
		// Nodes
		&unstructured.Unstructured{Object: map[string]interface{}{
			"apiVersion": "v1", "kind": "Node",
			"metadata": map[string]interface{}{"name": "master-0"},
			"status":   map[string]interface{}{"conditions": []interface{}{map[string]interface{}{"type": "Ready", "status": "True"}}},
		}},
		&unstructured.Unstructured{Object: map[string]interface{}{
			"apiVersion": "v1", "kind": "Node",
			"metadata": map[string]interface{}{"name": "worker-0"},
			"status":   map[string]interface{}{"conditions": []interface{}{map[string]interface{}{"type": "Ready", "status": "True"}}},
		}},
		// PDB
		&unstructured.Unstructured{Object: map[string]interface{}{
			"apiVersion": "policy/v1", "kind": "PodDisruptionBudget",
			"metadata": map[string]interface{}{"name": "etcd-guard", "namespace": "openshift-etcd"},
			"spec":     map[string]interface{}{"maxUnavailable": "1"},
			"status":   map[string]interface{}{"currentHealthy": int64(3), "desiredHealthy": int64(2), "disruptionsAllowed": int64(1)},
		}},
		// APIRequestCount with blocker
		&unstructured.Unstructured{Object: map[string]interface{}{
			"apiVersion": "apiserver.openshift.io/v1", "kind": "APIRequestCount",
			"metadata": map[string]interface{}{"name": "flowschemas.v1beta3.flowcontrol.apiserver.k8s.io"},
			"status": map[string]interface{}{
				"removedInRelease": "4.21.8", "requestCount": int64(100),
				"conditions": []interface{}{map[string]interface{}{"type": "Deprecated", "status": "True"}},
			},
		}},
		// Network, Proxy, APIServer
		&unstructured.Unstructured{Object: map[string]interface{}{
			"apiVersion": "config.openshift.io/v1", "kind": "Network",
			"metadata": map[string]interface{}{"name": "cluster"},
			"status":   map[string]interface{}{"networkType": "OVNKubernetes"},
		}},
		&unstructured.Unstructured{Object: map[string]interface{}{
			"apiVersion": "config.openshift.io/v1", "kind": "Proxy",
			"metadata": map[string]interface{}{"name": "cluster"},
			"spec":     map[string]interface{}{},
		}},
		&unstructured.Unstructured{Object: map[string]interface{}{
			"apiVersion": "config.openshift.io/v1", "kind": "APIServer",
			"metadata": map[string]interface{}{"name": "cluster"},
			"spec":     map[string]interface{}{},
		}},
		// OLM Subscription + CSV
		&unstructured.Unstructured{Object: map[string]interface{}{
			"apiVersion": "operators.coreos.com/v1alpha1", "kind": "Subscription",
			"metadata": map[string]interface{}{"name": "elasticsearch-operator", "namespace": "openshift-operators-redhat"},
			"spec":     map[string]interface{}{"channel": "stable-5.8", "name": "elasticsearch-operator", "source": "redhat-operators", "sourceNamespace": "openshift-marketplace"},
			"status":   map[string]interface{}{"state": "AtLatestKnown", "installedCSV": "elasticsearch-operator.v5.8.6", "currentCSV": "elasticsearch-operator.v5.8.6"},
		}},
		&unstructured.Unstructured{Object: map[string]interface{}{
			"apiVersion": "operators.coreos.com/v1alpha1", "kind": "ClusterServiceVersion",
			"metadata": map[string]interface{}{"name": "elasticsearch-operator.v5.8.6", "namespace": "openshift-operators-redhat"},
			"spec":     map[string]interface{}{"version": "5.8.6", "displayName": "OpenShift Elasticsearch Operator"},
			"status":   map[string]interface{}{"phase": "Succeeded"},
		}},
	)

	agenticRuns, err := getAgenticRuns(
		context.Background(),
		dc,
		[]configv1.Release{{Version: "4.21.8"}},
		nil,
		"openshift-lightspeed",
		"4.21.5",
		"stable-4.21",
		"Test prompt",
		"quay.io/openshift/ci:ocp_5.0_agentic-skills",
	)
	if err != nil {
		t.Fatalf("getAgenticRuns returned error: %v", err)
	}
	if len(agenticRuns) != 1 {
		t.Fatalf("expected 1 agentic run, got %d", len(agenticRuns))
	}

	request := agenticRuns[0].Spec.Request

	if !strings.Contains(request, "## Cluster Readiness Data") {
		t.Fatal("agentic run request missing readiness data section")
	}

	// Extract JSON from the request
	start := strings.Index(request, "```json\n")
	if start < 0 {
		t.Fatal("could not find readiness JSON fence in request")
	}
	jsonStart := start + len("```json\n")
	jsonEnd := strings.Index(request[jsonStart:], "\n```")
	if jsonEnd < 0 {
		t.Fatal("could not find closing fence for readiness JSON")
	}
	readinessJSON := request[jsonStart : jsonStart+jsonEnd]

	// Unmarshal into raw map since CheckResult.Data is json:"-" (flattened during marshal)
	var raw map[string]any
	if err := json.Unmarshal([]byte(readinessJSON), &raw); err != nil {
		t.Fatalf("readiness JSON is not valid: %v\nJSON: %s", err, readinessJSON)
	}

	if raw["current_version"] != "4.21.5" {
		t.Errorf("readiness current_version = %v, want 4.21.5", raw["current_version"])
	}
	if raw["target_version"] != "4.21.8" {
		t.Errorf("readiness target_version = %v, want 4.21.8", raw["target_version"])
	}

	meta, ok := raw["meta"].(map[string]any)
	if !ok {
		t.Fatal("readiness output missing 'meta'")
	}
	if meta["total_checks"] != float64(8) {
		t.Errorf("readiness total_checks = %v, want 8", meta["total_checks"])
	}
	if meta["checks_ok"] != float64(8) {
		t.Errorf("readiness checks_ok = %v, want 8 (all checks should succeed)", meta["checks_ok"])
	}

	checks, ok := raw["checks"].(map[string]any)
	if !ok {
		t.Fatal("readiness output missing 'checks'")
	}

	// Verify every check produced results with ok status
	for _, name := range []string{
		"cluster_conditions", "operator_health", "api_deprecations",
		"node_capacity", "pdb_drain", "etcd_health", "network",
		"olm_operator_lifecycle",
	} {
		check, ok := checks[name].(map[string]any)
		if !ok {
			t.Errorf("readiness output missing %s check", name)
			continue
		}
		if check["_status"] != "ok" {
			t.Errorf("check %s status = %v, error = %v", name, check["_status"], check["_error"])
		}
	}

	// Spot-check: api_deprecations found the blocker
	if ad, ok := checks["api_deprecations"].(map[string]any); !ok {
		t.Fatal("api_deprecations check missing or wrong type")
	} else if adSummary, ok := ad["summary"].(map[string]any); !ok {
		t.Fatal("api_deprecations summary missing or wrong type")
	} else if adSummary["blockers"] != float64(1) {
		t.Errorf("api_deprecations blockers = %v, want 1", adSummary["blockers"])
	}

	// Spot-check: olm found the subscription
	if olm, ok := checks["olm_operator_lifecycle"].(map[string]any); !ok {
		t.Fatal("olm_operator_lifecycle check missing or wrong type")
	} else if olmSummary, ok := olm["summary"].(map[string]any); !ok {
		t.Fatal("olm_operator_lifecycle summary missing or wrong type")
	} else if olmSummary["total_operators"] != float64(1) {
		t.Errorf("olm total_operators = %v, want 1", olmSummary["total_operators"])
	}

	// Spot-check: etcd has 3 healthy members
	if etcd, ok := checks["etcd_health"].(map[string]any); !ok {
		t.Fatal("etcd_health check missing or wrong type")
	} else if etcd["total_members"] != float64(3) {
		t.Errorf("etcd total_members = %v, want 3", etcd["total_members"])
	}

	// Spot-check: node_capacity found 2 nodes
	if nc, ok := checks["node_capacity"].(map[string]any); !ok {
		t.Fatal("node_capacity check missing or wrong type")
	} else if nc["total_nodes"] != float64(2) {
		t.Errorf("node_capacity total_nodes = %v, want 2", nc["total_nodes"])
	}
}

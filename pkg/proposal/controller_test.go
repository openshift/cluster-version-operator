package proposal

import (
	"context"
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
	kutilerrors "k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/utils/ptr"

	configv1 "github.com/openshift/api/config/v1"

	proposalv1alpha1 "github.com/openshift/cluster-version-operator/pkg/proposal/api/v1alpha1"
)

func init() {
	err := proposalv1alpha1.AddToScheme(scheme.Scheme)
	if err != nil {
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
				proposals := &proposalv1alpha1.ProposalList{}
				if err := client.List(context.Background(), proposals); err != nil {
					return err
				}
				expect := &proposalv1alpha1.ProposalList{
					Items: []proposalv1alpha1.Proposal{
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
							Spec: proposalv1alpha1.ProposalSpec{
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
								WorkflowRef: corev1.LocalObjectReference{Name: "ota-advisory"},
								MaxAttempts: ptr.To(2),
							},
						},
					}}
				if diff := cmp.Diff(expect, proposals, cmpopts.IgnoreFields(metav1.ObjectMeta{}, "ResourceVersion")); diff != "" {
					return fmt.Errorf("unexpected ProposalList (-want, +got) = \n%v", diff)
				}
				return nil
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := NewController(tt.updatesGetterFunc, tt.client, tt.cvGetterFunc, func(name, namespace string) (*corev1.ConfigMap, error) {
				return &corev1.ConfigMap{
					Data: map[string]string{
						"prompt": "prompt-abc",
						"foo":    "bar",
					},
				}, nil
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

func TestDeleteProposals(t *testing.T) {
	tests := []struct {
		name               string
		availableUpdates   []configv1.Release
		conditionalUpdates []configv1.ConditionalUpdate
		history            []configv1.UpdateHistory
		currentVersion     string
		existingProposals  []proposalv1alpha1.Proposal
		expectedDeleted    []string
		expectedKept       []string
	}{
		{
			name: "keeps relevant proposals matching current version and target",
			availableUpdates: []configv1.Release{
				{Version: "4.16.0"},
				{Version: "4.16.1"},
			},
			currentVersion: "4.15.3",
			existingProposals: []proposalv1alpha1.Proposal{
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
			name: "deletes proposals with outdated current version",
			availableUpdates: []configv1.Release{
				{Version: "4.16.0"},
			},
			currentVersion: "4.15.3",
			existingProposals: []proposalv1alpha1.Proposal{
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
			name: "deletes proposals for targets no longer in available updates",
			availableUpdates: []configv1.Release{
				{Version: "4.16.1"},
			},
			currentVersion: "4.15.3",
			existingProposals: []proposalv1alpha1.Proposal{
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
			name: "keeps proposals associated with history",
			availableUpdates: []configv1.Release{
				{Version: "4.16.2"},
			},
			history: []configv1.UpdateHistory{
				{Version: "4.16.1"},
			},
			currentVersion: "4.16.1",
			existingProposals: []proposalv1alpha1.Proposal{
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
			name: "keeps proposals not owned by CVO",
			availableUpdates: []configv1.Release{
				{Version: "4.16.0"},
			},
			currentVersion: "4.15.3",
			existingProposals: []proposalv1alpha1.Proposal{
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
			existingProposals: []proposalv1alpha1.Proposal{
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
			name: "deletes proposals with missing labels",
			availableUpdates: []configv1.Release{
				{Version: "4.16.0"},
			},
			currentVersion: "4.15.3",
			existingProposals: []proposalv1alpha1.Proposal{
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
			existingProposals: []proposalv1alpha1.Proposal{
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
			existingProposals: []proposalv1alpha1.Proposal{
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
			for _, p := range tt.existingProposals {
				clientBuilder.WithObjects(&p)
			}
			client := clientBuilder.Build()

			err := deleteProposals(ctx, client, tt.availableUpdates, tt.conditionalUpdates, tt.history, tt.currentVersion)
			if err != nil {
				t.Errorf("deleteProposals() returned unexpected error: %v", err)
			}

			for _, name := range tt.expectedKept {
				proposal := &proposalv1alpha1.Proposal{}
				err := client.Get(ctx, ctrlruntimeclient.ObjectKey{Name: name, Namespace: "openshift-lightspeed"}, proposal)
				if err != nil {
					t.Errorf("expected proposal %s to be kept, but got error: %v", name, err)
				}
			}

			for _, name := range tt.expectedDeleted {
				proposal := &proposalv1alpha1.Proposal{}
				err := client.Get(ctx, ctrlruntimeclient.ObjectKey{Name: name, Namespace: "openshift-lightspeed"}, proposal)
				if err == nil {
					t.Errorf("expected proposal %s to be deleted, but it still exists", name)
				}
			}
		})
	}
}

func TestDeleteProposal(t *testing.T) {
	tests := []struct {
		name            string
		proposal        *proposalv1alpha1.Proposal
		adjective       string
		setupClient     func() ctrlruntimeclient.Client
		expect          error
		shouldBeDeleted bool
	}{
		{
			name: "successfully deletes existing proposal",
			proposal: &proposalv1alpha1.Proposal{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-proposal",
					Namespace: "openshift-lightspeed",
				},
			},
			adjective: "expired",
			setupClient: func() ctrlruntimeclient.Client {
				return fake.NewClientBuilder().WithObjects(&proposalv1alpha1.Proposal{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test-proposal",
						Namespace: "openshift-lightspeed",
					},
				}).Build()
			},
			shouldBeDeleted: true,
		},
		{
			name: "handles not found error gracefully",
			proposal: &proposalv1alpha1.Proposal{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "nonexistent-proposal",
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
			name:      "handles nil proposal",
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

			err := deleteProposal(ctx, client, tt.proposal, tt.adjective)

			if diff := cmp.Diff(tt.expect, err, cmp.Transformer("Error", func(e error) string {
				if e == nil {
					return ""
				}
				return e.Error()
			})); diff != "" {
				t.Errorf("unexpected error (-want +got):\n%s", diff)
			}

			if tt.proposal != nil {
				proposal := &proposalv1alpha1.Proposal{}
				err = client.Get(ctx, ctrlruntimeclient.ObjectKey{
					Name:      tt.proposal.Name,
					Namespace: tt.proposal.Namespace,
				}, proposal)
				if tt.shouldBeDeleted {
					if !kerrors.IsNotFound(err) {
						t.Error("expected proposal to be deleted but it still exists")
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
		name     string
		proposal *proposalv1alpha1.Proposal
		expected bool
	}{
		{
			name: "nil proposal",
		},
		{
			name: "proposal owned by CVO",
			proposal: &proposalv1alpha1.Proposal{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						labelKeySource: labelValueSource,
					},
				},
			},
			expected: true,
		},
		{
			name: "proposal not owned by CVO",
			proposal: &proposalv1alpha1.Proposal{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						labelKeySource: "manual",
					},
				},
			},
		},
		{
			name: "proposal with missing labels",
			proposal: &proposalv1alpha1.Proposal{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{},
				},
			},
		},
		{
			name:     "proposal with nil labels",
			proposal: &proposalv1alpha1.Proposal{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ownedByCVO(tt.proposal)
			if result != tt.expected {
				t.Errorf("ownedByCVO() = %v, want %v", result, tt.expected)
			}
		})
	}
}

func TestGetProposals(t *testing.T) {
	tests := []struct {
		name               string
		availableUpdates   []configv1.Release
		conditionalUpdates []configv1.ConditionalUpdate
		namespace          string
		currentVersion     string
		channel            string
		workflowRefName    string
		systemPrompt       string
		readinessJSON      string
		expected           []*proposalv1alpha1.Proposal
		expectError        error
	}{
		{
			name: "available updates only",
			availableUpdates: []configv1.Release{
				{Version: "4.16.0"},
				{Version: "4.16.1"},
			},
			namespace:       "openshift-lightspeed",
			currentVersion:  "4.15.3",
			channel:         "stable-4.16",
			workflowRefName: "ota-advisory",
			systemPrompt:    "Test prompt",
			readinessJSON:   `{"test": "data"}`,
			expected: []*proposalv1alpha1.Proposal{
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
					Spec: proposalv1alpha1.ProposalSpec{
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
							`{"test": "data"}` + "\n```\n",
						WorkflowRef: corev1.LocalObjectReference{Name: "ota-advisory"},
						MaxAttempts: ptr.To(2),
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
					Spec: proposalv1alpha1.ProposalSpec{
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
							`{"test": "data"}` + "\n```\n",
						WorkflowRef: corev1.LocalObjectReference{Name: "ota-advisory"},
						MaxAttempts: ptr.To(2),
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
			namespace:       "openshift-lightspeed",
			currentVersion:  "4.15.3",
			channel:         "stable-4.16",
			workflowRefName: "ota-advisory",
			expected: []*proposalv1alpha1.Proposal{
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
					Spec: proposalv1alpha1.ProposalSpec{
						Request: `Current version: OCP 4.15.3
Target version: OCP 4.16.0
Channel: stable-4.16
Update type: Minor
Update path: Recommended

Other recommended versions available:
  - 4.16.1

`,
						WorkflowRef: corev1.LocalObjectReference{Name: "ota-advisory"},
						MaxAttempts: ptr.To(2),
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
					Spec: proposalv1alpha1.ProposalSpec{
						Request: `Current version: OCP 4.15.3
Target version: OCP 4.16.1
Channel: stable-4.16
Update type: Minor
Update path: Recommended

Other recommended versions available:
  - 4.16.0

`,
						WorkflowRef: corev1.LocalObjectReference{Name: "ota-advisory"},
						MaxAttempts: ptr.To(2),
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
					Spec: proposalv1alpha1.ProposalSpec{
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

`,
						WorkflowRef: corev1.LocalObjectReference{Name: "ota-advisory"},
						MaxAttempts: ptr.To(2),
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
					Spec: proposalv1alpha1.ProposalSpec{
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

`,
						WorkflowRef: corev1.LocalObjectReference{Name: "ota-advisory"},
						MaxAttempts: ptr.To(2),
					},
				},
			},
		},
		{
			name:            "empty updates",
			namespace:       "openshift-lightspeed",
			currentVersion:  "4.15.3",
			channel:         "stable-4.16",
			workflowRefName: "ota-advisory",
		},
		{
			name: "invalid version in available update",
			availableUpdates: []configv1.Release{
				{Version: "invalid-version"},
			},
			namespace:       "openshift-lightspeed",
			currentVersion:  "4.15.3",
			channel:         "stable-4.16",
			workflowRefName: "ota-advisory",
			expectError:     kutilerrors.NewAggregate([]error{fmt.Errorf("invalid version invalid-version: No Major.Minor.Patch elements found")}),
		},
		{
			name: "invalid current version",
			availableUpdates: []configv1.Release{
				{Version: "4.16.0"},
			},
			namespace:       "openshift-lightspeed",
			currentVersion:  "bad-version",
			channel:         "stable-4.16",
			workflowRefName: "ota-advisory",
			expectError:     kutilerrors.NewAggregate([]error{fmt.Errorf("invalid version bad-version: No Major.Minor.Patch elements found")}),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			proposals, err := getProposals(
				tt.availableUpdates,
				tt.conditionalUpdates,
				tt.namespace,
				tt.currentVersion,
				tt.channel,
				tt.workflowRefName,
				tt.systemPrompt,
				tt.readinessJSON,
			)

			if diff := cmp.Diff(err, tt.expectError, cmp.Transformer("Error", func(e error) string {
				if e == nil {
					return ""
				}
				return e.Error()
			})); diff != "" {
				t.Errorf("unexpected error (-want +got):\n%s", diff)
			}

			if diff := cmp.Diff(tt.expected, proposals); diff != "" {
				t.Errorf("unexpected proposals (-want +got):\n%s", diff)
			}
		})
	}
}

func Test_expired(t *testing.T) {
	tests := []struct {
		name     string
		proposal *proposalv1alpha1.Proposal
		expected bool
	}{
		{
			name: "nil proposal",
		},
		{
			name: "proposal not expired",
			proposal: &proposalv1alpha1.Proposal{
				ObjectMeta: metav1.ObjectMeta{
					CreationTimestamp: metav1.Time{Time: time.Now().Add(-12 * time.Hour)},
				},
			},
		},
		{
			name: "proposal expired",
			proposal: &proposalv1alpha1.Proposal{
				ObjectMeta: metav1.ObjectMeta{
					CreationTimestamp: metav1.Time{Time: time.Now().Add(-25 * time.Hour)},
				},
			},
			expected: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := expired(tt.proposal)
			if result != tt.expected {
				t.Errorf("expired() = %v, want %v", result, tt.expected)
			}
		})
	}
}

package agenticrun

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/dynamic"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	configv1 "github.com/openshift/api/config/v1"
	agenticrunv1alpha1 "github.com/openshift/lightspeed-agentic-operator/api/v1alpha1"

	"github.com/openshift/cluster-version-operator/pkg/readiness"
)

type recordingReadinessCheck struct{ targets []string }

func (r *recordingReadinessCheck) Name() string { return "test" }
func (r *recordingReadinessCheck) Run(_ context.Context, _ dynamic.Interface, _, target string) (map[string]any, error) {
	r.targets = append(r.targets, target)
	return map[string]any{"snapshot": "fresh"}, nil
}

func TestReanalysisReconciliation(t *testing.T) {
	for _, scenario := range []string{"missing", "fresh", "expired", "expired with finalizer", "terminating", "unowned", "progressing", "updates unavailable", "empty updates"} {
		t.Run(scenario, func(t *testing.T) {
			ctx := context.Background()
			updates := []configv1.Release{{Version: "4.16.0"}, {Version: "4.16.1"}}
			run, err := getAgenticRun("openshift-lightspeed", "4.15.3", "4.16.0", "stable-4.16", updateKindRecommended, prompt, "{}", updates, testSkillsImage)
			if err != nil {
				t.Fatal(err)
			}
			run.CreationTimestamp = metav1.Now()
			run.Spec.Request = "old snapshot"
			other := run.DeepCopy()
			other.Name = agenticRunName("4.15.3", "4.16.1")
			other.Labels[labelKeyTargetVersion] = "4.16.1"
			objects := []ctrlruntimeclient.Object{other}
			if scenario != "missing" {
				objects = append(objects, run)
			}
			switch scenario {
			case "expired":
				run.CreationTimestamp = metav1.NewTime(time.Now().Add(-25 * time.Hour))
			case "expired with finalizer":
				run.CreationTimestamp = metav1.NewTime(time.Now().Add(-25 * time.Hour))
				run.Finalizers = []string{"test.example/cleanup"}
			case "terminating":
				run.Finalizers = []string{"test.example/cleanup"}
				run.DeletionTimestamp = &metav1.Time{Time: time.Now()}
			case "unowned":
				delete(run.Labels, labelKeySource)
			}
			cv := &configv1.ClusterVersion{Spec: configv1.ClusterVersionSpec{Channel: "stable-4.16"}}
			if scenario == "progressing" {
				cv.Status.Conditions = []configv1.ClusterOperatorStatusCondition{{Type: configv1.OperatorProgressing, Status: configv1.ConditionTrue}}
			}
			client := fake.NewClientBuilder().WithObjects(objects...).Build()
			c := NewController(func() ([]configv1.Release, []configv1.ConditionalUpdate, error) {
				if scenario == "updates unavailable" {
					return nil, nil, fmt.Errorf("not initialized")
				}
				if scenario == "empty updates" {
					return nil, nil, nil
				}
				return updates, nil, nil
			}, client, dynamicfake.NewSimpleDynamicClient(runtime.NewScheme()), func(string) (*configv1.ClusterVersion, error) { return cv, nil }, func() string { return "4.15.3" }, nil)
			c.config.SkillsImage = testSkillsImage
			c.crdAvailableCache, c.crdLastChecked = true, time.Now()
			defer c.queue.ShutDown()
			check := &recordingReadinessCheck{}
			original := readiness.AllChecks
			readiness.AllChecks = func() []readiness.Check { return []readiness.Check{check} }
			t.Cleanup(func() { readiness.AllChecks = original })
			err = c.Sync(ctx, c.queueKey)
			if (err != nil) != (scenario == "updates unavailable") {
				t.Fatalf("Sync error = %v", err)
			}
			wantChecks := scenario == "missing" || scenario == "expired"
			if wantChecks {
				if len(check.targets) != 1 || check.targets[0] != "4.16.0" {
					t.Fatalf("readiness queried %v, want only 4.16.0", check.targets)
				}
				created := &agenticrunv1alpha1.AgenticRun{}
				if err := client.Get(ctx, ctrlruntimeclient.ObjectKeyFromObject(run), created); err != nil {
					t.Fatal(err)
				}
				if !strings.Contains(created.Spec.Request, `"snapshot":"fresh"`) {
					t.Fatal("replacement has no fresh readiness snapshot")
				}
			} else if len(check.targets) != 0 {
				t.Fatalf("unnecessary readiness queries: %v", check.targets)
			}
			if scenario == "expired with finalizer" {
				if err := client.Get(ctx, ctrlruntimeclient.ObjectKeyFromObject(run), run); err != nil {
					t.Fatal(err)
				}
				if run.DeletionTimestamp.IsZero() {
					t.Fatal("expired run was not deleted")
				}
				run.Finalizers = nil
				if err := client.Update(ctx, run); err != nil {
					t.Fatal(err)
				}
				if err := c.Sync(ctx, c.queueKey); err != nil {
					t.Fatal(err)
				}
				if len(check.targets) != 1 {
					t.Fatal("run was not recreated after finalizer cleanup")
				}
			}
			if scenario == "updates unavailable" {
				list := &agenticrunv1alpha1.AgenticRunList{}
				if err := client.List(ctx, list); err != nil {
					t.Fatal(err)
				}
				if len(list.Items) != 2 {
					t.Fatalf("startup reconciliation deleted runs: %d remain", len(list.Items))
				}
			}
		})
	}
}

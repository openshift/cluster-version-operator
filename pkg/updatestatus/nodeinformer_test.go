package updatestatus

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"gopkg.in/yaml.v3"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	kerrors "k8s.io/apimachinery/pkg/util/errors"
	corelistersv1 "k8s.io/client-go/listers/core/v1"
	"k8s.io/client-go/tools/cache"
	"k8s.io/klog/v2"

	configv1 "github.com/openshift/api/config/v1"
	machineconfigv1 "github.com/openshift/api/machineconfiguration/v1"
	fakeconfigv1client "github.com/openshift/client-go/config/clientset/versioned/fake"
	machineconfigv1listers "github.com/openshift/client-go/machineconfiguration/listers/machineconfiguration/v1"

	updatestatus "github.com/openshift/cluster-version-operator/pkg/updatestatus/api"
	"github.com/openshift/cluster-version-operator/pkg/updatestatus/mco"
)

func Test_nodeInformerControllerQueueKeys(t *testing.T) {
	testCases := []struct {
		name string

		object runtime.Object

		expected      []string
		expectedPanic bool
		expectedKind  string
		expectedName  string
	}{
		{
			name: "nil object",
		},
		{
			name:          "unexpected type",
			object:        &configv1.Image{},
			expectedPanic: true,
		},
		{
			name:         "node",
			object:       &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "bar"}},
			expected:     []string{"Node/bar"},
			expectedKind: "Node",
			expectedName: "bar",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {

			defer func() {
				if tc.expectedPanic {
					if r := recover(); r == nil {
						t.Errorf("The expected panic did not happen")
					}
				}
			}()

			actual := nodeInformerControllerQueueKeys(tc.object)

			if diff := cmp.Diff(tc.expected, actual); diff != "" {
				t.Errorf("%s: key differs from expected:\n%s", tc.name, diff)
			}

			if !tc.expectedPanic && len(actual) > 0 {
				kind, name, err := parseNodeInformerControllerQueueKey(actual[0])
				if err != nil {
					t.Errorf("%s: unexpected error raised:\n%v", tc.name, err)
				}

				if diff := cmp.Diff(tc.expectedKind, kind); diff != "" {
					t.Errorf("%s: kind differ from expected:\n%s", tc.name, diff)
				}
				if diff := cmp.Diff(tc.expectedName, name); diff != "" {
					t.Errorf("%s: name differ from expected:\n%s", tc.name, diff)
				}
			}

		})
	}
}

func getMCPs(names ...string) []*machineconfigv1.MachineConfigPool {
	var mcps []*machineconfigv1.MachineConfigPool
	for _, name := range names {
		mcps = append(mcps, getMCP(name))
	}
	return mcps
}

func getMCP(name string) *machineconfigv1.MachineConfigPool {
	switch name {
	case mco.MachineConfigPoolMaster:
		return &machineconfigv1.MachineConfigPool{
			ObjectMeta: metav1.ObjectMeta{
				Name:   mco.MachineConfigPoolMaster,
				Labels: map[string]string{"pools.operator.machineconfiguration.openshift.io/master": ""},
			},
			Spec: machineconfigv1.MachineConfigPoolSpec{
				NodeSelector: metav1.SetAsLabelSelector(labels.Set{"node-role.kubernetes.io/master": ""}),
			},
		}
	case mco.MachineConfigPoolWorker:
		return &machineconfigv1.MachineConfigPool{
			ObjectMeta: metav1.ObjectMeta{
				Name:   mco.MachineConfigPoolWorker,
				Labels: map[string]string{"pools.operator.machineconfiguration.openshift.io/worker": ""},
			},
			Spec: machineconfigv1.MachineConfigPoolSpec{
				NodeSelector: metav1.SetAsLabelSelector(labels.Set{"node-role.kubernetes.io/worker": ""}),
			},
		}
	case "abnormal":
		return &machineconfigv1.MachineConfigPool{
			ObjectMeta: metav1.ObjectMeta{
				Name: "abnormal",
			},
			Spec: machineconfigv1.MachineConfigPoolSpec{
				NodeSelector: &metav1.LabelSelector{
					MatchExpressions: []metav1.LabelSelectorRequirement{
						{
							Operator: "non-exists",
							Key:      "k",
							Values:   []string{"v"},
						},
					},
				},
			},
		}
	default:
		return &machineconfigv1.MachineConfigPool{
			ObjectMeta: metav1.ObjectMeta{
				Name:   name,
				Labels: map[string]string{"pools.operator.machineconfiguration.openshift.io/worker": ""},
			},
			Spec: machineconfigv1.MachineConfigPoolSpec{
				NodeSelector: metav1.SetAsLabelSelector(labels.Set{"node-role.kubernetes.io/worker": "", "mcp": name}),
			},
		}
	}
}

func (c *nodeInformerController) initializeCaches() error {
	var errs []error

	if pools, err := c.machineConfigPools.List(labels.Everything()); err != nil {
		errs = append(errs, err)
	} else {
		for _, pool := range pools {
			c.machineConfigPoolSelectorCache.ingest(pool)
		}
	}
	klog.V(2).Infof("Stored %d machineConfigPools in the cache", len(c.machineConfigPoolSelectorCache.cache))

	machineConfigs, err := c.machineConfigs.List(labels.Everything())
	if err != nil {
		errs = append(errs, err)
	} else {
		for _, mc := range machineConfigs {
			c.machineConfigVersionCache.ingest(mc)
		}
	}

	klog.V(2).Infof("Stored %d machineConfig versions in the cache", len(c.machineConfigVersionCache.cache))

	return kerrors.NewAggregate(errs)
}

func Test_whichMCP(t *testing.T) {
	testCases := []struct {
		name string

		labels labels.Labels
		pools  []*machineconfigv1.MachineConfigPool

		expected string
	}{
		{
			name:     "master",
			labels:   labels.Set(map[string]string{"node-role.kubernetes.io/control-plane": "", "node-role.kubernetes.io/master": ""}),
			pools:    getMCPs("master", "worker", "infra"),
			expected: "master",
		},
		{
			name:     "worker",
			labels:   labels.Set(map[string]string{"machine.openshift.io/interruptible-instance": "", "node-role.kubernetes.io/worker": ""}),
			pools:    getMCPs("master", "worker", "infra"),
			expected: "worker",
		},
		{
			name:     "infra",
			labels:   labels.Set(map[string]string{"machine.openshift.io/interruptible-instance": "", "node-role.kubernetes.io/worker": "", "mcp": "infra"}),
			pools:    getMCPs("master", "worker", "infra"),
			expected: "infra",
		},
		{
			name:   "no matching pool",
			labels: labels.Set(map[string]string{"machine.openshift.io/interruptible-instance": "", "node-role.kubernetes.io/not-worker": ""}),
			pools:  getMCPs("master", "worker", "infra"),
		},
		{
			name:     "abnormal mcp",
			labels:   labels.Set(map[string]string{"machine.openshift.io/interruptible-instance": "", "node-role.kubernetes.io/worker": "", "mcp": "abnormal"}),
			pools:    getMCPs("master", "worker", "abnormal"),
			expected: "worker",
		},
		{
			name:   "no matching pool and abnormal mcp",
			labels: labels.Set(map[string]string{"machine.openshift.io/interruptible-instance": "", "node-role.kubernetes.io/not-worker": "", "mcp": "abnormal"}),
			pools:  getMCPs("master", "worker", "abnormal"),
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {

			mcIndexer := cache.NewIndexer(cache.MetaNamespaceKeyFunc, cache.Indexers{})
			mcLister := machineconfigv1listers.NewMachineConfigLister(mcIndexer)

			mcpIndexer := cache.NewIndexer(cache.MetaNamespaceKeyFunc, cache.Indexers{})
			for _, o := range tc.pools {
				if err := mcpIndexer.Add(o); err != nil {
					t.Fatalf("Failed to add object to indexer: %v", err)
				}
			}

			mcpLister := machineconfigv1listers.NewMachineConfigPoolLister(mcpIndexer)

			c := nodeInformerController{machineConfigs: mcLister, machineConfigPools: mcpLister}

			if err := c.initializeCaches(); err != nil {
				t.Errorf("Failed to initialize caches: %v", err)
			}

			if diff := cmp.Diff(tc.expected, c.machineConfigPoolSelectorCache.whichMCP(tc.labels)); diff != "" {
				t.Errorf("%s: machine config pool differs from expected:\n%s", tc.name, diff)
			}

		})
	}
}

func Test_assessNode(t *testing.T) {
	now := metav1.Now()
	notReadyTime := metav1.Time{Time: now.Add(-3 * time.Minute)}
	testCases := []struct {
		name string

		node                         *corev1.Node
		mcp                          *machineconfigv1.MachineConfigPool
		machineConfigVersions        map[string]string
		mostRecentVersionInCVHistory string

		expected *updatestatus.NodeStatusInsight
	}{
		{
			name: "all nil input",
		},
		{
			// The node has no annotations which leads to node's unavailability but the node's status does not have the condition about it.
			// This should never happen in reality.
			name: "machineConfigs is nil and node has no annotations",
			node: &corev1.Node{
				ObjectMeta: metav1.ObjectMeta{Name: "worker-1"},
			},
			mcp: &machineconfigv1.MachineConfigPool{
				ObjectMeta: metav1.ObjectMeta{Name: "worker"},
			},
			expected: &updatestatus.NodeStatusInsight{
				Name:     "worker-1",
				Resource: updatestatus.ResourceRef{Resource: "nodes", Name: "worker-1"},
				PoolResource: updatestatus.PoolResourceRef{
					ResourceRef: updatestatus.ResourceRef{
						Group:    "machineconfiguration.openshift.io",
						Resource: "machineconfigpools",
						Name:     "worker",
					},
				},
				Scope:   "WorkerPool",
				Message: "Machine Config Daemon is processing the node",
				Conditions: []metav1.Condition{
					{
						Type:               "Updating",
						Status:             "False",
						LastTransitionTime: now,
						Reason:             "Pending",
					},
					{
						Type:    "Available",
						Status:  "False",
						Reason:  "Machine Config Daemon is processing the node",
						Message: "Machine Config Daemon is processing the node",
					},
					{
						Type:               "Degraded",
						Status:             "False",
						LastTransitionTime: now,
					},
				},
			},
		},
		{
			// This should never happen in reality.
			name: "machineConfigs is nil",
			node: &corev1.Node{
				ObjectMeta: metav1.ObjectMeta{Name: "worker-1", Annotations: map[string]string{
					"machineconfiguration.openshift.io/currentConfig": "aaa",
					"machineconfiguration.openshift.io/desiredConfig": "aaa",
					"machineconfiguration.openshift.io/currentImage":  "bbb",
					"machineconfiguration.openshift.io/desiredImage":  "bbb",
					"machineconfiguration.openshift.io/state":         "Done",
				}},
			},
			mcp: &machineconfigv1.MachineConfigPool{
				ObjectMeta: metav1.ObjectMeta{Name: "worker"},
			},
			expected: &updatestatus.NodeStatusInsight{
				Name:     "worker-1",
				Resource: updatestatus.ResourceRef{Resource: "nodes", Name: "worker-1"},
				PoolResource: updatestatus.PoolResourceRef{
					ResourceRef: updatestatus.ResourceRef{
						Group:    "machineconfiguration.openshift.io",
						Resource: "machineconfigpools",
						Name:     "worker",
					},
				},
				Scope: "WorkerPool",
				Conditions: []metav1.Condition{
					{
						Type:               "Updating",
						Status:             "False",
						Reason:             "Pending",
						LastTransitionTime: now,
					},
					{
						Type:               "Available",
						Status:             "True",
						LastTransitionTime: now,
					},
					{
						Type:               "Degraded",
						Status:             "False",
						LastTransitionTime: now,
					},
				},
			},
		},
		{
			name: "paused",
			node: &corev1.Node{
				ObjectMeta: metav1.ObjectMeta{Name: "worker-1", Annotations: map[string]string{
					"machineconfiguration.openshift.io/currentConfig": "aaa",
					"machineconfiguration.openshift.io/desiredConfig": "aaa",
					"machineconfiguration.openshift.io/currentImage":  "bbb",
					"machineconfiguration.openshift.io/desiredImage":  "bbb",
					"machineconfiguration.openshift.io/state":         "Done",
				}},
			},
			mcp: &machineconfigv1.MachineConfigPool{
				ObjectMeta: metav1.ObjectMeta{Name: "worker"},
				Spec:       machineconfigv1.MachineConfigPoolSpec{Paused: true},
			},
			expected: &updatestatus.NodeStatusInsight{
				Name:     "worker-1",
				Resource: updatestatus.ResourceRef{Resource: "nodes", Name: "worker-1"},
				PoolResource: updatestatus.PoolResourceRef{
					ResourceRef: updatestatus.ResourceRef{
						Group:    "machineconfiguration.openshift.io",
						Resource: "machineconfigpools",
						Name:     "worker",
					},
				},
				Scope:         "WorkerPool",
				EstToComplete: toPointer(time.Duration(0)),
				Conditions: []metav1.Condition{
					{
						Type:               "Updating",
						Status:             "False",
						Reason:             "Paused",
						LastTransitionTime: now,
					},
					{
						Type:               "Available",
						Status:             "True",
						LastTransitionTime: now,
					},
					{
						Type:               "Degraded",
						Status:             "False",
						LastTransitionTime: now,
					},
				},
			},
		},
		{
			name: "updated",
			node: &corev1.Node{
				ObjectMeta: metav1.ObjectMeta{Name: "worker-1", Annotations: map[string]string{
					"machineconfiguration.openshift.io/currentConfig": "aaa",
					"machineconfiguration.openshift.io/desiredConfig": "aaa",
					"machineconfiguration.openshift.io/currentImage":  "bbb",
					"machineconfiguration.openshift.io/desiredImage":  "bbb",
					"machineconfiguration.openshift.io/state":         "Done",
				}},
			},
			machineConfigVersions: map[string]string{"aaa": "4.1.23"},
			mcp: &machineconfigv1.MachineConfigPool{
				ObjectMeta: metav1.ObjectMeta{Name: "worker"},
			},
			mostRecentVersionInCVHistory: "4.1.23",
			expected: &updatestatus.NodeStatusInsight{
				Name:     "worker-1",
				Resource: updatestatus.ResourceRef{Resource: "nodes", Name: "worker-1"},
				PoolResource: updatestatus.PoolResourceRef{
					ResourceRef: updatestatus.ResourceRef{
						Group:    "machineconfiguration.openshift.io",
						Resource: "machineconfigpools",
						Name:     "worker",
					},
				},
				Scope:         "WorkerPool",
				Version:       "4.1.23",
				EstToComplete: toPointer(time.Duration(0)),
				Conditions: []metav1.Condition{
					{
						Type:               "Updating",
						Status:             "False",
						Reason:             "Completed",
						LastTransitionTime: now,
					},
					{
						Type:               "Available",
						Status:             "True",
						LastTransitionTime: now,
					},
					{
						Type:               "Degraded",
						Status:             "False",
						LastTransitionTime: now,
					},
				},
			},
		},
		{
			name: "updating",
			node: &corev1.Node{
				ObjectMeta: metav1.ObjectMeta{Name: "worker-1", Annotations: map[string]string{
					"machineconfiguration.openshift.io/currentConfig": "aaa",
					"machineconfiguration.openshift.io/desiredConfig": "ccc",
					"machineconfiguration.openshift.io/currentImage":  "4.1.23",
					"machineconfiguration.openshift.io/desiredImage":  "4.1.26",
				}},
			},
			machineConfigVersions: map[string]string{
				"aaa": "4.1.23",
				"ccc": "4.1.26",
			},
			mcp: &machineconfigv1.MachineConfigPool{
				ObjectMeta: metav1.ObjectMeta{Name: "worker"},
			},
			mostRecentVersionInCVHistory: "4.1.26",
			expected: &updatestatus.NodeStatusInsight{
				Name:     "worker-1",
				Resource: updatestatus.ResourceRef{Resource: "nodes", Name: "worker-1"},
				PoolResource: updatestatus.PoolResourceRef{
					ResourceRef: updatestatus.ResourceRef{
						Group:    "machineconfiguration.openshift.io",
						Resource: "machineconfigpools",
						Name:     "worker",
					},
				},
				Scope:         "WorkerPool",
				Version:       "4.1.23",
				EstToComplete: toPointer(10 * time.Minute),
				Conditions: []metav1.Condition{
					{
						Type:               "Updating",
						Status:             "True",
						Reason:             "Updating",
						LastTransitionTime: now,
					},
					{
						Type:               "Available",
						Status:             "True",
						LastTransitionTime: now,
					},
					{
						Type:               "Degraded",
						Status:             "False",
						LastTransitionTime: now,
					},
				},
			},
		},
		{
			name: "updating master node",
			node: &corev1.Node{
				ObjectMeta: metav1.ObjectMeta{Name: "master-1", Annotations: map[string]string{
					"machineconfiguration.openshift.io/currentConfig": "aaa",
					"machineconfiguration.openshift.io/desiredConfig": "ccc",
					"machineconfiguration.openshift.io/currentImage":  "4.1.23",
					"machineconfiguration.openshift.io/desiredImage":  "4.1.26",
				}},
			},
			machineConfigVersions: map[string]string{
				"aaa": "4.1.23",
				"ccc": "4.1.26",
			},
			mcp: &machineconfigv1.MachineConfigPool{
				ObjectMeta: metav1.ObjectMeta{Name: "master"},
			},
			mostRecentVersionInCVHistory: "4.1.26",
			expected: &updatestatus.NodeStatusInsight{
				Name:     "master-1",
				Resource: updatestatus.ResourceRef{Resource: "nodes", Name: "master-1"},
				PoolResource: updatestatus.PoolResourceRef{
					ResourceRef: updatestatus.ResourceRef{
						Group:    "machineconfiguration.openshift.io",
						Resource: "machineconfigpools",
						Name:     "master",
					},
				},
				Scope:         "ControlPlane",
				Version:       "4.1.23",
				EstToComplete: toPointer(10 * time.Minute),
				Conditions: []metav1.Condition{
					{
						Type:               "Updating",
						Status:             "True",
						Reason:             "Updating",
						LastTransitionTime: now,
					},
					{
						Type:               "Available",
						Status:             "True",
						LastTransitionTime: now,
					},
					{
						Type:               "Degraded",
						Status:             "False",
						LastTransitionTime: now,
					},
				},
			},
		},
		{
			name: "updating: multi-arch",
			node: &corev1.Node{
				ObjectMeta: metav1.ObjectMeta{Name: "worker-1", Annotations: map[string]string{
					"machineconfiguration.openshift.io/currentConfig": "aaa",
					"machineconfiguration.openshift.io/desiredConfig": "ccc",
					"machineconfiguration.openshift.io/currentImage":  "4.1.23",
					"machineconfiguration.openshift.io/desiredImage":  "4.1.23",
				}},
			},
			machineConfigVersions: map[string]string{
				"aaa": "4.1.23",
				"ccc": "4.1.23",
			},
			mcp: &machineconfigv1.MachineConfigPool{
				ObjectMeta: metav1.ObjectMeta{Name: "worker"},
			},
			mostRecentVersionInCVHistory: "4.1.23",
			expected: &updatestatus.NodeStatusInsight{
				Name:     "worker-1",
				Resource: updatestatus.ResourceRef{Resource: "nodes", Name: "worker-1"},
				PoolResource: updatestatus.PoolResourceRef{
					ResourceRef: updatestatus.ResourceRef{
						Group:    "machineconfiguration.openshift.io",
						Resource: "machineconfigpools",
						Name:     "worker",
					},
				},
				Scope:         "WorkerPool",
				Version:       "4.1.23",
				EstToComplete: toPointer(10 * time.Minute),
				Conditions: []metav1.Condition{
					{
						Type:               "Updating",
						Status:             "True",
						Reason:             "Updating",
						LastTransitionTime: now,
					},
					{
						Type:               "Available",
						Status:             "True",
						LastTransitionTime: now,
					},
					{
						Type:               "Degraded",
						Status:             "False",
						LastTransitionTime: now,
					},
				},
			},
		},
		{
			name: "pending",
			node: &corev1.Node{
				ObjectMeta: metav1.ObjectMeta{Name: "worker-1", Annotations: map[string]string{
					"machineconfiguration.openshift.io/currentConfig": "aaa",
					"machineconfiguration.openshift.io/desiredConfig": "aaa",
					"machineconfiguration.openshift.io/currentImage":  "bbb",
					"machineconfiguration.openshift.io/desiredImage":  "bbb",
					"machineconfiguration.openshift.io/state":         "Done",
				}},
			},
			machineConfigVersions: map[string]string{
				"aaa": "4.1.23",
			},
			mcp: &machineconfigv1.MachineConfigPool{
				ObjectMeta: metav1.ObjectMeta{Name: "worker"},
			},
			mostRecentVersionInCVHistory: "4.1.26",
			expected: &updatestatus.NodeStatusInsight{
				Name:     "worker-1",
				Resource: updatestatus.ResourceRef{Resource: "nodes", Name: "worker-1"},
				PoolResource: updatestatus.PoolResourceRef{
					ResourceRef: updatestatus.ResourceRef{
						Group:    "machineconfiguration.openshift.io",
						Resource: "machineconfigpools",
						Name:     "worker",
					},
				},
				Scope:   "WorkerPool",
				Version: "4.1.23",
				Conditions: []metav1.Condition{
					{
						Type:               "Updating",
						Status:             "False",
						Reason:             "Pending",
						LastTransitionTime: now,
					},
					{
						Type:               "Available",
						Status:             "True",
						LastTransitionTime: now,
					},
					{
						Type:               "Degraded",
						Status:             "False",
						LastTransitionTime: now,
					},
				},
			},
		},
		{
			name: "draining",
			node: &corev1.Node{
				ObjectMeta: metav1.ObjectMeta{Name: "worker-1", Annotations: map[string]string{
					"machineconfiguration.openshift.io/currentConfig":    "aaa",
					"machineconfiguration.openshift.io/desiredConfig":    "ccc",
					"machineconfiguration.openshift.io/currentImage":     "4.1.23",
					"machineconfiguration.openshift.io/desiredImage":     "4.1.26",
					"machineconfiguration.openshift.io/desiredDrain":     "drain-desired",
					"machineconfiguration.openshift.io/lastAppliedDrain": "some",
				}},
			},
			machineConfigVersions: map[string]string{
				"aaa": "4.1.23",
				"ccc": "4.1.26",
			},
			mcp: &machineconfigv1.MachineConfigPool{
				ObjectMeta: metav1.ObjectMeta{Name: "worker"},
			},
			mostRecentVersionInCVHistory: "4.1.26",
			expected: &updatestatus.NodeStatusInsight{
				Name:     "worker-1",
				Resource: updatestatus.ResourceRef{Resource: "nodes", Name: "worker-1"},
				PoolResource: updatestatus.PoolResourceRef{
					ResourceRef: updatestatus.ResourceRef{
						Group:    "machineconfiguration.openshift.io",
						Resource: "machineconfigpools",
						Name:     "worker",
					},
				},
				Scope:         "WorkerPool",
				Version:       "4.1.23",
				EstToComplete: toPointer(10 * time.Minute),
				Conditions: []metav1.Condition{
					{
						Type:               "Updating",
						Status:             "True",
						Reason:             "Draining",
						LastTransitionTime: now,
					},
					{
						Type:               "Available",
						Status:             "True",
						LastTransitionTime: now,
					},
					{
						Type:               "Degraded",
						Status:             "False",
						LastTransitionTime: now,
					},
				},
			},
		},
		{
			name: "rebooting",
			node: &corev1.Node{
				ObjectMeta: metav1.ObjectMeta{Name: "worker-1", Annotations: map[string]string{
					"machineconfiguration.openshift.io/currentConfig": "aaa",
					"machineconfiguration.openshift.io/desiredConfig": "ccc",
					"machineconfiguration.openshift.io/currentImage":  "4.1.23",
					"machineconfiguration.openshift.io/desiredImage":  "4.1.26",
					"machineconfiguration.openshift.io/state":         "Rebooting",
				}},
			},
			machineConfigVersions: map[string]string{
				"aaa": "4.1.23",
				"ccc": "4.1.26",
			},
			mcp: &machineconfigv1.MachineConfigPool{
				ObjectMeta: metav1.ObjectMeta{Name: "worker"},
			},
			mostRecentVersionInCVHistory: "4.1.26",
			expected: &updatestatus.NodeStatusInsight{
				Name:     "worker-1",
				Resource: updatestatus.ResourceRef{Resource: "nodes", Name: "worker-1"},
				PoolResource: updatestatus.PoolResourceRef{
					ResourceRef: updatestatus.ResourceRef{
						Group:    "machineconfiguration.openshift.io",
						Resource: "machineconfigpools",
						Name:     "worker",
					},
				},
				Scope:         "WorkerPool",
				Version:       "4.1.23",
				EstToComplete: toPointer(10 * time.Minute),
				Conditions: []metav1.Condition{
					{
						Type:               "Updating",
						Status:             "True",
						Reason:             "Rebooting",
						LastTransitionTime: now,
					},
					{
						Type:               "Available",
						Status:             "True",
						LastTransitionTime: now,
					},
					{
						Type:               "Degraded",
						Status:             "False",
						LastTransitionTime: now,
					},
				},
			},
		},
		{
			name: "unavailable",
			node: &corev1.Node{
				ObjectMeta: metav1.ObjectMeta{Name: "worker-1"},
				Status: corev1.NodeStatus{
					Conditions: []corev1.NodeCondition{
						{
							Type:               corev1.NodeReady,
							Status:             corev1.ConditionFalse,
							Reason:             "aaa",
							Message:            "bbb",
							LastTransitionTime: notReadyTime,
						},
					},
				},
			},
			mcp: &machineconfigv1.MachineConfigPool{
				ObjectMeta: metav1.ObjectMeta{Name: "worker"},
			},
			expected: &updatestatus.NodeStatusInsight{
				Name:     "worker-1",
				Resource: updatestatus.ResourceRef{Resource: "nodes", Name: "worker-1"},
				PoolResource: updatestatus.PoolResourceRef{
					ResourceRef: updatestatus.ResourceRef{
						Group:    "machineconfiguration.openshift.io",
						Resource: "machineconfigpools",
						Name:     "worker",
					},
				},
				Scope:   "WorkerPool",
				Message: "Node worker-1 is not ready",
				Conditions: []metav1.Condition{
					{
						Type:               "Updating",
						Status:             "False",
						LastTransitionTime: now,
						Reason:             "Pending",
					},
					{
						Type:               "Available",
						Status:             "False",
						Reason:             "Not ready",
						Message:            "Node worker-1 is not ready",
						LastTransitionTime: notReadyTime,
					},
					{
						Type:               "Degraded",
						Status:             "False",
						LastTransitionTime: now,
					},
				},
			},
		},
		{
			name: "degraded",
			node: &corev1.Node{
				ObjectMeta: metav1.ObjectMeta{Name: "worker-1", Annotations: map[string]string{
					"machineconfiguration.openshift.io/currentConfig": "aaa",
					"machineconfiguration.openshift.io/desiredConfig": "aaa",
					"machineconfiguration.openshift.io/currentImage":  "bbb",
					"machineconfiguration.openshift.io/desiredImage":  "bbb",
					"machineconfiguration.openshift.io/state":         "Degraded",
					"machineconfiguration.openshift.io/reason":        "bla",
				}},
			},
			machineConfigVersions: map[string]string{
				"aaa": "4.1.23",
			},
			mcp: &machineconfigv1.MachineConfigPool{
				ObjectMeta: metav1.ObjectMeta{Name: "worker"},
			},
			mostRecentVersionInCVHistory: "4.1.26",
			expected: &updatestatus.NodeStatusInsight{
				Name:     "worker-1",
				Resource: updatestatus.ResourceRef{Resource: "nodes", Name: "worker-1"},
				PoolResource: updatestatus.PoolResourceRef{
					ResourceRef: updatestatus.ResourceRef{
						Group:    "machineconfiguration.openshift.io",
						Resource: "machineconfigpools",
						Name:     "worker",
					},
				},
				Message: "bla",
				Scope:   "WorkerPool",
				Version: "4.1.23",
				Conditions: []metav1.Condition{
					{
						Type:               "Updating",
						Status:             "False",
						LastTransitionTime: now,
						Reason:             "Pending",
					},
					{
						Type:               "Available",
						Status:             "True",
						LastTransitionTime: now,
					},
					{
						Type:               "Degraded",
						Status:             "True",
						Reason:             "bla",
						Message:            "bla",
						LastTransitionTime: now,
					},
				},
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {

			actual := assessNode(tc.node, tc.mcp, func(k string) (string, bool) {
				v, ok := tc.machineConfigVersions[k]
				return v, ok
			}, tc.mostRecentVersionInCVHistory, now)

			if diff := cmp.Diff(tc.expected, actual); diff != "" {
				t.Errorf("%s: node status insight differs from expected:\n%s", tc.name, diff)
			}

		})
	}
}

func Test_sync_with_node(t *testing.T) {
	now := metav1.Now()
	cv := &configv1.ClusterVersion{
		ObjectMeta: metav1.ObjectMeta{Name: "version"},
		Status: configv1.ClusterVersionStatus{History: []configv1.UpdateHistory{
			{Version: "4.1.26"},
		}},
	}

	mcIndexer := cache.NewIndexer(cache.MetaNamespaceKeyFunc, cache.Indexers{})
	for _, o := range []metav1.Object{&machineconfigv1.MachineConfig{
		ObjectMeta: metav1.ObjectMeta{Name: "aaa", Annotations: map[string]string{
			"machineconfiguration.openshift.io/release-image-version": "4.1.23",
		}},
	}, &machineconfigv1.MachineConfig{
		ObjectMeta: metav1.ObjectMeta{Name: "ccc", Annotations: map[string]string{
			"machineconfiguration.openshift.io/release-image-version": "4.1.26",
		}},
	}} {
		if err := mcIndexer.Add(o); err != nil {
			t.Fatalf("Failed to add object to indexer: %v", err)
		}
	}
	mcLister := machineconfigv1listers.NewMachineConfigLister(mcIndexer)

	mcpIndexer := cache.NewIndexer(cache.MetaNamespaceKeyFunc, cache.Indexers{})
	for _, o := range []metav1.Object{getMCP("master"), getMCP("worker")} {
		if err := mcpIndexer.Add(o); err != nil {
			t.Fatalf("Failed to add object to indexer: %v", err)
		}
	}
	mcpLister := machineconfigv1listers.NewMachineConfigPoolLister(mcpIndexer)

	testCases := []struct {
		name string

		node *corev1.Node

		expectedErr  error
		expectedMsgs map[string]updatestatus.WorkerPoolInsight
	}{
		{
			name: "Node's update is pending",
			node: &corev1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Name:   "worker-1",
					Labels: map[string]string{"machine.openshift.io/interruptible-instance": "", "node-role.kubernetes.io/worker": ""},
					Annotations: map[string]string{
						"machineconfiguration.openshift.io/currentConfig": "aaa",
						"machineconfiguration.openshift.io/desiredConfig": "aaa",
						"machineconfiguration.openshift.io/currentImage":  "bbb",
						"machineconfiguration.openshift.io/desiredImage":  "bbb",
						"machineconfiguration.openshift.io/state":         "Done",
					},
				},
			},
			expectedMsgs: map[string]updatestatus.WorkerPoolInsight{
				"node-worker-1": {
					UID:        "node-worker-1",
					AcquiredAt: now,
					WorkerPoolInsightUnion: updatestatus.WorkerPoolInsightUnion{
						Type: updatestatus.NodeStatusInsightType,
						NodeStatusInsight: &updatestatus.NodeStatusInsight{
							Name: "worker-1",
							PoolResource: updatestatus.PoolResourceRef{
								ResourceRef: updatestatus.ResourceRef{
									Resource: "machineconfigpools",
									Group:    "machineconfiguration.openshift.io",
									Name:     "worker",
								},
							},
							Resource: updatestatus.ResourceRef{Resource: "nodes", Name: "worker-1"},
							Scope:    "WorkerPool",
							Version:  "4.1.23",
							Conditions: []metav1.Condition{
								{Type: "Updating", Status: "False", LastTransitionTime: now, Reason: "Pending"},
								{Type: "Available", Status: "True", LastTransitionTime: now},
								{Type: "Degraded", Status: "False", LastTransitionTime: now},
							},
						},
					},
				},
			},
		},
		{
			name: "Node's update is ongoing",
			node: &corev1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Name:   "worker-1",
					Labels: map[string]string{"machine.openshift.io/interruptible-instance": "", "node-role.kubernetes.io/worker": ""},
					Annotations: map[string]string{
						"machineconfiguration.openshift.io/currentConfig": "aaa",
						"machineconfiguration.openshift.io/desiredConfig": "ccc",
						"machineconfiguration.openshift.io/currentImage":  "bbb",
						"machineconfiguration.openshift.io/desiredImage":  "ddd",
					},
				},
			},
			expectedMsgs: map[string]updatestatus.WorkerPoolInsight{
				"node-worker-1": {
					UID:        "node-worker-1",
					AcquiredAt: now,
					WorkerPoolInsightUnion: updatestatus.WorkerPoolInsightUnion{
						Type: updatestatus.NodeStatusInsightType,
						NodeStatusInsight: &updatestatus.NodeStatusInsight{
							Name: "worker-1",
							PoolResource: updatestatus.PoolResourceRef{
								ResourceRef: updatestatus.ResourceRef{
									Resource: "machineconfigpools",
									Group:    "machineconfiguration.openshift.io",
									Name:     "worker",
								},
							},
							Resource:      updatestatus.ResourceRef{Resource: "nodes", Name: "worker-1"},
							Scope:         "WorkerPool",
							Version:       "4.1.23",
							EstToComplete: toPointer(10 * time.Minute),
							Conditions: []metav1.Condition{
								{Type: "Updating", Status: "True", LastTransitionTime: now, Reason: "Updating"},
								{Type: "Available", Status: "True", LastTransitionTime: now},
								{Type: "Degraded", Status: "False", LastTransitionTime: now},
							},
						},
					},
				},
			},
		},
		{
			name: "error",
			node: &corev1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Name:   "worker-1",
					Labels: map[string]string{"node-role.kubernetes.io/some": ""},
				},
			},
			expectedErr: fmt.Errorf("failed to determine which machine config pool the node worker-1 belongs to"),
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {

			nodeIndexer := cache.NewIndexer(cache.MetaNamespaceKeyFunc, cache.Indexers{})
			if err := nodeIndexer.Add(tc.node); err != nil {
				t.Fatalf("Failed to add ClusterOperator to indexer: %v", err)
			}
			nodeLister := corelistersv1.NewNodeLister(nodeIndexer)

			var actualMsgs []informerMsg
			var sendInsight sendInsightFn = func(insight informerMsg) {
				actualMsgs = append(actualMsgs, insight)
			}

			controller := nodeInformerController{
				nodes:              nodeLister,
				configClient:       fakeconfigv1client.NewClientset(cv),
				machineConfigs:     mcLister,
				machineConfigPools: mcpLister,
				sendInsight:        sendInsight,
				now:                func() metav1.Time { return now },
			}

			if err := controller.initializeCaches(); err != nil {
				t.Errorf("Failed to initialize caches: %v", err)
			}

			queueKey := nodeInformerControllerQueueKeys(tc.node)[0]

			actualErr := controller.sync(context.TODO(), newTestSyncContext(queueKey))

			if diff := cmp.Diff(tc.expectedErr, actualErr, cmp.Comparer(func(x, y error) bool {
				if x == nil || y == nil {
					return x == nil && y == nil
				}
				return x.Error() == y.Error()
			})); diff != "" {
				t.Errorf("%s: error differs from expected:\n%s", tc.name, diff)
			}

			var expectedMsgs []informerMsg
			for uid, insight := range tc.expectedMsgs {
				raw, err := yaml.Marshal(insight)
				if err != nil {
					t.Fatalf("Failed to marshal expected insight: %v", err)
				}
				expectedMsgs = append(expectedMsgs, informerMsg{
					informer: nodesInformerName,
					uid:      uid,
					insight:  raw,
				})
			}

			ignoreOrder := cmpopts.SortSlices(func(a, b informerMsg) bool {
				return a.uid < b.uid
			})

			if diff := cmp.Diff(expectedMsgs, actualMsgs, ignoreOrder, cmp.AllowUnexported(informerMsg{})); diff != "" {
				t.Errorf("Sync messages differ from expected:\n%s", diff)
			}

			for _, msg := range actualMsgs {
				if err := msg.validate(); err != nil {
					t.Errorf("Received message is invalid: %v\nMessage content: %v", err, msg)
				}
			}
		})
	}
}

func Test_sync_with_mcp(t *testing.T) {
	now := metav1.Now()

	mcIndexer := cache.NewIndexer(cache.MetaNamespaceKeyFunc, cache.Indexers{})
	for _, o := range []metav1.Object{} {
		if err := mcIndexer.Add(o); err != nil {
			t.Fatalf("Failed to add object to indexer: %v", err)
		}
	}
	mcLister := machineconfigv1listers.NewMachineConfigLister(mcIndexer)

	nodeIndexer := cache.NewIndexer(cache.MetaNamespaceKeyFunc, cache.Indexers{})
	for _, o := range []metav1.Object{&corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "master-1"}},
		&corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "worker-1"}}} {
		if err := nodeIndexer.Add(o); err != nil {
			t.Fatalf("Failed to add object to indexer: %v", err)
		}
	}
	nodeLister := corelistersv1.NewNodeLister(nodeIndexer)

	testCases := []struct {
		name string

		object                runtime.Object
		pools                 []*machineconfigv1.MachineConfigPool
		mcpToRemove, mcpToAdd string

		expectedErr      error
		expectedQueueLen int
	}{
		{
			name: "reconcile for deleted pool",
			object: &machineconfigv1.MachineConfigPool{
				ObjectMeta: metav1.ObjectMeta{
					Name: "non-exist-mcp",
				},
			},
			pools:            []*machineconfigv1.MachineConfigPool{getMCP("master"), getMCP("worker"), getMCP("non-exist-mcp")},
			mcpToRemove:      "non-exist-mcp",
			expectedQueueLen: 2,
		},
		{
			name: "reconcile for a new pool",
			object: &machineconfigv1.MachineConfigPool{
				ObjectMeta: metav1.ObjectMeta{
					Name: "infra",
				},
			},
			pools:            []*machineconfigv1.MachineConfigPool{getMCP("master"), getMCP("worker")},
			mcpToAdd:         "infra",
			expectedQueueLen: 2,
		},
		{
			name: "no-op if not-found",
			object: &machineconfigv1.MachineConfigPool{
				ObjectMeta: metav1.ObjectMeta{
					Name: "non-exist-mcp",
				},
			},
			pools: []*machineconfigv1.MachineConfigPool{getMCP("master"), getMCP("worker")},
		},
		{
			name: "no-op if existing",
			object: &machineconfigv1.MachineConfigPool{
				ObjectMeta: metav1.ObjectMeta{
					Name: "worker",
				},
			},
			pools: []*machineconfigv1.MachineConfigPool{getMCP("master"), getMCP("worker")},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {

			var actualMsgs []informerMsg
			var sendInsight sendInsightFn = func(insight informerMsg) {
				actualMsgs = append(actualMsgs, insight)
			}

			mcpIndexer := cache.NewIndexer(cache.MetaNamespaceKeyFunc, cache.Indexers{})
			for _, o := range tc.pools {
				if err := mcpIndexer.Add(o); err != nil {
					t.Fatalf("Failed to add object to indexer: %v", err)
				}
			}
			mcpLister := machineconfigv1listers.NewMachineConfigPoolLister(mcpIndexer)

			controller := nodeInformerController{
				nodes:              nodeLister,
				machineConfigs:     mcLister,
				machineConfigPools: mcpLister,
				sendInsight:        sendInsight,
				now:                func() metav1.Time { return now },
			}

			if err := controller.initializeCaches(); err != nil {
				t.Errorf("Failed to initialize caches: %v", err)
			}

			if tc.mcpToRemove != "" {
				if err := mcpIndexer.Delete(getMCP(tc.mcpToRemove)); err != nil {
					t.Fatalf("Failed to remove mcp: %v", err)
				}
			}

			if tc.mcpToAdd != "" {
				if err := mcpIndexer.Add(getMCP(tc.mcpToAdd)); err != nil {
					t.Fatalf("Failed to remove mcp: %v", err)
				}
			}

			queueKey := nodeInformerControllerQueueKeys(tc.object)[0]

			syncContext := newTestSyncContext(queueKey)
			actualErr := controller.sync(context.TODO(), syncContext)

			if diff := cmp.Diff(tc.expectedErr, actualErr, cmp.Comparer(func(x, y error) bool {
				if x == nil || y == nil {
					return x == nil && y == nil
				}
				return x.Error() == y.Error()
			})); diff != "" {
				t.Errorf("%s: error differs from expected:\n%s", tc.name, diff)
			}

			if diff := cmp.Diff(tc.expectedQueueLen, syncContext.Queue().Len()); diff != "" {
				t.Errorf("queue length after sync differs from expected:\n%s", diff)
			}

			var expectedMsgs []informerMsg
			if diff := cmp.Diff(expectedMsgs, actualMsgs, cmp.AllowUnexported(informerMsg{})); diff != "" {
				t.Errorf("Sync messages differ from expected:\n%s", diff)
			}
		})
	}
}

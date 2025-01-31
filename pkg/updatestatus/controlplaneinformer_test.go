package updatestatus

import (
	"context"
	"errors"
	"github.com/openshift/library-go/pkg/controller/factory"
	kerrors "k8s.io/apimachinery/pkg/api/errors"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"gopkg.in/yaml.v3"
	"k8s.io/utils/ptr"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	fakekubeclient "k8s.io/client-go/kubernetes/fake"
	appsv1client "k8s.io/client-go/kubernetes/typed/apps/v1"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/util/workqueue"

	configv1 "github.com/openshift/api/config/v1"
	configv1listers "github.com/openshift/client-go/config/listers/config/v1"
	"github.com/openshift/library-go/pkg/operator/events"
)

func newClusterVersionStatusInsightUpdating(status metav1.ConditionStatus, reason ClusterVersionStatusInsightUpdatingReason, message string, lastTransitionTime metav1.Time) metav1.Condition {
	return metav1.Condition{
		Type:               string(ClusterVersionStatusInsightUpdating),
		Status:             status,
		Reason:             string(reason),
		Message:            message,
		LastTransitionTime: lastTransitionTime,
	}
}

func Test_sync_with_cv(t *testing.T) {
	now := metav1.Now()
	var minutesAgo [120]metav1.Time
	for i := range minutesAgo {
		minutesAgo[i] = metav1.NewTime(now.Time.Add(-time.Duration(i) * time.Minute))
	}

	progressingTrue := configv1.ClusterOperatorStatusCondition{
		Type:    configv1.OperatorProgressing,
		Status:  configv1.ConditionTrue,
		Reason:  "ProgressingTrue",
		Message: "Cluster is progressing",
	}
	progressingFalse := configv1.ClusterOperatorStatusCondition{
		Type:    configv1.OperatorProgressing,
		Status:  configv1.ConditionFalse,
		Reason:  "ProgressingFalse",
		Message: "Cluster is on version 4.X.0",
	}

	inProgress418 := configv1.UpdateHistory{
		State:       configv1.PartialUpdate,
		StartedTime: minutesAgo[90],
		Version:     "4.18.0",
		Image:       "pullspec-4.18.0",
	}
	var completed418 configv1.UpdateHistory
	inProgress418.DeepCopyInto(&completed418)
	completed418.State = configv1.CompletedUpdate
	completed418.CompletionTime = &minutesAgo[60]

	inProgress419 := configv1.UpdateHistory{
		State:       configv1.PartialUpdate,
		StartedTime: minutesAgo[60],
		Version:     "4.19.0",
		Image:       "pullspec-4.19.0",
	}
	var completed419 configv1.UpdateHistory
	inProgress419.DeepCopyInto(&completed419)
	completed419.State = configv1.CompletedUpdate
	completed419.CompletionTime = &minutesAgo[30]

	cvRef := ResourceRef{Resource: "clusterversions", Group: "config.openshift.io", Name: "version"}

	testCases := []struct {
		name          string
		cvProgressing *configv1.ClusterOperatorStatusCondition
		cvHistory     []configv1.UpdateHistory

		expectedMsgs map[string]Insight
	}{
		{
			name:          "Cluster during installation",
			cvProgressing: &progressingTrue,
			cvHistory:     []configv1.UpdateHistory{inProgress418},
			expectedMsgs: map[string]Insight{
				"usc-cv-version": {
					UID:        "usc-cv-version",
					AcquiredAt: now,
					InsightUnion: InsightUnion{
						Type: ClusterVersionStatusInsightType,
						ClusterVersionStatusInsight: &ClusterVersionStatusInsight{
							Resource:   cvRef,
							Assessment: ControlPlaneAssessmentProgressing,
							Versions: ControlPlaneUpdateVersions{
								Target: Version{
									Version:  "4.18.0",
									Metadata: []VersionMetadata{{Key: InstallationMetadata}},
								},
							},
							Completion:           0,
							StartedAt:            minutesAgo[90],
							EstimatedCompletedAt: &minutesAgo[30],
							Conditions: []metav1.Condition{
								newClusterVersionStatusInsightUpdating(
									metav1.ConditionTrue,
									ClusterVersionProgressing,
									"ClusterVersion has Progressing=True(Reason=ProgressingTrue) | Message='Cluster is progressing'",
									now,
								),
							},
						},
					},
				},
			},
		},
		{
			name:          "Cluster after installation",
			cvProgressing: &progressingFalse,
			cvHistory:     []configv1.UpdateHistory{completed418},
			expectedMsgs: map[string]Insight{
				"usc-cv-version": {
					UID:        "usc-cv-version",
					AcquiredAt: now,
					InsightUnion: InsightUnion{
						Type: ClusterVersionStatusInsightType,
						ClusterVersionStatusInsight: &ClusterVersionStatusInsight{
							Resource:   cvRef,
							Assessment: ControlPlaneAssessmentCompleted,
							Versions: ControlPlaneUpdateVersions{
								Target: Version{
									Version:  "4.18.0",
									Metadata: []VersionMetadata{{Key: InstallationMetadata}},
								},
							},
							Completion:           100,
							StartedAt:            minutesAgo[90],
							CompletedAt:          &minutesAgo[60],
							EstimatedCompletedAt: &minutesAgo[30],
							Conditions: []metav1.Condition{
								newClusterVersionStatusInsightUpdating(
									metav1.ConditionFalse,
									ClusterVersionNotProgressing,
									"ClusterVersion has Progressing=False(Reason=ProgressingFalse) | Message='Cluster is on version 4.X.0'",
									now,
								),
							},
						},
					},
				},
			},
		},
		{
			name:          "Cluster during a standard update",
			cvProgressing: &progressingTrue,
			cvHistory:     []configv1.UpdateHistory{inProgress419, completed418},
			expectedMsgs: map[string]Insight{
				"usc-cv-version": {
					UID:        "usc-cv-version",
					AcquiredAt: now,
					InsightUnion: InsightUnion{
						Type: ClusterVersionStatusInsightType,
						ClusterVersionStatusInsight: &ClusterVersionStatusInsight{
							Resource:   cvRef,
							Assessment: ControlPlaneAssessmentProgressing,
							Versions: ControlPlaneUpdateVersions{
								Target:   Version{Version: "4.19.0"},
								Previous: Version{Version: "4.18.0"},
							},
							Completion:           0,
							StartedAt:            minutesAgo[60],
							EstimatedCompletedAt: &now,
							Conditions: []metav1.Condition{
								newClusterVersionStatusInsightUpdating(
									metav1.ConditionTrue,
									ClusterVersionProgressing,
									"ClusterVersion has Progressing=True(Reason=ProgressingTrue) | Message='Cluster is progressing'",
									now,
								),
							},
						},
					},
				},
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			cv := makeTestClusterVersion(tc.cvProgressing, tc.cvHistory)

			cvIndexer := cache.NewIndexer(cache.MetaNamespaceKeyFunc, cache.Indexers{})
			if err := cvIndexer.Add(cv); err != nil {
				t.Fatalf("Failed to add ClusterVersion to indexer: %v", err)
			}
			cvLister := configv1listers.NewClusterVersionLister(cvIndexer)

			var actualMsgs []informerMsg
			var sendInsight sendInsightFn = func(insight informerMsg) {
				actualMsgs = append(actualMsgs, insight)
			}

			controller := controlPlaneInformerController{
				clusterVersions: cvLister,
				sendInsight:     sendInsight,
				now:             func() metav1.Time { return now },
			}

			queueKey := controlPlaneInformerQueueKeys(cv)[0]

			err := controller.sync(context.Background(), newTestSyncContext(queueKey))
			if err != nil {
				t.Fatalf("unexpected error from sync(): %v", err)
			}

			var expectedMsgs []informerMsg
			for uid, insight := range tc.expectedMsgs {
				raw, err := yaml.Marshal(insight)
				if err != nil {
					t.Fatalf("Failed to marshal expected insight: %v", err)
				}
				expectedMsgs = append(expectedMsgs, informerMsg{
					uid:     uid,
					insight: raw,
				})
			}

			if diff := cmp.Diff(expectedMsgs, actualMsgs, cmp.AllowUnexported(informerMsg{})); diff != "" {
				t.Errorf("Sync messages differ from expected:\n%s", diff)
			}
		})
	}
}

func Test_getImagePullSpec(t *testing.T) {
	badMCODeployment := mcoDeployment.DeepCopy()
	badMCODeployment.Spec.Template.Spec.Containers[0].Name = "wrong-name"
	testCases := []struct {
		name string

		co         string
		appsClient appsv1client.AppsV1Interface

		expected    string
		expectedErr error
	}{
		{
			name:        "co/machine-config with nil apps client",
			co:          "machine-config",
			expectedErr: errors.New("apps client is nil"),
		},
		{
			name:        "co/machine-config: error getting MCO deployment",
			co:          "machine-config",
			appsClient:  fakekubeclient.NewClientset().AppsV1(),
			expectedErr: &kerrors.StatusError{ErrStatus: metav1.Status{Message: `deployments.apps "machine-config-operator" not found`}},
		},
		{
			name:        "co/machine-config: MCO deployment with a wrong name",
			co:          "machine-config",
			appsClient:  fakekubeclient.NewClientset(badMCODeployment).AppsV1(),
			expectedErr: errors.New("machine-config-operator container not found"),
		},
		{
			name:       "co/machine-config: all good",
			co:         "machine-config",
			appsClient: fakekubeclient.NewClientset(mcoDeployment.DeepCopy()).AppsV1(),
			expected:   "machine-config-operator:latest",
		},
		{
			name:        "co/unknown: the not-implemented error raised",
			co:          "unknown",
			expectedErr: operatorImageNotImplemented,
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			actual, actualErr := getImagePullSpec(context.TODO(), tc.co, tc.appsClient)

			if diff := cmp.Diff(tc.expectedErr, actualErr, cmp.Comparer(func(x, y error) bool {
				if x == nil || y == nil {
					return x == nil && y == nil
				}
				return x.Error() == y.Error()
			})); diff != "" {
				t.Errorf("%s: error differs from expected:\n%s", tc.name, diff)
			}

			if tc.expectedErr == nil {
				if diff := cmp.Diff(tc.expected, actual); diff != "" {
					t.Errorf("%s: image pull spec differs from expected:\n%s", tc.name, diff)
				}
			}
		})
	}
}

func Test_clusterOperatorEventFilterFunc(t *testing.T) {
	testCases := []struct {
		name string

		object runtime.Object

		expected bool
	}{
		{
			name: "nil object",
		},
		{
			name:   "unexpected type",
			object: &configv1.Image{},
		},
		{
			name:     "co with include annotation",
			object:   &configv1.ClusterOperator{ObjectMeta: metav1.ObjectMeta{Name: "bar", Annotations: map[string]string{"include.release.openshift.io/": ""}}},
			expected: true,
		},
		{
			name:     "co with exclude annotation",
			object:   &configv1.ClusterOperator{ObjectMeta: metav1.ObjectMeta{Name: "bar", Annotations: map[string]string{"exclude.release.openshift.io/": ""}}},
			expected: true,
		},
		{
			name:   "co without the openshift annotation",
			object: &configv1.ClusterOperator{ObjectMeta: metav1.ObjectMeta{Name: "bar"}},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			actual := clusterOperatorEventFilterFunc(tc.object)
			if diff := cmp.Diff(tc.expected, actual); diff != "" {
				t.Errorf("%s: result differs from expected:\n%s", tc.name, diff)
			}
		})
	}
}

func Test_configApiQueueKeys(t *testing.T) {
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
			name:         "cv",
			object:       &configv1.ClusterVersion{ObjectMeta: metav1.ObjectMeta{Name: "bar"}},
			expected:     []string{"ClusterVersion/bar"},
			expectedKind: "ClusterVersion",
			expectedName: "bar",
		},
		{
			name:         "co",
			object:       &configv1.ClusterOperator{ObjectMeta: metav1.ObjectMeta{Name: "bar", Annotations: map[string]string{"include.release.openshift.io/": ""}}},
			expected:     []string{"ClusterOperator/bar"},
			expectedKind: "ClusterOperator",
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

			actual := controlPlaneInformerQueueKeys(tc.object)

			if diff := cmp.Diff(tc.expected, actual); diff != "" {
				t.Errorf("%s: key differs from expected:\n%s", tc.name, diff)
			}

			if !tc.expectedPanic && len(actual) > 0 {
				kind, name, err := parseControlPlaneInformerQueueKey(actual[0])
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

var mcoDeployment = appsv1.Deployment{
	ObjectMeta: metav1.ObjectMeta{Name: "machine-config-operator", Namespace: "openshift-machine-config-operator"},
	Spec: appsv1.DeploymentSpec{
		Template: corev1.PodTemplateSpec{
			Spec: corev1.PodSpec{
				Containers: []corev1.Container{{Name: "machine-config-operator", Image: "machine-config-operator:latest"}}},
		},
	},
}

func Test_sync_with_co(t *testing.T) {
	now := metav1.Now()
	cv := &configv1.ClusterVersion{
		ObjectMeta: metav1.ObjectMeta{Name: "version"},
		Status:     configv1.ClusterVersionStatus{Desired: configv1.Release{Version: "4.18.2"}},
	}
	co := &configv1.ClusterOperator{
		ObjectMeta: metav1.ObjectMeta{Name: "some-co", Annotations: map[string]string{"exclude.release.openshift.io/": ""}},
		Status: configv1.ClusterOperatorStatus{
			Versions: []configv1.OperandVersion{
				{Name: "operator", Version: "4.18.1"},
			},
			Conditions: []configv1.ClusterOperatorStatusCondition{
				{
					Type:   configv1.OperatorProgressing,
					Status: configv1.ConditionTrue,
				},
				{
					Type:   configv1.OperatorAvailable,
					Status: configv1.ConditionTrue,
				},
			},
		},
	}

	testCases := []struct {
		name string

		expectedMsgs map[string]Insight
	}{
		{
			name: "Cluster during installation",
			expectedMsgs: map[string]Insight{
				"usc-co-some-co": {
					UID:        "usc-co-some-co",
					AcquiredAt: now,
					InsightUnion: InsightUnion{
						Type: ClusterOperatorStatusInsightType,
						ClusterOperatorStatusInsight: &ClusterOperatorStatusInsight{
							Name:     "some-co",
							Resource: ResourceRef{Resource: "clusteroperators", Group: "config.openshift.io", Name: "some-co"},
							Conditions: []metav1.Condition{
								{Type: "Updating", Status: "True", LastTransitionTime: now, Reason: "Progressing"},
								{Type: "Healthy", Status: "True", LastTransitionTime: now, Reason: "AsExpected"},
							},
						},
					},
				},
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			cvIndexer := cache.NewIndexer(cache.MetaNamespaceKeyFunc, cache.Indexers{})
			if err := cvIndexer.Add(cv); err != nil {
				t.Fatalf("Failed to add ClusterVersion to indexer: %v", err)
			}
			cvLister := configv1listers.NewClusterVersionLister(cvIndexer)

			coIndexer := cache.NewIndexer(cache.MetaNamespaceKeyFunc, cache.Indexers{})
			if err := coIndexer.Add(co); err != nil {
				t.Fatalf("Failed to add ClusterOperator to indexer: %v", err)
			}
			coLister := configv1listers.NewClusterOperatorLister(coIndexer)

			var actualMsgs []informerMsg
			var sendInsight sendInsightFn = func(insight informerMsg) {
				actualMsgs = append(actualMsgs, insight)
			}

			controller := controlPlaneInformerController{
				clusterVersions:  cvLister,
				clusterOperators: coLister,
				appsClient:       fakekubeclient.NewClientset(mcoDeployment.DeepCopy()).AppsV1(),
				sendInsight:      sendInsight,
				now:              func() metav1.Time { return now },
			}

			queueKey := controlPlaneInformerQueueKeys(co)[0]

			err := controller.sync(context.Background(), newTestSyncContext(queueKey))
			if err != nil {
				t.Fatalf("unexpected error from sync(): %v", err)
			}

			var expectedMsgs []informerMsg
			for uid, insight := range tc.expectedMsgs {
				raw, err := yaml.Marshal(insight)
				if err != nil {
					t.Fatalf("Failed to marshal expected insight: %v", err)
				}
				expectedMsgs = append(expectedMsgs, informerMsg{
					uid:     uid,
					insight: raw,
				})
			}

			if diff := cmp.Diff(expectedMsgs, actualMsgs, cmp.AllowUnexported(informerMsg{})); diff != "" {
				t.Errorf("Sync messages differ from expected:\n%s", diff)
			}
		})
	}
}

func makeTestClusterVersion(progressing *configv1.ClusterOperatorStatusCondition, history []configv1.UpdateHistory) *configv1.ClusterVersion {
	cv := &configv1.ClusterVersion{
		ObjectMeta: metav1.ObjectMeta{Name: "version"},
		Status: configv1.ClusterVersionStatus{
			Conditions: []configv1.ClusterOperatorStatusCondition{},
			History:    []configv1.UpdateHistory{},
		},
	}

	if progressing != nil {
		cv.Status.Conditions = append(cv.Status.Conditions, *progressing)
	}
	cv.Status.History = append(cv.Status.History, history...)

	return cv
}

type testSyncContext struct {
	queueKey      string
	eventRecorder events.Recorder
	queue         workqueue.TypedRateLimitingInterface[any]
}

func (c testSyncContext) Queue() workqueue.RateLimitingInterface { //nolint:staticcheck
	return c.queue
}

func (c testSyncContext) QueueKey() string {
	return c.queueKey
}

func (c testSyncContext) Recorder() events.Recorder {
	return c.eventRecorder
}

func newTestSyncContext(queueKey string) factory.SyncContext {
	return testSyncContext{
		queueKey:      queueKey,
		eventRecorder: events.NewInMemoryRecorder("test"),
	}
}

func Test_assessClusterOperator(t *testing.T) {
	now := metav1.Now()
	testCases := []struct {
		name string

		co            *configv1.ClusterOperator
		targetVersion string
		appsClient    appsv1client.AppsV1Interface

		expected    *ClusterOperatorStatusInsight
		expectedErr error
	}{
		{
			name: "cluster operator has no status",
			co: &configv1.ClusterOperator{
				ObjectMeta: metav1.ObjectMeta{Name: "some-co"},
			},
			expected: &ClusterOperatorStatusInsight{
				Name:     "some-co",
				Resource: ResourceRef{Group: "config.openshift.io", Resource: "clusteroperators", Name: "some-co"},
				Conditions: []metav1.Condition{
					{
						Type:               "Updating",
						Status:             "Unknown",
						Reason:             "CannotDetermine",
						LastTransitionTime: now,
					},
					{
						Type:               "Healthy",
						Status:             "Unknown",
						Reason:             "Unavailable",
						Message:            "The cluster operator is unavailable because the available condition is not found in the cluster operator's status",
						LastTransitionTime: now,
					},
				},
			},
		},
		{
			name: "cluster operator is updated",
			co: &configv1.ClusterOperator{
				ObjectMeta: metav1.ObjectMeta{Name: "some-co"},
				Status: configv1.ClusterOperatorStatus{
					Versions: []configv1.OperandVersion{
						{Name: "operator", Version: "x"},
					},
					Conditions: []configv1.ClusterOperatorStatusCondition{
						{
							Type:   configv1.OperatorProgressing,
							Status: configv1.ConditionFalse,
						},
						{
							Type:   configv1.OperatorAvailable,
							Status: configv1.ConditionTrue,
						},
					},
				},
			},
			targetVersion: "x",
			expected: &ClusterOperatorStatusInsight{
				Name:     "some-co",
				Resource: ResourceRef{Group: "config.openshift.io", Resource: "clusteroperators", Name: "some-co"},
				Conditions: []metav1.Condition{
					{
						Type:               "Updating",
						Status:             "False",
						Reason:             "Updated",
						LastTransitionTime: now,
					},
					{
						Type:               "Healthy",
						Status:             "True",
						Reason:             "AsExpected",
						LastTransitionTime: now,
					},
				},
			},
		},
		{
			name: "cluster operator is being updated",
			co: &configv1.ClusterOperator{
				ObjectMeta: metav1.ObjectMeta{Name: "some-co"},
				Status: configv1.ClusterOperatorStatus{
					Versions: []configv1.OperandVersion{
						{Name: "operator", Version: "x"},
					},
					Conditions: []configv1.ClusterOperatorStatusCondition{
						{
							Type:   configv1.OperatorProgressing,
							Status: configv1.ConditionTrue,
						},
						{
							Type:   configv1.OperatorAvailable,
							Status: configv1.ConditionTrue,
						},
					},
				},
			},
			targetVersion: "y",
			expected: &ClusterOperatorStatusInsight{
				Name:     "some-co",
				Resource: ResourceRef{Group: "config.openshift.io", Resource: "clusteroperators", Name: "some-co"},
				Conditions: []metav1.Condition{
					{
						Type:               "Updating",
						Status:             "True",
						Reason:             "Progressing",
						LastTransitionTime: now,
					},
					{
						Type:               "Healthy",
						Status:             "True",
						Reason:             "AsExpected",
						LastTransitionTime: now,
					},
				},
			},
		},
		{
			name: "machine-config is being updated",
			co: &configv1.ClusterOperator{
				ObjectMeta: metav1.ObjectMeta{Name: "machine-config"},
				Status: configv1.ClusterOperatorStatus{
					Versions: []configv1.OperandVersion{
						{Name: "operator", Version: "x"},
					},
					Conditions: []configv1.ClusterOperatorStatusCondition{
						{
							Type:   configv1.OperatorProgressing,
							Status: configv1.ConditionTrue,
						},
						{
							Type:   configv1.OperatorAvailable,
							Status: configv1.ConditionTrue,
						},
					},
				},
			},
			targetVersion: "y",
			appsClient:    fakekubeclient.NewClientset(mcoDeployment.DeepCopy()).AppsV1(),
			expected: &ClusterOperatorStatusInsight{
				Name:     "machine-config",
				Resource: ResourceRef{Group: "config.openshift.io", Resource: "clusteroperators", Name: "machine-config"},
				Conditions: []metav1.Condition{
					{
						Type:               "Updating",
						Status:             "True",
						Reason:             "Progressing",
						LastTransitionTime: now,
					},
					{
						Type:               "Healthy",
						Status:             "True",
						Reason:             "AsExpected",
						LastTransitionTime: now,
					},
				},
			},
		},
		{
			name: "machine-config is being updated to multi-arch",
			co: &configv1.ClusterOperator{
				ObjectMeta: metav1.ObjectMeta{Name: "machine-config"},
				Status: configv1.ClusterOperatorStatus{
					Versions: []configv1.OperandVersion{
						{Name: "operator", Version: "x"},
						{Name: "operator-image", Version: "new-pull-spec"},
					},
					Conditions: []configv1.ClusterOperatorStatusCondition{
						{
							Type:   configv1.OperatorProgressing,
							Status: configv1.ConditionTrue,
						},
						{
							Type:   configv1.OperatorAvailable,
							Status: configv1.ConditionTrue,
						},
					},
				},
			},
			targetVersion: "x",
			appsClient:    fakekubeclient.NewClientset(mcoDeployment.DeepCopy()).AppsV1(),
			expected: &ClusterOperatorStatusInsight{
				Name:     "machine-config",
				Resource: ResourceRef{Group: "config.openshift.io", Resource: "clusteroperators", Name: "machine-config"},
				Conditions: []metav1.Condition{
					{
						Type:               "Updating",
						Status:             "True",
						Reason:             "Progressing",
						LastTransitionTime: now,
					},
					{
						Type:               "Healthy",
						Status:             "True",
						Reason:             "AsExpected",
						LastTransitionTime: now,
					},
				},
			},
		},
		{
			name: "machine-config to multi-arch is completed",
			co: &configv1.ClusterOperator{
				ObjectMeta: metav1.ObjectMeta{Name: "machine-config"},
				Status: configv1.ClusterOperatorStatus{
					Versions: []configv1.OperandVersion{
						{Name: "operator", Version: "x"},
						{Name: "operator-image", Version: "machine-config-operator:latest"},
					},
					Conditions: []configv1.ClusterOperatorStatusCondition{
						{
							Type:   configv1.OperatorProgressing,
							Status: configv1.ConditionTrue,
						},
						{
							Type:   configv1.OperatorAvailable,
							Status: configv1.ConditionTrue,
						},
					},
				},
			},
			targetVersion: "x",
			appsClient:    fakekubeclient.NewClientset(mcoDeployment.DeepCopy()).AppsV1(),
			expected: &ClusterOperatorStatusInsight{
				Name:     "machine-config",
				Resource: ResourceRef{Group: "config.openshift.io", Resource: "clusteroperators", Name: "machine-config"},
				Conditions: []metav1.Condition{
					{
						Type:               "Updating",
						Status:             "False",
						Reason:             "Updated",
						LastTransitionTime: now,
					},
					{
						Type:               "Healthy",
						Status:             "True",
						Reason:             "AsExpected",
						LastTransitionTime: now,
					},
				},
			},
		},
		{
			name: "an error is raised upon getting image pull spec for co/machine-config",
			co: &configv1.ClusterOperator{
				ObjectMeta: metav1.ObjectMeta{Name: "machine-config"},
			},
			expectedErr: errors.New("apps client is nil"),
		},
		{
			name: "cluster operator is not available",
			co: &configv1.ClusterOperator{
				ObjectMeta: metav1.ObjectMeta{Name: "some-co"},
				Status: configv1.ClusterOperatorStatus{
					Versions: []configv1.OperandVersion{
						{Name: "operator", Version: "x"},
					},
					Conditions: []configv1.ClusterOperatorStatusCondition{
						{
							Type:   configv1.OperatorProgressing,
							Status: configv1.ConditionTrue,
						},
						{
							Type:    configv1.OperatorAvailable,
							Status:  configv1.ConditionFalse,
							Reason:  "a",
							Message: "b",
						},
					},
				},
			},
			targetVersion: "y",
			expected: &ClusterOperatorStatusInsight{
				Name:     "some-co",
				Resource: ResourceRef{Group: "config.openshift.io", Resource: "clusteroperators", Name: "some-co"},
				Conditions: []metav1.Condition{
					{
						Type:               "Updating",
						Status:             "True",
						Reason:             "Progressing",
						LastTransitionTime: now,
					},
					{
						Type:               "Healthy",
						Status:             "False",
						Reason:             "Unavailable",
						Message:            "b",
						LastTransitionTime: now,
					},
				},
			},
		},
		{
			name: "cluster operator is degraded",
			co: &configv1.ClusterOperator{
				ObjectMeta: metav1.ObjectMeta{Name: "some-co"},
				Status: configv1.ClusterOperatorStatus{
					Versions: []configv1.OperandVersion{
						{Name: "operator", Version: "x"},
					},
					Conditions: []configv1.ClusterOperatorStatusCondition{
						{
							Type:   configv1.OperatorProgressing,
							Status: configv1.ConditionTrue,
						},
						{
							Type:   configv1.OperatorAvailable,
							Status: configv1.ConditionTrue,
						},
						{
							Type:    configv1.OperatorDegraded,
							Status:  configv1.ConditionTrue,
							Reason:  "a",
							Message: "b",
						},
					},
				},
			},
			targetVersion: "y",
			expected: &ClusterOperatorStatusInsight{
				Name:     "some-co",
				Resource: ResourceRef{Group: "config.openshift.io", Resource: "clusteroperators", Name: "some-co"},
				Conditions: []metav1.Condition{
					{
						Type:               "Updating",
						Status:             "True",
						Reason:             "Progressing",
						LastTransitionTime: now,
					},
					{
						Type:               "Healthy",
						Status:             "False",
						Reason:             "Degraded",
						Message:            "b",
						LastTransitionTime: now,
					},
				},
			},
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			actual, actualErr := assessClusterOperator(context.TODO(), tc.co, tc.targetVersion, tc.appsClient, now)
			if diff := cmp.Diff(tc.expectedErr, actualErr, cmp.Comparer(func(x, y error) bool {
				if x == nil || y == nil {
					return x == nil && y == nil
				}
				return x.Error() == y.Error()
			})); diff != "" {
				t.Errorf("%s: error differs from expected:\n%s", tc.name, diff)
			}
			if actualErr == nil {
				if diff := cmp.Diff(tc.expected, actual); diff != "" {
					t.Errorf("%s: CO Status Insight differs from expected:\n%s", tc.name, diff)
				}
			}
		})
	}
}

func Test_assessClusterVersion(t *testing.T) {
	now := metav1.Now()
	var minutesAgo [120]metav1.Time
	for i := range minutesAgo {
		minutesAgo[i] = metav1.NewTime(now.Add(-time.Duration(i) * time.Minute))
	}

	cvReference := ResourceRef{
		Resource: "clusterversions",
		Group:    "config.openshift.io",
		Name:     "version",
	}
	testCases := []struct {
		name string

		cvHistory     []configv1.UpdateHistory
		cvProgressing configv1.ClusterOperatorStatusCondition

		expected *ClusterVersionStatusInsight
	}{
		{
			name: "ClusterVersion during installation",
			cvHistory: []configv1.UpdateHistory{
				{
					State:       configv1.PartialUpdate,
					StartedTime: minutesAgo[30],
					Version:     "4.18.0",
					Image:       "pullspec-4.18.0",
				},
			},
			cvProgressing: configv1.ClusterOperatorStatusCondition{
				Type:               configv1.OperatorProgressing,
				Status:             configv1.ConditionTrue,
				LastTransitionTime: minutesAgo[30],
				Reason:             "WhateverCVOHasHereWhenInstalling",
				Message:            "Whatever CVO has as a message while installing",
			},
			expected: &ClusterVersionStatusInsight{
				Resource:   cvReference,
				Assessment: ControlPlaneAssessmentProgressing,
				Versions: ControlPlaneUpdateVersions{
					Target: Version{
						Version:  "4.18.0",
						Metadata: []VersionMetadata{{Key: InstallationMetadata}},
					},
				},
				StartedAt:            minutesAgo[30],
				EstimatedCompletedAt: ptr.To[metav1.Time](metav1.NewTime(now.Add(30 * time.Minute))),
				Conditions: []metav1.Condition{
					newClusterVersionStatusInsightUpdating(
						metav1.ConditionTrue,
						ClusterVersionProgressing,
						"ClusterVersion has Progressing=True(Reason=WhateverCVOHasHereWhenInstalling) | Message='Whatever CVO has as a message while installing'",
						now,
					),
				},
			},
		},
		{
			name: "ClusterVersion after installation",
			cvHistory: []configv1.UpdateHistory{
				{
					State:          configv1.CompletedUpdate,
					StartedTime:    minutesAgo[60],
					CompletionTime: &minutesAgo[30],
					Version:        "4.18.0",
					Image:          "pullspec-4.18.0",
				},
			},
			cvProgressing: configv1.ClusterOperatorStatusCondition{
				Type:               configv1.OperatorProgressing,
				Status:             configv1.ConditionFalse,
				LastTransitionTime: minutesAgo[30],
				// CVO does not set up a Reason when Progressing=False
				Message: "Cluster version is 4.18.0",
			},
			expected: &ClusterVersionStatusInsight{
				Resource:   cvReference,
				Assessment: ControlPlaneAssessmentCompleted,
				Versions: ControlPlaneUpdateVersions{
					Target: Version{
						Version: "4.18.0",
						Metadata: []VersionMetadata{
							{
								Key: InstallationMetadata,
							},
						},
					},
				},
				Completion:           100,
				StartedAt:            minutesAgo[60],
				CompletedAt:          &minutesAgo[30],
				EstimatedCompletedAt: &now,
				Conditions: []metav1.Condition{
					newClusterVersionStatusInsightUpdating(
						metav1.ConditionFalse,
						ClusterVersionNotProgressing,
						"ClusterVersion has Progressing=False(Reason=) | Message='Cluster version is 4.18.0'",
						now,
					),
				},
			},
		},
		{
			name: "ClusterVersion during a standard update",
			cvHistory: []configv1.UpdateHistory{
				{
					State:       configv1.PartialUpdate,
					StartedTime: minutesAgo[20],
					Version:     "4.19.0",
					Image:       "pullspec-4.19.0",
				},
				{
					State:          configv1.CompletedUpdate,
					StartedTime:    minutesAgo[60],
					CompletionTime: &minutesAgo[30],
					Version:        "4.18.0",
					Image:          "pullspec-4.18.0",
				},
			},
			cvProgressing: configv1.ClusterOperatorStatusCondition{
				Type:               configv1.OperatorProgressing,
				Status:             configv1.ConditionTrue,
				LastTransitionTime: minutesAgo[20],
				Reason:             "WhateverCVOHasHereWhenUpdating",
				Message:            "Whatever CVO has as a message while updating",
			},
			expected: &ClusterVersionStatusInsight{
				Resource:   cvReference,
				Assessment: ControlPlaneAssessmentProgressing,
				Versions: ControlPlaneUpdateVersions{
					Target:   Version{Version: "4.19.0"},
					Previous: Version{Version: "4.18.0"},
				},
				StartedAt:            minutesAgo[20],
				EstimatedCompletedAt: ptr.To[metav1.Time](metav1.NewTime(now.Add(40 * time.Minute))),
				Conditions: []metav1.Condition{
					newClusterVersionStatusInsightUpdating(
						metav1.ConditionTrue,
						ClusterVersionProgressing,
						"ClusterVersion has Progressing=True(Reason=WhateverCVOHasHereWhenUpdating) | Message='Whatever CVO has as a message while updating'",
						now,
					),
				},
			},
		},
		{
			name: "ClusterVersion after a standard update",
			cvHistory: []configv1.UpdateHistory{
				{
					State:          configv1.CompletedUpdate,
					StartedTime:    minutesAgo[20],
					CompletionTime: &minutesAgo[10],
					Version:        "4.19.0",
					Image:          "pullspec-4.19.0",
				},
				{
					State:          configv1.CompletedUpdate,
					StartedTime:    minutesAgo[60],
					CompletionTime: &minutesAgo[30],
					Version:        "4.18.0",
					Image:          "pullspec-4.18.0",
				},
			},
			cvProgressing: configv1.ClusterOperatorStatusCondition{
				Type:               configv1.OperatorProgressing,
				Status:             configv1.ConditionFalse,
				LastTransitionTime: minutesAgo[10],
				// CVO does not set up a Reason when Progressing=False
				Message: "Cluster version is 4.19.0",
			},
			expected: &ClusterVersionStatusInsight{
				Resource:   cvReference,
				Assessment: ControlPlaneAssessmentCompleted,
				Completion: 100,
				Versions: ControlPlaneUpdateVersions{
					Target:   Version{Version: "4.19.0"},
					Previous: Version{Version: "4.18.0"},
				},
				StartedAt:            minutesAgo[20],
				CompletedAt:          &minutesAgo[10],
				EstimatedCompletedAt: ptr.To[metav1.Time](metav1.NewTime(now.Add(40 * time.Minute))),
				Conditions: []metav1.Condition{
					newClusterVersionStatusInsightUpdating(
						metav1.ConditionFalse,
						ClusterVersionNotProgressing,
						"ClusterVersion has Progressing=False(Reason=) | Message='Cluster version is 4.19.0'",
						now,
					),
				},
			},
		},
		{
			name: "ClusterVersion during an update from partial version",
			cvHistory: []configv1.UpdateHistory{
				{
					State:       configv1.PartialUpdate,
					StartedTime: minutesAgo[20],
					Version:     "4.19.1",
					Image:       "pullspec-4.19.1",
				},
				{
					State:          configv1.PartialUpdate,
					StartedTime:    minutesAgo[40],
					CompletionTime: &minutesAgo[20],
					Version:        "4.19.0",
					Image:          "pullspec-4.19.0",
				},
				{
					State:          configv1.CompletedUpdate,
					StartedTime:    minutesAgo[60],
					CompletionTime: &minutesAgo[50],
					Version:        "4.18.0",
					Image:          "pullspec-4.18.0",
				},
			},
			cvProgressing: configv1.ClusterOperatorStatusCondition{
				Type:               configv1.OperatorProgressing,
				Status:             configv1.ConditionTrue,
				LastTransitionTime: minutesAgo[20],
				Reason:             "WhateverCVOHasHereWhenUpdating",
				Message:            "Whatever CVO has as a message while updating",
			},
			expected: &ClusterVersionStatusInsight{
				Resource:   cvReference,
				Assessment: ControlPlaneAssessmentProgressing,
				Versions: ControlPlaneUpdateVersions{
					Target: Version{Version: "4.19.1"},
					Previous: Version{
						Version:  "4.19.0",
						Metadata: []VersionMetadata{{Key: PartialMetadata}},
					},
				},
				StartedAt:            minutesAgo[20],
				EstimatedCompletedAt: ptr.To[metav1.Time](metav1.NewTime(now.Add(40 * time.Minute))),
				Conditions: []metav1.Condition{
					newClusterVersionStatusInsightUpdating(
						metav1.ConditionTrue,
						ClusterVersionProgressing,
						"ClusterVersion has Progressing=True(Reason=WhateverCVOHasHereWhenUpdating) | Message='Whatever CVO has as a message while updating'",
						now,
					),
				},
			},
		},
		{
			name: "ClusterVersion after an update from partial version",
			cvHistory: []configv1.UpdateHistory{
				{
					State:          configv1.CompletedUpdate,
					StartedTime:    minutesAgo[20],
					CompletionTime: &minutesAgo[10],
					Version:        "4.19.1",
					Image:          "pullspec-4.19.1",
				},
				{
					State:          configv1.PartialUpdate,
					StartedTime:    minutesAgo[40],
					CompletionTime: &minutesAgo[20],
					Version:        "4.19.0",
					Image:          "pullspec-4.19.0",
				},
				{
					State:          configv1.CompletedUpdate,
					StartedTime:    minutesAgo[60],
					CompletionTime: &minutesAgo[50],
					Version:        "4.18.0",
					Image:          "pullspec-4.18.0",
				},
			},
			cvProgressing: configv1.ClusterOperatorStatusCondition{
				Type:               configv1.OperatorProgressing,
				Status:             configv1.ConditionFalse,
				LastTransitionTime: minutesAgo[10],
				// CVO does not set up a Reason when Progressing=False
				Message: "Cluster version is 4.19.1",
			},
			expected: &ClusterVersionStatusInsight{
				Resource:   cvReference,
				Assessment: ControlPlaneAssessmentCompleted,
				Completion: 100,
				Versions: ControlPlaneUpdateVersions{
					Target: Version{Version: "4.19.1"},
					Previous: Version{
						Version:  "4.19.0",
						Metadata: []VersionMetadata{{Key: PartialMetadata}},
					},
				},
				StartedAt:            minutesAgo[20],
				CompletedAt:          &minutesAgo[10],
				EstimatedCompletedAt: ptr.To[metav1.Time](metav1.NewTime(now.Add(40 * time.Minute))),
				Conditions: []metav1.Condition{
					newClusterVersionStatusInsightUpdating(
						metav1.ConditionFalse,
						ClusterVersionNotProgressing,
						"ClusterVersion has Progressing=False(Reason=) | Message='Cluster version is 4.19.1'",
						now,
					),
				},
			},
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			cv := &configv1.ClusterVersion{
				ObjectMeta: metav1.ObjectMeta{Name: "version"},
				Status: configv1.ClusterVersionStatus{
					History:    tc.cvHistory,
					Conditions: []configv1.ClusterOperatorStatusCondition{tc.cvProgressing},
				},
			}
			actual := assessClusterVersion(cv, now)
			if diff := cmp.Diff(tc.expected, actual, cmpopts.IgnoreFields(metav1.Condition{}, "LastTransitionTime")); diff != "" {
				t.Errorf("CV Status Insight differs from expected:\n%s", diff)
			}
		})
	}
}

func Test_isControlPlaneUpdating(t *testing.T) {
	now := time.Now()
	var minutesAgo [120]metav1.Time
	for i := range minutesAgo {
		minutesAgo[i] = metav1.NewTime(now.Add(-time.Duration(i) * time.Minute))
	}

	progressingTrue := configv1.ClusterOperatorStatusCondition{
		Type:               configv1.OperatorProgressing,
		Status:             configv1.ConditionTrue,
		LastTransitionTime: minutesAgo[30],
		Reason:             "WhateverCVOHasHereWhenUpdating",
		Message:            "Cluster is updating to 4.19.0",
	}
	progressingFalse := configv1.ClusterOperatorStatusCondition{
		Type:               configv1.OperatorProgressing,
		Status:             configv1.ConditionFalse,
		LastTransitionTime: minutesAgo[30],
		Message:            "Cluster version is 4.19.0",
	}
	completedUpdate := configv1.UpdateHistory{
		State:          configv1.CompletedUpdate,
		StartedTime:    minutesAgo[60],
		CompletionTime: &minutesAgo[30],
		Version:        "4.18.0",
		Image:          "pullspec-4.18.0",
	}
	partialUpdate := configv1.UpdateHistory{
		State:       configv1.PartialUpdate,
		StartedTime: minutesAgo[60],
		Version:     "4.19.0",
		Image:       "pullspec-4.19.0",
	}

	testCases := []struct {
		name string

		cvProgressing   *configv1.ClusterOperatorStatusCondition
		lastHistoryItem *configv1.UpdateHistory

		expectedCondition metav1.Condition
		expectedStarted   metav1.Time
		expectedCompleted metav1.Time
	}{
		{
			// ---GOOD CASES ---
			name:            "Progressing=True, last history item is Partial",
			cvProgressing:   &progressingTrue,
			lastHistoryItem: &partialUpdate,
			expectedCondition: metav1.Condition{
				Status:  metav1.ConditionTrue,
				Reason:  "ClusterVersionProgressing",
				Message: "ClusterVersion has Progressing=True(Reason=WhateverCVOHasHereWhenUpdating) | Message='Cluster is updating to 4.19.0'",
			},
			expectedStarted:   partialUpdate.StartedTime,
			expectedCompleted: metav1.Time{},
		},
		{
			name:            "Progressing=False, last history item is completed",
			cvProgressing:   &progressingFalse,
			lastHistoryItem: &completedUpdate,
			expectedCondition: metav1.Condition{
				Status:  metav1.ConditionFalse,
				Reason:  "ClusterVersionNotProgressing",
				Message: "ClusterVersion has Progressing=False(Reason=) | Message='Cluster version is 4.19.0'",
			},
			expectedStarted:   completedUpdate.StartedTime,
			expectedCompleted: *completedUpdate.CompletionTime,
		},

		// --- BAD CASES ---
		{
			name:            "no progressing condition",
			cvProgressing:   nil,
			lastHistoryItem: &partialUpdate,
			expectedCondition: metav1.Condition{
				Status:  metav1.ConditionUnknown,
				Reason:  "CannotDetermineUpdating",
				Message: "No Progressing condition in ClusterVersion",
			},
			expectedStarted:   metav1.Time{},
			expectedCompleted: metav1.Time{},
		},
		{
			name:            "no history",
			cvProgressing:   &progressingFalse,
			lastHistoryItem: nil,
			expectedCondition: metav1.Condition{
				Status:  metav1.ConditionUnknown,
				Reason:  "CannotDetermineUpdating",
				Message: "Empty history in ClusterVersion",
			},
		},
		{
			name:          "Progressing=True, but last history item is not Partial",
			cvProgressing: &progressingTrue,
			lastHistoryItem: &configv1.UpdateHistory{
				State:          configv1.CompletedUpdate,
				StartedTime:    minutesAgo[60],
				CompletionTime: &minutesAgo[30],
				Version:        "4.18.0",
				Image:          "pullspec-4.18.0",
			},
			expectedCondition: metav1.Condition{
				Status:  metav1.ConditionUnknown,
				Reason:  "CannotDetermineUpdating",
				Message: "Progressing=True in ClusterVersion but last history item is not Partial",
			},
		},
		{
			name:          "Progressing=True, but last history item has completion time",
			cvProgressing: &progressingTrue,
			lastHistoryItem: &configv1.UpdateHistory{
				State:          configv1.PartialUpdate,
				StartedTime:    minutesAgo[60],
				CompletionTime: &minutesAgo[50],
				Version:        "4.19.0",
				Image:          "pullspec-4.19.0",
			},
			expectedCondition: metav1.Condition{
				Status:  metav1.ConditionUnknown,
				Reason:  "CannotDetermineUpdating",
				Message: "Progressing=True in ClusterVersion but last history item has completion time",
			},
		},
		{
			name:          "Progressing=False, but last history item is not completed",
			cvProgressing: &progressingFalse,
			lastHistoryItem: &configv1.UpdateHistory{
				State:       configv1.PartialUpdate,
				StartedTime: minutesAgo[60],
				Version:     "4.19.0",
				Image:       "pullspec-4.19.0",
			},
			expectedCondition: metav1.Condition{
				Status:  metav1.ConditionUnknown,
				Reason:  "CannotDetermineUpdating",
				Message: "Progressing=False in ClusterVersion but last history item is not completed",
			},
		},
		{
			name:          "Progressing=False, but last history item has no completion time",
			cvProgressing: &progressingFalse,
			lastHistoryItem: &configv1.UpdateHistory{
				State:       configv1.CompletedUpdate,
				StartedTime: minutesAgo[60],
				Version:     "4.19.0",
				Image:       "pullspec-4.19.0",
			},
			expectedCondition: metav1.Condition{
				Status:  metav1.ConditionUnknown,
				Reason:  "CannotDetermineUpdating",
				Message: "Progressing=False in ClusterVersion but not no completion in last history item",
			},
		},
		{
			name: "Progressing=Unknown",
			cvProgressing: &configv1.ClusterOperatorStatusCondition{
				Type:    configv1.OperatorProgressing,
				Status:  configv1.ConditionUnknown,
				Reason:  "WhoKnows",
				Message: "Message about Progressing=Unknown",
			},
			lastHistoryItem: &partialUpdate,
			expectedCondition: metav1.Condition{
				Status:  metav1.ConditionUnknown,
				Reason:  "CannotDetermineUpdating",
				Message: "ClusterVersion has Progressing=Unknown(Reason=WhoKnows) | Message='Message about Progressing=Unknown'",
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			tc.expectedCondition.Type = string(ClusterVersionStatusInsightUpdating)

			actualStatus, actualStarted, actualCompleted := isControlPlaneUpdating(tc.cvProgressing, tc.lastHistoryItem)
			if diff := cmp.Diff(tc.expectedCondition, actualStatus); diff != "" {
				t.Errorf("Status differs from expected:\n%s", diff)
			}
			if diff := cmp.Diff(tc.expectedStarted, actualStarted); diff != "" {
				t.Errorf("Started differs from expected:\n%s", diff)
			}
			if diff := cmp.Diff(tc.expectedCompleted, actualCompleted); diff != "" {
				t.Errorf("Completed differs from expected:\n%s", diff)
			}
		})
	}
}

func Test_cvProgressingToUpdating(t *testing.T) {
	testCases := []struct {
		name        string
		progressing configv1.ClusterOperatorStatusCondition

		expectedStatus  metav1.ConditionStatus
		expectedReason  string
		expectedMessage string
	}{
		{
			name: "Progressing=True",
			progressing: configv1.ClusterOperatorStatusCondition{
				Status:  configv1.ConditionTrue,
				Reason:  "ReasonTrue",
				Message: "MessageTrue",
			},
			expectedStatus:  metav1.ConditionTrue,
			expectedReason:  "ClusterVersionProgressing",
			expectedMessage: "ClusterVersion has Progressing=True(Reason=ReasonTrue) | Message='MessageTrue'",
		},
		{
			name: "Progressing=False",
			progressing: configv1.ClusterOperatorStatusCondition{
				Status:  configv1.ConditionFalse,
				Reason:  "ReasonFalse",
				Message: "MessageFalse",
			},
			expectedStatus:  metav1.ConditionFalse,
			expectedReason:  "ClusterVersionNotProgressing",
			expectedMessage: "ClusterVersion has Progressing=False(Reason=ReasonFalse) | Message='MessageFalse'",
		},
		{
			name: "Progressing=Unknown",
			progressing: configv1.ClusterOperatorStatusCondition{
				Status:  configv1.ConditionUnknown,
				Reason:  "ReasonUnknown",
				Message: "MessageUnknown",
			},
			expectedStatus:  metav1.ConditionUnknown,
			expectedReason:  "CannotDetermineUpdating",
			expectedMessage: "ClusterVersion has Progressing=Unknown(Reason=ReasonUnknown) | Message='MessageUnknown'",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			actualStatus, actualReason, actualMessage := cvProgressingToUpdating(tc.progressing)
			if diff := cmp.Diff(tc.expectedStatus, actualStatus); diff != "" {
				t.Errorf("Status differs from expected:\n%s", diff)
			}
			if diff := cmp.Diff(tc.expectedReason, actualReason); diff != "" {
				t.Errorf("Reason differs from expected:\n%s", diff)
			}
			if diff := cmp.Diff(tc.expectedMessage, actualMessage); diff != "" {
				t.Errorf("Message differs from expected:\n%s", diff)
			}
		})
	}
}

func Test_versionsFromHistory(t *testing.T) {
	testCases := []struct {
		name     string
		history  []configv1.UpdateHistory
		expected ControlPlaneUpdateVersions
	}{
		{
			name:     "no history",
			history:  nil,
			expected: ControlPlaneUpdateVersions{},
		},
		{
			name:     "empty history",
			history:  []configv1.UpdateHistory{},
			expected: ControlPlaneUpdateVersions{},
		},
		{
			name: "single history item => installation",
			history: []configv1.UpdateHistory{
				{
					State:       configv1.CompletedUpdate,
					StartedTime: metav1.NewTime(time.Now()),
					Version:     "4.18.0",
					Image:       "pullspec-4.18.0",
					CompletionTime: &metav1.Time{
						Time: time.Now().Add(30 * time.Minute),
					},
				},
			},
			expected: ControlPlaneUpdateVersions{
				Target: Version{
					Version:  "4.18.0",
					Metadata: []VersionMetadata{{Key: InstallationMetadata}},
				},
			},
		},
		{
			name: "two history items => standard update",
			history: []configv1.UpdateHistory{
				{
					State:       configv1.PartialUpdate,
					StartedTime: metav1.NewTime(time.Now()),
					Version:     "4.19.0",
					Image:       "pullspec-4.19.0",
				},
				{
					State:          configv1.CompletedUpdate,
					StartedTime:    metav1.NewTime(time.Now().Add(-30 * time.Minute)),
					CompletionTime: &metav1.Time{Time: time.Now().Add(-20 * time.Minute)},
					Version:        "4.18.0",
					Image:          "pullspec-4.18.0",
				},
			},
			expected: ControlPlaneUpdateVersions{
				Target:   Version{Version: "4.19.0"},
				Previous: Version{Version: "4.18.0"},
			},
		},
		{
			name: "update from partial version",
			history: []configv1.UpdateHistory{
				{
					State:       configv1.PartialUpdate,
					StartedTime: metav1.NewTime(time.Now()),
					Version:     "4.19.1",
					Image:       "pullspec-4.19.1",
				},
				{
					State:          configv1.PartialUpdate,
					StartedTime:    metav1.NewTime(time.Now().Add(-20 * time.Minute)),
					CompletionTime: &metav1.Time{Time: time.Now().Add(-10 * time.Minute)},
					Version:        "4.19.0",
					Image:          "pullspec-4.19.0",
				},
			},
			expected: ControlPlaneUpdateVersions{
				Target: Version{Version: "4.19.1"},
				Previous: Version{
					Version:  "4.19.0",
					Metadata: []VersionMetadata{{Key: PartialMetadata}},
				},
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			actual := versionsFromHistory(tc.history)
			if diff := cmp.Diff(tc.expected, actual); diff != "" {
				t.Errorf("Versions differ from expected:\n%s", diff)
			}
		})
	}
}

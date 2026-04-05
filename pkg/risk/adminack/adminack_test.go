package adminack

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/blang/semver/v4"
	"github.com/google/go-cmp/cmp"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/informers"
	kfake "k8s.io/client-go/kubernetes/fake"
	ktesting "k8s.io/client-go/testing"
	"k8s.io/client-go/tools/cache"

	configv1 "github.com/openshift/api/config/v1"

	"github.com/openshift/cluster-version-operator/pkg/internal"
)

func TestOperator_upgradeableSync(t *testing.T) {
	var defaultGateCm = corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: internal.AdminGatesConfigMap, Namespace: internal.ConfigManagedNamespace},
	}
	var defaultAckCm = corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: internal.AdminAcksConfigMap, Namespace: internal.ConfigNamespace},
	}
	tests := []struct {
		name          string
		gateCm        *corev1.ConfigMap
		ackCm         *corev1.ConfigMap
		expectedRisks []configv1.ConditionalUpdateRisk
		expectedError string
	}{
		{
			name: "admin ack gate does not apply to version",
			gateCm: &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{Namespace: internal.ConfigManagedNamespace, Name: internal.AdminGatesConfigMap},
				Data:       map[string]string{"ack-4.8-test": "Testing"},
			},
			ackCm: &defaultAckCm,
		},
		{
			name: "admin-gates configmap gate does not have value",
			gateCm: &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{Namespace: internal.ConfigManagedNamespace, Name: internal.AdminGatesConfigMap},
				Data:       map[string]string{"ack-4.21-test": ""},
			},
			ackCm: &defaultAckCm,
			expectedRisks: []configv1.ConditionalUpdateRisk{{
				Name:          "AdminAck-ack-4.21-test",
				Message:       "admin-gates configmap gate ack-4.21-test must contain a non-empty value.",
				URL:           "https://docs.redhat.com/en/documentation/openshift_container_platform/",
				MatchingRules: []configv1.ClusterCondition{{Type: "Always"}},
			}},
		},
		{
			name: "admin ack required",
			gateCm: &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{Namespace: internal.ConfigManagedNamespace, Name: internal.AdminGatesConfigMap},
				Data:       map[string]string{"ack-4.21-test": "Description."},
			},
			ackCm: &defaultAckCm,
			expectedRisks: []configv1.ConditionalUpdateRisk{{
				Name:          "AdminAck-ack-4.21-test",
				Message:       "Description.",
				URL:           "https://example.com/FIXME-look-for-a-URI-in-the-message",
				MatchingRules: []configv1.ClusterCondition{{Type: "Always"}},
			}},
		},
		{
			name: "admin ack required and admin ack gate does not apply to version",
			gateCm: &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{Namespace: internal.ConfigManagedNamespace, Name: internal.AdminGatesConfigMap},
				Data: map[string]string{
					"ack-4.21-test": "Description A.",
					"ack-4.22-test": "Description B.",
				},
			},
			ackCm: &defaultAckCm,
			expectedRisks: []configv1.ConditionalUpdateRisk{{
				Name:          "AdminAck-ack-4.21-test",
				Message:       "Description A.",
				URL:           "https://example.com/FIXME-look-for-a-URI-in-the-message",
				MatchingRules: []configv1.ClusterCondition{{Type: "Always"}},
			}},
		},
		{
			name: "admin ack required and configmap gate does not have value",
			gateCm: &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{Namespace: internal.ConfigManagedNamespace, Name: internal.AdminGatesConfigMap},
				Data: map[string]string{
					"ack-4.21-with-description":    "Description.",
					"ack-4.21-without-description": "",
				},
			},
			ackCm: &defaultAckCm,
			expectedRisks: []configv1.ConditionalUpdateRisk{
				{
					Name:          "AdminAck-ack-4.21-with-description",
					Message:       "Description.",
					URL:           "https://example.com/FIXME-look-for-a-URI-in-the-message",
					MatchingRules: []configv1.ClusterCondition{{Type: "Always"}},
				},
				{
					Name:          "AdminAck-ack-4.21-without-description",
					Message:       "admin-gates configmap gate ack-4.21-without-description must contain a non-empty value.",
					URL:           "https://docs.redhat.com/en/documentation/openshift_container_platform/",
					MatchingRules: []configv1.ClusterCondition{{Type: "Always"}},
				},
			},
		},
		{
			name: "multiple admin acks required",
			gateCm: &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{Namespace: internal.ConfigManagedNamespace, Name: internal.AdminGatesConfigMap},
				Data: map[string]string{
					"ack-4.21-b": "Description 2.",
					"ack-4.21-a": "Description 1.",
					"ack-4.21-c": "Description 3.",
				},
			},
			ackCm: &defaultAckCm,
			expectedRisks: []configv1.ConditionalUpdateRisk{
				{
					Name:          "AdminAck-ack-4.21-a",
					Message:       "Description 1.",
					URL:           "https://example.com/FIXME-look-for-a-URI-in-the-message",
					MatchingRules: []configv1.ClusterCondition{{Type: "Always"}},
				},
				{
					Name:          "AdminAck-ack-4.21-b",
					Message:       "Description 2.",
					URL:           "https://example.com/FIXME-look-for-a-URI-in-the-message",
					MatchingRules: []configv1.ClusterCondition{{Type: "Always"}},
				},

				{
					Name:          "AdminAck-ack-4.21-c",
					Message:       "Description 3.",
					URL:           "https://example.com/FIXME-look-for-a-URI-in-the-message",
					MatchingRules: []configv1.ClusterCondition{{Type: "Always"}},
				},
			},
		},
		{
			name: "admin ack found",
			gateCm: &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{Namespace: internal.ConfigManagedNamespace, Name: internal.AdminGatesConfigMap},
				Data:       map[string]string{"ack-4.21-test": "Description."},
			},
			ackCm: &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{Namespace: internal.ConfigNamespace, Name: internal.AdminAcksConfigMap},
				Data:       map[string]string{"ack-4.21-test": "true"},
			},
		},
		{
			name: "admin ack 2 of 3 found",
			gateCm: &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{Namespace: internal.ConfigManagedNamespace, Name: internal.AdminGatesConfigMap},
				Data: map[string]string{
					"ack-4.21-a": "Description 1.",
					"ack-4.21-b": "Description 2.",
					"ack-4.21-c": "Description 3.",
				},
			},
			ackCm: &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{Namespace: internal.ConfigNamespace, Name: internal.AdminAcksConfigMap},
				Data:       map[string]string{"ack-4.21-a": "true", "ack-4.21-c": "true"},
			},
			expectedRisks: []configv1.ConditionalUpdateRisk{{
				Name:          "AdminAck-ack-4.21-b",
				Message:       "Description 2.",
				URL:           "https://example.com/FIXME-look-for-a-URI-in-the-message",
				MatchingRules: []configv1.ClusterCondition{{Type: "Always"}},
			}},
		},
		{
			name: "multiple admin acks found",
			gateCm: &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{Namespace: internal.ConfigManagedNamespace, Name: internal.AdminGatesConfigMap},
				Data: map[string]string{
					"ack-4.21-a": "Description 1.",
					"ack-4.21-b": "Description 2.",
					"ack-4.21-c": "Description 3.",
				},
			},
			ackCm: &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{Namespace: internal.ConfigNamespace, Name: internal.AdminAcksConfigMap},
				Data:       map[string]string{"ack-4.21-a": "true", "ack-4.21-b": "true", "ack-4.21-c": "true"},
			},
		},
		// delete tests are last so we don't have to re-create the config map for other tests
		{
			name: "admin-acks configmap not found",
			gateCm: &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{Namespace: internal.ConfigManagedNamespace, Name: internal.AdminGatesConfigMap},
				Data:       map[string]string{"ack-4.21-a": "Description 1."},
			},
			// Name triggers deletion of config map
			ackCm: &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{Namespace: internal.ConfigNamespace, Name: "delete"},
			},
			expectedRisks: []configv1.ConditionalUpdateRisk{{
				Name:          "AdminAckUnknown",
				Message:       `Failed to retrieve ConfigMap admin-acks from namespace openshift-config: configmap "admin-acks" not found`,
				URL:           "https://docs.redhat.com/en/documentation/openshift_container_platform/",
				MatchingRules: []configv1.ClusterCondition{{Type: "Always"}},
			}},
			expectedError: `configmap "admin-acks" not found`,
		},
		// delete tests are last so we don't have to re-create the config map for other tests
		{
			name: "admin-gates configmap not found",
			// Name triggers deletion of config map
			gateCm: &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{Namespace: internal.ConfigManagedNamespace, Name: "delete"},
			},
			ackCm: nil,
			expectedRisks: []configv1.ConditionalUpdateRisk{{
				Name:          "AdminAckUnknown",
				Message:       `Failed to retrieve ConfigMap admin-gates from namespace openshift-config-managed: configmap "admin-gates" not found`,
				URL:           "https://docs.redhat.com/en/documentation/openshift_container_platform/",
				MatchingRules: []configv1.ClusterCondition{{Type: "Always"}},
			}},
			expectedError: `configmap "admin-gates" not found`,
		},
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	watcherStarted := make(chan struct{})
	f := kfake.NewClientset()

	// A catch-all watch reactor that allows us to inject the watcherStarted channel.
	f.PrependWatchReactor("*", func(action ktesting.Action) (handled bool, ret watch.Interface, err error) {
		gvr := action.GetResource()
		ns := action.GetNamespace()
		watch, err := f.Tracker().Watch(gvr, ns)
		if err != nil {
			return false, nil, err
		}
		close(watcherStarted)
		return true, watch, nil
	})
	cms := make(chan *corev1.ConfigMap, 1)
	configManagedInformer := informers.NewSharedInformerFactory(f, 0)
	cmInformer := configManagedInformer.Core().V1().ConfigMaps()
	if _, err := cmInformer.Informer().AddEventHandler(&cache.ResourceEventHandlerFuncs{
		AddFunc: func(obj interface{}) {
			cm := obj.(*corev1.ConfigMap)
			t.Logf("cm added: %s/%s", cm.Namespace, cm.Name)
			cms <- cm
		},
		DeleteFunc: func(obj interface{}) {
			cm := obj.(*corev1.ConfigMap)
			t.Logf("cm deleted: %s/%s", cm.Namespace, cm.Name)
			cms <- cm
		},
		UpdateFunc: func(oldObj, newObj interface{}) {
			cm := newObj.(*corev1.ConfigMap)
			t.Logf("cm updated: %s/%s", cm.Namespace, cm.Name)
			cms <- cm
		},
	}); err != nil {
		t.Errorf("error adding ConfigMap event handler: %v", err)
	}
	configManagedInformer.Start(ctx.Done())

	_, err := f.CoreV1().ConfigMaps(internal.ConfigManagedNamespace).Create(ctx, &defaultGateCm, metav1.CreateOptions{})
	if err != nil {
		t.Errorf("error injecting admin-gates configmap: %v", err)
	}
	waitForCm(t, cms)
	_, err = f.CoreV1().ConfigMaps(internal.ConfigNamespace).Create(ctx, &defaultAckCm, metav1.CreateOptions{})
	if err != nil {
		t.Errorf("error injecting admin-acks configmap: %v", err)
	}
	waitForCm(t, cms)

	for _, tt := range tests {
		{
			t.Run(tt.name, func(t *testing.T) {
				source := New("AdminAck", func() configv1.Release { return configv1.Release{Version: "4.21.0"} }, cmInformer, cmInformer, nil)
				if tt.gateCm != nil {
					if tt.gateCm.Name == "delete" {
						err := f.CoreV1().ConfigMaps(internal.ConfigManagedNamespace).Delete(ctx, internal.AdminGatesConfigMap, metav1.DeleteOptions{})
						if err != nil {
							t.Errorf("error deleting configmap %s: %v", internal.AdminGatesConfigMap, err)
						}
					} else {
						_, err = f.CoreV1().ConfigMaps(internal.ConfigManagedNamespace).Update(ctx, tt.gateCm, metav1.UpdateOptions{})
						if err != nil {
							t.Errorf("error updating configmap %s: %v", internal.AdminGatesConfigMap, err)
						}
					}
					waitForCm(t, cms)
				}
				if tt.ackCm != nil {
					if tt.ackCm.Name == "delete" {
						err := f.CoreV1().ConfigMaps(internal.ConfigNamespace).Delete(ctx, internal.AdminAcksConfigMap, metav1.DeleteOptions{})
						if err != nil {
							t.Errorf("error deleting configmap %s: %v", internal.AdminAcksConfigMap, err)
						}
					} else {
						_, err = f.CoreV1().ConfigMaps(internal.ConfigNamespace).Update(ctx, tt.ackCm, metav1.UpdateOptions{})
						if err != nil {
							t.Errorf("error updating configmap admin-acks: %v", err)
						}
					}
					waitForCm(t, cms)
				}

				var expectedVersions map[string][]string
				risks, versionMap, err := source.Risks(ctx, nil)
				if diff := cmp.Diff(tt.expectedRisks, risks); diff != "" {
					t.Errorf("risk mismatch (-want +got):\n%s", diff)
				}
				if diff := cmp.Diff(expectedVersions, versionMap); diff != "" {
					t.Errorf("versions mismatch (-want +got):\n%s", diff)
				}
				var actualError string
				if err != nil {
					actualError = strings.ReplaceAll(err.Error(), "\u00a0", " ")
				}
				if diff := cmp.Diff(tt.expectedError, actualError); diff != "" {
					t.Errorf("error mismatch (-want +got):\n%s", diff)
				}
			})
		}
	}
}

func Test_gateApplicableToCurrentVersion(t *testing.T) {
	tests := []struct {
		name           string
		gateName       string
		cv             string
		wantErr        bool
		expectedResult bool
	}{
		{
			name:           "gate name invalid format no dot",
			gateName:       "ack-4>8-foo",
			cv:             "4.8.1",
			wantErr:        true,
			expectedResult: false,
		},
		{
			name:           "gate name invalid format 2 dots",
			gateName:       "ack-4..8-foo",
			cv:             "4.8.1",
			wantErr:        true,
			expectedResult: false,
		},
		{
			name:           "gate name invalid format does not start with ack",
			gateName:       "ck-4.8-foo",
			cv:             "4.8.1",
			wantErr:        true,
			expectedResult: false,
		},
		{
			name:           "gate name invalid format major version must be 4 or 5",
			gateName:       "ack-3.8-foo",
			cv:             "4.8.1",
			wantErr:        true,
			expectedResult: false,
		},
		{
			name:           "gate name invalid format minor version must be a number",
			gateName:       "ack-4.x-foo",
			cv:             "4.8.1",
			wantErr:        true,
			expectedResult: false,
		},
		{
			name:           "gate name invalid format no following dash",
			gateName:       "ack-4.8.1-foo",
			cv:             "4.8.1",
			wantErr:        true,
			expectedResult: false,
		},
		{
			name:           "gate name invalid format 2 following dashes",
			gateName:       "ack-4.x--foo",
			cv:             "4.8.1",
			wantErr:        true,
			expectedResult: false,
		},
		{
			name:           "gate name invalid format no description following dash",
			gateName:       "ack-4.x-",
			cv:             "4.8.1",
			wantErr:        true,
			expectedResult: false,
		},
		{
			name:           "gate name match",
			gateName:       "ack-4.8-foo",
			cv:             "4.8.1",
			wantErr:        false,
			expectedResult: true,
		},
		{
			name:           "gate name match big minor version",
			gateName:       "ack-4.123456-foo",
			cv:             "4.123456.0",
			wantErr:        false,
			expectedResult: true,
		},
		{
			name:           "gate name no match",
			gateName:       "ack-4.8-foo",
			cv:             "4.9.1",
			wantErr:        false,
			expectedResult: false,
		},
		{
			name:           "gate name no match multi digit minor",
			gateName:       "ack-4.8-foo",
			cv:             "4.80.1",
			wantErr:        false,
			expectedResult: false,
		},
	}
	for _, tt := range tests {
		{
			t.Run(tt.name, func(t *testing.T) {
				version, err := semver.Parse(tt.cv)
				if err != nil {
					t.Fatalf("unable to parse version %q: %v", tt.cv, err)
				}
				isApplicable, err := gateApplicableToCurrentVersion(tt.gateName, version)
				if isApplicable != tt.expectedResult {
					t.Errorf("gateApplicableToCurrentVersion() %s expected applicable %t, got applicable %t", tt.gateName, tt.expectedResult, isApplicable)
				}
				if err != nil && !tt.wantErr {
					t.Errorf("gateApplicableToCurrentVersion() unexpected error: %v", err)
				} else if err == nil && tt.wantErr {
					t.Error("gateApplicableToCurrentVersion() unexpected success when an error was expected")
				}
			})
		}
	}
}

// waits for admin ack configmap changes
func waitForCm(t *testing.T, cmChan chan *corev1.ConfigMap) {
	select {
	case cm := <-cmChan:
		t.Logf("Got configmap from channel: %s/%s", cm.Namespace, cm.Name)
	case <-time.After(wait.ForeverTestTimeout):
		t.Error("Informer did not get the configmap")
	}
}

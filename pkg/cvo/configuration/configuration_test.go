package configuration

import (
	"context"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	operatorv1 "github.com/openshift/api/operator/v1"
	operatorv1alpha1 "github.com/openshift/api/operator/v1alpha1"
	operatorclientsetfake "github.com/openshift/client-go/operator/clientset/versioned/fake"
	operatorexternalversions "github.com/openshift/client-go/operator/informers/externalversions"
)

func TestClusterVersionOperatorConfiguration_APIServerSync(t *testing.T) {
	tests := []struct {
		name                   string
		config                 *operatorv1alpha1.ClusterVersionOperator
		expectedConfig         *operatorv1alpha1.ClusterVersionOperator
		internalConfig         configuration
		expectedInternalConfig configuration
		handlerFunctionCalled  bool
	}{
		{
			name:           "the configuration resource does not exist in the cluster -> default configuration",
			config:         nil,
			expectedConfig: nil,
			internalConfig: configuration{
				lastObservedGeneration: 3,
				desiredLogLevel:        "Trace",
			},
			expectedInternalConfig: configuration{
				lastObservedGeneration: 3,       // TODO: Default to 0
				desiredLogLevel:        "Trace", // TODO: Default to Normal
			},
			// TODO: Apply the log level when the defaulting is implemented
			handlerFunctionCalled: false,
		},
		{
			name: "first sync run correctly updates the status",
			config: &operatorv1alpha1.ClusterVersionOperator{
				ObjectMeta: metav1.ObjectMeta{
					Generation: 1,
				},
				Spec: operatorv1alpha1.ClusterVersionOperatorSpec{
					OperatorLogLevel: operatorv1.Normal,
				},
			},
			expectedConfig: &operatorv1alpha1.ClusterVersionOperator{
				ObjectMeta: metav1.ObjectMeta{
					Generation: 1,
				},
				Spec: operatorv1alpha1.ClusterVersionOperatorSpec{
					OperatorLogLevel: operatorv1.Normal,
				},
				Status: operatorv1alpha1.ClusterVersionOperatorStatus{
					ObservedGeneration: 1,
				},
			},
			internalConfig: configuration{
				desiredLogLevel:        operatorv1.Normal,
				lastObservedGeneration: 0,
			},
			expectedInternalConfig: configuration{
				desiredLogLevel:        operatorv1.Normal,
				lastObservedGeneration: 1,
			},
			handlerFunctionCalled: true,
		},
		{
			name: "sync updates observed generation correctly",
			config: &operatorv1alpha1.ClusterVersionOperator{
				ObjectMeta: metav1.ObjectMeta{
					Generation: 3,
				},
				Spec: operatorv1alpha1.ClusterVersionOperatorSpec{
					OperatorLogLevel: operatorv1.Normal,
				},
				Status: operatorv1alpha1.ClusterVersionOperatorStatus{
					ObservedGeneration: 2,
				},
			},
			expectedConfig: &operatorv1alpha1.ClusterVersionOperator{
				ObjectMeta: metav1.ObjectMeta{
					Generation: 3,
				},
				Spec: operatorv1alpha1.ClusterVersionOperatorSpec{
					OperatorLogLevel: operatorv1.Normal,
				},
				Status: operatorv1alpha1.ClusterVersionOperatorStatus{
					ObservedGeneration: 3,
				},
			},
			internalConfig: configuration{
				desiredLogLevel:        operatorv1.Normal,
				lastObservedGeneration: 2,
			},
			expectedInternalConfig: configuration{
				desiredLogLevel:        operatorv1.Normal,
				lastObservedGeneration: 3,
			},
			handlerFunctionCalled: true,
		},
		{
			name: "sync updates desired log level correctly",
			config: &operatorv1alpha1.ClusterVersionOperator{
				ObjectMeta: metav1.ObjectMeta{
					Generation: 4,
				},
				Spec: operatorv1alpha1.ClusterVersionOperatorSpec{
					OperatorLogLevel: operatorv1.Trace,
				},
				Status: operatorv1alpha1.ClusterVersionOperatorStatus{
					ObservedGeneration: 3,
				},
			},
			expectedConfig: &operatorv1alpha1.ClusterVersionOperator{
				ObjectMeta: metav1.ObjectMeta{
					Generation: 4,
				},
				Spec: operatorv1alpha1.ClusterVersionOperatorSpec{
					OperatorLogLevel: operatorv1.Trace,
				},
				Status: operatorv1alpha1.ClusterVersionOperatorStatus{
					ObservedGeneration: 4,
				},
			},
			internalConfig: configuration{
				desiredLogLevel:        operatorv1.Normal,
				lastObservedGeneration: 3,
			},
			expectedInternalConfig: configuration{
				desiredLogLevel:        operatorv1.Trace,
				lastObservedGeneration: 4,
			},
			handlerFunctionCalled: true,
		},
		{
			name: "number of not observed generations does not impact sync",
			config: &operatorv1alpha1.ClusterVersionOperator{
				ObjectMeta: metav1.ObjectMeta{
					Generation: 40,
				},
				Spec: operatorv1alpha1.ClusterVersionOperatorSpec{
					OperatorLogLevel: operatorv1.TraceAll,
				},
				Status: operatorv1alpha1.ClusterVersionOperatorStatus{
					ObservedGeneration: 3,
				},
			},
			expectedConfig: &operatorv1alpha1.ClusterVersionOperator{
				ObjectMeta: metav1.ObjectMeta{
					Generation: 40,
				},
				Spec: operatorv1alpha1.ClusterVersionOperatorSpec{
					OperatorLogLevel: operatorv1.TraceAll,
				},
				Status: operatorv1alpha1.ClusterVersionOperatorStatus{
					ObservedGeneration: 40,
				},
			},
			internalConfig: configuration{
				desiredLogLevel:        operatorv1.Normal,
				lastObservedGeneration: 3,
			},
			expectedInternalConfig: configuration{
				desiredLogLevel:        operatorv1.TraceAll,
				lastObservedGeneration: 40,
			},
			handlerFunctionCalled: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Initialize testing logic
			client := operatorclientsetfake.NewClientset()
			if tt.config != nil {
				tt.config.Name = ClusterVersionOperatorConfigurationName
				tt.expectedConfig.Name = ClusterVersionOperatorConfigurationName
				client = operatorclientsetfake.NewClientset(tt.config)
			}
			factory := operatorexternalversions.NewSharedInformerFactoryWithOptions(client, time.Minute)
			configController := NewClusterVersionOperatorConfiguration(client, factory)

			called := false
			configController.handler = func(_ configuration) error {
				called = true
				return nil
			}

			ctx, cancelFunc := context.WithDeadline(context.Background(), time.Now().Add(time.Minute))

			if err := configController.Start(ctx); err != nil {
				t.Errorf("unexpected error %v", err)
			}
			configController.configuration = tt.internalConfig

			// Run tested functionality
			if err := configController.Sync(ctx, "key"); err != nil {
				t.Errorf("unexpected error %v", err)
			}

			// Verify results
			if configController.configuration.lastObservedGeneration != tt.expectedInternalConfig.lastObservedGeneration {
				t.Errorf("unexpected 'lastObservedGeneration' value; wanted=%v, got=%v", tt.expectedInternalConfig.lastObservedGeneration, configController.configuration.lastObservedGeneration)
			}
			if configController.configuration.desiredLogLevel != tt.expectedInternalConfig.desiredLogLevel {
				t.Errorf("unexpected 'desiredLogLevel' value; wanted=%v, got=%v", tt.expectedInternalConfig.desiredLogLevel, configController.configuration.desiredLogLevel)
			}

			config, err := client.OperatorV1alpha1().ClusterVersionOperators().Get(ctx, ClusterVersionOperatorConfigurationName, metav1.GetOptions{})
			if err != nil && !apierrors.IsNotFound(err) {
				t.Errorf("unexpected error %v", err)
			}

			// Set nil to differentiate between nonexisting configurations
			if apierrors.IsNotFound(err) {
				config = nil
			}
			if diff := cmp.Diff(tt.expectedConfig, config, cmpopts.IgnoreFields(metav1.ObjectMeta{}, "ManagedFields")); diff != "" {
				t.Errorf("unexpected config (-want, +got) = %v", diff)
			}

			if tt.handlerFunctionCalled != called {
				t.Errorf("unexpected handler function execution; wanted=%v, got=%v", tt.handlerFunctionCalled, called)
			}

			// Shutdown created resources
			cancelFunc()
		})
	}
}

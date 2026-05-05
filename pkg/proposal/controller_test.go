package proposal

import (
	"context"
	"fmt"
	"testing"

	"github.com/google/go-cmp/cmp"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"k8s.io/client-go/kubernetes/scheme"

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
		updatesGetterFunc UpdatesGetterFunc
		client            ctrlruntimeclient.Client
		cvGetterFunc      cvGetterFunc
		expected          error
		verifyFunc        func(client ctrlruntimeclient.Client) error
	}{
		{
			name: "basic case",
			updatesGetterFunc: func() ([]configv1.Release, []configv1.ConditionalUpdate, error) {
				return nil, nil, nil
			},
			cvGetterFunc: func(_ string) (*configv1.ClusterVersion, error) {
				return &configv1.ClusterVersion{}, nil
			},
			client: fake.NewClientBuilder().Build(),
			verifyFunc: func(client ctrlruntimeclient.Client) error {
				proposals := &proposalv1alpha1.ProposalList{}
				if err := client.List(context.Background(), proposals); err != nil {
					return err
				}
				if len(proposals.Items) == 0 {
					return fmt.Errorf("expected proposals, none")
				}
				return nil
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := NewController(tt.updatesGetterFunc, tt.client, tt.cvGetterFunc)
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

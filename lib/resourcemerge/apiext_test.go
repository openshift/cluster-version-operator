package resourcemerge

import (
	"testing"

	"github.com/google/go-cmp/cmp"

	apiextv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
)

func TestEnsureCustomResourceDefinitionV1(t *testing.T) {
	tests := []struct {
		name     string
		existing apiextv1.CustomResourceDefinition
		required apiextv1.CustomResourceDefinition

		expectedModified bool
		expected         apiextv1.CustomResourceDefinition
	}{
		{
			name: "respect injected caBundle when the annotation `...inject-cabundle=true` is set",
			existing: apiextv1.CustomResourceDefinition{
				ObjectMeta: v1.ObjectMeta{
					Annotations: map[string]string{
						injectCABundleAnnotation: "true",
					},
				},
				Spec: apiextv1.CustomResourceDefinitionSpec{
					Conversion: &apiextv1.CustomResourceConversion{
						Strategy: apiextv1.WebhookConverter,
						Webhook: &apiextv1.WebhookConversion{
							ClientConfig: &apiextv1.WebhookClientConfig{
								CABundle: []byte("CA bundle added by the ca operator"),
							},
						},
					},
				},
			},
			required: apiextv1.CustomResourceDefinition{
				ObjectMeta: v1.ObjectMeta{
					Annotations: map[string]string{
						injectCABundleAnnotation: "true",
					},
				},
				Spec: apiextv1.CustomResourceDefinitionSpec{
					Conversion: &apiextv1.CustomResourceConversion{
						Strategy: apiextv1.WebhookConverter,
						Webhook: &apiextv1.WebhookConversion{
							ClientConfig: &apiextv1.WebhookClientConfig{
								CABundle: nil,
							},
						},
					},
				},
			},

			expectedModified: false,
			expected: apiextv1.CustomResourceDefinition{
				ObjectMeta: v1.ObjectMeta{
					Annotations: map[string]string{
						injectCABundleAnnotation: "true",
					},
				},
				Spec: apiextv1.CustomResourceDefinitionSpec{
					Conversion: &apiextv1.CustomResourceConversion{
						Strategy: apiextv1.WebhookConverter,
						Webhook: &apiextv1.WebhookConversion{
							ClientConfig: &apiextv1.WebhookClientConfig{
								CABundle: []byte("CA bundle added by the ca operator"),
							},
						},
					},
				}},
		},
		{
			name: "respect injected caBundle when the annotation `...inject-cabundle=true` is set by the user",
			existing: apiextv1.CustomResourceDefinition{
				ObjectMeta: v1.ObjectMeta{
					Annotations: map[string]string{
						injectCABundleAnnotation: "true",
					},
				},
				Spec: apiextv1.CustomResourceDefinitionSpec{
					Conversion: &apiextv1.CustomResourceConversion{
						Strategy: apiextv1.WebhookConverter,
						Webhook: &apiextv1.WebhookConversion{
							ClientConfig: &apiextv1.WebhookClientConfig{
								CABundle: []byte("CA bundle added by the ca operator"),
							},
						},
					},
				},
			},
			required: apiextv1.CustomResourceDefinition{
				Spec: apiextv1.CustomResourceDefinitionSpec{
					Conversion: &apiextv1.CustomResourceConversion{
						Strategy: apiextv1.WebhookConverter,
						Webhook: &apiextv1.WebhookConversion{
							ClientConfig: &apiextv1.WebhookClientConfig{
								CABundle: nil,
							},
						},
					},
				},
			},

			expectedModified: false,
			expected: apiextv1.CustomResourceDefinition{
				ObjectMeta: v1.ObjectMeta{
					Annotations: map[string]string{
						injectCABundleAnnotation: "true",
					},
				},
				Spec: apiextv1.CustomResourceDefinitionSpec{
					Conversion: &apiextv1.CustomResourceConversion{
						Strategy: apiextv1.WebhookConverter,
						Webhook: &apiextv1.WebhookConversion{
							ClientConfig: &apiextv1.WebhookClientConfig{
								CABundle: []byte("CA bundle added by the ca operator"),
							},
						},
					},
				}},
		},
		{
			name: "remove injected caBundle when the annotation `...inject-cabundle=true` is not set",
			existing: apiextv1.CustomResourceDefinition{
				Spec: apiextv1.CustomResourceDefinitionSpec{
					Conversion: &apiextv1.CustomResourceConversion{
						Strategy: apiextv1.WebhookConverter,
						Webhook: &apiextv1.WebhookConversion{
							ClientConfig: &apiextv1.WebhookClientConfig{
								CABundle: []byte("CA bundle added by the user"),
							},
						},
					},
				},
			},
			required: apiextv1.CustomResourceDefinition{
				Spec: apiextv1.CustomResourceDefinitionSpec{
					Conversion: &apiextv1.CustomResourceConversion{
						Strategy: apiextv1.WebhookConverter,
						Webhook: &apiextv1.WebhookConversion{
							ClientConfig: &apiextv1.WebhookClientConfig{
								CABundle: nil,
							},
						},
					},
				},
			},

			expectedModified: true,
			expected: apiextv1.CustomResourceDefinition{
				Spec: apiextv1.CustomResourceDefinitionSpec{
					Conversion: &apiextv1.CustomResourceConversion{
						Strategy: apiextv1.WebhookConverter,
						Webhook: &apiextv1.WebhookConversion{
							ClientConfig: &apiextv1.WebhookClientConfig{
								CABundle: nil,
							},
						},
					},
				}},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			defaultCustomResourceDefinitionV1(&test.existing, test.existing)
			defaultCustomResourceDefinitionV1(&test.expected, test.expected)
			modified := ptr.To(false)
			EnsureCustomResourceDefinitionV1(modified, &test.existing, test.required)
			if *modified != test.expectedModified {
				t.Errorf("mismatch modified got: %v want: %v", *modified, test.expectedModified)
			}

			if !equality.Semantic.DeepEqual(test.existing, test.expected) {
				t.Errorf("unexpected: %s", cmp.Diff(test.expected, test.existing))
			}
		})
	}
}

// Ensures the structure contains any defaults not explicitly set by the test
func defaultCustomResourceDefinitionV1(in *apiextv1.CustomResourceDefinition, from apiextv1.CustomResourceDefinition) {
	modified := ptr.To(false)
	EnsureCustomResourceDefinitionV1(modified, in, from)
}

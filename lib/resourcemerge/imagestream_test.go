package resourcemerge

import (
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	imagev1 "github.com/openshift/api/image/v1"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
)

func TestEnsureImageStreamv1_ImageStreamStatus(t *testing.T) {
	tests := []struct {
		name     string
		existing imagev1.ImageStreamStatus
		required imagev1.ImageStreamStatus
	}{
		{
			name: "cvo should ignore same status field",
			existing: imagev1.ImageStreamStatus{
				DockerImageRepository:       "registry",
				PublicDockerImageRepository: "public_registry",
				Tags: []imagev1.NamedTagEventList{
					{
						Tag: "latest",
						Items: []imagev1.TagEvent{
							{
								Created:              metav1.Time{Time: time.Unix(0, 0)},
								DockerImageReference: "DockerImageReference",
								Image:                "Image",
								Generation:           5,
							},
						},
					},
				},
			},
			required: imagev1.ImageStreamStatus{
				DockerImageRepository:       "registry",
				PublicDockerImageRepository: "public_registry",
				Tags: []imagev1.NamedTagEventList{
					{
						Tag: "latest",
						Items: []imagev1.TagEvent{
							{
								Created:              metav1.Time{Time: time.Unix(0, 0)},
								DockerImageReference: "DockerImageReference",
								Image:                "Image",
								Generation:           5,
							},
						},
					},
				},
			},
		},
		{
			name: "cvo should ignore different status field",
			existing: imagev1.ImageStreamStatus{
				DockerImageRepository:       "registry",
				PublicDockerImageRepository: "public_registry",
				Tags: []imagev1.NamedTagEventList{
					{
						Tag: "latest",
						Items: []imagev1.TagEvent{
							{
								Created:              metav1.Time{Time: time.Unix(0, 0)},
								DockerImageReference: "DockerImageReference",
								Image:                "Image",
								Generation:           5,
							},
						},
					},
				},
			},
			required: imagev1.ImageStreamStatus{},
		},
	}

	var expected imagev1.ImageStream
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			existing := imagev1.ImageStream{Status: test.existing}
			required := imagev1.ImageStream{Status: test.required}
			existing.DeepCopyInto(&expected) // We expect CVO to never modify existing due to status field
			modified := ptr.To(false)
			EnsureImagestreamv1(modified, &existing, required)
			if *modified != false {
				t.Errorf("mismatch modified got: %v want: %v", *modified, false)
			}
			if !equality.Semantic.DeepEqual(existing, expected) {
				t.Errorf("unexpected: %s", cmp.Diff(expected, existing))
			}
		})
	}
}

func TestEnsureImageStreamv1_ImageStreamSpec(t *testing.T) {
	tests := []struct {
		name     string
		existing imagev1.ImageStreamSpec
		required imagev1.ImageStreamSpec

		expectedModified bool
		expected         imagev1.ImageStreamSpec
	}{
		{
			name: "same lookup policy",
			existing: imagev1.ImageStreamSpec{
				LookupPolicy: imagev1.ImageLookupPolicy{
					Local: true,
				},
			},
			required: imagev1.ImageStreamSpec{
				LookupPolicy: imagev1.ImageLookupPolicy{
					Local: true,
				},
			},

			expectedModified: false,
		},
		{
			name: "different lookup policy",
			existing: imagev1.ImageStreamSpec{
				LookupPolicy: imagev1.ImageLookupPolicy{
					Local: true,
				},
			},
			required: imagev1.ImageStreamSpec{
				LookupPolicy: imagev1.ImageLookupPolicy{
					Local: false,
				},
			},

			expectedModified: true,
			expected: imagev1.ImageStreamSpec{
				LookupPolicy: imagev1.ImageLookupPolicy{
					Local: false,
				},
			},
		},
		{
			name: "implicit lookup policy",
			existing: imagev1.ImageStreamSpec{
				LookupPolicy: imagev1.ImageLookupPolicy{
					Local: false,
				},
			},
			required: imagev1.ImageStreamSpec{},

			expectedModified: false,
		},
		{
			name: "same TagReference.From",
			existing: imagev1.ImageStreamSpec{
				Tags: []imagev1.TagReference{
					{
						From: &v1.ObjectReference{
							Name: "foo",
						},
					},
				},
			},
			required: imagev1.ImageStreamSpec{
				Tags: []imagev1.TagReference{
					{
						From: &v1.ObjectReference{
							Name: "foo",
						},
					},
				},
			},

			expectedModified: false,
		},
		{
			name: "different TagReference.From",
			existing: imagev1.ImageStreamSpec{
				Tags: []imagev1.TagReference{
					{
						From: &v1.ObjectReference{
							Name: "foo",
						},
					},
				},
			},
			required: imagev1.ImageStreamSpec{
				Tags: []imagev1.TagReference{
					{
						From: &v1.ObjectReference{
							Name: "bar",
						},
					},
				},
			},

			expectedModified: true,
			expected: imagev1.ImageStreamSpec{
				Tags: []imagev1.TagReference{
					{
						From: &v1.ObjectReference{
							Name: "bar",
						},
					},
				},
			},
		},
		{
			name: "same TagReference.ImportPolicy.Insecure",
			existing: imagev1.ImageStreamSpec{
				Tags: []imagev1.TagReference{
					{
						ImportPolicy: imagev1.TagImportPolicy{
							Insecure: false,
						},
					},
				},
			},
			required: imagev1.ImageStreamSpec{
				Tags: []imagev1.TagReference{
					{
						ImportPolicy: imagev1.TagImportPolicy{
							Insecure: false,
						},
					},
				},
			},

			expectedModified: false,
		},
		{
			name: "different TagReference.ImportPolicy.Insecure",
			existing: imagev1.ImageStreamSpec{
				Tags: []imagev1.TagReference{
					{
						ImportPolicy: imagev1.TagImportPolicy{
							Insecure: true,
						},
					},
				},
			},
			required: imagev1.ImageStreamSpec{
				Tags: []imagev1.TagReference{
					{
						ImportPolicy: imagev1.TagImportPolicy{
							Insecure: false,
						},
					},
				},
			},

			expectedModified: true,
			expected: imagev1.ImageStreamSpec{
				Tags: []imagev1.TagReference{
					{
						ImportPolicy: imagev1.TagImportPolicy{
							Insecure: false,
						},
					},
				},
			},
		},
		{
			name: "same TagReference.ImportPolicy.Scheduled",
			existing: imagev1.ImageStreamSpec{
				Tags: []imagev1.TagReference{
					{
						ImportPolicy: imagev1.TagImportPolicy{
							Scheduled: false,
						},
					},
				},
			},
			required: imagev1.ImageStreamSpec{
				Tags: []imagev1.TagReference{
					{
						ImportPolicy: imagev1.TagImportPolicy{
							Scheduled: false,
						},
					},
				},
			},

			expectedModified: false,
		},
		{
			name: "different TagReference.ImportPolicy.Scheduled",
			existing: imagev1.ImageStreamSpec{
				Tags: []imagev1.TagReference{
					{
						ImportPolicy: imagev1.TagImportPolicy{
							Scheduled: true,
						},
					},
				},
			},
			required: imagev1.ImageStreamSpec{
				Tags: []imagev1.TagReference{
					{
						ImportPolicy: imagev1.TagImportPolicy{
							Insecure: false,
						},
					},
				},
			},

			expectedModified: true,
			expected: imagev1.ImageStreamSpec{
				Tags: []imagev1.TagReference{
					{
						ImportPolicy: imagev1.TagImportPolicy{
							Insecure: false,
						},
					},
				},
			},
		},
		{
			name: "same TagReference.ImportPolicy.ImportMode",
			existing: imagev1.ImageStreamSpec{
				Tags: []imagev1.TagReference{
					{
						ImportPolicy: imagev1.TagImportPolicy{
							ImportMode: imagev1.ImportModeLegacy,
						},
					},
				},
			},
			required: imagev1.ImageStreamSpec{
				Tags: []imagev1.TagReference{
					{
						ImportPolicy: imagev1.TagImportPolicy{
							ImportMode: imagev1.ImportModeLegacy,
						},
					},
				},
			},

			expectedModified: false,
		},
		{
			name: "different TagReference.ImportPolicy.ImportMode",
			existing: imagev1.ImageStreamSpec{
				Tags: []imagev1.TagReference{
					{
						ImportPolicy: imagev1.TagImportPolicy{
							ImportMode: imagev1.ImportModeLegacy,
						},
					},
				},
			},
			required: imagev1.ImageStreamSpec{
				Tags: []imagev1.TagReference{
					{
						ImportPolicy: imagev1.TagImportPolicy{
							ImportMode: imagev1.ImportModePreserveOriginal,
						},
					},
				},
			},

			expectedModified: true,
			expected: imagev1.ImageStreamSpec{
				Tags: []imagev1.TagReference{
					{
						ImportPolicy: imagev1.TagImportPolicy{
							ImportMode: imagev1.ImportModePreserveOriginal,
						},
					},
				},
			},
		},
		{
			name: "implicit TagReference.ImportPolicy.ImportMode",
			existing: imagev1.ImageStreamSpec{
				Tags: []imagev1.TagReference{
					{
						ImportPolicy: imagev1.TagImportPolicy{
							ImportMode: imagev1.ImportModeLegacy,
						},
					},
				},
			},
			required: imagev1.ImageStreamSpec{
				Tags: []imagev1.TagReference{{}},
			},

			expectedModified: false,
		},
		{
			name: "cvo should ignore changes in fields that are set by controller - DockerImageRepository",
			existing: imagev1.ImageStreamSpec{
				DockerImageRepository: "foo",
			},
			required: imagev1.ImageStreamSpec{
				DockerImageRepository: "bar",
			},

			expectedModified: false,
		},
		{
			name: "cvo should ignore changes in fields that are set by controller - TagReference.Annotations",
			existing: imagev1.ImageStreamSpec{
				Tags: []imagev1.TagReference{
					{
						Annotations: map[string]string{
							"foo": "bar",
						},
					},
				},
			},
			required: imagev1.ImageStreamSpec{
				Tags: []imagev1.TagReference{
					{
						Annotations: map[string]string{
							"a": "b",
						},
					},
				},
			},

			expectedModified: false,
		},
		{
			name: "cvo should ignore changes in fields that are set by controller - TagReference.Reference",
			existing: imagev1.ImageStreamSpec{
				Tags: []imagev1.TagReference{
					{
						Reference: true,
					},
				},
			},
			required: imagev1.ImageStreamSpec{
				Tags: []imagev1.TagReference{
					{
						Reference: false,
					},
				},
			},

			expectedModified: false,
		},
		{
			name: "cvo should ignore changes in fields that are set by controller - TagReference.Generation",
			existing: imagev1.ImageStreamSpec{
				Tags: []imagev1.TagReference{
					{
						Generation: ptr.To(int64(42)),
					},
				},
			},
			required: imagev1.ImageStreamSpec{
				Tags: []imagev1.TagReference{
					{
						Generation: nil,
					},
				},
			},

			expectedModified: false,
		},
		{
			name: "cvo should ignore changes in fields that are set by controller - TagReference.ReferencePolicy",
			existing: imagev1.ImageStreamSpec{
				Tags: []imagev1.TagReference{
					{
						ReferencePolicy: imagev1.TagReferencePolicy{
							Type: imagev1.LocalTagReferencePolicy,
						},
					},
				},
			},
			required: imagev1.ImageStreamSpec{
				Tags: []imagev1.TagReference{
					{
						ReferencePolicy: imagev1.TagReferencePolicy{
							Type: imagev1.SourceTagReferencePolicy,
						},
					},
				},
			},

			expectedModified: false,
		},
		{
			name: "cvo should ignore changes in fields that are set by controller - TagReference.ReferencePolicy",
			existing: imagev1.ImageStreamSpec{
				Tags: []imagev1.TagReference{
					{
						ReferencePolicy: imagev1.TagReferencePolicy{
							Type: imagev1.LocalTagReferencePolicy,
						},
					},
				},
			},
			required: imagev1.ImageStreamSpec{
				Tags: []imagev1.TagReference{
					{
						ReferencePolicy: imagev1.TagReferencePolicy{
							Type: imagev1.SourceTagReferencePolicy,
						},
					},
				},
			},

			expectedModified: false,
		},
		{
			name:     "cvo should append missing required tag reference with its data to an empty existing tags",
			existing: imagev1.ImageStreamSpec{},
			required: imagev1.ImageStreamSpec{
				Tags: []imagev1.TagReference{
					{
						Name: "foo",
						Annotations: map[string]string{
							"key": "value",
						},
						From: &v1.ObjectReference{
							Name: "name",
							Kind: "example",
						},
						Reference:  false,
						Generation: ptr.To(int64(42)),
						ImportPolicy: imagev1.TagImportPolicy{
							Insecure:   true,
							Scheduled:  true,
							ImportMode: imagev1.ImportModePreserveOriginal,
						},
						ReferencePolicy: imagev1.TagReferencePolicy{
							Type: imagev1.SourceTagReferencePolicy,
						},
					},
				},
			},

			expectedModified: true,
			expected: imagev1.ImageStreamSpec{
				Tags: []imagev1.TagReference{
					{
						Name: "foo",
						Annotations: map[string]string{
							"key": "value",
						},
						From: &v1.ObjectReference{
							Name: "name",
							Kind: "example",
						},
						Reference:  false,
						Generation: ptr.To(int64(42)),
						ImportPolicy: imagev1.TagImportPolicy{
							Insecure:   true,
							Scheduled:  true,
							ImportMode: imagev1.ImportModePreserveOriginal,
						},
						ReferencePolicy: imagev1.TagReferencePolicy{
							Type: imagev1.SourceTagReferencePolicy,
						},
					},
				},
			},
		},
		{
			name: "cvo should append missing required tag reference with its data to a non-empty existing tags",
			existing: imagev1.ImageStreamSpec{
				Tags: []imagev1.TagReference{
					{
						Name: "bar",
					},
				},
			},
			required: imagev1.ImageStreamSpec{
				Tags: []imagev1.TagReference{
					{
						Name: "foo",
						Annotations: map[string]string{
							"key": "value",
						},
						From: &v1.ObjectReference{
							Name: "name",
							Kind: "example",
						},
						Reference:  false,
						Generation: ptr.To(int64(42)),
						ImportPolicy: imagev1.TagImportPolicy{
							Insecure:   true,
							Scheduled:  true,
							ImportMode: imagev1.ImportModePreserveOriginal,
						},
						ReferencePolicy: imagev1.TagReferencePolicy{
							Type: imagev1.SourceTagReferencePolicy,
						},
					},
				},
			},

			expectedModified: true,
			expected: imagev1.ImageStreamSpec{
				Tags: []imagev1.TagReference{
					{
						Name: "bar",
					},
					{
						Name: "foo",
						Annotations: map[string]string{
							"key": "value",
						},
						From: &v1.ObjectReference{
							Name: "name",
							Kind: "example",
						},
						Reference:  false,
						Generation: ptr.To(int64(42)),
						ImportPolicy: imagev1.TagImportPolicy{
							Insecure:   true,
							Scheduled:  true,
							ImportMode: imagev1.ImportModePreserveOriginal,
						},
						ReferencePolicy: imagev1.TagReferencePolicy{
							Type: imagev1.SourceTagReferencePolicy,
						},
					},
				},
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			existing := imagev1.ImageStream{Spec: test.existing}
			required := imagev1.ImageStream{Spec: test.required}
			expected := imagev1.ImageStream{Spec: test.expected}
			if test.expectedModified == false {
				// We expect CVO to not modify existing, thus expected is supposed to be same as existing
				existing.DeepCopyInto(&expected)
			}
			defaultImageStream(&existing, existing)
			defaultImageStream(&expected, expected)
			modified := ptr.To(false)
			EnsureImagestreamv1(modified, &existing, required)
			if *modified != test.expectedModified {
				t.Errorf("mismatch modified got: %v want: %v", *modified, test.expectedModified)
			}
			if !equality.Semantic.DeepEqual(existing, expected) {
				t.Errorf("unexpected: %s", cmp.Diff(expected, existing))
			}
		})
	}
}

// Ensures the structure contains any defaults not explicitly set by the test
func defaultImageStream(in *imagev1.ImageStream, from imagev1.ImageStream) {
	modified := ptr.To(false)
	EnsureImagestreamv1(modified, in, from)
}

func TestEnsureTagReferencev1Defaults(t *testing.T) {
	defaultedTagReference := imagev1.TagReference{
		ImportPolicy: imagev1.TagImportPolicy{
			ImportMode: imagev1.ImportModeLegacy,
		},
		ReferencePolicy: imagev1.TagReferencePolicy{
			Type: imagev1.SourceTagReferencePolicy,
		},
	}
	tests := []struct {
		name string
		in   imagev1.TagReference
	}{
		{
			name: "defaulting of TagReference.ImportPolicy.ImportMode",
			in: imagev1.TagReference{
				ReferencePolicy: imagev1.TagReferencePolicy{
					Type: imagev1.SourceTagReferencePolicy,
				},
			},
		},
		{
			name: "defaulting of TagReference.ReferencePolicy",
			in: imagev1.TagReference{
				ImportPolicy: imagev1.TagImportPolicy{
					ImportMode: imagev1.ImportModeLegacy,
				},
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ensureTagReferencev1Defaults(&test.in)
			if !equality.Semantic.DeepEqual(test.in, defaultedTagReference) {
				t.Errorf("unexpected: %s", cmp.Diff(test.in, defaultedTagReference))
			}
		})
	}
}

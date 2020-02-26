package resourcemerge

import (
	"testing"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/apimachinery/pkg/util/diff"
	"k8s.io/utils/pointer"
)

func TestEnsurePodSpec(t *testing.T) {
	tests := []struct {
		name     string
		existing corev1.PodSpec
		input    corev1.PodSpec

		expectedModified bool
		expected         corev1.PodSpec
	}{
		{
			name:     "empty inputs",
			existing: corev1.PodSpec{},
			input:    corev1.PodSpec{},

			expectedModified: false,
			expected:         corev1.PodSpec{},
		},
		{
			name: "remove regular containers from existing",
			existing: corev1.PodSpec{
				Containers: []corev1.Container{
					corev1.Container{Name: "test"},
					corev1.Container{Name: "to-be-removed"}}},
			input: corev1.PodSpec{
				Containers: []corev1.Container{
					corev1.Container{Name: "test"}}},

			expectedModified: true,
			expected: corev1.PodSpec{
				Containers: []corev1.Container{
					corev1.Container{Name: "test"}}},
		},
		{
			name: "remove regular and init containers from existing",
			existing: corev1.PodSpec{
				InitContainers: []corev1.Container{
					corev1.Container{Name: "test-init"}},
				Containers: []corev1.Container{
					corev1.Container{Name: "test"}}},
			input: corev1.PodSpec{},

			expectedModified: true,
			expected:         corev1.PodSpec{},
		},
		{
			name: "remove init containers from existing",
			existing: corev1.PodSpec{
				InitContainers: []corev1.Container{
					corev1.Container{Name: "test-init"}}},
			input: corev1.PodSpec{},

			expectedModified: true,
			expected:         corev1.PodSpec{},
		},
		{
			name: "append regular and init containers",
			existing: corev1.PodSpec{
				InitContainers: []corev1.Container{
					corev1.Container{Name: "test-init-a"}},
				Containers: []corev1.Container{
					corev1.Container{Name: "test-a"}}},
			input: corev1.PodSpec{
				InitContainers: []corev1.Container{
					corev1.Container{Name: "test-init-a"},
					corev1.Container{Name: "test-init-b"},
				},
				Containers: []corev1.Container{
					corev1.Container{Name: "test-a"},
					corev1.Container{Name: "test-b"},
				},
			},

			expectedModified: true,
			expected: corev1.PodSpec{
				InitContainers: []corev1.Container{
					corev1.Container{Name: "test-init-a"},
					corev1.Container{Name: "test-init-b"},
				},
				Containers: []corev1.Container{
					corev1.Container{Name: "test-a"},
					corev1.Container{Name: "test-b"},
				},
			},
		},
		{
			name: "match regular and init containers",
			existing: corev1.PodSpec{
				InitContainers: []corev1.Container{
					corev1.Container{Name: "test-init"}},
				Containers: []corev1.Container{
					corev1.Container{Name: "test"}}},
			input: corev1.PodSpec{
				InitContainers: []corev1.Container{
					corev1.Container{Name: "test-init"}},
				Containers: []corev1.Container{
					corev1.Container{Name: "test"}}},

			expectedModified: false,
			expected: corev1.PodSpec{
				InitContainers: []corev1.Container{
					corev1.Container{Name: "test-init"}},
				Containers: []corev1.Container{
					corev1.Container{Name: "test"}}},
		},
		{
			name: "remove limits and requests on container",
			existing: corev1.PodSpec{
				Containers: []corev1.Container{
					corev1.Container{
						Name: "test",
						Resources: corev1.ResourceRequirements{
							Limits:   corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("2m")},
							Requests: corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("2m")},
						},
					},
				},
			},
			input: corev1.PodSpec{
				Containers: []corev1.Container{
					corev1.Container{Name: "test"}}},

			expectedModified: true,
			expected: corev1.PodSpec{
				Containers: []corev1.Container{
					corev1.Container{Name: "test"},
				},
			},
		},
		{
			name: "modify limits and requests on container",
			existing: corev1.PodSpec{
				Containers: []corev1.Container{
					corev1.Container{
						Name: "test",
						Resources: corev1.ResourceRequirements{
							Limits:   corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("2m")},
							Requests: corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("2m")},
						},
					},
				},
			},
			input: corev1.PodSpec{
				Containers: []corev1.Container{
					corev1.Container{
						Name: "test",
						Resources: corev1.ResourceRequirements{
							Limits:   corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("4m")},
							Requests: corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("4m")},
						},
					},
				},
			},

			expectedModified: true,
			expected: corev1.PodSpec{
				Containers: []corev1.Container{
					corev1.Container{
						Name: "test",
						Resources: corev1.ResourceRequirements{
							Limits:   corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("4m")},
							Requests: corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("4m")},
						},
					},
				},
			},
		},
		{
			name: "match limits and requests on container",
			existing: corev1.PodSpec{
				Containers: []corev1.Container{
					corev1.Container{
						Name: "test",
						Resources: corev1.ResourceRequirements{
							Limits:   corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("2m")},
							Requests: corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("2m")},
						},
					},
				},
			},
			input: corev1.PodSpec{
				Containers: []corev1.Container{
					corev1.Container{
						Name: "test",
						Resources: corev1.ResourceRequirements{
							Limits:   corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("2m")},
							Requests: corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("2m")},
						},
					},
				},
			},

			expectedModified: false,
			expected: corev1.PodSpec{
				Containers: []corev1.Container{
					corev1.Container{
						Name: "test",
						Resources: corev1.ResourceRequirements{
							Limits:   corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("2m")},
							Requests: corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("2m")},
						},
					},
				},
			},
		},
		{
			name: "add limits and requests on container",
			existing: corev1.PodSpec{
				Containers: []corev1.Container{
					corev1.Container{Name: "test"},
				},
			},
			input: corev1.PodSpec{
				Containers: []corev1.Container{
					corev1.Container{
						Name: "test",
						Resources: corev1.ResourceRequirements{
							Limits:   corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("2m")},
							Requests: corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("2m")},
						},
					},
				},
			},

			expectedModified: true,
			expected: corev1.PodSpec{
				Containers: []corev1.Container{
					corev1.Container{
						Name: "test",
						Resources: corev1.ResourceRequirements{
							Limits:   corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("2m")},
							Requests: corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("2m")},
						},
					},
				},
			},
		},
		{
			name: "add ports on container",
			existing: corev1.PodSpec{
				Containers: []corev1.Container{
					corev1.Container{Name: "test"},
				},
			},
			input: corev1.PodSpec{
				Containers: []corev1.Container{
					corev1.Container{
						Name: "test",
						Ports: []corev1.ContainerPort{
							corev1.ContainerPort{ContainerPort: 8080},
						},
					},
				},
			},
			expectedModified: true,
			expected: corev1.PodSpec{
				Containers: []corev1.Container{
					corev1.Container{
						Name: "test",
						Ports: []corev1.ContainerPort{
							corev1.ContainerPort{ContainerPort: 8080},
						},
					},
				},
			},
		},
		{
			name: "replace ports on container",
			existing: corev1.PodSpec{
				Containers: []corev1.Container{
					corev1.Container{
						Name: "test",
						Ports: []corev1.ContainerPort{
							corev1.ContainerPort{ContainerPort: 8080},
						},
					},
				},
			},
			input: corev1.PodSpec{
				Containers: []corev1.Container{
					corev1.Container{
						Name: "test",
						Ports: []corev1.ContainerPort{
							corev1.ContainerPort{ContainerPort: 9191},
						},
					},
				},
			},
			expectedModified: true,
			expected: corev1.PodSpec{
				Containers: []corev1.Container{
					corev1.Container{
						Name: "test",
						Ports: []corev1.ContainerPort{
							corev1.ContainerPort{ContainerPort: 9191},
						},
					},
				},
			},
		},
		{
			name: "remove container ports",
			existing: corev1.PodSpec{
				Containers: []corev1.Container{
					corev1.Container{
						Name: "test",
						Ports: []corev1.ContainerPort{
							corev1.ContainerPort{ContainerPort: 8080},
						},
					},
				},
			},
			input: corev1.PodSpec{
				Containers: []corev1.Container{
					corev1.Container{Name: "test"},
				},
			},
			expectedModified: true,
			expected: corev1.PodSpec{
				Containers: []corev1.Container{
					corev1.Container{
						Name:  "test",
						Ports: []corev1.ContainerPort{},
					},
				},
			},
		},
		{
			name: "remove a container",
			existing: corev1.PodSpec{
				Containers: []corev1.Container{
					corev1.Container{Name: "test-A"},
					corev1.Container{Name: "test-B"},
				},
			},
			input: corev1.PodSpec{
				Containers: []corev1.Container{
					corev1.Container{Name: "test-B"},
				},
			},

			expectedModified: true,
			expected: corev1.PodSpec{
				Containers: []corev1.Container{
					corev1.Container{Name: "test-B"},
				},
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			modified := pointer.BoolPtr(false)
			ensurePodSpec(modified, &test.existing, test.input)
			if *modified != test.expectedModified {
				t.Errorf("mismatch modified got: %v want: %v", *modified, test.expectedModified)
			}

			if !equality.Semantic.DeepEqual(test.existing, test.expected) {
				t.Errorf("unexpected: %s", diff.ObjectReflectDiff(test.expected, test.existing))
			}
		})
	}
}

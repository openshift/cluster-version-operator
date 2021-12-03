package resourcemerge

import (
	"testing"

	"k8s.io/apimachinery/pkg/util/intstr"

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
			name:     "empty inputs/defaults",
			existing: corev1.PodSpec{},
			input:    corev1.PodSpec{},

			expectedModified: false,
			expected:         corev1.PodSpec{},
		},
		{
			name: "remove regular containers from existing",
			existing: corev1.PodSpec{
				Containers: []corev1.Container{
					{Name: "test"},
					{Name: "to-be-removed"}}},
			input: corev1.PodSpec{
				Containers: []corev1.Container{
					{Name: "test"}}},

			expectedModified: true,
			expected: corev1.PodSpec{
				Containers: []corev1.Container{
					{Name: "test"}}},
		},
		{
			name: "remove regular and init containers from existing",
			existing: corev1.PodSpec{
				InitContainers: []corev1.Container{
					{Name: "test-init"}},
				Containers: []corev1.Container{
					{Name: "test"}}},
			input: corev1.PodSpec{},

			expectedModified: true,
			expected:         corev1.PodSpec{},
		},
		{
			name: "remove init containers from existing",
			existing: corev1.PodSpec{
				InitContainers: []corev1.Container{
					{Name: "test-init"}}},
			input: corev1.PodSpec{},

			expectedModified: true,
			expected:         corev1.PodSpec{},
		},
		{
			name: "append regular and init containers",
			existing: corev1.PodSpec{
				InitContainers: []corev1.Container{
					{Name: "test-init-a"}},
				Containers: []corev1.Container{
					{Name: "test-a"}}},
			input: corev1.PodSpec{
				InitContainers: []corev1.Container{
					{Name: "test-init-a"},
					{Name: "test-init-b"},
				},
				Containers: []corev1.Container{
					{Name: "test-a"},
					{Name: "test-b"},
				},
			},

			expectedModified: true,
			expected: corev1.PodSpec{
				InitContainers: []corev1.Container{
					{Name: "test-init-a"},
					{Name: "test-init-b"},
				},
				Containers: []corev1.Container{
					{Name: "test-a"},
					{Name: "test-b"},
				},
			},
		},
		{
			name: "match regular and init containers",
			existing: corev1.PodSpec{
				InitContainers: []corev1.Container{
					{Name: "test-init"}},
				Containers: []corev1.Container{
					{Name: "test"}}},
			input: corev1.PodSpec{
				InitContainers: []corev1.Container{
					{Name: "test-init"}},
				Containers: []corev1.Container{
					{Name: "test"}}},

			expectedModified: false,
			expected: corev1.PodSpec{
				InitContainers: []corev1.Container{
					{Name: "test-init"}},
				Containers: []corev1.Container{
					{Name: "test"}}},
		},
		{
			name: "remove limits and requests on container",
			existing: corev1.PodSpec{
				Containers: []corev1.Container{
					{
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
					{Name: "test"}}},

			expectedModified: true,
			expected: corev1.PodSpec{
				Containers: []corev1.Container{
					{Name: "test"},
				},
			},
		},
		{
			name: "modify limits and requests on container",
			existing: corev1.PodSpec{
				Containers: []corev1.Container{
					{
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
					{
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
					{
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
					{
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
					{
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
					{
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
					{Name: "test"},
				},
			},
			input: corev1.PodSpec{
				Containers: []corev1.Container{
					{
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
					{
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
			name: "remove a container",
			existing: corev1.PodSpec{
				Containers: []corev1.Container{
					{Name: "test-A"},
					{Name: "test-B"},
				},
			},
			input: corev1.PodSpec{
				Containers: []corev1.Container{
					{Name: "test-B"},
				},
			},

			expectedModified: true,
			expected: corev1.PodSpec{
				Containers: []corev1.Container{
					{Name: "test-B"},
				},
			},
		},
		{
			name: "add ports on container",
			existing: corev1.PodSpec{
				Containers: []corev1.Container{
					{Name: "test"},
				},
			},
			input: corev1.PodSpec{
				Containers: []corev1.Container{
					{
						Name: "test",
						Ports: []corev1.ContainerPort{
							{ContainerPort: 8080},
						},
					},
				},
			},
			expectedModified: true,
			expected: corev1.PodSpec{
				Containers: []corev1.Container{
					{
						Name: "test",
						Ports: []corev1.ContainerPort{
							{ContainerPort: 8080},
						},
					},
				},
			},
		},
		{
			name: "replace ports on container",
			existing: corev1.PodSpec{
				Containers: []corev1.Container{
					{
						Name: "test",
						Ports: []corev1.ContainerPort{
							{ContainerPort: 8080},
						},
					},
				},
			},
			input: corev1.PodSpec{
				Containers: []corev1.Container{
					{
						Name: "test",
						Ports: []corev1.ContainerPort{
							{ContainerPort: 9191},
						},
					},
				},
			},
			expectedModified: true,
			expected: corev1.PodSpec{
				Containers: []corev1.Container{
					{
						Name: "test",
						Ports: []corev1.ContainerPort{
							{ContainerPort: 9191},
						},
					},
				},
			},
		},
		{
			name: "remove container ports",
			existing: corev1.PodSpec{
				Containers: []corev1.Container{
					{
						Name: "test",
						Ports: []corev1.ContainerPort{
							{ContainerPort: 8080},
						},
					},
				},
			},
			input: corev1.PodSpec{
				Containers: []corev1.Container{
					{Name: "test"},
				},
			},
			expectedModified: true,
			expected: corev1.PodSpec{
				Containers: []corev1.Container{
					{
						Name:  "test",
						Ports: []corev1.ContainerPort{},
					},
				},
			},
		},
		{
			name: "modify container readiness probe",
			existing: corev1.PodSpec{
				Containers: []corev1.Container{
					{
						Name: "test",
						ReadinessProbe: &corev1.Probe{
							InitialDelaySeconds: 1,
							TimeoutSeconds:      2,
							PeriodSeconds:       3,
							SuccessThreshold:    4,
							FailureThreshold:    5,
						},
					},
				},
			},
			input: corev1.PodSpec{
				Containers: []corev1.Container{
					{
						Name: "test",
						ReadinessProbe: &corev1.Probe{
							InitialDelaySeconds: 7,
							TimeoutSeconds:      8,
							PeriodSeconds:       9,
							SuccessThreshold:    10,
							FailureThreshold:    11,
						},
					},
				},
			},
			expectedModified: true,
			expected: corev1.PodSpec{
				Containers: []corev1.Container{
					{
						Name: "test",
						ReadinessProbe: &corev1.Probe{
							InitialDelaySeconds: 7,
							TimeoutSeconds:      8,
							PeriodSeconds:       9,
							SuccessThreshold:    10,
							FailureThreshold:    11,
						},
					},
				},
			},
		},
		{
			name: "modify container liveness probe",
			existing: corev1.PodSpec{
				Containers: []corev1.Container{
					{
						Name: "test",
						LivenessProbe: &corev1.Probe{
							InitialDelaySeconds: 1,
							TimeoutSeconds:      2,
							PeriodSeconds:       3,
							SuccessThreshold:    4,
							FailureThreshold:    5,
						},
					},
				},
			},
			input: corev1.PodSpec{
				Containers: []corev1.Container{
					{
						Name: "test",
						LivenessProbe: &corev1.Probe{
							InitialDelaySeconds: 7,
							TimeoutSeconds:      8,
							PeriodSeconds:       9,
							SuccessThreshold:    10,
							FailureThreshold:    11,
						},
					},
				},
			},
			expectedModified: true,
			expected: corev1.PodSpec{
				Containers: []corev1.Container{
					{
						Name: "test",
						LivenessProbe: &corev1.Probe{
							InitialDelaySeconds: 7,
							TimeoutSeconds:      8,
							PeriodSeconds:       9,
							SuccessThreshold:    10,
							FailureThreshold:    11,
						},
					},
				},
			},
		},
		{
			name: "add volumes on container",
			existing: corev1.PodSpec{
				Containers: []corev1.Container{
					{Name: "test"},
				},
			},
			input: corev1.PodSpec{
				Containers: []corev1.Container{
					{
						Name: "test",
						VolumeMounts: []corev1.VolumeMount{
							{
								Name:      "test-volume",
								MountPath: "/mnt",
							},
						},
					},
				},
				Volumes: []corev1.Volume{
					{
						Name: "test-volume",
						VolumeSource: corev1.VolumeSource{
							EmptyDir: &corev1.EmptyDirVolumeSource{},
						},
					},
				},
			},
			expectedModified: true,
			expected: corev1.PodSpec{
				Containers: []corev1.Container{
					{
						Name: "test",
						VolumeMounts: []corev1.VolumeMount{
							{
								Name:      "test-volume",
								MountPath: "/mnt",
							},
						},
					},
				},
				Volumes: []corev1.Volume{
					{
						Name: "test-volume",
						VolumeSource: corev1.VolumeSource{
							EmptyDir: &corev1.EmptyDirVolumeSource{},
						},
					},
				},
			},
		},
		{
			name: "modify volume path on container",
			existing: corev1.PodSpec{
				Containers: []corev1.Container{
					{
						Name: "test",
						VolumeMounts: []corev1.VolumeMount{
							{
								Name:      "test-volume",
								MountPath: "/mnt/a",
							},
						},
					},
				},
				Volumes: []corev1.Volume{
					{
						Name: "test-volume",
						VolumeSource: corev1.VolumeSource{
							EmptyDir: &corev1.EmptyDirVolumeSource{},
						},
					},
				},
			},
			input: corev1.PodSpec{
				Containers: []corev1.Container{
					{
						Name: "test",
						VolumeMounts: []corev1.VolumeMount{
							{
								Name:      "test-volume",
								MountPath: "/mnt/b",
							},
						},
					},
				},
				Volumes: []corev1.Volume{
					{
						Name: "test-volume",
						VolumeSource: corev1.VolumeSource{
							ConfigMap: &corev1.ConfigMapVolumeSource{
								LocalObjectReference: corev1.LocalObjectReference{
									Name: "test-config-map",
								},
							},
						},
					},
				},
			},
			expectedModified: true,
			expected: corev1.PodSpec{
				Containers: []corev1.Container{
					{
						Name: "test",
						VolumeMounts: []corev1.VolumeMount{
							{
								Name:      "test-volume",
								MountPath: "/mnt/b",
							},
						},
					},
				},
				Volumes: []corev1.Volume{
					{
						Name: "test-volume",
						VolumeSource: corev1.VolumeSource{
							ConfigMap: &corev1.ConfigMapVolumeSource{
								LocalObjectReference: corev1.LocalObjectReference{
									Name: "test-config-map",
								},
							},
						},
					},
				},
			},
		},
		{
			name: "modify volume name on container",
			existing: corev1.PodSpec{
				Containers: []corev1.Container{
					{
						Name: "test",
						VolumeMounts: []corev1.VolumeMount{
							{
								Name:      "test-volume-a",
								MountPath: "/mnt",
							},
						},
					},
				},
				Volumes: []corev1.Volume{
					{
						Name: "test-volume-a",
						VolumeSource: corev1.VolumeSource{
							EmptyDir: &corev1.EmptyDirVolumeSource{},
						},
					},
				},
			},
			input: corev1.PodSpec{
				Containers: []corev1.Container{
					{
						Name: "test",
						VolumeMounts: []corev1.VolumeMount{
							{
								Name:      "test-volume-b",
								MountPath: "/mnt",
							},
						},
					},
				},
				Volumes: []corev1.Volume{
					{
						Name: "test-volume-b",
						VolumeSource: corev1.VolumeSource{
							ConfigMap: &corev1.ConfigMapVolumeSource{
								LocalObjectReference: corev1.LocalObjectReference{
									Name: "test-config-map",
								},
							},
						},
					},
				},
			},
			expectedModified: true,
			expected: corev1.PodSpec{
				Containers: []corev1.Container{
					{
						Name: "test",
						VolumeMounts: []corev1.VolumeMount{
							{
								Name:      "test-volume-b",
								MountPath: "/mnt",
							},
						},
					},
				},
				Volumes: []corev1.Volume{
					{
						Name: "test-volume-b",
						VolumeSource: corev1.VolumeSource{
							ConfigMap: &corev1.ConfigMapVolumeSource{
								LocalObjectReference: corev1.LocalObjectReference{
									Name: "test-config-map",
								},
							},
						},
					},
				},
			},
		},
		{
			name: "remove container volumes",
			existing: corev1.PodSpec{
				Containers: []corev1.Container{
					{
						Name: "test",
						VolumeMounts: []corev1.VolumeMount{
							{
								Name:      "test-volume",
								MountPath: "/mnt",
							},
						},
					},
				},
				Volumes: []corev1.Volume{
					{
						Name: "test-volume",
						VolumeSource: corev1.VolumeSource{
							EmptyDir: &corev1.EmptyDirVolumeSource{},
						},
					},
				},
			},
			input: corev1.PodSpec{
				Containers: []corev1.Container{
					{Name: "test"},
				},
			},
			expectedModified: true,
			expected: corev1.PodSpec{
				Containers: []corev1.Container{
					{Name: "test"},
				},
			},
		},
		{
			name: "remove container volumes with masking name",
			existing: corev1.PodSpec{
				Containers: []corev1.Container{
					{
						Name: "test",
						VolumeMounts: []corev1.VolumeMount{
							{
								Name:      "test-volume",
								MountPath: "/mnt/a",
							},
							{
								Name:      "test-volume",
								MountPath: "/mnt/b",
							},
						},
					},
				},
				Volumes: []corev1.Volume{
					{
						Name: "test-volume",
						VolumeSource: corev1.VolumeSource{
							EmptyDir: &corev1.EmptyDirVolumeSource{},
						},
					},
				},
			},
			input: corev1.PodSpec{
				Containers: []corev1.Container{
					{
						Name: "test",
						VolumeMounts: []corev1.VolumeMount{
							{
								Name:      "test-volume",
								MountPath: "/mnt/a",
							},
						},
					},
				},
				Volumes: []corev1.Volume{
					{
						Name: "test-volume",
						VolumeSource: corev1.VolumeSource{
							EmptyDir: &corev1.EmptyDirVolumeSource{},
						},
					},
				},
			},
			expectedModified: true,
			expected: corev1.PodSpec{
				Containers: []corev1.Container{
					{
						Name: "test",
						VolumeMounts: []corev1.VolumeMount{
							{
								Name:      "test-volume",
								MountPath: "/mnt/a",
							},
						},
					},
				},
				Volumes: []corev1.Volume{
					{
						Name: "test-volume",
						VolumeSource: corev1.VolumeSource{
							EmptyDir: &corev1.EmptyDirVolumeSource{},
						},
					},
				},
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			defaultPodSpec(&test.existing, test.existing)
			defaultPodSpec(&test.expected, test.expected)
			modified := pointer.BoolPtr(false)
			ensurePodSpec(modified, &test.existing, test.input)

			// This has to be done again to get defaults set on structures that didn't exixt before
			// running ensurePodSpec (e.g. ContainerPort)
			defaultPodSpec(&test.existing, test.existing)

			if *modified != test.expectedModified {
				t.Errorf("mismatch modified got: %v want: %v", *modified, test.expectedModified)
			}

			if !equality.Semantic.DeepEqual(test.existing, test.expected) {
				t.Errorf("unexpected: %s", diff.ObjectReflectDiff(test.expected, test.existing))
			}
		})
	}
}

func TestEnsureServicePorts(t *testing.T) {
	tests := []struct {
		name     string
		existing corev1.Service
		input    corev1.Service

		expectedModified bool
		expected         corev1.Service
	}{
		{
			name:             "empty inputs",
			existing:         corev1.Service{},
			input:            corev1.Service{},
			expectedModified: false,
			expected:         corev1.Service{},
		},
		{
			name: "add port (no name)",
			existing: corev1.Service{
				Spec: corev1.ServiceSpec{},
			},
			input: corev1.Service{
				Spec: corev1.ServiceSpec{
					Ports: []corev1.ServicePort{
						{
							Protocol: corev1.ProtocolUDP,
							Port:     8282,
						},
					},
				},
			},
			expectedModified: true,
			expected: corev1.Service{
				Spec: corev1.ServiceSpec{
					Ports: []corev1.ServicePort{
						{
							Protocol:   corev1.ProtocolUDP,
							Port:       8282,
							TargetPort: intstr.FromInt(0),
						},
					},
				},
			},
		},
		{
			name: "add port (with name)",
			existing: corev1.Service{
				Spec: corev1.ServiceSpec{},
			},
			input: corev1.Service{
				Spec: corev1.ServiceSpec{
					Ports: []corev1.ServicePort{
						{
							Name:     "foo",
							Protocol: corev1.ProtocolUDP,
							Port:     8282,
						},
					},
				},
			},
			expectedModified: true,
			expected: corev1.Service{
				Spec: corev1.ServiceSpec{
					Ports: []corev1.ServicePort{
						{
							Name:     "foo",
							Protocol: corev1.ProtocolUDP,
							Port:     8282,
						},
					},
				},
			},
		},
		{
			name: "remove port (no name)",
			existing: corev1.Service{
				Spec: corev1.ServiceSpec{
					Ports: []corev1.ServicePort{
						{
							Port: 8282,
						},
					},
				},
			},
			input: corev1.Service{
				Spec: corev1.ServiceSpec{},
			},
			expectedModified: true,
			expected: corev1.Service{
				Spec: corev1.ServiceSpec{},
			},
		},
		{
			name: "remove port (with name)",
			existing: corev1.Service{
				Spec: corev1.ServiceSpec{
					Ports: []corev1.ServicePort{
						{
							Name: "bar",
							Port: 8282,
						},
					},
				},
			},
			input: corev1.Service{
				Spec: corev1.ServiceSpec{},
			},
			expectedModified: true,
			expected: corev1.Service{
				Spec: corev1.ServiceSpec{},
			},
		},
		{
			name: "replace port (same port name, different port)",
			existing: corev1.Service{
				Spec: corev1.ServiceSpec{
					Ports: []corev1.ServicePort{
						{
							Name:     "test",
							Protocol: corev1.ProtocolUDP,
							Port:     8080,
						},
					},
				},
			},
			input: corev1.Service{
				Spec: corev1.ServiceSpec{
					Ports: []corev1.ServicePort{
						{
							Name:     "test",
							Protocol: corev1.ProtocolUDP,
							Port:     8282,
						},
					},
				},
			},
			expectedModified: true,
			expected: corev1.Service{
				Spec: corev1.ServiceSpec{
					Ports: []corev1.ServicePort{
						{
							Name:       "test",
							Protocol:   corev1.ProtocolUDP,
							Port:       8282,
							TargetPort: intstr.FromInt(8282),
						},
					},
				},
			},
		},
		{
			name: "replace port (no name, different port)",
			existing: corev1.Service{
				Spec: corev1.ServiceSpec{
					Ports: []corev1.ServicePort{
						{
							Port: 8080,
						},
					},
				},
			},
			input: corev1.Service{
				Spec: corev1.ServiceSpec{
					Ports: []corev1.ServicePort{
						{
							Protocol: corev1.ProtocolUDP,
							Port:     8282,
						},
					},
				},
			},
			expectedModified: true,
			expected: corev1.Service{
				Spec: corev1.ServiceSpec{
					Ports: []corev1.ServicePort{
						{
							Protocol:   corev1.ProtocolUDP,
							Port:       8282,
							TargetPort: intstr.FromInt(8282),
						},
					},
				},
			},
		},
		{
			name: "replace multiple ports",
			existing: corev1.Service{
				Spec: corev1.ServiceSpec{
					Ports: []corev1.ServicePort{
						{
							Name: "foo",
							Port: 8080,
						},
						{
							Name: "bar",
							Port: 8081,
						},
					},
				},
			},
			input: corev1.Service{
				Spec: corev1.ServiceSpec{
					Ports: []corev1.ServicePort{
						{
							Name:     "foo",
							Protocol: corev1.ProtocolUDP,
							Port:     8282,
						},
						{
							Name:     "bar",
							Protocol: corev1.ProtocolUDP,
							Port:     8283,
						},
					},
				},
			},
			expectedModified: true,
			expected: corev1.Service{
				Spec: corev1.ServiceSpec{
					Ports: []corev1.ServicePort{
						{
							Name:       "foo",
							Protocol:   corev1.ProtocolUDP,
							Port:       8282,
							TargetPort: intstr.FromInt(8282),
						},
						{
							Name:       "bar",
							Protocol:   corev1.ProtocolUDP,
							Port:       8283,
							TargetPort: intstr.FromInt(8283),
						},
					},
				},
			},
		},
		{
			name: "equal (same ports different order)",
			existing: corev1.Service{
				Spec: corev1.ServiceSpec{
					Ports: []corev1.ServicePort{
						{
							Name:       "foo",
							Protocol:   corev1.ProtocolUDP,
							Port:       8080,
							TargetPort: intstr.FromInt(8080),
						},
						{
							Name:       "bar",
							Protocol:   corev1.ProtocolUDP,
							Port:       8081,
							TargetPort: intstr.FromInt(8081),
						},
					},
				},
			},
			input: corev1.Service{
				Spec: corev1.ServiceSpec{
					Ports: []corev1.ServicePort{
						{
							Name:       "bar",
							Protocol:   corev1.ProtocolUDP,
							Port:       8081,
							TargetPort: intstr.FromInt(8081),
						},
						{
							Name:       "foo",
							Protocol:   corev1.ProtocolUDP,
							Port:       8080,
							TargetPort: intstr.FromInt(8080),
						},
					},
				},
			},
			expectedModified: false,
			expected: corev1.Service{
				Spec: corev1.ServiceSpec{
					Ports: []corev1.ServicePort{
						{
							Name:       "foo",
							Protocol:   corev1.ProtocolUDP,
							Port:       8080,
							TargetPort: intstr.FromInt(8080),
						},
						{
							Name:       "bar",
							Protocol:   corev1.ProtocolUDP,
							Port:       8081,
							TargetPort: intstr.FromInt(8081),
						},
					},
				},
			},
		},
		{
			name: "equal (Protocol defaults to ProtocolTCP)",
			existing: corev1.Service{
				Spec: corev1.ServiceSpec{
					Ports: []corev1.ServicePort{
						{
							Name:       "foo",
							Port:       8080,
							TargetPort: intstr.FromInt(8080),
							Protocol:   corev1.ProtocolTCP,
						},
						{
							Name:       "bar",
							Protocol:   corev1.ProtocolTCP,
							Port:       8081,
							TargetPort: intstr.FromInt(8081),
						},
					},
				},
			},
			input: corev1.Service{
				Spec: corev1.ServiceSpec{
					Ports: []corev1.ServicePort{
						{
							Name:       "foo",
							Port:       8080,
							TargetPort: intstr.FromInt(8080),
						},
						{
							Name:       "bar",
							Port:       8081,
							TargetPort: intstr.FromInt(8081),
						},
					},
				},
			},
			expectedModified: false,
			expected: corev1.Service{
				Spec: corev1.ServiceSpec{
					Ports: []corev1.ServicePort{
						{
							Name:       "foo",
							Protocol:   corev1.ProtocolTCP,
							Port:       8080,
							TargetPort: intstr.FromInt(8080),
						},
						{
							Name:       "bar",
							Protocol:   corev1.ProtocolTCP,
							Port:       8081,
							TargetPort: intstr.FromInt(8081),
						},
					},
				},
			},
		},
		{
			name: "equal (TargetPort defaults to Port)",
			existing: corev1.Service{
				Spec: corev1.ServiceSpec{
					Ports: []corev1.ServicePort{
						{
							Name:       "foo",
							Port:       8080,
							Protocol:   corev1.ProtocolTCP,
							TargetPort: intstr.FromInt(8080),
						},
					},
				},
			},
			input: corev1.Service{
				Spec: corev1.ServiceSpec{
					Ports: []corev1.ServicePort{
						{
							Name: "foo",
							Port: 8080,
						},
					},
				},
			},
			expectedModified: false,
			expected: corev1.Service{
				Spec: corev1.ServiceSpec{
					Ports: []corev1.ServicePort{
						{
							Name:       "foo",
							Protocol:   corev1.ProtocolTCP,
							Port:       8080,
							TargetPort: intstr.FromInt(8080),
						},
					},
				},
			},
		},
		{
			name: "replace nodePort and targetPort, defaults to ProtocolTCP)",
			existing: corev1.Service{
				Spec: corev1.ServiceSpec{
					Ports: []corev1.ServicePort{
						{
							Name:       "foo",
							Port:       8080,
							TargetPort: intstr.FromInt(8081),
							NodePort:   8081,
							Protocol:   corev1.ProtocolTCP,
						},
					},
				},
			},
			input: corev1.Service{
				Spec: corev1.ServiceSpec{
					Ports: []corev1.ServicePort{
						{
							Name: "foo",
							Port: 8080,
						},
					},
				},
			},
			expectedModified: true,
			expected: corev1.Service{
				Spec: corev1.ServiceSpec{
					Ports: []corev1.ServicePort{
						{
							Name:       "foo",
							Protocol:   corev1.ProtocolTCP,
							Port:       8080,
							TargetPort: intstr.FromInt(8080),
						},
					},
				},
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			modified := pointer.BoolPtr(false)
			EnsureServicePorts(modified, &test.existing.Spec.Ports, test.input.Spec.Ports)
			if *modified != test.expectedModified {
				t.Errorf("mismatch modified got: %v want: %v", *modified, test.expectedModified)
			}

			if !equality.Semantic.DeepEqual(test.existing, test.expected) {
				t.Errorf("unexpected: %s", diff.ObjectReflectDiff(test.expected, test.existing))
			}
		})
	}
}

func TestEnsureServiceType(t *testing.T) {
	tests := []struct {
		name     string
		existing corev1.Service
		input    corev1.Service

		expectedModified bool
		expected         corev1.Service
	}{
		{
			name:     "required not set, empty existing",
			existing: corev1.Service{},
			input: corev1.Service{
				Spec: corev1.ServiceSpec{},
			},
			expectedModified: true,
			expected: corev1.Service{
				Spec: corev1.ServiceSpec{
					Type: "ClusterIP",
				},
			},
		},
		{
			name: "required not set, existing set to default",
			existing: corev1.Service{
				Spec: corev1.ServiceSpec{
					Type: "ClusterIP",
				},
			},
			input: corev1.Service{
				Spec: corev1.ServiceSpec{},
			},
			expectedModified: false,
			expected: corev1.Service{
				Spec: corev1.ServiceSpec{
					Type: "ClusterIP",
				},
			},
		},
		{
			name: "required not set, existing set to non-default",
			existing: corev1.Service{
				Spec: corev1.ServiceSpec{
					Type: "NodePort",
				},
			},
			input: corev1.Service{
				Spec: corev1.ServiceSpec{},
			},
			expectedModified: true,
			expected: corev1.Service{
				Spec: corev1.ServiceSpec{
					Type: "ClusterIP",
				},
			},
		},
		{
			name: "required matches existing",
			existing: corev1.Service{
				Spec: corev1.ServiceSpec{
					Type: "ClusterIP",
				},
			},
			input: corev1.Service{
				Spec: corev1.ServiceSpec{
					Type: "ClusterIP",
				},
			},
			expectedModified: false,
			expected: corev1.Service{
				Spec: corev1.ServiceSpec{
					Type: "ClusterIP",
				},
			},
		},
		{
			name: "required doesn't match existing",
			existing: corev1.Service{
				Spec: corev1.ServiceSpec{
					Type: "ClusterIP",
				},
			},
			input: corev1.Service{
				Spec: corev1.ServiceSpec{
					Type: "NodePort",
				},
			},
			expectedModified: true,
			expected: corev1.Service{
				Spec: corev1.ServiceSpec{
					Type: "NodePort",
				},
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			modified := pointer.BoolPtr(false)
			EnsureServiceType(modified, &test.existing.Spec.Type, test.input.Spec.Type)
			if *modified != test.expectedModified {
				t.Errorf("mismatch modified got: %v want: %v", *modified, test.expectedModified)
			}
			if test.existing.Spec.Type != test.expected.Spec.Type {
				t.Errorf("mismatch Spec.Type got: %s want: %s", test.existing.Spec.Type, test.expected.Spec.Type)
			}
		})
	}
}

func TestEnsureTolerations(t *testing.T) {
	tests := []struct {
		name     string
		existing []corev1.Toleration
		input    []corev1.Toleration

		expectedModified bool
		expected         []corev1.Toleration
	}{
		{
			name:             "required not set, empty existing",
			expectedModified: false,
		},
		{
			name: "required not set, existing has content",
			existing: []corev1.Toleration{{
				Key:      "node.kubernetes.io/unschedulable",
				Operator: corev1.TolerationOpExists,
				Effect:   corev1.TaintEffectNoSchedule,
			}},
			expectedModified: false,
			expected: []corev1.Toleration{{
				Key:      "node.kubernetes.io/unschedulable",
				Operator: corev1.TolerationOpExists,
				Effect:   corev1.TaintEffectNoSchedule,
			}},
		},
		{
			name: "required matches existing",
			existing: []corev1.Toleration{{
				Key:      "node.kubernetes.io/unschedulable",
				Operator: corev1.TolerationOpExists,
				Effect:   corev1.TaintEffectNoSchedule,
			}},
			input: []corev1.Toleration{{
				Key:      "node.kubernetes.io/unschedulable",
				Operator: corev1.TolerationOpExists,
				Effect:   corev1.TaintEffectNoSchedule,
			}},
			expectedModified: false,
			expected: []corev1.Toleration{{
				Key:      "node.kubernetes.io/unschedulable",
				Operator: corev1.TolerationOpExists,
				Effect:   corev1.TaintEffectNoSchedule,
			}},
		},
		{
			name: "required matches existing except for order",
			existing: []corev1.Toleration{{
				Key:      "node-role.kubernetes.io/master",
				Operator: corev1.TolerationOpExists,
				Effect:   corev1.TaintEffectNoSchedule,
			}, {
				Key:      "node.kubernetes.io/unschedulable",
				Operator: corev1.TolerationOpExists,
				Effect:   corev1.TaintEffectNoSchedule,
			}},
			input: []corev1.Toleration{{
				Key:      "node.kubernetes.io/unschedulable",
				Operator: corev1.TolerationOpExists,
				Effect:   corev1.TaintEffectNoSchedule,
			}, {
				Key:      "node-role.kubernetes.io/master",
				Operator: corev1.TolerationOpExists,
				Effect:   corev1.TaintEffectNoSchedule,
			}},
			expectedModified: false,
			expected: []corev1.Toleration{{
				Key:      "node-role.kubernetes.io/master",
				Operator: corev1.TolerationOpExists,
				Effect:   corev1.TaintEffectNoSchedule,
			}, {
				Key:      "node.kubernetes.io/unschedulable",
				Operator: corev1.TolerationOpExists,
				Effect:   corev1.TaintEffectNoSchedule,
			}},
		},
		{
			name: "required does not match existing",
			existing: []corev1.Toleration{{
				Key:      "node-role.kubernetes.io/master",
				Operator: corev1.TolerationOpExists,
				Effect:   corev1.TaintEffectNoSchedule,
			}, {
				Key:      "node.kubernetes.io/unschedulable",
				Operator: corev1.TolerationOpExists,
				Effect:   corev1.TaintEffectNoSchedule,
			}, {
				Key:      "node.kubernetes.io/network-unavailable",
				Operator: corev1.TolerationOpExists,
				Effect:   corev1.TaintEffectNoSchedule,
			}, {
				Key:               "node.kubernetes.io/not-ready",
				Operator:          corev1.TolerationOpExists,
				Effect:            corev1.TaintEffectNoExecute,
				TolerationSeconds: pointer.Int64Ptr(120),
			}, {
				Key:               "node.kubernetes.io/unreachable",
				Operator:          corev1.TolerationOpExists,
				Effect:            corev1.TaintEffectNoExecute,
				TolerationSeconds: pointer.Int64Ptr(120),
			}, {
				Key:               "node.kubernetes.io/not-ready",
				Operator:          corev1.TolerationOpExists,
				Effect:            corev1.TaintEffectNoExecute,
				TolerationSeconds: pointer.Int64Ptr(120),
			}},
			input: []corev1.Toleration{{
				Key:      "node-role.kubernetes.io/master",
				Operator: corev1.TolerationOpExists,
				Effect:   corev1.TaintEffectNoSchedule,
			}, {
				Key:      "node.kubernetes.io/unschedulable",
				Operator: corev1.TolerationOpExists,
				Effect:   corev1.TaintEffectNoSchedule,
			}, {
				Key:      "node.kubernetes.io/network-unavailable",
				Operator: corev1.TolerationOpExists,
				Effect:   corev1.TaintEffectNoSchedule,
			}, {
				Key:      "node.kubernetes.io/not-ready",
				Operator: corev1.TolerationOpExists,
				Effect:   corev1.TaintEffectNoSchedule,
			}, {
				Key:               "node.kubernetes.io/unreachable",
				Operator:          corev1.TolerationOpExists,
				Effect:            corev1.TaintEffectNoExecute,
				TolerationSeconds: pointer.Int64Ptr(120),
			}, {
				Key:               "node.kubernetes.io/not-ready",
				Operator:          corev1.TolerationOpExists,
				Effect:            corev1.TaintEffectNoExecute,
				TolerationSeconds: pointer.Int64Ptr(120),
			}},
			expectedModified: true,
			expected: []corev1.Toleration{{
				Key:      "node-role.kubernetes.io/master",
				Operator: corev1.TolerationOpExists,
				Effect:   corev1.TaintEffectNoSchedule,
			}, {
				Key:      "node.kubernetes.io/unschedulable",
				Operator: corev1.TolerationOpExists,
				Effect:   corev1.TaintEffectNoSchedule,
			}, {
				Key:      "node.kubernetes.io/network-unavailable",
				Operator: corev1.TolerationOpExists,
				Effect:   corev1.TaintEffectNoSchedule,
			}, {
				Key:               "node.kubernetes.io/not-ready",
				Operator:          corev1.TolerationOpExists,
				Effect:            corev1.TaintEffectNoExecute,
				TolerationSeconds: pointer.Int64Ptr(120),
			}, {
				Key:               "node.kubernetes.io/unreachable",
				Operator:          corev1.TolerationOpExists,
				Effect:            corev1.TaintEffectNoExecute,
				TolerationSeconds: pointer.Int64Ptr(120),
			}, {
				Key:      "node.kubernetes.io/not-ready",
				Operator: corev1.TolerationOpExists,
				Effect:   corev1.TaintEffectNoSchedule,
			}},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			modified := pointer.BoolPtr(false)
			ensureTolerations(modified, &test.existing, test.input)
			if *modified != test.expectedModified {
				t.Errorf("mismatch modified got: %v want: %v", *modified, test.expectedModified)
			}
			if !equality.Semantic.DeepEqual(test.existing, test.expected) {
				t.Errorf("unexpected: %s", diff.ObjectReflectDiff(test.expected, test.existing))
			}
		})
	}
}

func TestEnsureEnvVar(t *testing.T) {
	fieldRef := corev1.ObjectFieldSelector{
		APIVersion: "v1",
	}
	existingValueFrom := corev1.EnvVarSource{
		FieldRef: &fieldRef,
	}
	inputFieldRef := corev1.ObjectFieldSelector{}
	inputValueFrom := corev1.EnvVarSource{
		FieldRef: &inputFieldRef,
	}
	tests := []struct {
		name     string
		existing []corev1.EnvVar
		input    []corev1.EnvVar

		expectedModified bool
	}{
		{
			name: "required FieldRef not set",
			existing: []corev1.EnvVar{
				{ValueFrom: &existingValueFrom},
			},
			input: []corev1.EnvVar{
				{ValueFrom: &inputValueFrom},
			},
			expectedModified: false,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			modified := pointer.BoolPtr(false)

			ensureEnvVar(modified, &test.existing, test.input)
			if *modified != test.expectedModified {
				t.Errorf("mismatch modified got: %v want: %v", *modified, test.expectedModified)
			}
		})
	}
}

// Ensures the structure contains any defaults not explicitly set by the test
func defaultPodSpec(in *corev1.PodSpec, from corev1.PodSpec) {
	modified := pointer.BoolPtr(false)
	ensurePodSpec(modified, in, from)
}

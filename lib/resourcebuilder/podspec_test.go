package resourcebuilder

import (
	"reflect"
	"testing"

	"k8s.io/apimachinery/pkg/util/diff"

	corev1 "k8s.io/api/core/v1"
)

func TestUpgradePodSpecWithProxy(t *testing.T) {
	tests := []struct {
		name string

		input                          *corev1.PodSpec
		containerNames                 []string
		httpProxy, httpsProxy, noProxy string

		expectedErr string
		expected    *corev1.PodSpec
	}{
		{
			name: "no proxy info",
			input: &corev1.PodSpec{
				Containers: []corev1.Container{
					{
						Name: "foo",
					},
				},
			},
			expected: &corev1.PodSpec{
				Containers: []corev1.Container{
					{
						Name: "foo",
					},
				},
			},
		},
		{
			name:           "proxy info",
			containerNames: []string{"foo", "init-foo"},
			httpsProxy:     "httpsProxy-val",
			noProxy:        "noProxy-val",
			input: &corev1.PodSpec{
				InitContainers: []corev1.Container{
					{
						Name: "init-foo",
					},
					{
						Name: "init-bar",
					},
				},
				Containers: []corev1.Container{
					{
						Name: "foo",
					},
					{
						Name: "bar",
					},
				},
			},
			expected: &corev1.PodSpec{
				InitContainers: []corev1.Container{
					{
						Name: "init-foo",
						Env: []corev1.EnvVar{
							{Name: "HTTP_PROXY", Value: ""},
							{Name: "HTTPS_PROXY", Value: "httpsProxy-val"},
							{Name: "NO_PROXY", Value: "noProxy-val"},
						},
					},
					{
						Name: "init-bar",
					},
				},
				Containers: []corev1.Container{
					{
						Name: "foo",
						Env: []corev1.EnvVar{
							{Name: "HTTP_PROXY", Value: ""},
							{Name: "HTTPS_PROXY", Value: "httpsProxy-val"},
							{Name: "NO_PROXY", Value: "noProxy-val"},
						},
					},
					{
						Name: "bar",
					},
				},
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := upgradePodSpecWithProxy(test.input, test.containerNames, test.httpProxy, test.httpsProxy, test.noProxy)
			switch {
			case err == nil && len(test.expectedErr) == 0:
			case err != nil && len(test.expectedErr) == 0:
				t.Fatal(err)
			case err == nil && len(test.expectedErr) != 0:
				t.Fatal(err)
			case err != nil && len(test.expectedErr) != 0 && err.Error() != test.expectedErr:
				t.Fatal(err)
			}

			if !reflect.DeepEqual(test.input, test.expected) {
				t.Error(diff.ObjectDiff(test.input, test.expected))
			}
		})
	}
}

package cvo

import (
	"fmt"
	"reflect"
	"testing"

	configv1 "github.com/openshift/api/config/v1"
)

func TestFindDesiredUpdate(t *testing.T) {
	currentDigest := "sha256:b8307ac0f3ec4ac86c3f3b52846425205022da52c16f56ec31cbe428501001d6"
	currentRelease := configv1.Release{
		Version: "4.1.0",
		Image:   fmt.Sprintf("quay.io/openshift-release-dev/ocp-release@%s", currentDigest),
	}
	currentUpdate := configv1.Update{
		Version: currentRelease.Version,
		Image:   currentRelease.Image,
	}
	forcedCurrentUpdate := currentUpdate
	forcedCurrentUpdate.Force = true

	targetRelease := configv1.Release{
		Version: "4.1.1",
		Image:   "quay.io/openshift-release-dev/ocp-release@sha256:e9415dbf80988553adc6c34740243805a21d92e3cdedeb2fd8d743ca56522a61",
	}
	targetUpdate := configv1.Update{
		Version: targetRelease.Version,
		Image:   targetRelease.Image,
	}

	for _, testCase := range []struct {
		name           string
		clusterVersion *configv1.ClusterVersion
		expectedUpdate configv1.Update
		expectedError  error
	}{
		{
			name:           "nil ClusterVersion",
			expectedUpdate: currentUpdate,
		},
		{
			name:           "nil desiredUpdate",
			clusterVersion: &configv1.ClusterVersion{},
			expectedUpdate: currentUpdate,
		},
		{
			name: "empty desiredUpdate",
			clusterVersion: &configv1.ClusterVersion{
				Spec: configv1.ClusterVersionSpec{
					DesiredUpdate: &configv1.Update{},
				},
			},
			expectedUpdate: currentUpdate,
		},
		{
			name: "current image",
			clusterVersion: &configv1.ClusterVersion{
				Spec: configv1.ClusterVersionSpec{
					DesiredUpdate: &configv1.Update{
						Image: currentRelease.Image,
					},
				},
			},
			expectedUpdate: currentUpdate,
		},
		{
			name: "current version",
			clusterVersion: &configv1.ClusterVersion{
				Spec: configv1.ClusterVersionSpec{
					DesiredUpdate: &configv1.Update{
						Version: currentRelease.Version,
					},
				},
			},
			expectedUpdate: currentUpdate,
		},
		{
			name: "current image and version",
			clusterVersion: &configv1.ClusterVersion{
				Spec: configv1.ClusterVersionSpec{
					DesiredUpdate: &configv1.Update{
						Image:   currentRelease.Image,
						Version: currentRelease.Version,
					},
				},
			},
			expectedUpdate: currentUpdate,
		},
		{
			name: "current image and version, forced",
			clusterVersion: &configv1.ClusterVersion{
				Spec: configv1.ClusterVersionSpec{
					DesiredUpdate: &configv1.Update{
						Image:   currentRelease.Image,
						Version: currentRelease.Version,
						Force:   true,
					},
				},
			},
			expectedUpdate: forcedCurrentUpdate,
		},
		{
			name: "current image, different version",
			clusterVersion: &configv1.ClusterVersion{
				Spec: configv1.ClusterVersionSpec{
					DesiredUpdate: &configv1.Update{
						Image:   currentRelease.Image,
						Version: currentRelease.Version,
						Force:   true,
					},
				},
			},
			expectedUpdate: forcedCurrentUpdate,
		},
		{
			name: "current image digest",
			clusterVersion: &configv1.ClusterVersion{
				Spec: configv1.ClusterVersionSpec{
					DesiredUpdate: &configv1.Update{
						Image: fmt.Sprintf("mirror.example.com/openshift@%s", currentDigest),
					},
				},
			},
			expectedUpdate: configv1.Update{
				Image:   fmt.Sprintf("mirror.example.com/openshift@%s", currentDigest),
				Version: currentRelease.Version,
			},
		},
		{
			name: "image not in available updates",
			clusterVersion: &configv1.ClusterVersion{
				Spec: configv1.ClusterVersionSpec{
					DesiredUpdate: &configv1.Update{
						Image: targetRelease.Image,
					},
				},
			},
			expectedUpdate: configv1.Update{
				Image: targetRelease.Image,
			},
		},
		{
			name: "version not in available updates",
			clusterVersion: &configv1.ClusterVersion{
				Spec: configv1.ClusterVersionSpec{
					DesiredUpdate: &configv1.Update{
						Version: targetRelease.Version,
					},
				},
			},
			expectedUpdate: currentUpdate,
			expectedError:  fmt.Errorf(`no available updates found matching {"version":"4.1.1","image":"","force":false}`),
		},
		{
			name: "image with matched version, not in available updates",
			clusterVersion: &configv1.ClusterVersion{
				Spec: configv1.ClusterVersionSpec{
					DesiredUpdate: &configv1.Update{
						Image:   targetRelease.Image,
						Version: targetRelease.Version,
					},
				},
			},
			expectedUpdate: configv1.Update{
				Image:   targetRelease.Image,
				Version: targetRelease.Version,
			},
		},
		{
			name: "image with unmatched version, not in available updates",
			clusterVersion: &configv1.ClusterVersion{
				Spec: configv1.ClusterVersionSpec{
					DesiredUpdate: &configv1.Update{
						Image:   targetRelease.Image,
						Version: currentRelease.Version,
					},
				},
			},
			expectedUpdate: configv1.Update{
				Image:   targetRelease.Image,
				Version: currentRelease.Version,
			},
			// no error at this stage, but we will raise VerifyPayloadVersionFailed after pulling
		},
		{
			name: "image in available updates",
			clusterVersion: &configv1.ClusterVersion{
				Spec: configv1.ClusterVersionSpec{
					DesiredUpdate: &configv1.Update{
						Image: targetRelease.Image,
					},
				},
				Status: configv1.ClusterVersionStatus{
					AvailableUpdates: []configv1.Release{targetRelease},
				},
			},
			expectedUpdate: targetUpdate,
		},
		{
			name: "version in available updates",
			clusterVersion: &configv1.ClusterVersion{
				Spec: configv1.ClusterVersionSpec{
					DesiredUpdate: &configv1.Update{
						Version: targetRelease.Version,
					},
				},
				Status: configv1.ClusterVersionStatus{
					AvailableUpdates: []configv1.Release{targetRelease},
				},
			},
			expectedUpdate: targetUpdate,
		},
		{
			name: "image with matched version, in available updates",
			clusterVersion: &configv1.ClusterVersion{
				Spec: configv1.ClusterVersionSpec{
					DesiredUpdate: &configv1.Update{
						Image:   targetRelease.Image,
						Version: targetRelease.Version,
					},
				},
				Status: configv1.ClusterVersionStatus{
					AvailableUpdates: []configv1.Release{targetRelease},
				},
			},
			expectedUpdate: targetUpdate,
		},
		{
			name: "image with unmatched version, in available updates",
			clusterVersion: &configv1.ClusterVersion{
				Spec: configv1.ClusterVersionSpec{
					DesiredUpdate: &configv1.Update{
						Image:   targetRelease.Image,
						Version: currentRelease.Version,
					},
				},
				Status: configv1.ClusterVersionStatus{
					AvailableUpdates: []configv1.Release{targetRelease},
				},
			},
			expectedUpdate: configv1.Update{
				Image: targetRelease.Image,
			},
			expectedError: fmt.Errorf(`release version "4.1.1" does not match desired update "4.1.0"`),
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			update, err := findDesiredUpdate(testCase.clusterVersion, currentRelease)
			if !reflect.DeepEqual(err, testCase.expectedError) {
				t.Errorf("got %v, expected %v", err, testCase.expectedError)
			}
			if !reflect.DeepEqual(update, testCase.expectedUpdate) {
				t.Errorf("got %v, expected %v", update, testCase.expectedUpdate)
			}
		})
	}
}

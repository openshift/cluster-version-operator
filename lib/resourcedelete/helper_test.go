package resourcedelete

import (
	"testing"
	"time"
)

func TestValidDeleteAnnotation(t *testing.T) {
	tests := []struct {
		name      string
		annos     map[string]string
		wantValid bool
		wantErr   bool
	}{
		{name: "no delete annotation",
			annos:     map[string]string{"foo": "bar"},
			wantValid: false,
			wantErr:   false},
		{name: "no annotations",
			wantValid: false,
			wantErr:   false},
		{name: "valid delete annotation",
			annos:     map[string]string{"release.openshift.io/delete": "true"},
			wantValid: true,
			wantErr:   false},
		{name: "invalid delete annotation",
			annos:     map[string]string{"release.openshift.io/delete": "false"},
			wantValid: true,
			wantErr:   true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			valid, err := ValidDeleteAnnotation(test.annos)
			if (err != nil) != test.wantErr {
				t.Errorf("ValidDeleteAnnotation{} error = %v, wantErr %v", err, test.wantErr)
			}
			if valid != test.wantValid {
				t.Errorf("ValidDeleteAnnotation{} valid = %v, wantValid %v", valid, test.wantValid)
			}
		})
	}
}

func TestSetDeleteVerified(t *testing.T) {
	tests := []struct {
		name      string
		resource  Resource
		wantFound bool
	}{
		{name: "set delete verified",
			resource: Resource{Kind: "namespace",
				Namespace: "",
				Name:      "openshift-marketplace"},

			wantFound: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			SetDeleteVerified(test.resource)
			times, found := getDeleteTimes(test.resource)
			if found != test.wantFound {
				t.Errorf("SetDeleteVerified{} found = %v, wantFound %v", found, test.wantFound)
			}
			if times.Verified.IsZero() {
				t.Errorf("SetDeleteVerified{} resource's Verified time is zero")
			}
		})
	}
}

func TestDeletesInProgress(t *testing.T) {
	tests := []struct {
		name           string
		resources      []Resource
		delTime        deleteTimes
		wantInProgress []string
	}{
		{name: "2 deletes in progress",
			resources: []Resource{
				{Kind: "service",
					Namespace: "foo",
					Name:      "bar"},
				Resource{Kind: "deployment",
					Namespace: "openshift-cluster-version",
					Name:      "cvo"}},
			delTime: deleteTimes{
				Requested: time.Now()},
			wantInProgress: []string{"deployment \"openshift-cluster-version/cvo\"",
				"service \"foo/bar\""}},
		{name: "no deletes in progress",
			resources: []Resource{
				{Kind: "service",
					Namespace: "foo",
					Name:      "bar"},
				Resource{Kind: "deployment",
					Namespace: "openshift-cluster-version",
					Name:      "cvo"}},
			delTime: deleteTimes{
				Requested: time.Now(),
				Verified:  time.Now()}},
	}
	for _, test := range tests {
		for _, r := range test.resources {
			deletedResources.m[r] = test.delTime
		}
		t.Run(test.name, func(t *testing.T) {
			deletes := DeletesInProgress()
			if len(deletes) != len(test.wantInProgress) {
				t.Errorf("DeletesInProgress{} deletes in progress should be %d, found = %d", len(test.wantInProgress), len(deletes))
			}
			for _, s := range deletes {
				if s != test.wantInProgress[0] && s != test.wantInProgress[1] {
					t.Errorf("DeletesInProgress{} delete in progress %s not found. Should be either %s or %s.",
						s, test.wantInProgress[0], test.wantInProgress[1])
				}
			}
		})
	}
}

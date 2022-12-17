package precondition

import (
	"fmt"
	"testing"
)

func TestSummarize(t *testing.T) {
	tests := []struct {
		name          string
		errors        []error
		force         bool
		expectedBlock bool
		expectedError string
	}{{
		name:   "nil",
		errors: nil,
	}, {
		name:   "empty error slice",
		errors: []error{},
	}, {
		name:          "unrecognized error type",
		errors:        []error{fmt.Errorf("random error")},
		expectedBlock: true,
		expectedError: "random error",
	}, {
		name:          "forced unrecognized error type",
		errors:        []error{fmt.Errorf("random error")},
		force:         true,
		expectedBlock: false,
		expectedError: "Forced through blocking failures: random error",
	}, {
		name: "unforced warning",
		errors: []error{&Error{
			Nested:             nil,
			Reason:             "UnknownUpdate",
			Message:            "update from A to B is probably neither recommended nor supported.",
			NonBlockingWarning: true,
			Name:               "ClusterVersionRecommendedUpdate",
		}},
		expectedBlock: false,
		expectedError: `Precondition "ClusterVersionRecommendedUpdate" failed because of "UnknownUpdate": update from A to B is probably neither recommended nor supported.`,
	}, {
		name: "forced through warning",
		errors: []error{&Error{
			Nested:             nil,
			Reason:             "UnknownUpdate",
			Message:            "update from A to B is probably neither recommended nor supported.",
			NonBlockingWarning: true,
			Name:               "ClusterVersionRecommendedUpdate",
		}},
		force:         true,
		expectedError: `Precondition "ClusterVersionRecommendedUpdate" failed because of "UnknownUpdate": update from A to B is probably neither recommended nor supported.`,
	}, {
		name: "single feature-gate error",
		errors: []error{&Error{
			Nested:  nil,
			Reason:  "NotAllowedFeatureGateSet",
			Message: "Feature Gate random is set for the cluster. This Feature Gate turns on features that are not part of the normal supported platform.",
			Name:    "FeatureGate",
		}},
		expectedBlock: true,
		expectedError: `Precondition "FeatureGate" failed because of "NotAllowedFeatureGateSet": Feature Gate random is set for the cluster. This Feature Gate turns on features that are not part of the normal supported platform.`,
	}, {
		name:          "two unrecognized error types",
		errors:        []error{fmt.Errorf("random error"), fmt.Errorf("random error 2")},
		expectedBlock: true,
		expectedError: `Multiple precondition checks failed:
* random error
* random error 2`,
	}, {
		name: "two feature gate errors",
		errors: []error{&Error{
			Nested:  nil,
			Reason:  "NotAllowedFeatureGateSet",
			Message: "Feature Gate random is set for the cluster. This Feature Gate turns on features that are not part of the normal supported platform.",
			Name:    "FeatureGate",
		}, &Error{
			Nested:  nil,
			Reason:  "NotAllowedFeatureGateSet",
			Message: "Feature Gate random-2 is set for the cluster. This Feature Gate turns on features that are not part of the normal supported platform.",
			Name:    "FeatureGate",
		}},
		expectedBlock: true,
		expectedError: `Multiple precondition checks failed:
* Precondition "FeatureGate" failed because of "NotAllowedFeatureGateSet": Feature Gate random is set for the cluster. This Feature Gate turns on features that are not part of the normal supported platform.
* Precondition "FeatureGate" failed because of "NotAllowedFeatureGateSet": Feature Gate random-2 is set for the cluster. This Feature Gate turns on features that are not part of the normal supported platform.`,
	}, {
		name: "unrecognized type and a feature-gate error",
		errors: []error{
			fmt.Errorf("random error"),
			&Error{
				Nested:  nil,
				Reason:  "NotAllowedFeatureGateSet",
				Message: "Feature Gate random is set for the cluster. This Feature Gate turns on features that are not part of the normal supported platform.",
				Name:    "FeatureGate",
			}},
		expectedBlock: true,
		expectedError: `Multiple precondition checks failed:
* random error
* Precondition "FeatureGate" failed because of "NotAllowedFeatureGateSet": Feature Gate random is set for the cluster. This Feature Gate turns on features that are not part of the normal supported platform.`,
	}}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			block, err := Summarize(test.errors, test.force)

			if block != test.expectedBlock {
				t.Errorf("expected block %t, but got %t", test.expectedBlock, block)
			}

			if test.expectedError == "" {
				if err != nil {
					t.Fatalf("expected no error, got %v", err)
				}
			} else {
				if err == nil {
					t.Fatalf("expected err %s, got nil", test.expectedError)
				} else if err.Error() != test.expectedError {
					t.Fatalf("expected err %s, got %s", test.expectedError, err.Error())
				}
			}
		})
	}
}

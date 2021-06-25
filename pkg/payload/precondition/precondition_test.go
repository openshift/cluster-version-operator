package precondition

import (
	"fmt"
	"testing"
)

func TestSummarize(t *testing.T) {
	tests := []struct {
		input []error
		exp   string
	}{{
		input: nil,
	}, {
		input: []error{},
	}, {
		input: []error{fmt.Errorf("random error")},
		exp:   "random error",
	}, {
		input: []error{&Error{
			Nested:  nil,
			Reason:  "NotAllowedFeatureGateSet",
			Message: "Feature Gate random is set for the cluster. This Feature Gate turns on features that are not part of the normal supported platform.",
			Name:    "FeatureGate",
		}},
		exp: `Precondition "FeatureGate" failed because of "NotAllowedFeatureGateSet": Feature Gate random is set for the cluster. This Feature Gate turns on features that are not part of the normal supported platform.`,
	}, {
		input: []error{fmt.Errorf("random error"), fmt.Errorf("random error 2")},
		exp: `Multiple precondition checks failed:
* random error
* random error 2`,
	}, {
		input: []error{&Error{
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
		exp: `Multiple precondition checks failed:
* Precondition "FeatureGate" failed because of "NotAllowedFeatureGateSet": Feature Gate random is set for the cluster. This Feature Gate turns on features that are not part of the normal supported platform.
* Precondition "FeatureGate" failed because of "NotAllowedFeatureGateSet": Feature Gate random-2 is set for the cluster. This Feature Gate turns on features that are not part of the normal supported platform.`,
	}, {
		input: []error{
			fmt.Errorf("random error"),
			&Error{
				Nested:  nil,
				Reason:  "NotAllowedFeatureGateSet",
				Message: "Feature Gate random is set for the cluster. This Feature Gate turns on features that are not part of the normal supported platform.",
				Name:    "FeatureGate",
			}},
		exp: `Multiple precondition checks failed:
* random error
* Precondition "FeatureGate" failed because of "NotAllowedFeatureGateSet": Feature Gate random is set for the cluster. This Feature Gate turns on features that are not part of the normal supported platform.`,
	}}
	for _, test := range tests {
		t.Run("", func(t *testing.T) {
			err := Summarize(test.input)
			if test.exp == "" {
				if err != nil {
					t.Fatalf("expected no error, got %v", err)
				}
			} else {
				if err == nil {
					t.Fatalf("expected err %s, got nil", test.exp)
				} else if err.Error() != test.exp {
					t.Fatalf("expected err %s, got %s", test.exp, err.Error())
				}
			}
		})
	}
}

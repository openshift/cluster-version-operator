package cvo

import (
	"testing"

	configv1 "github.com/openshift/api/config/v1"
)

func TestGetAgenticRunUpdates(t *testing.T) {
	optr := &Operator{}
	if _, _, err := optr.getAgenticRunUpdates(); err == nil {
		t.Fatal("uninitialized updates must defer AgenticRun reconciliation")
	}
	optr.availableUpdates = &availableUpdates{}
	if _, _, err := optr.getAgenticRunUpdates(); err != nil {
		t.Fatalf("initialized empty updates: %v", err)
	}
	optr.availableUpdates.Updates = []configv1.Release{{Version: "4.16.0"}}
	optr.availableUpdates.ConditionalUpdates = []configv1.ConditionalUpdate{{Release: configv1.Release{Version: "4.16.1"}}}
	updates, conditional, err := optr.getAgenticRunUpdates()
	if err != nil || len(updates) != 1 || len(conditional) != 1 {
		t.Fatalf("updates=%v conditional=%v err=%v", updates, conditional, err)
	}
}

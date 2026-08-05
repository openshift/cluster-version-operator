package cvo

import (
	"fmt"
	"regexp"
	"strings"
	"testing"

	configv1 "github.com/openshift/api/config/v1"

	"github.com/openshift/cluster-version-operator/pkg/payload"
)

func TestSyncWorkerStatus_String(t *testing.T) {
	hexAddr := regexp.MustCompile(`0x[0-9a-fA-F]+`)

	t.Run("nil failures", func(t *testing.T) {
		got := SyncWorkerStatus{}.String()
		if strings.Contains(got, "0x") && hexAddr.MatchString(got) {
			t.Fatalf("zero-value String() should not dump pointer addresses: %s", got)
		}
		if !strings.Contains(got, `failure="<none>"`) {
			t.Fatalf("expected nil Failure as <none>: %s", got)
		}
		if !strings.Contains(got, `loadPayloadFailure="<none>"`) {
			t.Fatalf("expected nil loadPayload Failure as <none>: %s", got)
		}
	})

	t.Run("apply sync failure", func(t *testing.T) {
		msg := `Could not update deployment "openshift-cluster-version/cluster-version-operator" (33 of 33): timed out waiting for the condition`
		status := SyncWorkerStatus{
			Generation:  4,
			Failure:     &payload.UpdateError{Reason: "UpdatePayloadResourceFailed", Message: msg},
			Done:        3,
			Total:       33,
			Completed:   0,
			Reconciling: false,
			Initial:     true,
			Actual: configv1.Release{
				Version: "4.14.0-0.nightly-2023-05-28-215458",
				Image:   "registry.ci.openshift.org/ocp/release@sha256:abc",
			},
			loadPayloadStatus: LoadPayloadStatus{Step: "PayloadLoaded"},
		}
		got := status.String()
		if !strings.Contains(got, "Could not update deployment") || !strings.Contains(got, "timed out waiting for the condition") {
			t.Fatalf("missing apply failure message:\n%s", got)
		}
		if !strings.Contains(got, `generation=4`) || !strings.Contains(got, `done=3/33`) {
			t.Fatalf("missing scalar fields:\n%s", got)
		}
		if !strings.Contains(got, `loadPayloadStep="PayloadLoaded"`) {
			t.Fatalf("missing loadPayloadStep:\n%s", got)
		}
		if hexAddr.MatchString(got) {
			t.Fatalf("String() must not contain pointer addresses:\n%s", got)
		}
		// Contrast with %#v, which is the old buggy log format.
		bad := fmt.Sprintf("%#v", &status)
		if !strings.Contains(bad, "(*payload.UpdateError)") {
			t.Fatalf("test assumption failed: %%#v should still dump UpdateError as a pointer:\n%s", bad)
		}
	})

	t.Run("payload load failure", func(t *testing.T) {
		loadMsg := `Retrieving payload failed version="4.14.0" image="img" failure=no such host`
		status := SyncWorkerStatus{
			Generation: 5,
			Failure:    nil,
			Actual:     configv1.Release{Version: "4.14.0", Image: "img"},
			loadPayloadStatus: LoadPayloadStatus{
				Step:    "RetrievePayload",
				Message: loadMsg,
				Failure: fmt.Errorf("%s", loadMsg),
			},
		}
		got := status.String()
		if !strings.Contains(got, `failure="<none>"`) {
			t.Fatalf("expected apply Failure <none>:\n%s", got)
		}
		if !strings.Contains(got, `loadPayloadStep="RetrievePayload"`) {
			t.Fatalf("missing loadPayloadStep:\n%s", got)
		}
		if !strings.Contains(got, `loadPayloadFailure="`) || !strings.Contains(got, "Retrieving payload failed") || !strings.Contains(got, "no such host") {
			t.Fatalf("missing loadPayloadFailure text:\n%s", got)
		}
		if hexAddr.MatchString(got) {
			t.Fatalf("String() must not contain pointer addresses:\n%s", got)
		}
	})
}

func TestLoadPayloadStatus_String(t *testing.T) {
	got := LoadPayloadStatus{
		Step:    "VerifyPayloadVersion",
		Message: "verifying",
		Failure: fmt.Errorf("version mismatch"),
	}.String()
	wantSub := `step="VerifyPayloadVersion" message="verifying" failure="version mismatch"`
	if got != wantSub {
		t.Fatalf("got %q, want %q", got, wantSub)
	}
	if (LoadPayloadStatus{}).String() != `step="" message="" failure="<none>"` {
		t.Fatalf("unexpected zero value: %s", (LoadPayloadStatus{}).String())
	}
}

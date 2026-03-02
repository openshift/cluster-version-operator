package cvo

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	//nolint:staticcheck // verify,Verifier from openshift/library-go uses a type from this deprecated package (needs to be addressed there)
	"golang.org/x/crypto/openpgp"

	configv1 "github.com/openshift/api/config/v1"
	"github.com/openshift/library-go/pkg/verify/store"

	"github.com/openshift/cluster-version-operator/pkg/payload"
)

// EquateErrorMessage reports errors to be equal if both are nil
// or both have the same message
var EquateErrorMessage = cmp.FilterValues(func(x, y interface{}) bool {
	_, ok1 := x.(error)
	_, ok2 := y.(error)
	return ok1 && ok2
}, cmp.Comparer(func(x, y interface{}) bool {
	xe := x.(error)
	ye := y.(error)
	if xe == nil || ye == nil {
		return xe == nil && ye == nil
	}
	return xe.Error() == ye.Error()
}))

// mockVerifier implements verify.Verifier
type mockVerifier struct {
	t *testing.T

	expectNoVerify     bool
	expectVerifyDigest string
	expectVerifyCancel bool
	verifyReturns      error
}

func (m *mockVerifier) Verify(ctx context.Context, releaseDigest string) error {
	if m.expectNoVerify {
		m.t.Errorf("Unexpected call: Verify(releaseDigest=%s)", releaseDigest)
	}
	if !m.expectNoVerify && m.expectVerifyDigest != releaseDigest {
		m.t.Errorf("Verify() called with unexpected value: %v", releaseDigest)
	}

	timeout, timeoutCancel := context.WithTimeout(context.Background(), backstopDuration)
	defer timeoutCancel()

	if m.expectVerifyCancel {
		select {
		case <-ctx.Done():
		case <-timeout.Done():
			m.t.Errorf("Verify() expected to be cancelled by context but it was not")
		}
	} else {
		select {
		case <-ctx.Done():
			m.t.Errorf("Unexpected ctx cancel in Verify()")
		default:
		}
	}

	return m.verifyReturns
}
func (m *mockVerifier) Signatures() map[string][][]byte          { return nil }
func (m *mockVerifier) Verifiers() map[string]openpgp.EntityList { return nil }
func (m *mockVerifier) AddStore(_ store.Store)                   {}

type downloadMocker struct {
	expectCancel   bool
	duration       time.Duration
	returnLocation string
	returnErr      error
}

const (
	// backstopDuration is a maximum duration for which we wait on a tested operation
	backstopDuration = 5 * time.Second
	// hangs represents a "hanging" operation, always preempted by backstop
	hangs = 2 * backstopDuration
)

func (d *downloadMocker) make(t *testing.T) downloadFunc {
	if d == nil {
		return func(_ context.Context, update configv1.Update) (string, error) {
			t.Errorf("Unexpected call: downloader(<ctx>, upddate=%v", update)
			return "", nil
		}
	}
	return func(ctx context.Context, _ configv1.Update) (string, error) {
		backstopCtx, backstopCancel := context.WithTimeout(context.Background(), backstopDuration)
		defer backstopCancel()

		downloadCtx, downloadCancel := context.WithTimeout(context.Background(), d.duration)
		defer downloadCancel()

		if d.expectCancel {
			select {
			case <-backstopCtx.Done():
				t.Errorf("downloader: test backstop hit (expected cancel via ctx)")
				return "", errors.New("downloader: test backstop hit (expected cancel via ctx)")
			case <-downloadCtx.Done():
				t.Errorf("downloader: download finished (expected cancel via ctx)")
				return "/some/location", errors.New("downloader: download finished (expected cancel via ctx)")
			case <-ctx.Done():
			}
		} else {
			select {
			case <-backstopCtx.Done():
				t.Errorf("downloader: test backstop hit (expected download to finish)")
				return "", errors.New("downloader: test backstop hit (expected download to finish)")
			case <-ctx.Done():
				t.Errorf("downloader: unexpected ctx cancel (expected download to finish)")
				return "", errors.New("downloader: unexpected ctx cancel (expected download to finish)")
			case <-downloadCtx.Done():
			}
		}

		return d.returnLocation, d.returnErr
	}
}

func TestPayloadRetrieverRetrievePayload(t *testing.T) {
	t.Parallel()
	testCases := []struct {
		name       string
		verifier   *mockVerifier
		downloader *downloadMocker
		update     configv1.Update
		ctxTimeout time.Duration

		expected    PayloadInfo
		expectedErr error
	}{
		{
			name:     "when desired image matches retriever image then return local payload directory",
			verifier: &mockVerifier{expectNoVerify: true},
			update:   configv1.Update{Image: "releaseImage"},
			expected: PayloadInfo{
				Directory: "/local/payload/dir",
				Local:     true,
			},
		},
		{
			name:        "when desired image is empty then return error",
			verifier:    &mockVerifier{expectNoVerify: true},
			update:      configv1.Update{},
			expectedErr: errors.New("no payload image has been specified and the contents of the payload cannot be retrieved"),
		},
		{
			name:        "when desired image is tag pullspec and passes verification but fails to download then return error",
			verifier:    &mockVerifier{expectVerifyDigest: ""},
			downloader:  &downloadMocker{returnErr: errors.New("fails to download")},
			update:      configv1.Update{Image: "quay.io/openshift-release-dev/ocp-release:failing"},
			expectedErr: errors.New("Unable to download and prepare the update: fails to download"),
		},
		{
			name: "when desired image is tag pullspec and fails verification then return error",
			verifier: &mockVerifier{
				expectVerifyDigest: "",
				verifyReturns:      errors.New("fails-verification"),
			},
			update:      configv1.Update{Image: "quay.io/openshift-release-dev/ocp-release:failing"},
			expectedErr: errors.New("The update cannot be verified: fails-verification"),
		},
		{
			name:        "when desired image is sha digest pullspec and passes verification but fails to download then return error",
			verifier:    &mockVerifier{expectVerifyDigest: "sha256:08ef16270e643a001454410b22864db6246d782298be267688a4433d83f404f4"},
			downloader:  &downloadMocker{returnErr: errors.New("fails to download")},
			update:      configv1.Update{Image: "quay.io/openshift-release-dev/ocp-release@sha256:08ef16270e643a001454410b22864db6246d782298be267688a4433d83f404f4"},
			expectedErr: errors.New("Unable to download and prepare the update: fails to download"),
		},
		{
			name: "when sha digest pullspec image fails verification but update is forced then retrieval proceeds then when download fails then return error",
			verifier: &mockVerifier{
				expectVerifyDigest: "sha256:08ef16270e643a001454410b22864db6246d782298be267688a4433d83f404f4",
				verifyReturns:      errors.New("fails-to-verify"),
			},
			downloader: &downloadMocker{returnErr: errors.New("fails to download")},
			update: configv1.Update{
				Force: true,
				Image: "quay.io/openshift-release-dev/ocp-release@sha256:08ef16270e643a001454410b22864db6246d782298be267688a4433d83f404f4",
			},
			expectedErr: errors.New("Unable to download and prepare the update: fails to download"),
		},
		{
			name: "when sha digest pullspec image is timing out verification with unlimited context and update is forced " +
				"then verification times out promptly and retrieval proceeds but download fails then return error",
			verifier: &mockVerifier{
				expectVerifyDigest: "sha256:08ef16270e643a001454410b22864db6246d782298be267688a4433d83f404f4",
				expectVerifyCancel: true,
				verifyReturns:      errors.New("fails-to-verify"),
			},
			downloader: &downloadMocker{returnErr: errors.New("fails to download")},
			update: configv1.Update{
				Force: true,
				Image: "quay.io/openshift-release-dev/ocp-release@sha256:08ef16270e643a001454410b22864db6246d782298be267688a4433d83f404f4",
			},
			expectedErr: errors.New("Unable to download and prepare the update: fails to download"),
		},
		{
			name: "when sha digest pullspec image is timing out verification with long deadline context and update is forced " +
				"then verification times out promptly, retrieval proceeds but download fails then return error",
			verifier: &mockVerifier{
				expectVerifyDigest: "sha256:08ef16270e643a001454410b22864db6246d782298be267688a4433d83f404f4",
				expectVerifyCancel: true,
				verifyReturns:      errors.New("fails-to-verify"),
			},
			downloader: &downloadMocker{returnErr: errors.New("fails to download")},
			update: configv1.Update{
				Force: true,
				Image: "quay.io/openshift-release-dev/ocp-release@sha256:08ef16270e643a001454410b22864db6246d782298be267688a4433d83f404f4",
			},
			ctxTimeout:  backstopDuration - time.Second,
			expectedErr: errors.New("Unable to download and prepare the update: fails to download"),
		},
		{
			name: "when sha digest pullspec image is timing out verification with long deadline context and update is forced " +
				"then verification times out promptly, retrieval proceeds but download times out then return error",
			verifier: &mockVerifier{
				expectVerifyDigest: "sha256:08ef16270e643a001454410b22864db6246d782298be267688a4433d83f404f4",
				expectVerifyCancel: true,
				verifyReturns:      errors.New("fails-to-verify"),
			},
			downloader: &downloadMocker{
				expectCancel: true,
				duration:     backstopDuration - (2 * time.Second),
				returnErr:    errors.New("fails to download"),
			},
			update: configv1.Update{
				Force: true,
				Image: "quay.io/openshift-release-dev/ocp-release@sha256:08ef16270e643a001454410b22864db6246d782298be267688a4433d83f404f4",
			},
			ctxTimeout:  backstopDuration - time.Second,
			expectedErr: errors.New("Unable to download and prepare the update: fails to download"),
		},
		{
			name: "when sha digest pullspec image fails verification but update is forced then retrieval proceeds and download succeeds then return info with location and verification error",
			verifier: &mockVerifier{
				expectVerifyDigest: "sha256:08ef16270e643a001454410b22864db6246d782298be267688a4433d83f404f4",
				verifyReturns:      errors.New("fails-to-verify"),
			},
			downloader: &downloadMocker{returnLocation: "/location/of/download"},
			update: configv1.Update{
				Force: true,
				Image: "quay.io/openshift-release-dev/ocp-release@sha256:08ef16270e643a001454410b22864db6246d782298be267688a4433d83f404f4",
			},
			expected: PayloadInfo{
				Directory: "/location/of/download",
				VerificationError: &payload.UpdateError{
					Reason:  "ImageVerificationFailed",
					Message: `Target release version="" image="quay.io/openshift-release-dev/ocp-release@sha256:08ef16270e643a001454410b22864db6246d782298be267688a4433d83f404f4" cannot be verified, but continuing anyway because the update was forced: fails-to-verify`,
				},
			},
		},
		{
			name:     "when sha digest pullspec image passes and download hangs then it is terminated and returns error (RHBZ#2090680)",
			verifier: &mockVerifier{expectVerifyDigest: "sha256:08ef16270e643a001454410b22864db6246d782298be267688a4433d83f404f4"},
			downloader: &downloadMocker{
				duration:     hangs,
				expectCancel: true,
				returnErr:    errors.New("download was canceled"),
			},
			update: configv1.Update{
				Image: "quay.io/openshift-release-dev/ocp-release@sha256:08ef16270e643a001454410b22864db6246d782298be267688a4433d83f404f4",
			},
			expectedErr: errors.New("Unable to download and prepare the update: download was canceled"),
		},
		{
			name: "when sha digest pullspec image fails to verify until timeout but is forced then it allows enough time for download and it returns successfully",
			verifier: &mockVerifier{
				expectVerifyDigest: "sha256:08ef16270e643a001454410b22864db6246d782298be267688a4433d83f404f4",
				expectVerifyCancel: true,
				verifyReturns:      errors.New("fails-to-verify"),
			},
			downloader: &downloadMocker{
				duration:       300 * time.Millisecond,
				returnLocation: "/location/of/download",
			},
			update: configv1.Update{
				Force: true,
				Image: "quay.io/openshift-release-dev/ocp-release@sha256:08ef16270e643a001454410b22864db6246d782298be267688a4433d83f404f4",
			},
			expected: PayloadInfo{
				Directory: "/location/of/download",
				VerificationError: &payload.UpdateError{
					Reason:  "ImageVerificationFailed",
					Message: `Target release version="" image="quay.io/openshift-release-dev/ocp-release@sha256:08ef16270e643a001454410b22864db6246d782298be267688a4433d83f404f4" cannot be verified, but continuing anyway because the update was forced: fails-to-verify`,
				},
			},
		},
		{
			name:       "when sha digest pullspec image passes and download succeeds then returns location and no error",
			verifier:   &mockVerifier{expectVerifyDigest: "sha256:08ef16270e643a001454410b22864db6246d782298be267688a4433d83f404f4"},
			downloader: &downloadMocker{returnLocation: "/location/of/download"},
			update: configv1.Update{
				Image: "quay.io/openshift-release-dev/ocp-release@sha256:08ef16270e643a001454410b22864db6246d782298be267688a4433d83f404f4",
			},
			expected: PayloadInfo{
				Directory: "/location/of/download",
				Verified:  true,
			},
		},
	}
	for _, tc := range testCases {
		tc := tc // prevent parallel closures from sharing a single tc copy
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			retriever := payloadRetriever{
				releaseImage:    "releaseImage",
				payloadDir:      "/local/payload/dir",
				retrieveTimeout: 2 * time.Second,
			}

			if tc.verifier != nil {
				tc.verifier.t = t
				retriever.verifier = tc.verifier
			}

			if tc.downloader != nil {
				retriever.downloader = tc.downloader.make(t)
			}

			ctx := context.Background()
			if tc.ctxTimeout > 0 {
				var cancel context.CancelFunc
				ctx, cancel = context.WithTimeout(ctx, tc.ctxTimeout)
				defer cancel()
			}

			actual, err := retriever.RetrievePayload(ctx, tc.update)
			if diff := cmp.Diff(tc.expectedErr, err, EquateErrorMessage); diff != "" {
				t.Errorf("Returned error differs from expected:\n%s", diff)
			}
			if diff := cmp.Diff(tc.expected, actual, cmpopts.IgnoreFields(payload.UpdateError{}, "Nested")); err == nil && diff != "" {
				t.Errorf("Returned PayloadInfo differs from expected:\n%s", diff)
			}
		})
	}
}

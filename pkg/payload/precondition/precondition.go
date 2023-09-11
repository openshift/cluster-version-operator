package precondition

import (
	"context"
	"fmt"
	"strings"

	"k8s.io/klog/v2"

	"github.com/openshift/cluster-version-operator/pkg/payload"
)

// Error is a wrapper for errors that occur during a precondition check for payload.
type Error struct {
	Nested             error
	Reason             string
	Message            string
	Name               string
	NonBlockingWarning bool // For some errors we do not want to fail the precondition check but we want to communicate about it
}

// Error returns the message
func (e *Error) Error() string {
	return e.Message
}

// Cause returns the nested error.
func (e *Error) Cause() error {
	return e.Nested
}

// ReleaseContext holds information about the update being considered
type ReleaseContext struct {
	// DesiredVersion is the version of the payload being considered.
	// While this might be a semantic version, consumers should not
	// require SemVer validity so they can handle custom releases
	// where the author decided to use a different naming scheme, or
	// to leave the version completely unset.
	DesiredVersion string

        // Previous is a slice of valid previous versions.
        Previous []string
}

// Precondition defines the precondition check for a payload.
type Precondition interface {
	// Run executes the precondition checks ands returns an error when the precondition fails.
	Run(ctx context.Context, releaseContext ReleaseContext) error

	// Name returns a human friendly name for the precondition.
	Name() string
}

// List is a list of precondition checks.
type List []Precondition

// RunAll runs all the reflight checks in order, returning a list of errors if any.
// All checks are run, regardless if any one precondition fails.
func (pfList List) RunAll(ctx context.Context, releaseContext ReleaseContext) []error {
	var errs []error
	for _, pf := range pfList {
		if err := pf.Run(ctx, releaseContext); err != nil {
			klog.Errorf("Precondition %q failed: %v", pf.Name(), err)
			errs = append(errs, err)
		}
	}
	return errs
}

// Summarize summarizes all the precondition.Error from errs.
// Returns the consolidated error and a boolean for whether the error
// is blocking (true) or a warning (false).
func Summarize(errs []error, force bool) (bool, error) {
	if len(errs) == 0 {
		return false, nil
	}
	var msgs []string
	var isWarning = true
	for _, e := range errs {
		if pferr, ok := e.(*Error); ok {
			msgs = append(msgs, fmt.Sprintf("Precondition %q failed because of %q: %v", pferr.Name, pferr.Reason, pferr.Error()))
			if !pferr.NonBlockingWarning {
				isWarning = false
			}
			continue
		}
		isWarning = false
		msgs = append(msgs, e.Error())

	}
	msg := ""
	if len(msgs) == 1 {
		msg = msgs[0]
	} else {
		msg = fmt.Sprintf("Multiple precondition checks failed:\n* %s", strings.Join(msgs, "\n* "))
	}

	if force && !isWarning {
		msg = fmt.Sprintf("Forced through blocking failures: %s", msg)
		isWarning = true
	}

	return !isWarning, &payload.UpdateError{
		Nested:  nil,
		Reason:  "UpgradePreconditionCheckFailed",
		Message: msg,
		Name:    "PreconditionCheck",
	}
}

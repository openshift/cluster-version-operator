// Package mock implements a cluster condition with mock responses,
// for convenient testing.
package mock

import (
	"context"
	"errors"
	"time"

	configv1 "github.com/openshift/api/config/v1"
)

// MatchResult represents the response to a single Match invocation.
type MatchResult struct {
	// Match is whether the result was a match or not.
	Match bool

	// Error is the error returned by the Match call.
	Error error
}

// Call records a call to a cluster condition method.
type Call struct {
	// When records the time of the method call.
	When time.Time

	// Method records the method name.
	Method string

	// Condition records the condition configuration passed to the method call.
	Condition *configv1.ClusterCondition
}

// Mock implements a cluster condition with mock responses.
type Mock struct {
	// ValidQueue is a set of responses queued for Valid calls.
	ValidQueue []error

	// MatchQueue is a set of responses queued for Match calls.
	MatchQueue []MatchResult

	// Calls records calls to the cluster condition.
	Calls []Call
}

// Valid returns an error popped from ValidQueue.
func (m *Mock) Valid(_ context.Context, condition *configv1.ClusterCondition) error {
	m.Calls = append(m.Calls, Call{
		When:      time.Now(),
		Method:    "Valid",
		Condition: condition.DeepCopy(),
	})

	if len(m.ValidQueue) == 0 {
		return errors.New("the mock's ValidQueue stack is empty")
	}

	result := m.ValidQueue[0]
	m.ValidQueue = m.ValidQueue[1:]
	return result
}

// Match returns an error popped from MatchQueue.
func (m *Mock) Match(_ context.Context, condition *configv1.ClusterCondition) (bool, error) {
	m.Calls = append(m.Calls, Call{
		When:      time.Now(),
		Method:    "Match",
		Condition: condition.DeepCopy(),
	})

	if len(m.MatchQueue) == 0 {
		return false, errors.New("the mock's MatchQueue stack is empty")
	}

	result := m.MatchQueue[0]
	m.MatchQueue = m.MatchQueue[1:]
	return result.Match, result.Error
}

package mock_test

import (
	"context"
	"errors"
	"reflect"
	"regexp"
	"testing"
	"time"

	configv1 "github.com/openshift/api/config/v1"

	"github.com/openshift/cluster-version-operator/pkg/clusterconditions/mock"
)

func TestMock(t *testing.T) {
	ctx := context.Background()
	m := &mock.Mock{}

	validTestCases := []struct {
		condition     *configv1.ClusterCondition
		pushErrors    []error
		expectedError *regexp.Regexp
	}{
		{
			condition:  nil,
			pushErrors: []error{nil},
		},
		{
			condition:  &configv1.ClusterCondition{Type: "Valid() call 1"},
			pushErrors: []error{nil},
		},
		{
			condition:     &configv1.ClusterCondition{Type: "Valid() call 2"},
			pushErrors:    []error{errors.New("error a")},
			expectedError: regexp.MustCompile("^error a$"),
		},
		{
			condition:     &configv1.ClusterCondition{Type: "Valid() call 3"},
			pushErrors:    []error{errors.New("error b")},
			expectedError: regexp.MustCompile("^error b$"),
		},
		{
			condition:     &configv1.ClusterCondition{Type: "Valid() Call with empty queue"},
			expectedError: regexp.MustCompile("^the mock's ValidQueue stack is empty$"),
		},
	}

	for i := range validTestCases {
		m.ValidQueue = append(m.ValidQueue, validTestCases[i].pushErrors...)
	}

	for _, testCase := range validTestCases {
		name := "nil condition"
		if testCase.condition != nil {
			name = testCase.condition.Type
		}
		t.Run(name, func(t *testing.T) {
			before := time.Now()
			err := m.Valid(ctx, testCase.condition)
			after := time.Now()

			if err != nil && testCase.expectedError == nil {
				t.Errorf("unexpected error: %v", err)
			} else if testCase.expectedError != nil && err == nil {
				t.Errorf("unexpected success, expected: %s", testCase.expectedError)
			} else if testCase.expectedError != nil && !testCase.expectedError.MatchString(err.Error()) {
				t.Errorf("expected error %s, not: %v", testCase.expectedError, err)
			}

			if len(m.Calls) == 0 {
				t.Fatal("mock call was not logged")
			}
			logged := m.Calls[len(m.Calls)-1]
			if logged.When.Before(before) {
				t.Errorf("logged time %s but called after %s", logged.When, before)
			} else if logged.When.After(after) {
				t.Errorf("logged time %s but call completed by %s", logged.When, after)
			}
			if logged.Method != "Valid" {
				t.Errorf("logged method %q but expected Valid", logged.Method)
			}
			if !reflect.DeepEqual(logged.Condition, testCase.condition) {
				t.Errorf("logged condition %v but expected %v", logged.Condition, testCase.condition)
			}
		})
	}

	matchTestCases := []struct {
		condition     *configv1.ClusterCondition
		pushMatches   []mock.MatchResult
		expectedMatch bool
		expectedError *regexp.Regexp
	}{
		{
			condition:     nil,
			pushMatches:   []mock.MatchResult{{Match: true, Error: nil}},
			expectedMatch: true,
		},
		{
			condition:     &configv1.ClusterCondition{Type: "Match() call 1"},
			pushMatches:   []mock.MatchResult{{Match: true, Error: nil}},
			expectedMatch: true,
		},
		{
			condition:     &configv1.ClusterCondition{Type: "Match() call 2"},
			pushMatches:   []mock.MatchResult{{Match: false, Error: errors.New("error c")}},
			expectedMatch: false,
			expectedError: regexp.MustCompile("^error c$"),
		},
		{
			condition:     &configv1.ClusterCondition{Type: "Match() call 3"},
			pushMatches:   []mock.MatchResult{{Match: false, Error: nil}},
			expectedMatch: false,
		},
		{
			condition:     &configv1.ClusterCondition{Type: "Match() call with empty MatchQueue"},
			expectedMatch: false,
			expectedError: regexp.MustCompile("^the mock's MatchQueue stack is empty$"),
		},
	}
	for i := range matchTestCases {
		m.MatchQueue = append(m.MatchQueue, matchTestCases[i].pushMatches...)
	}

	for _, testCase := range matchTestCases {
		name := "nil condition"
		if testCase.condition != nil {
			name = testCase.condition.Type
		}
		t.Run(name, func(t *testing.T) {

			before := time.Now()
			match, err := m.Match(ctx, testCase.condition)
			after := time.Now()

			if match != testCase.expectedMatch {
				t.Errorf("got match %t but expected %t", match, testCase.expectedMatch)
			}
			if err != nil && testCase.expectedError == nil {
				t.Errorf("unexpected error: %v", err)
			} else if testCase.expectedError != nil && err == nil {
				t.Errorf("unexpected success, expected: %s", testCase.expectedError)
			} else if testCase.expectedError != nil && !testCase.expectedError.MatchString(err.Error()) {
				t.Errorf("expected error %s, not: %v", testCase.expectedError, err)
			}

			if len(m.Calls) == 0 {
				t.Fatal("mock call was not logged")
			}
			logged := m.Calls[len(m.Calls)-1]
			if logged.When.Before(before) {
				t.Errorf("logged time %s but called after %s", logged.When, before)
			} else if logged.When.After(after) {
				t.Errorf("logged time %s but call completed by %s", logged.When, after)
			}
			if logged.Method != "Match" {
				t.Errorf("logged method %q but expected Match", logged.Method)
			}
			if !reflect.DeepEqual(logged.Condition, testCase.condition) {
				t.Errorf("logged condition %v but expected %v", logged.Condition, testCase.condition)
			}
		})
	}
}

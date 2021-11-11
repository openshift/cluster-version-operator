package mock_test

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"regexp"
	"testing"
	"time"

	configv1 "github.com/openshift/api/config/v1"

	"github.com/openshift/cluster-version-operator/pkg/clusterconditions/mock"
)

func TestMock(t *testing.T) {
	ctx := context.Background()
	m := &mock.Mock{
		ValidQueue: []error{nil, errors.New("error a"), errors.New("error b")},
		MatchQueue: []mock.MatchResult{
			{
				Match: true,
				Error: nil,
			},
			{
				Match: false,
				Error: errors.New("error c"),
			},
			{
				Match: false,
				Error: nil,
			},
		},
	}

	for i, expectedError := range []*regexp.Regexp{
		nil,
		regexp.MustCompile("^error a$"),
		regexp.MustCompile("^error b$"),
		regexp.MustCompile("^the mock's ValidQueue stack is empty$"),
	} {
		name := fmt.Sprintf("Valid call %d", i)
		t.Run(name, func(t *testing.T) {
			condition := configv1.ClusterCondition{Type: name}

			before := time.Now()
			err := m.Valid(ctx, &condition)
			after := time.Now()

			if err != nil && expectedError == nil {
				t.Errorf("unexpected error: %v", err)
			} else if expectedError != nil && err == nil {
				t.Errorf("unexpected success, expected: %s", expectedError)
			} else if expectedError != nil && !expectedError.MatchString(err.Error()) {
				t.Errorf("expected error %s, not: %v", expectedError, err)
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
			if !reflect.DeepEqual(logged.Condition, condition) {
				t.Errorf("logged condition %v but expected %v", logged.Condition, condition)
			}
		})
	}

	for i, testCase := range []struct {
		expectedMatch bool
		expectedError *regexp.Regexp
	}{
		{
			expectedMatch: true,
			expectedError: nil,
		},
		{
			expectedMatch: false,
			expectedError: regexp.MustCompile("^error c$"),
		},
		{
			expectedMatch: false,
			expectedError: nil,
		},
		{
			expectedMatch: false,
			expectedError: regexp.MustCompile("^the mock's MatchQueue stack is empty$"),
		},
	} {
		name := fmt.Sprintf("Match call %d", i)
		t.Run(name, func(t *testing.T) {
			condition := configv1.ClusterCondition{Type: name}

			before := time.Now()
			match, err := m.Match(ctx, &condition)
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
			if !reflect.DeepEqual(logged.Condition, condition) {
				t.Errorf("logged condition %v but expected %v", logged.Condition, condition)
			}
		})
	}
}

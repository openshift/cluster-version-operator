package cache

import (
	"context"
	"fmt"
	"reflect"
	"regexp"
	"testing"
	"time"

	configv1 "github.com/openshift/api/config/v1"

	"github.com/openshift/cluster-version-operator/pkg/clusterconditions/mock"
)

func TestCache(t *testing.T) {
	ctx := context.Background()
	m := &mock.Mock{}
	c := &Cache{
		Condition:         m,
		MinBetweenMatches: 225 * time.Millisecond,
		MinForCondition:   time.Second,     // so 4 requests before we can repeat a previous condition
		Expiration:        2 * time.Second, // so ~8 conditions before expiration overtakes refreshing
	}

	minConcurrence := 2
	maxConcurrence := 10
	lowConcurrenceRounds := 5
	sleepBetweenRounds := 100 * time.Millisecond
	highConcurrenceRounds := maxConcurrence * 2 * int(c.Expiration) / int(sleepBetweenRounds) // long enough for a round of expiration and recovery
	tailRounds := 2 * int(c.Expiration) / int(sleepBetweenRounds)                             // long enough to clear out all the high-concurrency conditions
	totalRounds := lowConcurrenceRounds + highConcurrenceRounds + tailRounds
	success := make(map[int]bool, maxConcurrence)
	for i := 0; i < totalRounds; i++ {
		if i > 0 {
			time.Sleep(sleepBetweenRounds)
		}

		concurrent := minConcurrence
		if i >= lowConcurrenceRounds && i < lowConcurrenceRounds+highConcurrenceRounds {
			concurrent = maxConcurrence
		}
		for j := 0; j < concurrent; j++ {
			name := fmt.Sprintf("condition %d", j)
			condition := configv1.ClusterCondition{Type: name}
			m.MatchQueue = append(m.MatchQueue, mock.MatchResult{Match: true, Error: nil})
			match, err := c.Match(ctx, &condition)
			t.Logf("%s round %d, %s -> %t %v", time.Now(), i, name, match, err)
			if err == nil {
				success[j] = true
			}
		}
		if i == lowConcurrenceRounds-1 || i == lowConcurrenceRounds+highConcurrenceRounds-1 || i == totalRounds-1 {
			successful := 0
			for k := 0; k < concurrent; k++ {
				if success[k] {
					successful++
					success[k] = false
				} else {
					t.Logf("failed to achieve expected success for condition %d during round %d with %d concurrency", k, i, concurrent)
				}
			}
			if successful < concurrent && successful < 7 {
				t.Errorf("only %d successfully evaluated conditions during round %d with %d concurrency", successful, i, concurrent)
			}
		}
	}

	for i, call := range m.Calls {
		t.Logf("call %d, %s %v", i, call.When, call.Condition)
	}

	minSpaceBetweenCalls := m.Calls[1].When.Sub(m.Calls[0].When)
	maxSpaceBetweenCalls := minSpaceBetweenCalls
	for i := 2; i < len(m.Calls); i++ {
		spaceBetweenCalls := m.Calls[i].When.Sub(m.Calls[i-1].When)
		if spaceBetweenCalls < minSpaceBetweenCalls {
			minSpaceBetweenCalls = spaceBetweenCalls
		} else if spaceBetweenCalls > maxSpaceBetweenCalls {
			maxSpaceBetweenCalls = spaceBetweenCalls
		}
	}
	if minSpaceBetweenCalls < c.MinBetweenMatches {
		t.Errorf("the minimum space between calls of %s violated the configured minimum of %s", minSpaceBetweenCalls, c.MinBetweenMatches)
	} else {
		t.Logf("minimum space between Match calls was %s, which complies with the configured minimum of %s", minSpaceBetweenCalls, c.MinBetweenMatches)
	}
	expectedMaximumSpaceBetweenCalls := c.MinForCondition + sleepBetweenRounds + 50*time.Millisecond
	if maxSpaceBetweenCalls > expectedMaximumSpaceBetweenCalls {
		t.Errorf("the maximum space between calls of %s exceeded the expected maximum of %s", maxSpaceBetweenCalls, expectedMaximumSpaceBetweenCalls)
	} else {
		t.Logf("maximum space between Match calls was %s, which complies with the expected maximum of %s", maxSpaceBetweenCalls, expectedMaximumSpaceBetweenCalls)
	}

	for i := 0; i < maxConcurrence; i++ {
		name := fmt.Sprintf("condition %d", i)
		condition := &configv1.ClusterCondition{Type: name}
		var previousCall *mock.Call
		found := false
		for j, call := range m.Calls {
			if reflect.DeepEqual(call.Condition, condition) {
				found = true
				if previousCall != nil {
					spaceBetweenCalls := call.When.Sub(previousCall.When)
					if spaceBetweenCalls < c.MinForCondition {
						t.Errorf("the space between %s calls of %s violated the configured minimum of %s", name, spaceBetweenCalls, c.MinForCondition)
					}
				}
				previousCall = &m.Calls[j]
			}
		}
		if !found {
			t.Errorf("condition %v not found in recorded calls", condition)
		}
	}
}

func Test_calculateMostStale(t *testing.T) {
	now := time.Now()
	for _, testCase := range []struct {
		name              string
		cache             map[string]*MatchResult
		expectedKey       string
		expectedCondition *configv1.ClusterCondition
		expectedError     *regexp.Regexp
	}{
		{
			name:  "empty cache",
			cache: map[string]*MatchResult{},
		},
		{
			name: "single entry, invalid key",
			cache: map[string]*MatchResult{
				"a": {Access: now, Match: true, Error: nil},
			},
			expectedError: regexp.MustCompile(`^a is the most stale cached cluster-condition match entry, but key is invalid JSON: .*`),
		},
		{
			name: "single entry, never evaluated",
			cache: map[string]*MatchResult{
				`{"type": "a"}`: {Access: now, Match: true, Error: nil},
			},
			expectedKey:       `{"type": "a"}`,
			expectedCondition: &configv1.ClusterCondition{Type: "a"},
		},
		{
			name: "single entry, no access since old evaluation",
			cache: map[string]*MatchResult{
				`{"type": "a"}`: {When: now.Add(-time.Hour), Access: now.Add(-time.Hour), Match: true, Error: nil},
			},
		},
		{
			name: "single entry, has access since old evaluation",
			cache: map[string]*MatchResult{
				`{"type": "a"}`: {When: now.Add(-time.Hour), Access: now, Match: true, Error: nil},
			},
			expectedKey:       `{"type": "a"}`,
			expectedCondition: &configv1.ClusterCondition{Type: "a"},
		},
		{
			name: "single entry, has access since new evaluation",
			cache: map[string]*MatchResult{
				`{"type": "a"}`: {When: now.Add(-time.Minute), Access: now, Match: true, Error: nil},
			},
			expectedKey: `{"type": "a"}`,
		},
		{
			name: "two entries, both old evaluations, clear evaluation winner",
			cache: map[string]*MatchResult{
				`{"type": "a"}`: {When: now.Add(-2 * time.Hour), Access: now, Match: true, Error: nil},
				`{"type": "b"}`: {When: now.Add(-time.Hour), Access: now.Add(-time.Minute), Match: true, Error: nil},
			},
			expectedKey:       `{"type": "a"}`,
			expectedCondition: &configv1.ClusterCondition{Type: "a"},
		},
		{
			name: "two entries, both old evaluations, tied evaluation, access winner",
			cache: map[string]*MatchResult{
				`{"type": "a"}`: {When: now.Add(-time.Hour), Access: now.Add(-time.Minute), Match: true, Error: nil},
				`{"type": "b"}`: {When: now.Add(-time.Hour), Match: true, Error: nil},
			},
			expectedKey:       `{"type": "a"}`,
			expectedCondition: &configv1.ClusterCondition{Type: "a"},
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			c := Cache{
				MinForCondition: 30 * time.Minute,
				MatchResults:    testCase.cache,
			}
			key, condition, err := c.calculateMostStale(now)

			if key != testCase.expectedKey {
				t.Errorf("got key %q but expected %q", key, testCase.expectedKey)
			}

			if !reflect.DeepEqual(condition, testCase.expectedCondition) {
				t.Errorf("got condition %v but expected %v", condition, testCase.expectedCondition)
			}

			if err != nil && testCase.expectedError == nil {
				t.Errorf("unexpected error: %v", err)
			} else if testCase.expectedError != nil && err == nil {
				t.Errorf("unexpected success, expected: %s", testCase.expectedError)
			} else if testCase.expectedError != nil && !testCase.expectedError.MatchString(err.Error()) {
				t.Errorf("expected error %s, not: %v", testCase.expectedError, err)
			}
		})
	}

}

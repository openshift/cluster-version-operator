// Package cache implements a throttled, caching condition.
package cache

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	configv1 "github.com/openshift/api/config/v1"
	"k8s.io/klog/v2"

	"github.com/openshift/cluster-version-operator/pkg/clusterconditions"
)

// MatchResult represents a single Match invocation.
type MatchResult struct {
	// When is the completion time of the wrapped Match call.
	When time.Time

	// Access is the time of the most recent access.
	Access time.Time

	// Match is whether the result was a match or not.
	Match bool

	// Error is the error returned by the Match call.
	Error error
}

// Cache wraps a cluster condition with caching and throttling.
type Cache struct {
	// Condition is the wrapped cluster condition.
	Condition clusterconditions.Condition

	// LastMatch is the completion time of the most recent Match
	// call evaluated by the wrapped cluster condition.
	LastMatch time.Time

	// MinBetweenMatches is the minimum duration before a new
	// Match call may be evaluated by the wrapped cluster condition.
	MinBetweenMatches time.Duration

	// MinForCondition is the minimum duration before a new Match
	// call for a given condition may be evaluated.
	MinForCondition time.Duration

	// Expiration is the duration a value will be cached before
	// being evicted for stale-ness.
	Expiration time.Duration

	// MatchResults holds results of previous match invocations.
	MatchResults map[string]*MatchResult
}

// Valid returns an error if the wrapped cluster condition considers
// the value invalid.
func (c *Cache) Valid(ctx context.Context, condition *configv1.ClusterCondition) error {
	return c.Condition.Valid(ctx, condition)
}

// Match returns the match value from the wrapped cluster condition,
// possibly from a fresh evaluation, possibly from a local cache.
func (c *Cache) Match(ctx context.Context, condition *configv1.ClusterCondition) (bool, error) {
	if c.MatchResults == nil {
		c.MatchResults = map[string]*MatchResult{}
	}

	keyBytes, err := json.Marshal(condition)
	if err != nil {
		return false, fmt.Errorf("unable to marshal condition to JSON for use as a cache key: %w", err)
	}
	key := string(keyBytes)

	now := time.Now()

	defer func() {
		if result, ok := c.MatchResults[key]; ok {
			result.Access = now
		} else {
			c.MatchResults[key] = &MatchResult{Access: now}
		}
	}()

	c.expireStaleMatchResults(now)

	if sinceLastMatch := now.Sub(c.LastMatch); sinceLastMatch <= c.MinBetweenMatches {
		if result, ok := c.MatchResults[key]; ok && !result.When.IsZero() {
			return result.Match, result.Error
		}
		return false, fmt.Errorf("evaluation is throttled until %s", c.LastMatch.Add(c.MinBetweenMatches).Format("15:04:05Z07"))
	}

	// If we only attempt to evaluate the requested condition, and
	// the callers request conditions in batches with the same
	// order, we might continually evaluate the early conditions
	// while starving out later conditions.  Instead, spend our
	// Match call on the most stale condition.
	thiefKey, targetCondition, err := c.calculateMostStale(now)
	if err != nil {
		return false, fmt.Errorf("calculating the most stale cached cluster-condition match entry: %w", err)
	}

	if thiefKey == "" {
		thiefKey = key // cache is empty, or no access since last evaluation, so evaluate the requested condition
		targetCondition = condition
	}

	if targetCondition == nil {
		if result, ok := c.MatchResults[key]; ok && !result.When.IsZero() {
			return result.Match, result.Error
		}
		var detail string
		if thiefResult, ok := c.MatchResults[thiefKey]; ok {
			detail = fmt.Sprintf(" (last evaluated on %s)", thiefResult.When)
		}
		klog.V(2).Infof("%s is the most stale cached cluster-condition match entry, but it is too fresh%s.  However, we don't have a cached evaluation for %s, so attempt to evaluate that now.", thiefKey, detail, key)
	}

	// if we ended up stealing this Match call, log that, to make contention more clear
	if thiefKey != key {
		var reason string
		if thiefResult, ok := c.MatchResults[thiefKey]; !ok || thiefResult.When.IsZero() {
			reason = "it has never been evaluated"
		} else {
			reason = fmt.Sprintf("its last evaluation completed %s ago", now.Sub(thiefResult.When))
		}
		klog.V(2).Infof("%s is stealing this cluster-condition match call for %s, because %s", thiefKey, key, reason)
	}

	match, err := c.Condition.Match(ctx, targetCondition)
	now = time.Now()
	c.LastMatch = now
	if _, ok := c.MatchResults[thiefKey]; !ok {
		c.MatchResults[thiefKey] = &MatchResult{}
	}
	result := c.MatchResults[thiefKey]
	result.When = now
	result.Match = match
	result.Error = err

	if result, ok := c.MatchResults[key]; ok && !result.When.IsZero() {
		return result.Match, result.Error
	}
	return false, errors.New("evaluation is throttled")
}

// expireStaleMatchResults removes entries from MatchResults if their
// last-evaluation When is more than Expiration ago.  For MatchResults
// entries which have never been evaluated, the last-request Access
// time is used instead.
func (c *Cache) expireStaleMatchResults(now time.Time) {
	for key, value := range c.MatchResults {
		age := now.Sub(value.When)
		aspect := "result"
		if value.When.IsZero() {
			age = now.Sub(value.Access)
			aspect = "queued request"
		}
		if age > c.Expiration {
			klog.V(2).Infof("pruning %q from the condition cache, as the %s is %s old", key, aspect, age)
			delete(c.MatchResults, key)
		}
	}
}

// calculateMostStale returns the most-stale entry in the cache, or
// nil if the most-stale entry has been evaluated more recently than
// MinForCondition ago.
func (c *Cache) calculateMostStale(now time.Time) (string, *configv1.ClusterCondition, error) {
	var thiefKey string
	var thiefResult *MatchResult
	for candidateKey, value := range c.MatchResults {
		if !value.Access.After(value.When) {
			continue // no requests since its last wrapped Match call
		}
		if thiefResult == nil ||
			value.When.Before(thiefResult.When) || // refresh the most stale
			(value.When == thiefResult.When && value.Access.Before(thiefResult.Access)) { // break ties in favor of the longest-queued
			thiefKey = candidateKey
			thiefResult = c.MatchResults[candidateKey]
		}
	}

	if thiefKey == "" { // empty cache, or no access since last evaluation
		return "", nil, nil
	}

	if thiefResult != nil && now.Sub(thiefResult.When) < c.MinForCondition {
		// thief is our most stale, and it's still too fresh to Match again.  Just return from the cache if we have a cached result.
		return thiefKey, nil, nil
	}

	var targetCondition *configv1.ClusterCondition
	if err := json.Unmarshal([]byte(thiefKey), &targetCondition); err != nil {
		delete(c.MatchResults, thiefKey)
		return "", nil, fmt.Errorf("%s is the most stale cached cluster-condition match entry, but key is invalid JSON: %v", thiefKey, err)
	}
	return thiefKey, targetCondition, nil
}

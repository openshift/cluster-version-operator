package cvo

import (
	"math"
	"strings"

	"k8s.io/klog/v2"

	configv1 "github.com/openshift/api/config/v1"
)

const (
	// maxFinalEntryIndex is an upper bound index. History entries from 0 to maxFinalEntryIndex are given mostImportantWeight.
	maxFinalEntryIndex = 4

	// mostImportantWeight is given to history entries that should never be removed including the initial entry,
	// the most recent entries (indices 0..maxFinalEntryIndex), and the most recently completed update.
	mostImportantWeight = 1000.0

	// interestingWeight is given to the first or last completed update in a given minor. These are interesting
	// but not critical.
	interestingWeight = 30.0

	// partialMinorWeight is given to any partial minor updates between a completed minor version transition.
	partialMinorWeight = 20.0

	// partialZStreamWeight is given to any partial z-stream updates between a completed z-stream version transition.
	partialZStreamWeight = -20.0

	// sliceIndexWeight when applied will favor more recent updates and avoid ties.
	sliceIndexWeight = -1.01
)

// prune prunes history when at maxSize by ranking each entry using the above defined weights and removing the entry with
// the lowest rank. maxSize is passed in to allow the use of smaller values which eases unit test.
func prune(history []configv1.UpdateHistory, maxSize int) []configv1.UpdateHistory {
	if len(history) <= maxSize {
		return history
	}
	mostRecentCompletedEntryIndex := getTheMostRecentCompletedEntryIndex(history)

	var reason string
	lowestRank := math.MaxFloat64
	var lowestRankIdx int

	for i := range history {
		rank := 0.0
		thisReason := "an older update of less importance"
		if isTheInitialEntry(i, maxSize) || isAFinalEntry(i) || isTheMostRecentCompletedEntry(i, mostRecentCompletedEntryIndex) {
			rank = mostImportantWeight
		} else if isTheFirstOrLastCompletedInAMinor(i, history, maxSize) {
			rank = rank + interestingWeight
			thisReason = "the first or last completed minor update"
		} else if isPartialPortionOfMinorTransition(i, history, maxSize) {
			rank = rank + partialMinorWeight
			thisReason = "a partial update within a minor transition"
		} else if isPartialWithinAZStream(i, history, maxSize) {
			rank = rank + partialZStreamWeight
			thisReason = "a partial update within a z-stream transition"
		}
		rank += sliceIndexWeight * float64(i)

		if rank < lowestRank {
			lowestRank = rank
			lowestRankIdx = i
			reason = thisReason
		}
	}
	klog.V(2).Infof("Pruning %s version %s at index %d with rank %f.", reason, history[lowestRankIdx].Version, lowestRankIdx, lowestRank)

	var prunedHistory []configv1.UpdateHistory
	if lowestRankIdx == maxSize {
		prunedHistory = history[:maxSize]
	} else {
		prunedHistory = append(history[:lowestRankIdx], history[lowestRankIdx+1:]...)
	}
	return prunedHistory
}

// isTheInitialEntry returns true if entryIndex is the first entry entered into the slice (i.e. is maxHistorySize).
func isTheInitialEntry(entryIndex int, maxHistorySize int) bool {
	return entryIndex == maxHistorySize
}

// isAFinalEntry returns true if entryIndex falls within 0..maxFinalEntryIndex.
func isAFinalEntry(entryIndex int) bool {
	return entryIndex <= maxFinalEntryIndex
}

// isTheMostRecentCompletedEntry returns true if entryIndex is equal to theMostRecentCompletedEntryIndex.
func isTheMostRecentCompletedEntry(entryIndex int, theMostRecentCompletedEntryIndex int) bool {
	return entryIndex == theMostRecentCompletedEntryIndex
}

// isTheFirstOrLastCompletedInAMinor returns true if the entry at entryIndex is the first or last completed update for
// a given minor version change.
func isTheFirstOrLastCompletedInAMinor(entryIndex int, h []configv1.UpdateHistory, maxHistorySize int) bool {
	if h[entryIndex].State == configv1.PartialUpdate {
		return false
	}
	if entryIndex == 0 || entryIndex == maxHistorySize {
		return true
	}
	nextIdx := findNextOlderCompleted(entryIndex, h)
	if nextIdx == entryIndex || !sameMinorVersion(h[entryIndex], h[nextIdx]) {
		return true
	}
	nextIdx = findNextNewerCompleted(entryIndex, h)
	if nextIdx == entryIndex || !sameMinorVersion(h[entryIndex], h[nextIdx]) {
		return true
	}
	return false
}

// isPartialPortionOfMinorTransition returns true if the entry at entryIndex is a partial update that is between completed
// updates that have transitioned the version from one minor version to another.
func isPartialPortionOfMinorTransition(entryIndex int, h []configv1.UpdateHistory, maxHistorySize int) bool {
	if h[entryIndex].State == configv1.CompletedUpdate || entryIndex == 0 || entryIndex == maxHistorySize {
		return false
	}
	prevIdx := findNextOlderCompleted(entryIndex, h)
	if prevIdx == entryIndex {
		return false
	}
	nextIdx := findNextNewerCompleted(entryIndex, h)
	if nextIdx == entryIndex || sameMinorVersion(h[prevIdx], h[nextIdx]) {
		return false
	}
	return true
}

// isPartialWithinAZStream returns true if the entry at entryIndex is a partial update that is between completed
// updates that have transitioned the version from one z-stream version to another.
func isPartialWithinAZStream(entryIndex int, h []configv1.UpdateHistory, maxHistorySize int) bool {
	if h[entryIndex].State == configv1.CompletedUpdate || entryIndex == 0 || entryIndex == maxHistorySize {
		return false
	}
	prevIdx := findNextOlderCompleted(entryIndex, h)
	if prevIdx == entryIndex {
		return false
	}
	nextIdx := findNextNewerCompleted(entryIndex, h)
	if nextIdx == entryIndex || sameZStreamVersion(h[prevIdx], h[nextIdx]) {
		return false
	}
	return true
}

// getTheMostRecentCompletedEntryIndex returns the index of the entry that is the most recently completed update as defined
// by its position in the slice. The slice is ordered from the latest to the earliest update.
func getTheMostRecentCompletedEntryIndex(h []configv1.UpdateHistory) int {
	idx := -1
	for i, entry := range h {
		if entry.State == configv1.CompletedUpdate {
			idx = i
			break
		}
	}
	return idx
}

// findNextOlderCompleted starts at entryIndex and returns the index of the entry that is the next older completed
// update as defined by its position in the slice. The slice is ordered from the latest to the earliest update.
func findNextOlderCompleted(entryIndex int, h []configv1.UpdateHistory) int {
	idx := entryIndex
	for i := entryIndex + 1; i < len(h); i++ {
		if h[i].State == configv1.CompletedUpdate {
			idx = i
			break
		}
	}
	return idx
}

// findNextNewerCompleted starts at entryIndex and returns the index of the entry that is the next newer completed
// update as defined by its position in the slice. The slice is ordered from the latest to the earliest update.
func findNextNewerCompleted(entryIndex int, h []configv1.UpdateHistory) int {
	idx := entryIndex
	for i := entryIndex - 1; i >= 0; i-- {
		if h[i].State == configv1.CompletedUpdate {
			idx = i
			break
		}
	}
	return idx
}

// sameMinorVersion returns true if e1 is the same minor version as e2.
func sameMinorVersion(e1 configv1.UpdateHistory, e2 configv1.UpdateHistory) bool {
	return getEffectiveMinor(e1.Version) == getEffectiveMinor(e2.Version)
}

// sameZStreamVersion returns true if e1 is the same z-stream version as e2.
func sameZStreamVersion(e1 configv1.UpdateHistory, e2 configv1.UpdateHistory) bool {
	return getEffectiveMinor(e1.Version) == getEffectiveMinor(e2.Version) &&
		getEffectiveMicro(e1.Version) == getEffectiveMicro(e2.Version)
}

// getEffectiveMinor attempts to parse the given version string as x.y. If it does not parse an empty string is returned
// otherwise the minor version y is returned.
func getEffectiveMinor(version string) string {
	splits := strings.Split(version, ".")
	if len(splits) < 2 {
		return ""
	}
	return splits[1]
}

// getEffectiveMicro attempts to parse the given version string as x.y.z. If it does not parse an empty string is returned
// otherwise the micro (z-stream) version z is returned.
func getEffectiveMicro(version string) string {
	splits := strings.Split(version, ".")
	if len(splits) < 3 {
		return ""
	}
	return splits[2]
}

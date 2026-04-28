// Copyright (c) Microsoft. All rights reserved.

package compaction

import (
	"cmp"
	"context"
)

const defaultMinimumPreservedTruncationGroups = 32

// TruncationStrategy excludes the oldest non-system groups while preserving recent groups.
//
// System messages are always preserved. The strategy respects group boundaries, so tool-call groups
// are removed as atomic units instead of separating assistant calls from tool results.
type TruncationStrategy struct {
	// Trigger controls whether truncation should run.
	// When nil, truncation always runs.
	Trigger Trigger

	// Target controls when truncation stops after each exclusion.
	// When nil, truncation stops when Trigger would no longer fire.
	Target Trigger

	// MinimumPreservedGroups is the minimum number of most-recent non-system groups to preserve.
	// This is a hard floor; truncation will not remove groups beyond this limit.
	MinimumPreservedGroups int
}

// Compact compacts index in place.
func (strategy *TruncationStrategy) Compact(_ context.Context, index *MessageIndex) (bool, error) {
	target, ok := prepareCompaction(index, strategy.Trigger, strategy.Target)
	if !ok {
		return false, nil
	}

	minimumPreservedGroups := cmp.Or(max(strategy.MinimumPreservedGroups, 0), defaultMinimumPreservedTruncationGroups)
	var removableCount int
	for _, group := range index.Groups {
		if !group.IsExcluded && group.Kind != GroupKindSystem {
			removableCount++
		}
	}
	maxRemovable := removableCount - minimumPreservedGroups
	if maxRemovable <= 0 {
		return false, nil
	}

	var compacted bool
	var removed int
	for _, group := range index.Groups {
		if removed >= maxRemovable {
			break
		}
		if group.IsExcluded || group.Kind == GroupKindSystem {
			continue
		}
		group.IsExcluded = true
		group.ExcludeReason = "truncated by TruncationStrategy"
		removed++
		compacted = true
		if target(index) {
			break
		}
	}
	return compacted, nil
}

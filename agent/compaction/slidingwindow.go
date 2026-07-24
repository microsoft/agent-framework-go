// Copyright (c) Microsoft. All rights reserved.

package compaction

import (
	"context"
	"slices"
)

const defaultMinimumPreservedSlidingWindowTurns = 1

// SlidingWindowStrategy excludes the oldest user turns while preserving recent turns.
//
// System messages are always preserved. This strategy operates on logical turn boundaries rather
// than token estimates, making it predictable for bounding conversation length.
type SlidingWindowStrategy struct {
	// Trigger controls whether sliding-window compaction should run.
	// When nil, compaction always runs.
	Trigger Trigger

	// Target controls when compaction stops after each excluded turn.
	// When nil, compaction stops when Trigger would no longer fire.
	Target Trigger

	// MinimumPreservedTurns is the minimum number of most-recent user turns to preserve.
	// Groups with nil or non-positive turn indexes are preserved independently of this value.
	//
	// When nil, a default floor is used. An explicit value is honored as-is, so a pointer to 0
	// disables the floor entirely; a negative value is clamped to 0.
	MinimumPreservedTurns *int
}

// Compact compacts index in place.
func (strategy *SlidingWindowStrategy) Compact(_ context.Context, index *MessageIndex) (bool, error) {
	target, ok := prepareCompaction(index, strategy.Trigger, strategy.Target)
	if !ok {
		return false, nil
	}

	minimumPreservedTurns := defaultMinimumPreservedSlidingWindowTurns
	if strategy.MinimumPreservedTurns != nil {
		minimumPreservedTurns = max(*strategy.MinimumPreservedTurns, 0)
	}
	turnGroups := make(map[int][]int)
	var turnOrder []int
	for i, group := range index.Groups {
		if group.IsExcluded || group.Kind == GroupKindSystem || group.TurnIndex == nil {
			continue
		}
		turnIndex := *group.TurnIndex
		if turnIndex <= 0 {
			continue
		}
		if _, ok := turnGroups[turnIndex]; !ok {
			turnOrder = append(turnOrder, turnIndex)
		}
		turnGroups[turnIndex] = append(turnGroups[turnIndex], i)
	}

	turnsToProtect := min(minimumPreservedTurns, len(turnOrder))
	protectedTurns := turnOrder[len(turnOrder)-turnsToProtect:]

	var compacted bool
	for _, turnIndex := range turnOrder {
		if slices.Contains(protectedTurns, turnIndex) {
			continue
		}
		for _, groupIndex := range turnGroups[turnIndex] {
			group := index.Groups[groupIndex]
			group.IsExcluded = true
			group.ExcludeReason = "excluded by SlidingWindowStrategy"
		}
		compacted = true
		if target(index) {
			break
		}
	}
	return compacted, nil
}

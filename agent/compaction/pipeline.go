// Copyright (c) Microsoft. All rights reserved.

package compaction

import "context"

// PipelineStrategy executes strategies sequentially against the same index.
//
// Each strategy operates on the result of the previous one, enabling composed behaviors such as
// summarizing older messages and then truncating to fit a budget.
type PipelineStrategy struct {
	// Trigger controls whether the pipeline should run.
	// When nil, the pipeline always runs.
	Trigger Trigger

	// Target controls when the pipeline should stop compacting.
	// When nil, it defaults to the inverse of Trigger.
	Target Trigger

	// Strategies is the ordered sequence of strategies to execute.
	Strategies []Strategy
}

// Compact compacts index in place.
func (strategy *PipelineStrategy) Compact(ctx context.Context, index *MessageIndex) (bool, error) {
	if _, ok := prepareCompaction(index, strategy.Trigger, strategy.Target); !ok {
		return false, nil
	}

	var anyCompacted bool
	for _, child := range strategy.Strategies {
		if child == nil {
			continue
		}
		compacted, err := child.Compact(ctx, index)
		if err != nil {
			return anyCompacted, err
		}
		if compacted {
			anyCompacted = true
		}
	}
	return anyCompacted, nil
}

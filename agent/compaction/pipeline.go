// Copyright (c) Microsoft. All rights reserved.

package compaction

import "context"

// PipelineStrategy executes strategies sequentially against the same index.
//
// Each strategy operates on the result of the previous one, enabling composed behaviors such as
// summarizing older messages and then truncating to fit a budget.
type PipelineStrategy struct {
	// Strategies is the ordered sequence of strategies to execute.
	Strategies []Strategy
}

// Compact compacts index in place.
func (strategy *PipelineStrategy) Compact(ctx context.Context, index *MessageIndex) (bool, error) {
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

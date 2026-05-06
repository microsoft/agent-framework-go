// Copyright (c) Microsoft. All rights reserved.

package compaction

import (
	"context"
	"fmt"
)

// defaultContextWindowToolEvictionThreshold is the default fraction of the input budget at which
// tool-result eviction triggers.
const defaultContextWindowToolEvictionThreshold = 0.5

// defaultContextWindowTruncationThreshold is the default fraction of the input budget at which
// truncation triggers.
const defaultContextWindowTruncationThreshold = 0.8

// ContextWindowStrategy is a compaction strategy that derives token thresholds from a model's
// context window size and maximum output tokens, applying a two-phase pipeline:
//
//  1. Tool-result eviction ([ToolResultStrategy]) — collapses old tool-call groups into concise
//     summaries when the token count exceeds ToolEvictionThreshold × InputBudget.
//  2. Truncation ([TruncationStrategy]) — removes the oldest non-system message groups when
//     the token count exceeds TruncationThreshold × InputBudget.
//
// The input budget is MaxContextWindowTokens - MaxOutputTokens, representing the tokens available
// for conversation input (system messages, tools, and history).
//
// This is a convenience wrapper around [PipelineStrategy] that automates threshold calculation
// from model specifications.
type ContextWindowStrategy struct {
	// MaxContextWindowTokens is the maximum number of tokens the model's context window supports
	// (for example, 1,048,576 for gpt-4.1). Must be positive.
	MaxContextWindowTokens int

	// MaxOutputTokens is the maximum number of output tokens the model can generate per response
	// (for example, 32,768 for gpt-4.1). Must be non-negative and less than MaxContextWindowTokens.
	MaxOutputTokens int

	// ToolEvictionThreshold is the fraction of the input budget at which tool-result eviction
	// triggers. Must be in (0.0, 1.0]. Zero uses the default (0.5).
	ToolEvictionThreshold float64

	// TruncationThreshold is the fraction of the input budget at which truncation triggers.
	// Must be in (0.0, 1.0] and >= ToolEvictionThreshold. Zero uses the default (0.8).
	TruncationThreshold float64
}

// Compact compacts index in place using a two-phase tool-result eviction and truncation pipeline
// derived from the model's context window and output token limits.
func (s *ContextWindowStrategy) Compact(ctx context.Context, index *MessageIndex) (bool, error) {
	toolEviction := s.ToolEvictionThreshold
	if toolEviction == 0 {
		toolEviction = defaultContextWindowToolEvictionThreshold
	}
	truncation := s.TruncationThreshold
	if truncation == 0 {
		truncation = defaultContextWindowTruncationThreshold
	}

	if s.MaxContextWindowTokens <= 0 {
		return false, fmt.Errorf("compaction: ContextWindowStrategy.MaxContextWindowTokens must be positive, got %d", s.MaxContextWindowTokens)
	}
	if s.MaxOutputTokens < 0 || s.MaxOutputTokens >= s.MaxContextWindowTokens {
		return false, fmt.Errorf("compaction: ContextWindowStrategy.MaxOutputTokens must be in [0, MaxContextWindowTokens), got %d", s.MaxOutputTokens)
	}
	if toolEviction <= 0 || toolEviction > 1 {
		return false, fmt.Errorf("compaction: ContextWindowStrategy.ToolEvictionThreshold must be in (0, 1], got %g", toolEviction)
	}
	if truncation <= 0 || truncation > 1 {
		return false, fmt.Errorf("compaction: ContextWindowStrategy.TruncationThreshold must be in (0, 1], got %g", truncation)
	}
	if truncation < toolEviction {
		return false, fmt.Errorf("compaction: ContextWindowStrategy.TruncationThreshold (%g) must be >= ToolEvictionThreshold (%g)", truncation, toolEviction)
	}

	inputBudget := s.MaxContextWindowTokens - s.MaxOutputTokens
	toolEvictionTokens := int(float64(inputBudget) * toolEviction)
	truncationTokens := int(float64(inputBudget) * truncation)

	pipeline := &PipelineStrategy{
		Strategies: []Strategy{
			&ToolResultStrategy{
				Trigger:                TokensExceed(toolEvictionTokens),
				MinimumPreservedGroups: 2,
			},
			&TruncationStrategy{
				Trigger:                TokensExceed(truncationTokens),
				MinimumPreservedGroups: 2,
			},
		},
	}
	return pipeline.Compact(ctx, index)
}

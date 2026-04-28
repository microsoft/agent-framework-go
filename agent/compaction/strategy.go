// Copyright (c) Microsoft. All rights reserved.

package compaction

import (
	"context"

	"github.com/microsoft/agent-framework-go/message"
)

// Strategy compacts a message index to reduce context size.
//
// Strategies mutate the provided index in place by marking groups as excluded or inserting compact
// replacement groups such as summaries.
type Strategy interface {
	// Compact applies strategy-specific compaction to index.
	//
	// It returns true when the strategy changed the index. Implementations should honor ctx for
	// cancellation when they perform blocking work.
	Compact(context.Context, *MessageIndex) (bool, error)
}

func prepareCompaction(index *MessageIndex, trigger, target Trigger) (Trigger, bool) {
	if index == nil {
		panic("index is required")
	}
	if trigger == nil {
		trigger = Always()
	}
	if target == nil {
		target = func(index *MessageIndex) bool { return !trigger(index) }
	}
	if index.IncludedNonSystemGroupCount() <= 1 || !trigger(index) {
		return nil, false
	}
	return target, true
}

// Compact applies a strategy to messages and returns the included compacted messages.
//
// It is useful for ad-hoc compaction outside of a context provider. The input messages are first
// grouped into a MessageIndex, the strategy is applied, and only non-excluded messages are returned.
func Compact(ctx context.Context, strategy Strategy, messages []*message.Message, tokenCounter TokenCounter) ([]*message.Message, error) {
	if strategy == nil {
		panic("strategy is required")
	}
	index := CreateMessageIndex(messages, tokenCounter)
	if _, err := strategy.Compact(ctx, index); err != nil {
		return nil, err
	}
	return index.IncludedMessages(), nil
}

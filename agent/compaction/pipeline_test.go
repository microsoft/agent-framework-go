// Copyright (c) Microsoft. All rights reserved.

package compaction_test

import (
	"context"
	"slices"
	"testing"

	"github.com/microsoft/agent-framework-go/agent/compaction"
	"github.com/microsoft/agent-framework-go/message"
)

type testStrategy struct {
	fn    func(*compaction.MessageIndex) bool
	calls int
}

func (strategy *testStrategy) Compact(_ context.Context, index *compaction.MessageIndex) (bool, error) {
	strategy.calls++
	if strategy.fn == nil {
		return false, nil
	}
	return strategy.fn(index), nil
}

func TestPipelineStrategy_ExecutesAllStrategiesInOrder(t *testing.T) {
	var order []string
	first := &testStrategy{fn: func(*compaction.MessageIndex) bool { order = append(order, "first"); return false }}
	second := &testStrategy{fn: func(*compaction.MessageIndex) bool { order = append(order, "second"); return false }}
	pipeline := &compaction.PipelineStrategy{Strategies: []compaction.Strategy{first, second}}
	index := compaction.CreateMessageIndex(turnMessages(1), nil)

	compacted, err := pipeline.Compact(t.Context(), index)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if compacted {
		t.Fatal("expected no compaction")
	}
	if !slices.Equal(order, []string{"first", "second"}) {
		t.Fatalf("unexpected execution order: %v", order)
	}
}

func TestPipelineStrategy_ReturnsTrueWhenAnyStrategyCompactsAndContinues(t *testing.T) {
	first := &testStrategy{fn: func(*compaction.MessageIndex) bool { return true }}
	second := &testStrategy{fn: func(*compaction.MessageIndex) bool { return false }}
	pipeline := &compaction.PipelineStrategy{Strategies: []compaction.Strategy{first, second}}
	index := compaction.CreateMessageIndex(turnMessages(1), nil)

	compacted, err := pipeline.Compact(t.Context(), index)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !compacted {
		t.Fatal("expected compaction")
	}
	if first.calls != 1 || second.calls != 1 {
		t.Fatalf("expected both strategies to be called once, got first=%d second=%d", first.calls, second.calls)
	}
}

func TestPipelineStrategy_ComposesStrategiesEndToEnd(t *testing.T) {
	excludeOldestTwo := func(index *compaction.MessageIndex) bool {
		excluded := 0
		for _, group := range index.Groups {
			if !group.IsExcluded && group.Kind != compaction.GroupKindSystem && excluded < 2 {
				group.IsExcluded = true
				excluded++
			}
		}
		return true
	}
	pipeline := &compaction.PipelineStrategy{Strategies: []compaction.Strategy{
		&testStrategy{fn: excludeOldestTwo},
		&testStrategy{fn: excludeOldestTwo},
	}}
	index := compaction.CreateMessageIndex([]*message.Message{
		textMessage(message.RoleSystem, "system"),
		textMessage(message.RoleUser, "q1"),
		textMessage(message.RoleAssistant, "a1"),
		textMessage(message.RoleUser, "q2"),
		textMessage(message.RoleAssistant, "a2"),
		textMessage(message.RoleUser, "q3"),
	}, nil)

	compacted, err := pipeline.Compact(t.Context(), index)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !compacted {
		t.Fatal("expected compaction")
	}
	if got, want := messageTexts(index.IncludedMessages()), []string{"system", "q3"}; !slices.Equal(got, want) {
		t.Fatalf("unexpected included messages: got %v want %v", got, want)
	}
}

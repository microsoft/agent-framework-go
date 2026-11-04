// Copyright (c) Microsoft. All rights reserved.

package compaction_test

import (
	"testing"

	"github.com/microsoft/agent-framework-go/agent/compaction"
	"github.com/microsoft/agent-framework-go/message"
)

func TestCompactionTriggers_Thresholds(t *testing.T) {
	index := compaction.CreateMessageIndex([]*message.Message{
		textMessage(message.RoleUser, "hello world"),
		textMessage(message.RoleAssistant, "hi"),
		textMessage(message.RoleUser, "again"),
	}, nil)

	if !compaction.TokensExceed(0)(index) {
		t.Fatal("expected tokens to exceed zero")
	}
	if compaction.TokensExceed(999999)(index) {
		t.Fatal("did not expect tokens to exceed large threshold")
	}
	if !compaction.TokensBelow(999999)(index) {
		t.Fatal("expected tokens below large threshold")
	}
	if compaction.TokensBelow(0)(index) {
		t.Fatal("did not expect tokens below zero")
	}
	if !compaction.MessagesExceed(2)(index) {
		t.Fatal("expected message threshold to fire")
	}
	if !compaction.GroupsExceed(2)(index) {
		t.Fatal("expected group threshold to fire")
	}
	if !compaction.TurnsExceed(1)(index) {
		t.Fatal("expected turn threshold to fire")
	}
	if !compaction.Always()(index) || compaction.Never()(index) {
		t.Fatal("unexpected Always/Never trigger result")
	}
}

func TestCompactionTriggers_ToolCallsAndCombinators(t *testing.T) {
	withTool := compaction.CreateMessageIndex([]*message.Message{
		textMessage(message.RoleUser, "q"),
		functionCallMessage("c1", "fn"),
		functionResultMessage("c1", "result"),
	}, nil)
	withoutTool := compaction.CreateMessageIndex([]*message.Message{
		textMessage(message.RoleUser, "q"),
		textMessage(message.RoleAssistant, "a"),
	}, nil)

	if !compaction.HasToolCalls()(withTool) {
		t.Fatal("expected tool call trigger to fire")
	}
	if compaction.HasToolCalls()(withoutTool) {
		t.Fatal("did not expect tool call trigger without tool groups")
	}
	if compaction.All(compaction.TokensExceed(0), compaction.MessagesExceed(99))(withTool) {
		t.Fatal("all should require every trigger")
	}
	if !compaction.Any(compaction.TokensExceed(999999), compaction.MessagesExceed(0))(withTool) {
		t.Fatal("any should require at least one trigger")
	}
	if !compaction.All()(withTool) {
		t.Fatal("empty all should be true")
	}
	if compaction.Any()(withTool) {
		t.Fatal("empty any should be false")
	}
}

func TestCompactionTriggers_TokensExceedReturnsTrueWhenAboveThreshold(t *testing.T) {
	trigger := compaction.TokensExceed(0)
	index := compaction.CreateMessageIndex([]*message.Message{textMessage(message.RoleUser, "Hello world")}, nil)
	if !trigger(index) {
		t.Fatal("tokens should exceed zero")
	}
}

func TestCompactionTriggers_TokensExceedReturnsFalseWhenBelowThreshold(t *testing.T) {
	trigger := compaction.TokensExceed(999_999)
	index := compaction.CreateMessageIndex([]*message.Message{textMessage(message.RoleUser, "Hi")}, nil)
	if trigger(index) {
		t.Fatal("tokens should not exceed 999999")
	}
}

func TestCompactionTriggers_MessagesExceedReturnsExpectedResult(t *testing.T) {
	trigger := compaction.MessagesExceed(2)
	small := compaction.CreateMessageIndex([]*message.Message{
		textMessage(message.RoleUser, "A"),
		textMessage(message.RoleUser, "B"),
	}, nil)
	large := compaction.CreateMessageIndex([]*message.Message{
		textMessage(message.RoleUser, "A"),
		textMessage(message.RoleUser, "B"),
		textMessage(message.RoleUser, "C"),
	}, nil)
	if trigger(small) {
		t.Fatal("two messages should not exceed the threshold")
	}
	if !trigger(large) {
		t.Fatal("three messages should exceed the threshold")
	}
}

func TestCompactionTriggers_TurnsExceedReturnsExpectedResult(t *testing.T) {
	trigger := compaction.TurnsExceed(1)
	oneTurn := compaction.CreateMessageIndex([]*message.Message{
		textMessage(message.RoleUser, "Q1"),
		textMessage(message.RoleAssistant, "A1"),
	}, nil)
	twoTurns := compaction.CreateMessageIndex([]*message.Message{
		textMessage(message.RoleUser, "Q1"),
		textMessage(message.RoleAssistant, "A1"),
		textMessage(message.RoleUser, "Q2"),
	}, nil)
	if trigger(oneTurn) {
		t.Fatal("one turn should not exceed the threshold")
	}
	if !trigger(twoTurns) {
		t.Fatal("two turns should exceed the threshold")
	}
}

func TestCompactionTriggers_GroupsExceedReturnsExpectedResult(t *testing.T) {
	trigger := compaction.GroupsExceed(2)
	index := compaction.CreateMessageIndex([]*message.Message{
		textMessage(message.RoleUser, "A"),
		textMessage(message.RoleAssistant, "B"),
		textMessage(message.RoleUser, "C"),
	}, nil)
	if !trigger(index) {
		t.Fatal("three groups should exceed the threshold")
	}
}

func TestCompactionTriggers_HasToolCallsReturnsTrueWhenToolCallGroupExists(t *testing.T) {
	trigger := compaction.HasToolCalls()
	index := compaction.CreateMessageIndex([]*message.Message{
		textMessage(message.RoleUser, "Q1"),
		functionCallMessage("c1", "fn"),
		textMessage(message.RoleTool, "result"),
	}, nil)
	if !trigger(index) {
		t.Fatal("tool call group should satisfy the trigger")
	}
}

func TestCompactionTriggers_HasToolCallsReturnsFalseWhenNoToolCallGroup(t *testing.T) {
	trigger := compaction.HasToolCalls()
	index := compaction.CreateMessageIndex([]*message.Message{
		textMessage(message.RoleUser, "Hello"),
		textMessage(message.RoleAssistant, "Hi!"),
	}, nil)
	if trigger(index) {
		t.Fatal("text-only conversation should not satisfy the tool call trigger")
	}
}

func TestCompactionTriggers_AllRequiresAllConditions(t *testing.T) {
	trigger := compaction.All(compaction.TokensExceed(0), compaction.MessagesExceed(5))
	small := compaction.CreateMessageIndex([]*message.Message{textMessage(message.RoleUser, "A")}, nil)
	if trigger(small) {
		t.Fatal("All should be false when only the token condition is met")
	}
}

func TestCompactionTriggers_AnyRequiresAtLeastOneCondition(t *testing.T) {
	trigger := compaction.Any(compaction.TokensExceed(999_999), compaction.MessagesExceed(0))
	index := compaction.CreateMessageIndex([]*message.Message{textMessage(message.RoleUser, "A")}, nil)
	if !trigger(index) {
		t.Fatal("Any should be true when the message condition is met")
	}
}

func TestCompactionTriggers_AllEmptyTriggersReturnsTrue(t *testing.T) {
	trigger := compaction.All()
	index := compaction.CreateMessageIndex([]*message.Message{textMessage(message.RoleUser, "A")}, nil)
	if !trigger(index) {
		t.Fatal("empty All should be true")
	}
}

func TestCompactionTriggers_AnyEmptyTriggersReturnsFalse(t *testing.T) {
	trigger := compaction.Any()
	index := compaction.CreateMessageIndex([]*message.Message{textMessage(message.RoleUser, "A")}, nil)
	if trigger(index) {
		t.Fatal("empty Any should be false")
	}
}

func TestCompactionTriggers_TokensBelowReturnsTrueWhenBelowThreshold(t *testing.T) {
	trigger := compaction.TokensBelow(999_999)
	index := compaction.CreateMessageIndex([]*message.Message{textMessage(message.RoleUser, "Hi")}, nil)
	if !trigger(index) {
		t.Fatal("tokens should be below 999999")
	}
}

func TestCompactionTriggers_TokensBelowReturnsFalseWhenAboveThreshold(t *testing.T) {
	trigger := compaction.TokensBelow(0)
	index := compaction.CreateMessageIndex([]*message.Message{textMessage(message.RoleUser, "Hello world")}, nil)
	if trigger(index) {
		t.Fatal("tokens should not be below zero")
	}
}

func TestCompactionTriggers_AlwaysReturnsTrue(t *testing.T) {
	index := compaction.CreateMessageIndex([]*message.Message{textMessage(message.RoleUser, "A")}, nil)
	if !compaction.Always()(index) {
		t.Fatal("Always should be true")
	}
}

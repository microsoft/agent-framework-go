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

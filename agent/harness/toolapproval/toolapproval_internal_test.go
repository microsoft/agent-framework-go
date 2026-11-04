// Copyright (c) Microsoft. All rights reserved.

package toolapproval

import (
	"encoding/json"
	"maps"
	"strings"
	"testing"

	"github.com/microsoft/agent-framework-go/message"
)

func TestToolApprovalRule_NewRule_HasDefaultValues(t *testing.T) {
	var rule approvalRule
	if rule.ToolName != "" || rule.Arguments != nil {
		t.Fatalf("default rule=%+v", rule)
	}
}

func TestToolApprovalRule_ToolName_CanBeSet(t *testing.T) {
	rule := approvalRule{ToolName: "ReadFile"}
	if rule.ToolName != "ReadFile" {
		t.Fatalf("tool name=%q", rule.ToolName)
	}
}

func TestToolApprovalRule_Arguments_CanBeSet(t *testing.T) {
	args := map[string]string{"path": "test.txt"}
	rule := approvalRule{ToolName: "ReadFile", Arguments: args}
	if rule.Arguments == nil || rule.Arguments["path"] != "test.txt" {
		t.Fatalf("arguments=%v", rule.Arguments)
	}
}

func roundTripApprovalRule(t *testing.T, rule approvalRule) approvalRule {
	t.Helper()
	data, err := json.Marshal(rule)
	if err != nil {
		t.Fatal(err)
	}
	var restored approvalRule
	if err := json.Unmarshal(data, &restored); err != nil {
		t.Fatal(err)
	}
	return restored
}

func TestToolApprovalRule_Serialize_ToolLevelRule_RoundTrips(t *testing.T) {
	got := roundTripApprovalRule(t, approvalRule{ToolName: "MyTool"})
	if got.ToolName != "MyTool" || got.Arguments != nil {
		t.Fatalf("restored rule=%+v", got)
	}
}

func TestToolApprovalRule_Serialize_ToolWithArgsRule_RoundTrips(t *testing.T) {
	want := map[string]string{"path": "test.txt", "encoding": "utf-8"}
	got := roundTripApprovalRule(t, approvalRule{ToolName: "ReadFile", Arguments: want})
	if got.ToolName != "ReadFile" || got.Arguments == nil || len(got.Arguments) != 2 || !maps.Equal(got.Arguments, want) {
		t.Fatalf("restored rule=%+v", got)
	}
}

func TestToolApprovalRule_Serialize_EmptyArgsRule_RoundTrips(t *testing.T) {
	got := roundTripApprovalRule(t, approvalRule{ToolName: "SendPayment", Arguments: map[string]string{}})
	if got.ToolName != "SendPayment" || got.Arguments == nil || len(got.Arguments) != 0 {
		t.Fatalf("restored empty-args rule=%+v", got)
	}
}

func TestToolApprovalRule_Serialize_UsesJsonPropertyNames(t *testing.T) {
	data, err := json.Marshal(approvalRule{ToolName: "MyTool", Arguments: map[string]string{"key": "value"}})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"toolName"`) || !strings.Contains(string(data), `"arguments"`) {
		t.Fatalf("serialized rule=%s", data)
	}
}

func TestToolApprovalRule_Serialize_RuleList_RoundTrips(t *testing.T) {
	rules := []approvalRule{{ToolName: "ToolA"}, {ToolName: "ToolB", Arguments: map[string]string{"x": "1"}}}
	data, err := json.Marshal(rules)
	if err != nil {
		t.Fatal(err)
	}
	var restored []approvalRule
	if err := json.Unmarshal(data, &restored); err != nil {
		t.Fatal(err)
	}
	if len(restored) != 2 || restored[0].ToolName != "ToolA" || restored[0].Arguments != nil || restored[1].ToolName != "ToolB" || restored[1].Arguments == nil {
		t.Fatalf("restored rules=%+v", restored)
	}
}

func approvalMatchRequest(name, args string) *message.ToolApprovalRequestContent {
	return &message.ToolApprovalRequestContent{RequestID: "req1", ToolCall: &message.FunctionCallContent{CallID: "call1", Name: name, Arguments: args}}
}

func TestToolApprovalAgent_MatchesRule_ToolLevelRule_MatchesAnyArgs(t *testing.T) {
	if !matchesRule([]approvalRule{{ToolName: "MyTool"}}, approvalMatchRequest("MyTool", `{"x":"1"}`)) {
		t.Fatal("tool-level rule did not match argument-bearing call")
	}
}

func TestToolApprovalAgent_MatchesRule_ToolLevelRule_DoesNotMatchDifferentTool(t *testing.T) {
	if matchesRule([]approvalRule{{ToolName: "ToolA"}}, approvalMatchRequest("ToolB", "")) {
		t.Fatal("tool-level rule matched another tool")
	}
}

func TestToolApprovalAgent_MatchesRule_ToolWithArgsRule_MatchesExactArgs(t *testing.T) {
	if !matchesRule([]approvalRule{{ToolName: "ReadFile", Arguments: map[string]string{"path": `"test.txt"`}}}, approvalMatchRequest("ReadFile", `{"path":"test.txt"}`)) {
		t.Fatal("exact-args rule did not match")
	}
}

func TestToolApprovalAgent_MatchesRule_ToolWithArgsRule_DoesNotMatchDifferentValues(t *testing.T) {
	if matchesRule([]approvalRule{{ToolName: "ReadFile", Arguments: map[string]string{"path": `"test.txt"`}}}, approvalMatchRequest("ReadFile", `{"path":"other.txt"}`)) {
		t.Fatal("exact-args rule matched different values")
	}
}

func TestToolApprovalAgent_MatchesRule_ToolWithArgsRule_DoesNotMatchDifferentArgCount(t *testing.T) {
	if matchesRule([]approvalRule{{ToolName: "ReadFile", Arguments: map[string]string{"path": `"test.txt"`}}}, approvalMatchRequest("ReadFile", `{"path":"test.txt","encoding":"utf-8"}`)) {
		t.Fatal("exact-args rule matched additional arguments")
	}
}

func TestToolApprovalAgent_MatchesRule_NonFunctionCallContent_ReturnsFalse(t *testing.T) {
	request := &message.ToolApprovalRequestContent{RequestID: "req1", ToolCall: &message.WebSearchToolCallContent{CallID: "call1"}}
	if matchesRule([]approvalRule{{ToolName: "MyTool"}}, request) {
		t.Fatal("non-function tool call matched a function rule")
	}
}

func TestToolApprovalAgent_MatchesRule_JsonElementArgs_MatchesCorrectly(t *testing.T) {
	args, err := json.Marshal(map[string]json.RawMessage{"count": json.RawMessage("42")})
	if err != nil {
		t.Fatal(err)
	}
	if !matchesRule([]approvalRule{{ToolName: "MyTool", Arguments: map[string]string{"count": "42"}}}, approvalMatchRequest("MyTool", string(args))) {
		t.Fatal("JSON numeric arguments did not match")
	}
}

func TestToolApprovalAgent_MatchesRule_EmptyArgumentsRule_MatchesEmptyArgumentCall_ReturnsTrue(t *testing.T) {
	if !matchesRule([]approvalRule{{ToolName: "SendPayment", Arguments: map[string]string{}}}, approvalMatchRequest("SendPayment", "")) {
		t.Fatal("empty-args rule did not match empty call")
	}
}

func TestToolApprovalAgent_MatchesRule_EmptyArgumentsRule_DoesNotMatchCallWithArguments_ReturnsFalse(t *testing.T) {
	if matchesRule([]approvalRule{{ToolName: "SendPayment", Arguments: map[string]string{}}}, approvalMatchRequest("SendPayment", `{"recipient":"attacker@example.test","amount":5000}`)) {
		t.Fatal("empty-args rule widened to an argument-bearing call")
	}
}

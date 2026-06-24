// Copyright (c) Microsoft. All rights reserved.

package copilotagent_test

import (
	"bufio"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	copilot "github.com/github/copilot-sdk/go"
	"github.com/github/copilot-sdk/go/rpc"
	agentpkg "github.com/microsoft/agent-framework-go/agent"
	"github.com/microsoft/agent-framework-go/agent/provider/copilotagent"
	"github.com/microsoft/agent-framework-go/message"
	"github.com/microsoft/agent-framework-go/tool"
	"github.com/microsoft/agent-framework-go/tool/functool"
)

func TestConstructor_WithCopilotClient_InitializesPropertiesCorrectly(t *testing.T) {
	agent := copilotagent.New(copilot.NewClient(nil), copilotagent.Config{Config: agentpkg.Config{
		ID:          "test-id",
		Name:        "test-name",
		Description: "test-description",
	}})

	if got := agent.ID(); got != "test-id" {
		t.Fatalf("ID = %q, want test-id", got)
	}
	if got := agent.Name(); got != "test-name" {
		t.Fatalf("Name = %q, want test-name", got)
	}
	if got := agent.Description(); got != "test-description" {
		t.Fatalf("Description = %q, want test-description", got)
	}
}

func TestConstructor_WithNullCopilotClient_Panics(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("New did not panic")
		}
	}()
	copilotagent.New(nil, copilotagent.Config{})
}

func TestConstructor_WithDefaultParameters_UsesBaseProperties(t *testing.T) {
	agent := copilotagent.New(copilot.NewClient(nil), copilotagent.Config{})

	if agent.ID() == "" {
		t.Fatal("ID is empty")
	}
	if got := agent.Name(); got != "GitHub Copilot Agent" {
		t.Fatalf("Name = %q, want GitHub Copilot Agent", got)
	}
	if got := agent.Description(); got != "An AI agent powered by GitHub Copilot" {
		t.Fatalf("Description = %q, want default description", got)
	}
}

func TestCreateSession_ReturnsSession(t *testing.T) {
	agent := copilotagent.New(copilot.NewClient(nil), copilotagent.Config{})

	session, err := agent.CreateSession(context.Background())
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	if session == nil {
		t.Fatal("session is nil")
	}
}

func TestCreateSession_WithSessionID_ReturnsSessionWithSessionID(t *testing.T) {
	agent := copilotagent.New(copilot.NewClient(nil), copilotagent.Config{})

	session, err := agent.CreateSession(context.Background(), agentpkg.WithServiceID("test-session-id"))
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	if got := session.ServiceID(); got != "test-session-id" {
		t.Fatalf("ServiceID = %q, want test-session-id", got)
	}
}

func TestConstructor_WithTools_InitializesCorrectly(t *testing.T) {
	testTool := functool.MustNew(functool.Config{Name: "TestFunc", Description: "Test function"}, func(context.Context, struct{}) (string, error) {
		return "test", nil
	})
	agent := copilotagent.New(copilot.NewClient(nil), copilotagent.Config{Config: agentpkg.Config{Tools: []tool.Tool{testTool}}})

	if agent == nil {
		t.Fatal("agent is nil")
	}
	if agent.ID() == "" {
		t.Fatal("ID is empty")
	}
}

func TestCopySessionConfig_CopiesAllProperties(t *testing.T) {
	runtime := newFakeRuntime(t, idleEvent())
	agent := copilotagent.New(runtime.client(), copilotagent.Config{SessionConfig: richSessionConfig()})

	_, err := runText(t, agent, "hello")
	if err != nil {
		t.Fatalf("RunText: %v", err)
	}
	request := runtime.lastCreateRequest(t)

	assertEqual(t, request["model"], "gpt-4o", "model")
	assertEqual(t, request["reasoningEffort"], "high", "reasoningEffort")
	assertSystemMessage(t, request, "Be helpful")
	assertStringSlice(t, request["availableTools"], []string{"tool1", "tool2"}, "availableTools")
	assertStringSlice(t, request["excludedTools"], []string{"tool3"}, "excludedTools")
	assertEqual(t, request["workingDirectory"], "/workspace", "workingDirectory")
	assertEqual(t, request["configDir"], "/config", "configDir")
	assertEqual(t, request["requestPermission"], true, "requestPermission")
	assertEqual(t, request["requestUserInput"], true, "requestUserInput")
	assertEqual(t, request["hooks"], true, "hooks")
	if request["mcpServers"] == nil {
		t.Fatal("mcpServers was not sent")
	}
	assertStringSlice(t, request["disabledSkills"], []string{"skill1"}, "disabledSkills")
	assertEqual(t, request["streaming"], true, "streaming")
}

func TestCopyResumeSessionConfig_CopiesAllProperties(t *testing.T) {
	runtime := newFakeRuntime(t, idleEvent())
	agent := copilotagent.New(runtime.client(), copilotagent.Config{SessionConfig: richSessionConfig()})
	session, err := agent.CreateSession(context.Background(), agentpkg.WithServiceID("existing-session"))
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	_, err = runText(t, agent, "hello", agentpkg.WithSession(session))
	if err != nil {
		t.Fatalf("RunText: %v", err)
	}
	request := runtime.lastResumeRequest(t)

	assertEqual(t, request["sessionId"], "existing-session", "sessionId")
	assertEqual(t, request["model"], "gpt-4o", "model")
	assertEqual(t, request["reasoningEffort"], "high", "reasoningEffort")
	assertSystemMessage(t, request, "Be helpful")
	assertStringSlice(t, request["availableTools"], []string{"tool1", "tool2"}, "availableTools")
	assertStringSlice(t, request["excludedTools"], []string{"tool3"}, "excludedTools")
	assertEqual(t, request["workingDirectory"], "/workspace", "workingDirectory")
	assertEqual(t, request["configDir"], "/config", "configDir")
	assertEqual(t, request["requestPermission"], true, "requestPermission")
	assertEqual(t, request["requestUserInput"], true, "requestUserInput")
	assertEqual(t, request["hooks"], true, "hooks")
	if request["mcpServers"] == nil {
		t.Fatal("mcpServers was not sent")
	}
	assertStringSlice(t, request["disabledSkills"], []string{"skill1"}, "disabledSkills")
	assertEqual(t, request["streaming"], true, "streaming")
}

func TestCopySessionConfig_WithStreamingDisabled_PreservesStreamingValue(t *testing.T) {
	runtime := newFakeRuntime(t, idleEvent())
	agent := copilotagent.New(runtime.client(), copilotagent.Config{SessionConfig: &copilot.SessionConfig{Streaming: copilot.Bool(false), Model: "gpt-4o"}})

	_, err := runText(t, agent, "hello")
	if err != nil {
		t.Fatalf("RunText: %v", err)
	}
	assertEqual(t, runtime.lastCreateRequest(t)["streaming"], false, "streaming")
}

func TestCopySessionConfig_WithStreamingNull_DefaultsToTrue(t *testing.T) {
	runtime := newFakeRuntime(t, idleEvent())
	agent := copilotagent.New(runtime.client(), copilotagent.Config{SessionConfig: &copilot.SessionConfig{Model: "gpt-4o"}})

	_, err := runText(t, agent, "hello")
	if err != nil {
		t.Fatalf("RunText: %v", err)
	}
	assertEqual(t, runtime.lastCreateRequest(t)["streaming"], true, "streaming")
}

func TestCopyResumeSessionConfig_WithStreamingDisabled_PreservesStreamingValue(t *testing.T) {
	runtime := newFakeRuntime(t, idleEvent())
	agent := copilotagent.New(runtime.client(), copilotagent.Config{SessionConfig: &copilot.SessionConfig{Streaming: copilot.Bool(false), Model: "gpt-4o"}})
	session, err := agent.CreateSession(context.Background(), agentpkg.WithServiceID("existing-session"))
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	_, err = runText(t, agent, "hello", agentpkg.WithSession(session))
	if err != nil {
		t.Fatalf("RunText: %v", err)
	}
	assertEqual(t, runtime.lastResumeRequest(t)["streaming"], false, "streaming")
}

func TestCopyResumeSessionConfig_WithStreamingNull_DefaultsToTrue(t *testing.T) {
	runtime := newFakeRuntime(t, idleEvent())
	agent := copilotagent.New(runtime.client(), copilotagent.Config{SessionConfig: &copilot.SessionConfig{Model: "gpt-4o"}})
	session, err := agent.CreateSession(context.Background(), agentpkg.WithServiceID("existing-session"))
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	_, err = runText(t, agent, "hello", agentpkg.WithSession(session))
	if err != nil {
		t.Fatalf("RunText: %v", err)
	}
	assertEqual(t, runtime.lastResumeRequest(t)["streaming"], true, "streaming")
}

func TestConvertToAgentResponseUpdate_AssistantMessageEventWhenStreaming_DoesNotEmitTextContent(t *testing.T) {
	runtime := newFakeRuntime(t,
		sessionEvent("assistant.message", map[string]any{"messageId": "msg-456", "content": "Some streamed content that was already delivered via delta events"}),
		idleEvent(),
	)
	agent := copilotagent.New(runtime.client(), copilotagent.Config{})

	response, err := runText(t, agent, "hello")
	if err != nil {
		t.Fatalf("RunText: %v", err)
	}
	if got := response.String(); got != "" {
		t.Fatalf("response text = %q, want empty", got)
	}
	_ = firstContent[*message.RawContent](t, response)
}

func TestConvertToAgentResponseUpdate_AssistantMessageEventWhenNotStreaming_EmitsTextContent(t *testing.T) {
	const expected = "Full response text from non-streaming session"
	runtime := newFakeRuntime(t,
		sessionEvent("assistant.message", map[string]any{"messageId": "msg-789", "content": expected}),
		idleEvent(),
	)
	agent := copilotagent.New(runtime.client(), copilotagent.Config{})

	response, err := runText(t, agent, "hello", agentpkg.Stream(false))
	if err != nil {
		t.Fatalf("RunText: %v", err)
	}
	if got := response.String(); got != expected {
		t.Fatalf("response text = %q, want %q", got, expected)
	}
	if text := firstContent[*message.TextContent](t, response); text.Text != expected {
		t.Fatalf("text content = %q, want %q", text.Text, expected)
	}
}

func TestConvertToAgentResponseUpdate_AssistantMessageEventWhenNotStreaming_HandlesEmptyContent(t *testing.T) {
	runtime := newFakeRuntime(t,
		sessionEvent("assistant.message", map[string]any{"messageId": "msg-000", "content": ""}),
		idleEvent(),
	)
	agent := copilotagent.New(runtime.client(), copilotagent.Config{})

	response, err := runText(t, agent, "hello", agentpkg.Stream(false))
	if err != nil {
		t.Fatalf("RunText: %v", err)
	}
	if got := response.String(); got != "" {
		t.Fatalf("response text = %q, want empty", got)
	}
	_ = firstContent[*message.TextContent](t, response)
}

func TestConvertToAgentResponseUpdate_AssistantMessageEventWhenNotStreaming_HandlesNullData(t *testing.T) {
	runtime := newFakeRuntime(t,
		sessionEvent("assistant.message", nil),
		idleEvent(),
	)
	agent := copilotagent.New(runtime.client(), copilotagent.Config{})

	response, err := runText(t, agent, "hello", agentpkg.Stream(false))
	if err != nil {
		t.Fatalf("RunText: %v", err)
	}
	if got := response.String(); got != "" {
		t.Fatalf("response text = %q, want empty", got)
	}
	_ = firstContent[*message.TextContent](t, response)
	if len(response.Messages) == 0 || response.Messages[0].ID != "" {
		t.Fatalf("message ID = %#v, want empty", response.Messages)
	}
}

func TestConvertToAgentResponseUpdate_ToolExecutionStartEvent_ProducesFunctionCallContent(t *testing.T) {
	runtime := newFakeRuntime(t,
		sessionEvent("tool.execution_start", map[string]any{"toolCallId": "call-123", "toolName": "readFile", "arguments": map[string]any{"path": "/tmp/test.txt"}}),
		idleEvent(),
	)
	agent := copilotagent.New(runtime.client(), copilotagent.Config{Config: agentpkg.Config{ID: "agent-1"}})

	response, err := runText(t, agent, "hello")
	if err != nil {
		t.Fatalf("RunText: %v", err)
	}
	if got := response.AgentID; got != "agent-1" {
		t.Fatalf("AgentID = %q, want agent-1", got)
	}
	call := firstContent[*message.FunctionCallContent](t, response)
	if call.CallID != "call-123" || call.Name != "readFile" {
		t.Fatalf("call = (%q, %q), want (call-123, readFile)", call.CallID, call.Name)
	}
	var args map[string]any
	if err := json.Unmarshal([]byte(call.Arguments), &args); err != nil {
		t.Fatalf("unmarshal arguments: %v", err)
	}
	if args["path"] != "/tmp/test.txt" {
		t.Fatalf("path argument = %#v, want /tmp/test.txt", args["path"])
	}
}

func TestConvertToAgentResponseUpdate_ToolExecutionStartEvent_WithNullArguments_ProducesEmptyArguments(t *testing.T) {
	runtime := newFakeRuntime(t,
		sessionEvent("tool.execution_start", map[string]any{"toolCallId": "call-456", "toolName": "listTools", "arguments": nil}),
		idleEvent(),
	)
	agent := copilotagent.New(runtime.client(), copilotagent.Config{})

	response, err := runText(t, agent, "hello")
	if err != nil {
		t.Fatalf("RunText: %v", err)
	}
	call := firstContent[*message.FunctionCallContent](t, response)
	if call.CallID != "call-456" || call.Name != "listTools" {
		t.Fatalf("call = (%q, %q), want (call-456, listTools)", call.CallID, call.Name)
	}
	if call.Arguments != "" {
		t.Fatalf("Arguments = %q, want empty", call.Arguments)
	}
}

func TestConvertToAgentResponseUpdate_ToolExecutionStartEvent_WithNullData_ProducesEmptyFunctionCall(t *testing.T) {
	runtime := newFakeRuntime(t,
		sessionEvent("tool.execution_start", nil),
		idleEvent(),
	)
	agent := copilotagent.New(runtime.client(), copilotagent.Config{})

	response, err := runText(t, agent, "hello")
	if err != nil {
		t.Fatalf("RunText: %v", err)
	}
	call := firstContent[*message.FunctionCallContent](t, response)
	if call.CallID != "" || call.Name != "" || call.Arguments != "" {
		t.Fatalf("call = (%q, %q, %q), want empty fields", call.CallID, call.Name, call.Arguments)
	}
}

func TestConvertToAgentResponseUpdate_ToolExecutionCompleteEvent_WithSuccess_ProducesFunctionResultContent(t *testing.T) {
	const resultJSON = `{"users":[{"name":"Alice"}]}`
	runtime := newFakeRuntime(t,
		sessionEvent("tool.execution_complete", map[string]any{"toolCallId": "call-123", "success": true, "result": map[string]any{"content": resultJSON}}),
		idleEvent(),
	)
	agent := copilotagent.New(runtime.client(), copilotagent.Config{Config: agentpkg.Config{ID: "agent-2"}})

	response, err := runText(t, agent, "hello")
	if err != nil {
		t.Fatalf("RunText: %v", err)
	}
	if got := response.AgentID; got != "agent-2" {
		t.Fatalf("AgentID = %q, want agent-2", got)
	}
	result := firstContent[*message.FunctionResultContent](t, response)
	if result.CallID != "call-123" || result.Result != resultJSON {
		t.Fatalf("result = (%q, %#v), want (call-123, %q)", result.CallID, result.Result, resultJSON)
	}
}

func TestConvertToAgentResponseUpdate_ToolExecutionCompleteEvent_WithError_ProducesErrorResult(t *testing.T) {
	runtime := newFakeRuntime(t,
		sessionEvent("tool.execution_complete", map[string]any{"toolCallId": "call-789", "success": false, "error": map[string]any{"code": "PERMISSION_DENIED", "message": "Access denied to resource"}}),
		idleEvent(),
	)
	agent := copilotagent.New(runtime.client(), copilotagent.Config{})

	response, err := runText(t, agent, "hello")
	if err != nil {
		t.Fatalf("RunText: %v", err)
	}
	result := firstContent[*message.FunctionResultContent](t, response)
	if result.CallID != "call-789" || result.Result != "Access denied to resource" {
		t.Fatalf("result = (%q, %#v), want access denied", result.CallID, result.Result)
	}
}

func TestConvertToAgentResponseUpdate_ToolExecutionCompleteEvent_WithFailureNoError_ProducesDefaultErrorMessage(t *testing.T) {
	runtime := newFakeRuntime(t,
		sessionEvent("tool.execution_complete", map[string]any{"toolCallId": "call-000", "success": false}),
		idleEvent(),
	)
	agent := copilotagent.New(runtime.client(), copilotagent.Config{})

	response, err := runText(t, agent, "hello")
	if err != nil {
		t.Fatalf("RunText: %v", err)
	}
	result := firstContent[*message.FunctionResultContent](t, response)
	if result.CallID != "call-000" || result.Result != "Tool execution failed" {
		t.Fatalf("result = (%q, %#v), want default failure", result.CallID, result.Result)
	}
}

func TestConvertToAgentResponseUpdate_ToolExecutionCompleteEvent_WithNullData_ProducesEmptyResult(t *testing.T) {
	runtime := newFakeRuntime(t,
		sessionEvent("tool.execution_complete", nil),
		idleEvent(),
	)
	agent := copilotagent.New(runtime.client(), copilotagent.Config{})

	response, err := runText(t, agent, "hello")
	if err != nil {
		t.Fatalf("RunText: %v", err)
	}
	result := firstContent[*message.FunctionResultContent](t, response)
	if result.CallID != "" || result.Result != "Tool execution failed" {
		t.Fatalf("result = (%q, %#v), want empty call ID and default failure", result.CallID, result.Result)
	}
}

func TestConvertToAgentResponseUpdate_ToolExecutionCompleteEvent_WithSuccessButNullResult_ProducesNullResult(t *testing.T) {
	runtime := newFakeRuntime(t,
		sessionEvent("tool.execution_complete", map[string]any{"toolCallId": "call-null-result", "success": true, "result": nil}),
		idleEvent(),
	)
	agent := copilotagent.New(runtime.client(), copilotagent.Config{})

	response, err := runText(t, agent, "hello")
	if err != nil {
		t.Fatalf("RunText: %v", err)
	}
	result := firstContent[*message.FunctionResultContent](t, response)
	if result.CallID != "call-null-result" || result.Result != nil {
		t.Fatalf("result = (%q, %#v), want nil", result.CallID, result.Result)
	}
}

func TestConvertToAgentResponseUpdate_ToolExecutionStartEvent_WithEmptyObjectArguments_ProducesEmptyObjectArguments(t *testing.T) {
	runtime := newFakeRuntime(t,
		sessionEvent("tool.execution_start", map[string]any{"toolCallId": "call-empty", "toolName": "noArgsTool", "arguments": map[string]any{}}),
		idleEvent(),
	)
	agent := copilotagent.New(runtime.client(), copilotagent.Config{})

	response, err := runText(t, agent, "hello")
	if err != nil {
		t.Fatalf("RunText: %v", err)
	}
	call := firstContent[*message.FunctionCallContent](t, response)
	if call.CallID != "call-empty" || call.Arguments != "{}" {
		t.Fatalf("call = (%q, %q), want empty object arguments", call.CallID, call.Arguments)
	}
}

func TestConvertToAgentResponseUpdate_ToolExecutionStartEvent_WithMultipleArguments_ParsesAll(t *testing.T) {
	runtime := newFakeRuntime(t,
		sessionEvent("tool.execution_start", map[string]any{"toolCallId": "call-multi", "toolName": "queryTable", "arguments": map[string]any{"table": "incidents", "limit": 10, "filter": "active=true"}}),
		idleEvent(),
	)
	agent := copilotagent.New(runtime.client(), copilotagent.Config{})

	response, err := runText(t, agent, "hello")
	if err != nil {
		t.Fatalf("RunText: %v", err)
	}
	call := firstContent[*message.FunctionCallContent](t, response)
	if call.CallID != "call-multi" || call.Name != "queryTable" {
		t.Fatalf("call = (%q, %q), want call-multi/queryTable", call.CallID, call.Name)
	}
	var args map[string]any
	if err := json.Unmarshal([]byte(call.Arguments), &args); err != nil {
		t.Fatalf("unmarshal arguments: %v", err)
	}
	if args["table"] != "incidents" || args["limit"] != float64(10) || args["filter"] != "active=true" {
		t.Fatalf("arguments = %#v, want all top-level arguments", args)
	}
}

func TestConvertToAgentResponseUpdate_ToolExecutionStartEvent_WithNestedJsonArguments_ParsesTopLevel(t *testing.T) {
	runtime := newFakeRuntime(t,
		sessionEvent("tool.execution_start", map[string]any{"toolCallId": "call-nested", "toolName": "complexTool", "arguments": map[string]any{"config": map[string]any{"timeout": 30}, "name": "test"}}),
		idleEvent(),
	)
	agent := copilotagent.New(runtime.client(), copilotagent.Config{})

	response, err := runText(t, agent, "hello")
	if err != nil {
		t.Fatalf("RunText: %v", err)
	}
	call := firstContent[*message.FunctionCallContent](t, response)
	var args map[string]any
	if err := json.Unmarshal([]byte(call.Arguments), &args); err != nil {
		t.Fatalf("unmarshal arguments: %v", err)
	}
	if args["name"] != "test" || args["config"] == nil {
		t.Fatalf("arguments = %#v, want nested config and name", args)
	}
}

func TestRun_WithSessionError_ReturnsDotNetStyleError(t *testing.T) {
	runtime := newFakeRuntime(t,
		sessionEvent("session.error", map[string]any{"errorType": "query", "message": "Something went wrong"}),
	)
	agent := copilotagent.New(runtime.client(), copilotagent.Config{})

	_, err := runText(t, agent, "hello")
	if err == nil || err.Error() != "session error: Something went wrong" {
		t.Fatalf("err = %v, want session error: Something went wrong", err)
	}
}

func TestRun_WithSessionErrorMissingMessage_ReturnsUnknownError(t *testing.T) {
	runtime := newFakeRuntime(t,
		sessionEvent("session.error", map[string]any{"errorType": "query"}),
	)
	agent := copilotagent.New(runtime.client(), copilotagent.Config{})

	_, err := runText(t, agent, "hello")
	if err == nil || err.Error() != "session error: unknown error" {
		t.Fatalf("err = %v, want session error: unknown error", err)
	}
}

func TestRun_WithBurstOfStreamingEvents_Completes(t *testing.T) {
	const eventCount = 200
	events := make([]map[string]any, 0, eventCount+1)
	var expected strings.Builder
	for range eventCount {
		expected.WriteString("x")
		events = append(events, sessionEvent("assistant.message_delta", map[string]any{"messageId": "msg-1", "deltaContent": "x"}))
	}
	events = append(events, idleEvent())
	runtime := newFakeRuntime(t, events...)
	agent := copilotagent.New(runtime.client(), copilotagent.Config{})

	response, err := runText(t, agent, "hello")
	if err != nil {
		t.Fatalf("RunText: %v", err)
	}
	if got := response.String(); got != expected.String() {
		t.Fatalf("response text length = %d, want %d", len(got), expected.Len())
	}
}

func TestBuildMessageOptions_WithDuplicateAttachmentNames_SendsDistinctPaths(t *testing.T) {
	runtime := newFakeRuntime(t, idleEvent())
	agent := copilotagent.New(runtime.client(), copilotagent.Config{})
	contents := []message.Content{
		dataContent(t, "duplicate.txt", "first"),
		dataContent(t, "duplicate.txt", "second"),
	}

	_, err := agent.Run(context.Background(), []*message.Message{message.New(contents...)}).Collect()
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	attachments := runtime.lastSendAttachments(t)
	if len(attachments) != 2 {
		t.Fatalf("attachments length = %d, want 2", len(attachments))
	}
	if attachments[0]["path"] == attachments[1]["path"] {
		t.Fatalf("attachment paths are equal: %#v", attachments)
	}
	if attachments[0]["displayName"] != "duplicate.txt" || attachments[1]["displayName"] != "duplicate.txt" {
		t.Fatalf("display names = %#v, want duplicate.txt", attachments)
	}
}

func dataContent(t *testing.T, name, value string) *message.DataContent {
	t.Helper()
	return &message.DataContent{
		Name:      name,
		Data:      base64.StdEncoding.EncodeToString([]byte(value)),
		MediaType: "text/plain",
	}
}

func richSessionConfig() *copilot.SessionConfig {
	return &copilot.SessionConfig{
		Model:            "gpt-4o",
		ReasoningEffort:  "high",
		SystemMessage:    &copilot.SystemMessageConfig{Mode: "append", Content: "Be helpful"},
		AvailableTools:   []string{"tool1", "tool2"},
		ExcludedTools:    []string{"tool3"},
		WorkingDirectory: "/workspace",
		ConfigDirectory:  "/config",
		InfiniteSessions: &copilot.InfiniteSessionConfig{},
		OnPermissionRequest: func(copilot.PermissionRequest, copilot.PermissionInvocation) (rpc.PermissionDecision, error) {
			return &rpc.PermissionDecisionApproveOnce{}, nil
		},
		OnUserInputRequest: func(copilot.UserInputRequest, copilot.UserInputInvocation) (copilot.UserInputResponse, error) {
			return copilot.UserInputResponse{Answer: "input"}, nil
		},
		Hooks: &copilot.SessionHooks{
			OnPreToolUse: func(copilot.PreToolUseHookInput, copilot.HookInvocation) (*copilot.PreToolUseHookOutput, error) {
				return nil, nil
			},
		},
		MCPServers: map[string]copilot.MCPServerConfig{
			"server1": copilot.MCPStdioServerConfig{Command: "npx"},
		},
		DisabledSkills: []string{"skill1"},
	}
}

func runText(t *testing.T, a *agentpkg.Agent, prompt string, options ...agentpkg.Option) (*agentpkg.Response, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return a.RunText(ctx, prompt, options...).Collect()
}

func firstContent[T message.Content](t *testing.T, response *agentpkg.Response) T {
	t.Helper()
	for content := range response.Contents() {
		if typed, ok := content.(T); ok {
			return typed
		}
	}
	var zero T
	t.Fatalf("content of type %T not found", zero)
	return zero
}

func sessionEvent(eventType string, data any) map[string]any {
	return map[string]any{
		"id":        "00000000-0000-0000-0000-000000000001",
		"parentId":  nil,
		"timestamp": "2026-01-01T00:00:00Z",
		"type":      eventType,
		"data":      data,
	}
}

func idleEvent() map[string]any {
	return sessionEvent("session.idle", map[string]any{})
}

type fakeRuntime struct {
	t        *testing.T
	listener net.Listener

	mu             sync.Mutex
	sessionID      string
	events         []map[string]any
	createRequests []map[string]any
	resumeRequests []map[string]any
	sendRequests   []map[string]any
}

func newFakeRuntime(t *testing.T, events ...map[string]any) *fakeRuntime {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	runtime := &fakeRuntime{t: t, listener: listener, events: events}
	t.Cleanup(func() { _ = listener.Close() })
	go runtime.accept()
	return runtime
}

func (r *fakeRuntime) client() *copilot.Client {
	return copilot.NewClient(&copilot.ClientOptions{
		Connection: copilot.URIConnection{URL: r.listener.Addr().String()},
	})
}

func (r *fakeRuntime) lastCreateRequest(t *testing.T) map[string]any {
	t.Helper()
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.createRequests) == 0 {
		t.Fatal("no session.create request captured")
	}
	return r.createRequests[len(r.createRequests)-1]
}

func (r *fakeRuntime) lastResumeRequest(t *testing.T) map[string]any {
	t.Helper()
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.resumeRequests) == 0 {
		t.Fatal("no session.resume request captured")
	}
	return r.resumeRequests[len(r.resumeRequests)-1]
}

func (r *fakeRuntime) lastSendAttachments(t *testing.T) []map[string]any {
	t.Helper()
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.sendRequests) == 0 {
		t.Fatal("no session.send request captured")
	}
	attachments, ok := r.sendRequests[len(r.sendRequests)-1]["attachments"].([]any)
	if !ok {
		t.Fatalf("attachments = %#v, want slice", r.sendRequests[len(r.sendRequests)-1]["attachments"])
	}
	out := make([]map[string]any, 0, len(attachments))
	for _, attachment := range attachments {
		typed, ok := attachment.(map[string]any)
		if !ok {
			t.Fatalf("attachment = %#v, want object", attachment)
		}
		out = append(out, typed)
	}
	return out
}

func (r *fakeRuntime) accept() {
	for {
		conn, err := r.listener.Accept()
		if err != nil {
			return
		}
		go r.serve(conn)
	}
}

func (r *fakeRuntime) serve(conn net.Conn) {
	defer func() { _ = conn.Close() }()
	reader := bufio.NewReader(conn)
	for {
		payload, err := readFrame(reader)
		if err != nil {
			if err != io.EOF {
				r.t.Logf("fake runtime read: %v", err)
			}
			return
		}
		var req jsonRPCRequest
		if err := json.Unmarshal(payload, &req); err != nil {
			r.t.Logf("fake runtime unmarshal: %v", err)
			return
		}
		r.handle(conn, req)
	}
}

func (r *fakeRuntime) handle(conn net.Conn, req jsonRPCRequest) {
	switch req.Method {
	case "connect":
		writeResponse(r.t, conn, req.ID, map[string]any{"protocolVersion": copilot.SDKProtocolVersion})
	case "session.create":
		params := decodeParams(r.t, req.Params)
		sessionID, _ := params["sessionId"].(string)
		if sessionID == "" {
			sessionID = "session-1"
		}
		r.mu.Lock()
		r.sessionID = sessionID
		r.createRequests = append(r.createRequests, params)
		r.mu.Unlock()
		writeResponse(r.t, conn, req.ID, map[string]any{"sessionId": sessionID, "workspacePath": ""})
	case "session.resume":
		params := decodeParams(r.t, req.Params)
		sessionID, _ := params["sessionId"].(string)
		r.mu.Lock()
		r.sessionID = sessionID
		r.resumeRequests = append(r.resumeRequests, params)
		r.mu.Unlock()
		writeResponse(r.t, conn, req.ID, map[string]any{"sessionId": sessionID, "workspacePath": ""})
	case "session.send":
		params := decodeParams(r.t, req.Params)
		r.mu.Lock()
		r.sendRequests = append(r.sendRequests, params)
		sessionID := r.sessionID
		events := append([]map[string]any(nil), r.events...)
		r.mu.Unlock()
		writeResponse(r.t, conn, req.ID, map[string]any{"messageId": "sent-message"})
		for _, event := range events {
			writeNotification(r.t, conn, "session.event", map[string]any{"sessionId": sessionID, "event": event})
		}
	case "session.destroy", "runtime.shutdown":
		writeResponse(r.t, conn, req.ID, map[string]any{})
	default:
		writeResponse(r.t, conn, req.ID, map[string]any{})
	}
}

type jsonRPCRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

func decodeParams(t *testing.T, raw json.RawMessage) map[string]any {
	t.Helper()
	var params map[string]any
	if len(raw) == 0 {
		return params
	}
	if err := json.Unmarshal(raw, &params); err != nil {
		t.Fatalf("decode params: %v", err)
	}
	return params
}

func readFrame(reader *bufio.Reader) ([]byte, error) {
	var length int
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			return nil, err
		}
		line = strings.TrimSpace(line)
		if line == "" {
			break
		}
		name, value, ok := strings.Cut(line, ":")
		if !ok {
			return nil, fmt.Errorf("invalid header %q", line)
		}
		if name == "Content-Length" {
			parsed, err := strconv.Atoi(strings.TrimSpace(value))
			if err != nil {
				return nil, err
			}
			length = parsed
		}
	}
	if length == 0 {
		return nil, fmt.Errorf("missing Content-Length")
	}
	payload := make([]byte, length)
	_, err := io.ReadFull(reader, payload)
	return payload, err
}

func writeResponse(t *testing.T, writer io.Writer, id json.RawMessage, result any) {
	t.Helper()
	writeFrame(t, writer, map[string]any{"jsonrpc": "2.0", "id": id, "result": result})
}

func writeNotification(t *testing.T, writer io.Writer, method string, params any) {
	t.Helper()
	writeFrame(t, writer, map[string]any{"jsonrpc": "2.0", "method": method, "params": params})
}

func writeFrame(t *testing.T, writer io.Writer, value any) {
	t.Helper()
	payload, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal frame: %v", err)
	}
	if _, err := fmt.Fprintf(writer, "Content-Length: %d\r\n\r\n", len(payload)); err != nil {
		t.Fatalf("write frame header: %v", err)
	}
	if _, err := writer.Write(payload); err != nil {
		t.Fatalf("write frame payload: %v", err)
	}
}

func assertEqual(t *testing.T, got, want any, name string) {
	t.Helper()
	if got != want {
		t.Fatalf("%s = %#v, want %#v", name, got, want)
	}
}

func assertSystemMessage(t *testing.T, request map[string]any, content string) {
	t.Helper()
	systemMessage, ok := request["systemMessage"].(map[string]any)
	if !ok {
		t.Fatalf("systemMessage = %#v, want object", request["systemMessage"])
	}
	assertEqual(t, systemMessage["content"], content, "systemMessage.content")
}

func assertStringSlice(t *testing.T, got any, want []string, name string) {
	t.Helper()
	gotSlice, ok := got.([]any)
	if !ok {
		t.Fatalf("%s = %#v, want slice", name, got)
	}
	if len(gotSlice) != len(want) {
		t.Fatalf("%s length = %d, want %d", name, len(gotSlice), len(want))
	}
	for i, item := range gotSlice {
		if item != want[i] {
			t.Fatalf("%s[%d] = %#v, want %#v", name, i, item, want[i])
		}
	}
}

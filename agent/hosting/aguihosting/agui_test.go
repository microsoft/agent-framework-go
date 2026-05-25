// Copyright (c) Microsoft. All rights reserved.

package aguihosting_test

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"iter"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/microsoft/agent-framework-go/agent"
	"github.com/microsoft/agent-framework-go/agent/hosting/aguihosting"
	"github.com/microsoft/agent-framework-go/message"
)

func newTestAgent(runFn func(context.Context, []*message.Message, ...agent.Option) iter.Seq2[*agent.ResponseUpdate, error]) *agent.Agent {
	return agent.New(agent.ProviderConfig{Run: runFn}, agent.Config{Name: "test-agent", ID: "test-agent-id"})
}

func TestHandler_MethodNotAllowed(t *testing.T) {
	a := newTestAgent(func(_ context.Context, _ []*message.Message, _ ...agent.Option) iter.Seq2[*agent.ResponseUpdate, error] {
		return func(yield func(*agent.ResponseUpdate, error) bool) {}
	})
	h := aguihosting.NewJSONHTTPHandler(aguihosting.HandlerConfig{Agent: a})

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusMethodNotAllowed)
	}
}

func TestHandler_InvalidInput_ReturnsBadRequest(t *testing.T) {
	a := newTestAgent(func(_ context.Context, _ []*message.Message, _ ...agent.Option) iter.Seq2[*agent.ResponseUpdate, error] {
		return func(yield func(*agent.ResponseUpdate, error) bool) {}
	})
	h := aguihosting.NewJSONHTTPHandler(aguihosting.HandlerConfig{Agent: a})

	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader("{not-json"))
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusBadRequest)
	}
}

func TestHandler_StreamsSSEText(t *testing.T) {
	a := newTestAgent(func(_ context.Context, _ []*message.Message, _ ...agent.Option) iter.Seq2[*agent.ResponseUpdate, error] {
		return func(yield func(*agent.ResponseUpdate, error) bool) {
			yield(&agent.ResponseUpdate{
				MessageID: "msg-1",
				Role:      message.RoleAssistant,
				Contents:  message.Contents{&message.TextContent{Text: "hello agui"}},
			}, nil)
		}
	})
	h := aguihosting.NewJSONHTTPHandler(aguihosting.HandlerConfig{Agent: a})

	body := `{"threadId":"thread-1","runId":"run-1","messages":[{"id":"u1","role":"user","content":"ping"}]}`
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusOK)
	}
	if got := rr.Header().Get("Content-Type"); got != "text/event-stream" {
		t.Fatalf("content-type = %q, want %q", got, "text/event-stream")
	}
	content := rr.Body.String()
	if !strings.Contains(content, "data:") || !strings.Contains(content, "hello agui") {
		t.Fatalf("expected SSE data with text payload, got %q", content)
	}
}

func TestHandler_MixedToolInvocations_OnlyClientToolEmitted(t *testing.T) {
	a := newTestAgent(func(_ context.Context, _ []*message.Message, _ ...agent.Option) iter.Seq2[*agent.ResponseUpdate, error] {
		return func(yield func(*agent.ResponseUpdate, error) bool) {
			yield(&agent.ResponseUpdate{
				MessageID: "msg-1",
				Role:      message.RoleAssistant,
				Contents: message.Contents{
					&message.FunctionCallContent{CallID: "c1", Name: "client_tool", Arguments: `{}`},
					&message.FunctionCallContent{CallID: "c2", Name: "server_tool", Arguments: `{}`},
				},
			}, nil)
		}
	})
	h := aguihosting.NewJSONHTTPHandler(aguihosting.HandlerConfig{Agent: a})

	body := `{"threadId":"thread-1","runId":"run-1","messages":[{"id":"u1","role":"user","content":"ping"}],"tools":[{"name":"client_tool","description":"client","parameters":{"type":"object"}}]}`
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	content := rr.Body.String()
	if !strings.Contains(content, "client_tool") {
		t.Fatalf("expected client tool call in SSE payload, got %q", content)
	}
	if strings.Contains(content, "server_tool") {
		t.Fatalf("did not expect server tool call in mixed invocation payload, got %q", content)
	}
}

func TestHandler_StateSnapshotEmitsStateEvent(t *testing.T) {
	a := newTestAgent(func(_ context.Context, _ []*message.Message, _ ...agent.Option) iter.Seq2[*agent.ResponseUpdate, error] {
		return func(yield func(*agent.ResponseUpdate, error) bool) {
			payload := map[string]any{"counter": 42, "status": "active"}
			b, _ := json.Marshal(payload)
			yield(&agent.ResponseUpdate{
				Role: message.RoleAssistant,
				Contents: message.Contents{&message.DataContent{
					MediaType: "application/json",
					Data:      base64.StdEncoding.EncodeToString(b),
				}},
			}, nil)
		}
	})
	h := aguihosting.NewJSONHTTPHandler(aguihosting.HandlerConfig{Agent: a})

	body := `{"threadId":"thread-1","runId":"run-1","messages":[{"id":"u1","role":"user","content":"ping"}]}`
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	content := rr.Body.String()
	if !strings.Contains(content, "STATE_SNAPSHOT") || !strings.Contains(content, "counter") {
		t.Fatalf("expected state snapshot SSE event, got %q", content)
	}
}

func TestHandler_MixedToolInvocations_SuppressesServerToolResults(t *testing.T) {
	a := newTestAgent(func(_ context.Context, _ []*message.Message, _ ...agent.Option) iter.Seq2[*agent.ResponseUpdate, error] {
		return func(yield func(*agent.ResponseUpdate, error) bool) {
			yield(&agent.ResponseUpdate{
				MessageID: "msg-1",
				Role:      message.RoleAssistant,
				Contents: message.Contents{
					&message.FunctionCallContent{CallID: "c1", Name: "client_tool", Arguments: `{}`},
					&message.FunctionCallContent{CallID: "c2", Name: "server_tool", Arguments: `{}`},
				},
			}, nil)
			yield(&agent.ResponseUpdate{
				MessageID: "msg-1",
				Role:      message.RoleTool,
				Contents: message.Contents{
					&message.FunctionResultContent{CallID: "c1", Result: map[string]any{"ok": true}},
					&message.FunctionResultContent{CallID: "c2", Result: map[string]any{"secret": true}},
				},
			}, nil)
		}
	})
	h := aguihosting.NewJSONHTTPHandler(aguihosting.HandlerConfig{Agent: a})

	body := `{"threadId":"thread-1","runId":"run-1","messages":[{"id":"u1","role":"user","content":"ping"}],"tools":[{"name":"client_tool","description":"client","parameters":{"type":"object"}}]}`
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	content := rr.Body.String()
	if !strings.Contains(content, "\"toolCallId\":\"c1\"") {
		t.Fatalf("expected client tool result in SSE payload, got %q", content)
	}
	if strings.Contains(content, "\"toolCallId\":\"c2\"") {
		t.Fatalf("did not expect filtered server tool result in SSE payload, got %q", content)
	}
}

func TestHandler_ReasoningContent_EmitsReasoningEvents(t *testing.T) {
	a := newTestAgent(func(_ context.Context, _ []*message.Message, _ ...agent.Option) iter.Seq2[*agent.ResponseUpdate, error] {
		return func(yield func(*agent.ResponseUpdate, error) bool) {
			yield(&agent.ResponseUpdate{
				MessageID: "msg-reason-1",
				Role:      message.RoleAssistant,
				Contents: message.Contents{
					&message.TextReasoningContent{Text: "thinking step one"},
				},
			}, nil)
			yield(&agent.ResponseUpdate{
				MessageID: "msg-reason-1",
				Role:      message.RoleAssistant,
				Contents: message.Contents{
					&message.TextReasoningContent{Text: " and step two"},
				},
			}, nil)
			yield(&agent.ResponseUpdate{
				MessageID: "msg-text-1",
				Role:      message.RoleAssistant,
				Contents: message.Contents{
					&message.TextContent{Text: "final answer"},
				},
			}, nil)
		}
	})
	h := aguihosting.NewJSONHTTPHandler(aguihosting.HandlerConfig{Agent: a})

	body := `{"threadId":"thread-1","runId":"run-1","messages":[{"id":"u1","role":"user","content":"ping"}]}`
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	content := rr.Body.String()
	if !strings.Contains(content, "REASONING_MESSAGE_START") {
		t.Fatalf("expected REASONING_MESSAGE_START, got %q", content)
	}
	if !strings.Contains(content, "REASONING_MESSAGE_CONTENT") {
		t.Fatalf("expected REASONING_MESSAGE_CONTENT, got %q", content)
	}
	if !strings.Contains(content, "REASONING_MESSAGE_END") {
		t.Fatalf("expected REASONING_MESSAGE_END, got %q", content)
	}
	reasoningEndIdx := strings.Index(content, "REASONING_MESSAGE_END")
	textStartIdx := strings.Index(content, "TEXT_MESSAGE_START")
	if reasoningEndIdx == -1 || textStartIdx == -1 || reasoningEndIdx >= textStartIdx {
		t.Fatalf("expected REASONING_MESSAGE_END before TEXT_MESSAGE_START, got %q", content)
	}
	if !strings.Contains(content, "thinking step one") {
		t.Fatalf("expected reasoning text in events, got %q", content)
	}
	if !strings.Contains(content, "TEXT_MESSAGE_START") || !strings.Contains(content, "final answer") {
		t.Fatalf("expected text message events after reasoning, got %q", content)
	}
}

func TestHandler_ReasoningContent_WithEncryptedValue(t *testing.T) {
	a := newTestAgent(func(_ context.Context, _ []*message.Message, _ ...agent.Option) iter.Seq2[*agent.ResponseUpdate, error] {
		return func(yield func(*agent.ResponseUpdate, error) bool) {
			yield(&agent.ResponseUpdate{
				MessageID: "msg-enc-1",
				Role:      message.RoleAssistant,
				Contents: message.Contents{
					&message.TextReasoningContent{
						Text:          "hidden reasoning",
						ProtectedData: "encrypted-opaque-token",
					},
				},
			}, nil)
		}
	})
	h := aguihosting.NewJSONHTTPHandler(aguihosting.HandlerConfig{Agent: a})

	body := `{"threadId":"thread-1","runId":"run-1","messages":[{"id":"u1","role":"user","content":"ping"}]}`
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	content := rr.Body.String()
	if !strings.Contains(content, "REASONING_ENCRYPTED_VALUE") {
		t.Fatalf("expected REASONING_ENCRYPTED_VALUE event, got %q", content)
	}
	if !strings.Contains(content, "encrypted-opaque-token") {
		t.Fatalf("expected encrypted value in event, got %q", content)
	}
}

func TestHandler_UnknownDataContent_UsesCurrentMessageLifecycle(t *testing.T) {
	a := newTestAgent(func(_ context.Context, _ []*message.Message, _ ...agent.Option) iter.Seq2[*agent.ResponseUpdate, error] {
		return func(yield func(*agent.ResponseUpdate, error) bool) {
			yield(&agent.ResponseUpdate{
				MessageID: "msg-fallback",
				Role:      message.RoleAssistant,
				Contents: message.Contents{&message.DataContent{
					MediaType: "text/plain",
					Data:      base64.StdEncoding.EncodeToString([]byte("fallback text")),
				}},
			}, nil)
		}
	})
	h := aguihosting.NewJSONHTTPHandler(aguihosting.HandlerConfig{Agent: a})

	body := `{"threadId":"thread-1","runId":"run-1","messages":[{"id":"u1","role":"user","content":"ping"}]}`
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	content := rr.Body.String()
	if !strings.Contains(content, "TEXT_MESSAGE_START") || !strings.Contains(content, "TEXT_MESSAGE_END") {
		t.Fatalf("expected text message lifecycle events for unknown data content, got %q", content)
	}
	if !strings.Contains(content, "msg-fallback") || !strings.Contains(content, "fallback text") {
		t.Fatalf("expected fallback text to use current message id/content, got %q", content)
	}
}

// TestHandler_ToolResult_HasDistinctMessageID verifies that tool result events get a
// deterministic message ID in "result-{callID}" format, distinct from the preceding
// text/tool-call message (mirrors .NET fix in microsoft/agent-framework#5800 and #6009).
func TestHandler_ToolResult_HasDistinctMessageID(t *testing.T) {
	a := newTestAgent(func(_ context.Context, _ []*message.Message, _ ...agent.Option) iter.Seq2[*agent.ResponseUpdate, error] {
		return func(yield func(*agent.ResponseUpdate, error) bool) {
			yield(&agent.ResponseUpdate{
				MessageID: "msg-stream-1",
				Role:      message.RoleAssistant,
				Contents:  message.Contents{&message.TextContent{Text: "Checking weather..."}},
			}, nil)
			yield(&agent.ResponseUpdate{
				MessageID: "msg-stream-1",
				Role:      message.RoleAssistant,
				Contents:  message.Contents{&message.FunctionCallContent{CallID: "call-1", Name: "get_weather", Arguments: `{}`}},
			}, nil)
			yield(&agent.ResponseUpdate{
				MessageID: "msg-stream-1",
				Role:      message.RoleTool,
				Contents:  message.Contents{&message.FunctionResultContent{CallID: "call-1", Result: "72F and sunny"}},
			}, nil)
		}
	})
	h := aguihosting.NewJSONHTTPHandler(aguihosting.HandlerConfig{Agent: a})

	body := `{"threadId":"thread-1","runId":"run-1","messages":[{"id":"u1","role":"user","content":"what is the weather?"}]}`
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	content := rr.Body.String()

	// Tool call result must be present
	if !strings.Contains(content, `"toolCallId":"call-1"`) {
		t.Fatalf("expected tool call result event with toolCallId=call-1, got %q", content)
	}

	// Extract the tool result messageId and the text message start messageId.
	var toolResultMsgID, textStartMsgID string
	for _, line := range strings.Split(content, "\n") {
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		data := strings.TrimPrefix(line, "data: ")
		var evt map[string]any
		if err := json.Unmarshal([]byte(data), &evt); err != nil {
			continue
		}
		switch evt["type"] {
		case "TEXT_MESSAGE_START":
			textStartMsgID, _ = evt["messageId"].(string)
		case "TOOL_CALL_RESULT":
			toolResultMsgID, _ = evt["messageId"].(string)
		}
	}

	if textStartMsgID == "" {
		t.Fatal("expected TEXT_MESSAGE_START event")
	}
	if toolResultMsgID == "" {
		t.Fatal("expected TOOL_CALL_RESULT event")
	}
	// Tool result message ID must be "result-{callID}" — deterministic and distinct.
	if toolResultMsgID != "result-call-1" {
		t.Fatalf("tool result message ID = %q, want %q", toolResultMsgID, "result-call-1")
	}
	if textStartMsgID == toolResultMsgID {
		t.Fatalf("tool result message ID %q must differ from text message ID %q", toolResultMsgID, textStartMsgID)
	}
}

// TestHandler_ParallelToolResults_HaveUniqueMessageIDs verifies that parallel tool
// results each get a unique deterministic message ID (result-{callID}), so the FE
// does not collapse them in React reconciliation (mirrors .NET fix #6009 Bug #2).
func TestHandler_ParallelToolResults_HaveUniqueMessageIDs(t *testing.T) {
	a := newTestAgent(func(_ context.Context, _ []*message.Message, _ ...agent.Option) iter.Seq2[*agent.ResponseUpdate, error] {
		return func(yield func(*agent.ResponseUpdate, error) bool) {
			yield(&agent.ResponseUpdate{
				Role: message.RoleTool,
				Contents: message.Contents{
					&message.FunctionResultContent{CallID: "c1", Result: "result1"},
					&message.FunctionResultContent{CallID: "c2", Result: "result2"},
				},
			}, nil)
		}
	})
	h := aguihosting.NewJSONHTTPHandler(aguihosting.HandlerConfig{Agent: a})

	body := `{"threadId":"t1","runId":"r1","messages":[]}`
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	content := rr.Body.String()

	var resultMsgIDs []string
	for _, line := range strings.Split(content, "\n") {
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		data := strings.TrimPrefix(line, "data: ")
		var evt map[string]any
		if err := json.Unmarshal([]byte(data), &evt); err != nil {
			continue
		}
		if evt["type"] == "TOOL_CALL_RESULT" {
			id, _ := evt["messageId"].(string)
			resultMsgIDs = append(resultMsgIDs, id)
		}
	}

	if len(resultMsgIDs) != 2 {
		t.Fatalf("expected 2 TOOL_CALL_RESULT events, got %d", len(resultMsgIDs))
	}
	if resultMsgIDs[0] != "result-c1" {
		t.Errorf("first result messageId = %q, want %q", resultMsgIDs[0], "result-c1")
	}
	if resultMsgIDs[1] != "result-c2" {
		t.Errorf("second result messageId = %q, want %q", resultMsgIDs[1], "result-c2")
	}
	if resultMsgIDs[0] == resultMsgIDs[1] {
		t.Errorf("parallel tool result message IDs must be unique, both = %q", resultMsgIDs[0])
	}
}

// TestHandler_ConsecutiveAssistantToolCallMessages_Coalesced verifies that consecutive
// assistant messages that each carry tool calls are merged into one message before being
// forwarded to the agent. The AG-UI client produces one assistant message per tool call
// when parentMessageId is absent; sending them separately would trigger HTTP 400 from
// OpenAI because tool_call_ids must be immediately followed by tool results.
// Mirrors .NET fix in microsoft/agent-framework#6009 Bug #3.
func TestHandler_ConsecutiveAssistantToolCallMessages_Coalesced(t *testing.T) {
	var receivedMessages []*message.Message
	a := newTestAgent(func(_ context.Context, msgs []*message.Message, _ ...agent.Option) iter.Seq2[*agent.ResponseUpdate, error] {
		receivedMessages = msgs
		return func(yield func(*agent.ResponseUpdate, error) bool) {}
	})
	h := aguihosting.NewJSONHTTPHandler(aguihosting.HandlerConfig{Agent: a})

	// Two consecutive assistant messages, each with one tool call — simulates what the
	// AG-UI client sends after Bug #1 is fixed on the streaming side.
	body := `{
		"threadId":"t1","runId":"r1",
		"messages":[
			{"id":"u1","role":"user","content":"do both"},
			{"id":"a1","role":"assistant","content":"","toolCalls":[{"id":"c1","type":"function","function":{"name":"tool_a","arguments":"{}"}}]},
			{"id":"a2","role":"assistant","content":"","toolCalls":[{"id":"c2","type":"function","function":{"name":"tool_b","arguments":"{}"}}]},
			{"id":"r1","role":"tool","content":"res1","toolCallId":"c1"},
			{"id":"r2","role":"tool","content":"res2","toolCallId":"c2"}
		]
	}`
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	// Find the coalesced assistant message.
	var assistantMsgs []*message.Message
	for _, m := range receivedMessages {
		if m.Role == message.RoleAssistant {
			assistantMsgs = append(assistantMsgs, m)
		}
	}

	if len(assistantMsgs) != 1 {
		t.Fatalf("expected 1 assistant message after coalescing, got %d", len(assistantMsgs))
	}

	// The coalesced message must contain both tool calls.
	var callIDs []string
	for _, c := range assistantMsgs[0].Contents {
		if fcc, ok := c.(*message.FunctionCallContent); ok {
			callIDs = append(callIDs, fcc.CallID)
		}
	}
	if len(callIDs) != 2 {
		t.Fatalf("expected 2 tool calls in coalesced message, got %d", len(callIDs))
	}
	if callIDs[0] != "c1" || callIDs[1] != "c2" {
		t.Errorf("coalesced tool call IDs = %v, want [c1 c2]", callIDs)
	}
}

// Copyright (c) Microsoft. All rights reserved.

package anthropicprovider_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
	"github.com/microsoft/agent-framework-go/agent"
	"github.com/microsoft/agent-framework-go/agent/harness/toolautocall"
	"github.com/microsoft/agent-framework-go/message"
	"github.com/microsoft/agent-framework-go/provider/anthropicprovider"
	"github.com/microsoft/agent-framework-go/tool"
	"github.com/microsoft/agent-framework-go/tool/functool"
	"github.com/microsoft/agent-framework-go/tool/hostedtool"
)

// testOutput is the structured type used across structured output tests.
type testOutput struct {
	Name string `json:"name"`
	Age  int    `json:"age"`
}

func TestAgent_UnsupportedMessageRoleReturnsError(t *testing.T) {
	a := anthropicprovider.NewAgent(
		anthropic.NewClient(option.WithAPIKey("test")),
		anthropicprovider.AgentConfig{
			Model: "test-model",
		},
	)
	_, err := a.Run(t.Context(), []*message.Message{{Role: message.Role("custom")}}).Collect()
	if err == nil || !strings.Contains(err.Error(), "unsupported message role") {
		t.Fatalf("Run() error = %v, want unsupported message role", err)
	}
}

func newTestClient(t *testing.T, server *httptest.Server) *agent.Agent {
	t.Helper()
	return anthropicprovider.NewAgent(
		anthropic.NewClient(
			option.WithBaseURL(server.URL),
			option.WithAPIKey("test"),
		),
		anthropicprovider.AgentConfig{
			Model: "claude-3-5-sonnet-20241022",
			ToolAutoCall: &toolautocall.Config{
				MaximumIterationsPerRequest: new(0),
			},
		},
	)
}

// nestedKey traverses a decoded JSON map following the given path of keys and
// returns the terminal value and whether it was found.
func nestedKey(m map[string]any, keys ...string) (any, bool) {
	var cur any = m
	for _, k := range keys {
		mm, ok := cur.(map[string]any)
		if !ok {
			return nil, false
		}
		cur, ok = mm[k]
		if !ok {
			return nil, false
		}
	}
	return cur, true
}

// assertOutputConfigFormat checks that the captured request body has an
// output_config.format block of type "json_schema" and, when wantSchema is
// true, that a "schema" field is also present and non-nil.
func assertOutputConfigFormat(t *testing.T, body []byte, wantSchema bool) {
	t.Helper()
	var req map[string]any
	if err := json.Unmarshal(body, &req); err != nil {
		t.Fatalf("unmarshal request body: %v", err)
	}

	fmtType, ok := nestedKey(req, "output_config", "format", "type")
	if !ok {
		t.Error("request missing output_config.format.type")
	} else if fmtType != "json_schema" {
		t.Errorf("output_config.format.type = %q, want %q", fmtType, "json_schema")
	}

	if wantSchema {
		schema, ok := nestedKey(req, "output_config", "format", "schema")
		if !ok {
			t.Error("request missing output_config.format.schema")
		} else if schema == nil {
			t.Error("output_config.format.schema is nil, want non-nil schema")
		}
	}
}

// minimalMessageResponse returns a non-streaming Anthropic messages JSON
// response whose single text block contains the given JSON payload.
func minimalMessageResponse(payload string) string {
	resp := map[string]any{
		"id":            "msg_01XFDUDYJgAACzvnptvVoYEL",
		"type":          "message",
		"role":          "assistant",
		"model":         "claude-3-5-sonnet-20241022",
		"stop_reason":   "end_turn",
		"stop_sequence": nil,
		"content": []any{
			map[string]any{"type": "text", "text": payload},
		},
		"usage": map[string]any{
			"input_tokens":  10,
			"output_tokens": 5,
		},
	}
	b, _ := json.Marshal(resp)
	return string(b)
}

// TestStreamingUsage_DoesNotDoubleCountOutputTokens verifies that streaming
// usage reports the final cumulative output token count from message_delta,
// rather than summing it with the placeholder output count from message_start.
func TestStreamingUsage_DoesNotDoubleCountOutputTokens(t *testing.T) {
	output := "" +
		"event: message_start\n" +
		`data: {"type":"message_start","message":{"id":"m1","type":"message","role":"assistant","content":[],"model":"claude-3-5-sonnet-20241022","stop_reason":null,"stop_sequence":null,"usage":{"input_tokens":10,"output_tokens":1,"cache_creation_input_tokens":3,"cache_read_input_tokens":7,"output_tokens_details":{"thinking_tokens":1}}}}` + "\n\n" +
		"event: content_block_start\n" +
		`data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}` + "\n\n" +
		"event: content_block_delta\n" +
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"hi"}}` + "\n\n" +
		"event: content_block_stop\n" +
		`data: {"type":"content_block_stop","index":0}` + "\n\n" +
		"event: message_delta\n" +
		`data: {"type":"message_delta","delta":{"stop_reason":"end_turn","stop_sequence":null},"usage":{"output_tokens":5,"output_tokens_details":{"thinking_tokens":3}}}` + "\n\n" +
		"event: message_stop\n" +
		`data: {"type":"message_stop"}` + "\n\n"

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, output)
	}))
	defer server.Close()

	resp, err := newTestClient(t, server).RunText(t.Context(), "hi", agent.Stream(true)).Collect()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var usage *message.UsageContent
	for _, msg := range resp.Messages {
		for _, c := range msg.Contents {
			if uc, ok := c.(*message.UsageContent); ok {
				usage = uc
			}
		}
	}
	if usage == nil {
		t.Fatal("expected UsageContent, got none")
	}
	if usage.Details.OutputTokenCount != 5 {
		t.Errorf("OutputTokenCount = %d, want 5 (message_delta final count, not 1+5)", usage.Details.OutputTokenCount)
	}
	if usage.Details.ReasoningTokenCount != 3 {
		t.Errorf("ReasoningTokenCount = %d, want 3", usage.Details.ReasoningTokenCount)
	}
	if usage.Details.InputTokenCount != 20 {
		t.Errorf("InputTokenCount = %d, want 20 (uncached + cache creation + cache read)", usage.Details.InputTokenCount)
	}
	if usage.Details.TotalTokenCount != 25 {
		t.Errorf("TotalTokenCount = %d, want 25", usage.Details.TotalTokenCount)
	}
	if usage.Details.CachedInputTokenCount != 7 {
		t.Errorf("CachedInputTokenCount = %d, want 7", usage.Details.CachedInputTokenCount)
	}
	if got := usage.Details.AdditionalCounts["cache_creation_input_tokens"]; got != 3 {
		t.Errorf("cache_creation_input_tokens = %d, want 3", got)
	}
}

// minimalStreamingResponse returns an SSE stream that delivers payload as a
// single text delta.
func minimalStreamingResponse(payload string) string {
	payloadJSON, _ := json.Marshal(payload)
	return "" +
		"event: message_start\n" +
		`data: {"type":"message_start","message":{"id":"msg_stream01","type":"message","role":"assistant","content":[],"model":"claude-3-5-sonnet-20241022","stop_reason":null,"stop_sequence":null,"usage":{"input_tokens":10,"output_tokens":1}}}` + "\n\n" +
		"event: content_block_start\n" +
		`data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}` + "\n\n" +
		"event: content_block_delta\n" +
		"data: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":" + string(payloadJSON) + "}}\n\n" +
		"event: content_block_stop\n" +
		`data: {"type":"content_block_stop","index":0}` + "\n\n" +
		"event: message_delta\n" +
		`data: {"type":"message_delta","delta":{"stop_reason":"end_turn","stop_sequence":null},"usage":{"output_tokens":5}}` + "\n\n" +
		"event: message_stop\n" +
		`data: {"type":"message_stop"}` + "\n\n"
}

func streamingToolCallResponse(events string) string {
	return "" +
		"event: message_start\n" +
		`data: {"type":"message_start","message":{"id":"msg_tool01","type":"message","role":"assistant","content":[],"model":"claude-3-5-sonnet-20241022","stop_reason":null,"stop_sequence":null,"usage":{"input_tokens":10,"output_tokens":1}}}` + "\n\n" +
		events +
		"event: message_delta\n" +
		`data: {"type":"message_delta","delta":{"stop_reason":"tool_use","stop_sequence":null},"usage":{"output_tokens":5}}` + "\n\n" +
		"event: message_stop\n" +
		`data: {"type":"message_stop"}` + "\n\n"
}

func streamingToolStart(index int, callID, name string) string {
	return "event: content_block_start\n" +
		fmt.Sprintf(`data: {"type":"content_block_start","index":%d,"content_block":{"type":"tool_use","id":%q,"name":%q,"input":{}}}`, index, callID, name) + "\n\n"
}

func streamingToolDelta(index int, partialJSON string) string {
	partial, _ := json.Marshal(partialJSON)
	return "event: content_block_delta\n" +
		fmt.Sprintf(`data: {"type":"content_block_delta","index":%d,"delta":{"type":"input_json_delta","partial_json":%s}}`, index, partial) + "\n\n"
}

func streamingToolStop(index int) string {
	return "event: content_block_stop\n" +
		fmt.Sprintf(`data: {"type":"content_block_stop","index":%d}`, index) + "\n\n"
}

func collectStreamingToolCalls(t *testing.T, events string) []*message.FunctionCallContent {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, streamingToolCallResponse(events))
	}))
	defer server.Close()

	resp, err := newTestClient(t, server).RunText(t.Context(), "call tools", agent.Stream(true)).Collect()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var calls []*message.FunctionCallContent
	for content := range resp.Contents() {
		if call, ok := content.(*message.FunctionCallContent); ok {
			calls = append(calls, call)
		}
	}
	return calls
}

// finishReasonCases enumerates every Anthropic stop_reason the provider maps to
// a canonical FinishReason, exercising the unexported mapStopReason helper end
// to end through the public API.
var finishReasonCases = []struct {
	stopReason string
	want       string
}{
	{"end_turn", "stop"},
	{"stop_sequence", "stop"},
	{"pause_turn", "stop"},
	{"max_tokens", "length"},
	{"tool_use", "tool_calls"},
	{"refusal", "content_filter"},
}

// TestNonStreamingFinishReason verifies the provider maps the Anthropic
// stop_reason on a non-streaming response to the canonical FinishReason.
func TestNonStreamingFinishReason(t *testing.T) {
	for _, tc := range finishReasonCases {
		t.Run(tc.stopReason, func(t *testing.T) {
			body := fmt.Sprintf(`{
				"id":"msg_finish",
				"type":"message",
				"role":"assistant",
				"model":"claude-3-5-sonnet-20241022",
				"stop_reason":%q,
				"stop_sequence":null,
				"content":[{"type":"text","text":"hello"}],
				"usage":{"input_tokens":10,"output_tokens":5}
			}`, tc.stopReason)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(w, body)
			}))
			defer server.Close()

			resp, err := newTestClient(t, server).RunText(t.Context(), "hi").Collect()
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if resp.FinishReason != tc.want {
				t.Errorf("FinishReason = %q, want %q", resp.FinishReason, tc.want)
			}
		})
	}
}

// TestStreamingFinishReason verifies the provider captures the stop_reason from
// the streaming message_delta event and reports it as the canonical
// FinishReason on the collected response.
func TestStreamingFinishReason(t *testing.T) {
	for _, tc := range finishReasonCases {
		t.Run(tc.stopReason, func(t *testing.T) {
			stream := "" +
				"event: message_start\n" +
				`data: {"type":"message_start","message":{"id":"msg_stream_finish","type":"message","role":"assistant","content":[],"model":"claude-3-5-sonnet-20241022","stop_reason":null,"stop_sequence":null,"usage":{"input_tokens":10,"output_tokens":1}}}` + "\n\n" +
				"event: content_block_start\n" +
				`data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}` + "\n\n" +
				"event: content_block_delta\n" +
				`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"partial"}}` + "\n\n" +
				"event: content_block_stop\n" +
				`data: {"type":"content_block_stop","index":0}` + "\n\n" +
				"event: message_delta\n" +
				fmt.Sprintf(`data: {"type":"message_delta","delta":{"stop_reason":%q,"stop_sequence":null},"usage":{"output_tokens":5}}`, tc.stopReason) + "\n\n" +
				"event: message_stop\n" +
				`data: {"type":"message_stop"}` + "\n\n"

			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "text/event-stream")
				_, _ = io.WriteString(w, stream)
			}))
			defer server.Close()

			resp, err := newTestClient(t, server).RunText(t.Context(), "hi", agent.Stream(true)).Collect()
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if resp.FinishReason != tc.want {
				t.Errorf("FinishReason = %q, want %q", resp.FinishReason, tc.want)
			}
		})
	}
}

// TestStreamingFinishReasonNotClobbered verifies that once a non-empty
// stop_reason has been captured from a message_delta, a later message_delta
// carrying an empty stop_reason does not overwrite it. This exercises the guard
// in the streaming path that skips empty stop_reason chunks.
func TestStreamingFinishReasonNotClobbered(t *testing.T) {
	stream := "" +
		"event: message_start\n" +
		`data: {"type":"message_start","message":{"id":"msg_stream_finish","type":"message","role":"assistant","content":[],"model":"claude-3-5-sonnet-20241022","stop_reason":null,"stop_sequence":null,"usage":{"input_tokens":10,"output_tokens":1}}}` + "\n\n" +
		"event: content_block_start\n" +
		`data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}` + "\n\n" +
		"event: content_block_delta\n" +
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"partial"}}` + "\n\n" +
		"event: content_block_stop\n" +
		`data: {"type":"content_block_stop","index":0}` + "\n\n" +
		"event: message_delta\n" +
		`data: {"type":"message_delta","delta":{"stop_reason":"max_tokens","stop_sequence":null},"usage":{"output_tokens":5}}` + "\n\n" +
		"event: message_delta\n" +
		`data: {"type":"message_delta","delta":{"stop_reason":null,"stop_sequence":null},"usage":{"output_tokens":1}}` + "\n\n" +
		"event: message_stop\n" +
		`data: {"type":"message_stop"}` + "\n\n"

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, stream)
	}))
	defer server.Close()

	resp, err := newTestClient(t, server).RunText(t.Context(), "hi", agent.Stream(true)).Collect()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.FinishReason != "length" {
		t.Errorf("FinishReason = %q, want %q", resp.FinishReason, "length")
	}
}

func TestConfigInstructions(t *testing.T) {
	bodyCh := make(chan []byte, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read request body: %v", err)
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		bodyCh <- body
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, minimalMessageResponse("ok"))
	}))
	defer server.Close()

	a := anthropicprovider.NewAgent(
		anthropic.NewClient(
			option.WithBaseURL(server.URL),
			option.WithAPIKey("test"),
		),
		anthropicprovider.AgentConfig{
			Model:        "claude-3-5-sonnet-20241022",
			Instructions: "You are helpful.",
		},
	)

	if _, err := a.RunText(t.Context(), "hi").Collect(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var req map[string]any
	if err := json.Unmarshal(<-bodyCh, &req); err != nil {
		t.Fatalf("unmarshal request body: %v", err)
	}
	system, ok := req["system"].([]any)
	if !ok || len(system) != 1 {
		t.Fatalf("system = %#v, want one text block", req["system"])
	}
	block, _ := system[0].(map[string]any)
	if block["text"] != "You are helpful." {
		t.Fatalf("system text = %q, want %q", block["text"], "You are helpful.")
	}
	messages, _ := req["messages"].([]any)
	if len(messages) != 1 {
		t.Fatalf("messages length = %d, want 1", len(messages))
	}
}

func TestTextCitationsBecomeAnnotations(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{
			"id":"msg_citations",
			"type":"message",
			"role":"assistant",
			"model":"claude-3-5-sonnet-20241022",
			"stop_reason":"end_turn",
			"stop_sequence":null,
			"content":[{
				"type":"text",
				"text":"The answer cites the docs.",
				"citations":[{
					"type":"web_search_result_location",
					"cited_text":"source excerpt",
					"encrypted_index":"enc_123",
					"title":"Example Source",
					"url":"https://example.com/source"
				}]
			}],
			"usage":{"input_tokens":10,"output_tokens":5}
		}`)
	}))
	defer server.Close()

	a := newTestClient(t, server)
	resp, err := a.RunText(t.Context(), "cite something").Collect()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var text *message.TextContent
	for content := range resp.Contents() {
		if tc, ok := content.(*message.TextContent); ok {
			text = tc
			break
		}
	}
	if text == nil {
		t.Fatal("expected text content")
	}
	if len(text.Annotations) != 1 {
		t.Fatalf("annotations length = %d, want 1", len(text.Annotations))
	}
	citation, ok := text.Annotations[0].(*message.CitationAnnotation)
	if !ok {
		t.Fatalf("annotation type = %T, want *message.CitationAnnotation", text.Annotations[0])
	}
	if citation.URL != "https://example.com/source" {
		t.Errorf("citation URL = %q, want %q", citation.URL, "https://example.com/source")
	}
	if citation.Title != "Example Source" {
		t.Errorf("citation Title = %q, want %q", citation.Title, "Example Source")
	}
	if citation.Snippet != "source excerpt" {
		t.Errorf("citation Snippet = %q, want %q", citation.Snippet, "source excerpt")
	}
	if citation.RawRepresentation == nil {
		t.Error("citation RawRepresentation is nil")
	}
}

// TestStreamingTextCitationsBecomeAnnotations mirrors
// TestTextCitationsBecomeAnnotations for the streaming path: citations are only
// present on the accumulated text block, so the content_block_stop handler must
// surface them as annotations. The streamed text arrives via text_delta and the
// citation via a citations_delta.
func TestStreamingTextCitationsBecomeAnnotations(t *testing.T) {
	stream := "" +
		"event: message_start\n" +
		`data: {"type":"message_start","message":{"id":"msg_stream_cite","type":"message","role":"assistant","content":[],"model":"claude-3-5-sonnet-20241022","stop_reason":null,"stop_sequence":null,"usage":{"input_tokens":10,"output_tokens":1}}}` + "\n\n" +
		"event: content_block_start\n" +
		`data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}` + "\n\n" +
		"event: content_block_delta\n" +
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"The answer cites the docs."}}` + "\n\n" +
		"event: content_block_delta\n" +
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"citations_delta","citation":{"type":"web_search_result_location","cited_text":"source excerpt","encrypted_index":"enc_123","title":"Example Source","url":"https://example.com/source"}}}` + "\n\n" +
		"event: content_block_stop\n" +
		`data: {"type":"content_block_stop","index":0}` + "\n\n" +
		"event: message_delta\n" +
		`data: {"type":"message_delta","delta":{"stop_reason":"end_turn","stop_sequence":null},"usage":{"output_tokens":5}}` + "\n\n" +
		"event: message_stop\n" +
		`data: {"type":"message_stop"}` + "\n\n"

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, stream)
	}))
	defer server.Close()

	resp, err := newTestClient(t, server).RunText(t.Context(), "cite something", agent.Stream(true)).Collect()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var citation *message.CitationAnnotation
	var streamedText string
	for content := range resp.Contents() {
		tc, ok := content.(*message.TextContent)
		if !ok {
			continue
		}
		streamedText += tc.Text
		for _, ann := range tc.Annotations {
			if c, ok := ann.(*message.CitationAnnotation); ok {
				citation = c
			}
		}
	}
	if streamedText != "The answer cites the docs." {
		t.Errorf("streamed text = %q, want %q", streamedText, "The answer cites the docs.")
	}
	if citation == nil {
		t.Fatal("expected a citation annotation on the streamed text")
	}
	if citation.URL != "https://example.com/source" {
		t.Errorf("citation URL = %q, want %q", citation.URL, "https://example.com/source")
	}
	if citation.Title != "Example Source" {
		t.Errorf("citation Title = %q, want %q", citation.Title, "Example Source")
	}
	if citation.Snippet != "source excerpt" {
		t.Errorf("citation Snippet = %q, want %q", citation.Snippet, "source excerpt")
	}
}

// TestStructuredOutput_NonStreaming verifies that passing agent.WithStructuredOutput
// with a typed struct causes the provider to:
//  1. Send output_config.format with type "json_schema" and a schema derived
//     from the Go type.
//  2. Unmarshal the returned JSON text into the provided struct.
func TestStructuredOutput_NonStreaming(t *testing.T) {
	const payload = `{"name":"Alice","age":30}`

	bodyCh := make(chan []byte, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read request body: %v", err)
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		bodyCh <- body
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, minimalMessageResponse(payload))
	}))
	defer server.Close()

	a := newTestClient(t, server)

	var out testOutput
	for _, err := range a.RunText(t.Context(), "get user", agent.WithStructuredOutput(&out)) {
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	}

	capturedBody := <-bodyCh
	assertOutputConfigFormat(t, capturedBody, true /* wantSchema */)

	if out.Name != "Alice" {
		t.Errorf("out.Name = %q, want %q", out.Name, "Alice")
	}
	if out.Age != 30 {
		t.Errorf("out.Age = %d, want %d", out.Age, 30)
	}
}

// TestStructuredOutput_Streaming verifies the same guarantees as
// TestStructuredOutput_NonStreaming but with agent.Stream(true).
func TestStructuredOutput_Streaming(t *testing.T) {
	const payload = `{"name":"Bob","age":25}`

	bodyCh := make(chan []byte, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read request body: %v", err)
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		bodyCh <- body
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, minimalStreamingResponse(payload))
	}))
	defer server.Close()

	a := newTestClient(t, server)

	var out testOutput
	for _, err := range a.RunText(t.Context(), "get user", agent.WithStructuredOutput(&out), agent.Stream(true)) {
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	}

	capturedBody := <-bodyCh
	assertOutputConfigFormat(t, capturedBody, true /* wantSchema */)

	// Streaming requests must include stream:true.
	var req map[string]any
	if err := json.Unmarshal(capturedBody, &req); err != nil {
		t.Fatalf("unmarshal request body: %v", err)
	}
	if stream, _ := req["stream"].(bool); !stream {
		t.Error("request missing stream:true")
	}

	if out.Name != "Bob" {
		t.Errorf("out.Name = %q, want %q", out.Name, "Bob")
	}
	if out.Age != 25 {
		t.Errorf("out.Age = %d, want %d", out.Age, 25)
	}
}

func TestStreamingToolCallArgumentsExcludeStartPlaceholder(t *testing.T) {
	events := streamingToolStart(0, "toolu_01", "get_weather") +
		streamingToolDelta(0, `{"city":`) +
		streamingToolDelta(0, `"Seattle"}`) +
		streamingToolStop(0)
	calls := collectStreamingToolCalls(t, events)
	if len(calls) != 1 {
		t.Fatalf("function call count = %d, want 1", len(calls))
	}
	if calls[0].Arguments != `{"city":"Seattle"}` {
		t.Errorf("arguments = %q, want %q", calls[0].Arguments, `{"city":"Seattle"}`)
	}
}

func TestStreamingToolCallAccumulatesFragmentedArguments(t *testing.T) {
	want := `{"name":"tool_value","count":1,"description":"fragmented arguments"}`
	var events strings.Builder
	events.WriteString(streamingToolStart(0, "toolu_fragmented", "fragmented_tool"))
	for i := 0; i < len(want); i += 3 {
		end := min(i+3, len(want))
		events.WriteString(streamingToolDelta(0, want[i:end]))
	}
	events.WriteString(streamingToolStop(0))

	calls := collectStreamingToolCalls(t, events.String())
	if len(calls) != 1 {
		t.Fatalf("function call count = %d, want 1", len(calls))
	}
	if calls[0].Arguments != want {
		t.Errorf("arguments = %q, want %q", calls[0].Arguments, want)
	}
}

func TestStreamingMultipleToolCallsAreNotDuplicated(t *testing.T) {
	var events strings.Builder
	for i, arg := range []string{"a", "b", "c"} {
		events.WriteString(streamingToolStart(i, fmt.Sprintf("toolu_%d", i), fmt.Sprintf("tool_%s", arg)))
		events.WriteString(streamingToolDelta(i, fmt.Sprintf(`{"arg":%q}`, arg)))
		events.WriteString(streamingToolStop(i))
	}

	calls := collectStreamingToolCalls(t, events.String())
	if len(calls) != 3 {
		t.Fatalf("function call count = %d, want 3", len(calls))
	}
	for i, call := range calls {
		want := fmt.Sprintf(`{"arg":%q}`, string(rune('a'+i)))
		if call.Arguments != want {
			t.Errorf("call %d arguments = %q, want %q", i, call.Arguments, want)
		}
	}
}

func TestStreamingToolCallsSupportInterleavedDeltas(t *testing.T) {
	events := streamingToolStart(0, "toolu_alpha", "tool_alpha") +
		streamingToolStart(1, "toolu_beta", "tool_beta") +
		streamingToolStart(2, "toolu_gamma", "tool_gamma") +
		streamingToolDelta(0, `{"city":`) +
		streamingToolDelta(1, `{"query":`) +
		streamingToolDelta(2, `{"id":`) +
		streamingToolDelta(0, `"San Francisco"}`) +
		streamingToolDelta(1, `"weather forecast"}`) +
		streamingToolDelta(2, `123,"active":true}`) +
		streamingToolStop(0) +
		streamingToolStop(1) +
		streamingToolStop(2)

	calls := collectStreamingToolCalls(t, events)
	if len(calls) != 3 {
		t.Fatalf("function call count = %d, want 3", len(calls))
	}
	want := map[string]string{
		"toolu_alpha": `{"city":"San Francisco"}`,
		"toolu_beta":  `{"query":"weather forecast"}`,
		"toolu_gamma": `{"id":123,"active":true}`,
	}
	for _, call := range calls {
		if call.Arguments != want[call.CallID] {
			t.Errorf("call %q arguments = %q, want %q", call.CallID, call.Arguments, want[call.CallID])
		}
	}
}

// When the streaming request faults (here, an immediate HTTP 500 before any
// SSE event) the provider must surface only the error and emit no trailing
// UsageContent update. This matches openaiprovider chat streaming, which yields
// stream.Err() alone on failure; an aggregator/otel middleware counting
// UsageContent would otherwise record phantom usage for a call that never ran.
func TestStreamingFaultEmitsNoUsage(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer server.Close()

	var gotErr error
	var updates int
	for update, err := range newTestClient(t, server).RunText(t.Context(), "hi", agent.Stream(true)) {
		if err != nil {
			gotErr = err
			continue
		}
		updates++
		for _, c := range update.Contents {
			if _, ok := c.(*message.UsageContent); ok {
				t.Error("streaming fault emitted a UsageContent update; want none")
			}
		}
	}
	if gotErr == nil {
		t.Fatal("expected a terminal error from the faulted stream, got nil")
	}
	if updates != 0 {
		t.Errorf("got %d non-error updates, want 0 before the fault", updates)
	}
}

// Building the request must not mutate the caller's MessageNewParams slices.
// The provider appends system instructions to params.System; if it shares the
// caller's backing array (spare capacity), the append corrupts the caller's data.
func TestBuildMessageParams_DoesNotMutateCallerSystemSlice(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":"m","type":"message","role":"assistant","model":"claude","content":[{"type":"text","text":"ok"}],"stop_reason":"end_turn","usage":{"input_tokens":1,"output_tokens":1}}`)
	}))
	defer server.Close()
	a := newTestClient(t, server)

	// Caller-supplied System slice with spare capacity.
	system := make([]anthropic.TextBlockParam, 1, 4)
	system[0] = anthropic.TextBlockParam{Text: "s0"}
	opt := anthropicprovider.MessageNewParams(anthropic.MessageNewParams{System: system})

	if _, err := a.RunText(t.Context(), "hi", agent.WithInstructions("added"), opt).Collect(); err != nil {
		t.Fatalf("run: %v", err)
	}
	if full := system[:cap(system)]; full[1].Text != "" {
		t.Errorf("provider mutated the caller's System backing array: spare slot = %q", full[1].Text)
	}
}

func TestBuildMessageParams_DoesNotMutateCallerMessagesSlice(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":"m","type":"message","role":"assistant","model":"claude","content":[{"type":"text","text":"ok"}],"stop_reason":"end_turn","usage":{"input_tokens":1,"output_tokens":1}}`)
	}))
	defer server.Close()
	a := newTestClient(t, server)

	// Caller-supplied Messages slice with spare capacity. The provider appends
	// the run's messages to params.Messages; with aliasing that append lands in
	// the caller's spare slot instead of a cloned slice.
	messages := make([]anthropic.MessageParam, 1, 4)
	messages[0] = anthropic.NewUserMessage(anthropic.NewTextBlock("seeded"))
	opt := anthropicprovider.MessageNewParams(anthropic.MessageNewParams{Messages: messages})

	if _, err := a.RunText(t.Context(), "hi", opt).Collect(); err != nil {
		t.Fatalf("run: %v", err)
	}
	if full := messages[:cap(messages)]; len(full[1].Content) != 0 {
		t.Errorf("provider mutated the caller's Messages backing array: spare slot has %d content block(s)", len(full[1].Content))
	}
}

// A tool call with empty Arguments must serialize to an object input ({}), not
// null: Anthropic rejects a tool_use block whose input is null.
func TestToolUseEmptyArgumentsSerializeAsObject(t *testing.T) {
	bodyCh := make(chan []byte, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read request body: %v", err)
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		bodyCh <- body
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, minimalMessageResponse("ok"))
	}))
	defer server.Close()

	a := anthropicprovider.NewAgent(
		anthropic.NewClient(option.WithBaseURL(server.URL), option.WithAPIKey("test")),
		anthropicprovider.AgentConfig{
			Model: "claude-3-5-sonnet-20241022",
		},
	)

	msgs := []*message.Message{
		{Role: message.RoleUser, Contents: message.Contents{&message.TextContent{Text: "what time is it?"}}},
		{Role: message.RoleAssistant, Contents: message.Contents{&message.FunctionCallContent{CallID: "toolu_1", Name: "get_time", Arguments: ""}}},
		{Role: message.RoleTool, Contents: message.Contents{&message.FunctionResultContent{CallID: "toolu_1", Result: "12:00"}}},
	}
	if _, err := a.Run(t.Context(), msgs).Collect(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var req map[string]any
	if err := json.Unmarshal(<-bodyCh, &req); err != nil {
		t.Fatalf("unmarshal request body: %v", err)
	}
	messages, ok := req["messages"].([]any)
	if !ok {
		t.Fatalf("request messages = %#v, want a JSON array", req["messages"])
	}
	found := false
	for _, m := range messages {
		msg, ok := m.(map[string]any)
		if !ok {
			continue
		}
		blocks, ok := msg["content"].([]any)
		if !ok {
			continue
		}
		for _, b := range blocks {
			block, ok := b.(map[string]any)
			if !ok {
				continue
			}
			if block["type"] != "tool_use" || block["id"] != "toolu_1" {
				continue
			}
			found = true
			if _, isObject := block["input"].(map[string]any); !isObject {
				t.Errorf("tool_use input = %#v (%T), want an object", block["input"], block["input"])
			}
		}
	}
	if !found {
		t.Fatal("tool_use block for toolu_1 not found in request")
	}
}

// assistantBlocksFromRequest runs the agent against a server that records the
// outbound request body, then returns the decoded content blocks of the first
// assistant message so tests can assert on how prior contents were replayed.
func assistantBlocksFromRequest(t *testing.T, msgs []*message.Message) []map[string]any {
	t.Helper()
	bodyCh := make(chan []byte, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read request body: %v", err)
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		bodyCh <- body
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, minimalMessageResponse("ok"))
	}))
	defer server.Close()

	a := newTestClient(t, server)
	if _, err := a.Run(t.Context(), msgs).Collect(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var req map[string]any
	if err := json.Unmarshal(<-bodyCh, &req); err != nil {
		t.Fatalf("unmarshal request body: %v", err)
	}
	messages, ok := req["messages"].([]any)
	if !ok {
		t.Fatalf("request messages = %#v, want a JSON array", req["messages"])
	}
	for _, m := range messages {
		msg, ok := m.(map[string]any)
		if !ok || msg["role"] != "assistant" {
			continue
		}
		rawBlocks, ok := msg["content"].([]any)
		if !ok {
			t.Fatalf("assistant content = %#v, want a JSON array", msg["content"])
		}
		blocks := make([]map[string]any, 0, len(rawBlocks))
		for _, b := range rawBlocks {
			block, ok := b.(map[string]any)
			if !ok {
				t.Fatalf("content block = %#v, want an object", b)
			}
			blocks = append(blocks, block)
		}
		return blocks
	}
	t.Fatal("no assistant message found in request")
	return nil
}

func TestAssistantReasoningReplayedAsThinkingBlock(t *testing.T) {
	msgs := []*message.Message{
		{Role: message.RoleUser, Contents: message.Contents{&message.TextContent{Text: "what time is it?"}}},
		{Role: message.RoleAssistant, Contents: message.Contents{
			&message.TextReasoningContent{Text: "let me check the clock", ProtectedData: "sig123"},
			&message.FunctionCallContent{CallID: "toolu_1", Name: "get_time", Arguments: "{}"},
		}},
		{Role: message.RoleTool, Contents: message.Contents{&message.FunctionResultContent{CallID: "toolu_1", Result: "12:00"}}},
	}

	blocks := assistantBlocksFromRequest(t, msgs)
	if len(blocks) != 2 {
		t.Fatalf("assistant content blocks = %d, want 2 (%#v)", len(blocks), blocks)
	}

	// Reasoning must come first, carrying its signature, so Anthropic accepts
	// the replayed turn.
	if blocks[0]["type"] != "thinking" {
		t.Errorf("first block type = %v, want %q", blocks[0]["type"], "thinking")
	}
	if blocks[0]["thinking"] != "let me check the clock" {
		t.Errorf("thinking = %v, want %q", blocks[0]["thinking"], "let me check the clock")
	}
	if blocks[0]["signature"] != "sig123" {
		t.Errorf("signature = %v, want %q", blocks[0]["signature"], "sig123")
	}
	if blocks[1]["type"] != "tool_use" {
		t.Errorf("second block type = %v, want %q", blocks[1]["type"], "tool_use")
	}
}

func TestAssistantRedactedReasoningReplayedAsRedactedThinkingBlock(t *testing.T) {
	msgs := []*message.Message{
		{Role: message.RoleUser, Contents: message.Contents{&message.TextContent{Text: "hi"}}},
		{Role: message.RoleAssistant, Contents: message.Contents{
			&message.TextReasoningContent{ProtectedData: "redacted-data"},
			&message.TextContent{Text: "hello"},
		}},
	}

	blocks := assistantBlocksFromRequest(t, msgs)
	if len(blocks) != 2 {
		t.Fatalf("assistant content blocks = %d, want 2 (%#v)", len(blocks), blocks)
	}
	if blocks[0]["type"] != "redacted_thinking" {
		t.Errorf("first block type = %v, want %q", blocks[0]["type"], "redacted_thinking")
	}
	if blocks[0]["data"] != "redacted-data" {
		t.Errorf("data = %v, want %q", blocks[0]["data"], "redacted-data")
	}
}

func TestAssistantUnsignedReasoningIsSkipped(t *testing.T) {
	msgs := []*message.Message{
		{Role: message.RoleUser, Contents: message.Contents{&message.TextContent{Text: "hi"}}},
		{Role: message.RoleAssistant, Contents: message.Contents{
			&message.TextReasoningContent{Text: "partial with no signature"},
			&message.TextContent{Text: "hello"},
		}},
	}

	blocks := assistantBlocksFromRequest(t, msgs)
	if len(blocks) != 1 {
		t.Fatalf("assistant content blocks = %d, want 1 (%#v)", len(blocks), blocks)
	}
	if blocks[0]["type"] != "text" {
		t.Errorf("block type = %v, want %q", blocks[0]["type"], "text")
	}
}

// The tool's input_schema sent to Anthropic must carry the additionalProperties
// keyword emitted by functool's strict schema. functool.Call validates decoded
// arguments against the resolved schema (additionalProperties:false), so if the
// model-facing schema omits it the model can hallucinate an extra argument that
// passes the model but is rejected Go-side. OpenAI and Gemini forward the full
// schema; this keeps the Anthropic path in parity.
func TestToolInputSchemaCarriesAdditionalProperties(t *testing.T) {
	type getWeatherInput struct {
		City string `json:"city"`
	}
	weatherTool := functool.MustNew(functool.Config{
		Name:        "get_weather",
		Description: "Gets the weather for a city.",
	}, func(_ context.Context, in getWeatherInput) (string, error) {
		return "sunny", nil
	})

	bodyCh := make(chan []byte, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read request body: %v", err)
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		bodyCh <- body
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, minimalMessageResponse("ok"))
	}))
	defer server.Close()

	if _, err := newTestClient(t, server).RunText(
		t.Context(), "what's the weather?",
		agent.WithTool(weatherTool),
		agent.WithToolMode(tool.RequireTool("get_weather")),
	).Collect(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var req map[string]any
	if err := json.Unmarshal(<-bodyCh, &req); err != nil {
		t.Fatalf("unmarshal request body: %v", err)
	}
	tools, ok := req["tools"].([]any)
	if !ok || len(tools) == 0 {
		t.Fatalf("request tools = %#v, want a non-empty JSON array", req["tools"])
	}
	toolObj, ok := tools[0].(map[string]any)
	if !ok {
		t.Fatalf("tools[0] = %#v, want a JSON object", tools[0])
	}
	addl, ok := nestedKey(toolObj, "input_schema", "additionalProperties")
	if !ok {
		t.Fatal("tool input_schema is missing additionalProperties")
	}
	if addl != false {
		t.Errorf("input_schema.additionalProperties = %#v, want false", addl)
	}
	toolChoice, ok := req["tool_choice"].(map[string]any)
	if !ok {
		t.Fatalf("tool_choice = %#v, want a JSON object", req["tool_choice"])
	}
	if toolChoice["type"] != "tool" || toolChoice["name"] != "get_weather" {
		t.Errorf("tool_choice = %#v, want specific get_weather tool", toolChoice)
	}
}

// A URIContent image URL and a DataContent application/pdf must be forwarded to
// Anthropic as an image block with a URL source and a document block. Before the
// fix these inputs fell through the content switch and were silently dropped,
// diverging from the OpenAI chat provider which maps all three multimodal inputs.
func TestBuildMessageParam_ImageURLAndPDFAreForwarded(t *testing.T) {
	bodyCh := make(chan []byte, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read request body: %v", err)
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		bodyCh <- body
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, minimalMessageResponse("ok"))
	}))
	defer server.Close()

	a := newTestClient(t, server)

	msgs := []*message.Message{
		{Role: message.RoleUser, Contents: message.Contents{
			&message.URIContent{URI: "https://example.com/cat.png", MediaType: "image/png"},
			&message.DataContent{Data: "JVBERi0xLjQK", MediaType: "application/pdf"},
		}},
	}
	if _, err := a.Run(t.Context(), msgs).Collect(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var req map[string]any
	if err := json.Unmarshal(<-bodyCh, &req); err != nil {
		t.Fatalf("unmarshal request body: %v", err)
	}
	messages, ok := req["messages"].([]any)
	if !ok {
		t.Fatalf("request messages = %#v, want a JSON array", req["messages"])
	}

	var imageURL, documentBase64 bool
	for _, m := range messages {
		msg, ok := m.(map[string]any)
		if !ok {
			continue
		}
		blocks, ok := msg["content"].([]any)
		if !ok {
			continue
		}
		for _, b := range blocks {
			block, ok := b.(map[string]any)
			if !ok {
				continue
			}
			source, _ := block["source"].(map[string]any)
			switch block["type"] {
			case "image":
				if source["type"] == "url" && source["url"] == "https://example.com/cat.png" {
					imageURL = true
				}
			case "document":
				if source["type"] == "base64" && source["media_type"] == "application/pdf" && source["data"] == "JVBERi0xLjQK" {
					documentBase64 = true
				}
			}
		}
	}
	if !imageURL {
		t.Error("image block with a URL source not found in request")
	}
	if !documentBase64 {
		t.Error("document block with a base64 application/pdf source not found in request")
	}
}

// A PDF media type that carries parameters or non-canonical casing (e.g.
// "application/PDF; charset=binary") must still be recognized and forwarded as a
// document block, not dropped.
func TestBuildMessageParam_PDFMediaTypeWithParametersIsForwarded(t *testing.T) {
	bodyCh := make(chan []byte, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read request body: %v", err)
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		bodyCh <- body
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, minimalMessageResponse("ok"))
	}))
	defer server.Close()

	a := newTestClient(t, server)

	msgs := []*message.Message{
		{Role: message.RoleUser, Contents: message.Contents{
			&message.DataContent{Data: "JVBERi0xLjQK", MediaType: "application/PDF; charset=binary"},
		}},
	}
	if _, err := a.Run(t.Context(), msgs).Collect(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var req map[string]any
	if err := json.Unmarshal(<-bodyCh, &req); err != nil {
		t.Fatalf("unmarshal request body: %v", err)
	}
	messages, ok := req["messages"].([]any)
	if !ok {
		t.Fatalf("request messages = %#v, want a JSON array", req["messages"])
	}

	var documentBase64 bool
	for _, m := range messages {
		msg, ok := m.(map[string]any)
		if !ok {
			continue
		}
		blocks, ok := msg["content"].([]any)
		if !ok {
			continue
		}
		for _, b := range blocks {
			block, ok := b.(map[string]any)
			if !ok {
				continue
			}
			source, _ := block["source"].(map[string]any)
			if block["type"] == "document" && source["type"] == "base64" && source["data"] == "JVBERi0xLjQK" {
				documentBase64 = true
			}
		}
	}
	if !documentBase64 {
		t.Error("document block for a PDF media type with parameters not found in request")
	}
}

// A HostedFileContent cannot be represented by the stable Messages API, so the
// request must fail with an explicit error rather than silently dropping it.
func TestBuildMessageParam_HostedFileContentReturnsError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("request should not be sent when a hosted file reference is present")
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, minimalMessageResponse("ok"))
	}))
	defer server.Close()

	a := newTestClient(t, server)

	msgs := []*message.Message{
		{Role: message.RoleUser, Contents: message.Contents{
			&message.HostedFileContent{FileID: "file_123"},
		}},
	}
	_, err := a.Run(t.Context(), msgs).Collect()
	if err == nil {
		t.Fatal("expected an error for a hosted file reference, got nil")
	}
	if !strings.Contains(err.Error(), "file_123") {
		t.Errorf("error = %v, want it to mention the offending file id", err)
	}
}

// A hosted WebSearch tool must be mapped to the Anthropic web_search_20250305
// tool request, with MaxUses/AllowedDomains/UserLocation carried across from
// AdditionalProperties. This mirrors the OpenAI providers and the Python
// agent_framework_anthropic client (get_web_search_tool -> web_search_20250305).
func TestWebSearchHostedToolMapping(t *testing.T) {
	bodyCh := make(chan []byte, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read request body: %v", err)
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		bodyCh <- body
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, minimalMessageResponse("ok"))
	}))
	defer server.Close()

	ws := &hostedtool.WebSearch{
		AdditionalProperties: map[string]any{
			"max_uses":        5,
			"allowed_domains": []string{"example.com", "docs.example.com"},
			"user_location": map[string]string{
				"city":    "Seattle",
				"country": "US",
			},
		},
	}

	if _, err := newTestClient(t, server).RunText(t.Context(), "search the web", agent.WithTool(ws)).Collect(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var req map[string]any
	if err := json.Unmarshal(<-bodyCh, &req); err != nil {
		t.Fatalf("unmarshal request body: %v", err)
	}
	tools, ok := req["tools"].([]any)
	if !ok || len(tools) != 1 {
		t.Fatalf("tools = %#v, want one tool", req["tools"])
	}
	tl, _ := tools[0].(map[string]any)
	if tl["type"] != "web_search_20250305" {
		t.Fatalf("tool type = %v, want web_search_20250305", tl["type"])
	}
	if got := tl["max_uses"]; got != float64(5) {
		t.Errorf("max_uses = %#v, want 5", got)
	}
	domains, _ := tl["allowed_domains"].([]any)
	if len(domains) != 2 || domains[0] != "example.com" || domains[1] != "docs.example.com" {
		t.Errorf("allowed_domains = %#v, want [example.com docs.example.com]", tl["allowed_domains"])
	}
	if _, present := tl["blocked_domains"]; present {
		t.Errorf("blocked_domains should be omitted, got %#v", tl["blocked_domains"])
	}
	loc, ok := tl["user_location"].(map[string]any)
	if !ok {
		t.Fatalf("user_location = %#v, want an object", tl["user_location"])
	}
	if loc["city"] != "Seattle" {
		t.Errorf("user_location.city = %v, want Seattle", loc["city"])
	}
	if loc["country"] != "US" {
		t.Errorf("user_location.country = %v, want US", loc["country"])
	}
	if _, present := loc["region"]; present {
		t.Errorf("user_location.region should be omitted, got %#v", loc["region"])
	}
}

// A hosted WebSearch tool with no AdditionalProperties must still map to the
// web_search_20250305 request, with all optional fields omitted.
func TestWebSearchHostedToolMappingWithoutProperties(t *testing.T) {
	bodyCh := make(chan []byte, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read request body: %v", err)
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		bodyCh <- body
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, minimalMessageResponse("ok"))
	}))
	defer server.Close()

	if _, err := newTestClient(t, server).RunText(t.Context(), "search the web", agent.WithTool(&hostedtool.WebSearch{})).Collect(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var req map[string]any
	if err := json.Unmarshal(<-bodyCh, &req); err != nil {
		t.Fatalf("unmarshal request body: %v", err)
	}
	tools, ok := req["tools"].([]any)
	if !ok || len(tools) != 1 {
		t.Fatalf("tools = %#v, want one tool", req["tools"])
	}
	tl, _ := tools[0].(map[string]any)
	if tl["type"] != "web_search_20250305" {
		t.Fatalf("tool type = %v, want web_search_20250305", tl["type"])
	}
	for _, k := range []string{"max_uses", "allowed_domains", "blocked_domains", "user_location"} {
		if _, present := tl[k]; present {
			t.Errorf("%s should be omitted, got %#v", k, tl[k])
		}
	}
}

// findToolResultBlock returns the tool_result block for the given call ID from
// an Anthropic messages request body, or nil if not present.
func findToolResultBlock(t *testing.T, body []byte, callID string) map[string]any {
	t.Helper()
	var req map[string]any
	if err := json.Unmarshal(body, &req); err != nil {
		t.Fatalf("unmarshal request body: %v", err)
	}
	messages, ok := req["messages"].([]any)
	if !ok {
		t.Fatalf("request messages = %#v, want a JSON array", req["messages"])
	}
	for _, m := range messages {
		msg, ok := m.(map[string]any)
		if !ok {
			continue
		}
		blocks, ok := msg["content"].([]any)
		if !ok {
			continue
		}
		for _, b := range blocks {
			block, ok := b.(map[string]any)
			if !ok {
				continue
			}
			if block["type"] == "tool_result" && block["tool_use_id"] == callID {
				return block
			}
		}
	}
	return nil
}

func runWithToolResult(t *testing.T, result *message.FunctionResultContent) []byte {
	t.Helper()
	bodyCh := make(chan []byte, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read request body: %v", err)
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		bodyCh <- body
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, minimalMessageResponse("ok"))
	}))
	defer server.Close()

	a := anthropicprovider.NewAgent(
		anthropic.NewClient(option.WithBaseURL(server.URL), option.WithAPIKey("test")),
		anthropicprovider.AgentConfig{
			Model: "claude-3-5-sonnet-20241022",
		},
	)

	msgs := []*message.Message{
		{Role: message.RoleUser, Contents: message.Contents{&message.TextContent{Text: "what time is it?"}}},
		{Role: message.RoleAssistant, Contents: message.Contents{&message.FunctionCallContent{CallID: result.CallID, Name: "get_time", Arguments: ""}}},
		{Role: message.RoleTool, Contents: message.Contents{result}},
	}
	if _, err := a.Run(t.Context(), msgs).Collect(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	return <-bodyCh
}

func TestToolResultErrorSendsErrorText(t *testing.T) {
	body := runWithToolResult(t, &message.FunctionResultContent{
		CallID: "toolu_err",
		Error:  errors.New("boom"),
	})

	block := findToolResultBlock(t, body, "toolu_err")
	if block == nil {
		t.Fatal("tool_result block for toolu_err not found in request")
	}
	if isErr, _ := block["is_error"].(bool); !isErr {
		t.Errorf("tool_result is_error = %#v, want true", block["is_error"])
	}
	content, _ := json.Marshal(block["content"])
	if !strings.Contains(string(content), "boom") {
		t.Errorf("tool_result content = %s, want it to contain the error text %q", content, "boom")
	}
	if string(content) == "null" {
		t.Errorf("tool_result content = %s, want the error text and not the literal null", content)
	}
}

func TestToolResultSuccessSendsResult(t *testing.T) {
	body := runWithToolResult(t, &message.FunctionResultContent{
		CallID: "toolu_ok",
		Result: "12:00",
	})

	block := findToolResultBlock(t, body, "toolu_ok")
	if block == nil {
		t.Fatal("tool_result block for toolu_ok not found in request")
	}
	if isErr, _ := block["is_error"].(bool); isErr {
		t.Errorf("tool_result is_error = %#v, want false", block["is_error"])
	}
	content, _ := json.Marshal(block["content"])
	if !strings.Contains(string(content), "12:00") {
		t.Errorf("tool_result content = %s, want it to contain the result %q", content, "12:00")
	}
}

// countingReadCloser counts Close calls on an HTTP response body.
type countingReadCloser struct {
	io.ReadCloser
	closes *atomic.Int64
}

func (c *countingReadCloser) Close() error {
	c.closes.Add(1)
	return c.ReadCloser.Close()
}

// closeCountingTransport wraps each response body so tests can assert the
// streaming HTTP body is released once the run completes.
type closeCountingTransport struct {
	base   http.RoundTripper
	closes *atomic.Int64
}

func (t *closeCountingTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	resp, err := t.base.RoundTrip(req)
	if err != nil || resp == nil {
		return resp, err
	}
	resp.Body = &countingReadCloser{ReadCloser: resp.Body, closes: t.closes}
	return resp, nil
}

// TestStreamingClosesResponseBody verifies the streaming path releases the HTTP
// response body when the consumer stops iterating early. Without an explicit
// stream.Close(), the body is never returned to the pool, leaking the
// underlying connection. This mirrors the defer-close already present on the
// Chat Completions streaming path and matches the .NET/Python SDKs, which
// dispose the streaming response on early enumeration.
func TestStreamingClosesResponseBody(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, minimalStreamingResponse("hello world"))
	}))
	defer server.Close()

	var closes atomic.Int64
	httpClient := &http.Client{Transport: &closeCountingTransport{base: http.DefaultTransport, closes: &closes}}
	a := anthropicprovider.NewAgent(
		anthropic.NewClient(
			option.WithBaseURL(server.URL),
			option.WithAPIKey("test"),
			option.WithHTTPClient(httpClient),
		),
		anthropicprovider.AgentConfig{
			Model: "claude-3-5-sonnet-20241022",
		},
	)

	// Stop iterating after the first streamed update. The provider's run
	// closure then returns via yield=false, which must close the body.
	for _, err := range a.RunText(t.Context(), "hi", agent.Stream(true)) {
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		break
	}

	if got := closes.Load(); got == 0 {
		t.Fatal("streaming response body was not closed after early consumer exit")
	}
}

// A URIContent carrying a data: URI must be decoded and forwarded to Anthropic
// as an inline base64 block, not passed through as a URL source (which the API
// rejects). Mirrors the DataContent branch and the Gemini/OpenAI data: handling.
func TestBuildMessageParam_DataURIImageForwardedAsBase64(t *testing.T) {
	bodyCh := make(chan []byte, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read request body: %v", err)
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		bodyCh <- body
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, minimalMessageResponse("ok"))
	}))
	defer server.Close()

	a := newTestClient(t, server)
	msgs := []*message.Message{
		{Role: message.RoleUser, Contents: message.Contents{
			&message.URIContent{URI: "data:image/png;base64,aGVsbG8=", MediaType: "image/png"},
		}},
	}
	if _, err := a.Run(t.Context(), msgs).Collect(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	body := <-bodyCh
	var req map[string]any
	if err := json.Unmarshal(body, &req); err != nil {
		t.Fatalf("unmarshal request body: %v", err)
	}
	messages, _ := req["messages"].([]any)
	var base64Image bool
	for _, m := range messages {
		msg, _ := m.(map[string]any)
		blocks, _ := msg["content"].([]any)
		for _, b := range blocks {
			block, _ := b.(map[string]any)
			source, _ := block["source"].(map[string]any)
			if block["type"] == "image" && source["type"] == "base64" && source["media_type"] == "image/png" && source["data"] == "aGVsbG8=" {
				base64Image = true
			}
		}
	}
	if !base64Image {
		t.Fatalf("expected data: URIContent image forwarded as a base64 image source, got: %s", body)
	}
}

// Copyright (c) Microsoft. All rights reserved.

package anthropicprovider_test

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
	"github.com/microsoft/agent-framework-go/agent"
	"github.com/microsoft/agent-framework-go/message"
	"github.com/microsoft/agent-framework-go/provider/anthropicprovider"
)

// testOutput is the structured type used across structured output tests.
type testOutput struct {
	Name string `json:"name"`
	Age  int    `json:"age"`
}

func newTestClient(t *testing.T, server *httptest.Server) *agent.Agent {
	t.Helper()
	return anthropicprovider.NewAgent(
		anthropic.NewClient(
			option.WithBaseURL(server.URL),
			option.WithAPIKey("test"),
		),
		anthropicprovider.AgentConfig{
			Model:  "claude-3-5-sonnet-20241022",
			Config: agent.Config{DisableFuncAutoCall: true},
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
		`data: {"type":"message_start","message":{"id":"m1","type":"message","role":"assistant","content":[],"model":"claude-3-5-sonnet-20241022","stop_reason":null,"stop_sequence":null,"usage":{"input_tokens":10,"output_tokens":1}}}` + "\n\n" +
		"event: content_block_start\n" +
		`data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}` + "\n\n" +
		"event: content_block_delta\n" +
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"hi"}}` + "\n\n" +
		"event: content_block_stop\n" +
		`data: {"type":"content_block_stop","index":0}` + "\n\n" +
		"event: message_delta\n" +
		`data: {"type":"message_delta","delta":{"stop_reason":"end_turn","stop_sequence":null},"usage":{"output_tokens":5}}` + "\n\n" +
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
	if usage.Details.InputTokenCount != 10 {
		t.Errorf("InputTokenCount = %d, want 10", usage.Details.InputTokenCount)
	}
	if usage.Details.TotalTokenCount != 15 {
		t.Errorf("TotalTokenCount = %d, want 15", usage.Details.TotalTokenCount)
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
			Config: agent.Config{
				DisableFuncAutoCall: true,
			},
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
			Model:  "claude-3-5-sonnet-20241022",
			Config: agent.Config{DisableFuncAutoCall: true},
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

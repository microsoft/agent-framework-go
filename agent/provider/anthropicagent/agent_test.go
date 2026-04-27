// Copyright (c) Microsoft. All rights reserved.

package anthropicagent_test

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
	"github.com/microsoft/agent-framework-go/agent"
	"github.com/microsoft/agent-framework-go/agent/provider/anthropicagent"
)

// testOutput is the structured type used across structured output tests.
type testOutput struct {
	Name string `json:"name"`
	Age  int    `json:"age"`
}

func newTestClient(t *testing.T, server *httptest.Server) *agent.Agent {
	t.Helper()
	return anthropicagent.New(
		anthropic.NewClient(
			option.WithBaseURL(server.URL),
			option.WithAPIKey("test"),
		),
		anthropicagent.Config{
			Model:  "claude-3-5-sonnet-20241022",
			Config: agent.Config{DisableFuncAutoCall: true},
		})
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

// Copyright (c) Microsoft. All rights reserved.

package geminiagent_test

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/microsoft/agent-framework-go/agent"
	"github.com/microsoft/agent-framework-go/agent/provider/geminiagent"
	"github.com/microsoft/agent-framework-go/agentopt"
	"github.com/microsoft/agent-framework-go/message"
	"github.com/microsoft/agent-framework-go/tool"
	"github.com/microsoft/agent-framework-go/tool/functool"
	"google.golang.org/genai"
)

const testModel = "gemini-test"

// testOutput is the structured type used across structured output tests.
type testOutput struct {
	Name string `json:"name"`
	Age  int    `json:"age"`
}

func newTestClient(t *testing.T, server *httptest.Server) *agent.Agent {
	t.Helper()
	client, err := genai.NewClient(t.Context(), &genai.ClientConfig{
		Backend: genai.BackendGeminiAPI,
		APIKey:  "test",
		HTTPOptions: genai.HTTPOptions{
			BaseURL: server.URL,
		},
	})
	if err != nil {
		t.Fatalf("genai.NewClient: %v", err)
	}
	a, err := geminiagent.New(t.Context(), geminiagent.Config{
		Model:  testModel,
		Client: client,
		Agent:  agent.Config{DisableFuncAutoCall: true},
	})
	if err != nil {
		t.Fatalf("geminiagent.New: %v", err)
	}
	return a
}

// captureAndRespond returns a handler that saves the request body to bodyCh and
// writes responseBody as the HTTP response.
func captureAndRespond(t *testing.T, bodyCh chan<- []byte, contentType, responseBody string) http.HandlerFunc {
	t.Helper()
	return func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read request body: %v", err)
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		bodyCh <- body
		w.Header().Set("Content-Type", contentType)
		io.WriteString(w, responseBody)
	}
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

// minimalTextResponse returns a non-streaming Gemini generateContent JSON
// response whose single text part contains text.
func minimalTextResponse(text string) string {
	resp := map[string]any{
		"candidates": []any{
			map[string]any{
				"content": map[string]any{
					"role":  "model",
					"parts": []any{map[string]any{"text": text}},
				},
				"finishReason": "STOP",
			},
		},
		"usageMetadata": map[string]any{
			"promptTokenCount":     10,
			"candidatesTokenCount": 5,
			"totalTokenCount":      15,
		},
	}
	b, _ := json.Marshal(resp)
	return string(b)
}

// minimalStreamingResponse returns an SSE stream that delivers text across two
// chunks, with usage metadata in the final chunk.
func minimalStreamingResponse(text string) string {
	chunk := func(m map[string]any) string {
		b, _ := json.Marshal(m)
		return "data:" + string(b) + "\n\n"
	}
	return chunk(map[string]any{
		"candidates": []any{
			map[string]any{
				"content": map[string]any{
					"role":  "model",
					"parts": []any{map[string]any{"text": text}},
				},
			},
		},
	}) + chunk(map[string]any{
		"usageMetadata": map[string]any{
			"promptTokenCount":     10,
			"candidatesTokenCount": 5,
			"totalTokenCount":      15,
		},
	})
}

// TestBasicText_NonStreaming verifies that a simple user message results in the
// assistant's text content being returned.
func TestBasicText_NonStreaming(t *testing.T) {
	const wantText = "Hello, world!"
	server := httptest.NewServer(captureAndRespond(t, make(chan []byte, 1), "application/json", minimalTextResponse(wantText)))
	defer server.Close()

	a := newTestClient(t, server)

	resp, err := a.RunText(t.Context(), "hi").Collect()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := resp.String(); got != wantText {
		t.Errorf("response text = %q, want %q", got, wantText)
	}
}

// TestBasicText_Streaming verifies that streaming reassembles text chunks into
// the final response text.
func TestBasicText_Streaming(t *testing.T) {
	const wantText = "Hello!"
	server := httptest.NewServer(captureAndRespond(t, make(chan []byte, 1), "text/event-stream", minimalStreamingResponse(wantText)))
	defer server.Close()

	a := newTestClient(t, server)

	resp, err := a.RunText(t.Context(), "hi", agentopt.Stream(true)).Collect()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := resp.String(); got != wantText {
		t.Errorf("response text = %q, want %q", got, wantText)
	}
}

// TestStructuredOutput_NonStreaming verifies that passing agentopt.StructuredOutput
// causes the provider to:
//  1. Send generationConfig with responseMimeType "application/json" and a
//     responseJsonSchema derived from the Go type.
//  2. Unmarshal the returned JSON text into the provided struct.
func TestStructuredOutput_NonStreaming(t *testing.T) {
	const payload = `{"name":"Alice","age":30}`

	bodyCh := make(chan []byte, 1)
	server := httptest.NewServer(captureAndRespond(t, bodyCh, "application/json", minimalTextResponse(payload)))
	defer server.Close()

	a := newTestClient(t, server)

	var out testOutput
	for _, err := range a.RunText(t.Context(), "get user", agentopt.StructuredOutput(&out)) {
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	}

	assertStructuredOutputRequest(t, <-bodyCh)

	if out.Name != "Alice" {
		t.Errorf("out.Name = %q, want %q", out.Name, "Alice")
	}
	if out.Age != 30 {
		t.Errorf("out.Age = %d, want 30", out.Age)
	}
}

// TestStructuredOutput_Streaming verifies the same guarantees as
// TestStructuredOutput_NonStreaming but with agentopt.Stream(true).
func TestStructuredOutput_Streaming(t *testing.T) {
	const payload = `{"name":"Bob","age":25}`

	bodyCh := make(chan []byte, 1)
	server := httptest.NewServer(captureAndRespond(t, bodyCh, "text/event-stream", minimalStreamingResponse(payload)))
	defer server.Close()

	a := newTestClient(t, server)

	var out testOutput
	for _, err := range a.RunText(t.Context(), "get user", agentopt.StructuredOutput(&out), agentopt.Stream(true)) {
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	}

	assertStructuredOutputRequest(t, <-bodyCh)

	if out.Name != "Bob" {
		t.Errorf("out.Name = %q, want %q", out.Name, "Bob")
	}
	if out.Age != 25 {
		t.Errorf("out.Age = %d, want 25", out.Age)
	}
}

// assertStructuredOutputRequest checks that the captured request body has a
// generationConfig with responseMimeType "application/json" and a non-nil
// responseJsonSchema.
func assertStructuredOutputRequest(t *testing.T, body []byte) {
	t.Helper()
	var req map[string]any
	if err := json.Unmarshal(body, &req); err != nil {
		t.Fatalf("unmarshal request body: %v", err)
	}

	mime, ok := nestedKey(req, "generationConfig", "responseMimeType")
	if !ok {
		t.Error("request missing generationConfig.responseMimeType")
	} else if mime != "application/json" {
		t.Errorf("generationConfig.responseMimeType = %q, want \"application/json\"", mime)
	}

	schema, ok := nestedKey(req, "generationConfig", "responseJsonSchema")
	if !ok {
		t.Error("request missing generationConfig.responseJsonSchema")
	} else if schema == nil {
		t.Error("generationConfig.responseJsonSchema is nil, want non-nil schema")
	}
}

// TestSystemInstruction verifies that a system-role message is sent as the
// systemInstruction field rather than in the contents array.
func TestSystemInstruction(t *testing.T) {
	bodyCh := make(chan []byte, 1)
	server := httptest.NewServer(captureAndRespond(t, bodyCh, "application/json", minimalTextResponse("ok")))
	defer server.Close()

	a := newTestClient(t, server)

	messages := []*message.Message{
		{Role: message.RoleSystem, Contents: []message.Content{&message.TextContent{Text: "You are helpful."}}},
		{Role: message.RoleUser, Contents: []message.Content{&message.TextContent{Text: "hi"}}},
	}
	_, err := a.Run(t.Context(), messages).Collect()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var req map[string]any
	if err := json.Unmarshal(<-bodyCh, &req); err != nil {
		t.Fatalf("unmarshal request body: %v", err)
	}
	// System message must appear as systemInstruction, not in contents.
	si, hasSI := nestedKey(req, "systemInstruction", "parts")
	if !hasSI {
		t.Error("request missing systemInstruction.parts")
	} else {
		parts, _ := si.([]any)
		if len(parts) == 0 {
			t.Error("systemInstruction.parts is empty")
		} else if firstPart, ok := parts[0].(map[string]any); !ok {
			t.Errorf("systemInstruction.parts[0] is not a JSON object, got %T", parts[0])
		} else if text, _ := nestedKey(firstPart, "text"); text != "You are helpful." {
			t.Errorf("systemInstruction.parts[0].text = %q, want %q", text, "You are helpful.")
		}
	}
	contents, _ := req["contents"].([]any)
	for _, c := range contents {
		cm, _ := c.(map[string]any)
		if role, _ := cm["role"].(string); role == "system" {
			t.Error("system message must not appear in contents array")
		}
	}
}

// TestToolCall_NonStreaming verifies that tool definitions are sent in the
// request and that a functionCall part in the response is translated into a
// FunctionCallContent.
func TestToolCall_NonStreaming(t *testing.T) {
	weatherTool := functool.MustNew(&functool.Func{
		Name:        "get_weather",
		Description: "Get the weather for a city.",
	}, func(_ tool.Context, args struct{ City string }) (string, error) {
		return "sunny", nil
	})

	funcCallResp := map[string]any{
		"candidates": []any{
			map[string]any{
				"content": map[string]any{
					"role": "model",
					"parts": []any{
						map[string]any{
							"functionCall": map[string]any{
								"id":   "call_abc123",
								"name": "get_weather",
								"args": map[string]any{"City": "Seattle"},
							},
						},
					},
				},
				"finishReason": "STOP",
			},
		},
	}
	respBody, _ := json.Marshal(funcCallResp)

	bodyCh := make(chan []byte, 1)
	server := httptest.NewServer(captureAndRespond(t, bodyCh, "application/json", string(respBody)))
	defer server.Close()

	a := newTestClient(t, server)

	resp, err := a.RunText(t.Context(), "what's the weather?", agentopt.Tool(weatherTool)).Collect()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify tools were included in the request with the correct declaration.
	var req map[string]any
	if err := json.Unmarshal(<-bodyCh, &req); err != nil {
		t.Fatalf("unmarshal request body: %v", err)
	}
	// Gemini API key is "tools" → array of Tool objects
	toolsAny, ok := req["tools"].([]any)
	if !ok || len(toolsAny) == 0 {
		t.Fatal("request missing tools or tools is not an array")
	}
	firstTool, ok := toolsAny[0].(map[string]any)
	if !ok {
		t.Fatal("tools[0] is not an object")
	}
	decls, ok := firstTool["functionDeclarations"].([]any)
	if !ok || len(decls) == 0 {
		t.Fatal("tools[0].functionDeclarations is missing, not an array, or empty")
	}
	decl, ok := decls[0].(map[string]any)
	if !ok {
		t.Fatal("tools[0].functionDeclarations[0] is not an object")
	}
	if name, _ := decl["name"].(string); name != "get_weather" {
		t.Errorf("tools[0].functionDeclarations[0].name = %q, want %q", name, "get_weather")
	}

	// Verify the response contains a FunctionCallContent.
	var gotFuncCall *message.FunctionCallContent
	for _, msg := range resp.Messages {
		for _, c := range msg.Contents {
			if fc, ok := c.(*message.FunctionCallContent); ok {
				gotFuncCall = fc
			}
		}
	}
	if gotFuncCall == nil {
		t.Fatal("expected FunctionCallContent in response, got none")
	}
	if gotFuncCall.Name != "get_weather" {
		t.Errorf("FunctionCallContent.Name = %q, want %q", gotFuncCall.Name, "get_weather")
	}
	if gotFuncCall.CallID != "call_abc123" {
		t.Errorf("FunctionCallContent.CallID = %q, want %q", gotFuncCall.CallID, "call_abc123")
	}
	var args map[string]any
	if err := json.Unmarshal([]byte(gotFuncCall.Arguments), &args); err != nil {
		t.Fatalf("unmarshal arguments: %v", err)
	}
	if args["City"] != "Seattle" {
		t.Errorf("arguments[\"City\"] = %v, want \"Seattle\"", args["City"])
	}
}

// TestUsageContent verifies that usageMetadata from the response is translated
// into a UsageContent with the correct token counts.
func TestUsageContent(t *testing.T) {
	server := httptest.NewServer(captureAndRespond(t, make(chan []byte, 1), "application/json", minimalTextResponse("hi")))
	defer server.Close()

	a := newTestClient(t, server)

	resp, err := a.RunText(t.Context(), "hello").Collect()
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
		t.Fatal("expected UsageContent in response, got none")
	}
	if usage.Details.InputTokenCount != 10 {
		t.Errorf("InputTokenCount = %d, want 10", usage.Details.InputTokenCount)
	}
	if usage.Details.OutputTokenCount != 5 {
		t.Errorf("OutputTokenCount = %d, want 5", usage.Details.OutputTokenCount)
	}
	if usage.Details.TotalTokenCount != 15 {
		t.Errorf("TotalTokenCount = %d, want 15", usage.Details.TotalTokenCount)
	}
}

// Copyright (c) Microsoft. All rights reserved.

package geminiprovider_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/microsoft/agent-framework-go/agent"
	"github.com/microsoft/agent-framework-go/message"
	"github.com/microsoft/agent-framework-go/provider/geminiprovider"
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
	return geminiprovider.NewAgent(client, geminiprovider.AgentConfig{
		Model:  testModel,
		Config: agent.Config{DisableFuncAutoCall: true},
	})
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
		_, _ = io.WriteString(w, responseBody)
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

	resp, err := a.RunText(t.Context(), "hi", agent.Stream(true)).Collect()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := resp.String(); got != wantText {
		t.Errorf("response text = %q, want %q", got, wantText)
	}
}

// TestStructuredOutput_NonStreaming verifies that passing agent.WithStructuredOutput
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
	for _, err := range a.RunText(t.Context(), "get user", agent.WithStructuredOutput(&out)) {
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
// TestStructuredOutput_NonStreaming but with agent.Stream(true).
func TestStructuredOutput_Streaming(t *testing.T) {
	const payload = `{"name":"Bob","age":25}`

	bodyCh := make(chan []byte, 1)
	server := httptest.NewServer(captureAndRespond(t, bodyCh, "text/event-stream", minimalStreamingResponse(payload)))
	defer server.Close()

	a := newTestClient(t, server)

	var out testOutput
	for _, err := range a.RunText(t.Context(), "get user", agent.WithStructuredOutput(&out), agent.Stream(true)) {
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

func TestConfigInstructions(t *testing.T) {
	bodyCh := make(chan []byte, 1)
	server := httptest.NewServer(captureAndRespond(t, bodyCh, "application/json", minimalTextResponse("ok")))
	defer server.Close()

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
	a := geminiprovider.NewAgent(client, geminiprovider.AgentConfig{
		Model:        testModel,
		Instructions: "You are helpful.",
		Config: agent.Config{
			DisableFuncAutoCall: true,
		},
	})

	_, err = a.RunText(t.Context(), "hi").Collect()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var req map[string]any
	if err := json.Unmarshal(<-bodyCh, &req); err != nil {
		t.Fatalf("unmarshal request body: %v", err)
	}
	si, hasSI := nestedKey(req, "systemInstruction", "parts")
	if !hasSI {
		t.Fatal("request missing systemInstruction.parts")
	}
	parts, _ := si.([]any)
	if len(parts) != 1 {
		t.Fatalf("systemInstruction.parts length = %d, want 1", len(parts))
	}
	firstPart, _ := parts[0].(map[string]any)
	if text, _ := nestedKey(firstPart, "text"); text != "You are helpful." {
		t.Fatalf("systemInstruction text = %q, want %q", text, "You are helpful.")
	}
	contents, _ := req["contents"].([]any)
	if len(contents) != 1 {
		t.Fatalf("contents length = %d, want 1", len(contents))
	}
}

// TestToolCall_NonStreaming verifies that tool definitions are sent in the
// request and that a functionCall part in the response is translated into a
// FunctionCallContent.
func TestToolCall_NonStreaming(t *testing.T) {
	weatherTool := functool.MustNew(functool.Config{
		Name:        "get_weather",
		Description: "Get the weather for a city.",
	}, func(_ context.Context, args struct{ City string }) (string, error) {
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

	resp, err := a.RunText(t.Context(), "what's the weather?", agent.WithTool(weatherTool)).Collect()
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
	responseSchema, ok := decl["responseJsonSchema"].(map[string]any)
	if !ok {
		t.Fatalf("tools[0].functionDeclarations[0].responseJsonSchema = %T, want object", decl["responseJsonSchema"])
	}
	if responseType, _ := responseSchema["type"].(string); responseType != "string" {
		t.Errorf("responseJsonSchema.type = %q, want %q", responseType, "string")
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

// TestUsageContent_ReasoningTokens verifies that Gemini's thoughtsTokenCount is
// surfaced as ReasoningTokenCount, mirroring the OpenAI providers, instead of
// being dropped from usage accounting. thoughtsTokenCount is a separate bucket
// from candidatesTokenCount within totalTokenCount, so omitting it leaves the
// usage record internally inconsistent for thinking models.
func TestUsageContent_ReasoningTokens(t *testing.T) {
	resp := map[string]any{
		"candidates": []any{
			map[string]any{
				"content": map[string]any{
					"role":  "model",
					"parts": []any{map[string]any{"text": "hi"}},
				},
				"finishReason": "STOP",
			},
		},
		"usageMetadata": map[string]any{
			"promptTokenCount":     10,
			"candidatesTokenCount": 5,
			"thoughtsTokenCount":   8,
			"totalTokenCount":      23,
		},
	}
	body, _ := json.Marshal(resp)

	server := httptest.NewServer(captureAndRespond(t, make(chan []byte, 1), "application/json", string(body)))
	defer server.Close()

	a := newTestClient(t, server)

	out, err := a.RunText(t.Context(), "hello").Collect()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var usage *message.UsageContent
	for _, msg := range out.Messages {
		for _, c := range msg.Contents {
			if uc, ok := c.(*message.UsageContent); ok {
				usage = uc
			}
		}
	}
	if usage == nil {
		t.Fatal("expected UsageContent in response, got none")
	}
	if usage.Details.ReasoningTokenCount != 8 {
		t.Errorf("ReasoningTokenCount = %d, want 8", usage.Details.ReasoningTokenCount)
	}
	if usage.Details.OutputTokenCount != 5 {
		t.Errorf("OutputTokenCount = %d, want 5", usage.Details.OutputTokenCount)
	}
	if usage.Details.TotalTokenCount != 23 {
		t.Errorf("TotalTokenCount = %d, want 23", usage.Details.TotalTokenCount)
	}
}

// TestMultiTurnConversation verifies that multi-turn messages are sent to the
// API in the correct order with the correct roles.
func TestMultiTurnConversation(t *testing.T) {
	bodyCh := make(chan []byte, 1)
	server := httptest.NewServer(captureAndRespond(t, bodyCh, "application/json", minimalTextResponse("6")))
	defer server.Close()

	a := newTestClient(t, server)

	messages := []*message.Message{
		{Role: message.RoleUser, Contents: []message.Content{&message.TextContent{Text: "What is 2+2?"}}},
		{Role: message.RoleAssistant, Contents: []message.Content{&message.TextContent{Text: "4"}}},
		{Role: message.RoleUser, Contents: []message.Content{&message.TextContent{Text: "And what is 3+3?"}}},
	}
	resp, err := a.Run(t.Context(), messages).Collect()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := resp.String(); got != "6" {
		t.Errorf("response text = %q, want %q", got, "6")
	}

	var req map[string]any
	if err := json.Unmarshal(<-bodyCh, &req); err != nil {
		t.Fatalf("unmarshal request body: %v", err)
	}
	contents, _ := req["contents"].([]any)
	if len(contents) != 3 {
		t.Fatalf("expected 3 contents, got %d", len(contents))
	}
	roles := make([]string, len(contents))
	for i, c := range contents {
		cm, _ := c.(map[string]any)
		roles[i], _ = cm["role"].(string)
	}
	wantRoles := []string{"user", "model", "user"}
	for i, want := range wantRoles {
		if roles[i] != want {
			t.Errorf("contents[%d].role = %q, want %q", i, roles[i], want)
		}
	}
}

// TestMultipleSystemMessages verifies that multiple system text parts are sent
// in systemInstruction.
func TestMultipleSystemMessages(t *testing.T) {
	bodyCh := make(chan []byte, 1)
	server := httptest.NewServer(captureAndRespond(t, bodyCh, "application/json", minimalTextResponse("ok")))
	defer server.Close()

	a := newTestClient(t, server)

	messages := []*message.Message{
		{Role: message.RoleSystem, Contents: []message.Content{
			&message.TextContent{Text: "You are helpful."},
			&message.TextContent{Text: "You are concise."},
		}},
		{Role: message.RoleUser, Contents: []message.Content{&message.TextContent{Text: "Tell me something"}}},
	}
	_, err := a.Run(t.Context(), messages).Collect()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var req map[string]any
	if err := json.Unmarshal(<-bodyCh, &req); err != nil {
		t.Fatalf("unmarshal request body: %v", err)
	}
	si, ok := nestedKey(req, "systemInstruction", "parts")
	if !ok {
		t.Fatal("request missing systemInstruction.parts")
	}
	parts, _ := si.([]any)
	if len(parts) != 2 {
		t.Fatalf("systemInstruction.parts length = %d, want 2", len(parts))
	}
	for i, want := range []string{"You are helpful.", "You are concise."} {
		p, _ := parts[i].(map[string]any)
		if text, _ := p["text"].(string); text != want {
			t.Errorf("systemInstruction.parts[%d].text = %q, want %q", i, text, want)
		}
	}
}

// TestMultipleContentParts verifies that a user message with multiple text
// parts sends all parts in the request.
func TestMultipleContentParts(t *testing.T) {
	bodyCh := make(chan []byte, 1)
	server := httptest.NewServer(captureAndRespond(t, bodyCh, "application/json", minimalTextResponse("ok")))
	defer server.Close()

	a := newTestClient(t, server)

	messages := []*message.Message{
		{Role: message.RoleUser, Contents: []message.Content{
			&message.TextContent{Text: "First part."},
			&message.TextContent{Text: "Second part."},
		}},
	}
	_, err := a.Run(t.Context(), messages).Collect()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var req map[string]any
	if err := json.Unmarshal(<-bodyCh, &req); err != nil {
		t.Fatalf("unmarshal request body: %v", err)
	}
	contents, _ := req["contents"].([]any)
	if len(contents) != 1 {
		t.Fatalf("expected 1 content, got %d", len(contents))
	}
	c0, _ := contents[0].(map[string]any)
	parts, _ := c0["parts"].([]any)
	if len(parts) != 2 {
		t.Fatalf("expected 2 parts, got %d", len(parts))
	}
	for i, want := range []string{"First part.", "Second part."} {
		p, _ := parts[i].(map[string]any)
		if text, _ := p["text"].(string); text != want {
			t.Errorf("parts[%d].text = %q, want %q", i, text, want)
		}
	}
}

// TestResponseWithMultipleTextParts verifies that multiple text parts in a
// response are all included in the output.
func TestResponseWithMultipleTextParts(t *testing.T) {
	resp := map[string]any{
		"candidates": []any{
			map[string]any{
				"content": map[string]any{
					"role": "model",
					"parts": []any{
						map[string]any{"text": "AI, or Artificial Intelligence, "},
						map[string]any{"text": "refers to machines that think."},
					},
				},
				"finishReason": "STOP",
			},
		},
		"usageMetadata": map[string]any{
			"promptTokenCount":     6,
			"candidatesTokenCount": 10,
			"totalTokenCount":      16,
		},
	}
	respBody, _ := json.Marshal(resp)

	server := httptest.NewServer(captureAndRespond(t, make(chan []byte, 1), "application/json", string(respBody)))
	defer server.Close()

	a := newTestClient(t, server)

	result, err := a.RunText(t.Context(), "Tell me about AI").Collect()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	got := result.String()
	if got != "AI, or Artificial Intelligence, refers to machines that think." {
		t.Errorf("response text = %q, want combined text", got)
	}
}

// TestResponseWithMultipleCandidates_NonStreaming verifies that only the first
// candidate is used in non-streaming responses.
func TestResponseWithMultipleCandidates_NonStreaming(t *testing.T) {
	resp := map[string]any{
		"candidates": []any{
			map[string]any{
				"content": map[string]any{
					"role":  "model",
					"parts": []any{map[string]any{"text": "first candidate"}},
				},
				"finishReason": "STOP",
			},
			map[string]any{
				"content": map[string]any{
					"role":  "model",
					"parts": []any{map[string]any{"text": "second candidate"}},
				},
				"finishReason": "STOP",
			},
		},
	}
	respBody, _ := json.Marshal(resp)

	server := httptest.NewServer(captureAndRespond(t, make(chan []byte, 1), "application/json", string(respBody)))
	defer server.Close()

	a := newTestClient(t, server)

	result, err := a.RunText(t.Context(), "Pick one candidate").Collect()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := result.String(); got != "first candidate" {
		t.Errorf("response text = %q, want %q", got, "first candidate")
	}
}

// TestFunctionResultMapping verifies that FunctionResultContent is correctly
// mapped back to the API with the function name resolved from the call ID.
func TestFunctionResultMapping(t *testing.T) {
	bodyCh := make(chan []byte, 1)
	server := httptest.NewServer(captureAndRespond(t, bodyCh, "application/json", minimalTextResponse("It's sunny!")))
	defer server.Close()

	a := newTestClient(t, server)

	messages := []*message.Message{
		{Role: message.RoleAssistant, Contents: []message.Content{
			&message.FunctionCallContent{
				CallID:    "call_123",
				Name:      "get_weather",
				Arguments: `{"location":"Seattle"}`,
			},
		}},
		{Role: message.RoleTool, Contents: []message.Content{
			&message.FunctionResultContent{
				CallID: "call_123",
				Result: "sunny",
			},
		}},
	}
	resp, err := a.Run(t.Context(), messages).Collect()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := resp.String(); got != "It's sunny!" {
		t.Errorf("response text = %q, want %q", got, "It's sunny!")
	}

	var req map[string]any
	if err := json.Unmarshal(<-bodyCh, &req); err != nil {
		t.Fatalf("unmarshal request body: %v", err)
	}
	contents, _ := req["contents"].([]any)
	if len(contents) != 2 {
		t.Fatalf("expected 2 contents, got %d", len(contents))
	}

	// Second content should have a functionResponse with name resolved.
	c1, _ := contents[1].(map[string]any)
	parts, _ := c1["parts"].([]any)
	if len(parts) == 0 {
		t.Fatal("expected parts in function result content")
	}
	p0, _ := parts[0].(map[string]any)
	fr, _ := p0["functionResponse"].(map[string]any)
	if fr == nil {
		t.Fatal("expected functionResponse in part")
	}
	if name, _ := fr["name"].(string); name != "get_weather" {
		t.Errorf("functionResponse.name = %q, want %q", name, "get_weather")
	}
	if id, _ := fr["id"].(string); id != "call_123" {
		t.Errorf("functionResponse.id = %q, want %q", id, "call_123")
	}
}

// TestFunctionResultMissingCallID verifies that a FunctionResultContent with no
// matching function call returns an error.
func TestFunctionResultMissingCallID(t *testing.T) {
	server := httptest.NewServer(captureAndRespond(t, make(chan []byte, 1), "application/json", minimalTextResponse("ok")))
	defer server.Close()

	a := newTestClient(t, server)

	messages := []*message.Message{
		{Role: message.RoleTool, Contents: []message.Content{
			&message.FunctionResultContent{
				CallID: "unknown_call",
				Result: "value",
			},
		}},
	}
	_, err := a.Run(t.Context(), messages).Collect()
	if err == nil {
		t.Fatal("expected error for missing function name, got nil")
	}
}

// TestResponseWithThinkingContent verifies that response parts with thought=true
// are translated into TextReasoningContent.
func TestResponseWithThinkingContent(t *testing.T) {
	resp := map[string]any{
		"candidates": []any{
			map[string]any{
				"content": map[string]any{
					"role": "model",
					"parts": []any{
						map[string]any{"thought": true, "text": "Let me think about this..."},
						map[string]any{"text": "The answer is 42."},
					},
				},
				"finishReason": "STOP",
			},
		},
		"usageMetadata": map[string]any{
			"promptTokenCount":     10,
			"candidatesTokenCount": 15,
			"totalTokenCount":      25,
		},
	}
	respBody, _ := json.Marshal(resp)

	server := httptest.NewServer(captureAndRespond(t, make(chan []byte, 1), "application/json", string(respBody)))
	defer server.Close()

	a := newTestClient(t, server)

	result, err := a.RunText(t.Context(), "What is the meaning of life?").Collect()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var foundReasoning *message.TextReasoningContent
	var foundText *message.TextContent
	for _, msg := range result.Messages {
		for _, c := range msg.Contents {
			switch c := c.(type) {
			case *message.TextReasoningContent:
				foundReasoning = c
			case *message.TextContent:
				foundText = c
			}
		}
	}
	if foundReasoning == nil {
		t.Fatal("expected TextReasoningContent in response")
	}
	if foundReasoning.Text != "Let me think about this..." {
		t.Errorf("reasoning text = %q, want %q", foundReasoning.Text, "Let me think about this...")
	}
	if foundText == nil {
		t.Fatal("expected TextContent in response")
	}
	if foundText.Text != "The answer is 42." {
		t.Errorf("text = %q, want %q", foundText.Text, "The answer is 42.")
	}
}

// TestResponseWithThoughtSignature verifies that thought signatures in response
// parts are encoded as base64 in TextReasoningContent.ProtectedData.
func TestResponseWithThoughtSignature(t *testing.T) {
	const wantProtectedData = "dGhpbmtpbmcgc2lnbmF0dXJlIGRhdGE="
	resp := map[string]any{
		"candidates": []any{
			map[string]any{
				"content": map[string]any{
					"role": "model",
					"parts": []any{
						map[string]any{"thought": true, "thoughtSignature": "dGhpbmtpbmcgc2lnbmF0dXJlIGRhdGE="},
						map[string]any{"text": "7^3 = 343"},
					},
				},
				"finishReason": "STOP",
			},
		},
		"usageMetadata": map[string]any{
			"promptTokenCount":     8,
			"candidatesTokenCount": 15,
			"totalTokenCount":      23,
		},
	}
	respBody, _ := json.Marshal(resp)

	server := httptest.NewServer(captureAndRespond(t, make(chan []byte, 1), "application/json", string(respBody)))
	defer server.Close()

	a := newTestClient(t, server)

	result, err := a.RunText(t.Context(), "Solve 7^3").Collect()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var foundReasoning *message.TextReasoningContent
	for _, msg := range result.Messages {
		for _, c := range msg.Contents {
			if rc, ok := c.(*message.TextReasoningContent); ok {
				foundReasoning = rc
			}
		}
	}
	if foundReasoning == nil {
		t.Fatal("expected TextReasoningContent in response")
	}
	if foundReasoning.ProtectedData != wantProtectedData {
		t.Errorf("reasoning ProtectedData = %q, want %q", foundReasoning.ProtectedData, wantProtectedData)
	}
}

// TestInvalidReasoningProtectedData verifies that invalid base64 in
// TextReasoningContent.ProtectedData returns a clear error.
func TestInvalidReasoningProtectedData(t *testing.T) {
	server := httptest.NewServer(captureAndRespond(t, make(chan []byte, 1), "application/json", minimalTextResponse("ok")))
	defer server.Close()

	a := newTestClient(t, server)

	messages := []*message.Message{
		{Role: message.RoleUser, Contents: []message.Content{
			&message.TextReasoningContent{
				Text:          "thinking",
				ProtectedData: "not-base64!!",
			},
		}},
	}

	_, err := a.Run(t.Context(), messages).Collect()
	if err == nil {
		t.Fatal("expected error for invalid reasoning protected data, got nil")
	}
	if got := err.Error(); got == "" || !strings.Contains(got, "failed to decode reasoning protected data") {
		t.Fatalf("unexpected error: %v", err)
	}
}

// TestDataInRequest verifies that DataContent is sent as inlineData in the
// request.
func TestDataInRequest(t *testing.T) {
	bodyCh := make(chan []byte, 1)
	server := httptest.NewServer(captureAndRespond(t, bodyCh, "application/json", minimalTextResponse("I see an image.")))
	defer server.Close()

	a := newTestClient(t, server)

	for _, tc := range []struct {
		name      string
		mediaType string
		wantText  string
	}{
		{name: "image", mediaType: "image/png", wantText: "I see an image."},
		{name: "json", mediaType: "application/json", wantText: "I see an image."},
	} {
		t.Run(tc.name, func(t *testing.T) {
			messages := []*message.Message{
				{Role: message.RoleUser, Contents: []message.Content{
					&message.TextContent{Text: "What's in this data?"},
					&message.DataContent{
						Data:      "eyJrZXkiOiAidmFsdWUifQ==",
						MediaType: tc.mediaType,
					},
				}},
			}
			resp, err := a.Run(t.Context(), messages).Collect()
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got := resp.String(); got != tc.wantText {
				t.Errorf("response text = %q, want %q", got, tc.wantText)
			}

			var req map[string]any
			if err := json.Unmarshal(<-bodyCh, &req); err != nil {
				t.Fatalf("unmarshal request body: %v", err)
			}
			contents, _ := req["contents"].([]any)
			if len(contents) != 1 {
				t.Fatalf("expected 1 content, got %d", len(contents))
			}
			c0, _ := contents[0].(map[string]any)
			parts, _ := c0["parts"].([]any)
			if len(parts) != 2 {
				t.Fatalf("expected 2 parts, got %d", len(parts))
			}
			p1, _ := parts[1].(map[string]any)
			inlineData, _ := p1["inlineData"].(map[string]any)
			if inlineData == nil {
				t.Fatal("expected inlineData in second part")
			}
			if mime, _ := inlineData["mimeType"].(string); mime != tc.mediaType {
				t.Errorf("inlineData.mimeType = %q, want %q", mime, tc.mediaType)
			}
		})
	}
}

func TestURIAndHostedFileInRequest(t *testing.T) {
	bodyCh := make(chan []byte, 1)
	server := httptest.NewServer(captureAndRespond(t, bodyCh, "application/json", minimalTextResponse("ok")))
	defer server.Close()

	a := newTestClient(t, server)
	messages := []*message.Message{{
		Role: message.RoleUser,
		Contents: []message.Content{
			&message.URIContent{URI: "gs://bucket/image.png", MediaType: "image/png"},
			&message.HostedFileContent{FileID: "gs://bucket/doc.pdf", Name: "doc.pdf", MediaType: "application/pdf"},
		},
	}}
	if _, err := a.Run(t.Context(), messages).Collect(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var req map[string]any
	if err := json.Unmarshal(<-bodyCh, &req); err != nil {
		t.Fatalf("unmarshal request body: %v", err)
	}
	contents, _ := req["contents"].([]any)
	if len(contents) != 1 {
		t.Fatalf("contents length = %d, want 1", len(contents))
	}
	content0, _ := contents[0].(map[string]any)
	parts, _ := content0["parts"].([]any)
	if len(parts) != 2 {
		t.Fatalf("parts length = %d, want 2", len(parts))
	}
	for i, wantURI := range []string{"gs://bucket/image.png", "gs://bucket/doc.pdf"} {
		part, _ := parts[i].(map[string]any)
		fileData, _ := part["fileData"].(map[string]any)
		if fileData == nil {
			t.Fatalf("parts[%d].fileData missing", i)
		}
		if fileURI, _ := fileData["fileUri"].(string); fileURI != wantURI {
			t.Errorf("parts[%d].fileData.fileUri = %q, want %q", i, fileURI, wantURI)
		}
	}
}

func TestResponseWithFileAndInlineData(t *testing.T) {
	resp := map[string]any{
		"candidates": []any{
			map[string]any{
				"content": map[string]any{
					"role": "model",
					"parts": []any{
						map[string]any{"inlineData": map[string]any{"mimeType": "image/png", "data": "aW1hZ2U="}},
						map[string]any{"fileData": map[string]any{"displayName": "doc.pdf", "mimeType": "application/pdf", "fileUri": "gs://bucket/doc.pdf"}},
					},
				},
				"finishReason": "STOP",
			},
		},
	}
	respBody, _ := json.Marshal(resp)

	server := httptest.NewServer(captureAndRespond(t, make(chan []byte, 1), "application/json", string(respBody)))
	defer server.Close()

	a := newTestClient(t, server)
	result, err := a.RunText(t.Context(), "return media").Collect()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var data *message.DataContent
	var uri *message.URIContent
	for content := range result.Contents() {
		switch c := content.(type) {
		case *message.DataContent:
			data = c
		case *message.URIContent:
			uri = c
		}
	}
	if data == nil || data.Data != "aW1hZ2U=" || data.MediaType != "image/png" {
		t.Fatalf("data content = %#v", data)
	}
	if uri == nil || uri.URI != "gs://bucket/doc.pdf" || uri.MediaType != "application/pdf" {
		t.Fatalf("uri content = %#v", uri)
	}
	if uri.AdditionalProperties["displayName"] != "doc.pdf" {
		t.Fatalf("uri displayName = %#v, want doc.pdf", uri.AdditionalProperties["displayName"])
	}
}

func TestResponseWithCodeExecutionParts(t *testing.T) {
	resp := map[string]any{
		"candidates": []any{
			map[string]any{
				"content": map[string]any{
					"role": "model",
					"parts": []any{
						map[string]any{"executableCode": map[string]any{"id": "code_1", "language": "PYTHON", "code": "print(1)"}},
						map[string]any{"codeExecutionResult": map[string]any{"id": "code_1", "outcome": "OUTCOME_OK", "output": "1\n"}},
					},
				},
				"finishReason": "STOP",
			},
		},
	}
	respBody, _ := json.Marshal(resp)

	server := httptest.NewServer(captureAndRespond(t, make(chan []byte, 1), "application/json", string(respBody)))
	defer server.Close()

	a := newTestClient(t, server)
	result, err := a.RunText(t.Context(), "run code").Collect()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var call *message.CodeInterpreterToolCallContent
	var codeResult *message.CodeInterpreterToolResultContent
	for content := range result.Contents() {
		switch c := content.(type) {
		case *message.CodeInterpreterToolCallContent:
			call = c
		case *message.CodeInterpreterToolResultContent:
			codeResult = c
		}
	}
	if call == nil || call.CallID != "code_1" || len(call.Inputs) != 1 {
		t.Fatalf("code call = %#v", call)
	}
	input, ok := call.Inputs[0].(*message.DataContent)
	if !ok || input.MediaType != "text/x-python" {
		t.Fatalf("code input = %#v", call.Inputs[0])
	}
	if codeResult == nil || codeResult.CallID != "code_1" || len(codeResult.Outputs) != 1 {
		t.Fatalf("code result = %#v", codeResult)
	}
	output, ok := codeResult.Outputs[0].(*message.TextContent)
	if !ok || output.Text != "1\n" {
		t.Fatalf("code output = %#v", codeResult.Outputs[0])
	}
}

// TestStreamingWithFunctionCall verifies that a function call in a streaming
// response is correctly translated.
func TestStreamingWithFunctionCall(t *testing.T) {
	funcCallResp := map[string]any{
		"candidates": []any{
			map[string]any{
				"content": map[string]any{
					"role": "model",
					"parts": []any{
						map[string]any{
							"functionCall": map[string]any{
								"name": "get_weather",
								"args": map[string]any{"city": "Paris"},
							},
						},
					},
				},
				"finishReason": "STOP",
			},
		},
		"usageMetadata": map[string]any{
			"promptTokenCount":     15,
			"candidatesTokenCount": 8,
			"totalTokenCount":      23,
		},
	}
	respBody, _ := json.Marshal(funcCallResp)
	streamResp := "data:" + string(respBody) + "\n\n"

	weatherTool := functool.MustNew(functool.Config{
		Name:        "get_weather",
		Description: "Gets weather",
	}, func(_ context.Context, args struct{ City string }) (string, error) {
		return "sunny", nil
	})

	server := httptest.NewServer(captureAndRespond(t, make(chan []byte, 1), "text/event-stream", streamResp))
	defer server.Close()

	a := newTestClient(t, server)

	resp, err := a.RunText(t.Context(), "Weather in Paris?", agent.Stream(true), agent.WithTool(weatherTool)).Collect()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var gotFuncCall *message.FunctionCallContent
	for _, msg := range resp.Messages {
		for _, c := range msg.Contents {
			if fc, ok := c.(*message.FunctionCallContent); ok {
				gotFuncCall = fc
			}
		}
	}
	if gotFuncCall == nil {
		t.Fatal("expected FunctionCallContent in streaming response")
	}
	if gotFuncCall.Name != "get_weather" {
		t.Errorf("FunctionCallContent.Name = %q, want %q", gotFuncCall.Name, "get_weather")
	}
}

// TestMultiTurnWithFunctionCalls verifies a multi-turn conversation that
// includes function call and result round-trips.
func TestMultiTurnWithFunctionCalls(t *testing.T) {
	bodyCh := make(chan []byte, 1)
	server := httptest.NewServer(captureAndRespond(t, bodyCh, "application/json", minimalTextResponse("MSFT is at $378.91.")))
	defer server.Close()

	a := newTestClient(t, server)

	messages := []*message.Message{
		{Role: message.RoleUser, Contents: []message.Content{
			&message.TextContent{Text: "Check the stock price for GOOGL"},
		}},
		{Role: message.RoleAssistant, Contents: []message.Content{
			&message.FunctionCallContent{
				CallID:    "call_stock_1",
				Name:      "get_stock_price",
				Arguments: `{"symbol":"GOOGL"}`,
			},
		}},
		{Role: message.RoleTool, Contents: []message.Content{
			&message.FunctionResultContent{
				CallID: "call_stock_1",
				Result: map[string]any{"symbol": "GOOGL", "price": 142.50},
			},
		}},
		{Role: message.RoleAssistant, Contents: []message.Content{
			&message.TextContent{Text: "GOOGL is at $142.50."},
		}},
		{Role: message.RoleUser, Contents: []message.Content{
			&message.TextContent{Text: "Now check MSFT"},
		}},
		{Role: message.RoleAssistant, Contents: []message.Content{
			&message.FunctionCallContent{
				CallID:    "call_stock_2",
				Name:      "get_stock_price",
				Arguments: `{"symbol":"MSFT"}`,
			},
		}},
		{Role: message.RoleTool, Contents: []message.Content{
			&message.FunctionResultContent{
				CallID: "call_stock_2",
				Result: map[string]any{"symbol": "MSFT", "price": 378.91},
			},
		}},
	}

	stockTool := functool.MustNew(functool.Config{
		Name:        "get_stock_price",
		Description: "Gets current stock price",
	}, func(_ context.Context, args struct{ Symbol string }) (string, error) {
		return `{"price": 378.91}`, nil
	})

	resp, err := a.Run(t.Context(), messages, agent.WithTool(stockTool)).Collect()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := resp.String(); got != "MSFT is at $378.91." {
		t.Errorf("response text = %q, want %q", got, "MSFT is at $378.91.")
	}

	var req map[string]any
	if err := json.Unmarshal(<-bodyCh, &req); err != nil {
		t.Fatalf("unmarshal request body: %v", err)
	}
	contents, _ := req["contents"].([]any)
	if len(contents) != 7 {
		t.Fatalf("expected 7 contents, got %d", len(contents))
	}

	// Verify roles alternate correctly.
	wantRoles := []string{"user", "model", "user", "model", "user", "model", "user"}
	for i, c := range contents {
		cm, _ := c.(map[string]any)
		role, _ := cm["role"].(string)
		if role != wantRoles[i] {
			t.Errorf("contents[%d].role = %q, want %q", i, role, wantRoles[i])
		}
	}
}

// TestParallelFunctionCalls verifies that multiple function calls and results
// can be sent in a single turn.
func TestParallelFunctionCalls(t *testing.T) {
	resp := map[string]any{
		"candidates": []any{
			map[string]any{
				"content": map[string]any{
					"role": "model",
					"parts": []any{
						map[string]any{"text": "Weather comparison complete."},
					},
				},
				"finishReason": "STOP",
			},
		},
		"usageMetadata": map[string]any{
			"promptTokenCount":     75,
			"candidatesTokenCount": 10,
			"totalTokenCount":      85,
		},
	}
	respBody, _ := json.Marshal(resp)

	bodyCh := make(chan []byte, 1)
	server := httptest.NewServer(captureAndRespond(t, bodyCh, "application/json", string(respBody)))
	defer server.Close()

	a := newTestClient(t, server)

	messages := []*message.Message{
		{Role: message.RoleUser, Contents: []message.Content{
			&message.TextContent{Text: "Compare weather in NYC, London, and Tokyo"},
		}},
		{Role: message.RoleAssistant, Contents: []message.Content{
			&message.FunctionCallContent{CallID: "call_w1", Name: "get_weather", Arguments: `{"city":"New York"}`},
			&message.FunctionCallContent{CallID: "call_w2", Name: "get_weather", Arguments: `{"city":"London"}`},
			&message.FunctionCallContent{CallID: "call_w3", Name: "get_weather", Arguments: `{"city":"Tokyo"}`},
		}},
		{Role: message.RoleTool, Contents: []message.Content{
			&message.FunctionResultContent{CallID: "call_w1", Result: map[string]any{"city": "New York", "temp": 45}},
			&message.FunctionResultContent{CallID: "call_w2", Result: map[string]any{"city": "London", "temp": 50}},
			&message.FunctionResultContent{CallID: "call_w3", Result: map[string]any{"city": "Tokyo", "temp": 65}},
		}},
	}

	weatherTool := functool.MustNew(functool.Config{
		Name:        "get_weather",
		Description: "Gets weather for a city",
	}, func(_ context.Context, args struct{ City string }) (string, error) {
		return "sunny", nil
	})

	result, err := a.Run(t.Context(), messages, agent.WithTool(weatherTool)).Collect()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := result.String(); got != "Weather comparison complete." {
		t.Errorf("response text = %q, want %q", got, "Weather comparison complete.")
	}

	var req map[string]any
	if err := json.Unmarshal(<-bodyCh, &req); err != nil {
		t.Fatalf("unmarshal request body: %v", err)
	}

	// The tool message should have 3 function results as parts.
	contents, _ := req["contents"].([]any)
	if len(contents) != 3 {
		t.Fatalf("expected 3 contents, got %d", len(contents))
	}
	toolContent, _ := contents[2].(map[string]any)
	toolParts, _ := toolContent["parts"].([]any)
	if len(toolParts) != 3 {
		t.Errorf("expected 3 tool parts, got %d", len(toolParts))
	}
}

// TestThinkingContentRoundTrip verifies that TextReasoningContent sent back to
// the model includes thought=true and the thought signature.
func TestThinkingContentRoundTrip(t *testing.T) {
	bodyCh := make(chan []byte, 1)
	server := httptest.NewServer(captureAndRespond(t, bodyCh, "application/json", minimalTextResponse("done")))
	defer server.Close()

	a := newTestClient(t, server)

	messages := []*message.Message{
		{Role: message.RoleUser, Contents: []message.Content{
			&message.TextContent{Text: "Think about this"},
		}},
		{Role: message.RoleAssistant, Contents: []message.Content{
			&message.TextReasoningContent{
				Text:          "Thinking deeply...",
				ProtectedData: "dGhpbmtpbmcgc2lnbmF0dXJlIGRhdGE=",
			},
			&message.TextContent{Text: "Here's my answer."},
		}},
		{Role: message.RoleUser, Contents: []message.Content{
			&message.TextContent{Text: "Continue"},
		}},
	}
	_, err := a.Run(t.Context(), messages).Collect()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var req map[string]any
	if err := json.Unmarshal(<-bodyCh, &req); err != nil {
		t.Fatalf("unmarshal request body: %v", err)
	}
	contents, _ := req["contents"].([]any)
	if len(contents) != 3 {
		t.Fatalf("expected 3 contents, got %d", len(contents))
	}

	// The assistant message should have thought=true part with signature.
	assistantContent, _ := contents[1].(map[string]any)
	parts, _ := assistantContent["parts"].([]any)
	if len(parts) < 2 {
		t.Fatalf("expected at least 2 parts in assistant message, got %d", len(parts))
	}
	thoughtPart, _ := parts[0].(map[string]any)
	if thought, _ := thoughtPart["thought"].(bool); !thought {
		t.Error("expected thought=true in first part")
	}
	if sig, _ := thoughtPart["thoughtSignature"].(string); sig == "" {
		t.Error("expected non-empty thoughtSignature in thought part")
	}
}

// TestStreamingBasicResponse verifies that streaming reassembles multiple
// chunks into the final response and individual updates are received.
func TestStreamingBasicResponse(t *testing.T) {
	chunk := func(m map[string]any) string {
		b, _ := json.Marshal(m)
		return "data:" + string(b) + "\n\n"
	}
	streamResp := chunk(map[string]any{
		"candidates": []any{map[string]any{
			"content": map[string]any{
				"role":  "model",
				"parts": []any{map[string]any{"text": "Hello"}},
			},
		}},
	}) + chunk(map[string]any{
		"candidates": []any{map[string]any{
			"content": map[string]any{
				"role":  "model",
				"parts": []any{map[string]any{"text": " there"}},
			},
		}},
	}) + chunk(map[string]any{
		"candidates": []any{map[string]any{
			"content": map[string]any{
				"role":  "model",
				"parts": []any{map[string]any{"text": "!"}},
			},
			"finishReason": "STOP",
		}},
		"usageMetadata": map[string]any{
			"promptTokenCount":     4,
			"candidatesTokenCount": 3,
			"totalTokenCount":      7,
		},
	})

	server := httptest.NewServer(captureAndRespond(t, make(chan []byte, 2), "text/event-stream", streamResp))
	defer server.Close()

	a := newTestClient(t, server)

	// Iterate streaming updates and count text chunks.
	var updateCount int
	for update, err := range a.RunText(t.Context(), "Say hello", agent.Stream(true)) {
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		for _, c := range update.Contents {
			if _, ok := c.(*message.TextContent); ok {
				updateCount++
			}
		}
	}
	if updateCount < 3 {
		t.Fatalf("expected at least 3 text updates, got %d", updateCount)
	}

	// Verify Collect assembles the text correctly.
	resp, err := a.RunText(t.Context(), "Say hello", agent.Stream(true)).Collect()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := resp.String(); got != "Hello there!" {
		t.Errorf("collected text = %q, want %q", got, "Hello there!")
	}
}

// TestStreamingMultipleChunks verifies that streaming with many chunks
// reassembles correctly.
func TestStreamingMultipleChunks(t *testing.T) {
	chunk := func(m map[string]any) string {
		b, _ := json.Marshal(m)
		return "data:" + string(b) + "\n\n"
	}

	var streamResp string
	texts := []string{"1", ", 2", ", 3", ", 4", ", 5"}
	for i, text := range texts {
		c := map[string]any{
			"candidates": []any{map[string]any{
				"content": map[string]any{
					"role":  "model",
					"parts": []any{map[string]any{"text": text}},
				},
			}},
		}
		if i == len(texts)-1 {
			c["candidates"].([]any)[0].(map[string]any)["finishReason"] = "STOP"
			c["usageMetadata"] = map[string]any{
				"promptTokenCount":     5,
				"candidatesTokenCount": 9,
				"totalTokenCount":      14,
			}
		}
		streamResp += chunk(c)
	}

	server := httptest.NewServer(captureAndRespond(t, make(chan []byte, 1), "text/event-stream", streamResp))
	defer server.Close()

	a := newTestClient(t, server)

	resp, err := a.RunText(t.Context(), "Count to five", agent.Stream(true)).Collect()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := resp.String(); got != "1, 2, 3, 4, 5" {
		t.Errorf("collected text = %q, want %q", got, "1, 2, 3, 4, 5")
	}
}

// TestStreamingMultipleCandidatesPerChunk verifies that only the first
// candidate is used from each streamed chunk.
func TestStreamingMultipleCandidatesPerChunk(t *testing.T) {
	chunk := func(m map[string]any) string {
		b, _ := json.Marshal(m)
		return "data:" + string(b) + "\n\n"
	}
	streamResp := chunk(map[string]any{
		"candidates": []any{
			map[string]any{
				"content": map[string]any{
					"role":  "model",
					"parts": []any{map[string]any{"text": "first"}},
				},
			},
			map[string]any{
				"content": map[string]any{
					"role":  "model",
					"parts": []any{map[string]any{"text": "second"}},
				},
			},
		},
	}) + chunk(map[string]any{
		"usageMetadata": map[string]any{
			"promptTokenCount":     4,
			"candidatesTokenCount": 2,
			"totalTokenCount":      6,
		},
	})

	server := httptest.NewServer(captureAndRespond(t, make(chan []byte, 1), "text/event-stream", streamResp))
	defer server.Close()

	a := newTestClient(t, server)

	resp, err := a.RunText(t.Context(), "Pick one stream candidate", agent.Stream(true)).Collect()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := resp.String(); got != "first" {
		t.Errorf("collected text = %q, want %q", got, "first")
	}
}

// TestLongConversationHistory verifies that a long conversation history is
// correctly sent to the API.
func TestLongConversationHistory(t *testing.T) {
	bodyCh := make(chan []byte, 1)
	server := httptest.NewServer(captureAndRespond(t, bodyCh, "application/json", minimalTextResponse("Glad to hear it!")))
	defer server.Close()

	a := newTestClient(t, server)

	messages := []*message.Message{
		{Role: message.RoleUser, Contents: []message.Content{&message.TextContent{Text: "Hi"}}},
		{Role: message.RoleAssistant, Contents: []message.Content{&message.TextContent{Text: "Hello"}}},
		{Role: message.RoleUser, Contents: []message.Content{&message.TextContent{Text: "How are you?"}}},
		{Role: message.RoleAssistant, Contents: []message.Content{&message.TextContent{Text: "I'm good"}}},
		{Role: message.RoleUser, Contents: []message.Content{&message.TextContent{Text: "Great"}}},
	}
	resp, err := a.Run(t.Context(), messages).Collect()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := resp.String(); got != "Glad to hear it!" {
		t.Errorf("response text = %q, want %q", got, "Glad to hear it!")
	}

	var req map[string]any
	if err := json.Unmarshal(<-bodyCh, &req); err != nil {
		t.Fatalf("unmarshal request body: %v", err)
	}
	contents, _ := req["contents"].([]any)
	if len(contents) != 5 {
		t.Fatalf("expected 5 contents, got %d", len(contents))
	}
}

// TestEmptyMessage verifies that an empty text message is handled.
func TestEmptyMessage(t *testing.T) {
	server := httptest.NewServer(captureAndRespond(t, make(chan []byte, 1), "application/json", minimalTextResponse("Hello!")))
	defer server.Close()

	a := newTestClient(t, server)

	resp, err := a.RunText(t.Context(), "").Collect()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := resp.String(); got != "Hello!" {
		t.Errorf("response text = %q, want %q", got, "Hello!")
	}
}

// TestGenerateContentConfigOption verifies that the GenerateContentConfig option
// is applied to the request.
func TestGenerateContentConfigOption(t *testing.T) {
	bodyCh := make(chan []byte, 1)
	server := httptest.NewServer(captureAndRespond(t, bodyCh, "application/json", minimalTextResponse("ok")))
	defer server.Close()

	a := newTestClient(t, server)

	temp := float32(0.5)
	topP := float32(0.9)
	topK := float32(20)
	_, err := a.RunText(t.Context(), "Test",
		geminiprovider.GenerateContentConfig(genai.GenerateContentConfig{
			Temperature:     &temp,
			TopP:            &topP,
			TopK:            &topK,
			MaxOutputTokens: 100,
			StopSequences:   []string{"END"},
		}),
	).Collect()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var req map[string]any
	if err := json.Unmarshal(<-bodyCh, &req); err != nil {
		t.Fatalf("unmarshal request body: %v", err)
	}
	gc, ok := req["generationConfig"].(map[string]any)
	if !ok {
		t.Fatal("request missing generationConfig")
	}
	if temp, _ := gc["temperature"].(float64); temp != 0.5 {
		t.Errorf("temperature = %v, want 0.5", temp)
	}
	if topP, _ := gc["topP"].(float64); topP != 0.9 {
		t.Errorf("topP = %v, want 0.9", topP)
	}
	if maxTokens, _ := gc["maxOutputTokens"].(float64); maxTokens != 100 {
		t.Errorf("maxOutputTokens = %v, want 100", maxTokens)
	}
	stops, _ := gc["stopSequences"].([]any)
	if len(stops) != 1 || stops[0] != "END" {
		t.Errorf("stopSequences = %v, want [END]", stops)
	}
}

// Gemini streams usageMetadata cumulatively across chunks, with the final
// chunk's totals authoritative. The provider must report that final total once,
// not sum the running totals from every chunk.
func TestUsageContent_Streaming_CumulativeUsageNotSummed(t *testing.T) {
	chunk := func(m map[string]any) string {
		b, _ := json.Marshal(m)
		return "data:" + string(b) + "\n\n"
	}
	stream := chunk(map[string]any{
		"candidates": []any{map[string]any{
			"content": map[string]any{"role": "model", "parts": []any{map[string]any{"text": "Hi"}}},
		}},
		"usageMetadata": map[string]any{"promptTokenCount": 10, "candidatesTokenCount": 2, "totalTokenCount": 12},
	}) + chunk(map[string]any{
		"candidates": []any{map[string]any{
			"content": map[string]any{"role": "model", "parts": []any{map[string]any{"text": " there"}}},
		}},
		"usageMetadata": map[string]any{"promptTokenCount": 10, "candidatesTokenCount": 5, "totalTokenCount": 15},
	})

	server := httptest.NewServer(captureAndRespond(t, make(chan []byte, 1), "text/event-stream", stream))
	defer server.Close()

	a := newTestClient(t, server)

	resp, err := a.RunText(t.Context(), "hello", agent.Stream(true)).Collect()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	usage := resp.Usage()
	if usage.TotalTokenCount != 15 {
		t.Errorf("TotalTokenCount = %d, want 15 (final cumulative total, not the sum of per-chunk usage)", usage.TotalTokenCount)
	}
	if usage.InputTokenCount != 10 {
		t.Errorf("InputTokenCount = %d, want 10", usage.InputTokenCount)
	}
	if usage.OutputTokenCount != 5 {
		t.Errorf("OutputTokenCount = %d, want 5", usage.OutputTokenCount)
	}
}

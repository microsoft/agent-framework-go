// Copyright (c) Microsoft. All rights reserved.

package aguiprovider_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	aguiSSEClient "github.com/ag-ui-protocol/ag-ui/sdks/community/go/pkg/client/sse"
	aguiEvents "github.com/ag-ui-protocol/ag-ui/sdks/community/go/pkg/core/events"
	aguiTypes "github.com/ag-ui-protocol/ag-ui/sdks/community/go/pkg/core/types"
	"github.com/microsoft/agent-framework-go/agent"
	"github.com/microsoft/agent-framework-go/message"
	"github.com/microsoft/agent-framework-go/provider/aguiprovider"
	"github.com/microsoft/agent-framework-go/tool/functool"
)

func TestAGUIAgentRun_AggregatesStreamingText(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		writeSSE(t, w, aguiEvents.NewRunStartedEvent("thread-1", "run-1"))
		writeSSE(t, w, aguiEvents.NewTextMessageStartEvent("msg-1", aguiEvents.WithRole("assistant")))
		writeSSE(t, w, aguiEvents.NewTextMessageContentEvent("msg-1", "Hello"))
		writeSSE(t, w, aguiEvents.NewTextMessageContentEvent("msg-1", " World"))
		writeSSE(t, w, aguiEvents.NewTextMessageEndEvent("msg-1"))
		writeSSE(t, w, aguiEvents.NewRunFinishedEvent("thread-1", "run-1"))
	}))
	defer server.Close()

	a := aguiprovider.NewAgent(newTestClient(server.URL), aguiprovider.AgentConfig{})
	resp, err := a.RunText(context.Background(), "hi").Collect()
	if err != nil {
		t.Fatalf("run error: %v", err)
	}
	if got := resp.String(); got != "Hello World" {
		t.Fatalf("response text = %q, want %q", got, "Hello World")
	}
}

func TestAGUIAgentRun_ConfigInstructionsBecomeSystemMessage(t *testing.T) {
	var captured aguiTypes.RunAgentInput
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&captured); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		writeSSE(t, w, aguiEvents.NewRunStartedEvent(captured.ThreadID, captured.RunID))
		writeSSE(t, w, aguiEvents.NewRunFinishedEvent(captured.ThreadID, captured.RunID))
	}))
	defer server.Close()

	a := aguiprovider.NewAgent(newTestClient(server.URL), aguiprovider.AgentConfig{Instructions: "Be concise."})
	if _, err := a.RunText(context.Background(), "hi").Collect(); err != nil {
		t.Fatalf("run error: %v", err)
	}
	if len(captured.Messages) < 2 {
		t.Fatalf("messages length = %d, want at least 2", len(captured.Messages))
	}
	if captured.Messages[0].Role != aguiTypes.RoleSystem || captured.Messages[0].Content != "Be concise." {
		t.Fatalf("first message = %#v, want system instructions", captured.Messages[0])
	}
	if captured.Messages[1].Role != aguiTypes.RoleUser || captured.Messages[1].Content != "hi" {
		t.Fatalf("second message = %#v, want user input", captured.Messages[1])
	}
}

func TestAGUIAgentRun_WithEmptyEventStream_EmitsMetadataUpdate(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		writeSSE(t, w, aguiEvents.NewRunStartedEvent("thread-1", "run-1"))
		writeSSE(t, w, aguiEvents.NewRunFinishedEvent("thread-1", "run-1"))
	}))
	defer server.Close()

	a := aguiprovider.NewAgent(newTestClient(server.URL), aguiprovider.AgentConfig{})
	resp, err := a.RunText(context.Background(), "hi").Collect()
	if err != nil {
		t.Fatalf("run error: %v", err)
	}
	if len(resp.Messages) == 0 {
		t.Fatal("expected at least one metadata message")
	}
	for _, msg := range resp.Messages {
		if msg.Role != message.RoleAssistant {
			t.Fatalf("message role = %q, want %q", msg.Role, message.RoleAssistant)
		}
	}
}

func TestAGUIAgentCreateSession_UsesServiceIDAsThreadID(t *testing.T) {
	a := aguiprovider.NewAgent(newTestClient("http://localhost"), aguiprovider.AgentConfig{})
	s, err := a.CreateSession(context.Background(), agent.WithServiceID("thread-existing"))
	if err != nil {
		t.Fatalf("create session error: %v", err)
	}
	if got := s.ServiceID(); got != "thread-existing" {
		t.Fatalf("session.ServiceID = %q, want %q", got, "thread-existing")
	}
}

func TestAGUIAgentRun_UsesExistingSessionServiceIDAsThreadID(t *testing.T) {
	var mu sync.Mutex
	var capturedThreadID string

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var input aguiTypes.RunAgentInput
		if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		mu.Lock()
		capturedThreadID = input.ThreadID
		mu.Unlock()

		w.Header().Set("Content-Type", "text/event-stream")
		writeSSE(t, w, aguiEvents.NewRunStartedEvent(input.ThreadID, input.RunID))
		writeSSE(t, w, aguiEvents.NewRunFinishedEvent(input.ThreadID, input.RunID))
	}))
	defer server.Close()

	a := aguiprovider.NewAgent(newTestClient(server.URL), aguiprovider.AgentConfig{})
	session, err := a.CreateSession(context.Background(), agent.WithServiceID("thread-existing"))
	if err != nil {
		t.Fatalf("create session error: %v", err)
	}
	_, err = a.RunText(context.Background(), "hi", agent.WithSession(session)).Collect()
	if err != nil {
		t.Fatalf("run error: %v", err)
	}

	mu.Lock()
	gotThreadID := capturedThreadID
	mu.Unlock()
	if gotThreadID != "thread-existing" {
		t.Fatalf("captured thread id = %q, want %q", gotThreadID, "thread-existing")
	}
}

func TestAGUIAgentRun_GeneratesUniqueRunIDPerInvocation(t *testing.T) {
	var mu sync.Mutex
	runIDs := []string{}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var input aguiTypes.RunAgentInput
		if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		mu.Lock()
		runIDs = append(runIDs, input.RunID)
		mu.Unlock()

		w.Header().Set("Content-Type", "text/event-stream")
		writeSSE(t, w, aguiEvents.NewRunStartedEvent(input.ThreadID, input.RunID))
		writeSSE(t, w, aguiEvents.NewRunFinishedEvent(input.ThreadID, input.RunID))
	}))
	defer server.Close()

	a := aguiprovider.NewAgent(newTestClient(server.URL), aguiprovider.AgentConfig{})
	_, err := a.RunText(context.Background(), "first").Collect()
	if err != nil {
		t.Fatalf("first run error: %v", err)
	}
	_, err = a.RunText(context.Background(), "second").Collect()
	if err != nil {
		t.Fatalf("second run error: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(runIDs) < 2 {
		t.Fatalf("expected at least 2 run ids, got %d", len(runIDs))
	}
	if runIDs[0] == runIDs[1] {
		t.Fatalf("expected different run IDs, got %q and %q", runIDs[0], runIDs[1])
	}
}

// TestAGUIAgentRun_WithSession_PreservesHistoryAcrossMultipleTurns verifies
// that subsequent runs with the same session include the full conversation
// history, even after the provider assigns a thread ID to the session on the
// first run. This mirrors the .NET fix in ChatClientAgent (PR #5904) that
// ensures the ChatHistoryProvider is not discarded when a ConversationId
// (thread ID) is present for an AGUI provider.
func TestAGUIAgentRun_WithSession_PreservesHistoryAcrossMultipleTurns(t *testing.T) {
	var mu sync.Mutex
	var captured []aguiTypes.RunAgentInput

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var input aguiTypes.RunAgentInput
		if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		mu.Lock()
		captured = append(captured, input)
		mu.Unlock()

		w.Header().Set("Content-Type", "text/event-stream")
		writeSSE(t, w, aguiEvents.NewRunStartedEvent(input.ThreadID, input.RunID))
		writeSSE(t, w, aguiEvents.NewTextMessageStartEvent("msg-1", aguiEvents.WithRole("assistant")))
		writeSSE(t, w, aguiEvents.NewTextMessageContentEvent("msg-1", "Hello"))
		writeSSE(t, w, aguiEvents.NewTextMessageEndEvent("msg-1"))
		writeSSE(t, w, aguiEvents.NewRunFinishedEvent(input.ThreadID, input.RunID))
	}))
	defer server.Close()

	a := aguiprovider.NewAgent(newTestClient(server.URL), aguiprovider.AgentConfig{})
	session, err := a.CreateSession(context.Background())
	if err != nil {
		t.Fatalf("create session error: %v", err)
	}

	if _, err := a.Run(context.Background(), []*message.Message{message.NewText("First")}, agent.WithSession(session)).Collect(); err != nil {
		t.Fatalf("first run error: %v", err)
	}

	if _, err := a.Run(context.Background(), []*message.Message{message.NewText("Second")}, agent.WithSession(session)).Collect(); err != nil {
		t.Fatalf("second run error: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(captured) != 2 {
		t.Fatalf("expected 2 requests, got %d", len(captured))
	}
	// First run: only the user message.
	if len(captured[0].Messages) != 1 {
		t.Fatalf("first run message count = %d, want 1", len(captured[0].Messages))
	}
	// Second run: history (user + assistant) plus new user message = 3.
	if len(captured[1].Messages) != 3 {
		t.Fatalf("second run message count = %d, want 3 (history + new message)", len(captured[1].Messages))
	}
	// Both runs share the same thread ID.
	if captured[0].ThreadID == "" || captured[0].ThreadID != captured[1].ThreadID {
		t.Fatalf("thread IDs do not match: first=%q second=%q", captured[0].ThreadID, captured[1].ThreadID)
	}
}

func TestAGUIAgentRun_InvokesTools_WhenFunctionCallsReturned(t *testing.T) {
	var mu sync.Mutex
	requestCount := 0
	type sentMessage struct {
		Role string `json:"role"`
	}
	sentPayloads := [][]sentMessage{}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var input struct {
			Messages []sentMessage `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
			t.Fatalf("decode request: %v", err)
		}

		mu.Lock()
		requestCount++
		current := requestCount
		sentPayloads = append(sentPayloads, append([]sentMessage{}, input.Messages...))
		mu.Unlock()

		w.Header().Set("Content-Type", "text/event-stream")
		switch current {
		case 1:
			writeSSE(t, w, aguiEvents.NewRunStartedEvent("thread-1", "run-1"))
			writeSSE(t, w, aguiEvents.NewToolCallStartEvent("call-1", "GetWeather"))
			writeSSE(t, w, aguiEvents.NewToolCallArgsEvent("call-1", `{"location":"Seattle"}`))
			writeSSE(t, w, aguiEvents.NewToolCallEndEvent("call-1"))
			writeSSE(t, w, aguiEvents.NewRunFinishedEvent("thread-1", "run-1"))
		default:
			writeSSE(t, w, aguiEvents.NewRunStartedEvent("thread-1", "run-2"))
			writeSSE(t, w, aguiEvents.NewTextMessageStartEvent("msg-2", aguiEvents.WithRole("assistant")))
			writeSSE(t, w, aguiEvents.NewTextMessageContentEvent("msg-2", "The weather is nice!"))
			writeSSE(t, w, aguiEvents.NewTextMessageEndEvent("msg-2"))
			writeSSE(t, w, aguiEvents.NewRunFinishedEvent("thread-1", "run-2"))
		}
	}))
	defer server.Close()

	a := aguiprovider.NewAgent(newTestClient(server.URL), aguiprovider.AgentConfig{})
	invoked := false
	type weatherInput struct {
		Location string `json:"location"`
	}
	weatherTool := functool.MustNew(functool.Config{Name: "GetWeather", Description: "Get weather"}, func(ctx context.Context, in weatherInput) (string, error) {
		invoked = true
		return "Sunny", nil
	})

	resp, err := a.RunText(context.Background(), "What's the weather?", agent.Stream(true), agent.WithTool(weatherTool)).Collect()
	if err != nil {
		t.Fatalf("run error: %v", err)
	}
	if !invoked {
		t.Fatal("expected tool to be invoked")
	}
	if !strings.Contains(resp.String(), "The weather is nice!") {
		t.Fatalf("expected final text response, got %q", resp.String())
	}

	mu.Lock()
	defer mu.Unlock()
	if requestCount < 2 {
		t.Fatalf("expected at least 2 requests (tool call + follow-up), got %d", requestCount)
	}
	hasToolRole := false
	if len(sentPayloads) >= 2 {
		for _, m := range sentPayloads[1] {
			if strings.EqualFold(m.Role, "tool") {
				hasToolRole = true
				break
			}
		}
	}
	if !hasToolRole {
		t.Fatalf("expected second request payload to include a tool role message")
	}
}

func TestAGUIAgentRun_ForwardsAllToolResults_WhenMultipleToolCallsReturned(t *testing.T) {
	var mu sync.Mutex
	requestCount := 0
	type sentMessage struct {
		Role       string `json:"role"`
		ToolCallID string `json:"toolCallId"`
	}
	sentPayloads := [][]sentMessage{}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var input struct {
			Messages []sentMessage `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
			t.Fatalf("decode request: %v", err)
		}

		mu.Lock()
		requestCount++
		current := requestCount
		sentPayloads = append(sentPayloads, append([]sentMessage{}, input.Messages...))
		mu.Unlock()

		w.Header().Set("Content-Type", "text/event-stream")
		switch current {
		case 1:
			writeSSE(t, w, aguiEvents.NewRunStartedEvent("thread-1", "run-1"))
			writeSSE(t, w, aguiEvents.NewToolCallStartEvent("call-1", "GetWeather"))
			writeSSE(t, w, aguiEvents.NewToolCallArgsEvent("call-1", `{"location":"Seattle"}`))
			writeSSE(t, w, aguiEvents.NewToolCallEndEvent("call-1"))
			writeSSE(t, w, aguiEvents.NewToolCallStartEvent("call-2", "GetTime"))
			writeSSE(t, w, aguiEvents.NewToolCallArgsEvent("call-2", `{"timezone":"UTC"}`))
			writeSSE(t, w, aguiEvents.NewToolCallEndEvent("call-2"))
			writeSSE(t, w, aguiEvents.NewRunFinishedEvent("thread-1", "run-1"))
		default:
			writeSSE(t, w, aguiEvents.NewRunStartedEvent("thread-1", "run-2"))
			writeSSE(t, w, aguiEvents.NewTextMessageStartEvent("msg-2", aguiEvents.WithRole("assistant")))
			writeSSE(t, w, aguiEvents.NewTextMessageContentEvent("msg-2", "done"))
			writeSSE(t, w, aguiEvents.NewTextMessageEndEvent("msg-2"))
			writeSSE(t, w, aguiEvents.NewRunFinishedEvent("thread-1", "run-2"))
		}
	}))
	defer server.Close()

	a := aguiprovider.NewAgent(newTestClient(server.URL), aguiprovider.AgentConfig{})
	weatherInvoked := false
	timeInvoked := false
	type weatherInput struct {
		Location string `json:"location"`
	}
	type timeInput struct {
		Timezone string `json:"timezone"`
	}
	weatherTool := functool.MustNew(functool.Config{Name: "GetWeather", Description: "Get weather"}, func(ctx context.Context, in weatherInput) (string, error) {
		weatherInvoked = true
		return "Sunny", nil
	})
	timeTool := functool.MustNew(functool.Config{Name: "GetTime", Description: "Get time"}, func(ctx context.Context, in timeInput) (string, error) {
		timeInvoked = true
		return "12:00", nil
	})

	_, err := a.RunText(context.Background(), "Do both", agent.Stream(true), agent.WithTool(weatherTool), agent.WithTool(timeTool)).Collect()
	if err != nil {
		t.Fatalf("run error: %v", err)
	}
	if !weatherInvoked || !timeInvoked {
		t.Fatalf("expected both tools invoked, got weather=%v time=%v", weatherInvoked, timeInvoked)
	}

	mu.Lock()
	defer mu.Unlock()
	if requestCount < 2 {
		t.Fatalf("expected at least 2 requests, got %d", requestCount)
	}
	toolCalls := map[string]bool{}
	for _, m := range sentPayloads[1] {
		if strings.EqualFold(m.Role, "tool") {
			toolCalls[m.ToolCallID] = true
		}
	}
	if !toolCalls["call-1"] || !toolCalls["call-2"] {
		t.Fatalf("expected second payload to include tool results for call-1 and call-2, got %+v", toolCalls)
	}
}

func TestAGUIAgentRun_ConvertsStateSnapshotEventToDataContent(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		writeSSE(t, w, aguiEvents.NewRunStartedEvent("thread-1", "run-1"))
		writeSSE(t, w, aguiEvents.NewStateSnapshotEvent(map[string]any{"counter": 42, "status": "active"}))
		writeSSE(t, w, aguiEvents.NewRunFinishedEvent("thread-1", "run-1"))
	}))
	defer server.Close()

	a := aguiprovider.NewAgent(newTestClient(server.URL), aguiprovider.AgentConfig{})
	resp, err := a.RunText(context.Background(), "hi").Collect()
	if err != nil {
		t.Fatalf("run error: %v", err)
	}

	var data *message.DataContent
	for c := range resp.Contents() {
		if dc, ok := c.(*message.DataContent); ok {
			data = dc
			break
		}
	}
	if data == nil {
		t.Fatal("expected DataContent from state snapshot")
	}
	if data.MediaType != "application/json" {
		t.Fatalf("media type = %q, want %q", data.MediaType, "application/json")
	}
	b, err := data.Bytes()
	if err != nil {
		t.Fatalf("decode data bytes: %v", err)
	}
	var snapshot map[string]any
	if err := json.Unmarshal(b, &snapshot); err != nil {
		t.Fatalf("unmarshal snapshot: %v", err)
	}
	if snapshot["status"] != "active" {
		t.Fatalf("snapshot status = %v, want active", snapshot["status"])
	}
}

func TestAGUIAgentRun_WithUnknownEventType_ReturnsError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprint(w, "data: {\"type\":\"UNKNOWN_EVENT\"}\n\n")
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
	}))
	defer server.Close()

	a := aguiprovider.NewAgent(newTestClient(server.URL), aguiprovider.AgentConfig{})
	_, err := a.RunText(context.Background(), "hi").Collect()
	if err == nil {
		t.Fatal("expected error for unknown event type")
	}
}

func writeSSE(t *testing.T, w http.ResponseWriter, evt aguiEvents.Event) {
	t.Helper()
	b, err := json.Marshal(evt)
	if err != nil {
		t.Fatalf("marshal event: %v", err)
	}
	if _, err := fmt.Fprintf(w, "data: %s\n\n", string(b)); err != nil {
		t.Fatalf("write event: %v", err)
	}
	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	}
}

func newTestClient(endpoint string) *aguiSSEClient.Client {
	return aguiSSEClient.NewClient(aguiSSEClient.Config{Endpoint: endpoint})
}

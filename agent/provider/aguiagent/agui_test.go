// Copyright (c) Microsoft. All rights reserved.

package aguiagent_test

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
	"github.com/microsoft/agent-framework-go/agent/provider/aguiagent"
	"github.com/microsoft/agent-framework-go/agentopt"
	"github.com/microsoft/agent-framework-go/message"
	"github.com/microsoft/agent-framework-go/tool"
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

	a := aguiagent.New(aguiagent.Config{Client: newTestClient(server.URL)})
	resp, err := a.RunText(context.Background(), "hi").Collect()
	if err != nil {
		t.Fatalf("run error: %v", err)
	}
	if got := resp.String(); got != "Hello World" {
		t.Fatalf("response text = %q, want %q", got, "Hello World")
	}
}

func TestAGUIAgentRun_WithEmptyEventStream_EmitsMetadataUpdate(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		writeSSE(t, w, aguiEvents.NewRunStartedEvent("thread-1", "run-1"))
		writeSSE(t, w, aguiEvents.NewRunFinishedEvent("thread-1", "run-1"))
	}))
	defer server.Close()

	a := aguiagent.New(aguiagent.Config{Client: newTestClient(server.URL)})
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
	a := aguiagent.New(aguiagent.Config{Client: newTestClient("http://localhost")})
	s, err := a.CreateSession(context.Background(), agentopt.ServiceID("thread-existing"))
	if err != nil {
		t.Fatalf("create session error: %v", err)
	}
	if got := s.ServiceID; got != "thread-existing" {
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

	a := aguiagent.New(aguiagent.Config{Client: newTestClient(server.URL)})
	session, err := a.CreateSession(context.Background(), agentopt.ServiceID("thread-existing"))
	if err != nil {
		t.Fatalf("create session error: %v", err)
	}
	_, err = a.RunText(context.Background(), "hi", agentopt.Session(session)).Collect()
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

	a := aguiagent.New(aguiagent.Config{Client: newTestClient(server.URL)})
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

	a := aguiagent.New(aguiagent.Config{Client: newTestClient(server.URL)})
	invoked := false
	type weatherInput struct {
		Location string `json:"location"`
	}
	weatherTool := functool.MustNew(&functool.Func{Name: "GetWeather", Description: "Get weather"}, func(ctx tool.Context, in weatherInput) (string, error) {
		invoked = true
		return "Sunny", nil
	})

	resp, err := a.RunText(context.Background(), "What's the weather?", agentopt.Stream(true), agentopt.Tool(weatherTool)).Collect()
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

	a := aguiagent.New(aguiagent.Config{Client: newTestClient(server.URL)})
	weatherInvoked := false
	timeInvoked := false
	type weatherInput struct {
		Location string `json:"location"`
	}
	type timeInput struct {
		Timezone string `json:"timezone"`
	}
	weatherTool := functool.MustNew(&functool.Func{Name: "GetWeather", Description: "Get weather"}, func(ctx tool.Context, in weatherInput) (string, error) {
		weatherInvoked = true
		return "Sunny", nil
	})
	timeTool := functool.MustNew(&functool.Func{Name: "GetTime", Description: "Get time"}, func(ctx tool.Context, in timeInput) (string, error) {
		timeInvoked = true
		return "12:00", nil
	})

	_, err := a.RunText(context.Background(), "Do both", agentopt.Stream(true), agentopt.Tool(weatherTool), agentopt.Tool(timeTool)).Collect()
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

	a := aguiagent.New(aguiagent.Config{Client: newTestClient(server.URL)})
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

	a := aguiagent.New(aguiagent.Config{Client: newTestClient(server.URL)})
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

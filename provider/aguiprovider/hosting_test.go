// Copyright (c) Microsoft. All rights reserved.

package aguiprovider_test

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"iter"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	aguiEvents "github.com/ag-ui-protocol/ag-ui/sdks/community/go/pkg/core/events"
	"github.com/microsoft/agent-framework-go/agent"
	"github.com/microsoft/agent-framework-go/agent/harness/toolautocall"
	"github.com/microsoft/agent-framework-go/message"
	"github.com/microsoft/agent-framework-go/provider/aguiprovider"
	"github.com/microsoft/agent-framework-go/tool"
	"github.com/microsoft/agent-framework-go/tool/functool"
)

func newTestAgent(runFn func(context.Context, []*message.Message, ...agent.Option) iter.Seq2[*agent.ResponseUpdate, error]) *agent.Agent {
	return agent.New(agent.ProviderConfig{Run: runFn}, agent.Config{Name: "test-agent", ID: "test-agent-id"})
}

func newStreamingPortAgent(multipleMessages bool) *agent.Agent {
	return newTestAgent(func(context.Context, []*message.Message, ...agent.Option) iter.Seq2[*agent.ResponseUpdate, error] {
		return func(yield func(*agent.ResponseUpdate, error) bool) {
			chunks := [][]string{{"Hello", " ", "from", " ", "fake", " ", "agent", "!"}}
			if multipleMessages {
				chunks = [][]string{{"First", " ", "message"}, {"Second", " ", "message"}, {"Third", " ", "message"}}
			}
			for _, messageChunks := range chunks {
				id := aguiEvents.GenerateMessageID()
				for _, chunk := range messageChunks {
					if !yield(&agent.ResponseUpdate{
						MessageID: id,
						Role:      message.RoleAssistant,
						Contents:  message.NewText(chunk).Contents,
					}, nil) {
						return
					}
				}
			}
		}
	})
}

func newHostedPortClient(t *testing.T, hosted *agent.Agent, tools ...tool.Tool) (*agent.Agent, *agent.Session) {
	t.Helper()
	mux := http.NewServeMux()
	mux.Handle("/agent", aguiprovider.NewJSONHTTPHandler(hosted, aguiprovider.HandlerConfig{}))
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	a := aguiprovider.NewAgent(newTestClient(server.URL+"/agent"), aguiprovider.AgentConfig{
		Config: agent.Config{Name: "assistant", Description: "Sample assistant", Tools: tools},
	})
	session, err := a.CreateSession(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	return a, session
}

func collectPortUpdates(t *testing.T, stream agent.ResponseStream) ([]*agent.ResponseUpdate, *agent.Response) {
	t.Helper()
	var updates []*agent.ResponseUpdate
	response := new(agent.Response)
	for update, err := range stream {
		if err != nil {
			t.Fatal(err)
		}
		updates = append(updates, update)
		response.Update(update)
	}
	if len(updates) == 0 {
		t.Fatal("expected response updates")
	}
	return updates, response
}

func assertPortAssistantMessage(t *testing.T, response *agent.Response, text string) {
	t.Helper()
	if len(response.Messages) != 1 {
		t.Fatalf("messages = %#v, want one assistant message", response.Messages)
	}
	if got := response.Messages[0]; got.Role != message.RoleAssistant || got.String() != text {
		t.Fatalf("message = %#v, want assistant text %q", got, text)
	}
}

func TestAGUIIntegrationClientReceivesStreamedAssistantMessage(t *testing.T) {
	a, session := newHostedPortClient(t, newStreamingPortAgent(false))
	updates, response := collectPortUpdates(t, a.Run(t.Context(), []*message.Message{message.NewText("hello")},
		agent.WithSession(session), agent.Stream(true)))
	if session == nil {
		t.Fatal("expected session")
	}
	for _, update := range updates {
		if update.Role != message.RoleAssistant {
			t.Errorf("update role = %q, want assistant", update.Role)
		}
	}
	assertPortAssistantMessage(t, response, "Hello from fake agent!")
}

func TestAGUIIntegrationClientReceivesRunLifecycleEvents(t *testing.T) {
	a, session := newHostedPortClient(t, newStreamingPortAgent(false))
	updates, _ := collectPortUpdates(t, a.Run(t.Context(), []*message.Message{message.NewText("test")},
		agent.WithSession(session), agent.Stream(true)))
	first := updates[0]
	started, ok := first.RawRepresentation.(*aguiEvents.RunStartedEvent)
	if !ok || started.ThreadID() == "" || first.ResponseID == "" || started.RunID() != first.ResponseID {
		t.Fatalf("first update = %#v, want RUN_STARTED with thread and run IDs", first)
	}
	var textMessageID string
	var finished *aguiEvents.RunFinishedEvent
	for _, update := range updates {
		if update.Contents.Text() != "" {
			if update.MessageID == "" {
				t.Fatal("text update has no message ID")
			}
			if textMessageID == "" {
				textMessageID = update.MessageID
			}
			if update.MessageID != textMessageID {
				t.Errorf("message ID = %q, want %q", update.MessageID, textMessageID)
			}
		}
		if event, ok := update.RawRepresentation.(*aguiEvents.RunFinishedEvent); ok {
			finished = event
		}
	}
	last := updates[len(updates)-1]
	if textMessageID == "" || last.ResponseID != first.ResponseID || last.ConversationID != nil {
		t.Fatalf("text message ID = %q, last update = %#v", textMessageID, last)
	}
	if finished == nil || finished.ThreadID() != started.ThreadID() || finished.RunID() != first.ResponseID {
		t.Fatalf("RUN_FINISHED = %#v, want matching thread and run IDs", finished)
	}
}

func TestAGUIIntegrationRunAggregatesStreamingUpdates(t *testing.T) {
	a, session := newHostedPortClient(t, newStreamingPortAgent(false))
	response, err := a.Run(t.Context(), []*message.Message{message.NewText("hello")}, agent.WithSession(session)).Collect()
	if err != nil {
		t.Fatal(err)
	}
	if len(response.Messages) == 0 {
		t.Fatal("expected response messages")
	}
	var hasAssistant, hasText bool
	for _, msg := range response.Messages {
		hasAssistant = hasAssistant || msg.Role == message.RoleAssistant
		hasText = hasText || msg.String() == "Hello from fake agent!"
	}
	if !hasAssistant || !hasText {
		t.Fatalf("messages = %#v, want assistant response text", response.Messages)
	}
}

func TestAGUIIntegrationLocalHistoryAcrossTurns(t *testing.T) {
	a, session := newHostedPortClient(t, newStreamingPortAgent(false))
	for _, text := range []string{"First question", "Second question"} {
		updates, response := collectPortUpdates(t, a.Run(t.Context(), []*message.Message{message.NewText(text)},
			agent.WithSession(session), agent.Stream(true)))
		var hasText bool
		for _, update := range updates {
			hasText = hasText || update.Contents.Text() != ""
			if update.ConversationID != nil {
				t.Errorf("conversation ID = %q, want unset", *update.ConversationID)
			}
		}
		if !hasText || session.ServiceID() != "" {
			t.Fatalf("has text = %t, service ID = %q", hasText, session.ServiceID())
		}
		assertPortAssistantMessage(t, response, "Hello from fake agent!")
	}
	history, err := session.InMemoryHistory("")
	if err != nil {
		t.Fatal(err)
	}
	if len(history) != 4 {
		t.Fatalf("history length = %d, want 4", len(history))
	}
	for i, want := range []struct {
		role message.Role
		text string
	}{
		{message.RoleUser, "First question"},
		{message.RoleAssistant, "Hello from fake agent!"},
		{message.RoleUser, "Second question"},
		{message.RoleAssistant, "Hello from fake agent!"},
	} {
		if history[i].Role != want.role || history[i].String() != want.text {
			t.Errorf("history[%d] = %#v, want %q: %q", i, history[i], want.role, want.text)
		}
	}
}

func TestAGUIIntegrationAgentSendsMultipleMessages(t *testing.T) {
	a, session := newHostedPortClient(t, newStreamingPortAgent(true))
	updates, response := collectPortUpdates(t, a.Run(t.Context(), []*message.Message{message.NewText("Tell me a story")},
		agent.WithSession(session), agent.Stream(true)))
	ids := map[string]bool{}
	for _, update := range updates {
		if update.Contents.Text() != "" && update.MessageID != "" {
			ids[update.MessageID] = true
		}
	}
	if len(ids) <= 1 || len(response.Messages) <= 1 {
		t.Fatalf("text message IDs = %v, response messages = %d, want multiple", ids, len(response.Messages))
	}
	for _, msg := range response.Messages {
		if msg.Role != message.RoleAssistant {
			t.Errorf("message role = %q, want assistant", msg.Role)
		}
	}
}

func TestAGUIIntegrationUserSendsMultipleMessages(t *testing.T) {
	a, session := newHostedPortClient(t, newStreamingPortAgent(false))
	input := []*message.Message{
		message.NewText("First part of question"),
		message.NewText("Second part of question"),
		message.NewText("Third part of question"),
	}
	updates, response := collectPortUpdates(t, a.Run(t.Context(), input, agent.WithSession(session), agent.Stream(true)))
	var hasText, hasAssistant bool
	for _, update := range updates {
		hasText = hasText || update.Contents.Text() != ""
		hasAssistant = hasAssistant || update.Role == message.RoleAssistant
	}
	if !hasText || !hasAssistant {
		t.Fatalf("has text = %t, has assistant = %t", hasText, hasAssistant)
	}
	assertPortAssistantMessage(t, response, "Hello from fake agent!")
}

func TestAGUIHandlerRejectsNilAgent(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("expected panic for nil agent")
		}
	}()
	aguiprovider.NewJSONHTTPHandler(nil, aguiprovider.HandlerConfig{})
}

func TestAGUIHandlerMapsSpecifiedPattern(t *testing.T) {
	mux := http.NewServeMux()
	mux.Handle("/api/agent", aguiprovider.NewJSONHTTPHandler(newStreamingPortAgent(false), aguiprovider.HandlerConfig{}))
	handler, pattern := mux.Handler(httptest.NewRequest(http.MethodPost, "/api/agent", nil))
	if handler == nil || pattern != "/api/agent" {
		t.Fatalf("handler = %v, pattern = %q", handler, pattern)
	}
}

func newToolCallingPortAgent(tools ...tool.Tool) *agent.Agent {
	return agent.New(agent.ProviderConfig{
		Run: func(_ context.Context, messages []*message.Message, options ...agent.Option) iter.Seq2[*agent.ResponseUpdate, error] {
			return func(yield func(*agent.ResponseUpdate, error) bool) {
				id := aguiEvents.GenerateMessageID()
				for _, msg := range messages {
					for _, content := range msg.Contents {
						if _, ok := content.(*message.FunctionResultContent); ok {
							yield(&agent.ResponseUpdate{
								MessageID: id, Role: message.RoleAssistant,
								Contents: message.NewText("Function calls completed successfully").Contents,
							}, nil)
							return
						}
					}
				}
				hasTools := false
				for tl := range agent.AllOptions(options, agent.WithTool) {
					hasTools = true
					arguments := "{}"
					switch tl.Name() {
					case "GetWeather":
						arguments = `{"location":"Seattle"}`
					case "Calculate":
						arguments = `{"a":5,"b":3}`
					case "FormatText":
						arguments = `{"text":"hello"}`
					case "GetServerForecast":
						arguments = `{"request":{"Location":"Seattle","Days":5}}`
					case "GetClientForecast":
						arguments = `{"request":{"City":"Portland","IncludeHourly":true}}`
					}
					if !yield(&agent.ResponseUpdate{
						MessageID: id, Role: message.RoleAssistant,
						Contents: message.Contents{&message.FunctionCallContent{
							CallID: "call_" + aguiEvents.GenerateMessageID(), Name: tl.Name(), Arguments: arguments,
						}},
					}, nil) {
						return
					}
				}
				if !hasTools {
					yield(&agent.ResponseUpdate{
						MessageID: id, Role: message.RoleAssistant, Contents: message.NewText("No tools available").Contents,
					}, nil)
				}
			}
		},
	}, agent.Config{
		Name: "base-agent", Description: "A base agent for tool testing", Tools: tools,
		Middlewares: []agent.Middleware{toolautocall.New(toolautocall.Config{})},
	})
}

func portFunctionContents(updates []*agent.ResponseUpdate) (calls []*message.FunctionCallContent, results []*message.FunctionResultContent, callUpdates, resultUpdates int) {
	for _, update := range updates {
		var hasCall, hasResult bool
		for _, content := range update.Contents {
			switch content := content.(type) {
			case *message.FunctionCallContent:
				calls = append(calls, content)
				hasCall = true
			case *message.FunctionResultContent:
				results = append(results, content)
				hasResult = true
			}
		}
		if hasCall {
			callUpdates++
		}
		if hasResult {
			resultUpdates++
		}
	}
	return
}

func TestAGUIIntegrationServerTriggersSingleFunctionCall(t *testing.T) {
	var count atomic.Int32
	tl := functool.MustNew(functool.Config{Name: "ServerFunction", Description: "A function on the server"},
		func(context.Context, struct{}) (string, error) {
			count.Add(1)
			return "Server function result", nil
		})
	a, session := newHostedPortClient(t, newToolCallingPortAgent(tl))
	updates, _ := collectPortUpdates(t, a.RunText(t.Context(), "Call the server function", agent.WithSession(session), agent.Stream(true)))
	_, results, callUpdates, resultUpdates := portFunctionContents(updates)
	if count.Load() != 1 || callUpdates != 1 || resultUpdates != 1 || len(results) != 1 || results[0].Result == nil {
		t.Fatalf("calls = %d, call updates = %d, result updates = %d, results = %#v", count.Load(), callUpdates, resultUpdates, results)
	}
}

func TestAGUIIntegrationClientTriggersSingleFunctionCall(t *testing.T) {
	var count atomic.Int32
	tl := functool.MustNew(functool.Config{Name: "ClientFunction", Description: "A function on the client"},
		func(context.Context, struct{}) (string, error) {
			count.Add(1)
			return "Client function result", nil
		})
	a, session := newHostedPortClient(t, newToolCallingPortAgent(), tl)
	updates, _ := collectPortUpdates(t, a.RunText(t.Context(), "Call the client function", agent.WithSession(session), agent.Stream(true)))
	_, results, callUpdates, resultUpdates := portFunctionContents(updates)
	if count.Load() != 1 || callUpdates != 1 || resultUpdates != 1 || len(results) != 1 || results[0].Result == nil {
		t.Fatalf("calls = %d, call updates = %d, result updates = %d, results = %#v", count.Load(), callUpdates, resultUpdates, results)
	}
}

func TestAGUIIntegrationClientTriggersMultipleFunctionCalls(t *testing.T) {
	var calculateCount, formatCount atomic.Int32
	calculate := functool.MustNew(functool.Config{Name: "Calculate", Description: "Calculates sum of two numbers"},
		func(_ context.Context, input struct {
			A int `json:"a"`
			B int `json:"b"`
		},
		) (int, error) {
			calculateCount.Add(1)
			return input.A + input.B, nil
		})
	format := functool.MustNew(functool.Config{Name: "FormatText", Description: "Formats text to uppercase"},
		func(_ context.Context, input struct {
			Text string `json:"text"`
		},
		) (string, error) {
			formatCount.Add(1)
			return strings.ToUpper(input.Text), nil
		})
	a, session := newHostedPortClient(t, newToolCallingPortAgent(), calculate, format)
	updates, _ := collectPortUpdates(t, a.RunText(t.Context(), "Calculate 5 + 3 and format 'hello'", agent.WithSession(session), agent.Stream(true)))
	calls, results, callUpdates, _ := portFunctionContents(updates)
	assertPortToolNames(t, calls, "Calculate", "FormatText")
	if calculateCount.Load() != 1 || formatCount.Load() != 1 || callUpdates == 0 || len(results) != 2 {
		t.Fatalf("calculate = %d, format = %d, call updates = %d, results = %#v", calculateCount.Load(), formatCount.Load(), callUpdates, results)
	}
}

func assertPortToolNames(t *testing.T, calls []*message.FunctionCallContent, names ...string) {
	t.Helper()
	if len(calls) != len(names) {
		t.Fatalf("function calls = %#v, want %d", calls, len(names))
	}
	for _, name := range names {
		found := false
		for _, call := range calls {
			found = found || call.Name == name
		}
		if !found {
			t.Errorf("missing function call %q", name)
		}
	}
}

func TestAGUIIntegrationFunctionCallsPreserveCallIDAndName(t *testing.T) {
	tl := functool.MustNew(functool.Config{Name: "TestFunction", Description: "A test function"},
		func(context.Context, struct{}) (string, error) { return "Test result", nil })
	a, session := newHostedPortClient(t, newToolCallingPortAgent(tl))
	updates, _ := collectPortUpdates(t, a.RunText(t.Context(), "Call the test function", agent.WithSession(session), agent.Stream(true)))
	calls, results, _, _ := portFunctionContents(updates)
	if len(calls) == 0 || calls[0].CallID == "" || calls[0].Name != "TestFunction" {
		t.Fatalf("function calls = %#v, want TestFunction with call ID", calls)
	}
	if len(results) == 0 || results[0].CallID != calls[0].CallID {
		t.Fatalf("results = %#v, want matching call ID", results)
	}
}

func TestAGUIIntegrationParallelServerFunctionCalls(t *testing.T) {
	var firstCount, secondCount atomic.Int32
	first := functool.MustNew(functool.Config{Name: "Function1", Description: "First function"},
		func(context.Context, struct{}) (string, error) {
			firstCount.Add(1)
			return "Result 1", nil
		})
	second := functool.MustNew(functool.Config{Name: "Function2", Description: "Second function"},
		func(context.Context, struct{}) (string, error) {
			secondCount.Add(1)
			return "Result 2", nil
		})
	a, session := newHostedPortClient(t, newToolCallingPortAgent(first, second))
	updates, _ := collectPortUpdates(t, a.RunText(t.Context(), "Call both functions in parallel", agent.WithSession(session), agent.Stream(true)))
	calls, results, _, _ := portFunctionContents(updates)
	assertPortToolNames(t, calls, "Function1", "Function2")
	if firstCount.Load() != 1 || secondCount.Load() != 1 || len(results) != 2 {
		t.Fatalf("first = %d, second = %d, results = %#v", firstCount.Load(), secondCount.Load(), results)
	}
	for _, call := range calls {
		matched := false
		for _, result := range results {
			matched = matched || result.CallID == call.CallID
		}
		if !matched {
			t.Errorf("missing result for call %q", call.CallID)
		}
	}
}

type serverForecastRequest struct {
	Location string
	Days     int
}

type serverForecastResponse struct {
	Temperature int
	Condition   string
	Humidity    int
}

type clientForecastRequest struct {
	City          string
	IncludeHourly bool
}

type clientForecastResponse struct {
	MaxTemp int
	MinTemp int
	Outlook string
}

func TestAGUIIntegrationServerToolCustomArguments(t *testing.T) {
	var count atomic.Int32
	tl := functool.MustNew(functool.Config{Name: "GetServerForecast", Description: "Gets the weather forecast from server"},
		func(_ context.Context, input struct {
			Request serverForecastRequest `json:"request"`
		},
		) (serverForecastResponse, error) {
			count.Add(1)
			condition := "Sunny"
			if input.Request.Location == "Seattle" {
				condition = "Rainy"
			}
			return serverForecastResponse{Temperature: 72, Condition: condition, Humidity: 65}, nil
		})
	a, session := newHostedPortClient(t, newToolCallingPortAgent(tl))
	updates, _ := collectPortUpdates(t, a.RunText(t.Context(), "Get server forecast for Seattle for 5 days",
		agent.WithSession(session), agent.Stream(true)))
	calls, results, callUpdates, resultUpdates := portFunctionContents(updates)
	if count.Load() != 1 || callUpdates == 0 || resultUpdates == 0 || len(calls) == 0 || calls[0].Name != "GetServerForecast" ||
		len(results) == 0 || results[0].Result == nil {
		t.Fatalf("count = %d, calls = %#v, results = %#v", count.Load(), calls, results)
	}
}

func TestAGUIIntegrationClientToolCustomArguments(t *testing.T) {
	var count atomic.Int32
	tl := functool.MustNew(functool.Config{Name: "GetClientForecast", Description: "Gets the weather forecast from client"},
		func(_ context.Context, input struct {
			Request clientForecastRequest `json:"request"`
		},
		) (clientForecastResponse, error) {
			count.Add(1)
			maxTemp := 75
			if input.Request.City == "Portland" {
				maxTemp = 68
			}
			return clientForecastResponse{MaxTemp: maxTemp, MinTemp: 55, Outlook: "Partly Cloudy"}, nil
		})
	a, session := newHostedPortClient(t, newToolCallingPortAgent(), tl)
	updates, _ := collectPortUpdates(t, a.RunText(t.Context(), "Get client forecast for Portland with hourly data",
		agent.WithSession(session), agent.Stream(true)))
	calls, results, callUpdates, resultUpdates := portFunctionContents(updates)
	if count.Load() != 1 || callUpdates == 0 || resultUpdates == 0 || len(calls) == 0 || calls[0].Name != "GetClientForecast" ||
		len(results) == 0 || results[0].Result == nil {
		t.Fatalf("count = %d, calls = %#v, results = %#v", count.Load(), calls, results)
	}
}

type sseEvent struct {
	Type      string `json:"type"`
	MessageID string `json:"messageId"`
	Role      string `json:"role"`
}

func decodeSSEEvents(t *testing.T, body string) []sseEvent {
	t.Helper()
	var events []sseEvent
	for _, line := range strings.Split(body, "\n") {
		payload, ok := strings.CutPrefix(line, "data: ")
		if !ok {
			continue
		}
		var event sseEvent
		if err := json.Unmarshal([]byte(payload), &event); err != nil {
			t.Fatalf("decode SSE event: %v", err)
		}
		events = append(events, event)
	}
	return events
}

func eventsWithPrefixes(events []sseEvent, prefixes ...string) []sseEvent {
	var filtered []sseEvent
	for _, event := range events {
		for _, prefix := range prefixes {
			if strings.HasPrefix(event.Type, prefix) {
				filtered = append(filtered, event)
				break
			}
		}
	}
	return filtered
}

func assertEventTypes(t *testing.T, events []sseEvent, want []string) {
	t.Helper()
	if len(events) != len(want) {
		t.Fatalf("event count = %d, want %d; events=%+v", len(events), len(want), events)
	}
	for i, wantType := range want {
		if events[i].Type != wantType {
			t.Fatalf("events[%d].type = %q, want %q; events=%+v", i, events[i].Type, wantType, events)
		}
	}
}

func assertSharedMessageID(t *testing.T, events []sseEvent) string {
	t.Helper()
	if len(events) == 0 || events[0].MessageID == "" {
		t.Fatalf("events must have a non-empty message ID: %+v", events)
	}
	messageID := events[0].MessageID
	for _, event := range events[1:] {
		if event.MessageID != messageID {
			t.Fatalf("event %q messageId = %q, want %q", event.Type, event.MessageID, messageID)
		}
	}
	return messageID
}

func TestHandler_MethodNotAllowed(t *testing.T) {
	a := newTestAgent(func(_ context.Context, _ []*message.Message, _ ...agent.Option) iter.Seq2[*agent.ResponseUpdate, error] {
		return func(yield func(*agent.ResponseUpdate, error) bool) {}
	})
	h := aguiprovider.NewJSONHTTPHandler(a, aguiprovider.HandlerConfig{})

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
	h := aguiprovider.NewJSONHTTPHandler(a, aguiprovider.HandlerConfig{})

	for _, tt := range []struct {
		name string
		body string
	}{
		{name: "malformed", body: "{not-json"},
		{name: "empty", body: ""},
	} {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(tt.body))
			req.Header.Set("Content-Type", "application/json; charset=utf-8")
			rr := httptest.NewRecorder()
			h.ServeHTTP(rr, req)

			if rr.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want %d", rr.Code, http.StatusBadRequest)
			}
		})
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
	h := aguiprovider.NewJSONHTTPHandler(a, aguiprovider.HandlerConfig{})

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
	h := aguiprovider.NewJSONHTTPHandler(a, aguiprovider.HandlerConfig{})

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

// A tool call's TOOL_CALL_START must carry parentMessageId so the client
// associates it with the assistant message rather than creating a separate
// message per tool call. Matches the Python host.
func TestHandler_ToolCallCarriesParentMessageID(t *testing.T) {
	a := newTestAgent(func(_ context.Context, _ []*message.Message, _ ...agent.Option) iter.Seq2[*agent.ResponseUpdate, error] {
		return func(yield func(*agent.ResponseUpdate, error) bool) {
			yield(&agent.ResponseUpdate{
				MessageID: "msg-1",
				Role:      message.RoleAssistant,
				Contents:  message.Contents{&message.FunctionCallContent{CallID: "c1", Name: "tool", Arguments: `{}`}},
			}, nil)
		}
	})
	h := aguiprovider.NewJSONHTTPHandler(a, aguiprovider.HandlerConfig{})

	body := `{"threadId":"thread-1","runId":"run-1","messages":[{"id":"u1","role":"user","content":"ping"}]}`
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	content := rr.Body.String()
	if !strings.Contains(content, "TOOL_CALL_START") || !strings.Contains(content, `"parentMessageId":"msg-1"`) {
		t.Fatalf("expected TOOL_CALL_START with parentMessageId msg-1, got %q", content)
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
	h := aguiprovider.NewJSONHTTPHandler(a, aguiprovider.HandlerConfig{})

	body := `{"threadId":"thread-1","runId":"run-1","messages":[{"id":"u1","role":"user","content":"ping"}]}`
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	content := rr.Body.String()
	if !strings.Contains(content, "STATE_SNAPSHOT") || !strings.Contains(content, "counter") {
		t.Fatalf("expected state snapshot SSE event, got %q", content)
	}
}

// Hosted MCP tool calls/results must be surfaced as AG-UI tool-call events
// rather than dropped, matching the Python host.
func TestHandler_MCPToolCallAndResultEmitEvents(t *testing.T) {
	a := newTestAgent(func(_ context.Context, _ []*message.Message, _ ...agent.Option) iter.Seq2[*agent.ResponseUpdate, error] {
		return func(yield func(*agent.ResponseUpdate, error) bool) {
			yield(&agent.ResponseUpdate{
				MessageID: "msg-1",
				Role:      message.RoleAssistant,
				Contents: message.Contents{
					&message.MCPServerToolCallContent{CallID: "m1", Name: "mcp_tool", ServerName: "github", Arguments: `{}`},
					&message.MCPServerToolResultContent{CallID: "m1", Outputs: message.Contents{&message.TextContent{Text: "ok"}}},
				},
			}, nil)
		}
	})
	h := aguiprovider.NewJSONHTTPHandler(a, aguiprovider.HandlerConfig{})

	body := `{"threadId":"thread-1","runId":"run-1","messages":[{"id":"u1","role":"user","content":"ping"}]}`
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	content := rr.Body.String()
	// Assert the full tool-call lifecycle and the serialized result, so a
	// regression dropping any event or the payload is caught.
	for _, want := range []string{"TOOL_CALL_START", "mcp_tool", "TOOL_CALL_ARGS", "TOOL_CALL_END", "TOOL_CALL_RESULT", "ok"} {
		if !strings.Contains(content, want) {
			t.Fatalf("expected MCP tool-call SSE stream to contain %q, got %q", want, content)
		}
	}
}

// UsageContent must be surfaced as a "usage" CUSTOM event rather than dropped,
// matching the Python host.
func TestHandler_UsageContentEmitsCustomEvent(t *testing.T) {
	a := newTestAgent(func(_ context.Context, _ []*message.Message, _ ...agent.Option) iter.Seq2[*agent.ResponseUpdate, error] {
		return func(yield func(*agent.ResponseUpdate, error) bool) {
			yield(&agent.ResponseUpdate{
				Role: message.RoleAssistant,
				Contents: message.Contents{&message.UsageContent{
					Details: message.UsageDetails{InputTokenCount: 10, OutputTokenCount: 5, TotalTokenCount: 15},
				}},
			}, nil)
		}
	})
	h := aguiprovider.NewJSONHTTPHandler(a, aguiprovider.HandlerConfig{})

	body := `{"threadId":"thread-1","runId":"run-1","messages":[{"id":"u1","role":"user","content":"ping"}]}`
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	content := rr.Body.String()
	if !strings.Contains(content, "CUSTOM") || !strings.Contains(content, `"usage"`) {
		t.Fatalf("expected a usage CUSTOM event, got %q", content)
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
	h := aguiprovider.NewJSONHTTPHandler(a, aguiprovider.HandlerConfig{})

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
	h := aguiprovider.NewJSONHTTPHandler(a, aguiprovider.HandlerConfig{})

	body := `{"threadId":"thread-1","runId":"run-1","messages":[{"id":"u1","role":"user","content":"ping"}]}`
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	content := rr.Body.String()
	foundReasoningStart := false
	for _, line := range strings.Split(content, "\n") {
		payload, ok := strings.CutPrefix(line, "data: ")
		if !ok {
			continue
		}
		var event struct {
			Type string `json:"type"`
			Role string `json:"role"`
		}
		if err := json.Unmarshal([]byte(payload), &event); err != nil {
			t.Fatalf("decode SSE event: %v", err)
		}
		if event.Type != "REASONING_MESSAGE_START" {
			continue
		}
		foundReasoningStart = true
		if event.Role != "reasoning" {
			t.Errorf("reasoning message role = %q, want %q", event.Role, "reasoning")
		}
		break
	}
	if !foundReasoningStart {
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

func TestHandler_MixedReasoningAndText_UsesDistinctOrderedLifecycles(t *testing.T) {
	a := newTestAgent(func(_ context.Context, _ []*message.Message, _ ...agent.Option) iter.Seq2[*agent.ResponseUpdate, error] {
		return func(yield func(*agent.ResponseUpdate, error) bool) {
			yield(&agent.ResponseUpdate{
				MessageID: "msg-shared",
				Role:      message.RoleAssistant,
				Contents: message.Contents{
					&message.TextReasoningContent{Text: "thinking"},
					&message.TextContent{Text: "final answer"},
				},
			}, nil)
		}
	})
	h := aguiprovider.NewJSONHTTPHandler(a, aguiprovider.HandlerConfig{})

	body := `{"threadId":"thread-1","runId":"run-1","messages":[{"id":"u1","role":"user","content":"ping"}]}`
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	lifecycle := eventsWithPrefixes(decodeSSEEvents(t, rr.Body.String()), "REASONING_", "TEXT_MESSAGE_")
	wantTypes := []string{
		"REASONING_START",
		"REASONING_MESSAGE_START",
		"REASONING_MESSAGE_CONTENT",
		"REASONING_MESSAGE_END",
		"REASONING_END",
		"TEXT_MESSAGE_START",
		"TEXT_MESSAGE_CONTENT",
		"TEXT_MESSAGE_END",
	}
	assertEventTypes(t, lifecycle, wantTypes)
	reasoningID := assertSharedMessageID(t, lifecycle[:5])
	textID := assertSharedMessageID(t, lifecycle[5:])
	if reasoningID == textID {
		t.Fatalf("reasoning and text IDs must differ, both were %q", reasoningID)
	}
	if lifecycle[1].Role != "reasoning" {
		t.Fatalf("reasoning role = %q, want %q", lifecycle[1].Role, "reasoning")
	}
}

func TestHandler_ReasoningDeltasWithoutMessageID_ShareLifecycle(t *testing.T) {
	a := newTestAgent(func(_ context.Context, _ []*message.Message, _ ...agent.Option) iter.Seq2[*agent.ResponseUpdate, error] {
		return func(yield func(*agent.ResponseUpdate, error) bool) {
			yield(&agent.ResponseUpdate{
				Role:     message.RoleAssistant,
				Contents: message.Contents{&message.TextReasoningContent{Text: "Think "}},
			}, nil)
			yield(&agent.ResponseUpdate{
				Role:     message.RoleAssistant,
				Contents: message.Contents{&message.TextReasoningContent{Text: "carefully."}},
			}, nil)
			yield(&agent.ResponseUpdate{
				Role:     message.RoleAssistant,
				Contents: message.Contents{&message.TextContent{Text: "Answer."}},
			}, nil)
		}
	})
	h := aguiprovider.NewJSONHTTPHandler(a, aguiprovider.HandlerConfig{})

	body := `{"threadId":"thread-1","runId":"run-1","messages":[{"id":"u1","role":"user","content":"ping"}]}`
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	events := decodeSSEEvents(t, rr.Body.String())
	reasoning := eventsWithPrefixes(events, "REASONING_")
	textEvents := eventsWithPrefixes(events, "TEXT_MESSAGE_")
	wantReasoningTypes := []string{
		"REASONING_START",
		"REASONING_MESSAGE_START",
		"REASONING_MESSAGE_CONTENT",
		"REASONING_MESSAGE_CONTENT",
		"REASONING_MESSAGE_END",
		"REASONING_END",
	}
	assertEventTypes(t, reasoning, wantReasoningTypes)
	reasoningID := assertSharedMessageID(t, reasoning)
	wantTextTypes := []string{"TEXT_MESSAGE_START", "TEXT_MESSAGE_CONTENT", "TEXT_MESSAGE_END"}
	assertEventTypes(t, textEvents, wantTextTypes)
	textID := assertSharedMessageID(t, textEvents)
	if reasoningID == textID {
		t.Fatalf("reasoning and text IDs must differ, both were %q", reasoningID)
	}
}

func TestHandler_ReasoningIgnoresNonEmittingContent(t *testing.T) {
	a := newTestAgent(func(_ context.Context, _ []*message.Message, _ ...agent.Option) iter.Seq2[*agent.ResponseUpdate, error] {
		return func(yield func(*agent.ResponseUpdate, error) bool) {
			yield(&agent.ResponseUpdate{
				Role: message.RoleAssistant,
				Contents: message.Contents{
					&message.TextReasoningContent{Text: "Think "},
					&message.URIContent{},
					&message.TextReasoningContent{Text: "carefully."},
				},
			}, nil)
		}
	})
	h := aguiprovider.NewJSONHTTPHandler(a, aguiprovider.HandlerConfig{})

	body := `{"threadId":"thread-1","runId":"run-1","messages":[{"id":"u1","role":"user","content":"ping"}]}`
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	reasoning := eventsWithPrefixes(decodeSSEEvents(t, rr.Body.String()), "REASONING_")
	wantTypes := []string{
		"REASONING_START",
		"REASONING_MESSAGE_START",
		"REASONING_MESSAGE_CONTENT",
		"REASONING_MESSAGE_CONTENT",
		"REASONING_MESSAGE_END",
		"REASONING_END",
	}
	assertEventTypes(t, reasoning, wantTypes)
	assertSharedMessageID(t, reasoning)
}

func TestHandler_TextDeltasWithoutMessageID_ShareLifecycle(t *testing.T) {
	a := newTestAgent(func(_ context.Context, _ []*message.Message, _ ...agent.Option) iter.Seq2[*agent.ResponseUpdate, error] {
		return func(yield func(*agent.ResponseUpdate, error) bool) {
			yield(&agent.ResponseUpdate{
				Role:     message.RoleAssistant,
				Contents: message.Contents{&message.TextContent{Text: "Hello "}},
			}, nil)
			yield(&agent.ResponseUpdate{
				Role:     message.RoleAssistant,
				Contents: message.Contents{&message.TextContent{Text: "world."}},
			}, nil)
		}
	})
	h := aguiprovider.NewJSONHTTPHandler(a, aguiprovider.HandlerConfig{})

	body := `{"threadId":"thread-1","runId":"run-1","messages":[{"id":"u1","role":"user","content":"ping"}]}`
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	textEvents := eventsWithPrefixes(decodeSSEEvents(t, rr.Body.String()), "TEXT_MESSAGE_")
	wantTypes := []string{
		"TEXT_MESSAGE_START",
		"TEXT_MESSAGE_CONTENT",
		"TEXT_MESSAGE_CONTENT",
		"TEXT_MESSAGE_END",
	}
	assertEventTypes(t, textEvents, wantTypes)
	assertSharedMessageID(t, textEvents)
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
	h := aguiprovider.NewJSONHTTPHandler(a, aguiprovider.HandlerConfig{})

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
	h := aguiprovider.NewJSONHTTPHandler(a, aguiprovider.HandlerConfig{})

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
	h := aguiprovider.NewJSONHTTPHandler(a, aguiprovider.HandlerConfig{})

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
	for line := range strings.SplitSeq(content, "\n") {
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
	h := aguiprovider.NewJSONHTTPHandler(a, aguiprovider.HandlerConfig{})

	body := `{"threadId":"t1","runId":"r1","messages":[]}`
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	content := rr.Body.String()

	var resultMsgIDs []string
	for line := range strings.SplitSeq(content, "\n") {
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
	h := aguiprovider.NewJSONHTTPHandler(a, aguiprovider.HandlerConfig{})

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

// TestHandler_MidStreamError_EmitsRunErrorEvent verifies that when the hosted agent
// fails mid-stream, the handler emits the standard RUN_ERROR terminal event (carrying
// the runID) that updatesToAGUIEvents already built, rather than a non-standard CUSTOM
// error event. The AG-UI client accumulator has no CUSTOM-event case, so a CUSTOM error
// frame would be silently dropped and the consumer would see no error. This mirrors the
// AG-UI protocol semantics used by the .NET/Python SDKs, where run failures terminate
// the stream with RUN_ERROR.
func TestHandler_MidStreamError_EmitsRunErrorEvent(t *testing.T) {
	a := newTestAgent(func(_ context.Context, _ []*message.Message, _ ...agent.Option) iter.Seq2[*agent.ResponseUpdate, error] {
		return func(yield func(*agent.ResponseUpdate, error) bool) {
			yield(&agent.ResponseUpdate{
				MessageID: "msg-1",
				Role:      message.RoleAssistant,
				Contents:  message.Contents{&message.TextContent{Text: "partial"}},
			}, nil)
			yield(nil, errors.New("boom"))
		}
	})
	h := aguiprovider.NewJSONHTTPHandler(a, aguiprovider.HandlerConfig{})

	body := `{"threadId":"thread-1","runId":"run-err-1","messages":[{"id":"u1","role":"user","content":"ping"}]}`
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	content := rr.Body.String()

	var sawRunError bool
	var runErrorRunID string
	for line := range strings.SplitSeq(content, "\n") {
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		data := strings.TrimPrefix(line, "data: ")
		var evt map[string]any
		if err := json.Unmarshal([]byte(data), &evt); err != nil {
			continue
		}
		if evt["type"] == "RUN_ERROR" {
			sawRunError = true
			runErrorRunID, _ = evt["runId"].(string)
		}
	}

	if !sawRunError {
		t.Fatalf("expected a RUN_ERROR frame on mid-stream failure, got %q", content)
	}
	if runErrorRunID != "run-err-1" {
		t.Fatalf("RUN_ERROR runId = %q, want %q", runErrorRunID, "run-err-1")
	}
	if strings.Contains(content, "CUSTOM") {
		t.Fatalf("did not expect a non-standard CUSTOM error event, got %q", content)
	}
}

// failOnMatchResponseWriter fails Write calls whose payload contains a sentinel,
// simulating a client disconnect/broken pipe partway through the SSE stream.
type failOnMatchResponseWriter struct {
	*httptest.ResponseRecorder
	sentinel string
	writeErr error
}

func (w *failOnMatchResponseWriter) Write(b []byte) (int, error) {
	if strings.Contains(string(b), w.sentinel) {
		return 0, w.writeErr
	}
	return w.ResponseRecorder.Write(b)
}

func (w *failOnMatchResponseWriter) Flush() { w.ResponseRecorder.Flush() }

// TestHandler_MidStreamError_TerminalWriteFailureSurfaced verifies that when the hosted
// agent fails mid-stream AND writing the terminal RUN_ERROR frame also fails (e.g. client
// disconnect/broken pipe), the delivery failure is surfaced to the handler alongside the
// original agent error rather than silently dropped. The handler logs the returned error,
// so the captured log must reference both the agent error and the write failure.
func TestHandler_MidStreamError_TerminalWriteFailureSurfaced(t *testing.T) {
	a := newTestAgent(func(_ context.Context, _ []*message.Message, _ ...agent.Option) iter.Seq2[*agent.ResponseUpdate, error] {
		return func(yield func(*agent.ResponseUpdate, error) bool) {
			yield(nil, errors.New("boom"))
		}
	})

	var logBuf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logBuf, nil))
	h := aguiprovider.NewJSONHTTPHandler(a, aguiprovider.HandlerConfig{Logger: logger})

	body := `{"threadId":"thread-1","runId":"run-err-1","messages":[{"id":"u1","role":"user","content":"ping"}]}`
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
	rr := &failOnMatchResponseWriter{
		ResponseRecorder: httptest.NewRecorder(),
		sentinel:         "RUN_ERROR",
		writeErr:         errors.New("broken pipe"),
	}
	h.ServeHTTP(rr, req)

	logged := logBuf.String()
	if !strings.Contains(logged, "agui stream failed") {
		t.Fatalf("expected stream failure to be logged, got %q", logged)
	}
	if !strings.Contains(logged, "broken pipe") {
		t.Fatalf("expected terminal-write failure to be surfaced, got %q", logged)
	}
	if !strings.Contains(logged, "boom") {
		t.Fatalf("expected original agent error to be preserved, got %q", logged)
	}
}

// TestHandler_MidStreamError_ClientObservesErrorContent verifies the end-to-end contract:
// a RUN_ERROR frame emitted by the host is decoded by the AG-UI client accumulator into a
// message.ErrorContent. A CUSTOM error frame (the pre-fix behavior) would be swallowed and
// the client would observe a clean stream end with no error surfaced.
func TestHandler_MidStreamError_ClientObservesErrorContent(t *testing.T) {
	hosted := newTestAgent(func(_ context.Context, _ []*message.Message, _ ...agent.Option) iter.Seq2[*agent.ResponseUpdate, error] {
		return func(yield func(*agent.ResponseUpdate, error) bool) {
			yield(nil, errors.New("boom"))
		}
	})
	server := httptest.NewServer(aguiprovider.NewJSONHTTPHandler(hosted, aguiprovider.HandlerConfig{}))
	defer server.Close()

	client := aguiprovider.NewAgent(newTestClient(server.URL), aguiprovider.AgentConfig{})
	resp, err := client.RunText(context.Background(), "hi").Collect()
	if err != nil {
		t.Fatalf("run error: %v", err)
	}

	var sawErrorContent bool
	for _, msg := range resp.Messages {
		for _, c := range msg.Contents {
			if ec, ok := c.(*message.ErrorContent); ok {
				sawErrorContent = true
				if ec.Message != "boom" {
					t.Fatalf("error content message = %q, want %q", ec.Message, "boom")
				}
			}
		}
	}
	if !sawErrorContent {
		t.Fatalf("expected client to observe an *message.ErrorContent from RUN_ERROR, got messages %#v", resp.Messages)
	}
}

func TestHandler_URIContentEmittedAsText(t *testing.T) {
	a := newTestAgent(func(_ context.Context, _ []*message.Message, _ ...agent.Option) iter.Seq2[*agent.ResponseUpdate, error] {
		return func(yield func(*agent.ResponseUpdate, error) bool) {
			yield(&agent.ResponseUpdate{
				MessageID: "msg-1",
				Role:      message.RoleAssistant,
				Contents:  message.Contents{&message.URIContent{URI: "https://example.com/generated.png", MediaType: "image/png"}},
			}, nil)
		}
	})
	h := aguiprovider.NewJSONHTTPHandler(a, aguiprovider.HandlerConfig{})

	body := `{"threadId":"thread-1","runId":"run-1","messages":[{"id":"u1","role":"user","content":"ping"}]}`
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusOK)
	}
	if content := rr.Body.String(); !strings.Contains(content, "https://example.com/generated.png") {
		t.Fatalf("expected URIContent URI surfaced in SSE payload, got %q", content)
	}
}

func TestHandler_TextThenToolCall_ClosesTextBeforeToolLifecycle(t *testing.T) {
	a := newTestAgent(func(_ context.Context, _ []*message.Message, _ ...agent.Option) iter.Seq2[*agent.ResponseUpdate, error] {
		return func(yield func(*agent.ResponseUpdate, error) bool) {
			yield(&agent.ResponseUpdate{
				MessageID: "m1",
				Role:      message.RoleAssistant,
				Contents:  message.Contents{&message.TextContent{Text: "Let me check."}},
			}, nil)
			yield(&agent.ResponseUpdate{
				MessageID: "m1",
				Role:      message.RoleAssistant,
				Contents:  message.Contents{&message.FunctionCallContent{CallID: "call-1", Name: "get_weather", Arguments: "{}"}},
			}, nil)
		}
	})
	h := aguiprovider.NewJSONHTTPHandler(a, aguiprovider.HandlerConfig{})

	body := `{"threadId":"thread-1","runId":"run-1","messages":[{"id":"u1","role":"user","content":"ping"}]}`
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	lifecycle := eventsWithPrefixes(decodeSSEEvents(t, rr.Body.String()), "TEXT_MESSAGE_", "TOOL_CALL_")
	// The text message lifecycle must be fully closed before the tool-call
	// lifecycle opens, rather than the tool call nesting inside an open message.
	assertEventTypes(t, lifecycle, []string{
		"TEXT_MESSAGE_START",
		"TEXT_MESSAGE_CONTENT",
		"TEXT_MESSAGE_END",
		"TOOL_CALL_START",
		"TOOL_CALL_ARGS",
		"TOOL_CALL_END",
	})
}

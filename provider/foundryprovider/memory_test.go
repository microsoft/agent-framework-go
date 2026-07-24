// Copyright (c) Microsoft. All rights reserved.

package foundryprovider_test

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/microsoft/agent-framework-go/agent"
	"github.com/microsoft/agent-framework-go/message"
	"github.com/microsoft/agent-framework-go/provider/foundryprovider"
)

func TestNewMemoryProviderPanicsWithInvalidArguments(t *testing.T) {
	tests := []struct {
		name string
		act  func()
	}{
		{
			name: "empty endpoint",
			act: func() {
				_ = foundryprovider.NewMemoryProvider(" ", validCredential, "memory", validScope, foundryprovider.MemoryProviderConfig{})
			},
		},
		{
			name: "nil credential",
			act: func() {
				_ = foundryprovider.NewMemoryProvider(validEndpoint, nil, "memory", validScope, foundryprovider.MemoryProviderConfig{})
			},
		},
		{
			name: "empty memory store",
			act: func() {
				_ = foundryprovider.NewMemoryProvider(validEndpoint, validCredential, " ", validScope, foundryprovider.MemoryProviderConfig{})
			},
		},
		{
			name: "nil scope",
			act: func() {
				_ = foundryprovider.NewMemoryProvider(validEndpoint, validCredential, "memory", nil, foundryprovider.MemoryProviderConfig{})
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assertPanics(t, tt.act)
		})
	}
}

func TestNewMemoryProviderSucceedsWithValidParameters(t *testing.T) {
	provider := foundryprovider.NewMemoryProvider(validEndpoint, validCredential, "memory", validScope, foundryprovider.MemoryProviderConfig{})
	if provider == nil {
		t.Fatal("provider is nil")
	}
	messages, options, err := provider.Invoking(t.Context(), agent.InvokingContext{})
	if err != nil {
		t.Fatalf("Invoking error = %v", err)
	}
	if len(messages) != 0 || len(options) != 0 {
		t.Fatalf("messages/options = %d/%d, want 0/0", len(messages), len(options))
	}
}

func TestNewMemoryProviderUsesCustomSearchInputFilter(t *testing.T) {
	called := false
	filter := func(context.Context, []*message.Message) ([]*message.Message, error) {
		called = true
		return nil, nil
	}
	provider := foundryprovider.NewMemoryProvider(validEndpoint, validCredential, "memory", validScope, foundryprovider.MemoryProviderConfig{SearchInputFilter: filter})

	_, _, err := provider.Invoking(t.Context(), agent.InvokingContext{Messages: []*message.Message{message.NewText("hello")}})
	if err != nil {
		t.Fatalf("Invoking error = %v", err)
	}
	if !called {
		t.Fatal("custom search input filter was not called")
	}
}

func TestMemoryProviderPanicsWhenScopeIsEmptyOnUse(t *testing.T) {
	provider := foundryprovider.NewMemoryProvider(validEndpoint, validCredential, "memory", func(*agent.Session) string { return " " }, foundryprovider.MemoryProviderConfig{})
	assertPanics(t, func() {
		_, _, _ = provider.Invoking(t.Context(), agent.InvokingContext{Messages: []*message.Message{message.NewText("hello")}})
	})
}

func TestMemoryProviderInvokingSearchesAndInjectsRetrievedMemories(t *testing.T) {
	transport := &recordingTransport{}
	transport.handle = func(req *http.Request, _ string) (*http.Response, error) {
		return jsonResponse(req, http.StatusOK, `{
			"memories":[
				{"memory_item":{"content":"memory one","kind":"user_profile","memory_id":"mem_1","scope":"user-456","updated_at":1700000000}},
				{"memory_item":{"content":" memory two ","kind":"procedural","memory_id":"mem_2","scope":"user-456","updated_at":1700000001}}
			],
			"search_id":"search_1"
		}`), nil
	}
	provider := foundryprovider.NewMemoryProvider(validEndpoint, validCredential, "memory", validScope, foundryprovider.MemoryProviderConfig{
		ClientOptions: azcore.ClientOptions{Transport: transport},
		ContextPrompt: "Memories:",
		MaxMemories:   2,
	})

	messages, options, err := provider.Invoking(t.Context(), agent.InvokingContext{Messages: []*message.Message{message.NewText("what do you remember?")}})
	if err != nil {
		t.Fatalf("Invoking error = %v", err)
	}
	if len(options) != 0 {
		t.Fatalf("options length = %d, want 0", len(options))
	}
	if len(messages) != 2 {
		t.Fatalf("messages length = %d, want 2", len(messages))
	}
	if got := messages[1].String(); got != "Memories:\nmemory one\nmemory two" {
		t.Fatalf("context message = %q", got)
	}
	if messages[1].Source.Type != agent.SourceTypeContextProvider {
		t.Fatalf("context source type = %q", messages[1].Source.Type)
	}

	requests := transport.Requests()
	if len(requests) != 1 {
		t.Fatalf("request count = %d, want 1", len(requests))
	}
	req := requests[0]
	if req.Method != http.MethodPost || req.Path != "/memory_stores/memory:search_memories" {
		t.Fatalf("request = %s %s", req.Method, req.Path)
	}
	if req.Query != "api-version=v1" {
		t.Fatalf("query = %q", req.Query)
	}
	if got := req.Header.Get("Foundry-Features"); got != "MemoryStores=V1Preview" {
		t.Fatalf("Foundry-Features = %q", got)
	}
	body := jsonMap(t, req.Body)
	if body["scope"] != "user-456" {
		t.Fatalf("scope = %#v", body["scope"])
	}
	optionsBody, ok := body["options"].(map[string]any)
	if !ok || optionsBody["max_memories"] != float64(2) {
		t.Fatalf("options = %#v", body["options"])
	}
	items, ok := body["items"].([]any)
	if !ok || len(items) != 1 {
		t.Fatalf("items = %#v", body["items"])
	}
	item := items[0].(map[string]any)
	if item["role"] != "user" {
		t.Fatalf("item role = %#v", item["role"])
	}
}

func TestMemoryProviderInvokingSearchFailureLogsAndReturnsOriginalMessages(t *testing.T) {
	transport := &recordingTransport{handle: func(req *http.Request, _ string) (*http.Response, error) {
		return jsonResponse(req, http.StatusInternalServerError, `{"error":"boom"}`), nil
	}}
	var logs bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
	request := message.NewText("hello")
	provider := foundryprovider.NewMemoryProvider(validEndpoint, validCredential, "memory", validScope, foundryprovider.MemoryProviderConfig{
		ClientOptions: azcore.ClientOptions{Transport: transport},
		Logger:        logger,
	})

	messages, _, err := provider.Invoking(t.Context(), agent.InvokingContext{Messages: []*message.Message{request}})
	if err != nil {
		t.Fatalf("Invoking error = %v", err)
	}
	if len(messages) != 1 || messages[0] != request {
		t.Fatalf("messages = %#v", messages)
	}
	logText := logs.String()
	if !strings.Contains(logText, "foundrymemory: failed to search memories") {
		t.Fatalf("logs = %q", logText)
	}
	if strings.Contains(logText, "user-456") {
		t.Fatalf("logs should not include scope: %q", logText)
	}
}

func TestMemoryProviderInvokedUpdatesMemories(t *testing.T) {
	transport := &recordingTransport{}
	transport.handle = func(req *http.Request, _ string) (*http.Response, error) {
		resp := jsonResponse(req, http.StatusAccepted, `{"update_id":"update_1","status":"queued"}`)
		resp.Header.Set("Operation-Location", validEndpoint+"/memory_stores/memory/updates/update_1?api-version=v1")
		return resp, nil
	}
	provider := foundryprovider.NewMemoryProvider(validEndpoint, validCredential, "memory", validScope, foundryprovider.MemoryProviderConfig{
		ClientOptions: azcore.ClientOptions{Transport: transport},
		UpdateDelay:   7,
	})

	err := provider.Invoked(t.Context(), agent.InvokedContext{
		RequestMessages: []*message.Message{
			message.NewText("remember me"),
			{Role: message.RoleTool, Contents: message.Contents{&message.TextContent{Text: "skip tool"}}},
		},
		ResponseMessages: []*message.Message{{Role: message.RoleAssistant, Contents: message.Contents{&message.TextContent{Text: "assistant text"}}}},
	})
	if err != nil {
		t.Fatalf("Invoked error = %v", err)
	}

	requests := transport.Requests()
	if len(requests) != 1 {
		t.Fatalf("request count = %d, want 1", len(requests))
	}
	req := requests[0]
	if req.Method != http.MethodPost || req.Path != "/memory_stores/memory:update_memories" {
		t.Fatalf("request = %s %s", req.Method, req.Path)
	}
	if got := req.Header.Get("Foundry-Features"); got != "MemoryStores=V1Preview" {
		t.Fatalf("Foundry-Features = %q", got)
	}
	body := jsonMap(t, req.Body)
	if body["scope"] != "user-456" {
		t.Fatalf("scope = %#v", body["scope"])
	}
	if body["update_delay"] != float64(7) {
		t.Fatalf("update_delay = %#v", body["update_delay"])
	}
	items, ok := body["items"].([]any)
	if !ok || len(items) != 2 {
		t.Fatalf("items = %#v", body["items"])
	}
	if items[0].(map[string]any)["role"] != "user" || items[1].(map[string]any)["role"] != "assistant" {
		t.Fatalf("items = %#v", items)
	}
}

func TestMemoryProviderInvokedSkipsStoreWhenInvocationFailed(t *testing.T) {
	transport := &recordingTransport{}
	provider := foundryprovider.NewMemoryProvider(validEndpoint, validCredential, "memory", validScope, foundryprovider.MemoryProviderConfig{
		ClientOptions: azcore.ClientOptions{Transport: transport},
	})

	if err := provider.Invoked(t.Context(), agent.InvokedContext{RequestMessages: []*message.Message{message.NewText("remember me")}, Err: errors.New("run failed")}); err != nil {
		t.Fatalf("Invoked error = %v", err)
	}
	if got := len(transport.Requests()); got != 0 {
		t.Fatalf("request count = %d, want 0", got)
	}
}

func TestMemoryProviderInvokedLogsUpdateFailureAndDoesNotReturnError(t *testing.T) {
	expected := errors.New("update failed")
	transport := &recordingTransport{handle: func(*http.Request, string) (*http.Response, error) {
		return nil, expected
	}}
	var logs bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
	provider := foundryprovider.NewMemoryProvider(validEndpoint, validCredential, "memory", validScope, foundryprovider.MemoryProviderConfig{
		ClientOptions: azcore.ClientOptions{Transport: transport},
		Logger:        logger,
	})

	err := provider.Invoked(t.Context(), agent.InvokedContext{RequestMessages: []*message.Message{message.NewText("remember me")}})
	if err != nil {
		t.Fatalf("Invoked error = %v", err)
	}

	logText := logs.String()
	if !strings.Contains(logText, "foundrymemory: failed to update memories") || !strings.Contains(logText, expected.Error()) {
		t.Fatalf("logs = %q", logText)
	}
	if strings.Contains(logText, "user-456") {
		t.Fatalf("logs should not include scope: %q", logText)
	}
}

func TestMemoryProviderEnsureMemoryStoreCreated(t *testing.T) {
	description := "team memory"
	tests := []struct {
		name        string
		description *string
		handle      func(req *http.Request, body string) (*http.Response, error)
		wantErr     bool
		wantPaths   []string
	}{
		{
			name: "store exists returns nil without create",
			handle: func(req *http.Request, _ string) (*http.Response, error) {
				return jsonResponse(req, http.StatusOK, `{"name":"memory"}`), nil
			},
			wantPaths: []string{"/memory_stores/memory"},
		},
		{
			name:        "store missing creates default store",
			description: &description,
			handle: func(req *http.Request, _ string) (*http.Response, error) {
				if req.Method == http.MethodGet {
					return jsonResponse(req, http.StatusNotFound, `{"error":{"code":"NotFound"}}`), nil
				}
				return jsonResponse(req, http.StatusOK, `{"name":"memory"}`), nil
			},
			wantPaths: []string{"/memory_stores/memory", "/memory_stores"},
		},
		{
			name:        "concurrent create conflict is treated as no-op",
			description: &description,
			handle: func(req *http.Request, _ string) (*http.Response, error) {
				if req.Method == http.MethodGet {
					return jsonResponse(req, http.StatusNotFound, `{"error":{"code":"NotFound"}}`), nil
				}
				return jsonResponse(req, http.StatusConflict, `{"error":{"code":"Conflict"}}`), nil
			},
			wantPaths: []string{"/memory_stores/memory", "/memory_stores"},
		},
		{
			name: "non-404 error is propagated",
			handle: func(req *http.Request, _ string) (*http.Response, error) {
				return jsonResponse(req, http.StatusInternalServerError, `{"error":{"code":"Boom"}}`), nil
			},
			wantErr:   true,
			wantPaths: []string{"/memory_stores/memory"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			transport := &recordingTransport{handle: tt.handle}
			provider := foundryprovider.NewMemoryProvider(validEndpoint, validCredential, "memory", validScope, foundryprovider.MemoryProviderConfig{
				ClientOptions: azcore.ClientOptions{Transport: transport, Retry: policy.RetryOptions{MaxRetries: -1}},
			})

			err := provider.EnsureMemoryStoreCreated(t.Context(), "gpt-4o-mini", "text-embedding-3-small", tt.description)
			if tt.wantErr != (err != nil) {
				t.Fatalf("EnsureMemoryStoreCreated error = %v, wantErr = %v", err, tt.wantErr)
			}

			requests := transport.Requests()
			if len(requests) != len(tt.wantPaths) {
				t.Fatalf("request count = %d, want %d", len(requests), len(tt.wantPaths))
			}
			for i, want := range tt.wantPaths {
				if requests[i].Path != want {
					t.Fatalf("request[%d] path = %q, want %q", i, requests[i].Path, want)
				}
			}
			if requests[0].Method != http.MethodGet {
				t.Fatalf("first request method = %q, want GET", requests[0].Method)
			}

			if len(tt.wantPaths) != 2 {
				return
			}
			post := requests[1]
			if post.Method != http.MethodPost {
				t.Fatalf("second request method = %q, want POST", post.Method)
			}
			body := jsonMap(t, post.Body)
			if body["name"] != "memory" || body["description"] != "team memory" {
				t.Fatalf("create body = %#v", body)
			}
			definition, ok := body["definition"].(map[string]any)
			if !ok {
				t.Fatalf("definition = %#v", body["definition"])
			}
			if definition["chat_model"] != "gpt-4o-mini" || definition["embedding_model"] != "text-embedding-3-small" {
				t.Fatalf("definition = %#v", definition)
			}
		})
	}
}

func validScope(*agent.Session) string { return "user-456" }

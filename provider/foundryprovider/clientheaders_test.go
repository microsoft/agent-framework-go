// Copyright (c) Microsoft. All rights reserved.

package foundryprovider_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/microsoft/agent-framework-go/agent"
	"github.com/microsoft/agent-framework-go/provider/foundryprovider"
	"github.com/openai/openai-go/v3/option"
)

func TestWithClientHeaderStampsRequest(t *testing.T) {
	var requests atomic.Int64
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if got := r.Header.Get("x-client-end-user-id"); got != "user-123" {
			t.Fatalf("x-client-end-user-id = %q", got)
		}
		writeResponsesOK(w)
	}))
	defer server.Close()

	foundryAgent := newFoundryAgent(t, server, foundryprovider.ModelDeployment("gpt-4o-mini"), foundryprovider.AgentConfig{})

	if _, err := foundryAgent.RunText(t.Context(), "hello", foundryprovider.WithClientHeader("x-client-end-user-id", "user-123")).Collect(); err != nil {
		t.Fatalf("RunText error = %v", err)
	}
	if got := requests.Load(); got == 0 {
		t.Fatal("no request was sent")
	}
}

func TestWithClientHeaderRejectsInvalidArguments(t *testing.T) {
	tests := []struct {
		name   string
		header string
		value  string
	}{
		{name: "authorization", header: "authorization", value: "secret"},
		{name: "custom header", header: "x-custom-header", value: "value"},
		{name: "missing prefix", header: "client-end-user-id", value: "value"},
		{name: "missing hyphen after x", header: "xclient-end-user-id", value: "value"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assertPanics(t, func() { _ = foundryprovider.WithClientHeader(tt.header, tt.value) })
		})
	}
}

func TestWithClientHeadersRejectsInvalidHeader(t *testing.T) {
	headers := map[string]string{
		"x-client-end-user-id": "alice",
		"authorization":        "secret",
		"x-client-chat-id":     "chat-1",
	}
	var result agent.Option
	assertPanics(t, func() {
		result = foundryprovider.WithClientHeaders(headers)
	})
	if result != nil {
		t.Fatal("invalid headers returned a partially initialized option")
	}
	if len(headers) != 3 || headers["x-client-end-user-id"] != "alice" ||
		headers["authorization"] != "secret" || headers["x-client-chat-id"] != "chat-1" {
		t.Fatalf("caller headers were mutated: %#v", headers)
	}
}

func TestWithClientHeaderRejectsBlankName(t *testing.T) {
	for _, name := range []string{"", "   "} {
		t.Run(fmt.Sprintf("%q", name), func(t *testing.T) {
			assertPanics(t, func() { foundryprovider.WithClientHeader(name, "v") }, "client header name is required")
		})
	}
}

func TestWithClientHeaderRejectsEmptyValue(t *testing.T) {
	assertPanics(t, func() {
		foundryprovider.WithClientHeader("x-client-foo", "")
	}, "client header value is required")
}

func TestClientHeadersWithoutRunOptionsDoNotStampRequest(t *testing.T) {
	var requests int
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		for name := range r.Header {
			if strings.HasPrefix(strings.ToLower(name), "x-client-") {
				t.Errorf("unexpected client header %q", name)
			}
		}
		writeResponsesOK(w)
	}))
	defer server.Close()
	a := newFoundryAgent(t, server, foundryprovider.ModelDeployment("gpt-4o-mini"), foundryprovider.AgentConfig{})
	if _, err := a.RunText(t.Context(), "hi").Collect(); err != nil {
		t.Fatal(err)
	}
	if requests != 1 {
		t.Fatalf("requests = %d, want 1", requests)
	}
}

func TestClientHeadersOverwritePreexistingHeader(t *testing.T) {
	var requests int
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if got := r.Header.Values("x-client-end-user-id"); len(got) != 1 || got[0] != "alice" {
			t.Errorf("x-client-end-user-id = %#v, want [alice]", got)
		}
		writeResponsesOK(w)
	}))
	defer server.Close()
	a := newFoundryAgent(t, server, foundryprovider.ModelDeployment("gpt-4o-mini"), foundryprovider.AgentConfig{
		OpenAIOptions: []option.RequestOption{option.WithHeader("x-client-end-user-id", "initial")},
	})
	if _, err := a.RunText(t.Context(), "hi", foundryprovider.WithClientHeader("x-client-end-user-id", "alice")).Collect(); err != nil {
		t.Fatal(err)
	}
	if requests != 1 {
		t.Fatalf("requests = %d, want 1", requests)
	}
}

func TestClientHeadersStreamingStampsRequest(t *testing.T) {
	responseJSON, err := json.Marshal(json.RawMessage(minimalResponsesJSON))
	if err != nil {
		t.Fatal(err)
	}
	var requests int
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if got := r.Header.Get("x-client-end-user-id"); got != "carol" {
			t.Errorf("x-client-end-user-id = %q, want carol", got)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprint(w, "event: response.output_text.delta\ndata: {\"type\":\"response.output_text.delta\",\"delta\":\"hello\",\"item_id\":\"msg_test\",\"output_index\":0,\"content_index\":0}\n\n")
		_, _ = fmt.Fprintf(w, "event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":%s}\n\n", responseJSON)
	}))
	defer server.Close()
	a := newFoundryAgent(t, server, foundryprovider.ModelDeployment("gpt-4o-mini"), foundryprovider.AgentConfig{})
	response, err := a.RunText(t.Context(), "hi", agent.Stream(true), foundryprovider.WithClientHeader("x-client-end-user-id", "carol")).Collect()
	if err != nil {
		t.Fatal(err)
	}
	if requests != 1 || response.String() != "hello" {
		t.Fatalf("requests = %d, response = %q", requests, response.String())
	}
}

func TestWithClientHeadersClonesInputMap(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("x-client-end-user-id"); got != "alice" {
			t.Fatalf("x-client-end-user-id = %q", got)
		}
		writeResponsesOK(w)
	}))
	defer server.Close()

	headers := map[string]string{"x-client-end-user-id": "alice"}
	option := foundryprovider.WithClientHeaders(headers)
	headers["x-client-end-user-id"] = "bob"

	foundryAgent := newFoundryAgent(t, server, foundryprovider.ModelDeployment("gpt-4o-mini"), foundryprovider.AgentConfig{})
	if _, err := foundryAgent.RunText(t.Context(), "hello", option).Collect(); err != nil {
		t.Fatalf("RunText error = %v", err)
	}
}

func TestWithClientHeaderAccumulatesAndUpserts(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var count int
		for name := range r.Header {
			if strings.HasPrefix(strings.ToLower(name), "x-client-") {
				count++
			}
		}
		if count != 2 {
			t.Errorf("client header count = %d, want 2", count)
		}
		if got := r.Header.Get("x-client-a"); got != "1-updated" {
			t.Fatalf("x-client-a = %q", got)
		}
		if got := r.Header.Get("x-client-b"); got != "2" {
			t.Fatalf("x-client-b = %q", got)
		}
		writeResponsesOK(w)
	}))
	defer server.Close()

	foundryAgent := newFoundryAgent(t, server, foundryprovider.ModelDeployment("gpt-4o-mini"), foundryprovider.AgentConfig{})
	_, err := foundryAgent.RunText(t.Context(), "hello",
		foundryprovider.WithClientHeader("x-client-a", "1"),
		foundryprovider.WithClientHeader("x-client-b", "2"),
		foundryprovider.WithClientHeader("x-client-a", "1-updated"),
	).Collect()
	if err != nil {
		t.Fatalf("RunText error = %v", err)
	}
}

func TestWithClientHeaderUpsertsCaseInsensitively(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Values("x-client-a"); len(got) != 1 || got[0] != "updated" {
			t.Fatalf("x-client-a = %#v, want [updated]", got)
		}
		writeResponsesOK(w)
	}))
	defer server.Close()

	foundryAgent := newFoundryAgent(t, server, foundryprovider.ModelDeployment("gpt-4o-mini"), foundryprovider.AgentConfig{})
	_, err := foundryAgent.RunText(t.Context(), "hello",
		foundryprovider.WithClientHeader("x-client-a", "first"),
		foundryprovider.WithClientHeader("X-CLIENT-A", "updated"),
	).Collect()
	if err != nil {
		t.Fatalf("RunText error = %v", err)
	}
}

func TestWithClientHeaderDoesNotLeakToSubsequentRun(t *testing.T) {
	var mu sync.Mutex
	var values []string
	var afterRun1 int
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		values = append(values, r.Header.Get("x-client-end-user-id"))
		if afterRun1 > 0 {
			for name := range r.Header {
				if strings.EqualFold(name, "x-client-end-user-id") {
					t.Fatalf("second-run request retained header key %q: %#v", name, r.Header[name])
				}
			}
		}
		writeResponsesOK(w)
	}))
	defer server.Close()

	foundryAgent := newFoundryAgent(t, server, foundryprovider.ModelDeployment("gpt-4o-mini"), foundryprovider.AgentConfig{})
	if _, err := foundryAgent.RunText(t.Context(), "hello", foundryprovider.WithClientHeader("x-client-end-user-id", "alice")).Collect(); err != nil {
		t.Fatalf("first RunText error = %v", err)
	}
	mu.Lock()
	afterRun1 = len(values)
	var firstValue string
	if afterRun1 > 0 {
		firstValue = values[0]
	}
	mu.Unlock()
	if afterRun1 == 0 {
		t.Fatal("first run sent no HTTP requests")
	}
	if got := firstValue; got != "alice" {
		t.Fatalf("first-run x-client-end-user-id = %q, want alice", got)
	}
	if _, err := foundryAgent.RunText(t.Context(), "hello").Collect(); err != nil {
		t.Fatalf("second RunText error = %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(values) <= afterRun1 {
		t.Fatal("second run sent no HTTP requests")
	}
	if len(values) != 2 {
		t.Fatalf("request count = %d", len(values))
	}
	if values[0] != "alice" || values[1] != "" {
		t.Fatalf("x-client-end-user-id values = %#v", values)
	}
}

func TestWithHostedAgentUserIdentityStampsRequest(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get(foundryprovider.HostedAgentUserIdentityHeader); got != "user-123" {
			t.Fatalf("%s = %q", foundryprovider.HostedAgentUserIdentityHeader, got)
		}
		writeResponsesOK(w)
	}))
	defer server.Close()

	foundryAgent := newFoundryAgent(t, server, foundryprovider.ServerAgent("my-agent"), foundryprovider.AgentConfig{})

	if _, err := foundryAgent.RunText(t.Context(), "hello", foundryprovider.WithHostedAgentUserIdentity("user-123")).Collect(); err != nil {
		t.Fatalf("RunText error = %v", err)
	}
}

func TestWithHostedAgentUserIdentityRejectsInvalidArguments(t *testing.T) {
	tests := []struct {
		name         string
		userIdentity string
	}{
		{name: "empty", userIdentity: ""},
		{name: "whitespace", userIdentity: "   "},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assertPanics(t, func() { _ = foundryprovider.WithHostedAgentUserIdentity(tt.userIdentity) }, "hosted agent user identity is required")
		})
	}
}

func TestWithHostedAgentUserIdentityDoesNotLeakToSubsequentRun(t *testing.T) {
	var values []string
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		values = append(values, r.Header.Get(foundryprovider.HostedAgentUserIdentityHeader))
		writeResponsesOK(w)
	}))
	defer server.Close()

	foundryAgent := newFoundryAgent(t, server, foundryprovider.ServerAgent("my-agent"), foundryprovider.AgentConfig{})
	if _, err := foundryAgent.RunText(t.Context(), "hello", foundryprovider.WithHostedAgentUserIdentity("alice")).Collect(); err != nil {
		t.Fatalf("first RunText error = %v", err)
	}
	if _, err := foundryAgent.RunText(t.Context(), "hello").Collect(); err != nil {
		t.Fatalf("second RunText error = %v", err)
	}
	if len(values) != 2 {
		t.Fatalf("request count = %d", len(values))
	}
	if values[0] != "alice" || values[1] != "" {
		t.Fatalf("%s values = %#v", foundryprovider.HostedAgentUserIdentityHeader, values)
	}
}

func TestWithHostedAgentUserIdentityCoexistsWithClientHeaders(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get(foundryprovider.HostedAgentUserIdentityHeader); got != "user-123" {
			t.Fatalf("%s = %q", foundryprovider.HostedAgentUserIdentityHeader, got)
		}
		if got := r.Header.Get("x-client-chat-id"); got != "chat-42" {
			t.Fatalf("x-client-chat-id = %q", got)
		}
		writeResponsesOK(w)
	}))
	defer server.Close()

	foundryAgent := newFoundryAgent(t, server, foundryprovider.ServerAgent("my-agent"), foundryprovider.AgentConfig{})

	if _, err := foundryAgent.RunText(t.Context(), "hello",
		foundryprovider.WithHostedAgentUserIdentity("user-123"),
		foundryprovider.WithClientHeader("x-client-chat-id", "chat-42"),
	).Collect(); err != nil {
		t.Fatalf("RunText error = %v", err)
	}
}

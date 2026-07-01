// Copyright (c) Microsoft. All rights reserved.

package foundryprovider_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/microsoft/agent-framework-go/agent"
	"github.com/microsoft/agent-framework-go/provider/foundryprovider"
)

func TestWithClientHeaderStampsRequest(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("x-client-end-user-id"); got != "user-123" {
			t.Fatalf("x-client-end-user-id = %q", got)
		}
		writeResponsesOK(w)
	}))
	defer server.Close()

	foundryAgent := newFoundryAgent(t, server, foundryprovider.ModelDeployment("gpt-4o-mini"), foundryprovider.AgentConfig{
		Config: agent.Config{DisableFuncAutoCall: true},
	})

	if _, err := foundryAgent.RunText(t.Context(), "hello", foundryprovider.WithClientHeader("x-client-end-user-id", "user-123")).Collect(); err != nil {
		t.Fatalf("RunText error = %v", err)
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
		{name: "empty name", header: "", value: "value"},
		{name: "whitespace name", header: "   ", value: "value"},
		{name: "empty value", header: "x-client-end-user-id", value: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assertPanics(t, func() { _ = foundryprovider.WithClientHeader(tt.header, tt.value) })
		})
	}
}

func TestWithClientHeadersRejectsInvalidHeader(t *testing.T) {
	assertPanics(t, func() {
		_ = foundryprovider.WithClientHeaders(map[string]string{
			"x-client-end-user-id": "alice",
			"authorization":        "secret",
			"x-client-chat-id":     "chat-1",
		})
	})
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

	foundryAgent := newFoundryAgent(t, server, foundryprovider.ModelDeployment("gpt-4o-mini"), foundryprovider.AgentConfig{
		Config: agent.Config{DisableFuncAutoCall: true},
	})
	if _, err := foundryAgent.RunText(t.Context(), "hello", option).Collect(); err != nil {
		t.Fatalf("RunText error = %v", err)
	}
}

func TestWithClientHeaderAccumulatesAndUpserts(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("x-client-a"); got != "1-updated" {
			t.Fatalf("x-client-a = %q", got)
		}
		if got := r.Header.Get("x-client-b"); got != "2" {
			t.Fatalf("x-client-b = %q", got)
		}
		writeResponsesOK(w)
	}))
	defer server.Close()

	foundryAgent := newFoundryAgent(t, server, foundryprovider.ModelDeployment("gpt-4o-mini"), foundryprovider.AgentConfig{
		Config: agent.Config{DisableFuncAutoCall: true},
	})
	_, err := foundryAgent.RunText(t.Context(), "hello",
		foundryprovider.WithClientHeader("x-client-a", "1"),
		foundryprovider.WithClientHeader("x-client-b", "2"),
		foundryprovider.WithClientHeader("x-client-a", "1-updated"),
	).Collect()
	if err != nil {
		t.Fatalf("RunText error = %v", err)
	}
}

func TestWithClientHeaderDoesNotLeakToSubsequentRun(t *testing.T) {
	var values []string
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		values = append(values, r.Header.Get("x-client-end-user-id"))
		writeResponsesOK(w)
	}))
	defer server.Close()

	foundryAgent := newFoundryAgent(t, server, foundryprovider.ModelDeployment("gpt-4o-mini"), foundryprovider.AgentConfig{
		Config: agent.Config{DisableFuncAutoCall: true},
	})
	if _, err := foundryAgent.RunText(t.Context(), "hello", foundryprovider.WithClientHeader("x-client-end-user-id", "alice")).Collect(); err != nil {
		t.Fatalf("first RunText error = %v", err)
	}
	if _, err := foundryAgent.RunText(t.Context(), "hello").Collect(); err != nil {
		t.Fatalf("second RunText error = %v", err)
	}
	if len(values) != 2 {
		t.Fatalf("request count = %d", len(values))
	}
	if values[0] != "alice" || values[1] != "" {
		t.Fatalf("x-client-end-user-id values = %#v", values)
	}
}

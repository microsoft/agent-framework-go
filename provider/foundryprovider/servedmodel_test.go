// Copyright (c) Microsoft. All rights reserved.

package foundryprovider_test

import (
	"context"
	"iter"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/microsoft/agent-framework-go/agent"
	"github.com/microsoft/agent-framework-go/message"
	"github.com/microsoft/agent-framework-go/provider/foundryprovider"
)

func TestServedModelHeaderUpdatesResponseMetadata(t *testing.T) {
	tests := []struct {
		name        string
		headerValue string
		want        any
	}{
		{name: "header present", headerValue: "gpt-5-nano-2025-08-07", want: "gpt-5-nano-2025-08-07"},
		{name: "header trimmed", headerValue: "  gpt-5-nano-2025-08-07  ", want: "gpt-5-nano-2025-08-07"},
		{name: "empty header ignored", headerValue: "", want: nil},
		{name: "whitespace header ignored", headerValue: "   ", want: nil},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				if tt.headerValue != "" {
					w.Header().Set("x-ms-served-model", tt.headerValue)
				}
				writeResponsesOK(w)
			}))
			defer server.Close()

			foundryAgent := newFoundryAgent(t, server, foundryprovider.ModelDeployment("gpt-4o-mini"), foundryprovider.AgentConfig{})

			resp, err := foundryAgent.RunText(t.Context(), "hello").Collect()
			if err != nil {
				t.Fatalf("RunText error = %v", err)
			}
			if got := resp.AdditionalProperties["ServedModel"]; got != tt.want {
				t.Fatalf("ServedModel = %#v, want %#v", got, tt.want)
			}
		})
	}
}

func TestServedModelHeaderDoesNotLeakAcrossServiceCalls(t *testing.T) {
	requestCount := 0
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requestCount++
		if requestCount == 1 {
			w.Header().Set("x-ms-served-model", "first-model")
		}
		writeResponsesOK(w)
	}))
	defer server.Close()

	twice := agent.MiddlewareFunc(func(next agent.RunFunc, ctx context.Context, messages []*message.Message, options ...agent.Option) iter.Seq2[*agent.ResponseUpdate, error] {
		return func(yield func(*agent.ResponseUpdate, error) bool) {
			for range 2 {
				for update, err := range next(ctx, messages, options...) {
					if !yield(update, err) {
						return
					}
				}
			}
		}
	})
	foundryAgent := newFoundryAgent(t, server, foundryprovider.ModelDeployment("gpt-4o-mini"), foundryprovider.AgentConfig{
		Config: agent.Config{Middlewares: []agent.Middleware{twice}},
	})

	var updates []*agent.ResponseUpdate
	for update, err := range foundryAgent.RunText(t.Context(), "hello") {
		if err != nil {
			t.Fatalf("RunText error = %v", err)
		}
		updates = append(updates, update)
	}
	if len(updates) != 2 {
		t.Fatalf("updates = %d, want 2", len(updates))
	}
	if got := updates[0].AdditionalProperties["ServedModel"]; got != "first-model" {
		t.Fatalf("first ServedModel = %#v, want first-model", got)
	}
	if got := updates[1].AdditionalProperties["ServedModel"]; got != nil {
		t.Fatalf("second ServedModel = %#v, want nil", got)
	}
}

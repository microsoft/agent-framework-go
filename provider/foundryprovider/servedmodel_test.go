// Copyright (c) Microsoft. All rights reserved.

package foundryprovider_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/microsoft/agent-framework-go/agent"
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

			foundryAgent := newFoundryAgent(t, server, foundryprovider.ModelDeployment("gpt-4o-mini"), foundryprovider.AgentConfig{
				Config: agent.Config{DisableFuncAutoCall: true},
			})

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

// Copyright (c) Microsoft. All rights reserved.

package foundryprovider_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/microsoft/agent-framework-go/agent"
	"github.com/microsoft/agent-framework-go/provider/foundryprovider"
	"github.com/microsoft/agent-framework-go/provider/openaiprovider"
	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/azure"
	"github.com/openai/openai-go/v3/option"
)

func TestNewAgentUsesProjectResponsesEndpointAndConfig(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Fatalf("method = %q, want %q", r.Method, http.MethodPost)
		}
		if r.URL.Path != "/projects/proj/openai/v1/responses" {
			t.Fatalf("path = %q", r.URL.Path)
		}
		if got := r.URL.Query().Get("api-version"); got != "" {
			t.Fatalf("api-version = %q, want omitted", got)
		}
		body := jsonMap(t, mustReadBody(t, r))
		if body["model"] != "gpt-4o-mini" {
			t.Fatalf("model = %#v", body["model"])
		}
		if body["instructions"] != "Be concise." {
			t.Fatalf("instructions = %#v", body["instructions"])
		}
		writeResponsesOK(w)
	}))
	defer server.Close()

	foundryAgent := newFoundryAgent(t, server, foundryprovider.ModelDeployment("gpt-4o-mini"), foundryprovider.AgentConfig{
		Config: agent.Config{
			Name:        "test-agent",
			Description: "A test agent",
		},
		Instructions: "Be concise.",
	})
	if foundryAgent.Name() != "test-agent" {
		t.Fatalf("Name = %q", foundryAgent.Name())
	}
	if foundryAgent.Description() != "A test agent" {
		t.Fatalf("Description = %q", foundryAgent.Description())
	}
	if foundryAgent.ProviderName() != "microsoft.foundry" {
		t.Fatalf("ProviderName = %q, want %q", foundryAgent.ProviderName(), "microsoft.foundry")
	}

	resp, err := foundryAgent.RunText(t.Context(), "hello").Collect()
	if err != nil {
		t.Fatalf("RunText error = %v", err)
	}
	if got := resp.String(); got != "hello" {
		t.Fatalf("response text = %q", got)
	}
}

func TestNewAgentDisableStoreOutputSetsStoreFalse(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body := jsonMap(t, mustReadBody(t, r))
		if body["store"] != false {
			t.Fatalf("store = %#v, want false", body["store"])
		}
		writeResponsesOK(w)
	}))
	defer server.Close()

	foundryAgent := newFoundryAgent(t, server, foundryprovider.ModelDeployment("gpt-4o-mini"), foundryprovider.AgentConfig{
		DisableStoreOutput: true,
	})
	session, err := foundryAgent.CreateSession(t.Context())
	if err != nil {
		t.Fatal(err)
	}

	if _, err := foundryAgent.RunText(t.Context(), "hello", agent.WithSession(session)).Collect(); err != nil {
		t.Fatalf("RunText error = %v", err)
	}
	if got := session.ServiceID(); got != "" {
		t.Fatalf("session ServiceID = %q, want empty", got)
	}
}

func TestNewAgentRunOptionCanDisableReasoningEncryptedContentInclude(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body := jsonMap(t, mustReadBody(t, r))
		if body["store"] != false {
			t.Fatalf("store = %#v, want false", body["store"])
		}
		if _, ok := body["include"]; ok {
			t.Fatalf("include = %#v, want omitted", body["include"])
		}
		writeResponsesOK(w)
	}))
	defer server.Close()

	foundryAgent := newFoundryAgent(t, server, foundryprovider.ModelDeployment("gpt-4o-mini"), foundryprovider.AgentConfig{
		DisableStoreOutput: true,
		Config: agent.Config{
			RunOptions: []agent.Option{
				openaiprovider.ResponsesIncludeReasoningEncryptedContent(false),
			},
		},
	})

	if _, err := foundryAgent.RunText(t.Context(), "hello").Collect(); err != nil {
		t.Fatalf("RunText error = %v", err)
	}
}

func TestNewAgentRunsAgainstFoundryAgentEndpoint(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/projects/proj/agents/my-agent/endpoint/protocols/openai/responses" {
			t.Fatalf("path = %q", r.URL.Path)
		}
		if got := r.URL.Query().Get("api-version"); got != "v1" {
			t.Fatalf("api-version = %q", got)
		}
		if got := r.Header.Get("Authorization"); got == "" {
			t.Fatal("missing Authorization header")
		}
		if got := r.Header.Get("User-Agent"); !strings.Contains(got, "agent-framework-go/") {
			t.Fatalf("User-Agent = %q", got)
		}
		body := jsonMap(t, mustReadBody(t, r))
		if _, ok := body["model"]; ok {
			t.Fatalf("request body should not include model: %#v", body)
		}
		if _, ok := body["instructions"]; ok {
			t.Fatalf("request body should not include instructions: %#v", body)
		}
		writeResponsesOK(w)
	}))
	defer server.Close()

	foundryAgent := newFoundryAgent(t, server, foundryprovider.ServerAgent("my-agent"), foundryprovider.AgentConfig{
		Instructions: "server-owned instructions should not be sent",
	})
	if foundryAgent.ID() != "my-agent" {
		t.Fatalf("ID = %q", foundryAgent.ID())
	}
	if foundryAgent.Name() != "my-agent" {
		t.Fatalf("Name = %q", foundryAgent.Name())
	}

	resp, err := foundryAgent.RunText(t.Context(), "hello").Collect()
	if err != nil {
		t.Fatalf("RunText error = %v", err)
	}
	if got := resp.String(); got != "hello" {
		t.Fatalf("response text = %q", got)
	}
	if resp.AgentID != "my-agent" {
		t.Fatalf("AgentID = %q", resp.AgentID)
	}
}

func TestNewAgentOwnedRoutingOverridesOpenAIOptions(t *testing.T) {
	attackerCalls := 0
	attacker := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		attackerCalls++
		writeResponsesOK(w)
	}))
	defer attacker.Close()

	foundryCalls := 0
	foundry := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		foundryCalls++
		if r.URL.Path != "/projects/proj/agents/my-agent/endpoint/protocols/openai/responses" {
			t.Fatalf("path = %q", r.URL.Path)
		}
		if got := r.URL.Query().Get("api-version"); got != "v1" {
			t.Fatalf("api-version = %q, want v1", got)
		}
		if got := r.Header.Get("Authorization"); got == "" {
			t.Fatal("missing Authorization header")
		}
		writeResponsesOK(w)
	}))
	defer foundry.Close()

	foundryAgent := newFoundryAgent(t, foundry, foundryprovider.ServerAgent("my-agent"), foundryprovider.AgentConfig{
		OpenAIOptions: []option.RequestOption{
			option.WithBaseURL(attacker.URL),
			option.WithQuery("api-version", "hostile"),
		},
	})
	if _, err := foundryAgent.RunText(t.Context(), "hello").Collect(); err != nil {
		t.Fatalf("RunText error = %v", err)
	}
	if attackerCalls != 0 || foundryCalls != 1 {
		t.Fatalf("request counts = attacker:%d foundry:%d, want attacker:0 foundry:1", attackerCalls, foundryCalls)
	}
}

func TestNewAgentEscapesServerAgentNameInEndpoint(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.EscapedPath(); got != "/projects/proj/agents/my%2Fagent/endpoint/protocols/openai/responses" {
			t.Fatalf("escaped path = %q", got)
		}
		writeResponsesOK(w)
	}))
	defer server.Close()

	foundryAgent := newFoundryAgent(t, server, foundryprovider.ServerAgent("my/agent"), foundryprovider.AgentConfig{})

	if _, err := foundryAgent.RunText(t.Context(), "hello").Collect(); err != nil {
		t.Fatalf("RunText error = %v", err)
	}
}

func TestNewAgentWithAPIKey(t *testing.T) {
	responseJSON, err := json.Marshal(json.RawMessage(minimalResponsesJSON))
	if err != nil {
		t.Fatal(err)
	}
	for _, target := range []foundryprovider.AgentTarget{
		foundryprovider.ModelDeployment("model"),
		foundryprovider.ServerAgent("my-agent"),
	} {
		for _, stream := range []bool{false, true} {
			for _, withKey := range []bool{false, true} {
				t.Run(fmt.Sprintf("%T/stream=%t/key=%t", target, stream, withKey), func(t *testing.T) {
					var calls int
					server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						calls++
						if r.Header.Get("Api-Key") != "test-foundry-key" || r.Header.Get("Authorization") != "" {
							t.Error("expected only Azure API-key authentication")
						}
						wantPath, wantVersion := "/projects/proj/openai/v1/responses", ""
						if _, ok := target.(foundryprovider.ServerAgent); ok {
							wantPath = "/projects/proj/agents/my-agent/endpoint/protocols/openai/responses"
							wantVersion = "v1"
						}
						if r.URL.Path != wantPath || r.URL.Query().Get("api-version") != wantVersion {
							t.Errorf("unexpected request URL: %s", r.URL)
						}
						if stream {
							w.Header().Set("Content-Type", "text/event-stream")
							_, _ = fmt.Fprint(w, "event: response.output_text.delta\ndata: {\"type\":\"response.output_text.delta\",\"delta\":\"hello\",\"item_id\":\"msg_test\",\"output_index\":0,\"content_index\":0}\n\n")
							_, _ = fmt.Fprintf(w, "event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":%s}\n\n", responseJSON)
						} else {
							writeResponsesOK(w)
						}
					}))
					defer server.Close()
					options := []option.RequestOption{option.WithHTTPClient(server.Client()), option.WithMaxRetries(0)}
					if withKey {
						options = append(options, azure.WithAPIKey("test-foundry-key"))
					}
					a := foundryprovider.NewAgent(server.URL+"/projects/proj", nil, target, foundryprovider.AgentConfig{OpenAIOptions: options})
					response, err := a.RunText(t.Context(), "hello", agent.Stream(stream)).Collect()
					if !withKey {
						if err == nil || !strings.Contains(err.Error(), "authentication is required") || calls != 0 {
							t.Fatalf("error = %v, requests = %d; want missing authentication and no requests", err, calls)
						}
						return
					}
					if err != nil {
						t.Fatal(err)
					}
					if response.String() != "hello" || calls != 1 {
						t.Fatalf("text = %q, requests = %d; want hello and 1", response.String(), calls)
					}
				})
			}
		}
	}
}

func TestNewAgentWithAPIKeyPropagatesAuthenticationErrors(t *testing.T) {
	for _, target := range []foundryprovider.AgentTarget{
		foundryprovider.ModelDeployment("model"),
		foundryprovider.ServerAgent("my-agent"),
	} {
		for _, stream := range []bool{false, true} {
			for _, status := range []int{http.StatusUnauthorized, http.StatusForbidden} {
				t.Run(fmt.Sprintf("%T/stream=%t/status=%d", target, stream, status), func(t *testing.T) {
					var calls int
					a := foundryprovider.NewAgent(validEndpoint+"/api/projects/proj", nil, target, foundryprovider.AgentConfig{
						OpenAIOptions: []option.RequestOption{
							azure.WithAPIKey("test-foundry-key"),
							option.WithMaxRetries(0),
							option.WithHTTPClient(&http.Client{Transport: foundryClientTransport(func(req *http.Request) (*http.Response, error) {
								calls++
								return jsonResponse(req, status, `{"error":{"message":"API-key authentication rejected","type":"invalid_request_error","code":"authentication_failed"}}`), nil
							})}),
						},
					})
					_, err := a.RunText(t.Context(), "hello", agent.Stream(stream)).Collect()
					var apiErr *openai.Error
					if !errors.As(err, &apiErr) || apiErr.StatusCode != status || apiErr.Code != "authentication_failed" {
						t.Fatalf("error = %v, want service authentication error with status %d", err, status)
					}
					if calls != 1 {
						t.Fatalf("requests = %d, want 1", calls)
					}
				})
			}
		}
	}
}

func TestNewAgentPanicsWithInvalidArguments(t *testing.T) {
	tests := []struct {
		name        string
		act         func()
		wantMessage string
	}{
		{
			name: "empty endpoint",
			act: func() {
				_ = foundryprovider.NewAgent(" ", validCredential, foundryprovider.ModelDeployment("gpt-4o-mini"), foundryprovider.AgentConfig{})
			},
		},
		{
			name: "empty model deployment",
			act: func() {
				_ = foundryprovider.NewAgent(validEndpoint, validCredential, foundryprovider.ModelDeployment(" "), foundryprovider.AgentConfig{})
			},
		},
		{
			name: "empty server agent name",
			act: func() {
				_ = foundryprovider.NewAgent(validEndpoint, validCredential, foundryprovider.ServerAgent(" "), foundryprovider.AgentConfig{})
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assertPanics(t, tt.act, tt.wantMessage)
		})
	}
}

// Copyright (c) Microsoft. All rights reserved.

package a2aprovider_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/a2aproject/a2a-go/v2/a2aclient"
	"github.com/a2aproject/a2a-go/v2/a2aclient/agentcard"
	"github.com/microsoft/agent-framework-go/agent"
	"github.com/microsoft/agent-framework-go/provider/a2aprovider"
)

func newFactoryTestClient(t *testing.T) *a2aclient.Client {
	t.Helper()
	client, err := a2aclient.NewFromEndpoints(t.Context(), []*a2a.AgentInterface{
		a2a.NewAgentInterface("http://test-endpoint", a2a.TransportProtocolJSONRPC),
	})
	if err != nil {
		t.Fatalf("NewFromEndpoints: %v", err)
	}
	t.Cleanup(func() {
		if err := client.Destroy(); err != nil {
			t.Errorf("Destroy: %v", err)
		}
	})
	return client
}

func TestNewAgentFromClientWithProperties(t *testing.T) {
	client := newFactoryTestClient(t)
	const (
		id          = "test-agent-id"
		name        = "Test Agent"
		description = "This is a test agent description"
	)

	a := a2aprovider.NewAgent(client, a2aprovider.AgentConfig{Config: agent.Config{
		ID: id, Name: name, Description: description,
	}})

	if a == nil {
		t.Fatal("NewAgent returned nil")
	}
	if got := a.ProviderName(); got != "a2a" {
		t.Errorf("ProviderName = %q, want %q", got, "a2a")
	}
	if got := a.ID(); got != id {
		t.Errorf("ID = %q, want %q", got, id)
	}
	if got := a.Name(); got != name {
		t.Errorf("Name = %q, want %q", got, name)
	}
	if got := a.Description(); got != description {
		t.Errorf("Description = %q, want %q", got, description)
	}
}

func TestNewAgentFromClientWithConfig(t *testing.T) {
	client := newFactoryTestClient(t)
	config := a2aprovider.AgentConfig{Config: agent.Config{
		ID:          "options-agent-id",
		Name:        "Options Agent",
		Description: "Agent created with options",
	}}

	a := a2aprovider.NewAgent(client, config)

	if a == nil {
		t.Fatal("NewAgent returned nil")
	}
	if got := a.ProviderName(); got != "a2a" {
		t.Errorf("ProviderName = %q, want %q", got, "a2a")
	}
	if got := a.ID(); got != "options-agent-id" {
		t.Errorf("ID = %q, want %q", got, "options-agent-id")
	}
	if got := a.Name(); got != "Options Agent" {
		t.Errorf("Name = %q, want %q", got, "Options Agent")
	}
	if got := a.Description(); got != "Agent created with options" {
		t.Errorf("Description = %q, want %q", got, "Agent created with options")
	}
}

func TestNewAgentFromClientWithEmptyConfig(t *testing.T) {
	client := newFactoryTestClient(t)

	a := a2aprovider.NewAgent(client, a2aprovider.AgentConfig{})

	if a == nil {
		t.Fatal("NewAgent returned nil")
	}
	if got := a.ProviderName(); got != "a2a" {
		t.Errorf("ProviderName = %q, want %q", got, "a2a")
	}
	if a.ID() == "" {
		t.Error("ID is empty")
	}
	if got := a.Name(); got != "" {
		t.Errorf("Name = %q, want empty", got)
	}
	if got := a.Description(); got != "" {
		t.Errorf("Description = %q, want empty", got)
	}
}

func TestA2ASessionJSONRoundTripPreservesIDs(t *testing.T) {
	const (
		contextID = "context-rt-001"
		taskID    = "task-rt-002"
	)
	a := a2aprovider.NewAgent(newFactoryTestClient(t), a2aprovider.AgentConfig{})
	original, err := a.CreateSession(t.Context(), agent.WithServiceID(contextID), a2aprovider.WithTaskID(taskID))
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	serialized, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	var restored agent.Session
	if err := json.Unmarshal(serialized, &restored); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}

	if got, want := restored.ServiceID(), original.ServiceID(); got != want {
		t.Errorf("ServiceID = %q, want %q", got, want)
	}
	if got, want := a2aprovider.TaskIDFromSession(&restored), a2aprovider.TaskIDFromSession(original); got != want {
		t.Errorf("TaskID = %q, want %q", got, want)
	}
}

func TestA2ASessionJSONRoundTripPreservesState(t *testing.T) {
	a := a2aprovider.NewAgent(newFactoryTestClient(t), a2aprovider.AgentConfig{})
	original, err := a.CreateSession(t.Context(), agent.WithServiceID("ctx-1"), a2aprovider.WithTaskID("task-1"))
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	original.Set("testKey", "testValue")

	serialized, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	var restored agent.Session
	if err := json.Unmarshal(serialized, &restored); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}

	if got := restored.ServiceID(); got != "ctx-1" {
		t.Errorf("ServiceID = %q, want %q", got, "ctx-1")
	}
	if got := a2aprovider.TaskIDFromSession(&restored); got != "task-1" {
		t.Errorf("TaskID = %q, want %q", got, "task-1")
	}
	var value string
	if ok, err := restored.Get("testKey", &value); err != nil || !ok {
		t.Fatalf("Get(testKey) = %v, %v, want true, nil", ok, err)
	}
	if value != "testValue" {
		t.Errorf("state value = %q, want %q", value, "testValue")
	}
}

type factoryHTTPStub struct {
	responses    []any
	capturedURLs []string
}

func (h *factoryHTTPStub) RoundTrip(req *http.Request) (*http.Response, error) {
	h.capturedURLs = append(h.capturedURLs, req.URL.String())
	if req.Body != nil {
		defer func() { _ = req.Body.Close() }()
	}
	if len(h.responses) == 0 {
		return nil, fmt.Errorf("unexpected request to %s", req.URL)
	}
	response := h.responses[0]
	h.responses = h.responses[1:]

	var payload any
	switch response := response.(type) {
	case error:
		return nil, response
	case *a2a.AgentCard:
		payload = response
	case *a2a.Message:
		if req.Body == nil {
			return nil, fmt.Errorf("message request to %s has no body", req.URL)
		}
		var rpcRequest struct {
			JSONRPC string          `json:"jsonrpc"`
			ID      json.RawMessage `json:"id"`
		}
		if err := json.NewDecoder(req.Body).Decode(&rpcRequest); err != nil {
			return nil, fmt.Errorf("decode message request: %w", err)
		}
		result := a2a.StreamResponse{Event: response}
		payload = result
		if rpcRequest.JSONRPC == "2.0" {
			if len(rpcRequest.ID) == 0 {
				return nil, fmt.Errorf("JSON-RPC request has no ID")
			}
			payload = struct {
				JSONRPC string             `json:"jsonrpc"`
				ID      json.RawMessage    `json:"id"`
				Result  a2a.StreamResponse `json:"result"`
			}{JSONRPC: "2.0", ID: rpcRequest.ID, Result: result}
		}
	default:
		return nil, fmt.Errorf("unsupported queued response %T", response)
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("marshal queued response: %w", err)
	}
	return &http.Response{
		StatusCode: http.StatusOK,
		Status:     "200 OK",
		Header:     http.Header{"Content-Type": {"application/json"}},
		Body:       io.NopCloser(bytes.NewReader(body)),
		Request:    req,
	}, nil
}

func TestNewAgentFromCardReturnsAgent(t *testing.T) {
	card := &a2a.AgentCard{
		Name:        "Test Agent",
		Description: "A test agent for unit testing",
		SupportedInterfaces: []*a2a.AgentInterface{
			a2a.NewAgentInterface("http://test-endpoint/agent", a2a.TransportProtocolJSONRPC),
		},
	}

	a, err := a2aprovider.NewAgentFromCard(t.Context(), card, a2aprovider.AgentConfig{})
	if err != nil {
		t.Fatalf("NewAgentFromCard: %v", err)
	}
	if a == nil {
		t.Fatal("NewAgentFromCard returned nil")
	}
	if got := a.ProviderName(); got != "a2a" {
		t.Errorf("ProviderName = %q, want %q", got, "a2a")
	}
	if got := a.Name(); got != "Test Agent" {
		t.Errorf("Name = %q, want %q", got, "Test Agent")
	}
	if got := a.Description(); got != "A test agent for unit testing" {
		t.Errorf("Description = %q, want %q", got, "A test agent for unit testing")
	}
}

func TestNewAgentFromCardSendsRequestToCardURL(t *testing.T) {
	card := &a2a.AgentCard{
		Name:        "Test Agent",
		Description: "A test agent for unit testing",
		SupportedInterfaces: []*a2a.AgentInterface{
			a2a.NewAgentInterface("http://test-endpoint/agent", a2a.TransportProtocolJSONRPC),
		},
	}
	handler := &factoryHTTPStub{responses: []any{
		a2a.NewMessage(a2a.MessageRoleAgent, a2a.NewTextPart("Response")),
	}}
	httpClient := &http.Client{Transport: handler}
	a, err := a2aprovider.NewAgentFromCard(t.Context(), card, a2aprovider.AgentConfig{},
		a2aclient.WithJSONRPCTransport(httpClient),
	)
	if err != nil {
		t.Fatalf("NewAgentFromCard: %v", err)
	}

	if _, err := a.RunText(t.Context(), "Test input").Collect(); err != nil {
		t.Fatalf("RunText: %v", err)
	}
	if len(handler.capturedURLs) != 1 {
		t.Fatalf("request count = %d, want 1", len(handler.capturedURLs))
	}
	if got := handler.capturedURLs[0]; got != "http://test-endpoint/agent" {
		t.Errorf("request URL = %q, want %q", got, "http://test-endpoint/agent")
	}
}

func TestNewAgentFromCardWithPreferredTransportsUsesMatchingInterface(t *testing.T) {
	card := &a2a.AgentCard{
		Name:        "Multi-Interface Agent",
		Description: "An agent with multiple interfaces",
		SupportedInterfaces: []*a2a.AgentInterface{
			a2a.NewAgentInterface("http://first/agent", a2a.TransportProtocolHTTPJSON),
			a2a.NewAgentInterface("http://second/agent", a2a.TransportProtocolJSONRPC),
		},
	}
	handler := &factoryHTTPStub{responses: []any{
		a2a.NewMessage(a2a.MessageRoleAgent, a2a.NewTextPart("Response")),
	}}
	httpClient := &http.Client{Transport: handler}
	a, err := a2aprovider.NewAgentFromCard(t.Context(), card, a2aprovider.AgentConfig{},
		a2aclient.WithRESTTransport(httpClient),
		a2aclient.WithJSONRPCTransport(httpClient),
		a2aclient.WithConfig(a2aclient.Config{PreferredTransports: []a2a.TransportProtocol{a2a.TransportProtocolJSONRPC}}),
	)
	if err != nil {
		t.Fatalf("NewAgentFromCard: %v", err)
	}

	if _, err := a.RunText(t.Context(), "Test input").Collect(); err != nil {
		t.Fatalf("RunText: %v", err)
	}
	if len(handler.capturedURLs) != 1 {
		t.Fatalf("request count = %d, want 1", len(handler.capturedURLs))
	}
	if got := handler.capturedURLs[0]; got != "http://second/agent" {
		t.Errorf("request URL = %q, want %q", got, "http://second/agent")
	}
}

func TestNewAgentFromCardWithDefaultOptions(t *testing.T) {
	card := &a2a.AgentCard{
		Name:        "Default Options Agent",
		Description: "Tests default A2AClientOptions behavior",
		SupportedInterfaces: []*a2a.AgentInterface{
			a2a.NewAgentInterface("http://default/agent", a2a.TransportProtocolJSONRPC),
		},
	}

	a, err := a2aprovider.NewAgentFromCard(t.Context(), card, a2aprovider.AgentConfig{})
	if err != nil {
		t.Fatalf("NewAgentFromCard: %v", err)
	}
	if a == nil {
		t.Fatal("NewAgentFromCard returned nil")
	}
	if got := a.ProviderName(); got != "a2a" {
		t.Errorf("ProviderName = %q, want %q", got, "a2a")
	}
	if got := a.Name(); got != "Default Options Agent" {
		t.Errorf("Name = %q, want %q", got, "Default Options Agent")
	}
}

func TestNewAgentFromCardWithNoMatchingTransport(t *testing.T) {
	card := &a2a.AgentCard{
		Name:        "Unmatched Binding Agent",
		Description: "Agent with unsupported binding only",
		SupportedInterfaces: []*a2a.AgentInterface{
			a2a.NewAgentInterface("http://grpc/agent", a2a.TransportProtocolGRPC),
		},
	}

	if _, err := a2aprovider.NewAgentFromCard(t.Context(), card, a2aprovider.AgentConfig{},
		a2aclient.WithConfig(a2aclient.Config{PreferredTransports: []a2a.TransportProtocol{a2a.TransportProtocolJSONRPC}}),
	); err == nil {
		t.Fatal("NewAgentFromCard error = nil, want unsupported transport error")
	}
}

func TestNewAgentFromCardWithNoSupportedInterfaces(t *testing.T) {
	card := &a2a.AgentCard{
		Name:        "No Interfaces Agent",
		Description: "Agent with no supported interfaces",
	}

	if _, err := a2aprovider.NewAgentFromCard(t.Context(), card, a2aprovider.AgentConfig{}); err == nil {
		t.Fatal("NewAgentFromCard error = nil, want missing interfaces error")
	}
}

func TestNewAgentFromCardWithConfigOverridesCardValues(t *testing.T) {
	card := &a2a.AgentCard{
		Name:        "Card Agent",
		Description: "Card description",
		SupportedInterfaces: []*a2a.AgentInterface{
			a2a.NewAgentInterface("http://test-endpoint/agent", a2a.TransportProtocolJSONRPC),
		},
	}
	config := a2aprovider.AgentConfig{Config: agent.Config{
		ID:          "custom-id",
		Name:        "Custom Agent",
		Description: "Custom description",
	}}

	a, err := a2aprovider.NewAgentFromCard(t.Context(), card, config)
	if err != nil {
		t.Fatalf("NewAgentFromCard: %v", err)
	}
	if a == nil {
		t.Fatal("NewAgentFromCard returned nil")
	}
	if got := a.ProviderName(); got != "a2a" {
		t.Errorf("ProviderName = %q, want %q", got, "a2a")
	}
	if got := a.ID(); got != "custom-id" {
		t.Errorf("ID = %q, want %q", got, "custom-id")
	}
	if got := a.Name(); got != "Custom Agent" {
		t.Errorf("Name = %q, want %q", got, "Custom Agent")
	}
	if got := a.Description(); got != "Custom description" {
		t.Errorf("Description = %q, want %q", got, "Custom description")
	}
}

func TestNewAgentFromCardWithConfigFallsBackToCardValues(t *testing.T) {
	card := &a2a.AgentCard{
		Name:        "Card Agent",
		Description: "Card description",
		SupportedInterfaces: []*a2a.AgentInterface{
			a2a.NewAgentInterface("http://test-endpoint/agent", a2a.TransportProtocolJSONRPC),
		},
	}
	config := a2aprovider.AgentConfig{Config: agent.Config{ID: "custom-id"}}

	a, err := a2aprovider.NewAgentFromCard(t.Context(), card, config)
	if err != nil {
		t.Fatalf("NewAgentFromCard: %v", err)
	}
	if a == nil {
		t.Fatal("NewAgentFromCard returned nil")
	}
	if got := a.ID(); got != "custom-id" {
		t.Errorf("ID = %q, want %q", got, "custom-id")
	}
	if got := a.Name(); got != "Card Agent" {
		t.Errorf("Name = %q, want %q", got, "Card Agent")
	}
	if got := a.Description(); got != "Card description" {
		t.Errorf("Description = %q, want %q", got, "Card description")
	}
}

func TestNewAgentFromCardWithEmptyConfigUsesCardValues(t *testing.T) {
	card := &a2a.AgentCard{
		Name:        "Card Agent",
		Description: "Card description",
		SupportedInterfaces: []*a2a.AgentInterface{
			a2a.NewAgentInterface("http://test-endpoint/agent", a2a.TransportProtocolJSONRPC),
		},
	}

	a, err := a2aprovider.NewAgentFromCard(t.Context(), card, a2aprovider.AgentConfig{})
	if err != nil {
		t.Fatalf("NewAgentFromCard: %v", err)
	}
	if a == nil {
		t.Fatal("NewAgentFromCard returned nil")
	}
	if got := a.Name(); got != "Card Agent" {
		t.Errorf("Name = %q, want %q", got, "Card Agent")
	}
	if got := a.Description(); got != "Card description" {
		t.Errorf("Description = %q, want %q", got, "Card description")
	}
}

func TestNewAgentFromResolverWithValidCardReturnsAgent(t *testing.T) {
	handler := &factoryHTTPStub{responses: []any{
		&a2a.AgentCard{
			Name:        "Test Agent",
			Description: "A test agent for unit testing",
			SupportedInterfaces: []*a2a.AgentInterface{
				a2a.NewAgentInterface("http://test-endpoint/agent", a2a.TransportProtocolJSONRPC),
			},
		},
	}}
	resolver := agentcard.NewResolver(&http.Client{Transport: handler})

	a, err := a2aprovider.NewAgentFromResolver(t.Context(), resolver, "http://test-host", a2aprovider.AgentConfig{})
	if err != nil {
		t.Fatalf("NewAgentFromResolver: %v", err)
	}
	if a == nil {
		t.Fatal("NewAgentFromResolver returned nil")
	}
	if got := a.ProviderName(); got != "a2a" {
		t.Errorf("ProviderName = %q, want %q", got, "a2a")
	}
	if got := a.Name(); got != "Test Agent" {
		t.Errorf("Name = %q, want %q", got, "Test Agent")
	}
	if got := a.Description(); got != "A test agent for unit testing" {
		t.Errorf("Description = %q, want %q", got, "A test agent for unit testing")
	}
	if len(handler.capturedURLs) != 1 {
		t.Fatalf("request count = %d, want 1", len(handler.capturedURLs))
	}
	if got := handler.capturedURLs[0]; !strings.HasPrefix(got, "http://test-host/") {
		t.Errorf("card URL = %q, want prefix %q", got, "http://test-host/")
	}
}

func TestNewAgentFromResolverSendsRequestToCardURL(t *testing.T) {
	handler := &factoryHTTPStub{responses: []any{
		&a2a.AgentCard{
			SupportedInterfaces: []*a2a.AgentInterface{
				a2a.NewAgentInterface("http://test-endpoint/agent", a2a.TransportProtocolJSONRPC),
			},
		},
		a2a.NewMessage(a2a.MessageRoleAgent, a2a.NewTextPart("Response")),
	}}
	httpClient := &http.Client{Transport: handler}
	resolver := agentcard.NewResolver(httpClient)
	a, err := a2aprovider.NewAgentFromResolver(t.Context(), resolver, "http://test-host", a2aprovider.AgentConfig{},
		a2aclient.WithJSONRPCTransport(httpClient),
	)
	if err != nil {
		t.Fatalf("NewAgentFromResolver: %v", err)
	}

	if _, err := a.RunText(t.Context(), "Test input").Collect(); err != nil {
		t.Fatalf("RunText: %v", err)
	}
	if len(handler.capturedURLs) != 2 {
		t.Fatalf("request count = %d, want 2", len(handler.capturedURLs))
	}
	if got := handler.capturedURLs[1]; got != "http://test-endpoint/agent" {
		t.Errorf("request URL = %q, want %q", got, "http://test-endpoint/agent")
	}
}

func TestNewAgentFromResolverWithOptionsPassesOptionsToFactory(t *testing.T) {
	handler := &factoryHTTPStub{responses: []any{
		&a2a.AgentCard{
			Name:        "Options Agent",
			Description: "Agent with multiple interfaces",
			SupportedInterfaces: []*a2a.AgentInterface{
				a2a.NewAgentInterface("http://httpjson/agent", a2a.TransportProtocolHTTPJSON),
				a2a.NewAgentInterface("http://jsonrpc/agent", a2a.TransportProtocolJSONRPC),
			},
		},
		a2a.NewMessage(a2a.MessageRoleAgent, a2a.NewTextPart("Response")),
	}}
	httpClient := &http.Client{Transport: handler}
	resolver := agentcard.NewResolver(httpClient)
	a, err := a2aprovider.NewAgentFromResolver(t.Context(), resolver, "http://test-host", a2aprovider.AgentConfig{},
		a2aclient.WithRESTTransport(httpClient),
		a2aclient.WithJSONRPCTransport(httpClient),
		a2aclient.WithConfig(a2aclient.Config{PreferredTransports: []a2a.TransportProtocol{a2a.TransportProtocolJSONRPC}}),
	)
	if err != nil {
		t.Fatalf("NewAgentFromResolver: %v", err)
	}

	if _, err := a.RunText(t.Context(), "Test input").Collect(); err != nil {
		t.Fatalf("RunText: %v", err)
	}
	if len(handler.capturedURLs) != 2 {
		t.Fatalf("request count = %d, want 2", len(handler.capturedURLs))
	}
	if got := handler.capturedURLs[1]; got != "http://jsonrpc/agent" {
		t.Errorf("request URL = %q, want %q", got, "http://jsonrpc/agent")
	}
}

func TestNewAgentFromResolverWithConfigOverridesCardValues(t *testing.T) {
	handler := &factoryHTTPStub{responses: []any{
		&a2a.AgentCard{
			Name:        "Card Agent",
			Description: "Card description",
			SupportedInterfaces: []*a2a.AgentInterface{
				a2a.NewAgentInterface("http://test-endpoint/agent", a2a.TransportProtocolJSONRPC),
			},
		},
	}}
	httpClient := &http.Client{Transport: handler}
	resolver := agentcard.NewResolver(httpClient)
	config := a2aprovider.AgentConfig{Config: agent.Config{
		ID:          "custom-id",
		Name:        "Custom Agent",
		Description: "Custom description",
	}}

	a, err := a2aprovider.NewAgentFromResolver(t.Context(), resolver, "http://test-host", config,
		a2aclient.WithJSONRPCTransport(httpClient),
	)
	if err != nil {
		t.Fatalf("NewAgentFromResolver: %v", err)
	}
	if a == nil {
		t.Fatal("NewAgentFromResolver returned nil")
	}
	if got := a.ProviderName(); got != "a2a" {
		t.Errorf("ProviderName = %q, want %q", got, "a2a")
	}
	if got := a.ID(); got != "custom-id" {
		t.Errorf("ID = %q, want %q", got, "custom-id")
	}
	if got := a.Name(); got != "Custom Agent" {
		t.Errorf("Name = %q, want %q", got, "Custom Agent")
	}
	if got := a.Description(); got != "Custom description" {
		t.Errorf("Description = %q, want %q", got, "Custom description")
	}
}

func TestNewAgentFromResolverWithConfigFallsBackToCardValues(t *testing.T) {
	handler := &factoryHTTPStub{responses: []any{
		&a2a.AgentCard{
			Name:        "Card Agent",
			Description: "Card description",
			SupportedInterfaces: []*a2a.AgentInterface{
				a2a.NewAgentInterface("http://test-endpoint/agent", a2a.TransportProtocolJSONRPC),
			},
		},
	}}
	httpClient := &http.Client{Transport: handler}
	resolver := agentcard.NewResolver(httpClient)
	config := a2aprovider.AgentConfig{Config: agent.Config{ID: "custom-id"}}

	a, err := a2aprovider.NewAgentFromResolver(t.Context(), resolver, "http://test-host", config,
		a2aclient.WithJSONRPCTransport(httpClient),
	)
	if err != nil {
		t.Fatalf("NewAgentFromResolver: %v", err)
	}
	if a == nil {
		t.Fatal("NewAgentFromResolver returned nil")
	}
	if got := a.ID(); got != "custom-id" {
		t.Errorf("ID = %q, want %q", got, "custom-id")
	}
	if got := a.Name(); got != "Card Agent" {
		t.Errorf("Name = %q, want %q", got, "Card Agent")
	}
	if got := a.Description(); got != "Card description" {
		t.Errorf("Description = %q, want %q", got, "Card description")
	}
}

func TestNewAgentFromCardTransportPreferences(t *testing.T) {
	tests := []struct {
		name    string
		options []a2aclient.FactoryOption
		wantURL string
	}{
		{
			name:    "defaults prefer HTTP+JSON",
			wantURL: "http://httpjson/agent/message:send",
		},
		{
			name: "native preference overrides defaults",
			options: []a2aclient.FactoryOption{
				a2aclient.WithConfig(a2aclient.Config{PreferredTransports: []a2a.TransportProtocol{a2a.TransportProtocolJSONRPC}}),
			},
			wantURL: "http://jsonrpc/agent",
		},
		{
			name: "empty native config uses server ordering",
			options: []a2aclient.FactoryOption{
				a2aclient.WithConfig(a2aclient.Config{}),
			},
			wantURL: "http://jsonrpc/agent",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			card := &a2a.AgentCard{
				SupportedInterfaces: []*a2a.AgentInterface{
					a2a.NewAgentInterface("http://jsonrpc/agent", a2a.TransportProtocolJSONRPC),
					a2a.NewAgentInterface("http://httpjson/agent", a2a.TransportProtocolHTTPJSON),
				},
			}
			handler := &factoryHTTPStub{responses: []any{
				a2a.NewMessage(a2a.MessageRoleAgent, a2a.NewTextPart("Response")),
			}}
			httpClient := &http.Client{Transport: handler}
			options := append([]a2aclient.FactoryOption{
				a2aclient.WithRESTTransport(httpClient),
				a2aclient.WithJSONRPCTransport(httpClient),
			}, test.options...)
			a, err := a2aprovider.NewAgentFromCard(t.Context(), card, a2aprovider.AgentConfig{}, options...)
			if err != nil {
				t.Fatalf("NewAgentFromCard: %v", err)
			}

			if _, err := a.RunText(t.Context(), "Test input").Collect(); err != nil {
				t.Fatalf("RunText: %v", err)
			}
			if len(handler.capturedURLs) != 1 {
				t.Fatalf("request count = %d, want 1", len(handler.capturedURLs))
			}
			if got := handler.capturedURLs[0]; got != test.wantURL {
				t.Errorf("request URL = %q, want %q", got, test.wantURL)
			}
		})
	}
}

func TestNewAgentFromResolverUsesSeparateHTTPClients(t *testing.T) {
	cardHandler := &factoryHTTPStub{responses: []any{
		&a2a.AgentCard{
			SupportedInterfaces: []*a2a.AgentInterface{
				a2a.NewAgentInterface("http://test-endpoint/agent", a2a.TransportProtocolJSONRPC),
			},
		},
	}}
	runHandler := &factoryHTTPStub{responses: []any{
		a2a.NewMessage(a2a.MessageRoleAgent, a2a.NewTextPart("Response")),
	}}
	resolver := agentcard.NewResolver(&http.Client{Transport: cardHandler})
	a, err := a2aprovider.NewAgentFromResolver(t.Context(), resolver, "http://test-host", a2aprovider.AgentConfig{},
		a2aclient.WithJSONRPCTransport(&http.Client{Transport: runHandler}),
	)
	if err != nil {
		t.Fatalf("NewAgentFromResolver: %v", err)
	}

	if _, err := a.RunText(t.Context(), "Test input").Collect(); err != nil {
		t.Fatalf("RunText: %v", err)
	}
	if len(cardHandler.capturedURLs) != 1 {
		t.Fatalf("resolver request count = %d, want 1", len(cardHandler.capturedURLs))
	}
	if got := cardHandler.capturedURLs[0]; got != "http://test-host/.well-known/agent-card.json" {
		t.Errorf("card URL = %q, want %q", got, "http://test-host/.well-known/agent-card.json")
	}
	if len(runHandler.capturedURLs) != 1 {
		t.Fatalf("run request count = %d, want 1", len(runHandler.capturedURLs))
	}
	if got, want := runHandler.capturedURLs[0], "http://test-endpoint/agent"; got != want {
		t.Errorf("request URL = %q, want %q", got, want)
	}
}

func TestNewAgentFactoriesRejectNilInputs(t *testing.T) {
	if a, err := a2aprovider.NewAgentFromCard(t.Context(), nil, a2aprovider.AgentConfig{}); err == nil || a != nil {
		t.Fatalf("nil card: agent = %v, error = %v", a, err)
	}
	if a, err := a2aprovider.NewAgentFromResolver(t.Context(), nil, "http://test-host", a2aprovider.AgentConfig{}); err == nil || a != nil {
		t.Fatalf("nil resolver: agent = %v, error = %v", a, err)
	}
}

func TestNewAgentFromResolverPreservesErrors(t *testing.T) {
	for _, want := range []error{errors.New("card retrieval failed"), context.Canceled} {
		t.Run(want.Error(), func(t *testing.T) {
			handler := &factoryHTTPStub{responses: []any{want}}
			resolver := agentcard.NewResolver(&http.Client{Transport: handler})
			a, err := a2aprovider.NewAgentFromResolver(t.Context(), resolver, "http://test-host", a2aprovider.AgentConfig{})
			if a != nil || !errors.Is(err, want) {
				t.Fatalf("agent = %v, error = %v, want nil agent and %v", a, err, want)
			}
			if len(handler.capturedURLs) != 1 {
				t.Fatalf("requests = %v, want one discovery request", handler.capturedURLs)
			}
		})
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	handler := &factoryHTTPStub{responses: []any{context.Canceled}}
	resolver := agentcard.NewResolver(&http.Client{Transport: handler})
	if a, err := a2aprovider.NewAgentFromResolver(ctx, resolver, "http://test-host", a2aprovider.AgentConfig{}); a != nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled discovery: agent = %v, error = %v", a, err)
	}
}

func TestNewAgentFromResolverRejectsUnusableCard(t *testing.T) {
	handler := &factoryHTTPStub{responses: []any{&a2a.AgentCard{Name: "No Interfaces Agent"}}}
	resolver := agentcard.NewResolver(&http.Client{Transport: handler})
	if a, err := a2aprovider.NewAgentFromResolver(t.Context(), resolver, "http://test-host", a2aprovider.AgentConfig{}); a != nil || err == nil {
		t.Fatalf("agent = %v, error = %v, want creation failure", a, err)
	}
}

func TestNewAgentFromCardPreservesInputs(t *testing.T) {
	card := &a2a.AgentCard{
		Name: "Card Agent", Description: "Card description",
		SupportedInterfaces: []*a2a.AgentInterface{a2a.NewAgentInterface("http://test-endpoint/agent", a2a.TransportProtocolJSONRPC)},
	}
	config := a2aprovider.AgentConfig{Config: agent.Config{ID: "custom-id"}}
	a, err := a2aprovider.NewAgentFromCard(t.Context(), card, config)
	if err != nil {
		t.Fatal(err)
	}
	if config.Name != "" || config.Description != "" || card.Name != "Card Agent" || card.Description != "Card description" {
		t.Fatalf("factory changed its inputs: config = %+v, card = %+v", config, card)
	}
	card.Name = "Changed Agent"
	card.Description = "Changed description"
	if a.Name() != "Card Agent" || a.Description() != "Card description" || a.ID() != "custom-id" {
		t.Fatalf("agent metadata changed: ID = %q, name = %q, description = %q", a.ID(), a.Name(), a.Description())
	}
}

// Copyright (c) Microsoft. All rights reserved.

package a2aprovider_test

import (
	"context"
	"errors"
	"fmt"
	"iter"
	"math/big"
	"os"
	"os/exec"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/a2aproject/a2a-go/v2/a2aclient"
	"github.com/microsoft/agent-framework-go/agent"
	"github.com/microsoft/agent-framework-go/internal/agenttest"
	"github.com/microsoft/agent-framework-go/internal/telemetry"
	"github.com/microsoft/agent-framework-go/message"
	a2a1 "github.com/microsoft/agent-framework-go/provider/a2aprovider"
)

// mockA2ATransport is a stub that implements a2aclient.Transport for testing
type mockA2ATransport struct {
	capturedMessageSendParams  *a2a.SendMessageRequest
	capturedSubscribeToTaskReq *a2a.SubscribeToTaskRequest
	capturedGetTaskReq         *a2a.GetTaskRequest
	responseToReturn           a2a.SendMessageResult
	streamingResponseToReturn  a2a.Event
	streamingResponsesToReturn []a2a.Event
	emptyStreamingResponse     bool
	subscribeResponseToReturn  a2a.Event
	subscribeErrToReturn       error
	getTaskErrToReturn         error
	sendMessageCalled          bool
	sendStreamingMessageCalled bool
	subscribeToTaskCalled      bool
	getTaskCalled              bool
	sendMessageCallCount       int
	sendStreamingCallCount     int
	requestMethods             []string
	// rawStreamingResponse yields streamingResponseToReturn verbatim, without
	// backfilling an empty ContextID from the request. It lets tests exercise
	// bare streamed messages that carry no context ID.
	rawStreamingResponse bool
}

func (m *mockA2ATransport) SendMessage(ctx context.Context, _ a2aclient.ServiceParams, params *a2a.SendMessageRequest) (a2a.SendMessageResult, error) {
	m.requestMethods = append(m.requestMethods, "SendMessage")
	m.sendMessageCalled = true
	m.sendMessageCallCount++
	m.capturedMessageSendParams = params
	if m.responseToReturn != nil {
		return m.responseToReturn, nil
	}
	// Return default empty message with context ID from request
	return &a2a.Message{
		ID:        "default-response-id",
		Role:      a2a.MessageRoleAgent,
		ContextID: params.Message.ContextID,
	}, nil
}

func (m *mockA2ATransport) SendStreamingMessage(ctx context.Context, _ a2aclient.ServiceParams, params *a2a.SendMessageRequest) iter.Seq2[a2a.Event, error] {
	m.requestMethods = append(m.requestMethods, "SendStreamingMessage")
	m.sendStreamingMessageCalled = true
	m.sendStreamingCallCount++
	m.capturedMessageSendParams = params
	if m.emptyStreamingResponse {
		return func(func(a2a.Event, error) bool) {}
	}
	if len(m.streamingResponsesToReturn) > 0 {
		for _, response := range m.streamingResponsesToReturn {
			switch response := response.(type) {
			case *a2a.Message:
				if response.ContextID == "" {
					response.ContextID = params.Message.ContextID
				}
			case *a2a.Task:
				if response.ContextID == "" {
					response.ContextID = params.Message.ContextID
				}
			case *a2a.TaskStatusUpdateEvent:
				if response.ContextID == "" {
					response.ContextID = params.Message.ContextID
				}
			case *a2a.TaskArtifactUpdateEvent:
				if response.ContextID == "" {
					response.ContextID = params.Message.ContextID
				}
			}
		}
		return func(yield func(a2a.Event, error) bool) {
			for _, response := range m.streamingResponsesToReturn {
				if !yield(response, nil) {
					return
				}
			}
		}
	}
	responseToYield := m.streamingResponseToReturn
	if m.rawStreamingResponse {
		if responseToYield == nil {
			// Default to a non-nil event so a raw test that forgets to set
			// streamingResponseToReturn doesn't yield a nil event (which would
			// panic when production code calls TaskInfo()). Deliberately leave
			// ContextID empty — the "raw" mode exists to exercise bare messages
			// that carry no context ID.
			responseToYield = &a2a.Message{
				ID:   "default-stream-id",
				Role: a2a.MessageRoleAgent,
			}
		}
		return func(yield func(a2a.Event, error) bool) {
			yield(responseToYield, nil)
		}
	}
	if responseToYield == nil {
		// Return default empty message with context ID from request
		responseToYield = &a2a.Message{
			ID:        "default-stream-id",
			Role:      a2a.MessageRoleAgent,
			ContextID: params.Message.ContextID,
		}
	} else {
		// Set context ID based on response type
		switch resp := responseToYield.(type) {
		case *a2a.Message:
			if resp.ContextID == "" {
				resp.ContextID = params.Message.ContextID
			}
		case *a2a.Task:
			if resp.ContextID == "" {
				resp.ContextID = params.Message.ContextID
			}
		case *a2a.TaskStatusUpdateEvent:
			if resp.ContextID == "" {
				resp.ContextID = params.Message.ContextID
			}
		case *a2a.TaskArtifactUpdateEvent:
			if resp.ContextID == "" {
				resp.ContextID = params.Message.ContextID
			}
		}
	}
	return func(yield func(a2a.Event, error) bool) {
		yield(responseToYield, nil)
	}
}

func (m *mockA2ATransport) GetTask(ctx context.Context, _ a2aclient.ServiceParams, params *a2a.GetTaskRequest) (*a2a.Task, error) {
	m.requestMethods = append(m.requestMethods, "GetTask")
	m.getTaskCalled = true
	m.capturedGetTaskReq = params
	if m.getTaskErrToReturn != nil {
		return nil, m.getTaskErrToReturn
	}
	if m.responseToReturn != nil {
		if task, ok := m.responseToReturn.(*a2a.Task); ok {
			return task, nil
		}
	}
	return nil, errors.New("not implemented")
}

func (m *mockA2ATransport) ListTasks(ctx context.Context, _ a2aclient.ServiceParams, _ *a2a.ListTasksRequest) (*a2a.ListTasksResponse, error) {
	return nil, errors.New("not implemented")
}

func (m *mockA2ATransport) CancelTask(ctx context.Context, _ a2aclient.ServiceParams, _ *a2a.CancelTaskRequest) (*a2a.Task, error) {
	return nil, errors.New("not implemented")
}

func (m *mockA2ATransport) SubscribeToTask(ctx context.Context, _ a2aclient.ServiceParams, params *a2a.SubscribeToTaskRequest) iter.Seq2[a2a.Event, error] {
	m.requestMethods = append(m.requestMethods, "SubscribeToTask")
	m.subscribeToTaskCalled = true
	m.capturedSubscribeToTaskReq = params
	return func(yield func(a2a.Event, error) bool) {
		if m.subscribeErrToReturn != nil {
			yield(nil, m.subscribeErrToReturn)
			return
		}

		responseToYield := m.subscribeResponseToReturn
		if responseToYield == nil {
			responseToYield = &a2a.Message{
				ID:        "default-subscribe-id",
				Role:      a2a.MessageRoleAgent,
				TaskID:    params.ID,
				ContextID: "default-subscribe-context",
			}
		}

		switch resp := responseToYield.(type) {
		case *a2a.Message:
			if resp.TaskID == "" {
				resp.TaskID = params.ID
			}
		case *a2a.Task:
			if resp.ID == "" {
				resp.ID = params.ID
			}
		case *a2a.TaskStatusUpdateEvent:
			if resp.TaskID == "" {
				resp.TaskID = params.ID
			}
		case *a2a.TaskArtifactUpdateEvent:
			if resp.TaskID == "" {
				resp.TaskID = params.ID
			}
		}

		yield(responseToYield, nil)
	}
}

func (m *mockA2ATransport) GetTaskPushConfig(ctx context.Context, _ a2aclient.ServiceParams, _ *a2a.GetTaskPushConfigRequest) (*a2a.TaskPushConfig, error) {
	return nil, errors.New("not implemented")
}

func (m *mockA2ATransport) ListTaskPushConfigs(ctx context.Context, _ a2aclient.ServiceParams, _ *a2a.ListTaskPushConfigRequest) ([]*a2a.TaskPushConfig, error) {
	return nil, errors.New("not implemented")
}

func (m *mockA2ATransport) CreateTaskPushConfig(ctx context.Context, _ a2aclient.ServiceParams, _ *a2a.PushConfig) (*a2a.TaskPushConfig, error) {
	return nil, errors.New("not implemented")
}

func (m *mockA2ATransport) DeleteTaskPushConfig(ctx context.Context, _ a2aclient.ServiceParams, _ *a2a.DeleteTaskPushConfigRequest) error {
	return errors.New("not implemented")
}

func (m *mockA2ATransport) GetExtendedAgentCard(ctx context.Context, _ a2aclient.ServiceParams, _ *a2a.GetExtendedAgentCardRequest) (*a2a.AgentCard, error) {
	return nil, errors.New("not implemented")
}

func (m *mockA2ATransport) Destroy() error {
	return nil
}

// Test fixtures
func newTestAgent(transport a2aclient.Transport, config agent.Config) *agent.Agent {
	card := &a2a.AgentCard{
		Capabilities: a2a.AgentCapabilities{
			Streaming: true,
		},
		SupportedInterfaces: []*a2a.AgentInterface{
			a2a.NewAgentInterface("test://localhost", a2a.TransportProtocol("test")),
		},
	}
	client, err := a2aclient.NewFromCard(
		context.Background(),
		card,
		a2aclient.WithDefaultsDisabled(),
		a2aclient.WithTransport("test", a2aclient.TransportFactoryFn(func(ctx context.Context, card *a2a.AgentCard, iface *a2a.AgentInterface) (a2aclient.Transport, error) {
			return transport, nil
		})),
	)
	if err != nil {
		panic(err)
	}
	return a2a1.NewAgent(client, a2a1.AgentConfig{Config: config})
}

func latestTaskID(session *agent.Session) string {
	return a2a1.TaskIDFromSession(session)
}

func TestFeatureUsageRunActivatesA2A(t *testing.T) {
	if runFeatureUsageSubprocess(t) {
		return
	}
	a := newTestAgent(&mockA2ATransport{
		responseToReturn:       &a2a.Message{ID: "response", Role: a2a.MessageRoleAgent},
		emptyStreamingResponse: true,
	}, agent.Config{})

	if _, err := a.RunText(t.Context(), "hello").Collect(); err != nil {
		t.Fatal(err)
	}

	assertA2AFeatureMarked(t)
}

func TestFeatureUsageStreamingIsColdAndActivatesA2A(t *testing.T) {
	if runFeatureUsageSubprocess(t) {
		return
	}
	a := newTestAgent(&mockA2ATransport{
		responseToReturn:       &a2a.Message{ID: "response", Role: a2a.MessageRoleAgent},
		emptyStreamingResponse: true,
	}, agent.Config{})

	stream := a.RunText(t.Context(), "hello", agent.Stream(true))

	if got := telemetry.ApplyToUserAgent("", true); got != "" {
		t.Fatalf("unconsumed stream marked features: %s", got)
	}
	for update, err := range stream {
		if err != nil {
			t.Fatal(err)
		}
		t.Fatalf("empty stream yielded an update: %#v", update)
	}
	assertA2AFeatureMarked(t)
}

func runFeatureUsageSubprocess(t *testing.T) bool {
	t.Helper()
	const helperEnv = "AGENT_FRAMEWORK_A2A_FEATURE_USAGE_TEST"
	if os.Getenv(helperEnv) == t.Name() {
		return false
	}
	// The accumulator is process-global and must not inherit other tests' bits.
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(exe, "-test.run=^"+regexp.QuoteMeta(t.Name())+"$")
	cmd.Env = append(os.Environ(),
		helperEnv+"="+t.Name(),
		"AGENT_FRAMEWORK_FEATURE_MASK_DISABLED=",
		"AGENT_FRAMEWORK_USER_AGENT_DISABLED=",
	)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("isolated feature-usage test failed: %v\n%s", err, output)
	}
	return true
}

func assertA2AFeatureMarked(t *testing.T) {
	t.Helper()
	userAgent := telemetry.ApplyToUserAgent("", true)
	const prefix = "(feat=v1."
	if !strings.HasPrefix(userAgent, prefix) || !strings.HasSuffix(userAgent, ")") {
		t.Fatalf("feature comment = %q, want version-1 mask", userAgent)
	}
	mask, ok := new(big.Int).SetString(userAgent[len(prefix):len(userAgent)-1], 16)
	if !ok {
		t.Fatalf("feature comment has invalid mask: %q", userAgent)
	}
	if mask.Bit(62) != 1 {
		t.Fatalf("A2A feature bit 62 is not set: %q", userAgent)
	}
}

// TestConstructorWithNilClient tests that nil client is handled
func TestConstructorWithNilClient(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Error("Expected panic when client is nil")
		}
	}()
	a2a1.NewAgent(nil, a2a1.AgentConfig{})
}

func TestCreateSessionValidatesExistingA2AIDs(t *testing.T) {
	a := newTestAgent(&mockA2ATransport{}, agent.Config{})
	tests := []struct {
		name    string
		options []agent.Option
	}{
		{name: "blank context ID", options: []agent.Option{agent.WithServiceID(" ")}},
		{name: "task ID without context ID", options: []agent.Option{a2a1.WithTaskID("task-123")}},
		{name: "blank task ID", options: []agent.Option{agent.WithServiceID("context-123"), a2a1.WithTaskID(" ")}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := a.CreateSession(t.Context(), test.options...); err == nil {
				t.Fatal("CreateSession error = nil")
			}
		})
	}
}

// TestRunAllowsNonUserRoleMessages tests that non-user role messages are accepted
func TestRunAllowsNonUserRoleMessages(t *testing.T) {
	transport := &mockA2ATransport{}
	a := newTestAgent(transport, agent.Config{})

	inputMessages := []*message.Message{
		{Role: message.RoleSystem, Contents: []message.Content{&message.TextContent{Text: "I am a system message"}}},
		{Role: message.RoleAssistant, Contents: []message.Content{&message.TextContent{Text: "I am an assistant message"}}},
		{Role: message.RoleUser, Contents: []message.Content{&message.TextContent{Text: "Valid user message"}}},
	}

	_, err := a.Run(t.Context(), inputMessages).Collect()
	if err != nil {
		t.Fatalf("error = %v, want nil", err)
	}
	if transport.capturedMessageSendParams == nil || transport.capturedMessageSendParams.Message == nil {
		t.Fatal("no message was sent")
	}
	sent := transport.capturedMessageSendParams.Message
	if sent.Role != a2a.MessageRoleUser {
		t.Errorf("sent role = %q, want %q", sent.Role, a2a.MessageRoleUser)
	}
	if len(sent.Parts) != len(inputMessages) {
		t.Fatalf("sent parts = %d, want %d", len(sent.Parts), len(inputMessages))
	}
	for i, input := range inputMessages {
		if got := sent.Parts[i].Text(); got != input.String() {
			t.Errorf("sent part %d = %q, want %q", i, got, input.String())
		}
	}
}

// An ErrorContent must be sent to A2A as its human-readable text, not as an
// opaque JSON blob (the default-branch behavior), matching the Python client.
func TestRunSendsErrorContentAsText(t *testing.T) {
	transport := &mockA2ATransport{
		responseToReturn: &a2a.Message{
			ID:    "response-err",
			Role:  a2a.MessageRoleAgent,
			Parts: a2a.ContentParts{a2a.NewTextPart("ok")},
		},
	}
	a := newTestAgent(transport, agent.Config{})

	msgs := []*message.Message{{Role: message.RoleUser, Contents: message.Contents{&message.ErrorContent{Message: "boom"}}}}
	if _, err := a.Run(t.Context(), msgs).Collect(); err != nil {
		t.Fatalf("error = %v, want nil", err)
	}

	if transport.capturedMessageSendParams == nil || transport.capturedMessageSendParams.Message == nil {
		t.Fatal("captured message is nil")
	}
	parts := transport.capturedMessageSendParams.Message.Parts
	if len(parts) != 1 {
		t.Fatalf("parts count = %d, want 1", len(parts))
	}
	if got := parts[0].Text(); got != "boom" {
		t.Errorf("error part text = %q, want %q", got, "boom")
	}
}

// TestRunWithValidUserMessage tests successful run with valid user message.
func TestRunWithValidUserMessage(t *testing.T) {
	transport := &mockA2ATransport{
		responseToReturn: &a2a.Message{
			ID:    "response-123",
			Role:  a2a.MessageRoleAgent,
			Parts: a2a.ContentParts{a2a.NewTextPart("Hello! How can I help you today?")},
		},
	}
	a := newTestAgent(transport, agent.Config{})

	result, err := a.RunText(t.Context(), "Hello, world!").Collect()
	if err != nil {
		t.Fatalf("error = %v, want nil", err)
	}

	// Assert input message sent to A2AClient
	if transport.capturedMessageSendParams == nil {
		t.Fatal("capturedMessageSendParams is nil")
	}
	inputMessage := transport.capturedMessageSendParams.Message
	if inputMessage == nil {
		t.Fatal("captured message is nil")
	}
	if len(inputMessage.Parts) != 1 {
		t.Errorf("captured message parts count = %d, want 1", len(inputMessage.Parts))
	}
	if inputMessage.Role != a2a.MessageRoleUser {
		t.Errorf("captured message role = %q, want %q", inputMessage.Role, a2a.MessageRoleUser)
	}
	if got := inputMessage.Parts[0].Text(); got != "Hello, world!" {
		t.Errorf("captured message text = %q, want %q", got, "Hello, world!")
	}

	// Assert response from A2AClient is converted correctly
	if result == nil {
		t.Fatal("result is nil")
	}
	if result.AgentID != a.ID() {
		t.Errorf("result.AgentID = %q, want %q", result.AgentID, a.ID())
	}
	if result.ID != "response-123" {
		t.Errorf("result.ID = %q, want %q", result.ID, "response-123")
	}
	if rawMsg, ok := result.RawRepresentation.(*a2a.Message); !ok || rawMsg == nil {
		t.Errorf("result.RawRepresentation = %#v, want non-nil *a2a.Message", result.RawRepresentation)
	} else if rawMsg.ID != "response-123" {
		t.Errorf("raw response message ID = %q, want %q", rawMsg.ID, "response-123")
	}
	if len(result.Messages) != 1 {
		t.Fatalf("len(result.Messages) = %d, want 1", len(result.Messages))
	}
	msg := result.Messages[0]
	if msg.ID != "response-123" {
		t.Errorf("ID = %q, want %q", msg.ID, "response-123")
	}

	if _, ok := msg.RawRepresentation.(*a2a.Message); !ok {
		t.Errorf("RawRepresentation type = %T, want *a2a.Message", msg.RawRepresentation)
	}
	if rawMsg, ok := msg.RawRepresentation.(*a2a.Message); ok {
		if rawMsg.ID != "response-123" {
			t.Errorf("raw message ID = %q, want %q", rawMsg.ID, "response-123")
		}
	}
	if msg.Role != message.RoleAssistant {
		t.Errorf("Role = %q, want %q", msg.Role, message.RoleAssistant)
	}
	if msg.String() != "Hello! How can I help you today?" {
		t.Errorf("String() = %q, want %q", msg.String(), "Hello! How can I help you today?")
	}
	if result.FinishReason != "stop" {
		t.Errorf("FinishReason = %q, want stop", result.FinishReason)
	}
	if !result.CreatedAt.IsZero() || !msg.CreatedAt.IsZero() {
		t.Fatalf("A2A response synthesized a creation timestamp: response=%v message=%v", result.CreatedAt, msg.CreatedAt)
	}
}

func TestRunForwardsRequestMetadata(t *testing.T) {
	transport := &mockA2ATransport{}
	a := newTestAgent(transport, agent.Config{})
	metadata := map[string]any{
		"tenant": "contoso",
		"key1":   "value1",
		"key2":   42,
		"key3":   true,
	}
	option := a2a1.WithMetadata(metadata)
	metadata["tenant"] = "mutated"

	if _, err := a.RunText(t.Context(), "hello", option).Collect(); err != nil {
		t.Fatal(err)
	}
	if transport.capturedMessageSendParams == nil {
		t.Fatal("no SendMessage request was captured")
	}
	if got := transport.capturedMessageSendParams.Metadata["tenant"]; got != "contoso" {
		t.Fatalf("request metadata tenant = %#v, want contoso", got)
	}
	if got := transport.capturedMessageSendParams.Metadata; got["key1"] != "value1" || got["key2"] != 42 || got["key3"] != true {
		t.Fatalf("request metadata = %#v, want string value1, integer 42 and Boolean true", got)
	}
}

func TestRunIgnoresInstructions(t *testing.T) {
	transport := &mockA2ATransport{}
	a := newTestAgent(transport, agent.Config{})

	_, err := a.RunText(t.Context(), "Hello, world!", agent.WithInstructions("Be concise.")).Collect()
	if err != nil {
		t.Fatalf("error = %v, want nil", err)
	}
	if !transport.sendMessageCalled {
		t.Fatal("SendMessage was not called")
	}
	inputMessage := transport.capturedMessageSendParams.Message
	if inputMessage == nil {
		t.Fatal("captured message is nil")
	}
	if len(inputMessage.Parts) != 1 {
		t.Fatalf("captured message parts count = %d, want 1", len(inputMessage.Parts))
	}
	if got := inputMessage.Parts[0].Text(); got != "Hello, world!" {
		t.Errorf("captured message text = %q, want %q", got, "Hello, world!")
	}
}

// TestRunWithCreateSession tests that new session updates context ID
func TestRunWithCreateSession(t *testing.T) {
	transport := &mockA2ATransport{
		responseToReturn: &a2a.Message{
			ID:        "response-123",
			Role:      a2a.MessageRoleAgent,
			Parts:     a2a.ContentParts{a2a.NewTextPart("Response")},
			ContextID: "new-context-id",
		},
	}
	a := newTestAgent(transport, agent.Config{})

	session, err := a.CreateSession(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	_, err = a.RunText(t.Context(), "Test message", agent.WithSession(session)).Collect()
	if err != nil {
		t.Fatalf("error = %v, want nil", err)
	}

	if got := session.ServiceID(); got != "new-context-id" {
		t.Errorf("session.ServiceID = %q, want %q", got, "new-context-id")
	}
}

// TestRunWithExistingSession tests that existing session context ID is used
func TestRunWithExistingSession(t *testing.T) {
	for _, test := range []struct {
		name                string
		omitResponseContext bool
	}{
		{name: "echoed response context"},
		{name: "omitted response context", omitResponseContext: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			transport := &mockA2ATransport{}
			if test.omitResponseContext {
				transport.responseToReturn = &a2a.Message{}
			}
			a := newTestAgent(transport, agent.Config{})

			session, err := a.CreateSession(t.Context(), agent.WithServiceID("existing-context-id"))
			if err != nil {
				t.Fatal(err)
			}

			result, err := a.RunText(t.Context(), "Test message", agent.WithSession(session)).Collect()
			if err != nil {
				t.Fatalf("error = %v, want nil", err)
			}
			if result == nil {
				t.Fatal("result is nil")
			}
			if got := result.ConversationID; got == nil || *got != "existing-context-id" {
				t.Errorf("response conversation ID = %v, want existing-context-id", got)
			}
			if got := session.ServiceID(); got != "existing-context-id" {
				t.Errorf("session.ServiceID = %q, want existing-context-id", got)
			}
			if transport.capturedMessageSendParams == nil || transport.capturedMessageSendParams.Message == nil {
				t.Fatal("no message was sent")
			}
			if got := transport.capturedMessageSendParams.Message.ContextID; got != "existing-context-id" {
				t.Errorf("message.ContextID = %q, want existing-context-id", got)
			}
		})
	}
}

// TestRunAllowBackgroundResponsesSetsReturnImmediately verifies that the
// AllowBackgroundResponses option propagates to the non-streaming send config's
// ReturnImmediately field, mirroring .NET A2AAgent.RunCoreAsync.
func TestRunAllowBackgroundResponsesSetsReturnImmediately(t *testing.T) {
	newSession := func(a *agent.Agent) *agent.Session {
		t.Helper()
		session, err := a.CreateSession(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		return session
	}

	t.Run("enabled", func(t *testing.T) {
		transport := &mockA2ATransport{}
		a := newTestAgent(transport, agent.Config{})

		_, err := a.RunText(t.Context(), "Test message",
			agent.WithSession(newSession(a)),
			agent.AllowBackgroundResponses(true),
		).Collect()
		if err != nil {
			t.Fatalf("error = %v, want nil", err)
		}
		if transport.capturedMessageSendParams == nil {
			t.Fatal("capturedMessageSendParams is nil")
		}
		if transport.capturedMessageSendParams.Config == nil {
			t.Fatal("capturedMessageSendParams.Config is nil, want non-nil")
		}
		if !transport.capturedMessageSendParams.Config.ReturnImmediately {
			t.Error("Config.ReturnImmediately = false, want true")
		}
	})

	t.Run("disabled", func(t *testing.T) {
		transport := &mockA2ATransport{}
		a := newTestAgent(transport, agent.Config{})

		_, err := a.RunText(t.Context(), "Test message",
			agent.WithSession(newSession(a)),
		).Collect()
		if err != nil {
			t.Fatalf("error = %v, want nil", err)
		}
		if transport.capturedMessageSendParams == nil {
			t.Fatal("capturedMessageSendParams is nil")
		}
		if cfg := transport.capturedMessageSendParams.Config; cfg == nil || cfg.ReturnImmediately {
			t.Errorf("Config = %+v, want blocking configuration", cfg)
		}
	})
}

// TestRunWithSessionHavingDifferentContextID tests error when context ID mismatch
func TestRunWithSessionHavingDifferentContextID(t *testing.T) {
	transport := &mockA2ATransport{
		responseToReturn: &a2a.Message{
			ID:        "response-123",
			Role:      a2a.MessageRoleAgent,
			Parts:     a2a.ContentParts{a2a.NewTextPart("Response")},
			ContextID: "different-context",
		},
	}
	a := newTestAgent(transport, agent.Config{})

	session, err := a.CreateSession(t.Context(), agent.WithServiceID("existing-context-id"))
	if err != nil {
		t.Fatal(err)
	}

	result, err := a.RunText(t.Context(), "Test message", agent.WithSession(session)).Collect()
	const wantError = `mismatched context ID: expected "existing-context-id" but A2A response has "different-context"`
	if err == nil || err.Error() != wantError {
		t.Fatalf("error = %v, want %q", err, wantError)
	}
	if result != nil {
		t.Fatalf("result = %#v, want nil on context mismatch", result)
	}
	if got := session.ServiceID(); got != "existing-context-id" {
		t.Errorf("session.ServiceID = %q, want unchanged context", got)
	}
	if got := latestTaskID(session); got != "" {
		t.Errorf("session task ID = %q, want unchanged empty task ID", got)
	}
}

// TestRunStreamingWithValidUserMessage tests streaming with valid user message
func TestRunStreamingWithValidUserMessage(t *testing.T) {
	transport := &mockA2ATransport{
		streamingResponseToReturn: &a2a.Message{
			ID:        "stream-1",
			Role:      a2a.MessageRoleAgent,
			Parts:     a2a.ContentParts{a2a.NewTextPart("Hello")},
			ContextID: "stream-context",
		},
	}
	a := newTestAgent(transport, agent.Config{})

	var updates []*agent.ResponseUpdate
	for update, err := range a.RunText(t.Context(), "Hello, streaming!", agent.Stream(true)) {
		if err != nil {
			t.Fatalf("error = %v, want nil", err)
		}
		updates = append(updates, update)
	}

	if len(updates) != 1 {
		t.Fatalf("len(updates) = %d, want 1", len(updates))
	}

	// Debug: Check which transport method was called
	if !transport.sendStreamingMessageCalled {
		t.Fatalf("SendStreamingMessage was not called. SendMessage called: %v", transport.sendMessageCalled)
	}

	// Assert input message sent to A2AClient
	if transport.capturedMessageSendParams == nil {
		t.Fatal("capturedMessageSendParams is nil")
	}
	inputMessage := transport.capturedMessageSendParams.Message
	if inputMessage == nil {
		t.Fatal("captured message is nil")
	}
	if len(inputMessage.Parts) != 1 {
		t.Errorf("captured message parts count = %d, want 1", len(inputMessage.Parts))
	}
	if inputMessage.Role != a2a.MessageRoleUser {
		t.Errorf("captured message role = %q, want %q", inputMessage.Role, a2a.MessageRoleUser)
	}
	if got := inputMessage.Parts[0].Text(); got != "Hello, streaming!" {
		t.Errorf("captured message text = %q, want %q", got, "Hello, streaming!")
	}

	// Assert response from A2AClient is converted correctly
	update := updates[0]
	if update.Role != message.RoleAssistant {
		t.Errorf("update.Role = %q, want %q", update.Role, message.RoleAssistant)
	}
	if update.String() != "Hello" {
		t.Errorf("update.String() = %q, want %q", update.String(), "Hello")
	}
	if update.MessageID != "stream-1" {
		t.Errorf("update.MessageID = %q, want %q", update.MessageID, "stream-1")
	}
	if update.ResponseID != "stream-1" {
		t.Errorf("update.ResponseID = %q, want %q", update.ResponseID, "stream-1")
	}
	if update.AgentID != a.ID() {
		t.Errorf("update.AgentID = %q, want %q", update.AgentID, a.ID())
	}
	if update.FinishReason != "stop" {
		t.Errorf("update.FinishReason = %q, want stop", update.FinishReason)
	}

	if update.RawRepresentation == nil {
		t.Fatal("update.RawRepresentation is nil")
	}
	if _, ok := update.RawRepresentation.(*a2a.Message); !ok {
		t.Errorf("update.RawRepresentation type = %T, want *a2a.Message", update.RawRepresentation)
	}
	if rawMsg, ok := update.RawRepresentation.(*a2a.Message); ok {
		if rawMsg.ID != "stream-1" {
			t.Errorf("raw message ID = %q, want %q", rawMsg.ID, "stream-1")
		}
	}
}

// TestRunStreamingWithSession tests streaming with session context ID update
func TestRunStreamingWithSession(t *testing.T) {
	transport := &mockA2ATransport{
		streamingResponseToReturn: &a2a.Message{
			ID:        "stream-1",
			Role:      a2a.MessageRoleAgent,
			Parts:     a2a.ContentParts{a2a.NewTextPart("Response")},
			ContextID: "new-stream-context",
		},
	}
	a := newTestAgent(transport, agent.Config{})

	session, err := a.CreateSession(t.Context())
	if err != nil {
		t.Fatal(err)
	}

	for _, err := range a.RunText(t.Context(), "Test streaming", agent.WithSession(session), agent.Stream(true)) {
		if err != nil {
			t.Fatalf("error = %v, want nil", err)
		}
	}

	if got := session.ServiceID(); got != "new-stream-context" {
		t.Errorf("session.ContextID = %q, want %q", got, "new-stream-context")
	}
}

// TestRunStreamingWithExistingSession tests streaming with existing session
func TestRunStreamingWithExistingSession(t *testing.T) {
	for _, test := range []struct {
		name                string
		omitResponseContext bool
	}{
		{name: "echoed response context"},
		{name: "omitted response context", omitResponseContext: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			transport := &mockA2ATransport{
				streamingResponseToReturn: &a2a.Message{},
				rawStreamingResponse:      test.omitResponseContext,
			}
			a := newTestAgent(transport, agent.Config{})

			session, err := a.CreateSession(t.Context(), agent.WithServiceID("existing-context-id"))
			if err != nil {
				t.Fatal(err)
			}
			var updates []*agent.ResponseUpdate
			for update, err := range a.RunText(t.Context(), "Test streaming", agent.WithSession(session), agent.Stream(true)) {
				if err != nil {
					t.Fatalf("error = %v, want nil", err)
				}
				updates = append(updates, update)
			}
			if len(updates) != 1 || updates[0] == nil {
				t.Fatalf("updates = %#v, want one response update", updates)
			}
			if got := updates[0].ConversationID; got == nil || *got != "existing-context-id" {
				t.Errorf("update conversation ID = %v, want existing-context-id", got)
			}
			if got := session.ServiceID(); got != "existing-context-id" {
				t.Errorf("session.ServiceID = %q, want existing-context-id", got)
			}
			if transport.capturedMessageSendParams == nil || transport.capturedMessageSendParams.Message == nil {
				t.Fatal("no message was sent")
			}
			if got := transport.capturedMessageSendParams.Message.ContextID; got != "existing-context-id" {
				t.Errorf("message.ContextID = %q, want existing-context-id", got)
			}
		})
	}
}

// TestRunStreamingWithSessionHavingDifferentContextID tests error on context ID mismatch in streaming
func TestRunStreamingWithSessionHavingDifferentContextID(t *testing.T) {
	transport := &mockA2ATransport{
		streamingResponseToReturn: &a2a.Message{
			ID:        "stream-1",
			Role:      a2a.MessageRoleAgent,
			Parts:     a2a.ContentParts{a2a.NewTextPart("Response")},
			ContextID: "different-context",
		},
	}
	a := newTestAgent(transport, agent.Config{})

	session, err := a.CreateSession(t.Context(), agent.WithServiceID("existing-context-id"))
	if err != nil {
		t.Fatal(err)
	}

	var gotErr error
	var yields int
	for update, err := range a.RunText(t.Context(), "Test streaming", agent.WithSession(session), agent.Stream(true)) {
		yields++
		if update != nil {
			t.Errorf("update = %#v, want no update from a different context", update)
		}
		gotErr = err
	}
	const wantError = `mismatched context ID: expected "existing-context-id" but A2A response has "different-context"`
	if gotErr == nil || gotErr.Error() != wantError {
		t.Fatalf("error = %v, want %q", gotErr, wantError)
	}
	if yields != 1 {
		t.Errorf("stream yields = %d, want one terminal error", yields)
	}
	if got := session.ServiceID(); got != "existing-context-id" {
		t.Errorf("session.ServiceID = %q, want unchanged context", got)
	}
	if got := latestTaskID(session); got != "" {
		t.Errorf("session task ID = %q, want unchanged empty task ID", got)
	}
}

// TestRunStreamingWithEmptyContextIDKeepsSessionContext verifies that a bare
// streamed message carrying an empty context ID neither errors the run nor
// clobbers the context ID already stored in the session. This mirrors the .NET
// behavior where ContextId is only assigned when currently unset
// (ContextId ??= contextId).
func TestRunStreamingWithEmptyContextIDKeepsSessionContext(t *testing.T) {
	transport := &mockA2ATransport{
		rawStreamingResponse: true,
		streamingResponseToReturn: &a2a.Message{
			ID:    "stream-1",
			Role:  a2a.MessageRoleAgent,
			Parts: a2a.ContentParts{a2a.NewTextPart("Response")},
			// No ContextID: a bare streamed message chunk.
		},
	}
	a := newTestAgent(transport, agent.Config{})

	session, err := a.CreateSession(t.Context(), agent.WithServiceID("ctx-1"))
	if err != nil {
		t.Fatal(err)
	}

	for update, err := range a.RunText(t.Context(), "Test streaming", agent.WithSession(session), agent.Stream(true)) {
		if err != nil {
			t.Fatalf("error = %v, want nil", err)
		}
		if update.ConversationID == nil || *update.ConversationID != "ctx-1" {
			t.Errorf("conversation ID = %v, want ctx-1", update.ConversationID)
		}
	}

	if got := session.ServiceID(); got != "ctx-1" {
		t.Errorf("session.ServiceID = %q, want %q", got, "ctx-1")
	}
}

// TestRunStreamingWithEmptyInitialContextStoresResponseContext verifies that when
// the session has no context ID yet, the first streamed event's context ID is stored.
func TestRunStreamingWithEmptyInitialContextStoresResponseContext(t *testing.T) {
	transport := &mockA2ATransport{
		rawStreamingResponse: true,
		streamingResponseToReturn: &a2a.Message{
			ID:        "stream-1",
			Role:      a2a.MessageRoleAgent,
			Parts:     a2a.ContentParts{a2a.NewTextPart("Response")},
			ContextID: "ctx-1",
		},
	}
	a := newTestAgent(transport, agent.Config{})

	session, err := a.CreateSession(t.Context())
	if err != nil {
		t.Fatal(err)
	}

	for update, err := range a.RunText(t.Context(), "Test streaming", agent.WithSession(session), agent.Stream(true)) {
		if err != nil {
			t.Fatalf("error = %v, want nil", err)
		}
		if update.ConversationID == nil || *update.ConversationID != "ctx-1" {
			t.Errorf("conversation ID = %v, want ctx-1", update.ConversationID)
		}
	}

	if got := session.ServiceID(); got != "ctx-1" {
		t.Errorf("session.ServiceID = %q, want %q", got, "ctx-1")
	}
}

// TestRunStreamingAllowsNonUserRoleMessages tests that streaming allows non-user messages
func TestRunStreamingAllowsNonUserRoleMessages(t *testing.T) {
	transport := &mockA2ATransport{
		streamingResponseToReturn: &a2a.Message{
			ID:        "stream-1",
			Role:      a2a.MessageRoleAgent,
			Parts:     a2a.ContentParts{a2a.NewTextPart("Response")},
			ContextID: "new-stream-context",
		},
	}
	a := newTestAgent(transport, agent.Config{})

	inputMessages := []*message.Message{
		{Role: message.RoleSystem, Contents: []message.Content{&message.TextContent{Text: "I am a system message"}}},
		{Role: message.RoleAssistant, Contents: []message.Content{&message.TextContent{Text: "I am an assistant message"}}},
		{Role: message.RoleUser, Contents: []message.Content{&message.TextContent{Text: "Valid user message"}}},
	}

	for _, err := range a.Run(t.Context(), inputMessages, agent.Stream(true)) {
		if err != nil {
			t.Fatalf("error = %v, want nil", err)
		}
	}
	if transport.capturedMessageSendParams == nil || transport.capturedMessageSendParams.Message == nil {
		t.Fatal("no message was sent")
	}
	sent := transport.capturedMessageSendParams.Message
	if sent.Role != a2a.MessageRoleUser {
		t.Errorf("sent role = %q, want %q", sent.Role, a2a.MessageRoleUser)
	}
	if len(sent.Parts) != len(inputMessages) {
		t.Fatalf("sent parts = %d, want %d", len(sent.Parts), len(inputMessages))
	}
	for i, input := range inputMessages {
		if got := sent.Parts[i].Text(); got != input.String() {
			t.Errorf("sent part %d = %q, want %q", i, got, input.String())
		}
	}
}

// TestRunWithHostedFileContent tests conversion of hosted file content to file part
func TestRunWithHostedFileContent(t *testing.T) {
	transport := &mockA2ATransport{}
	a := newTestAgent(transport, agent.Config{})

	inputMessages := []*message.Message{
		{
			Role: message.RoleUser,
			Contents: []message.Content{
				&message.TextContent{Text: "Check this file:"},
				&message.URIContent{
					URI:       "https://example.com/file.pdf",
					MediaType: "application/pdf",
				},
			},
		},
	}

	_, err := a.Run(t.Context(), inputMessages).Collect()
	if err != nil {
		t.Fatalf("error = %v, want nil", err)
	}

	capturedMsg := transport.capturedMessageSendParams.Message
	if capturedMsg == nil {
		t.Fatal("capturedMessageSendParams.Message is nil")
	}
	if len(capturedMsg.Parts) != 2 {
		t.Fatalf("len(message.Parts) = %d, want 2", len(capturedMsg.Parts))
	}

	if got := capturedMsg.Parts[0].Text(); got != "Check this file:" {
		t.Errorf("Parts[0].Text() = %q, want %q", got, "Check this file:")
	}

	expectedURI := "https://example.com/file.pdf"
	if got := string(capturedMsg.Parts[1].URL()); got != expectedURI {
		t.Errorf("Parts[1].URL() = %q, want %q", got, expectedURI)
	}
}

// TestRunCombinesMultipleMessagesIntoSingleRequest verifies that a run with several
// input messages issues exactly one A2A request whose message carries the parts of
// every input message. This matches the framework's one-run/one-request contract and
// the .NET/Python A2A providers, which map a run's messages to a single A2A Message.
func TestRunCombinesMultipleMessagesIntoSingleRequest(t *testing.T) {
	transport := &mockA2ATransport{
		responseToReturn: &a2a.Task{
			ID:        a2a.TaskID("task-abc"),
			ContextID: "ctx-abc",
			Status: a2a.TaskStatus{
				State: a2a.TaskStateSubmitted,
			},
		},
	}
	a := newTestAgent(transport, agent.Config{})

	session, err := a.CreateSession(t.Context())
	if err != nil {
		t.Fatal(err)
	}

	inputMessages := []*message.Message{
		{Role: message.RoleUser, Contents: []message.Content{&message.TextContent{Text: "A"}}},
		{Role: message.RoleUser, Contents: []message.Content{&message.TextContent{Text: "B"}}},
	}

	_, err = a.Run(t.Context(), inputMessages, agent.WithSession(session)).Collect()
	if err != nil {
		t.Fatalf("error = %v, want nil", err)
	}

	if transport.sendMessageCallCount != 1 {
		t.Fatalf("SendMessage call count = %d, want 1", transport.sendMessageCallCount)
	}

	capturedMsg := transport.capturedMessageSendParams.Message
	if capturedMsg == nil {
		t.Fatal("capturedMessageSendParams.Message is nil")
	}
	if len(capturedMsg.Parts) != 2 {
		t.Fatalf("len(message.Parts) = %d, want 2", len(capturedMsg.Parts))
	}
	if got := capturedMsg.Parts[0].Text(); got != "A" {
		t.Errorf("Parts[0].Text() = %q, want %q", got, "A")
	}
	if got := capturedMsg.Parts[1].Text(); got != "B" {
		t.Errorf("Parts[1].Text() = %q, want %q", got, "B")
	}

	// The combined message must not be cross-linked to a task created earlier in the
	// same run: there is no prior task, so it references and targets none.
	if len(capturedMsg.ReferenceTasks) != 0 {
		t.Errorf("message.ReferenceTasks = %v, want empty", capturedMsg.ReferenceTasks)
	}
	if capturedMsg.TaskID != "" {
		t.Errorf("message.TaskID = %q, want empty", capturedMsg.TaskID)
	}
}

func TestRunOmitsEmptyMetadata(t *testing.T) {
	transport := &mockA2ATransport{
		responseToReturn: &a2a.Task{
			ID:        a2a.TaskID("task-abc"),
			ContextID: "ctx-abc",
			Status: a2a.TaskStatus{
				State: a2a.TaskStateSubmitted,
			},
		},
	}
	a := newTestAgent(transport, agent.Config{})

	session, err := a.CreateSession(t.Context())
	if err != nil {
		t.Fatal(err)
	}

	inputMessages := []*message.Message{
		{Role: message.RoleUser, Contents: []message.Content{&message.TextContent{Text: "A"}}, AdditionalProperties: map[string]any{}},
	}

	_, err = a.Run(t.Context(), inputMessages, agent.WithSession(session)).Collect()
	if err != nil {
		t.Fatalf("error = %v, want nil", err)
	}

	capturedMsg := transport.capturedMessageSendParams.Message
	if capturedMsg == nil {
		t.Fatal("capturedMessageSendParams.Message is nil")
	}
	if capturedMsg.Metadata != nil {
		t.Fatalf("message.Metadata = %#v, want nil", capturedMsg.Metadata)
	}
}

// TestRunStreamingCombinesMultipleMessagesIntoSingleRequest is the streaming variant of
// TestRunCombinesMultipleMessagesIntoSingleRequest.
func TestRunStreamingCombinesMultipleMessagesIntoSingleRequest(t *testing.T) {
	transport := &mockA2ATransport{
		streamingResponseToReturn: &a2a.Task{
			ID:        a2a.TaskID("task-abc"),
			ContextID: "ctx-abc",
			Status: a2a.TaskStatus{
				State: a2a.TaskStateSubmitted,
			},
		},
	}
	a := newTestAgent(transport, agent.Config{})

	session, err := a.CreateSession(t.Context())
	if err != nil {
		t.Fatal(err)
	}

	inputMessages := []*message.Message{
		{Role: message.RoleUser, Contents: []message.Content{&message.TextContent{Text: "A"}}},
		{Role: message.RoleUser, Contents: []message.Content{&message.TextContent{Text: "B"}}},
	}

	for _, err := range a.Run(t.Context(), inputMessages, agent.WithSession(session), agent.Stream(true)) {
		if err != nil {
			t.Fatalf("error = %v, want nil", err)
		}
	}

	if transport.sendStreamingCallCount != 1 {
		t.Fatalf("SendStreamingMessage call count = %d, want 1", transport.sendStreamingCallCount)
	}

	capturedMsg := transport.capturedMessageSendParams.Message
	if capturedMsg == nil {
		t.Fatal("capturedMessageSendParams.Message is nil")
	}
	if len(capturedMsg.Parts) != 2 {
		t.Fatalf("len(message.Parts) = %d, want 2", len(capturedMsg.Parts))
	}
	if got := capturedMsg.Parts[0].Text(); got != "A" {
		t.Errorf("Parts[0].Text() = %q, want %q", got, "A")
	}
	if got := capturedMsg.Parts[1].Text(); got != "B" {
		t.Errorf("Parts[1].Text() = %q, want %q", got, "B")
	}
	if len(capturedMsg.ReferenceTasks) != 0 {
		t.Errorf("message.ReferenceTasks = %v, want empty", capturedMsg.ReferenceTasks)
	}
	if capturedMsg.TaskID != "" {
		t.Errorf("message.TaskID = %q, want empty", capturedMsg.TaskID)
	}
}

// TestRunWithContinuationTokenAndMessages tests error when both continuation token and messages are provided
func TestRunWithContinuationTokenAndMessages(t *testing.T) {
	transport := &mockA2ATransport{}
	a := newTestAgent(transport, agent.Config{})

	result, err := a.RunText(t.Context(), "Test message", agent.WithContinuationToken(agenttest.NewContinuationToken(t, "task-123"))).Collect()
	const wantError = "messages are not allowed when continuing a background response using a continuation token"
	if err == nil || err.Error() != wantError {
		t.Fatalf("error = %v, want %q", err, wantError)
	}
	if result != nil {
		t.Errorf("result = %#v, want nil for invalid continuation input", result)
	}
}

// TestRunWithContinuationToken tests that continuation token calls GetTask
func TestRunWithContinuationToken(t *testing.T) {
	transport := &mockA2ATransport{
		responseToReturn: &a2a.Task{
			ID:        a2a.TaskID("task-123"),
			ContextID: "context-123",
			Status:    a2a.TaskStatus{State: a2a.TaskStateSubmitted},
		},
	}
	a := newTestAgent(transport, agent.Config{})

	_, err := a.Run(t.Context(), nil, agent.WithContinuationToken(agenttest.NewContinuationToken(t, "task-123"))).Collect()
	if err != nil {
		t.Fatalf("error = %v, want nil", err)
	}
	if !transport.getTaskCalled {
		t.Error("GetTask was not called")
	}
	if transport.capturedGetTaskReq == nil {
		t.Fatal("capturedGetTaskReq is nil")
	}
	if transport.capturedGetTaskReq.ID != "task-123" {
		t.Errorf("GetTask request ID = %q, want %q", transport.capturedGetTaskReq.ID, "task-123")
	}
}

func TestRunValidatesRequestedContextID(t *testing.T) {
	for _, stream := range []bool{false, true} {
		for _, continuation := range []bool{false, true} {
			for _, withSession := range []bool{false, true} {
				for _, responseContext := range []string{"ctx-request", "ctx-other", ""} {
					t.Run(fmt.Sprintf("stream=%t/continuation=%t/session=%t/response=%s", stream, continuation, withSession, responseContext), func(t *testing.T) {
						responseMessage := &a2a.Message{
							ID: "response-1", Role: a2a.MessageRoleAgent, ContextID: responseContext,
							Parts: a2a.ContentParts{a2a.NewTextPart("reply")},
						}
						transport := &mockA2ATransport{
							responseToReturn:          responseMessage,
							streamingResponseToReturn: responseMessage,
							rawStreamingResponse:      true,
						}
						opts := []agent.Option{agent.WithServiceID("ctx-request"), agent.Stream(stream)}
						messages := []*message.Message{message.NewText("hello")}
						if continuation {
							task := &a2a.Task{
								ID: "task-1", ContextID: responseContext,
								Status: a2a.TaskStatus{State: a2a.TaskStateCompleted},
							}
							transport.responseToReturn = task
							transport.subscribeResponseToReturn = task
							opts = append(opts, agent.WithContinuationToken(agenttest.NewContinuationToken(t, "task-1")))
							messages = nil
						}
						session := &agent.Session{}
						if withSession {
							opts = append(opts, agent.WithSession(session))
						}
						a := newTestAgent(transport, agent.Config{})
						response, err := a.Run(t.Context(), messages, opts...).Collect()
						if !continuation {
							if transport.capturedMessageSendParams == nil || transport.capturedMessageSendParams.Message.ContextID != "ctx-request" {
								t.Fatalf("request did not use the run option context: %+v", transport.capturedMessageSendParams)
							}
						}
						if responseContext == "ctx-other" {
							if err == nil || !strings.Contains(err.Error(), "mismatched context ID") {
								t.Fatalf("error = %v, want mismatched context ID", err)
							}
							if session.ServiceID() != "" || latestTaskID(session) != "" {
								t.Fatalf("invalid response changed session: context=%q, task=%q", session.ServiceID(), latestTaskID(session))
							}
							return
						}
						if err != nil {
							t.Fatal(err)
						}
						if response.ConversationID == nil || *response.ConversationID != "ctx-request" {
							t.Errorf("conversation ID = %v, want ctx-request", response.ConversationID)
						}
						if withSession && session.ServiceID() != "ctx-request" {
							t.Errorf("session ID = %q, want ctx-request", session.ServiceID())
						}
					})
				}
			}
		}
	}
}

// TestRunWithTaskInSessionAndMessage tests that task ID is added as reference
func TestRunWithTaskInSessionAndMessage(t *testing.T) {
	transport := &mockA2ATransport{
		responseToReturn: &a2a.Message{
			ID:    "response-123",
			Role:  a2a.MessageRoleAgent,
			Parts: a2a.ContentParts{a2a.NewTextPart("Response to task")},
		},
	}
	a := newTestAgent(transport, agent.Config{})

	session, err := a.CreateSession(t.Context(), agent.WithServiceID("context-123"), a2a1.WithTaskID("task-123"))
	if err != nil {
		t.Fatal(err)
	}

	_, err = a.RunText(t.Context(), "Please make the background transparent", agent.WithSession(session)).Collect()
	if err != nil {
		t.Fatalf("error = %v, want nil", err)
	}

	capturedMsg := transport.capturedMessageSendParams.Message
	if capturedMsg == nil {
		t.Fatal("capturedMessageSendParams.Message is nil")
	}
	if capturedMsg.TaskID != "" {
		t.Errorf("message.TaskID = %q, want empty", capturedMsg.TaskID)
	}
	if len(capturedMsg.ReferenceTasks) == 0 {
		t.Error("message.ReferenceTasks is empty, expected task-123")
	} else if string(capturedMsg.ReferenceTasks[0]) != "task-123" {
		t.Errorf("message.ReferenceTasks[0] = %q, want %q", capturedMsg.ReferenceTasks[0], "task-123")
	}
}

func TestRunPerServiceCallHistoryCanBecomeServiceManaged(t *testing.T) {
	for _, stream := range []bool{false, true} {
		t.Run(fmt.Sprint(stream), func(t *testing.T) {
			first := &a2a.Message{ID: "first", Role: a2a.MessageRoleAgent, Parts: a2a.ContentParts{a2a.NewTextPart("first")}}
			transport := &mockA2ATransport{
				responseToReturn: first, streamingResponseToReturn: first, rawStreamingResponse: true,
			}
			a := newTestAgent(transport, agent.Config{RequirePerServiceCallHistoryPersistence: true})
			session := &agent.Session{}
			if _, err := a.RunText(t.Context(), "start", agent.WithSession(session), agent.Stream(stream)).Collect(); err != nil {
				t.Fatal(err)
			}
			second := &a2a.Message{ID: "second", Role: a2a.MessageRoleAgent, ContextID: "ctx-new", Parts: a2a.ContentParts{a2a.NewTextPart("second")}}
			transport.responseToReturn = second
			transport.streamingResponseToReturn = second
			response, err := a.RunText(t.Context(), "next", agent.WithSession(session), agent.Stream(stream)).Collect()
			if err != nil {
				t.Fatal(err)
			}
			if got := transport.capturedMessageSendParams.Message.ContextID; got != "" {
				t.Errorf("local history ID reached the A2A service: %q", got)
			}
			if response.ConversationID == nil || *response.ConversationID != "ctx-new" || session.ServiceID() != "ctx-new" {
				t.Fatalf("response/session ID = %v/%q, want ctx-new", response.ConversationID, session.ServiceID())
			}
		})
	}
}

func TestCreateSessionWithRepeatedTaskIDUsesLastValue(t *testing.T) {
	transport := &mockA2ATransport{
		responseToReturn: &a2a.Message{
			ID:    "response-123",
			Role:  a2a.MessageRoleAgent,
			Parts: a2a.ContentParts{a2a.NewTextPart("Response to tasks")},
		},
	}
	a := newTestAgent(transport, agent.Config{})

	session, err := a.CreateSession(
		t.Context(),
		agent.WithServiceID("context-123"),
		a2a1.WithTaskID("task-123"),
		a2a1.WithTaskID("task-456"),
	)
	if err != nil {
		t.Fatal(err)
	}

	_, err = a.RunText(t.Context(), "Please make the background transparent", agent.WithSession(session)).Collect()
	if err != nil {
		t.Fatalf("error = %v, want nil", err)
	}

	capturedMsg := transport.capturedMessageSendParams.Message
	if capturedMsg == nil {
		t.Fatal("capturedMessageSendParams.Message is nil")
	}
	if len(capturedMsg.ReferenceTasks) != 1 {
		t.Fatalf("len(message.ReferenceTasks) = %d, want 1", len(capturedMsg.ReferenceTasks))
	}
	if string(capturedMsg.ReferenceTasks[0]) != "task-456" {
		t.Errorf("message.ReferenceTasks[0] = %q, want %q", capturedMsg.ReferenceTasks[0], "task-456")
	}
}

func TestRunKeepsCurrentTaskIDAcrossTurns(t *testing.T) {
	transport := &mockA2ATransport{
		responseToReturn: &a2a.Task{
			ID:        a2a.TaskID("task-123"),
			ContextID: "ctx-123",
			Status:    a2a.TaskStatus{State: a2a.TaskStateCompleted},
		},
	}
	a := newTestAgent(transport, agent.Config{})

	session, err := a.CreateSession(t.Context())
	if err != nil {
		t.Fatal(err)
	}

	for i := range 3 {
		if _, err := a.RunText(t.Context(), "Do something", agent.WithSession(session)).Collect(); err != nil {
			t.Fatalf("run %d error = %v, want nil", i, err)
		}
	}

	if taskID := a2a1.TaskIDFromSession(session); taskID != "task-123" {
		t.Fatalf("TaskIDFromSession = %q, want %q", taskID, "task-123")
	}
}

func TestRunReplacesCurrentTaskID(t *testing.T) {
	transport := &mockA2ATransport{
		responseToReturn: &a2a.Task{
			ID:        a2a.TaskID("task-123"),
			ContextID: "ctx-123",
			Status:    a2a.TaskStatus{State: a2a.TaskStateCompleted},
		},
	}
	a := newTestAgent(transport, agent.Config{})

	session, err := a.CreateSession(t.Context())
	if err != nil {
		t.Fatal(err)
	}

	if _, err := a.RunText(t.Context(), "Do something", agent.WithSession(session)).Collect(); err != nil {
		t.Fatalf("first run error = %v, want nil", err)
	}

	transport.responseToReturn = &a2a.Task{
		ID:        a2a.TaskID("task-456"),
		ContextID: "ctx-123",
		Status:    a2a.TaskStatus{State: a2a.TaskStateCompleted},
	}
	if _, err := a.RunText(t.Context(), "Do something else", agent.WithSession(session)).Collect(); err != nil {
		t.Fatalf("second run error = %v, want nil", err)
	}

	if taskID := a2a1.TaskIDFromSession(session); taskID != "task-456" {
		t.Fatalf("TaskIDFromSession = %q, want %q", taskID, "task-456")
	}
}

func assertTaskRoutingAfterFollowUp(t *testing.T, initialTaskID string, initialTaskState a2a.TaskState, wantTaskID string, wantReferenceTask string) {
	transport := &mockA2ATransport{
		responseToReturn: &a2a.Task{
			ID:        a2a.TaskID(initialTaskID),
			ContextID: "ctx-123",
			Status: a2a.TaskStatus{
				State: initialTaskState,
			},
		},
	}
	a := newTestAgent(transport, agent.Config{})

	session, err := a.CreateSession(t.Context())
	if err != nil {
		t.Fatal(err)
	}

	_, err = a.RunText(t.Context(), "Do something", agent.WithSession(session)).Collect()
	if err != nil {
		t.Fatalf("first run error = %v, want nil", err)
	}

	transport.responseToReturn = &a2a.Message{
		ID:        "response-final",
		ContextID: "ctx-123",
		Role:      a2a.MessageRoleAgent,
		Parts:     a2a.ContentParts{a2a.NewTextPart("Done")},
	}
	transport.capturedMessageSendParams = nil

	_, err = a.RunText(t.Context(), "Here is your input", agent.WithSession(session)).Collect()
	if err != nil {
		t.Fatalf("second run error = %v, want nil", err)
	}

	capturedMsg := transport.capturedMessageSendParams.Message
	if capturedMsg == nil {
		t.Fatal("capturedMessageSendParams.Message is nil")
	}
	if got := string(capturedMsg.TaskID); got != wantTaskID {
		t.Errorf("message.TaskID = %q, want %q", capturedMsg.TaskID, wantTaskID)
	}
	switch {
	case wantReferenceTask == "":
		if len(capturedMsg.ReferenceTasks) != 0 {
			t.Errorf("message.ReferenceTasks = %v, want empty", capturedMsg.ReferenceTasks)
		}
	case len(capturedMsg.ReferenceTasks) == 0:
		t.Fatalf("message.ReferenceTasks is empty, want %q", wantReferenceTask)
	default:
		if got := string(capturedMsg.ReferenceTasks[0]); got != wantReferenceTask {
			t.Errorf("message.ReferenceTasks[0] = %q, want %q", got, wantReferenceTask)
		}
	}
}

// TestRunWithInputRequiredTask_UsesTaskId tests that when the last task state is InputRequired,
// the follow-up message uses TaskId (not ReferenceTasks) to link to the waiting task.
func TestRunWithInputRequiredTask_UsesTaskId(t *testing.T) {
	assertTaskRoutingAfterFollowUp(t, "task-waiting", a2a.TaskStateInputRequired, "task-waiting", "")
}

// TestRunWithCompletedTask_UsesReferenceTasks tests that a follow-up after a completed task
// uses ReferenceTasks (not TaskId).
func TestRunWithCompletedTask_UsesReferenceTasks(t *testing.T) {
	assertTaskRoutingAfterFollowUp(t, "task-done", a2a.TaskStateCompleted, "", "task-done")
}

// TestRunWithAgentTask tests that session task ID is updated
func TestRunWithAgentTask(t *testing.T) {
	transport := &mockA2ATransport{
		responseToReturn: &a2a.Task{
			ID:        a2a.TaskID("task-456"),
			ContextID: "context-789",
			Status: a2a.TaskStatus{
				State: a2a.TaskStateSubmitted,
			},
		},
	}
	a := newTestAgent(transport, agent.Config{})

	session, err := a.CreateSession(t.Context())
	if err != nil {
		t.Fatal(err)
	}

	_, err = a.RunText(t.Context(), "Start a task", agent.WithSession(session)).Collect()
	if err != nil {
		t.Fatalf("error = %v, want nil", err)
	}

	if got := latestTaskID(session); got != "task-456" {
		t.Errorf("session.TaskID = %q, want %q", got, "task-456")
	}
}

// TestRunWithAgentTaskResponse tests task response conversion
func TestRunWithAgentTaskResponse(t *testing.T) {
	transport := &mockA2ATransport{
		responseToReturn: &a2a.Task{
			ID:        a2a.TaskID("task-789"),
			ContextID: "context-456",
			Status: a2a.TaskStatus{
				State: a2a.TaskStateSubmitted,
			},
			Artifacts: []*a2a.Artifact{
				{
					ID:    a2a.ArtifactID("art-1"),
					Parts: a2a.ContentParts{a2a.NewTextPart("Artifact content")},
				},
			},
		},
	}
	a := newTestAgent(transport, agent.Config{})

	session, err := a.CreateSession(t.Context())
	if err != nil {
		t.Fatal(err)
	}

	result, err := a.RunText(t.Context(), "Start a long-running task", agent.WithSession(session)).Collect()
	if err != nil {
		t.Fatalf("error = %v, want nil", err)
	}

	if result == nil {
		t.Fatal("result is nil")
	}
	if len(result.Messages) != 1 {
		t.Fatalf("len(result.Messages) = %d, want 1", len(result.Messages))
	}
	msg := result.Messages[0]
	if msg.ID != "art-1" {
		t.Errorf("ID = %q, want art-1", msg.ID)
	}

	if result.ContinuationToken == "" {
		t.Error("ContinuationToken is empty, want non-empty")
	}

	if got := session.ServiceID(); got != "context-456" {
		t.Errorf("session.ContextID = %q, want %q", got, "context-456")
	}
	if got := latestTaskID(session); got != "task-789" {
		t.Errorf("session.TaskID = %q, want %q", got, "task-789")
	}
}

// TestRunWithVariousTaskStates tests continuation token behavior for different task states
func TestRunWithVariousTaskStates(t *testing.T) {
	tests := []struct {
		name                    string
		state                   a2a.TaskState
		expectContinuationToken bool
	}{
		{"Submitted", a2a.TaskStateSubmitted, true},
		{"Working", a2a.TaskStateWorking, true},
		{"Completed", a2a.TaskStateCompleted, false},
		{"Failed", a2a.TaskStateFailed, false},
		{"Canceled", a2a.TaskStateCanceled, false},
		{"InputRequired", a2a.TaskStateInputRequired, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			for _, withArtifacts := range []bool{false, true} {
				t.Run(fmt.Sprintf("artifacts=%t", withArtifacts), func(t *testing.T) {
					task := &a2a.Task{
						ID:        a2a.TaskID("task-123"),
						ContextID: "context-123",
						Status:    a2a.TaskStatus{State: tt.state},
					}
					if withArtifacts {
						task.Artifacts = []*a2a.Artifact{
							{
								ID:    a2a.ArtifactID("art-1"),
								Parts: a2a.ContentParts{a2a.NewTextPart("Content")},
							},
						}
					}
					transport := &mockA2ATransport{responseToReturn: task}
					a := newTestAgent(transport, agent.Config{})

					result, err := a.RunText(t.Context(), "Test message").Collect()
					if err != nil {
						t.Fatalf("error = %v, want nil", err)
					}
					if result == nil || result.ID != "task-123" {
						t.Fatalf("response = %#v, want task-123", result)
					}

					if tt.expectContinuationToken && result.ContinuationToken == "" {
						t.Error("ContinuationToken is empty, want non-empty")
					} else if !tt.expectContinuationToken && result.ContinuationToken != "" {
						t.Errorf("ContinuationToken = %v, want empty", result.ContinuationToken)
					}
					wantFinishReason := ""
					if tt.state == a2a.TaskStateCompleted {
						wantFinishReason = "stop"
					}
					if result.FinishReason != wantFinishReason {
						t.Errorf("FinishReason = %q, want %q", result.FinishReason, wantFinishReason)
					}
				})
			}
		})
	}
}

func TestStreamingArtifactPreservesTaskStateAndDirectMessageClearsTask(t *testing.T) {
	transport := &mockA2ATransport{
		streamingResponsesToReturn: []a2a.Event{
			&a2a.TaskStatusUpdateEvent{TaskID: "task-1", ContextID: "context-1", Status: a2a.TaskStatus{State: a2a.TaskStateWorking}},
			&a2a.TaskArtifactUpdateEvent{TaskID: "task-1", ContextID: "context-1", Artifact: &a2a.Artifact{ID: "artifact-1", Parts: a2a.ContentParts{a2a.NewTextPart("progress")}}},
		},
		responseToReturn: &a2a.Message{ID: "message-2", ContextID: "context-1", Role: a2a.MessageRoleAgent},
	}
	a := newTestAgent(transport, agent.Config{})
	session, err := a.CreateSession(t.Context())
	if err != nil {
		t.Fatal(err)
	}

	if _, err := a.RunText(t.Context(), "start", agent.WithSession(session), agent.Stream(true)).Collect(); err != nil {
		t.Fatal(err)
	}
	if got := a2a1.TaskIDFromSession(session); got != "task-1" {
		t.Fatalf("task ID after artifact = %q, want task-1", got)
	}
	if _, err := a.RunText(t.Context(), "follow up", agent.WithSession(session)).Collect(); err != nil {
		t.Fatal(err)
	}
	if refs := transport.capturedMessageSendParams.Message.ReferenceTasks; len(refs) != 1 || refs[0] != "task-1" {
		t.Fatalf("follow-up references = %v, want [task-1]", refs)
	}
	if got := a2a1.TaskIDFromSession(session); got != "" {
		t.Fatalf("task ID after direct message = %q, want empty", got)
	}
}

// TestRunStreamingWithContinuationTokenAndMessages tests error in streaming mode
func TestRunStreamingWithContinuationTokenAndMessages(t *testing.T) {
	transport := &mockA2ATransport{}
	a := newTestAgent(transport, agent.Config{})

	var gotErr error
	var yields int
	for update, err := range a.RunText(t.Context(), "Test message", agent.WithContinuationToken(agenttest.NewContinuationToken(t, "task-123")), agent.Stream(true)) {
		yields++
		if update != nil {
			t.Errorf("update = %#v, want nil for invalid continuation input", update)
		}
		gotErr = err
	}
	const wantError = "messages are not allowed when continuing a background response using a continuation token"
	if gotErr == nil || gotErr.Error() != wantError {
		t.Fatalf("error = %v, want %q", gotErr, wantError)
	}
	if yields != 1 {
		t.Errorf("stream yields = %d, want one terminal error", yields)
	}
}

func TestRunStreamingWithContinuationToken_UsesSubscribeToTask(t *testing.T) {
	transport := &mockA2ATransport{
		subscribeResponseToReturn: &a2a.Message{
			ID:        "response-123",
			Role:      a2a.MessageRoleAgent,
			TaskID:    "task-456",
			ContextID: "ctx-456",
			Parts:     a2a.ContentParts{a2a.NewTextPart("Continuation response")},
		},
	}
	a := newTestAgent(transport, agent.Config{})

	var updates []*agent.ResponseUpdate
	for update, err := range a.Run(t.Context(), nil, agent.WithContinuationToken(agenttest.NewContinuationToken(t, "task-456")), agent.Stream(true)) {
		if err != nil {
			t.Fatalf("error = %v, want nil", err)
		}
		updates = append(updates, update)
	}

	if len(updates) != 1 || updates[0] == nil {
		t.Fatalf("updates = %#v, want one response update", updates)
	}
	if !transport.subscribeToTaskCalled {
		t.Fatal("SubscribeToTask was not called")
	}
	if transport.sendStreamingMessageCalled {
		t.Fatal("SendStreamingMessage was called, want SubscribeToTask only")
	}
	if !slices.Equal(transport.requestMethods, []string{"SubscribeToTask"}) {
		t.Errorf("request methods = %v, want [SubscribeToTask]", transport.requestMethods)
	}
	if got := updates[0].String(); got != "Continuation response" {
		t.Errorf("update.String() = %q, want %q", got, "Continuation response")
	}
}

func TestRunStreamingWithContinuationToken_PassesCorrectTaskID(t *testing.T) {
	const expectedTaskID = "my-task-789"

	transport := &mockA2ATransport{
		subscribeResponseToReturn: &a2a.Message{
			ID:        "response-123",
			Role:      a2a.MessageRoleAgent,
			TaskID:    a2a.TaskID(expectedTaskID),
			ContextID: "ctx-456",
			Parts:     a2a.ContentParts{a2a.NewTextPart("Continuation response")},
		},
	}
	a := newTestAgent(transport, agent.Config{})

	for _, err := range a.Run(t.Context(), nil, agent.WithContinuationToken(agenttest.NewContinuationToken(t, expectedTaskID)), agent.Stream(true)) {
		if err != nil {
			t.Fatalf("error = %v, want nil", err)
		}
	}

	if transport.capturedSubscribeToTaskReq == nil {
		t.Fatal("capturedSubscribeToTaskReq is nil")
	}
	if got := string(transport.capturedSubscribeToTaskReq.ID); got != expectedTaskID {
		t.Errorf("SubscribeToTaskRequest.ID = %q, want %q", got, expectedTaskID)
	}
}

func TestRunStreamingWithContinuationTokenWhenSubscribeFailsWithUnsupportedOperationFallsBackToGetTask(t *testing.T) {
	const taskID = "completed-task-123"
	const contextID = "ctx-completed"

	transport := &mockA2ATransport{
		subscribeErrToReturn: a2a.ErrUnsupportedOperation,
		responseToReturn: &a2a.Task{
			ID:        a2a.TaskID(taskID),
			ContextID: contextID,
			Status: a2a.TaskStatus{
				State: a2a.TaskStateCompleted,
			},
			Artifacts: []*a2a.Artifact{{
				ID:    a2a.ArtifactID("art-1"),
				Parts: a2a.ContentParts{a2a.NewTextPart("Final result")},
			}},
		},
	}
	a := newTestAgent(transport, agent.Config{})

	var updates []*agent.ResponseUpdate
	for update, err := range a.Run(t.Context(), nil, agent.WithContinuationToken(agenttest.NewContinuationToken(t, taskID)), agent.Stream(true)) {
		if err != nil {
			t.Fatalf("error = %v, want nil", err)
		}
		updates = append(updates, update)
	}

	if len(updates) != 1 {
		t.Fatalf("len(updates) = %d, want 1", len(updates))
	}
	update := updates[0]
	if update.ResponseID != taskID {
		t.Errorf("update.ResponseID = %q, want %q", update.ResponseID, taskID)
	}
	if update.FinishReason != "stop" {
		t.Errorf("update.FinishReason = %q, want stop", update.FinishReason)
	}
	rawTask, ok := update.RawRepresentation.(*a2a.Task)
	if !ok || rawTask == nil {
		t.Fatalf("update.RawRepresentation type = %T, want *a2a.Task", update.RawRepresentation)
	}
	if rawTask.ID != taskID {
		t.Errorf("raw task ID = %q, want %q", rawTask.ID, taskID)
	}
	if !transport.subscribeToTaskCalled {
		t.Fatal("SubscribeToTask was not called")
	}
	if !transport.getTaskCalled {
		t.Fatal("GetTask was not called after SubscribeToTask fallback")
	}
	if !slices.Equal(transport.requestMethods, []string{"SubscribeToTask", "GetTask"}) {
		t.Errorf("request methods = %v, want [SubscribeToTask GetTask]", transport.requestMethods)
	}
}

func TestRunStreamingWithContinuationTokenWhenSubscribeFailsWithUnsupportedOperationUpdatesSession(t *testing.T) {
	const taskID = "completed-task-456"
	const contextID = "ctx-completed-456"

	for _, test := range []struct {
		name string
		err  error
	}{
		{name: "unsupported operation", err: a2a.ErrUnsupportedOperation},
		{name: "wrapped unsupported operation", err: fmt.Errorf("subscribe failed: %w", a2a.ErrUnsupportedOperation)},
	} {
		t.Run(test.name, func(t *testing.T) {
			transport := &mockA2ATransport{
				subscribeErrToReturn: test.err,
				responseToReturn: &a2a.Task{
					ID:        a2a.TaskID(taskID),
					ContextID: contextID,
					Status:    a2a.TaskStatus{State: a2a.TaskStateCompleted},
				},
			}
			a := newTestAgent(transport, agent.Config{})

			session, err := a.CreateSession(t.Context())
			if err != nil {
				t.Fatal(err)
			}

			for _, err := range a.Run(t.Context(), nil, agent.WithSession(session), agent.WithContinuationToken(agenttest.NewContinuationToken(t, taskID)), agent.Stream(true)) {
				if err != nil {
					t.Fatalf("error = %v, want nil", err)
				}
			}

			if got := session.ServiceID(); got != contextID {
				t.Errorf("session.ContextID = %q, want %q", got, contextID)
			}
			if got := latestTaskID(session); got != taskID {
				t.Errorf("session.TaskID = %q, want %q", got, taskID)
			}
		})
	}
}

func TestRunStreamingWithContinuationTokenWhenSubscribeFailsWithNonUnsupportedErrorPropagatesWithoutFallback(t *testing.T) {
	for _, test := range []struct {
		name string
		err  error
	}{
		{name: "task not found", err: a2a.ErrTaskNotFound},
		{name: "wrapped task not found", err: fmt.Errorf("subscribe failed: %w", a2a.ErrTaskNotFound)},
	} {
		t.Run(test.name, func(t *testing.T) {
			transport := &mockA2ATransport{subscribeErrToReturn: test.err}
			a := newTestAgent(transport, agent.Config{})

			var gotErr error
			var yields int
			for update, err := range a.Run(t.Context(), nil, agent.WithContinuationToken(agenttest.NewContinuationToken(t, "error-task-123")), agent.Stream(true)) {
				yields++
				if update != nil {
					t.Errorf("update = %#v, want none from failed subscription", update)
				}
				gotErr = err
			}
			if !errors.Is(gotErr, test.err) || !errors.Is(gotErr, a2a.ErrTaskNotFound) {
				t.Fatalf("error = %v, want original subscription error %v", gotErr, test.err)
			}
			if yields != 1 {
				t.Errorf("stream yields = %d, want one terminal error", yields)
			}
			if !transport.subscribeToTaskCalled {
				t.Fatal("SubscribeToTask was not called")
			}
			if transport.getTaskCalled {
				t.Fatal("GetTask was called, want no fallback for non-unsupported errors")
			}
			if !slices.Equal(transport.requestMethods, []string{"SubscribeToTask"}) {
				t.Errorf("request methods = %v, want [SubscribeToTask]", transport.requestMethods)
			}
			if transport.capturedSubscribeToTaskReq == nil || transport.capturedSubscribeToTaskReq.ID != "error-task-123" {
				t.Errorf("subscription request = %#v, want error-task-123", transport.capturedSubscribeToTaskReq)
			}
		})
	}
}

func TestRunStreamingWithContinuationTokenWhenSubscribeAndGetTaskBothFailPropagatesError(t *testing.T) {
	transport := &mockA2ATransport{
		subscribeErrToReturn: a2a.ErrUnsupportedOperation,
		getTaskErrToReturn:   a2a.ErrTaskNotFound,
	}
	a := newTestAgent(transport, agent.Config{})

	var gotErr error
	var yields int
	for update, err := range a.Run(t.Context(), nil, agent.WithContinuationToken(agenttest.NewContinuationToken(t, "failed-task-789")), agent.Stream(true)) {
		yields++
		if update != nil {
			t.Errorf("update = %#v, want none from failed fallback", update)
		}
		gotErr = err
	}

	if !errors.Is(gotErr, a2a.ErrTaskNotFound) {
		t.Fatalf("error = %v, want %v", gotErr, a2a.ErrTaskNotFound)
	}
	if errors.Is(gotErr, a2a.ErrUnsupportedOperation) {
		t.Errorf("error = %v, want only the GetTask failure", gotErr)
	}
	if yields != 1 {
		t.Errorf("stream yields = %d, want one terminal error", yields)
	}
	if transport.capturedSubscribeToTaskReq == nil || transport.capturedSubscribeToTaskReq.ID != "failed-task-789" {
		t.Errorf("subscription request = %#v, want failed-task-789", transport.capturedSubscribeToTaskReq)
	}
	if transport.capturedGetTaskReq == nil || transport.capturedGetTaskReq.ID != "failed-task-789" {
		t.Errorf("poll request = %#v, want failed-task-789", transport.capturedGetTaskReq)
	}
	if !slices.Equal(transport.requestMethods, []string{"SubscribeToTask", "GetTask"}) {
		t.Errorf("request methods = %v, want [SubscribeToTask GetTask]", transport.requestMethods)
	}
}

// TestRunStreamingWithTaskInSessionAndMessage tests task reference in streaming
func TestRunStreamingWithTaskInSessionAndMessage(t *testing.T) {
	transport := &mockA2ATransport{
		streamingResponseToReturn: &a2a.Message{
			ID:    "response-123",
			Role:  a2a.MessageRoleAgent,
			Parts: a2a.ContentParts{a2a.NewTextPart("Response to task")},
		},
	}
	a := newTestAgent(transport, agent.Config{})

	session, err := a.CreateSession(t.Context(), agent.WithServiceID("context-123"), a2a1.WithTaskID("task-123"))
	if err != nil {
		t.Fatal(err)
	}

	for _, err := range a.RunText(t.Context(), "Please make the background transparent", agent.WithSession(session), agent.Stream(true)) {
		if err != nil {
			t.Fatalf("error = %v, want nil", err)
		}
	}

	capturedMsg := transport.capturedMessageSendParams.Message
	if capturedMsg == nil {
		t.Fatal("capturedMessageSendParams.Message is nil")
	}
	if capturedMsg.TaskID != "" {
		t.Errorf("message.TaskID = %q, want empty", capturedMsg.TaskID)
	}
	if len(capturedMsg.ReferenceTasks) == 0 {
		t.Error("message.ReferenceTasks is empty, expected task-123")
	} else if string(capturedMsg.ReferenceTasks[0]) != "task-123" {
		t.Errorf("message.ReferenceTasks[0] = %q, want %q", capturedMsg.ReferenceTasks[0], "task-123")
	}
}

// TestRunStreamingWithAgentTaskUpdatesSession tests session task ID update in streaming
func TestRunStreamingWithAgentTaskUpdatesSession(t *testing.T) {
	for _, test := range []struct {
		name      string
		artifacts []*a2a.Artifact
	}{
		{name: "without artifacts"},
		{
			name: "with artifacts",
			artifacts: []*a2a.Artifact{{
				ID:    a2a.ArtifactID("art-1"),
				Parts: a2a.ContentParts{a2a.NewTextPart("Task content")},
			}},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			transport := &mockA2ATransport{
				streamingResponseToReturn: &a2a.Task{
					ID:        a2a.TaskID("task-456"),
					ContextID: "context-789",
					Status:    a2a.TaskStatus{State: a2a.TaskStateSubmitted},
					Artifacts: test.artifacts,
				},
			}
			a := newTestAgent(transport, agent.Config{})

			session, err := a.CreateSession(t.Context())
			if err != nil {
				t.Fatal(err)
			}

			for _, err := range a.RunText(t.Context(), "Start a task", agent.WithSession(session), agent.Stream(true)) {
				if err != nil {
					t.Fatalf("error = %v, want nil", err)
				}
			}

			if got := session.ServiceID(); got != "context-789" {
				t.Errorf("session.ServiceID = %q, want context-789", got)
			}
			if got := latestTaskID(session); got != "task-456" {
				t.Errorf("session.TaskID = %q, want %q", got, "task-456")
			}
		})
	}
}

// TestRunStreamingWithAgentMessage tests streaming message response
func TestRunStreamingWithAgentMessage(t *testing.T) {
	const messageID = "msg-123"
	const contextID = "ctx-456"
	const messageText = "Hello from agent!"

	transport := &mockA2ATransport{
		streamingResponseToReturn: &a2a.Message{
			ID:        messageID,
			Role:      a2a.MessageRoleAgent,
			ContextID: contextID,
			Parts:     a2a.ContentParts{a2a.NewTextPart(messageText)},
		},
	}
	a := newTestAgent(transport, agent.Config{})

	var updates []*agent.ResponseUpdate
	for update, err := range a.RunText(t.Context(), "Test message", agent.Stream(true)) {
		if err != nil {
			t.Fatalf("error = %v, want nil", err)
		}
		updates = append(updates, update)
	}

	if len(updates) != 1 {
		t.Fatalf("len(updates) = %d, want 1", len(updates))
	}

	update := updates[0]
	if update.Role != message.RoleAssistant {
		t.Errorf("update.Role = %q, want %q", update.Role, message.RoleAssistant)
	}
	if update.MessageID != messageID {
		t.Errorf("update.MessageID = %q, want %q", update.MessageID, messageID)
	}
	if update.ResponseID != messageID {
		t.Errorf("update.ResponseID = %q, want %q", update.ResponseID, messageID)
	}
	if update.String() != messageText {
		t.Errorf("update.String() = %q, want %q", update.String(), messageText)
	}
	if update.AgentID != a.ID() {
		t.Errorf("update.AgentID = %q, want %q", update.AgentID, a.ID())
	}
	if update.FinishReason != "stop" {
		t.Errorf("update.FinishReason = %q, want stop", update.FinishReason)
	}
	if rawMsg, ok := update.RawRepresentation.(*a2a.Message); !ok || rawMsg == nil {
		t.Errorf("update.RawRepresentation = %#v, want non-nil *a2a.Message", update.RawRepresentation)
	} else if rawMsg.ID != messageID {
		t.Errorf("raw message ID = %q, want %q", rawMsg.ID, messageID)
	}
}

// TestRunStreamingWithAgentTaskYieldsUpdate tests streaming task response
func TestRunStreamingWithAgentTaskYieldsUpdate(t *testing.T) {
	const taskID = "task-789"
	const contextID = "ctx-012"

	transport := &mockA2ATransport{
		streamingResponseToReturn: &a2a.Task{
			ID:        a2a.TaskID(taskID),
			ContextID: contextID,
			Status: a2a.TaskStatus{
				State: a2a.TaskStateSubmitted,
			},
			Artifacts: []*a2a.Artifact{
				{
					ID:    a2a.ArtifactID("art-123"),
					Parts: a2a.ContentParts{a2a.NewTextPart("Task artifact content")},
				},
			},
		},
	}
	a := newTestAgent(transport, agent.Config{})

	session, err := a.CreateSession(t.Context())
	if err != nil {
		t.Fatal(err)
	}

	var updates []*agent.ResponseUpdate
	for update, err := range a.RunText(t.Context(), "Start long-running task", agent.WithSession(session), agent.Stream(true)) {
		if err != nil {
			t.Fatalf("error = %v, want nil", err)
		}
		updates = append(updates, update)
	}

	if len(updates) != 1 {
		t.Fatalf("len(updates) = %d, want 1", len(updates))
	}

	update := updates[0]
	if update.Role != message.RoleAssistant {
		t.Errorf("update.Role = %q, want %q", update.Role, message.RoleAssistant)
	}
	if update.ResponseID != taskID {
		t.Errorf("update.ResponseID = %q, want %q", update.ResponseID, taskID)
	}
	if update.AgentID != a.ID() {
		t.Errorf("update.AgentID = %q, want %q", update.AgentID, a.ID())
	}
	if update.FinishReason != "" {
		t.Errorf("update.FinishReason = %q, want empty", update.FinishReason)
	}
	if rawTask, ok := update.RawRepresentation.(*a2a.Task); !ok || rawTask == nil {
		t.Errorf("update.RawRepresentation = %#v, want non-nil *a2a.Task", update.RawRepresentation)
	} else if string(rawTask.ID) != taskID {
		t.Errorf("raw task ID = %q, want %q", rawTask.ID, taskID)
	}

	if got := session.ServiceID(); got != contextID {
		t.Errorf("session.ContextID = %q, want %q", got, contextID)
	}
	if got := latestTaskID(session); got != taskID {
		t.Errorf("session.TaskID = %q, want %q", got, taskID)
	}
}

// TestRunStreamingWithTaskStatusUpdateEvent tests handling of TaskStatusUpdateEvent
func TestRunStreamingWithTaskStatusUpdateEvent(t *testing.T) {
	const taskID = "task-status-123"
	const contextID = "ctx-status-456"

	transport := &mockA2ATransport{
		streamingResponseToReturn: &a2a.TaskStatusUpdateEvent{
			TaskID:    a2a.TaskID(taskID),
			ContextID: contextID,
			Status: a2a.TaskStatus{
				State: a2a.TaskStateWorking,
			},
		},
	}
	a := newTestAgent(transport, agent.Config{})

	session, err := a.CreateSession(t.Context())
	if err != nil {
		t.Fatal(err)
	}

	var updates []*agent.ResponseUpdate
	for update, err := range a.RunText(t.Context(), "Check task status", agent.WithSession(session), agent.Stream(true)) {
		if err != nil {
			t.Fatalf("error = %v, want nil", err)
		}
		updates = append(updates, update)
	}

	if len(updates) != 1 {
		t.Fatalf("len(updates) = %d, want 1", len(updates))
	}

	update := updates[0]
	if update.Role != message.RoleAssistant {
		t.Errorf("update.Role = %q, want %q", update.Role, message.RoleAssistant)
	}
	if update.ResponseID != taskID {
		t.Errorf("update.ResponseID = %q, want %q", update.ResponseID, taskID)
	}
	if update.AgentID != a.ID() {
		t.Errorf("update.AgentID = %q, want %q", update.AgentID, a.ID())
	}
	if update.FinishReason != "" {
		t.Errorf("update.FinishReason = %q, want empty", update.FinishReason)
	}
	if update.MessageID != "" {
		t.Errorf("update.MessageID = %q, want empty (Status.Message is nil)", update.MessageID)
	}
	if _, ok := update.RawRepresentation.(*a2a.TaskStatusUpdateEvent); !ok {
		t.Errorf("update.RawRepresentation type = %T, want *a2a.TaskStatusUpdateEvent", update.RawRepresentation)
	}

	if got := session.ServiceID(); got != contextID {
		t.Errorf("session.ContextID = %q, want %q", got, contextID)
	}
	if got := latestTaskID(session); got != taskID {
		t.Errorf("session.TaskID = %q, want %q", got, taskID)
	}
}

// TestRunStreamingWithTaskStatusUpdateEvent_WithMessage tests that MessageID is populated
// from Status.Message.ID when the status update contains a message, aligning with the .NET
// fix in microsoft/agent-framework#6043. Contents are only populated for InputRequired or
// terminal states.
func TestRunStreamingWithTaskStatusUpdateEvent_WithMessage(t *testing.T) {
	const taskID = "task-status-msg-123"
	const contextID = "ctx-status-msg-456"
	const msgID = "msg-abc-789"
	const msgText = "Processing your request..."

	t.Run("InputRequired_populatesContents", func(t *testing.T) {
		transport := &mockA2ATransport{
			streamingResponseToReturn: &a2a.TaskStatusUpdateEvent{
				TaskID:    a2a.TaskID(taskID),
				ContextID: contextID,
				Status: a2a.TaskStatus{
					State: a2a.TaskStateInputRequired,
					Message: &a2a.Message{
						ID:    msgID,
						Role:  a2a.MessageRoleAgent,
						Parts: a2a.ContentParts{a2a.NewTextPart(msgText)},
					},
				},
			},
		}
		a := newTestAgent(transport, agent.Config{})

		session, err := a.CreateSession(t.Context())
		if err != nil {
			t.Fatal(err)
		}

		var updates []*agent.ResponseUpdate
		for update, err := range a.RunText(t.Context(), "Check task status", agent.WithSession(session), agent.Stream(true)) {
			if err != nil {
				t.Fatalf("error = %v, want nil", err)
			}
			updates = append(updates, update)
		}

		if len(updates) != 1 {
			t.Fatalf("len(updates) = %d, want 1", len(updates))
		}

		update := updates[0]
		if update.MessageID != msgID {
			t.Errorf("update.MessageID = %q, want %q (from Status.Message.ID)", update.MessageID, msgID)
		}
		if update.ResponseID != taskID {
			t.Errorf("update.ResponseID = %q, want %q", update.ResponseID, taskID)
		}
		if got := update.String(); got != msgText {
			t.Errorf("update.String() = %q, want %q", got, msgText)
		}
		if _, ok := update.RawRepresentation.(*a2a.TaskStatusUpdateEvent); !ok {
			t.Errorf("update.RawRepresentation type = %T, want *a2a.TaskStatusUpdateEvent", update.RawRepresentation)
		}
	})

	t.Run("Working_doesNotPopulateContents", func(t *testing.T) {
		transport := &mockA2ATransport{
			streamingResponseToReturn: &a2a.TaskStatusUpdateEvent{
				TaskID:    a2a.TaskID(taskID),
				ContextID: contextID,
				Status: a2a.TaskStatus{
					State: a2a.TaskStateWorking,
					Message: &a2a.Message{
						ID:    msgID,
						Role:  a2a.MessageRoleAgent,
						Parts: a2a.ContentParts{a2a.NewTextPart(msgText)},
					},
				},
			},
		}
		a := newTestAgent(transport, agent.Config{})

		session, err := a.CreateSession(t.Context())
		if err != nil {
			t.Fatal(err)
		}

		var updates []*agent.ResponseUpdate
		for update, err := range a.RunText(t.Context(), "Check task status", agent.WithSession(session), agent.Stream(true)) {
			if err != nil {
				t.Fatalf("error = %v, want nil", err)
			}
			updates = append(updates, update)
		}

		if len(updates) != 1 {
			t.Fatalf("len(updates) = %d, want 1", len(updates))
		}

		update := updates[0]
		if update.MessageID != msgID {
			t.Errorf("update.MessageID = %q, want %q (from Status.Message.ID)", update.MessageID, msgID)
		}
		if update.ResponseID != taskID {
			t.Errorf("update.ResponseID = %q, want %q", update.ResponseID, taskID)
		}
		if _, ok := update.RawRepresentation.(*a2a.TaskStatusUpdateEvent); !ok {
			t.Errorf("update.RawRepresentation type = %T, want *a2a.TaskStatusUpdateEvent", update.RawRepresentation)
		}
		if len(update.Contents) != 0 {
			t.Errorf("update.Contents = %v, want empty (Working state should not populate contents)", update.Contents)
		}
		if got := update.String(); got != "" {
			t.Errorf("update.String() = %q, want empty (Working state should not populate contents)", got)
		}
	})
}

// TestRunWithAgentTaskResponse_SurfacesArtifactMetadata asserts that an
// artifact's own Metadata is surfaced in the response update's
// AdditionalProperties, matching .NET's A2A conversion which preserves
// artifact-level metadata rather than dropping it.
func TestRunWithAgentTaskResponse_SurfacesArtifactMetadata(t *testing.T) {
	transport := &mockA2ATransport{
		responseToReturn: &a2a.Task{
			ID:        a2a.TaskID("task-meta"),
			ContextID: "context-meta",
			Status: a2a.TaskStatus{
				State: a2a.TaskStateCompleted,
			},
			Metadata: map[string]any{"task-key": "task-value"},
			Artifacts: []*a2a.Artifact{
				{
					ID:       a2a.ArtifactID("art-1"),
					Metadata: map[string]any{"ext": "v"},
					Parts:    a2a.ContentParts{a2a.NewTextPart("Artifact content")},
				},
			},
		},
	}
	a := newTestAgent(transport, agent.Config{})

	var updates []*agent.ResponseUpdate
	for update, err := range a.RunText(t.Context(), "Start a task") {
		if err != nil {
			t.Fatalf("error = %v, want nil", err)
		}
		updates = append(updates, update)
	}

	if len(updates) != 2 {
		t.Fatalf("len(updates) = %d, want artifact and response updates", len(updates))
	}
	props := updates[0].AdditionalProperties
	if got, ok := props["ext"]; !ok || got != "v" {
		t.Errorf("AdditionalProperties[ext] = %v (ok=%v), want %q", got, ok, "v")
	}
	if _, ok := props["task-key"]; ok {
		t.Error("artifact update contains task metadata")
	}
	if updates[0].MessageID != "art-1" || updates[1].MessageID != "" || updates[1].AdditionalProperties["task-key"] != "task-value" {
		t.Error("artifact identity or response-scoped task metadata was lost")
	}
	if updates[1].RawRepresentation != nil {
		t.Errorf("response metadata update raw = %T, want nil", updates[1].RawRepresentation)
	}
}

// TestRunWithAgentTaskResponse_NoMetadataYieldsNilProperties asserts that a task
// with no task-level or artifact-level metadata produces an update with nil
// AdditionalProperties rather than a non-nil empty map.
func TestRunWithAgentTaskResponse_NoMetadataYieldsNilProperties(t *testing.T) {
	transport := &mockA2ATransport{
		responseToReturn: &a2a.Task{
			ID:        a2a.TaskID("task-nometa"),
			ContextID: "context-nometa",
			Status: a2a.TaskStatus{
				State: a2a.TaskStateCompleted,
			},
			Metadata: map[string]any{},
			Artifacts: []*a2a.Artifact{
				{
					ID:       a2a.ArtifactID("art-1"),
					Metadata: map[string]any{},
					Parts:    a2a.ContentParts{a2a.NewTextPart("Artifact content")},
				},
			},
		},
	}
	a := newTestAgent(transport, agent.Config{})

	var updates []*agent.ResponseUpdate
	for update, err := range a.RunText(t.Context(), "Start a task") {
		if err != nil {
			t.Fatalf("error = %v, want nil", err)
		}
		updates = append(updates, update)
	}

	if len(updates) != 1 {
		t.Fatalf("len(updates) = %d, want one artifact update", len(updates))
	}
	for _, update := range updates {
		if props := update.AdditionalProperties; props != nil {
			t.Errorf("AdditionalProperties = %v, want nil", props)
		}
	}
}

func TestRunStreamingWithTaskArtifactUpdateEvent_UsesEventMetadata(t *testing.T) {
	transport := &mockA2ATransport{
		streamingResponseToReturn: &a2a.TaskArtifactUpdateEvent{
			TaskID:    a2a.TaskID("task-artifact-meta"),
			ContextID: "ctx-artifact-meta",
			Metadata:  map[string]any{"event-key": "event-value"},
			Artifact: &a2a.Artifact{
				ID:       a2a.ArtifactID("artifact-meta"),
				Metadata: map[string]any{"ext": "v"},
				Parts:    a2a.ContentParts{a2a.NewTextPart("Artifact data")},
			},
		},
	}
	a := newTestAgent(transport, agent.Config{})

	session, err := a.CreateSession(t.Context())
	if err != nil {
		t.Fatal(err)
	}

	var updates []*agent.ResponseUpdate
	for update, err := range a.RunText(t.Context(), "Process artifact", agent.WithSession(session), agent.Stream(true)) {
		if err != nil {
			t.Fatalf("error = %v, want nil", err)
		}
		updates = append(updates, update)
	}

	if len(updates) != 1 {
		t.Fatalf("len(updates) = %d, want 1", len(updates))
	}
	props := updates[0].AdditionalProperties
	if _, ok := props["ext"]; ok {
		t.Errorf("AdditionalProperties unexpectedly contains artifact metadata: %v", props)
	}
	if got, ok := props["event-key"]; !ok || got != "event-value" {
		t.Errorf("AdditionalProperties[event-key] = %v (ok=%v), want %q", got, ok, "event-value")
	}
}

// TestRunStreamingWithTaskArtifactUpdateEvent tests handling of TaskArtifactUpdateEvent
func TestRunStreamingWithTaskArtifactUpdateEvent(t *testing.T) {
	const taskID = "task-artifact-123"
	const contextID = "ctx-artifact-456"
	const artifactContent = "Task artifact data"

	transport := &mockA2ATransport{
		streamingResponseToReturn: &a2a.TaskArtifactUpdateEvent{
			TaskID:    a2a.TaskID(taskID),
			ContextID: contextID,
			Artifact: &a2a.Artifact{
				ID:    a2a.ArtifactID("artifact-789"),
				Parts: a2a.ContentParts{a2a.NewTextPart(artifactContent)},
			},
		},
	}
	a := newTestAgent(transport, agent.Config{})

	session, err := a.CreateSession(t.Context())
	if err != nil {
		t.Fatal(err)
	}

	var updates []*agent.ResponseUpdate
	for update, err := range a.RunText(t.Context(), "Process artifact", agent.WithSession(session), agent.Stream(true)) {
		if err != nil {
			t.Fatalf("error = %v, want nil", err)
		}
		updates = append(updates, update)
	}

	if len(updates) != 1 {
		t.Fatalf("len(updates) = %d, want 1", len(updates))
	}

	update := updates[0]
	if update.Role != message.RoleAssistant {
		t.Errorf("update.Role = %q, want %q", update.Role, message.RoleAssistant)
	}
	if update.ResponseID != taskID {
		t.Errorf("update.ResponseID = %q, want %q", update.ResponseID, taskID)
	}
	if update.AgentID != a.ID() {
		t.Errorf("update.AgentID = %q, want %q", update.AgentID, a.ID())
	}
	if update.FinishReason != "" {
		t.Errorf("update.FinishReason = %q, want empty", update.FinishReason)
	}
	if _, ok := update.RawRepresentation.(*a2a.TaskArtifactUpdateEvent); !ok {
		t.Errorf("update.RawRepresentation type = %T, want *a2a.TaskArtifactUpdateEvent", update.RawRepresentation)
	}

	if len(update.Contents) == 0 {
		t.Error("update.Contents is empty, want non-empty")
	}
	if update.String() != artifactContent {
		t.Errorf("update.String() = %q, want %q", update.String(), artifactContent)
	}

	if got := session.ServiceID(); got != contextID {
		t.Errorf("session.ContextID = %q, want %q", got, contextID)
	}
	if got := latestTaskID(session); got != taskID {
		t.Errorf("session.TaskID = %q, want %q", got, taskID)
	}
}

func TestConstructorWithAllProperties(t *testing.T) {
	a := newTestAgent(&mockA2ATransport{}, agent.Config{
		ID: "test-id", Name: "test-name", Description: "test-description",
	})
	if a.ID() != "test-id" || a.Name() != "test-name" || a.Description() != "test-description" {
		t.Errorf("agent properties = %q, %q, %q", a.ID(), a.Name(), a.Description())
	}
}

func TestConstructorWithDefaultProperties(t *testing.T) {
	a := newTestAgent(&mockA2ATransport{}, agent.Config{})
	if a.ID() == "" {
		t.Error("agent ID is empty")
	}
	if a.Name() != "" || a.Description() != "" {
		t.Errorf("name and description = %q, %q, want empty", a.Name(), a.Description())
	}
}

func TestConstructorWithEmptyConfig(t *testing.T) {
	config := agent.Config{}
	a := newTestAgent(&mockA2ATransport{}, config)
	if a.ID() == "" {
		t.Error("agent ID is empty")
	}
	if a.Name() != "" || a.Description() != "" {
		t.Errorf("name and description = %q, %q, want empty", a.Name(), a.Description())
	}
}

func TestConstructorWithConfigProperties(t *testing.T) {
	config := agent.Config{
		ID: "options-id", Name: "options-name", Description: "options-description",
	}
	a := newTestAgent(&mockA2ATransport{}, config)
	if a.ID() != "options-id" || a.Name() != "options-name" || a.Description() != "options-description" {
		t.Errorf("agent properties = %q, %q, %q", a.ID(), a.Name(), a.Description())
	}
}

func TestConstructorIsolatesConfigMutation(t *testing.T) {
	config := agent.Config{
		ID: "original-id", Name: "Original Name", Description: "Original Description",
	}
	a := newTestAgent(&mockA2ATransport{}, config)
	config.ID = "mutated-id"
	config.Name = "Mutated Name"
	config.Description = "Mutated Description"
	if a.ID() != "original-id" || a.Name() != "Original Name" || a.Description() != "Original Description" {
		t.Errorf("agent properties after config mutation = %q, %q, %q", a.ID(), a.Name(), a.Description())
	}
}

func TestConstructorWithConfigRejectsNilClient(t *testing.T) {
	config := a2a1.AgentConfig{}
	defer func() {
		if recover() == nil {
			t.Error("NewAgent with nil client did not panic")
		}
	}()
	a2a1.NewAgent(nil, config)
}

func TestCreateSessionWithContextID(t *testing.T) {
	a := newTestAgent(&mockA2ATransport{}, agent.Config{})
	session, err := a.CreateSession(t.Context(), agent.WithServiceID("test-context-123"))
	if err != nil {
		t.Fatal(err)
	}
	if session == nil {
		t.Fatal("session is nil")
	}
	if session.ServiceID() != "test-context-123" || latestTaskID(session) != "" {
		t.Errorf("session IDs = %q, %q, want test-context-123 and empty task ID", session.ServiceID(), latestTaskID(session))
	}
}

func TestCreateSessionWithContextAndTaskIDs(t *testing.T) {
	a := newTestAgent(&mockA2ATransport{}, agent.Config{})
	session, err := a.CreateSession(t.Context(), agent.WithServiceID("test-context-456"), a2a1.WithTaskID("test-task-789"))
	if err != nil {
		t.Fatal(err)
	}
	if session == nil {
		t.Fatal("session is nil")
	}
	if session.ServiceID() != "test-context-456" || latestTaskID(session) != "test-task-789" {
		t.Errorf("session IDs = %q, %q, want test-context-456 and test-task-789", session.ServiceID(), latestTaskID(session))
	}
}

func TestCreateSessionRejectsInvalidContextID(t *testing.T) {
	a := newTestAgent(&mockA2ATransport{}, agent.Config{})
	for _, test := range []struct{ name, id string }{
		{"null", ""}, {"empty", ""}, {"space", " "}, {"tab", "\t"}, {"newline", "\r\n"},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, err := a.CreateSession(t.Context(), agent.WithServiceID(test.id)); err == nil {
				t.Errorf("CreateSession with context ID %q succeeded, want error", test.id)
			}
		})
	}
}

func TestCreateSessionRejectsInvalidContextIDWithTaskID(t *testing.T) {
	a := newTestAgent(&mockA2ATransport{}, agent.Config{})
	for _, test := range []struct{ name, id string }{
		{"null", ""}, {"empty", ""}, {"space", " "}, {"tab", "\t"}, {"newline", "\r\n"},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, err := a.CreateSession(t.Context(), agent.WithServiceID(test.id), a2a1.WithTaskID("valid-task-id")); err == nil {
				t.Errorf("CreateSession with context ID %q and valid task ID succeeded, want error", test.id)
			}
		})
	}
}

func TestCreateSessionRejectsInvalidTaskIDWithContextID(t *testing.T) {
	a := newTestAgent(&mockA2ATransport{}, agent.Config{})
	for _, test := range []struct{ name, id string }{
		{"null", ""}, {"empty", ""}, {"space", " "}, {"tab", "\t"}, {"newline", "\r\n"},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, err := a.CreateSession(t.Context(), agent.WithServiceID("valid-context-id"), a2a1.WithTaskID(test.id)); err == nil {
				t.Errorf("CreateSession with valid context ID and task ID %q succeeded, want error", test.id)
			}
		})
	}
}

func TestRunWithMessageResponseMetadata(t *testing.T) {
	transport := &mockA2ATransport{responseToReturn: &a2a.Message{
		ID: "response-123", Role: a2a.MessageRoleAgent,
		Parts:    a2a.ContentParts{a2a.NewTextPart("Response with metadata")},
		Metadata: map[string]any{"responseKey1": "responseValue1", "responseCount": 99},
	}}
	a := newTestAgent(transport, agent.Config{})
	result, err := a.Run(t.Context(), []*message.Message{message.NewText("Test message")}).Collect()
	if err != nil {
		t.Fatal(err)
	}
	if result == nil {
		t.Fatal("response is nil")
	}
	if result.AdditionalProperties == nil {
		t.Fatal("response additional properties are nil")
	}
	if got := result.AdditionalProperties["responseKey1"]; got != "responseValue1" {
		t.Errorf("responseKey1 = %#v, want responseValue1", got)
	}
	if got := result.AdditionalProperties["responseCount"]; got != 99 {
		t.Errorf("responseCount = %#v, want 99", got)
	}
}

func TestRunWithSubmittedTaskResponseMetadata(t *testing.T) {
	transport := &mockA2ATransport{responseToReturn: &a2a.Task{
		ID: "task-789", ContextID: "context-456",
		Status:   a2a.TaskStatus{State: a2a.TaskStateSubmitted},
		Metadata: map[string]any{"key1": "value1", "count": 42},
	}}
	a := newTestAgent(transport, agent.Config{})
	session, err := a.CreateSession(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	result, err := a.RunText(t.Context(), "Start a long-running task", agent.WithSession(session)).Collect()
	if err != nil {
		t.Fatal(err)
	}
	if result == nil {
		t.Fatal("response is nil")
	}
	if result.AgentID != a.ID() {
		t.Errorf("AgentID = %q, want %q", result.AgentID, a.ID())
	}
	if result.ID != "task-789" || result.FinishReason != "" {
		t.Errorf("response ID and finish reason = %q, %q, want task-789 and empty", result.ID, result.FinishReason)
	}
	raw, ok := result.RawRepresentation.(*a2a.Task)
	if !ok || raw == nil {
		t.Errorf("raw response = %#v, want *a2a.Task", result.RawRepresentation)
	} else if raw.ID != "task-789" {
		t.Errorf("raw task ID = %q, want task-789", raw.ID)
	}
	if result.ContinuationToken == "" {
		t.Error("continuation token is empty")
	} else if got := agenttest.DecodeContinuationToken(t, result.ContinuationToken).InnerToken; got != "task-789" {
		t.Errorf("continuation task ID = %q, want task-789", got)
	}
	if session.ServiceID() != "context-456" || latestTaskID(session) != "task-789" {
		t.Errorf("session IDs = %q, %q, want context-456 and task-789", session.ServiceID(), latestTaskID(session))
	}
	if result.AdditionalProperties == nil {
		t.Fatal("response additional properties are nil")
	}
	if got := result.AdditionalProperties["key1"]; got != "value1" {
		t.Errorf("key1 = %#v, want value1", got)
	}
	if got := result.AdditionalProperties["count"]; got != 42 {
		t.Errorf("count = %#v, want 42", got)
	}
}

func TestRunWithTaskResponsePreservesMetadataAndRawScopes(t *testing.T) {
	for _, inputRequired := range []bool{false, true} {
		for _, metadata := range []bool{false, true} {
			for _, artifacts := range []int{0, 1, 2} {
				t.Run(fmt.Sprintf("input-required=%t/metadata=%t/artifacts=%d", inputRequired, metadata, artifacts), func(t *testing.T) {
					task := &a2a.Task{ID: "task", ContextID: "ctx", Status: a2a.TaskStatus{State: a2a.TaskStateCompleted}}
					if metadata {
						task.Metadata = map[string]any{"task-only": "task-value", "shared": "task"}
					}
					for i := range artifacts {
						task.Artifacts = append(task.Artifacts, &a2a.Artifact{
							ID: a2a.ArtifactID(fmt.Sprintf("artifact-%d", i)),
							Parts: a2a.ContentParts{&a2a.Part{
								Content: a2a.Text(fmt.Sprintf("reply-%d", i)), Metadata: map[string]any{"part-only": i},
							}},
							Metadata: map[string]any{"artifact-only": i, "shared": "artifact"},
						})
					}
					if inputRequired {
						task.Status = a2a.TaskStatus{State: a2a.TaskStateInputRequired, Message: &a2a.Message{
							ID: "input", Role: a2a.MessageRoleAgent, Parts: a2a.ContentParts{a2a.NewTextPart("question")},
						}}
					}
					a := newTestAgent(&mockA2ATransport{responseToReturn: task}, agent.Config{})
					result, err := a.RunText(t.Context(), "hello").Collect()
					if err != nil {
						t.Fatal(err)
					}
					if !reflect.DeepEqual(result.AdditionalProperties, task.Metadata) {
						t.Errorf("response metadata = %#v, want task metadata %#v", result.AdditionalProperties, task.Metadata)
					}
					var wantRaw any = task
					if artifacts > 0 || inputRequired {
						rawValues := make([]any, 0, artifacts+1)
						for _, artifact := range task.Artifacts {
							rawValues = append(rawValues, artifact)
						}
						if inputRequired {
							rawValues = append(rawValues, task.Status)
						}
						if len(rawValues) == 1 {
							wantRaw = rawValues[0]
						} else {
							wantRaw = rawValues
						}
					}
					if !reflect.DeepEqual(result.RawRepresentation, wantRaw) {
						t.Errorf("response raw = %#v, want collected raw values %#v", result.RawRepresentation, wantRaw)
					}
					if result.ID != string(task.ID) || result.ConversationID == nil || *result.ConversationID != task.ContextID {
						t.Errorf("response IDs = %q, %v, want task and ctx", result.ID, result.ConversationID)
					}
					wantMessages := artifacts
					if inputRequired {
						wantMessages++
					}
					// The existing iterator collector retains one empty lifecycle message.
					if len(result.Messages) != max(1, wantMessages) {
						t.Fatalf("messages = %d, want %d", len(result.Messages), max(1, wantMessages))
					}
					if wantMessages == 0 && result.Messages[0].RawRepresentation != task {
						t.Errorf("empty message raw = %T, want *a2a.Task", result.Messages[0].RawRepresentation)
					}
					for i, artifact := range task.Artifacts {
						msg := result.Messages[i]
						if !reflect.DeepEqual(msg.AdditionalProperties, artifact.Metadata) || msg.RawRepresentation != artifact {
							t.Errorf("artifact message %d metadata/raw = %#v/%T, want only artifact data", i, msg.AdditionalProperties, msg.RawRepresentation)
						}
						if msg.ID != string(artifact.ID) || msg.String() != fmt.Sprintf("reply-%d", i) || len(msg.Contents) != 1 || msg.Contents[0].Header().AdditionalProperties["part-only"] != i {
							t.Errorf("artifact message %d lost identity, content, or part metadata: %#v", i, msg)
						}
						if artifact.Metadata["shared"] != "artifact" {
							t.Error("conversion mutated artifact metadata")
						}
					}
					if inputRequired {
						msg := result.Messages[artifacts]
						raw, ok := msg.RawRepresentation.(a2a.TaskStatus)
						if !ok || raw.State != a2a.TaskStateInputRequired || raw.Message != task.Status.Message {
							t.Errorf("input message raw = %#v, want the task status by value", msg.RawRepresentation)
						}
						if msg.ID != "input" || msg.String() != "question" || msg.AdditionalProperties != nil {
							t.Errorf("input message = %#v, want question without task metadata", msg)
						}
					}
					if metadata && task.Metadata["shared"] != "task" {
						t.Error("conversion mutated task metadata")
					}
				})
			}
		}
	}
}

func TestRunWithTaskResponseScopesSurviveCollectedMiddleware(t *testing.T) {
	task := &a2a.Task{
		ID: "task", ContextID: "ctx", Status: a2a.TaskStatus{State: a2a.TaskStateCompleted},
		Metadata: map[string]any{"shared": "task"},
		Artifacts: []*a2a.Artifact{{
			ID: "artifact", Parts: a2a.ContentParts{a2a.NewTextPart("reply")}, Metadata: map[string]any{"shared": "artifact"},
		}},
	}
	middleware := agent.MiddlewareFunc(func(next agent.RunFunc, ctx context.Context, messages []*message.Message, options ...agent.Option) iter.Seq2[*agent.ResponseUpdate, error] {
		return func(yield func(*agent.ResponseUpdate, error) bool) {
			response, err := agent.ResponseStream(next(ctx, messages, options...)).Collect()
			if err != nil {
				yield(nil, err)
				return
			}
			for _, update := range response.ToUpdates() {
				if !yield(update, nil) {
					return
				}
			}
		}
	})
	a := newTestAgent(&mockA2ATransport{responseToReturn: task}, agent.Config{Middlewares: []agent.Middleware{middleware, middleware}})
	result, err := a.RunText(t.Context(), "hello").Collect()
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(result.AdditionalProperties, task.Metadata) {
		t.Errorf("response metadata = %#v, want task metadata after middleware", result.AdditionalProperties)
	}
	if result.RawRepresentation != task.Artifacts[0] {
		t.Errorf("response raw = %T, want the collected artifact after middleware", result.RawRepresentation)
	}
	if len(result.Messages) != 1 {
		t.Fatalf("messages = %d, want one artifact message", len(result.Messages))
	}
	msg := result.Messages[0]
	if msg.ID != "artifact" || msg.String() != "reply" || !reflect.DeepEqual(msg.AdditionalProperties, task.Artifacts[0].Metadata) || msg.RawRepresentation != task.Artifacts[0] {
		t.Errorf("message = %#v, want unchanged artifact message after middleware", msg)
	}
}

func TestRunWithBackgroundResponsesRequiresSession(t *testing.T) {
	a := newTestAgent(&mockA2ATransport{}, agent.Config{})
	_, err := a.Run(t.Context(), []*message.Message{message.NewText("Test message")}, agent.AllowBackgroundResponses(true)).Collect()
	if err == nil {
		t.Fatal("run without a session succeeded, want error")
	}
}

func TestRunStreamingWithBackgroundResponsesRequiresSession(t *testing.T) {
	a := newTestAgent(&mockA2ATransport{}, agent.Config{})
	var gotErr error
	for _, err := range a.Run(t.Context(), []*message.Message{message.NewText("Test message")}, agent.AllowBackgroundResponses(true), agent.Stream(true)) {
		if err != nil {
			gotErr = err
		}
	}
	if gotErr == nil {
		t.Fatal("streaming run without a session succeeded, want error")
	}
}

func TestRunWithNilRequestMetadata(t *testing.T) {
	transport := &mockA2ATransport{responseToReturn: &a2a.Message{
		ID: "response-123", Role: a2a.MessageRoleAgent,
		Parts: a2a.ContentParts{a2a.NewTextPart("Response")},
	}}
	a := newTestAgent(transport, agent.Config{})
	if _, err := a.Run(t.Context(), []*message.Message{message.NewText("Test message")}, a2a1.WithMetadata(nil)).Collect(); err != nil {
		t.Fatal(err)
	}
	if transport.capturedMessageSendParams == nil {
		t.Fatal("no send request was captured")
	}
	if transport.capturedMessageSendParams.Metadata != nil {
		t.Errorf("request metadata = %#v, want nil", transport.capturedMessageSendParams.Metadata)
	}
}

func assertBlockingSendConfig(t *testing.T, transport *mockA2ATransport) {
	t.Helper()
	if transport.capturedMessageSendParams == nil {
		t.Fatal("no send request was captured")
	}
	config := transport.capturedMessageSendParams.Config
	if config == nil {
		t.Fatal("send configuration is nil, want non-nil blocking configuration")
	}
	if config.ReturnImmediately {
		t.Error("ReturnImmediately = true, want false")
	}
}

func TestRunWithBackgroundResponsesDisabledSetsBlockingConfig(t *testing.T) {
	transport := &mockA2ATransport{}
	a := newTestAgent(transport, agent.Config{})
	if _, err := a.Run(t.Context(), []*message.Message{message.NewText("Test message")}, agent.AllowBackgroundResponses(false)).Collect(); err != nil {
		t.Fatal(err)
	}
	assertBlockingSendConfig(t, transport)
}

func TestRunWithDefaultOptionsSetsBlockingConfig(t *testing.T) {
	transport := &mockA2ATransport{}
	a := newTestAgent(transport, agent.Config{})
	if _, err := a.Run(t.Context(), []*message.Message{message.NewText("Test message")}).Collect(); err != nil {
		t.Fatal(err)
	}
	assertBlockingSendConfig(t, transport)
}

func TestRunWithNilOptionsSetsBlockingConfig(t *testing.T) {
	transport := &mockA2ATransport{}
	a := newTestAgent(transport, agent.Config{})
	var options []agent.Option
	if _, err := a.Run(t.Context(), []*message.Message{message.NewText("Test message")}, options...).Collect(); err != nil {
		t.Fatal(err)
	}
	assertBlockingSendConfig(t, transport)
}

func TestRunStreamingOmitsSendConfig(t *testing.T) {
	transport := &mockA2ATransport{streamingResponseToReturn: &a2a.Message{
		ID: "response-123", Role: a2a.MessageRoleAgent,
		Parts: a2a.ContentParts{a2a.NewTextPart("Streaming response")},
	}}
	a := newTestAgent(transport, agent.Config{})
	for _, err := range a.Run(t.Context(), []*message.Message{message.NewText("Test message")}, agent.Stream(true)) {
		if err != nil {
			t.Fatal(err)
		}
	}
	if transport.capturedMessageSendParams == nil {
		t.Fatal("no send request was captured")
	}
	if transport.capturedMessageSendParams.Config != nil {
		t.Errorf("send configuration = %#v, want nil", transport.capturedMessageSendParams.Config)
	}
}

func TestRunStreamingForwardsRequestMetadata(t *testing.T) {
	transport := &mockA2ATransport{streamingResponseToReturn: &a2a.Message{
		ID: "stream-123", Role: a2a.MessageRoleAgent,
		Parts: a2a.ContentParts{a2a.NewTextPart("Streaming response")},
	}}
	a := newTestAgent(transport, agent.Config{})
	metadata := map[string]any{"streamKey1": "streamValue1", "streamKey2": 100, "streamKey3": false}
	for _, err := range a.Run(t.Context(), []*message.Message{message.NewText("Test streaming message")}, a2a1.WithMetadata(metadata), agent.Stream(true)) {
		if err != nil {
			t.Fatal(err)
		}
	}
	if transport.capturedMessageSendParams == nil {
		t.Fatal("no send request was captured")
	}
	got := transport.capturedMessageSendParams.Metadata
	if got == nil {
		t.Fatal("request metadata is nil")
	}
	if got["streamKey1"] != "streamValue1" || got["streamKey2"] != 100 || got["streamKey3"] != false {
		t.Errorf("request metadata = %#v, want streamValue1, 100 and false", got)
	}
}

func TestRunStreamingWithNilRequestMetadata(t *testing.T) {
	transport := &mockA2ATransport{streamingResponseToReturn: &a2a.Message{
		ID: "stream-123", Role: a2a.MessageRoleAgent,
		Parts: a2a.ContentParts{a2a.NewTextPart("Streaming response")},
	}}
	a := newTestAgent(transport, agent.Config{})
	for _, err := range a.Run(t.Context(), []*message.Message{message.NewText("Test streaming message")}, a2a1.WithMetadata(nil), agent.Stream(true)) {
		if err != nil {
			t.Fatal(err)
		}
	}
	if transport.capturedMessageSendParams == nil {
		t.Fatal("no send request was captured")
	}
	if transport.capturedMessageSendParams.Metadata != nil {
		t.Errorf("request metadata = %#v, want nil", transport.capturedMessageSendParams.Metadata)
	}
}

func TestRunStreamingWithInputRequiredStatusContents(t *testing.T) {
	transport := &mockA2ATransport{streamingResponseToReturn: &a2a.TaskStatusUpdateEvent{
		TaskID: "task-input-123", ContextID: "ctx-input-456",
		Status: a2a.TaskStatus{
			State: a2a.TaskStateInputRequired,
			Message: &a2a.Message{
				ID: "input-msg-789", Parts: a2a.ContentParts{a2a.NewTextPart("Where would you like to fly?")},
			},
		},
	}}
	a := newTestAgent(transport, agent.Config{})
	session, err := a.CreateSession(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	var updates []*agent.ResponseUpdate
	for update, err := range a.RunText(t.Context(), "I'd like to book a flight.", agent.WithSession(session), agent.Stream(true)) {
		if err != nil {
			t.Fatal(err)
		}
		updates = append(updates, update)
	}
	if len(updates) != 1 {
		t.Fatalf("updates = %d, want 1", len(updates))
	}
	update := updates[0]
	if update.ResponseID != "task-input-123" || update.MessageID != "input-msg-789" || update.FinishReason != "" {
		t.Errorf("response ID, message ID and finish reason = %q, %q, %q", update.ResponseID, update.MessageID, update.FinishReason)
	}
	var texts []*message.TextContent
	for _, content := range update.Contents {
		if text, ok := content.(*message.TextContent); ok {
			texts = append(texts, text)
		}
	}
	if len(texts) != 1 {
		t.Fatalf("text contents = %d, want 1", len(texts))
	}
	if texts[0].Text != "Where would you like to fly?" {
		t.Errorf("text = %q, want Where would you like to fly?", texts[0].Text)
	}
}

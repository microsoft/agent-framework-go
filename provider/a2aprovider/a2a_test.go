// Copyright (c) Microsoft. All rights reserved.

package a2aprovider_test

import (
	"context"
	"errors"
	"iter"
	"testing"

	"github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/a2aproject/a2a-go/v2/a2aclient"
	"github.com/microsoft/agent-framework-go/agent"
	"github.com/microsoft/agent-framework-go/internal/agenttest"
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
	subscribeResponseToReturn  a2a.Event
	subscribeErrToReturn       error
	getTaskErrToReturn         error
	sendMessageCalled          bool
	sendStreamingMessageCalled bool
	subscribeToTaskCalled      bool
	getTaskCalled              bool
}

func (m *mockA2ATransport) SendMessage(ctx context.Context, _ a2aclient.ServiceParams, params *a2a.SendMessageRequest) (a2a.SendMessageResult, error) {
	m.sendMessageCalled = true
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
	m.sendStreamingMessageCalled = true
	m.capturedMessageSendParams = params
	responseToYield := m.streamingResponseToReturn
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
	taskIDs := a2a1.TaskIDsFromSession(session)
	if len(taskIDs) == 0 {
		return ""
	}
	return taskIDs[len(taskIDs)-1]
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
		t.Errorf("error = %v, want nil", err)
	}
}

// TestRunWithValidUserMessage tests successful run with valid user message
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
	transport := &mockA2ATransport{}
	a := newTestAgent(transport, agent.Config{})

	session, err := a.CreateSession(t.Context(), agent.WithServiceID("existing-context-id"))
	if err != nil {
		t.Fatal(err)
	}

	_, err = a.RunText(t.Context(), "Test message", agent.WithSession(session)).Collect()
	if err != nil {
		t.Fatalf("error = %v, want nil", err)
	}

	capturedMsg := transport.capturedMessageSendParams.Message
	if capturedMsg == nil {
		t.Fatal("capturedMessageSendParams.Message is nil")
	}
	if capturedMsg.ContextID != "existing-context-id" {
		t.Errorf("message.ContextID = %q, want %q", capturedMsg.ContextID, "existing-context-id")
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
		if cfg := transport.capturedMessageSendParams.Config; cfg != nil {
			t.Errorf("Config = %+v, want nil to preserve the default wire request", cfg)
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

	_, err = a.RunText(t.Context(), "Test message", agent.WithSession(session)).Collect()
	if err == nil {
		t.Error("expected error, got nil")
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
	transport := &mockA2ATransport{
		streamingResponseToReturn: &a2a.Message{},
	}
	a := newTestAgent(transport, agent.Config{})

	session, err := a.CreateSession(t.Context(), agent.WithServiceID("existing-context-id"))
	if err != nil {
		t.Fatal(err)
	}
	for _, err := range a.RunText(t.Context(), "Test streaming", agent.WithSession(session), agent.Stream(true)) {
		if err != nil {
			t.Fatalf("error = %v, want nil", err)
		}
	}

	capturedMsg := transport.capturedMessageSendParams.Message
	if capturedMsg == nil {
		t.Fatal("capturedMessageSendParams.Message is nil")
	}
	if capturedMsg.ContextID != "existing-context-id" {
		t.Errorf("message.ContextID = %q, want %q", capturedMsg.ContextID, "existing-context-id")
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

	gotError := false
	for _, err := range a.RunText(t.Context(), "Test streaming", agent.WithSession(session), agent.Stream(true)) {
		if err != nil {
			gotError = true
			break
		}
	}

	if !gotError {
		t.Error("expected error, got nil")
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

// TestRunWithContinuationTokenAndMessages tests error when both continuation token and messages are provided
func TestRunWithContinuationTokenAndMessages(t *testing.T) {
	transport := &mockA2ATransport{}
	a := newTestAgent(transport, agent.Config{})

	_, err := a.RunText(t.Context(), "Test message", agent.WithContinuationToken(agenttest.NewContinuationToken(t, "task-123"))).Collect()
	if err == nil {
		t.Error("error = nil, want error when continuation token and messages are provided")
	}
}

// TestRunWithContinuationToken tests that continuation token calls GetTask
func TestRunWithContinuationToken(t *testing.T) {
	transport := &mockA2ATransport{
		responseToReturn: &a2a.Task{
			ID:        a2a.TaskID("task-123"),
			ContextID: "context-123",
		},
	}
	a := newTestAgent(transport, agent.Config{})

	_, err := a.Run(t.Context(), nil, agent.WithContinuationToken(agenttest.NewContinuationToken(t, "task-123"))).Collect()
	if err != nil {
		t.Fatalf("error = %v, want nil", err)
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

	session, err := a.CreateSession(t.Context(), a2a1.TaskID("task-123"))
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
	if len(capturedMsg.ReferenceTasks) == 0 {
		t.Error("message.ReferenceTasks is empty, expected task-123")
	} else if string(capturedMsg.ReferenceTasks[0]) != "task-123" {
		t.Errorf("message.ReferenceTasks[0] = %q, want %q", capturedMsg.ReferenceTasks[0], "task-123")
	}
}

func TestRunWithMultipleTaskIDsInSessionAndMessage(t *testing.T) {
	transport := &mockA2ATransport{
		responseToReturn: &a2a.Message{
			ID:    "response-123",
			Role:  a2a.MessageRoleAgent,
			Parts: a2a.ContentParts{a2a.NewTextPart("Response to tasks")},
		},
	}
	a := newTestAgent(transport, agent.Config{})

	session, err := a.CreateSession(t.Context(), a2a1.TaskID("task-123"), a2a1.TaskID("task-456"))
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
	if len(capturedMsg.ReferenceTasks) != 2 {
		t.Fatalf("len(message.ReferenceTasks) = %d, want 2", len(capturedMsg.ReferenceTasks))
	}
	if string(capturedMsg.ReferenceTasks[0]) != "task-123" {
		t.Errorf("message.ReferenceTasks[0] = %q, want %q", capturedMsg.ReferenceTasks[0], "task-123")
	}
	if string(capturedMsg.ReferenceTasks[1]) != "task-456" {
		t.Errorf("message.ReferenceTasks[1] = %q, want %q", capturedMsg.ReferenceTasks[1], "task-456")
	}
}

// TestRunDeduplicatesTaskIDsAcrossTurns tests that repeating the same task ID
// across turns (as happens when every streamed event carries the same task ID)
// stores it only once, so follow-up requests do not accumulate duplicates.
func TestRunDeduplicatesTaskIDsAcrossTurns(t *testing.T) {
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

	for i := 0; i < 3; i++ {
		if _, err := a.RunText(t.Context(), "Do something", agent.WithSession(session)).Collect(); err != nil {
			t.Fatalf("run %d error = %v, want nil", i, err)
		}
	}

	taskIDs := a2a1.TaskIDsFromSession(session)
	if len(taskIDs) != 1 || taskIDs[0] != "task-123" {
		t.Fatalf("TaskIDsFromSession = %v, want [task-123]", taskIDs)
	}
}

// TestRunKeepsDistinctTaskIDs tests that dedup does not collapse distinct task IDs.
func TestRunKeepsDistinctTaskIDs(t *testing.T) {
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

	taskIDs := a2a1.TaskIDsFromSession(session)
	want := []string{"task-123", "task-456"}
	if len(taskIDs) != len(want) || taskIDs[0] != want[0] || taskIDs[1] != want[1] {
		t.Fatalf("TaskIDsFromSession = %v, want %v", taskIDs, want)
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
	if msg.ID != "" {
		t.Errorf("ID = %q, want empty", msg.ID)
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
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			transport := &mockA2ATransport{
				responseToReturn: &a2a.Task{
					ID:        a2a.TaskID("task-123"),
					ContextID: "context-123",
					Status: a2a.TaskStatus{
						State: tt.state,
					},
					Artifacts: []*a2a.Artifact{
						{
							ID:    a2a.ArtifactID("art-1"),
							Parts: a2a.ContentParts{a2a.NewTextPart("Content")},
						},
					},
				},
			}
			a := newTestAgent(transport, agent.Config{})

			result, err := a.RunText(t.Context(), "Test message").Collect()
			if err != nil {
				t.Fatalf("error = %v, want nil", err)
			}

			if tt.expectContinuationToken && result.ContinuationToken == "" {
				t.Error("ContinuationToken is empty, want non-empty")
			} else if !tt.expectContinuationToken && result.ContinuationToken != "" {
				t.Errorf("ContinuationToken = %v, want empty", result.ContinuationToken)
			}
		})
	}
}

// TestRunStreamingWithContinuationTokenAndMessages tests error in streaming mode
func TestRunStreamingWithContinuationTokenAndMessages(t *testing.T) {
	transport := &mockA2ATransport{}
	a := newTestAgent(transport, agent.Config{})

	gotError := false
	for _, err := range a.RunText(t.Context(), "Test message", agent.WithContinuationToken(agenttest.NewContinuationToken(t, "task-123")), agent.Stream(true)) {
		if err != nil {
			gotError = true
			break
		}
	}

	if !gotError {
		t.Error("expected error when continuation token used with streaming, got nil")
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

	if len(updates) != 1 {
		t.Fatalf("len(updates) = %d, want 1", len(updates))
	}
	if !transport.subscribeToTaskCalled {
		t.Fatal("SubscribeToTask was not called")
	}
	if transport.sendStreamingMessageCalled {
		t.Fatal("SendStreamingMessage was called, want SubscribeToTask only")
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
	if _, ok := update.RawRepresentation.(*a2a.Task); !ok {
		t.Errorf("update.RawRepresentation type = %T, want *a2a.Task", update.RawRepresentation)
	}
	if !transport.subscribeToTaskCalled {
		t.Fatal("SubscribeToTask was not called")
	}
	if !transport.getTaskCalled {
		t.Fatal("GetTask was not called after SubscribeToTask fallback")
	}
}

func TestRunStreamingWithContinuationTokenWhenSubscribeFailsWithUnsupportedOperationUpdatesSession(t *testing.T) {
	const taskID = "completed-task-456"
	const contextID = "ctx-completed-456"

	transport := &mockA2ATransport{
		subscribeErrToReturn: a2a.ErrUnsupportedOperation,
		responseToReturn: &a2a.Task{
			ID:        a2a.TaskID(taskID),
			ContextID: contextID,
			Status: a2a.TaskStatus{
				State: a2a.TaskStateCompleted,
			},
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
}

func TestRunStreamingWithContinuationTokenWhenSubscribeFailsWithNonUnsupportedErrorPropagatesWithoutFallback(t *testing.T) {
	transport := &mockA2ATransport{
		subscribeErrToReturn: a2a.ErrTaskNotFound,
	}
	a := newTestAgent(transport, agent.Config{})

	var gotErr error
	for _, err := range a.Run(t.Context(), nil, agent.WithContinuationToken(agenttest.NewContinuationToken(t, "error-task-123")), agent.Stream(true)) {
		if err != nil {
			gotErr = err
			break
		}
	}

	if !errors.Is(gotErr, a2a.ErrTaskNotFound) {
		t.Fatalf("error = %v, want %v", gotErr, a2a.ErrTaskNotFound)
	}
	if !transport.subscribeToTaskCalled {
		t.Fatal("SubscribeToTask was not called")
	}
	if transport.getTaskCalled {
		t.Fatal("GetTask was called, want no fallback for non-unsupported errors")
	}
}

func TestRunStreamingWithContinuationTokenWhenSubscribeAndGetTaskBothFailPropagatesError(t *testing.T) {
	transport := &mockA2ATransport{
		subscribeErrToReturn: a2a.ErrUnsupportedOperation,
		getTaskErrToReturn:   a2a.ErrTaskNotFound,
	}
	a := newTestAgent(transport, agent.Config{})

	var gotErr error
	for _, err := range a.Run(t.Context(), nil, agent.WithContinuationToken(agenttest.NewContinuationToken(t, "failed-task-789")), agent.Stream(true)) {
		if err != nil {
			gotErr = err
			break
		}
	}

	if !errors.Is(gotErr, a2a.ErrTaskNotFound) {
		t.Fatalf("error = %v, want %v", gotErr, a2a.ErrTaskNotFound)
	}
	if !transport.subscribeToTaskCalled {
		t.Fatal("SubscribeToTask was not called")
	}
	if !transport.getTaskCalled {
		t.Fatal("GetTask was not called after SubscribeToTask fallback")
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

	session, err := a.CreateSession(t.Context(), a2a1.TaskID("task-123"))
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
	if len(capturedMsg.ReferenceTasks) == 0 {
		t.Error("message.ReferenceTasks is empty, expected task-123")
	} else if string(capturedMsg.ReferenceTasks[0]) != "task-123" {
		t.Errorf("message.ReferenceTasks[0] = %q, want %q", capturedMsg.ReferenceTasks[0], "task-123")
	}
}

// TestRunStreamingWithAgentTaskUpdatesSession tests session task ID update in streaming
func TestRunStreamingWithAgentTaskUpdatesSession(t *testing.T) {
	transport := &mockA2ATransport{
		streamingResponseToReturn: &a2a.Task{
			ID:        a2a.TaskID("task-456"),
			ContextID: "context-789",
			Status: a2a.TaskStatus{
				State: a2a.TaskStateSubmitted,
			},
			Artifacts: []*a2a.Artifact{
				{
					ID:    a2a.ArtifactID("art-1"),
					Parts: a2a.ContentParts{a2a.NewTextPart("Task content")},
				},
			},
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

	if got := latestTaskID(session); got != "task-456" {
		t.Errorf("session.TaskID = %q, want %q", got, "task-456")
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
	if _, ok := update.RawRepresentation.(*a2a.Message); !ok {
		t.Errorf("update.RawRepresentation type = %T, want *a2a.Message", update.RawRepresentation)
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
	if _, ok := update.RawRepresentation.(*a2a.Task); !ok {
		t.Errorf("update.RawRepresentation type = %T, want *a2a.Task", update.RawRepresentation)
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
		if len(update.Contents) != 0 {
			t.Errorf("update.Contents = %v, want empty (Working state should not populate contents)", update.Contents)
		}
		if got := update.String(); got != "" {
			t.Errorf("update.String() = %q, want empty (Working state should not populate contents)", got)
		}
	})
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

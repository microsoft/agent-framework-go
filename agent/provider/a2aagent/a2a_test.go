// Copyright (c) Microsoft. All rights reserved.

package a2aagent_test

import (
	"context"
	"errors"
	"iter"
	"testing"

	"github.com/a2aproject/a2a-go/a2a"
	"github.com/a2aproject/a2a-go/a2aclient"
	"github.com/microsoft/agent-framework-go/agent"
	a2a1 "github.com/microsoft/agent-framework-go/agent/provider/a2aagent"
	"github.com/microsoft/agent-framework-go/agentopt"
	"github.com/microsoft/agent-framework-go/memory"
	"github.com/microsoft/agent-framework-go/message"
)

// mockA2ATransport is a stub that implements a2aclient.Transport for testing
type mockA2ATransport struct {
	capturedMessageSendParams  *a2a.MessageSendParams
	responseToReturn           a2a.SendMessageResult
	streamingResponseToReturn  a2a.Event
	sendMessageCalled          bool
	sendStreamingMessageCalled bool
}

func (m *mockA2ATransport) SendMessage(ctx context.Context, params *a2a.MessageSendParams) (a2a.SendMessageResult, error) {
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

func (m *mockA2ATransport) SendStreamingMessage(ctx context.Context, params *a2a.MessageSendParams) iter.Seq2[a2a.Event, error] {
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

func (m *mockA2ATransport) GetTask(ctx context.Context, params *a2a.TaskQueryParams) (*a2a.Task, error) {
	if m.responseToReturn != nil {
		if task, ok := m.responseToReturn.(*a2a.Task); ok {
			return task, nil
		}
	}
	return nil, errors.New("not implemented")
}

func (m *mockA2ATransport) CancelTask(ctx context.Context, params *a2a.TaskIDParams) (*a2a.Task, error) {
	return nil, errors.New("not implemented")
}

func (m *mockA2ATransport) ResubscribeToTask(ctx context.Context, params *a2a.TaskIDParams) iter.Seq2[a2a.Event, error] {
	return func(yield func(a2a.Event, error) bool) {
		yield(nil, errors.New("not implemented"))
	}
}

func (m *mockA2ATransport) GetTaskPushConfig(ctx context.Context, params *a2a.GetTaskPushConfigParams) (*a2a.TaskPushConfig, error) {
	return nil, errors.New("not implemented")
}

func (m *mockA2ATransport) ListTaskPushConfig(ctx context.Context, params *a2a.ListTaskPushConfigParams) ([]*a2a.TaskPushConfig, error) {
	return nil, errors.New("not implemented")
}

func (m *mockA2ATransport) SetTaskPushConfig(ctx context.Context, config *a2a.TaskPushConfig) (*a2a.TaskPushConfig, error) {
	return nil, errors.New("not implemented")
}

func (m *mockA2ATransport) DeleteTaskPushConfig(ctx context.Context, params *a2a.DeleteTaskPushConfigParams) error {
	return errors.New("not implemented")
}

func (m *mockA2ATransport) GetAgentCard(ctx context.Context) (*a2a.AgentCard, error) {
	return nil, errors.New("not implemented")
}

func (m *mockA2ATransport) Destroy() error {
	return nil
}

// Test fixtures
func newTestAgent(transport a2aclient.Transport, config agent.Config) *agent.Agent {
	card := &a2a.AgentCard{
		URL:                "test://localhost",
		PreferredTransport: "test",
		Capabilities: a2a.AgentCapabilities{
			Streaming: true,
		},
	}
	client, err := a2aclient.NewFromCard(
		context.Background(),
		card,
		a2aclient.WithDefaultsDisabled(),
		a2aclient.WithTransport("test", a2aclient.TransportFactoryFn(func(ctx context.Context, url string, card *a2a.AgentCard) (a2aclient.Transport, error) {
			return transport, nil
		})),
	)
	if err != nil {
		panic(err)
	}
	return a2a1.New(client, a2a1.Config{Config: config})
}

func latestTaskID(session *memory.Session) string {
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
	a2a1.New(nil, a2a1.Config{})
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
			ID:   "response-123",
			Role: a2a.MessageRoleAgent,
			Parts: []a2a.Part{
				a2a.TextPart{Text: "Hello! How can I help you today?"},
			},
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
	if textPart, ok := inputMessage.Parts[0].(a2a.TextPart); ok {
		if textPart.Text != "Hello, world!" {
			t.Errorf("captured message text = %q, want %q", textPart.Text, "Hello, world!")
		}
	} else {
		t.Errorf("captured message part is not TextPart")
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

// TestRunWithCreateSession tests that new session updates context ID
func TestRunWithCreateSession(t *testing.T) {
	transport := &mockA2ATransport{
		responseToReturn: &a2a.Message{
			ID:        "response-123",
			Role:      a2a.MessageRoleAgent,
			Parts:     []a2a.Part{a2a.TextPart{Text: "Response"}},
			ContextID: "new-context-id",
		},
	}
	a := newTestAgent(transport, agent.Config{})

	session, err := a.CreateSession(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	_, err = a.RunText(t.Context(), "Test message", agentopt.Session(session)).Collect()
	if err != nil {
		t.Fatalf("error = %v, want nil", err)
	}

	if got := session.ServiceID; got != "new-context-id" {
		t.Errorf("session.ServiceID = %q, want %q", got, "new-context-id")
	}
}

// TestRunWithExistingSession tests that existing session context ID is used
func TestRunWithExistingSession(t *testing.T) {
	transport := &mockA2ATransport{}
	a := newTestAgent(transport, agent.Config{})

	session, err := a.CreateSession(t.Context(), agentopt.ServiceID("existing-context-id"))
	if err != nil {
		t.Fatal(err)
	}

	_, err = a.RunText(t.Context(), "Test message", agentopt.Session(session)).Collect()
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

// TestRunWithSessionHavingDifferentContextID tests error when context ID mismatch
func TestRunWithSessionHavingDifferentContextID(t *testing.T) {
	transport := &mockA2ATransport{
		responseToReturn: &a2a.Message{
			ID:        "response-123",
			Role:      a2a.MessageRoleAgent,
			Parts:     []a2a.Part{a2a.TextPart{Text: "Response"}},
			ContextID: "different-context",
		},
	}
	a := newTestAgent(transport, agent.Config{})

	session, err := a.CreateSession(t.Context(), agentopt.ServiceID("existing-context-id"))
	if err != nil {
		t.Fatal(err)
	}

	_, err = a.RunText(t.Context(), "Test message", agentopt.Session(session)).Collect()
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
			Parts:     []a2a.Part{a2a.TextPart{Text: "Hello"}},
			ContextID: "stream-context",
		},
	}
	a := newTestAgent(transport, agent.Config{})

	var updates []*message.ResponseUpdate
	for update, err := range a.RunText(t.Context(), "Hello, streaming!", agentopt.Stream(true)) {
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
	if textPart, ok := inputMessage.Parts[0].(a2a.TextPart); ok {
		if textPart.Text != "Hello, streaming!" {
			t.Errorf("captured message text = %q, want %q", textPart.Text, "Hello, streaming!")
		}
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
			Parts:     []a2a.Part{a2a.TextPart{Text: "Response"}},
			ContextID: "new-stream-context",
		},
	}
	a := newTestAgent(transport, agent.Config{})

	session, err := a.CreateSession(t.Context())
	if err != nil {
		t.Fatal(err)
	}

	for _, err := range a.RunText(t.Context(), "Test streaming", agentopt.Session(session), agentopt.Stream(true)) {
		if err != nil {
			t.Fatalf("error = %v, want nil", err)
		}
	}

	if got := session.ServiceID; got != "new-stream-context" {
		t.Errorf("session.ContextID = %q, want %q", got, "new-stream-context")
	}
}

// TestRunStreamingWithExistingSession tests streaming with existing session
func TestRunStreamingWithExistingSession(t *testing.T) {
	transport := &mockA2ATransport{
		streamingResponseToReturn: &a2a.Message{},
	}
	a := newTestAgent(transport, agent.Config{})

	session, err := a.CreateSession(t.Context(), agentopt.ServiceID("existing-context-id"))
	if err != nil {
		t.Fatal(err)
	}
	for _, err := range a.RunText(t.Context(), "Test streaming", agentopt.Session(session), agentopt.Stream(true)) {
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
			Parts:     []a2a.Part{a2a.TextPart{Text: "Response"}},
			ContextID: "different-context",
		},
	}
	a := newTestAgent(transport, agent.Config{})

	session, err := a.CreateSession(t.Context(), agentopt.ServiceID("existing-context-id"))
	if err != nil {
		t.Fatal(err)
	}

	gotError := false
	for _, err := range a.RunText(t.Context(), "Test streaming", agentopt.Session(session), agentopt.Stream(true)) {
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
			Parts:     []a2a.Part{a2a.TextPart{Text: "Response"}},
			ContextID: "new-stream-context",
		},
	}
	a := newTestAgent(transport, agent.Config{})

	inputMessages := []*message.Message{
		{Role: message.RoleSystem, Contents: []message.Content{&message.TextContent{Text: "I am a system message"}}},
		{Role: message.RoleAssistant, Contents: []message.Content{&message.TextContent{Text: "I am an assistant message"}}},
		{Role: message.RoleUser, Contents: []message.Content{&message.TextContent{Text: "Valid user message"}}},
	}

	for _, err := range a.Run(t.Context(), inputMessages, agentopt.Stream(true)) {
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

	if textPart, ok := capturedMsg.Parts[0].(a2a.TextPart); !ok {
		t.Errorf("Parts[0] type = %T, want a2a.TextPart", capturedMsg.Parts[0])
	} else if textPart.Text != "Check this file:" {
		t.Errorf("TextPart.Text = %q, want %q", textPart.Text, "Check this file:")
	}

	if filePart, ok := capturedMsg.Parts[1].(a2a.FilePart); !ok {
		t.Errorf("Parts[1] type = %T, want a2a.FilePart", capturedMsg.Parts[1])
	} else {
		if fileURI, ok := filePart.File.(a2a.FileURI); !ok {
			t.Errorf("FilePart.File type = %T, want a2a.FileURI", filePart.File)
		} else {
			expectedURI := "https://example.com/file.pdf"
			if fileURI.URI != expectedURI {
				t.Errorf("FileURI.URI = %q, want %q", fileURI.URI, expectedURI)
			}
		}
	}
}

// TestRunWithContinuationTokenAndMessages tests error when both continuation token and messages are provided
func TestRunWithContinuationTokenAndMessages(t *testing.T) {
	transport := &mockA2ATransport{}
	a := newTestAgent(transport, agent.Config{})

	_, err := a.RunText(t.Context(), "Test message", agentopt.ContinuationToken("task-123")).Collect()
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

	_, err := a.Run(t.Context(), nil, agentopt.ContinuationToken("task-123")).Collect()
	if err != nil {
		t.Fatalf("error = %v, want nil", err)
	}
}

// TestRunWithTaskInSessionAndMessage tests that task ID is added as reference
func TestRunWithTaskInSessionAndMessage(t *testing.T) {
	transport := &mockA2ATransport{
		responseToReturn: &a2a.Message{
			ID:   "response-123",
			Role: a2a.MessageRoleAgent,
			Parts: []a2a.Part{
				a2a.TextPart{Text: "Response to task"},
			},
		},
	}
	a := newTestAgent(transport, agent.Config{})

	session, err := a.CreateSession(t.Context(), a2a1.TaskID("task-123"))
	if err != nil {
		t.Fatal(err)
	}

	_, err = a.RunText(t.Context(), "Please make the background transparent", agentopt.Session(session)).Collect()
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
			ID:   "response-123",
			Role: a2a.MessageRoleAgent,
			Parts: []a2a.Part{
				a2a.TextPart{Text: "Response to tasks"},
			},
		},
	}
	a := newTestAgent(transport, agent.Config{})

	session, err := a.CreateSession(t.Context(), a2a1.TaskID("task-123"), a2a1.TaskID("task-456"))
	if err != nil {
		t.Fatal(err)
	}

	_, err = a.RunText(t.Context(), "Please make the background transparent", agentopt.Session(session)).Collect()
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

	_, err = a.RunText(t.Context(), "Start a task", agentopt.Session(session)).Collect()
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
					ID: a2a.ArtifactID("art-1"),
					Parts: []a2a.Part{
						a2a.TextPart{Text: "Artifact content"},
					},
				},
			},
		},
	}
	a := newTestAgent(transport, agent.Config{})

	session, err := a.CreateSession(t.Context())
	if err != nil {
		t.Fatal(err)
	}

	result, err := a.RunText(t.Context(), "Start a long-running task", agentopt.Session(session)).Collect()
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

	if result.ContinuationToken != "task-789" {
		t.Errorf("continuation token = %q, want %q", result.ContinuationToken, "task-789")
	}

	if got := session.ServiceID; got != "context-456" {
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
							Parts: []a2a.Part{a2a.TextPart{Text: "Content"}},
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
	for _, err := range a.RunText(t.Context(), "Test message", agentopt.ContinuationToken("task-123"), agentopt.Stream(true)) {
		if err != nil {
			gotError = true
			break
		}
	}

	if !gotError {
		t.Error("expected error when continuation token used with streaming, got nil")
	}
}

// TestRunStreamingWithTaskInSessionAndMessage tests task reference in streaming
func TestRunStreamingWithTaskInSessionAndMessage(t *testing.T) {
	transport := &mockA2ATransport{
		streamingResponseToReturn: &a2a.Message{
			ID:   "response-123",
			Role: a2a.MessageRoleAgent,
			Parts: []a2a.Part{
				a2a.TextPart{Text: "Response to task"},
			},
		},
	}
	a := newTestAgent(transport, agent.Config{})

	session, err := a.CreateSession(t.Context(), a2a1.TaskID("task-123"))
	if err != nil {
		t.Fatal(err)
	}

	for _, err := range a.RunText(t.Context(), "Please make the background transparent", agentopt.Session(session), agentopt.Stream(true)) {
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
					Parts: []a2a.Part{a2a.TextPart{Text: "Task content"}},
				},
			},
		},
	}
	a := newTestAgent(transport, agent.Config{})

	session, err := a.CreateSession(t.Context())
	if err != nil {
		t.Fatal(err)
	}

	for _, err := range a.RunText(t.Context(), "Start a task", agentopt.Session(session), agentopt.Stream(true)) {
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
			Parts: []a2a.Part{
				a2a.TextPart{Text: messageText},
			},
		},
	}
	a := newTestAgent(transport, agent.Config{})

	var updates []*message.ResponseUpdate
	for update, err := range a.RunText(t.Context(), "Test message", agentopt.Stream(true)) {
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
					ID: a2a.ArtifactID("art-123"),
					Parts: []a2a.Part{
						a2a.TextPart{Text: "Task artifact content"},
					},
				},
			},
		},
	}
	a := newTestAgent(transport, agent.Config{})

	session, err := a.CreateSession(t.Context())
	if err != nil {
		t.Fatal(err)
	}

	var updates []*message.ResponseUpdate
	for update, err := range a.RunText(t.Context(), "Start long-running task", agentopt.Session(session), agentopt.Stream(true)) {
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

	if got := session.ServiceID; got != contextID {
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

	var updates []*message.ResponseUpdate
	for update, err := range a.RunText(t.Context(), "Check task status", agentopt.Session(session), agentopt.Stream(true)) {
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
	if _, ok := update.RawRepresentation.(*a2a.TaskStatusUpdateEvent); !ok {
		t.Errorf("update.RawRepresentation type = %T, want *a2a.TaskStatusUpdateEvent", update.RawRepresentation)
	}

	if got := session.ServiceID; got != contextID {
		t.Errorf("session.ContextID = %q, want %q", got, contextID)
	}
	if got := latestTaskID(session); got != taskID {
		t.Errorf("session.TaskID = %q, want %q", got, taskID)
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
				ID: a2a.ArtifactID("artifact-789"),
				Parts: []a2a.Part{
					a2a.TextPart{Text: artifactContent},
				},
			},
		},
	}
	a := newTestAgent(transport, agent.Config{})

	session, err := a.CreateSession(t.Context())
	if err != nil {
		t.Fatal(err)
	}

	var updates []*message.ResponseUpdate
	for update, err := range a.RunText(t.Context(), "Process artifact", agentopt.Session(session), agentopt.Stream(true)) {
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

	if got := session.ServiceID; got != contextID {
		t.Errorf("session.ContextID = %q, want %q", got, contextID)
	}
	if got := latestTaskID(session); got != taskID {
		t.Errorf("session.TaskID = %q, want %q", got, taskID)
	}
}

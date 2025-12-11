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
	"github.com/microsoft/agent-framework-go/agent/a2aagent"
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
func newTestAgent(transport a2aclient.Transport, opts a2aagent.Options) *a2aagent.Agent {
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
	return a2aagent.NewAgent(client, opts)
}

// TestConstructorWithAllParameters tests that the constructor initializes all properties correctly
func TestConstructorWithAllParameters(t *testing.T) {
	testID := "test-id"
	testName := "test-name"
	testDescription := "test-description"
	testDisplayName := "test-display-name"

	transport := &mockA2ATransport{}
	opts := a2aagent.Options{
		ID:          testID,
		Name:        testName,
		Description: testDescription,
		DisplayName: testDisplayName,
	}
	a := newTestAgent(transport, opts)

	if a.Identity().ID() != testID {
		t.Errorf("ID() = %q, want %q", a.Identity().ID(), testID)
	}
	if a.Identity().Name() != testName {
		t.Errorf("Name() = %q, want %q", a.Identity().Name(), testName)
	}
	if a.Identity().Description() != testDescription {
		t.Errorf("Description() = %q, want %q", a.Identity().Description(), testDescription)
	}
}

// TestConstructorWithNilClient tests that nil client is handled
func TestConstructorWithNilClient(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Error("Expected panic when client is nil")
		}
	}()
	a2aagent.NewAgent(nil, a2aagent.Options{})
}

// TestConstructorWithDefaultParameters tests default parameter behavior
func TestConstructorWithDefaultParameters(t *testing.T) {
	transport := &mockA2ATransport{}
	a := newTestAgent(transport, a2aagent.Options{})

	id := a.Identity().ID()
	if id == "" {
		t.Error("ID() returned empty string, want non-empty")
	}
	if a.Identity().Name() != "" {
		t.Errorf("Name() = %q, want empty string", a.Identity().Name())
	}
	if a.Identity().Description() != "" {
		t.Errorf("Description() = %q, want empty string", a.Identity().Description())
	}
}

// TestRunAllowsNonUserRoleMessages tests that non-user role messages are accepted
func TestRunAllowsNonUserRoleMessages(t *testing.T) {
	transport := &mockA2ATransport{}
	a := newTestAgent(transport, a2aagent.Options{})

	inputMessages := []agent.Option{
		agent.WithMessage(&message.Message{Role: message.RoleSystem, Contents: []message.Content{&message.TextContent{Text: "I am a system message"}}}),
		agent.WithMessage(&message.Message{Role: message.RoleAssistant, Contents: []message.Content{&message.TextContent{Text: "I am an assistant message"}}}),
		agent.WithMessage(&message.Message{Role: message.RoleUser, Contents: []message.Content{&message.TextContent{Text: "Valid user message"}}}),
	}

	_, err := agent.Run(t.Context(), a, inputMessages...)
	if err != nil {
		t.Errorf("Run() error = %v, want nil", err)
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
	a := newTestAgent(transport, a2aagent.Options{})

	result, err := agent.RunText(t.Context(), a, "Hello, world!")
	if err != nil {
		t.Fatalf("Run() error = %v, want nil", err)
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
	if result.AgentID != a.Identity().ID() {
		t.Errorf("result.AgentID = %q, want %q", result.AgentID, a.Identity().ID())
	}
	if result.ID != "response-123" {
		t.Errorf("result.ID = %q, want %q", result.ID, "response-123")
	}

	if result.RawRepresentation == nil {
		t.Fatal("result.RawRepresentation is nil")
	}
	if _, ok := result.RawRepresentation.(*a2a.Message); !ok {
		t.Errorf("result.RawRepresentation type = %T, want *a2a.Message", result.RawRepresentation)
	}
	if rawMsg, ok := result.RawRepresentation.(*a2a.Message); ok {
		if rawMsg.ID != "response-123" {
			t.Errorf("raw message ID = %q, want %q", rawMsg.ID, "response-123")
		}
	}

	if len(result.Messages) != 1 {
		t.Fatalf("len(result.Messages) = %d, want 1", len(result.Messages))
	}
	if result.Messages[0].Role != message.RoleAssistant {
		t.Errorf("result.Messages[0].Role = %q, want %q", result.Messages[0].Role, message.RoleAssistant)
	}
	if result.Messages[0].Text() != "Hello! How can I help you today?" {
		t.Errorf("result.Messages[0].Text() = %q, want %q", result.Messages[0].Text(), "Hello! How can I help you today?")
	}
}

// TestRunWithNewThread tests that new thread updates context ID
func TestRunWithNewThread(t *testing.T) {
	transport := &mockA2ATransport{
		responseToReturn: &a2a.Message{
			ID:        "response-123",
			Role:      a2a.MessageRoleAgent,
			Parts:     []a2a.Part{a2a.TextPart{Text: "Response"}},
			ContextID: "new-context-id",
		},
	}
	a := newTestAgent(transport, a2aagent.Options{})

	thread := a.NewThread()
	_, err := agent.RunText(t.Context(), a, "Test message", agent.WithThread(thread))
	if err != nil {
		t.Fatalf("Run() error = %v, want nil", err)
	}

	a2aThread, ok := thread.(*a2aagent.Thread)
	if !ok {
		t.Fatalf("thread type = %T, want *a2aagent.Thread", thread)
	}
	if a2aThread.ContextID != "new-context-id" {
		t.Errorf("thread.ContextID = %q, want %q", a2aThread.ContextID, "new-context-id")
	}
}

// TestRunWithExistingThread tests that existing thread context ID is used
func TestRunWithExistingThread(t *testing.T) {
	transport := &mockA2ATransport{}
	a := newTestAgent(transport, a2aagent.Options{})

	thread := a.NewThreadWithContextID("existing-context-id")

	_, err := agent.RunText(t.Context(), a, "Test message", agent.WithThread(thread))
	if err != nil {
		t.Fatalf("Run() error = %v, want nil", err)
	}

	capturedMsg := transport.capturedMessageSendParams.Message
	if capturedMsg == nil {
		t.Fatal("capturedMessageSendParams.Message is nil")
	}
	if capturedMsg.ContextID != "existing-context-id" {
		t.Errorf("message.ContextID = %q, want %q", capturedMsg.ContextID, "existing-context-id")
	}
}

// TestRunWithThreadHavingDifferentContextID tests error when context ID mismatch
func TestRunWithThreadHavingDifferentContextID(t *testing.T) {
	transport := &mockA2ATransport{
		responseToReturn: &a2a.Message{
			ID:        "response-123",
			Role:      a2a.MessageRoleAgent,
			Parts:     []a2a.Part{a2a.TextPart{Text: "Response"}},
			ContextID: "different-context",
		},
	}
	a := newTestAgent(transport, a2aagent.Options{})

	thread := a.NewThreadWithContextID("existing-context-id")

	_, err := agent.RunText(t.Context(), a, "Test message", agent.WithThread(thread))
	if err == nil {
		t.Error("Run() error = nil, want error")
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
	a := newTestAgent(transport, a2aagent.Options{})

	var updates []*agent.RunResponseUpdate
	for update, err := range agent.RunTextStream(t.Context(), a, "Hello, streaming!") {
		if err != nil {
			t.Fatalf("RunStream() error = %v, want nil", err)
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
	if update.AgentID != a.Identity().ID() {
		t.Errorf("update.AgentID = %q, want %q", update.AgentID, a.Identity().ID())
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

// TestRunStreamingWithThread tests streaming with thread context ID update
func TestRunStreamingWithThread(t *testing.T) {
	transport := &mockA2ATransport{
		streamingResponseToReturn: &a2a.Message{
			ID:        "stream-1",
			Role:      a2a.MessageRoleAgent,
			Parts:     []a2a.Part{a2a.TextPart{Text: "Response"}},
			ContextID: "new-stream-context",
		},
	}
	a := newTestAgent(transport, a2aagent.Options{})

	thread := a.NewThread()

	for _, err := range agent.RunTextStream(t.Context(), a, "Test streaming", agent.WithThread(thread)) {
		if err != nil {
			t.Fatalf("RunStream() error = %v, want nil", err)
		}
	}

	a2aThread, ok := thread.(*a2aagent.Thread)
	if !ok {
		t.Fatalf("thread type = %T, want *a2aagent.Thread", thread)
	}
	if a2aThread.ContextID != "new-stream-context" {
		t.Errorf("thread.ContextID = %q, want %q", a2aThread.ContextID, "new-stream-context")
	}
}

// TestRunStreamingWithExistingThread tests streaming with existing thread
func TestRunStreamingWithExistingThread(t *testing.T) {
	transport := &mockA2ATransport{
		streamingResponseToReturn: &a2a.Message{},
	}
	a := newTestAgent(transport, a2aagent.Options{})

	thread := a.NewThreadWithContextID("existing-context-id")

	for _, err := range agent.RunTextStream(t.Context(), a, "Test streaming", agent.WithThread(thread)) {
		if err != nil {
			t.Fatalf("RunStream() error = %v, want nil", err)
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

// TestRunStreamingWithThreadHavingDifferentContextID tests error on context ID mismatch in streaming
func TestRunStreamingWithThreadHavingDifferentContextID(t *testing.T) {
	transport := &mockA2ATransport{
		streamingResponseToReturn: &a2a.Message{
			ID:        "stream-1",
			Role:      a2a.MessageRoleAgent,
			Parts:     []a2a.Part{a2a.TextPart{Text: "Response"}},
			ContextID: "different-context",
		},
	}
	a := newTestAgent(transport, a2aagent.Options{})

	thread := a.NewThreadWithContextID("existing-context-id")

	gotError := false
	for _, err := range agent.RunTextStream(t.Context(), a, "Test streaming", agent.WithThread(thread)) {
		if err != nil {
			gotError = true
			break
		}
	}

	if !gotError {
		t.Error("RunStream() expected error, got nil")
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
	a := newTestAgent(transport, a2aagent.Options{})

	inputMessages := []agent.Option{
		agent.WithMessage(&message.Message{Role: message.RoleSystem, Contents: []message.Content{&message.TextContent{Text: "I am a system message"}}}),
		agent.WithMessage(&message.Message{Role: message.RoleAssistant, Contents: []message.Content{&message.TextContent{Text: "I am an assistant message"}}}),
		agent.WithMessage(&message.Message{Role: message.RoleUser, Contents: []message.Content{&message.TextContent{Text: "Valid user message"}}}),
	}

	for _, err := range agent.RunStream(t.Context(), a, inputMessages...) {
		if err != nil {
			t.Fatalf("RunStream() error = %v, want nil", err)
		}
	}
}

// TestRunWithHostedFileContent tests conversion of hosted file content to file part
func TestRunWithHostedFileContent(t *testing.T) {
	transport := &mockA2ATransport{}
	a := newTestAgent(transport, a2aagent.Options{})

	inputMessages := []agent.Option{
		agent.WithMessage(&message.Message{
			Role: message.RoleUser,
			Contents: []message.Content{
				&message.TextContent{Text: "Check this file:"},
				&message.URIContent{
					URI:       "https://example.com/file.pdf",
					MediaType: "application/pdf",
				},
			},
		}),
	}

	_, err := agent.Run(t.Context(), a, inputMessages...)
	if err != nil {
		t.Fatalf("Run() error = %v, want nil", err)
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

// TestRunWithInvalidThreadType tests error when using invalid thread type
func TestRunWithInvalidThreadType(t *testing.T) {
	transport := &mockA2ATransport{}
	a := newTestAgent(transport, a2aagent.Options{})

	// Create a custom thread type that is not an a2aagent.Thread
	invalidThread := &customAgentThread{}

	_, err := agent.RunText(t.Context(), a, "Test message", agent.WithThread(invalidThread))
	if err == nil {
		t.Error("Run() error = nil, want error for invalid thread type")
	}
}

// TestRunStreamingWithInvalidThreadType tests error when using invalid thread type in streaming
func TestRunStreamingWithInvalidThreadType(t *testing.T) {
	transport := &mockA2ATransport{
		streamingResponseToReturn: &a2a.Message{
			ID:   "stream-1",
			Role: a2a.MessageRoleAgent,
			Parts: []a2a.Part{
				a2a.TextPart{Text: "Response"},
			},
		},
	}
	a := newTestAgent(transport, a2aagent.Options{})

	// Create a custom thread type that is not an a2aagent.Thread
	invalidThread := &customAgentThread{}

	gotError := false
	for _, err := range agent.RunTextStream(t.Context(), a, "Test message", agent.WithThread(invalidThread)) {
		if err != nil {
			gotError = true
			break
		}
	}

	if !gotError {
		t.Error("RunStream() expected error for invalid thread type, got nil")
	}
}

// TestRunWithContinuationTokenAndMessages tests error when both continuation token and messages are provided
func TestRunWithContinuationTokenAndMessages(t *testing.T) {
	transport := &mockA2ATransport{}
	a := newTestAgent(transport, a2aagent.Options{})

	_, err := agent.RunText(t.Context(), a, "Test message", agent.WithContinuationToken(a2a.TaskID("task-123")))
	if err == nil {
		t.Error("Run() error = nil, want error when continuation token and messages are provided")
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
	a := newTestAgent(transport, a2aagent.Options{})

	_, err := agent.Run(t.Context(), a, agent.WithContinuationToken(a2a.TaskID("task-123")))
	if err != nil {
		t.Fatalf("Run() error = %v, want nil", err)
	}
}

// TestRunWithTaskInThreadAndMessage tests that task ID is added as reference
func TestRunWithTaskInThreadAndMessage(t *testing.T) {
	transport := &mockA2ATransport{
		responseToReturn: &a2a.Message{
			ID:   "response-123",
			Role: a2a.MessageRoleAgent,
			Parts: []a2a.Part{
				a2a.TextPart{Text: "Response to task"},
			},
		},
	}
	a := newTestAgent(transport, a2aagent.Options{})

	thread := a.NewThread().(*a2aagent.Thread)
	thread.TaskID = "task-123"

	_, err := agent.RunText(t.Context(), a, "Please make the background transparent", agent.WithThread(thread))
	if err != nil {
		t.Fatalf("Run() error = %v, want nil", err)
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

// TestRunWithAgentTask tests that thread task ID is updated
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
	a := newTestAgent(transport, a2aagent.Options{})

	thread := a.NewThread()

	_, err := agent.RunText(t.Context(), a, "Start a task", agent.WithThread(thread))
	if err != nil {
		t.Fatalf("Run() error = %v, want nil", err)
	}

	a2aThread, ok := thread.(*a2aagent.Thread)
	if !ok {
		t.Fatalf("thread type = %T, want *a2aagent.Thread", thread)
	}
	if a2aThread.TaskID != "task-456" {
		t.Errorf("thread.TaskID = %q, want %q", a2aThread.TaskID, "task-456")
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
	a := newTestAgent(transport, a2aagent.Options{})

	thread := a.NewThread()

	result, err := agent.RunText(t.Context(), a, "Start a long-running task", agent.WithThread(thread))
	if err != nil {
		t.Fatalf("Run() error = %v, want nil", err)
	}

	if result == nil {
		t.Fatal("result is nil")
	}
	if result.AgentID != a.Identity().ID() {
		t.Errorf("result.AgentID = %q, want %q", result.AgentID, a.Identity().ID())
	}
	if result.ID != "task-789" {
		t.Errorf("result.ID = %q, want %q", result.ID, "task-789")
	}

	if result.ContinuationToken == nil {
		t.Error("result.ContinuationToken is nil, want non-nil for submitted task")
	} else if token, ok := result.ContinuationToken.(a2a.TaskID); !ok {
		t.Errorf("result.ContinuationToken type = %T, want a2a.TaskID", result.ContinuationToken)
	} else if string(token) != "task-789" {
		t.Errorf("continuation token = %q, want %q", token, "task-789")
	}

	a2aThread, ok := thread.(*a2aagent.Thread)
	if !ok {
		t.Fatalf("thread type = %T, want *a2aagent.Thread", thread)
	}
	if a2aThread.ContextID != "context-456" {
		t.Errorf("thread.ContextID = %q, want %q", a2aThread.ContextID, "context-456")
	}
	if a2aThread.TaskID != "task-789" {
		t.Errorf("thread.TaskID = %q, want %q", a2aThread.TaskID, "task-789")
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
			a := newTestAgent(transport, a2aagent.Options{})

			result, err := agent.RunText(t.Context(), a, "Test message")
			if err != nil {
				t.Fatalf("Run() error = %v, want nil", err)
			}

			if tt.expectContinuationToken {
				if result.ContinuationToken == nil {
					t.Error("result.ContinuationToken is nil, want non-nil")
				}
			} else {
				if result.ContinuationToken != nil {
					t.Errorf("result.ContinuationToken = %v, want nil", result.ContinuationToken)
				}
			}
		})
	}
}

// TestRunStreamingWithContinuationTokenAndMessages tests error in streaming mode
func TestRunStreamingWithContinuationTokenAndMessages(t *testing.T) {
	transport := &mockA2ATransport{}
	a := newTestAgent(transport, a2aagent.Options{})

	gotError := false
	for _, err := range agent.RunTextStream(t.Context(), a, "Test message", agent.WithContinuationToken(a2a.TaskID("task-123"))) {
		if err != nil {
			gotError = true
			break
		}
	}

	if !gotError {
		t.Error("RunStream() expected error when continuation token used with streaming, got nil")
	}
}

// TestRunStreamingWithTaskInThreadAndMessage tests task reference in streaming
func TestRunStreamingWithTaskInThreadAndMessage(t *testing.T) {
	transport := &mockA2ATransport{
		streamingResponseToReturn: &a2a.Message{
			ID:   "response-123",
			Role: a2a.MessageRoleAgent,
			Parts: []a2a.Part{
				a2a.TextPart{Text: "Response to task"},
			},
		},
	}
	a := newTestAgent(transport, a2aagent.Options{})

	thread := a.NewThread().(*a2aagent.Thread)
	thread.TaskID = "task-123"

	for _, err := range agent.RunTextStream(t.Context(), a, "Please make the background transparent", agent.WithThread(thread)) {
		if err != nil {
			t.Fatalf("RunStream() error = %v, want nil", err)
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

// TestRunStreamingWithAgentTaskUpdatesThread tests thread task ID update in streaming
func TestRunStreamingWithAgentTaskUpdatesThread(t *testing.T) {
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
	a := newTestAgent(transport, a2aagent.Options{})

	thread := a.NewThread()

	for _, err := range agent.RunTextStream(t.Context(), a, "Start a task", agent.WithThread(thread)) {
		if err != nil {
			t.Fatalf("RunStream() error = %v, want nil", err)
		}
	}

	a2aThread, ok := thread.(*a2aagent.Thread)
	if !ok {
		t.Fatalf("thread type = %T, want *a2aagent.Thread", thread)
	}
	if a2aThread.TaskID != "task-456" {
		t.Errorf("thread.TaskID = %q, want %q", a2aThread.TaskID, "task-456")
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
	a := newTestAgent(transport, a2aagent.Options{})

	var updates []*agent.RunResponseUpdate
	for update, err := range agent.RunTextStream(t.Context(), a, "Test message") {
		if err != nil {
			t.Fatalf("RunStream() error = %v, want nil", err)
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
	if update.AgentID != a.Identity().ID() {
		t.Errorf("update.AgentID = %q, want %q", update.AgentID, a.Identity().ID())
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
	a := newTestAgent(transport, a2aagent.Options{})

	thread := a.NewThread()

	var updates []*agent.RunResponseUpdate
	for update, err := range agent.RunTextStream(t.Context(), a, "Start long-running task", agent.WithThread(thread)) {
		if err != nil {
			t.Fatalf("RunStream() error = %v, want nil", err)
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
	if update.AgentID != a.Identity().ID() {
		t.Errorf("update.AgentID = %q, want %q", update.AgentID, a.Identity().ID())
	}
	if _, ok := update.RawRepresentation.(*a2a.Artifact); !ok {
		t.Errorf("update.RawRepresentation type = %T, want *a2a.Artifact", update.RawRepresentation)
	}

	a2aThread, ok := thread.(*a2aagent.Thread)
	if !ok {
		t.Fatalf("thread type = %T, want *a2aagent.Thread", thread)
	}
	if a2aThread.ContextID != contextID {
		t.Errorf("thread.ContextID = %q, want %q", a2aThread.ContextID, contextID)
	}
	if a2aThread.TaskID != taskID {
		t.Errorf("thread.TaskID = %q, want %q", a2aThread.TaskID, taskID)
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
	a := newTestAgent(transport, a2aagent.Options{})

	thread := a.NewThread()

	var updates []*agent.RunResponseUpdate
	for update, err := range agent.RunTextStream(t.Context(), a, "Check task status", agent.WithThread(thread)) {
		if err != nil {
			t.Fatalf("RunStream() error = %v, want nil", err)
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
	if update.AgentID != a.Identity().ID() {
		t.Errorf("update.AgentID = %q, want %q", update.AgentID, a.Identity().ID())
	}
	if _, ok := update.RawRepresentation.(*a2a.TaskStatusUpdateEvent); !ok {
		t.Errorf("update.RawRepresentation type = %T, want *a2a.TaskStatusUpdateEvent", update.RawRepresentation)
	}

	a2aThread, ok := thread.(*a2aagent.Thread)
	if !ok {
		t.Fatalf("thread type = %T, want *a2aagent.Thread", thread)
	}
	if a2aThread.ContextID != contextID {
		t.Errorf("thread.ContextID = %q, want %q", a2aThread.ContextID, contextID)
	}
	if a2aThread.TaskID != taskID {
		t.Errorf("thread.TaskID = %q, want %q", a2aThread.TaskID, taskID)
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
	a := newTestAgent(transport, a2aagent.Options{})

	thread := a.NewThread()

	var updates []*agent.RunResponseUpdate
	for update, err := range agent.RunTextStream(t.Context(), a, "Process artifact", agent.WithThread(thread)) {
		if err != nil {
			t.Fatalf("RunStream() error = %v, want nil", err)
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
	if update.AgentID != a.Identity().ID() {
		t.Errorf("update.AgentID = %q, want %q", update.AgentID, a.Identity().ID())
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

	a2aThread, ok := thread.(*a2aagent.Thread)
	if !ok {
		t.Fatalf("thread type = %T, want *a2aagent.Thread", thread)
	}
	if a2aThread.ContextID != contextID {
		t.Errorf("thread.ContextID = %q, want %q", a2aThread.ContextID, contextID)
	}
	if a2aThread.TaskID != taskID {
		t.Errorf("thread.TaskID = %q, want %q", a2aThread.TaskID, taskID)
	}
}

// TestRunWithAllowBackgroundResponsesAndNoThread tests error when no thread provided
func TestRunWithAllowBackgroundResponsesAndNoThread(t *testing.T) {
	transport := &mockA2ATransport{}
	a := newTestAgent(transport, a2aagent.Options{})

	// Call the agent's Run method directly (not the helper functions) to test the validation
	gotError := false
	for _, err := range a.Run(context.Background(), agent.WithMessage(message.NewText("Test message")), agent.WithAllowBackgroundResponses(true)) {
		if err != nil {
			gotError = true
			break
		}
	}
	if !gotError {
		t.Error("Run() error = nil, want error when AllowBackgroundResponses is true without thread")
	}
}

// TestRunStreamingWithAllowBackgroundResponsesAndNoThread tests error in streaming mode
func TestRunStreamingWithAllowBackgroundResponsesAndNoThread(t *testing.T) {
	transport := &mockA2ATransport{}
	a := newTestAgent(transport, a2aagent.Options{})

	// Call the agent's Run method directly with streaming enabled
	gotError := false
	for _, err := range a.Run(context.Background(), agent.WithMessage(message.NewText("Test message")), agent.WithAllowBackgroundResponses(true), agent.WithStreaming(true)) {
		if err != nil {
			gotError = true
			break
		}
	}

	if !gotError {
		t.Error("RunStream() expected error when AllowBackgroundResponses is true without thread, got nil")
	}
}

// customAgentThread is a custom agent thread for testing invalid thread type scenario
type customAgentThread struct{}

func (c *customAgentThread) ID() string {
	return "custom-thread-id"
}

func (c *customAgentThread) MessagesReceived(ctx context.Context, messages ...*message.Message) error {
	return nil
}

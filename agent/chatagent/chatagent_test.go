// Copyright (c) Microsoft. All rights reserved.

package chatagent

import (
	"context"
	"encoding/json"
	"errors"
	"iter"
	"testing"

	"github.com/microsoft/agent-framework-go/agent/agentopt"
	"github.com/microsoft/agent-framework-go/agent/memory"
	"github.com/microsoft/agent-framework-go/message"
)

// testContextProvider is a configurable ContextProvider for testing.
type testContextProvider struct {
	provideContext *memory.Context
	lastInvoked    *memory.InvokedContext
	invokingCalls  int
	invokedCalls   int
}

func (p *testContextProvider) Invoking(ctx *memory.InvokingContext) (*memory.Context, error) {
	p.invokingCalls++
	return p.provideContext, nil
}

func (p *testContextProvider) Invoked(ctx *memory.InvokedContext) error {
	p.invokedCalls++
	p.lastInvoked = ctx
	return nil
}

// errorContextProvider always returns an error from Invoking.
type errorContextProvider struct {
	err error
}

func (p *errorContextProvider) Invoking(ctx *memory.InvokingContext) (*memory.Context, error) {
	return nil, p.err
}

func (p *errorContextProvider) Invoked(ctx *memory.InvokedContext) error {
	return nil
}

func noopRunFunc(_ context.Context, _ []*message.Message, _ ...agentopt.RunOption) iter.Seq2[*message.ResponseUpdate, error] {
	return func(yield func(*message.ResponseUpdate, error) bool) {
		yield(&message.ResponseUpdate{
			Role:     message.RoleAssistant,
			Contents: []message.Content{&message.TextContent{Text: "response"}},
		}, nil)
	}
}

func failRunFunc(runErr error) RunFunc {
	return func(_ context.Context, _ []*message.Message, _ ...agentopt.RunOption) iter.Seq2[*message.ResponseUpdate, error] {
		return func(yield func(*message.ResponseUpdate, error) bool) {
			yield(nil, runErr)
		}
	}
}

// --- Session Tests ---

func TestSession_IsLightweight(t *testing.T) {
	session := &Session{
		ConversationID: "conv-123",
	}
	if session.ConversationID != "conv-123" {
		t.Errorf("expected ConversationID 'conv-123', got %q", session.ConversationID)
	}
	if session.GetStateBag() == nil {
		t.Error("expected non-nil StateBag")
	}
}

func TestSession_Serialization_Roundtrip(t *testing.T) {
	session := &Session{
		ConversationID: "conv-456",
	}
	session.State.Set("key1", "value1")

	data, err := json.Marshal(session)
	if err != nil {
		t.Fatalf("failed to marshal session: %v", err)
	}

	restored, err := newSessionFromJSON(data)
	if err != nil {
		t.Fatalf("failed to unmarshal session: %v", err)
	}

	if restored.ConversationID != "conv-456" {
		t.Errorf("expected ConversationID 'conv-456', got %q", restored.ConversationID)
	}
	if restored.GetStateBag() == nil {
		t.Error("expected non-nil StateBag after deserialization")
	}
}

func TestSession_Serialization_WithStateBag(t *testing.T) {
	session := &Session{
		ConversationID: "conv-789",
	}
	session.State.Set("greeting", "hello")

	data, err := json.Marshal(session)
	if err != nil {
		t.Fatalf("failed to marshal: %v", err)
	}

	// Verify JSON structure
	var m map[string]json.RawMessage
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("failed to parse JSON: %v", err)
	}
	if _, ok := m["State"]; !ok {
		t.Error("expected 'State' field in JSON")
	}
}

func TestSession_Serialization_Empty(t *testing.T) {
	session := &Session{}

	data, err := json.Marshal(session)
	if err != nil {
		t.Fatalf("failed to marshal: %v", err)
	}

	restored, err := newSessionFromJSON(data)
	if err != nil {
		t.Fatalf("failed to unmarshal: %v", err)
	}
	if restored.ConversationID != "" {
		t.Errorf("expected empty ConversationID, got %q", restored.ConversationID)
	}
}

// --- CreateSession Tests ---

func TestCreateSession_ReturnsSessionWithStateBag(t *testing.T) {
	a := NewAgent(noopRunFunc, Config{}, ProviderConfig{})
	ctx := t.Context()
	session, err := a.CreateSession(ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	chatSession, ok := session.(*Session)
	if !ok {
		t.Fatalf("expected *Session, got %T", session)
	}
	if chatSession.GetStateBag() == nil {
		t.Error("expected non-nil StateBag on new session")
	}
}

func TestCreateSession_WithConversationID(t *testing.T) {
	a := NewAgent(noopRunFunc, Config{}, ProviderConfig{})
	ctx := t.Context()
	session, err := a.CreateSession(ctx, ConversationID("conv-abc"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	chatSession := session.(*Session)
	if chatSession.ConversationID != "conv-abc" {
		t.Errorf("expected ConversationID 'conv-abc', got %q", chatSession.ConversationID)
	}
}

func TestCreateSession_DoesNotAttachProviders(t *testing.T) {
	provider := &testContextProvider{}
	a := NewAgent(noopRunFunc, Config{
		ContextProviders: []memory.ContextProvider{provider},
	}, ProviderConfig{})
	ctx := t.Context()
	session, err := a.CreateSession(ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Session should be lightweight - only ConversationID and StateBag
	chatSession := session.(*Session)
	if chatSession.ConversationID != "" {
		t.Error("expected empty ConversationID")
	}
}

// --- Context Provider Pipeline Tests ---

func TestRun_InvokesSingleContextProvider(t *testing.T) {
	provider := &testContextProvider{
		provideContext: &memory.Context{
			Messages: []*message.Message{
				{Role: message.RoleSystem, Contents: []message.Content{&message.TextContent{Text: "context message"}}},
			},
		},
	}

	var capturedMessages []*message.Message
	runFn := func(_ context.Context, msgs []*message.Message, _ ...agentopt.RunOption) iter.Seq2[*message.ResponseUpdate, error] {
		capturedMessages = msgs
		return func(yield func(*message.ResponseUpdate, error) bool) {
			yield(&message.ResponseUpdate{
				Role:     message.RoleAssistant,
				Contents: []message.Content{&message.TextContent{Text: "response"}},
			}, nil)
		}
	}

	a := NewAgent(runFn, Config{
		Instructions:     "base instructions",
		ContextProviders: []memory.ContextProvider{provider},
	}, ProviderConfig{})

	ctx := t.Context()
	session, _ := a.CreateSession(ctx)
	_, err := a.Run([]*message.Message{message.NewText("user input")}, agentopt.Session(session)).Collect(ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify provider was called
	if provider.invokingCalls != 1 {
		t.Errorf("expected 1 Invoking call, got %d", provider.invokingCalls)
	}
	if provider.invokedCalls != 1 {
		t.Errorf("expected 1 Invoked call, got %d", provider.invokedCalls)
	}

	// Verify context message was injected
	foundContext := false
	for _, msg := range capturedMessages {
		for _, c := range msg.Contents {
			if tc, ok := c.(*message.TextContent); ok && tc.Text == "context message" {
				foundContext = true
			}
		}
	}
	if !foundContext {
		t.Error("expected context message to be included in messages sent to run function")
	}
}

func TestRun_InvokesMultipleContextProvidersInSequence(t *testing.T) {
	provider1 := &testContextProvider{
		provideContext: &memory.Context{
			Messages: []*message.Message{
				{Role: message.RoleSystem, Contents: []message.Content{&message.TextContent{Text: "provider1 context"}}},
			},
		},
	}
	provider2 := &testContextProvider{
		provideContext: &memory.Context{
			Messages: []*message.Message{
				{Role: message.RoleSystem, Contents: []message.Content{&message.TextContent{Text: "provider2 context"}}},
			},
		},
	}

	var capturedMessages []*message.Message
	runFn := func(_ context.Context, msgs []*message.Message, _ ...agentopt.RunOption) iter.Seq2[*message.ResponseUpdate, error] {
		capturedMessages = msgs
		return func(yield func(*message.ResponseUpdate, error) bool) {
			yield(&message.ResponseUpdate{
				Role:     message.RoleAssistant,
				Contents: []message.Content{&message.TextContent{Text: "response"}},
			}, nil)
		}
	}

	a := NewAgent(runFn, Config{
		ContextProviders: []memory.ContextProvider{provider1, provider2},
	}, ProviderConfig{})

	ctx := t.Context()
	session, _ := a.CreateSession(ctx)
	_, err := a.Run([]*message.Message{message.NewText("user input")}, agentopt.Session(session)).Collect(ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Both providers should be invoked
	if provider1.invokingCalls != 1 {
		t.Errorf("expected 1 Invoking call for provider1, got %d", provider1.invokingCalls)
	}
	if provider2.invokingCalls != 1 {
		t.Errorf("expected 1 Invoking call for provider2, got %d", provider2.invokingCalls)
	}

	// Both providers should be notified of success
	if provider1.invokedCalls != 1 {
		t.Errorf("expected 1 Invoked call for provider1, got %d", provider1.invokedCalls)
	}
	if provider2.invokedCalls != 1 {
		t.Errorf("expected 1 Invoked call for provider2, got %d", provider2.invokedCalls)
	}

	// Verify both context messages are present
	found1, found2 := false, false
	for _, msg := range capturedMessages {
		for _, c := range msg.Contents {
			if tc, ok := c.(*message.TextContent); ok {
				if tc.Text == "provider1 context" {
					found1 = true
				}
				if tc.Text == "provider2 context" {
					found2 = true
				}
			}
		}
	}
	if !found1 {
		t.Error("expected provider1 context message in captured messages")
	}
	if !found2 {
		t.Error("expected provider2 context message in captured messages")
	}
}

func TestRun_ContextProviderReceivesSession(t *testing.T) {
	// Use a custom provider that captures the session
	customProvider := &sessionCapturingProvider{}

	a := NewAgent(noopRunFunc, Config{
		ContextProviders: []memory.ContextProvider{customProvider},
	}, ProviderConfig{})

	ctx := t.Context()
	session, _ := a.CreateSession(ctx)
	_, err := a.Run([]*message.Message{message.NewText("test")}, agentopt.Session(session)).Collect(ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if customProvider.lastSession != session {
		t.Error("expected context provider to receive the session")
	}
}

type sessionCapturingProvider struct {
	lastSession memory.Session
}

func (p *sessionCapturingProvider) Invoking(ctx *memory.InvokingContext) (*memory.Context, error) {
	p.lastSession = ctx.Session
	return nil, nil
}

func (p *sessionCapturingProvider) Invoked(ctx *memory.InvokedContext) error {
	return nil
}

// --- Context Provider Failure Notification Tests ---

func TestRun_NotifiesContextProvidersOnFailure(t *testing.T) {
	provider := &testContextProvider{}
	runErr := errors.New("run failed")

	a := NewAgent(failRunFunc(runErr), Config{
		ContextProviders: []memory.ContextProvider{provider},
	}, ProviderConfig{})

	ctx := t.Context()
	session, _ := a.CreateSession(ctx)
	_, err := a.Run([]*message.Message{message.NewText("test")}, agentopt.Session(session)).Collect(ctx)
	if err == nil {
		t.Fatal("expected error")
	}

	if provider.invokedCalls != 1 {
		t.Errorf("expected 1 Invoked call on failure, got %d", provider.invokedCalls)
	}
	if provider.lastInvoked == nil {
		t.Fatal("expected lastInvoked to be set")
	}
	if provider.lastInvoked.InvokeError != runErr {
		t.Errorf("expected InvokeError to be %v, got %v", runErr, provider.lastInvoked.InvokeError)
	}
}

func TestRun_NotifiesMultipleContextProvidersOnFailure(t *testing.T) {
	provider1 := &testContextProvider{}
	provider2 := &testContextProvider{}
	runErr := errors.New("run failed")

	a := NewAgent(failRunFunc(runErr), Config{
		ContextProviders: []memory.ContextProvider{provider1, provider2},
	}, ProviderConfig{})

	ctx := t.Context()
	session, _ := a.CreateSession(ctx)
	_, err := a.Run([]*message.Message{message.NewText("test")}, agentopt.Session(session)).Collect(ctx)
	if err == nil {
		t.Fatal("expected error")
	}

	if provider1.invokedCalls != 1 {
		t.Errorf("expected 1 Invoked call for provider1, got %d", provider1.invokedCalls)
	}
	if provider2.invokedCalls != 1 {
		t.Errorf("expected 1 Invoked call for provider2, got %d", provider2.invokedCalls)
	}
}

// --- Message History Provider Tests ---

func TestRun_UsesMessageHistoryProvider(t *testing.T) {
	historyProvider := &memory.InMemoryMessageHistoryProvider{
		Messages: []*message.Message{
			{Role: message.RoleUser, Contents: []message.Content{&message.TextContent{Text: "previous message"}}},
		},
	}

	var capturedMessages []*message.Message
	runFn := func(_ context.Context, msgs []*message.Message, _ ...agentopt.RunOption) iter.Seq2[*message.ResponseUpdate, error] {
		capturedMessages = msgs
		return func(yield func(*message.ResponseUpdate, error) bool) {
			yield(&message.ResponseUpdate{
				Role:     message.RoleAssistant,
				Contents: []message.Content{&message.TextContent{Text: "response"}},
			}, nil)
		}
	}

	a := NewAgent(runFn, Config{
		MessageHistoryProvider: historyProvider,
	}, ProviderConfig{})

	ctx := t.Context()
	session, _ := a.CreateSession(ctx)
	_, err := a.Run([]*message.Message{message.NewText("new message")}, agentopt.Session(session)).Collect(ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify history message was included
	foundHistory := false
	for _, msg := range capturedMessages {
		for _, c := range msg.Contents {
			if tc, ok := c.(*message.TextContent); ok && tc.Text == "previous message" {
				foundHistory = true
			}
		}
	}
	if !foundHistory {
		t.Error("expected history message to be included in messages sent to run function")
	}
}

func TestRun_DefaultsToInMemoryMessageHistoryProvider(t *testing.T) {
	a := NewAgent(noopRunFunc, Config{}, ProviderConfig{})

	ctx := t.Context()
	session, _ := a.CreateSession(ctx)
	_, err := a.Run([]*message.Message{message.NewText("test")}, agentopt.Session(session)).Collect(ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Should not fail — a default InMemoryMessageHistoryProvider should be created.
}

func TestRun_SkipsMessageHistoryProviderWithConversationID(t *testing.T) {
	historyProvider := &memory.InMemoryMessageHistoryProvider{
		Messages: []*message.Message{
			{Role: message.RoleUser, Contents: []message.Content{&message.TextContent{Text: "should not appear"}}},
		},
	}

	var capturedMessages []*message.Message
	runFn := func(_ context.Context, msgs []*message.Message, _ ...agentopt.RunOption) iter.Seq2[*message.ResponseUpdate, error] {
		capturedMessages = msgs
		return func(yield func(*message.ResponseUpdate, error) bool) {
			yield(&message.ResponseUpdate{
				Role:     message.RoleAssistant,
				Contents: []message.Content{&message.TextContent{Text: "response"}},
			}, nil)
		}
	}

	a := NewAgent(runFn, Config{
		MessageHistoryProvider: historyProvider,
	}, ProviderConfig{})

	ctx := t.Context()
	session, _ := a.CreateSession(ctx, ConversationID("server-managed"))
	_, err := a.Run([]*message.Message{message.NewText("test")}, agentopt.Session(session)).Collect(ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// History messages should not be included when ConversationID is set
	// (service manages history)
	for _, msg := range capturedMessages {
		for _, c := range msg.Contents {
			if tc, ok := c.(*message.TextContent); ok && tc.Text == "should not appear" {
				t.Error("history messages should not be included when ConversationID is set")
			}
		}
	}
}

// --- Stream Resumption Validation Tests ---

func TestValidateStreamResumption_RejectsWithoutConversationID(t *testing.T) {
	session := &Session{}
	err := validateStreamResumptionAllowed("some-token", session, nil)
	if err == nil {
		t.Fatal("expected error for streaming resumption without ConversationID")
	}
}

func TestValidateStreamResumption_RejectsWithContextProviders(t *testing.T) {
	session := &Session{
		ConversationID: "conv-123",
	}
	providers := []memory.ContextProvider{&testContextProvider{}}
	err := validateStreamResumptionAllowed("some-token", session, providers)
	if err == nil {
		t.Fatal("expected error for streaming resumption with context providers")
	}
}

func TestValidateStreamResumption_AllowsWithConversationIDAndNoProviders(t *testing.T) {
	session := &Session{
		ConversationID: "conv-123",
	}
	err := validateStreamResumptionAllowed("some-token", session, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestValidateStreamResumption_AllowsEmptyToken(t *testing.T) {
	session := &Session{}
	err := validateStreamResumptionAllowed("", session, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// --- Instructions Tests ---

func TestRun_IncludesInstructions(t *testing.T) {
	var capturedMessages []*message.Message
	runFn := func(_ context.Context, msgs []*message.Message, _ ...agentopt.RunOption) iter.Seq2[*message.ResponseUpdate, error] {
		capturedMessages = msgs
		return func(yield func(*message.ResponseUpdate, error) bool) {
			yield(&message.ResponseUpdate{
				Role:     message.RoleAssistant,
				Contents: []message.Content{&message.TextContent{Text: "response"}},
			}, nil)
		}
	}

	a := NewAgent(runFn, Config{
		Instructions: "You are a helpful assistant.",
	}, ProviderConfig{})

	ctx := t.Context()
	session, _ := a.CreateSession(ctx)
	_, err := a.Run([]*message.Message{message.NewText("hello")}, agentopt.Session(session)).Collect(ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(capturedMessages) == 0 {
		t.Fatal("expected at least 1 message")
	}

	// First message should be system instruction
	if capturedMessages[0].Role != message.RoleSystem {
		t.Errorf("expected first message to be system role, got %s", capturedMessages[0].Role)
	}
	tc, ok := capturedMessages[0].Contents[0].(*message.TextContent)
	if !ok || tc.Text != "You are a helpful assistant." {
		t.Error("expected instructions message as first message")
	}
}

func TestRun_ContextProviderAddsInstructions(t *testing.T) {
	provider := &testContextProvider{
		provideContext: &memory.Context{
			Instructions: "extra instructions from provider",
		},
	}

	var capturedMessages []*message.Message
	runFn := func(_ context.Context, msgs []*message.Message, _ ...agentopt.RunOption) iter.Seq2[*message.ResponseUpdate, error] {
		capturedMessages = msgs
		return func(yield func(*message.ResponseUpdate, error) bool) {
			yield(&message.ResponseUpdate{
				Role:     message.RoleAssistant,
				Contents: []message.Content{&message.TextContent{Text: "response"}},
			}, nil)
		}
	}

	a := NewAgent(runFn, Config{
		ContextProviders: []memory.ContextProvider{provider},
	}, ProviderConfig{})

	ctx := t.Context()
	session, _ := a.CreateSession(ctx)
	_, err := a.Run([]*message.Message{message.NewText("hello")}, agentopt.Session(session)).Collect(ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	foundInstruction := false
	for _, msg := range capturedMessages {
		if msg.Role == message.RoleSystem {
			for _, c := range msg.Contents {
				if tc, ok := c.(*message.TextContent); ok && tc.Text == "extra instructions from provider" {
					foundInstruction = true
				}
			}
		}
	}
	if !foundInstruction {
		t.Error("expected context provider instructions to be included as system message")
	}
}

// --- No Context Providers Tests ---

func TestRun_WorksWithoutContextProviders(t *testing.T) {
	var capturedMessages []*message.Message
	runFn := func(_ context.Context, msgs []*message.Message, _ ...agentopt.RunOption) iter.Seq2[*message.ResponseUpdate, error] {
		capturedMessages = msgs
		return func(yield func(*message.ResponseUpdate, error) bool) {
			yield(&message.ResponseUpdate{
				Role:     message.RoleAssistant,
				Contents: []message.Content{&message.TextContent{Text: "response"}},
			}, nil)
		}
	}

	a := NewAgent(runFn, Config{}, ProviderConfig{})

	ctx := t.Context()
	session, _ := a.CreateSession(ctx)
	_, err := a.Run([]*message.Message{message.NewText("hello")}, agentopt.Session(session)).Collect(ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Should contain just the user message
	if len(capturedMessages) != 1 {
		t.Fatalf("expected 1 message, got %d", len(capturedMessages))
	}
}

// --- Marshal/Unmarshal Tests ---

func TestMarshalUnmarshalSession(t *testing.T) {
	a := NewAgent(noopRunFunc, Config{}, ProviderConfig{})
	ctx := t.Context()

	session, err := a.CreateSession(ctx, ConversationID("conv-test"))
	if err != nil {
		t.Fatalf("failed to create session: %v", err)
	}

	data, err := a.MarshalSession(ctx, session)
	if err != nil {
		t.Fatalf("failed to marshal session: %v", err)
	}

	restored, err := a.UnmarshalSession(ctx, data)
	if err != nil {
		t.Fatalf("failed to unmarshal session: %v", err)
	}

	restoredSession := restored.(*Session)
	if restoredSession.ConversationID != "conv-test" {
		t.Errorf("expected ConversationID 'conv-test', got %q", restoredSession.ConversationID)
	}
}

func TestMarshalSession_RejectsIncompatibleSession(t *testing.T) {
	a := NewAgent(noopRunFunc, Config{}, ProviderConfig{})
	ctx := t.Context()

	incompatible := &incompatibleSession{}
	_, err := a.MarshalSession(ctx, incompatible)
	if err == nil {
		t.Fatal("expected error for incompatible session")
	}
}

type incompatibleSession struct{}

func (incompatibleSession) GetStateBag() *memory.StateBag { return nil }
